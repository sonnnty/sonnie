package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// fakeTTSServer runs handle for every StreamingSynthesize call.
type fakeTTSServer struct {
	texttospeechpb.UnimplementedTextToSpeechServer
	handle func(texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error
}

func (s *fakeTTSServer) StreamingSynthesize(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
	return s.handle(stream)
}

// grpcOpener serves handle in-process and returns an opener connected to it.
func grpcOpener(t *testing.T, handle func(texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error) StreamOpener {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	texttospeechpb.RegisterTextToSpeechServer(srv, &fakeTTSServer{handle: handle})
	go srv.Serve(lis)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		srv.Stop()
	})
	client := texttospeechpb.NewTextToSpeechClient(conn)
	return func(ctx context.Context) (texttospeechpb.TextToSpeech_StreamingSynthesizeClient, error) {
		return client.StreamingSynthesize(ctx)
	}
}

// restServer counts unary calls and answers with audio.
func restServer(t *testing.T, audio []byte) (*Google, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"audioContent":"` + base64.StdEncoding.EncodeToString(audio) + `"}`))
	}))
	t.Cleanup(srv.Close)
	return &Google{
		Endpoint: srv.URL, Client: srv.Client(),
		Model: "gemini-2.5-flash-tts", Voice: "Kore", Prompt: "อ่านแบบสบาย ๆ",
	}, &calls
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	var buf syncBuffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(flags)
	})
	return &buf
}

// readRequests reads the config and input requests and the client's
// half-close.
func readRequests(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) (*texttospeechpb.StreamingSynthesizeConfig, *texttospeechpb.StreamingSynthesisInput, error) {
	first, err := stream.Recv()
	if err != nil {
		return nil, nil, err
	}
	second, err := stream.Recv()
	if err != nil {
		return nil, nil, err
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("client did not half-close after the input")
	}
	return first.GetStreamingConfig(), second.GetInput(), nil
}

func send(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer, chunk string) error {
	return stream.Send(&texttospeechpb.StreamingSynthesizeResponse{AudioContent: []byte(chunk)})
}

func TestGoogleStream_SendsGeminiRequestsAndStreamsChunks(t *testing.T) {
	logs := captureLog(t)
	release := make(chan struct{})
	var (
		gotCfg *texttospeechpb.StreamingSynthesizeConfig
		gotIn  *texttospeechpb.StreamingSynthesisInput
	)
	open := grpcOpener(t, func(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
		var err error
		if gotCfg, gotIn, err = readRequests(stream); err != nil {
			return err
		}
		if err := send(stream, "one"); err != nil {
			return err
		}
		<-release // the client must get "one" before the rest exists
		_ = send(stream, "")
		_ = send(stream, "two")
		return send(stream, "three")
	})
	rest, calls := restServer(t, []byte("unused"))
	g := &GoogleStream{REST: rest, Open: open}

	stream, err := g.SynthesizeStream(context.Background(), "สวัสดี", "th")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if stream.Format() != FormatOggOpus {
		t.Fatalf("format %q", stream.Format())
	}
	if chunk, err := stream.Recv(); err != nil || string(chunk) != "one" {
		t.Fatalf("first chunk %q, %v", chunk, err)
	}
	close(release)
	var rest2 []string
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		rest2 = append(rest2, string(chunk))
	}
	if strings.Join(rest2, ",") != "two,three" {
		t.Fatalf("chunks %v (empty responses must be skipped)", rest2)
	}
	if calls.Load() != 0 {
		t.Fatal("REST was called on a healthy stream")
	}

	v := gotCfg.GetVoice()
	ac := gotCfg.GetStreamingAudioConfig()
	if v.GetLanguageCode() != "th-TH" || v.GetName() != "Kore" || v.GetModelName() != "gemini-2.5-flash-tts" {
		t.Fatalf("voice %v", v)
	}
	if ac.GetAudioEncoding() != texttospeechpb.AudioEncoding_OGG_OPUS || ac.GetSampleRateHertz() != 48000 {
		t.Fatalf("audio config %v", ac)
	}
	if gotIn.GetText() != "สวัสดี" || gotIn.GetPrompt() != "อ่านแบบสบาย ๆ" {
		t.Fatalf("input %v", gotIn)
	}
	if !strings.Contains(logs.String(), "tts: timing path=stream first_audio=") || !strings.Contains(logs.String(), "bytes=11") {
		t.Fatalf("missing timing line: %q", logs.String())
	}
}

func TestGoogleStream_ChirpGetsNoPromptOrModel(t *testing.T) {
	g := &GoogleStream{REST: &Google{Voice: "th-TH-Chirp3-HD-Achird", Prompt: "ignored"}}
	cfg, in := g.streamRequests("hi", "th")
	if cfg.GetStreamingConfig().GetVoice().GetModelName() != "" || in.GetInput().Prompt != nil {
		t.Fatalf("Gemini fields on a Chirp request: %v %v", cfg, in)
	}
}

func TestGoogleStream_FallsBackToRESTWhenStreamFailsBeforeAudio(t *testing.T) {
	cases := map[string]func(texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error{
		"error status": func(texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
			return status.Error(codes.InvalidArgument, "OGG_OPUS is not supported")
		},
		"no audio": func(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
			if _, _, err := readRequests(stream); err != nil {
				return err
			}
			return send(stream, "")
		},
	}
	for name, handle := range cases {
		t.Run(name, func(t *testing.T) {
			logs := captureLog(t)
			rest, calls := restServer(t, []byte("OggS-whole"))
			g := &GoogleStream{REST: rest, Open: grpcOpener(t, handle)}
			audio, err := g.Synthesize(context.Background(), "สวัสดี", "th")
			if err != nil {
				t.Fatal(err)
			}
			if string(audio.Data) != "OggS-whole" || audio.Format != FormatOggOpus {
				t.Fatalf("audio %+v", audio)
			}
			if calls.Load() != 1 {
				t.Fatalf("REST called %d times, want 1", calls.Load())
			}
			out := logs.String()
			if !strings.Contains(out, "retrying once with REST") || !strings.Contains(out, "path=rest-fallback") {
				t.Fatalf("log %q", out)
			}
			if strings.Contains(out, "path=rest ") {
				t.Fatalf("fallback logged a second timing line: %q", out)
			}
		})
	}
}

func TestGoogleStream_FallbackFailureReportsBoth(t *testing.T) {
	captureLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"quota","status":"RESOURCE_EXHAUSTED"}}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()
	g := &GoogleStream{
		REST: &Google{Endpoint: srv.URL, Client: srv.Client(), Model: "gemini-2.5-flash-tts"},
		Open: grpcOpener(t, func(texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
			return status.Error(codes.Unavailable, "down")
		}),
	}
	_, err := g.SynthesizeStream(context.Background(), "x", "th")
	if err == nil || !strings.Contains(err.Error(), "quota") || !strings.Contains(err.Error(), "down") {
		t.Fatalf("error %v", err)
	}
}

func TestGoogleStream_ErrorAfterAudioIsNotRetried(t *testing.T) {
	captureLog(t)
	rest, calls := restServer(t, []byte("unused"))
	g := &GoogleStream{REST: rest, Open: grpcOpener(t, func(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
		if _, _, err := readRequests(stream); err != nil {
			return err
		}
		if err := send(stream, "partial"); err != nil {
			return err
		}
		return status.Error(codes.Internal, "boom")
	})}
	stream, err := g.SynthesizeStream(context.Background(), "x", "th")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if chunk, err := stream.Recv(); err != nil || string(chunk) != "partial" {
		t.Fatalf("chunk %q, %v", chunk, err)
	}
	if _, err := stream.Recv(); err == nil || status.Code(errors.Unwrap(err)) != codes.Internal {
		t.Fatalf("want the Internal status, got %v", err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("error must be sticky")
	}
	if calls.Load() != 0 {
		t.Fatal("REST was called after audio had arrived")
	}
}

func TestGoogleStream_CancelledBeforeAudioDoesNotFallBack(t *testing.T) {
	captureLog(t)
	rest, calls := restServer(t, []byte("unused"))
	g := &GoogleStream{REST: rest, Open: grpcOpener(t, func(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
		<-stream.Context().Done()
		return stream.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, err := g.SynthesizeStream(ctx, "x", "th"); err == nil {
		t.Fatal("want an error")
	}
	if calls.Load() != 0 {
		t.Fatal("REST was called for a cancelled request")
	}
}

func TestGoogleStream_CloseCancelsTheCall(t *testing.T) {
	captureLog(t)
	cancelled := make(chan struct{})
	g := &GoogleStream{REST: &Google{}, Open: grpcOpener(t, func(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
		if _, _, err := readRequests(stream); err != nil {
			return err
		}
		if err := send(stream, "one"); err != nil {
			return err
		}
		<-stream.Context().Done()
		close(cancelled)
		return nil
	})}
	stream, err := g.SynthesizeStream(context.Background(), "x", "th")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		blocked <- err
	}()
	stream.Close()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("server never saw the cancellation")
	}
	select {
	case err := <-blocked:
		if err == nil {
			t.Fatal("blocked Recv returned no error after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock Recv")
	}
}

func TestGoogle_SynthesizeLogsRESTTiming(t *testing.T) {
	logs := captureLog(t)
	rest, _ := restServer(t, []byte("OggS"))
	if _, err := rest.Synthesize(context.Background(), "x", "th"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "tts: timing path=rest first_audio=") {
		t.Fatalf("log %q", logs.String())
	}
}

func TestOpenStream_WrapsUnarySynthesizer(t *testing.T) {
	stream, err := OpenStream(context.Background(), &Fake{}, "hello", "en")
	if err != nil {
		t.Fatal(err)
	}
	audio, err := ReadAll(stream)
	if err != nil || string(audio.Data) != "hello" || audio.Format != FormatRaw {
		t.Fatalf("%+v, %v", audio, err)
	}
	if _, err := OpenStream(context.Background(), &Fake{Err: errors.New("x")}, "a", "b"); err == nil {
		t.Fatal("want the synthesizer's error")
	}
}

func TestGoogleStream_UsesPerMessageVoiceOnStreamAndRESTFallback(t *testing.T) {
	t.Run("stream uses context voice", func(t *testing.T) {
		captureLog(t)
		var gotVoice string
		open := grpcOpener(t, func(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
			cfg, _, err := readRequests(stream)
			if err != nil {
				return err
			}
			gotVoice = cfg.GetVoice().GetName()
			return send(stream, "audio")
		})
		rest, _ := restServer(t, []byte("unused"))
		g := &GoogleStream{REST: rest, Open: open}
		if _, err := g.Synthesize(WithVoice(context.Background(), "Orus"), "สวัสดี", "th"); err != nil {
			t.Fatal(err)
		}
		if gotVoice != "Orus" {
			t.Fatalf("stream got voice %q, want Orus", gotVoice)
		}
	})

	t.Run("rest fallback uses context voice", func(t *testing.T) {
		captureLog(t)
		srv, body := captureBody(t)
		g := &GoogleStream{
			REST: &Google{
				Endpoint: srv.URL, Client: srv.Client(),
				Model: "gemini-2.5-flash-tts", Voice: "Charon",
			},
			Open: grpcOpener(t, func(texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
				return status.Error(codes.Unavailable, "down")
			}),
		}
		if _, err := g.Synthesize(WithVoice(context.Background(), "Fenrir"), "สวัสดี", "th"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(*body, `"name":"Fenrir"`) {
			t.Fatalf("REST fallback body %s missing Fenrir", *body)
		}
	})
}
