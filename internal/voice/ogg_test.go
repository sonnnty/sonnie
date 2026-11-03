package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
)

func TestOggOpusEncoder_RealFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "sample.ogg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	frames, err := OggOpusEncoder{}.Encode(context.Background(), tts.Audio{
		Format: tts.FormatOggOpus,
		Data:   data,
	})
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// sample.ogg is 0.5 s at 20 ms per frame (~25 frames, plus 1 frame for
	// libopus 312-sample pre-skip).
	if len(frames) < 24 || len(frames) > 27 {
		t.Fatalf("expected ~25 frames for 0.5s 20ms fixture, got %d", len(frames))
	}
	for i, f := range frames {
		if len(f) == 0 {
			t.Fatalf("frame %d is empty", i)
		}
		if bytes.HasPrefix(f, opusHeadMagic) || bytes.HasPrefix(f, opusTagsMagic) {
			t.Fatalf("frame %d still contains an Opus header: %q", i, f)
		}
		// RFC 6716 §3.1: bit 2 of the TOC byte (0x04) is the stereo flag.
		// Google TTS and sample.ogg are mono, so s == 0.
		if f[0]&0x04 != 0 {
			t.Fatalf("frame %d expected mono TOC byte, got 0x%02x", i, f[0])
		}
	}
}

