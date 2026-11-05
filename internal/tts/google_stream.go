package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
)

// StreamOpener starts one bidirectional StreamingSynthesize call. In
// production it is (*texttospeech.Client).StreamingSynthesize without
// options; tests point it at an in-process gRPC server.
type StreamOpener func(ctx context.Context) (texttospeechpb.TextToSpeech_StreamingSynthesizeClient, error)

// GoogleStream synthesizes with the Cloud Text-to-Speech gRPC
// StreamingSynthesize call, requesting 48 kHz Ogg/Opus, and hands audio on as
// each response arrives instead of waiting for the whole file. It is meant
// for Gemini-TTS models, which take seconds to produce a sentence.
//
// If the stream fails before any audio arrives it retries once with the
// unary REST call of REST and logs that.
type GoogleStream struct {
	// REST supplies the voice settings (Voice, Model, Prompt) and is the
	// fallback. It must be set.
	REST *Google
	// Open starts a call. It must be set.
	Open StreamOpener
}

var (
	_ Synthesizer       = (*GoogleStream)(nil)
	_ StreamSynthesizer = (*GoogleStream)(nil)
)

// Synthesize streams the audio and returns it whole.
func (g *GoogleStream) Synthesize(ctx context.Context, text, lang string) (Audio, error) {
	stream, err := g.SynthesizeStream(ctx, text, lang)
	if err != nil {
		return Audio{}, err
	}
	return ReadAll(stream)
}

// SynthesizeStream opens the stream and waits for the first audio chunk, so
// that a failure before any audio can still fall back to REST. The returned
// stream yields FormatOggOpus chunks.
func (g *GoogleStream) SynthesizeStream(ctx context.Context, text, lang string) (AudioStream, error) {
	start := time.Now()
	sctx, cancel := context.WithCancel(ctx)
	call, first, err := g.start(sctx, text, lang)
	if err == nil {
		return &googleAudioStream{
			call:    call,
			cancel:  cancel,
			pending: first,
			start:   start,
			firstAt: time.Since(start),
		}, nil
	}
	cancel()
	if ctx.Err() != nil {
		// Cancelled by the caller (player closed, bot left): no retry.
		return nil, err
	}

	log.Printf("tts: google: stream failed before any audio, retrying once with REST: %v", err)
	audio, rerr := g.REST.synthesize(ctx, text, lang)
	if rerr != nil {
		return nil, fmt.Errorf("%w (after stream failure: %v)", rerr, err)
	}
	elapsed := time.Since(start)
	logTiming("rest-fallback", elapsed, elapsed, len(audio.Data))
	return SingleChunk(audio), nil
}

// start sends the config and the text, then reads until the first non-empty
// audio chunk.
func (g *GoogleStream) start(ctx context.Context, text, lang string) (texttospeechpb.TextToSpeech_StreamingSynthesizeClient, []byte, error) {
	if g.REST == nil || g.Open == nil {
		return nil, nil, errors.New("tts: google: stream: not configured")
	}
	call, err := g.Open(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("tts: google: stream: open: %w", err)
	}
	cfg, input := g.streamRequestsWithVoice(text, lang, VoiceFromContext(ctx))
	if err := call.Send(cfg); err != nil {
		return nil, nil, fmt.Errorf("tts: google: stream: send config: %w", recvErr(call, err))
	}
	if err := call.Send(input); err != nil {
		return nil, nil, fmt.Errorf("tts: google: stream: send input: %w", recvErr(call, err))
	}
	// One input request per utterance: tell the server no more text is
	// coming so it finishes the stream after the last audio.
	if err := call.CloseSend(); err != nil {
		return nil, nil, fmt.Errorf("tts: google: stream: close send: %w", err)
	}
	for {
		resp, err := call.Recv()
		if errors.Is(err, io.EOF) {
			return nil, nil, errors.New("tts: google: stream: ended without audio")
		}
		if err != nil {
			return nil, nil, fmt.Errorf("tts: google: stream: %w", err)
		}
		if chunk := resp.GetAudioContent(); len(chunk) > 0 {
			return call, chunk, nil
		}
	}
}

// recvErr turns the io.EOF a gRPC Send returns when the server has already
// ended the call into the call's real status.
func recvErr(call texttospeechpb.TextToSpeech_StreamingSynthesizeClient, err error) error {
	if !errors.Is(err, io.EOF) {
		return err
	}
	if _, rerr := call.Recv(); rerr != nil && !errors.Is(rerr, io.EOF) {
		return rerr
	}
	return err
}

// streamRequests builds the two requests of a call from the same voice
// settings as the REST request: first the config, then the text (and, for
// Gemini, the style prompt).
func (g *GoogleStream) streamRequests(text, lang string) (cfg, input *texttospeechpb.StreamingSynthesizeRequest) {
	return g.streamRequestsWithVoice(text, lang, "")
}

func (g *GoogleStream) streamRequestsWithVoice(text, lang, voice string) (cfg, input *texttospeechpb.StreamingSynthesizeRequest) {
	r := g.REST.buildRequestWithVoice(text, lang, voice)
	cfg = &texttospeechpb.StreamingSynthesizeRequest{
		StreamingRequest: &texttospeechpb.StreamingSynthesizeRequest_StreamingConfig{
			StreamingConfig: &texttospeechpb.StreamingSynthesizeConfig{
				Voice: &texttospeechpb.VoiceSelectionParams{
					LanguageCode: r.Voice.LanguageCode,
					Name:         r.Voice.Name,
					ModelName:    r.Voice.ModelName,
				},
				StreamingAudioConfig: &texttospeechpb.StreamingAudioConfig{
					AudioEncoding:   texttospeechpb.AudioEncoding_OGG_OPUS,
					SampleRateHertz: googleSampleRateHertz,
				},
			},
		},
	}
	in := &texttospeechpb.StreamingSynthesisInput{
		InputSource: &texttospeechpb.StreamingSynthesisInput_Text{Text: r.Input.Text},
	}
	if r.Input.Prompt != "" {
		prompt := r.Input.Prompt
		in.Prompt = &prompt
	}
	input = &texttospeechpb.StreamingSynthesizeRequest{
		StreamingRequest: &texttospeechpb.StreamingSynthesizeRequest_Input{Input: in},
	}
	return cfg, input
}

type googleAudioStream struct {
	call    texttospeechpb.TextToSpeech_StreamingSynthesizeClient
	cancel  context.CancelFunc
	start   time.Time
	firstAt time.Duration

	mu      sync.Mutex
	pending []byte
	bytes   int
	done    error // sticky result once the call has ended
}

func (s *googleAudioStream) Format() Format { return FormatOggOpus }

func (s *googleAudioStream) Recv() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != nil {
		return nil, s.done
	}
	if s.pending != nil {
		chunk := s.pending
		s.pending = nil
		s.bytes += len(chunk)
		return chunk, nil
	}
	for {
		resp, err := s.call.Recv()
		if errors.Is(err, io.EOF) {
			s.done = io.EOF
			s.cancel()
			logTiming("stream", s.firstAt, time.Since(s.start), s.bytes)
			return nil, io.EOF
		}
		if err != nil {
			s.done = fmt.Errorf("tts: google: stream: %w", err)
			s.cancel()
			return nil, s.done
		}
		if chunk := resp.GetAudioContent(); len(chunk) > 0 {
			s.bytes += len(chunk)
			return chunk, nil
		}
	}
}

// Close cancels the call. It does not take mu, so it can interrupt a Recv
// that is blocked waiting for the server.
func (s *googleAudioStream) Close() error {
	s.cancel()
	return nil
}