func TestOggOpusEncoder_ReassemblesMultiSegmentAndCrossPagePackets(t *testing.T) {
	head := makeOpusHead(1, SampleRate)
	tags := makeOpusTags()

	// Packet A: 300 bytes (lacing 255 + 45 on a single page).
	pktA := bytes.Repeat([]byte{0x78}, 300)
	// Packet B: 510 bytes (exact multiple of 255: 255 on page 3, then 255 + 0
	// continued on page 4).
	pktB := bytes.Repeat([]byte{0x79}, 510)

	var stream []byte
	stream = append(stream, makeOggPage(0x02, 0, []byte{byte(len(head))}, head)...)
	stream = append(stream, makeOggPage(0x00, 1, []byte{byte(len(tags))}, tags)...)
	stream = append(stream, makeOggPage(0x00, 2, []byte{255, 45, 255}, append(append([]byte{}, pktA...), pktB[:255]...))...)
	stream = append(stream, makeOggPage(0x01|0x04, 3, []byte{255, 0}, pktB[255:])...)

	frames, err := OggOpusEncoder{}.Encode(context.Background(), tts.Audio{
		Format: tts.FormatOggOpus,
		Data:   stream,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("expected 2 reassembled frames, got %d", len(frames))
	}
	if !bytes.Equal(frames[0], pktA) {
		t.Fatalf("frame 0 length = %d, want %d", len(frames[0]), len(pktA))
	}
	if !bytes.Equal(frames[1], pktB) {
		t.Fatalf("frame 1 length = %d, want %d", len(frames[1]), len(pktB))
	}
}

func TestOggOpusEncoder_RejectsOtherFormatsAndMalformedInput(t *testing.T) {
	validHead := makeOpusHead(1, SampleRate)
	validTags := makeOpusTags()

	tests := []struct {
		name  string
		audio tts.Audio
	}{
		{
			name:  "wrong format mp3",
			audio: tts.Audio{Format: tts.FormatMP3, Data: []byte("ID3")},
		},
		{
			name:  "wrong format wav",
			audio: tts.Audio{Format: tts.FormatWAV, Data: []byte("RIFF")},
		},
		{
			name:  "empty data",
			audio: tts.Audio{Format: tts.FormatOggOpus, Data: nil},
		},
		{
			name:  "truncated page header",
			audio: tts.Audio{Format: tts.FormatOggOpus, Data: []byte("OggS\x00")},
		},
		{
			name:  "not Ogg capture pattern",
			audio: tts.Audio{Format: tts.FormatOggOpus, Data: make([]byte, 32)},
		},
		{
			name: "unexpected continuation flag on first page",
			audio: tts.Audio{
				Format: tts.FormatOggOpus,
				Data:   makeOggPage(0x01, 0, []byte{byte(len(validHead))}, validHead),
			},
		},
		{
			name: "missing continuation flag on second page",
			audio: tts.Audio{
				Format: tts.FormatOggOpus,
				Data: append(
					makeOggPage(0x02, 0, []byte{255}, bytes.Repeat([]byte{'a'}, 255)),
					makeOggPage(0x00, 1, []byte{10}, bytes.Repeat([]byte{'b'}, 10))...,
				),
			},
		},
		{
			name: "unfinished packet at EOF",
			audio: tts.Audio{
				Format: tts.FormatOggOpus,
				Data:   makeOggPage(0x02, 0, []byte{255}, bytes.Repeat([]byte{'a'}, 255)),
			},
		},
		{
			name: "missing OpusTags packet",
			audio: tts.Audio{
				Format: tts.FormatOggOpus,
				Data:   makeOggPage(0x02, 0, []byte{byte(len(validHead))}, validHead),
			},
		},
		{
			name: "non-Opus codec in Ogg",
			audio: tts.Audio{
				Format: tts.FormatOggOpus,
				Data: append(
					makeOggPage(0x02, 0, []byte{7}, []byte("\x01vorbis")),
					makeOggPage(0x00, 1, []byte{byte(len(validTags))}, validTags)...,
				),
			},
		},
		{
			name: "invalid channel count in OpusHead",
			audio: tts.Audio{
				Format: tts.FormatOggOpus,
				Data: append(
					makeOggPage(0x02, 0, []byte{byte(len(validHead))}, makeOpusHead(0, SampleRate)),
					makeOggPage(0x00, 1, []byte{byte(len(validTags))}, validTags)...,
				),
			},
		},
		{
			name: "invalid OpusTags magic",
			audio: tts.Audio{
				Format: tts.FormatOggOpus,
				Data: append(
					makeOggPage(0x02, 0, []byte{byte(len(validHead))}, validHead),
					makeOggPage(0x00, 1, []byte{16}, []byte("NotOpusTags12345"))...,
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := (OggOpusEncoder{}).Encode(context.Background(), tt.audio); err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

// google-th-24k.ogg is real Google TTS output (th-TH-Neural2-C, OGG_OPUS,
// sampleRateHertz 48000). Its OpusHead says 24000 Hz; the packets are still
// 20 ms Opus frames that Discord accepts.
func TestOggOpusEncoder_GoogleFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "google-th-24k.ogg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	frames, err := OggOpusEncoder{}.Encode(context.Background(), tts.Audio{
		Format: tts.FormatOggOpus,
		Data:   data,
	})
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(frames) != 113 {
		t.Fatalf("expected 113 frames, got %d", len(frames))
	}
	for i, f := range frames {
		// TOC config 0-11 SILK, 12-15 hybrid, 16-31 CELT; config%4 == 1
		// (SILK/CELT) or config%2 == 1 (hybrid) is 20 ms; code 0 is one frame.
		cfg := f[0] >> 3
		is20ms := (cfg < 12 && cfg%4 == 1) || (cfg >= 12 && cfg < 16 && cfg%2 == 1) || (cfg >= 16 && cfg%4 == 3)
		if !is20ms || f[0]&0x03 != 0 {
			t.Fatalf("frame %d: TOC 0x%02x is not a single 20 ms frame", i, f[0])
		}
	}
}

func makeOpusHead(channels byte, sampleRate uint32) []byte {
	b := make([]byte, 19)
	copy(b[0:8], "OpusHead")
	b[8] = 1 // version
	b[9] = channels
	binary.LittleEndian.PutUint16(b[10:12], 312) // pre-skip
	binary.LittleEndian.PutUint32(b[12:16], sampleRate)
	return b
}

func makeOpusTags() []byte {
	b := make([]byte, 16)
	copy(b[0:8], "OpusTags")
	return b
}

func makeOggPage(headerType byte, seq uint32, segTable, payload []byte) []byte {
	page := make([]byte, oggPageHeaderSize+len(segTable)+len(payload))
	copy(page[0:4], "OggS")
	page[4] = 0 // version
	page[5] = headerType
	binary.LittleEndian.PutUint32(page[18:22], seq)
	page[26] = byte(len(segTable))
	copy(page[oggPageHeaderSize:], segTable)
	copy(page[oggPageHeaderSize+len(segTable):], payload)
	return page
}

// OggOpusDuration (granule position) must agree with counting 20 ms packets.
// The two differ only by the pre-skip, which libopus sets to 312 samples
// (6.5 ms) for Google's streams, so allow one frame of slack.
func TestOggOpusDuration_MatchesPacketCount(t *testing.T) {
	for _, name := range []string{"sample.ogg", "google-th-24k.ogg"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			frames, err := OggOpusEncoder{}.Encode(context.Background(), tts.Audio{Format: tts.FormatOggOpus, Data: data})
			if err != nil {
				t.Fatal(err)
			}
			got, err := tts.OggOpusDuration(data)
			if err != nil {
				t.Fatal(err)
			}
			byPackets := time.Duration(len(frames)) * 20 * time.Millisecond
			if diff := byPackets - got; diff < 0 || diff > 20*time.Millisecond {
				t.Fatalf("granule duration %v vs %d packets × 20 ms = %v", got, len(frames), byPackets)
			}
			t.Logf("%s: %v by granule, %v by packets", name, got, byPackets)
		})
	}
}
