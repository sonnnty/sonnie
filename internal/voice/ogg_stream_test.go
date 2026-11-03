package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sonty15/SONNIE/internal/tts"
)

var fixtures = []string{"sample.ogg", "google-th-24k.ogg"}

// splitPages cuts an Ogg file at its page boundaries.
func splitPages(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var pages [][]byte
	for len(data) > 0 {
		if len(data) < oggPageHeaderSize {
			t.Fatal("fixture has a truncated page")
		}
		n := oggPageHeaderSize + int(data[26])
		for _, lace := range data[oggPageHeaderSize:n] {
			n += int(lace)
		}
		pages = append(pages, data[:n])
		data = data[n:]
	}
	return pages
}

func streamFrames(t *testing.T, chunks [][]byte) [][]byte {
	t.Helper()
	s, err := OggOpusEncoder{}.NewStream(tts.FormatOggOpus)
	if err != nil {
		t.Fatal(err)
	}
	var frames [][]byte
	for i, c := range chunks {
		got, err := s.Write(c)
		if err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		frames = append(frames, got...)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return frames
}

func equalFrames(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d frames, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("frame %d differs", i)
		}
	}
}

func TestOggStream_ByteByByteAndPageByPageMatchWholeFile(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			whole, err := OggOpusEncoder{}.Encode(context.Background(), tts.Audio{Format: tts.FormatOggOpus, Data: data})
			if err != nil {
				t.Fatal(err)
			}

			bytewise := make([][]byte, len(data))
			for i := range data {
				bytewise[i] = data[i : i+1]
			}
			equalFrames(t, streamFrames(t, bytewise), whole)
			equalFrames(t, streamFrames(t, splitPages(t, data)), whole)
			// Odd chunk sizes that straddle page boundaries.
			var odd [][]byte
			for rest := data; len(rest) > 0; {
				n := min(len(rest), 777)
				odd = append(odd, rest[:n])
				rest = rest[n:]
			}
			equalFrames(t, streamFrames(t, odd), whole)
		})
	}
}

// Frames come out as soon as their page is complete, not at end of stream.
func TestOggStream_EmitsEachPageAsSoonAsItIsComplete(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "google-th-24k.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	pages := splitPages(t, data)
	s, _ := OggOpusEncoder{}.NewStream(tts.FormatOggOpus)
	total := 0
	for i, p := range pages {
		// All but the last byte: nothing new may come out yet.
		got, err := s.Write(p[:len(p)-1])
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("page %d: %d frames before the page was complete", i, len(got))
		}
		got, err = s.Write(p[len(p)-1:])
		if err != nil {
			t.Fatal(err)
		}
		if i >= 2 && len(got) == 0 {
			t.Fatalf("audio page %d produced no frames when completed", i)
		}
		total += len(got)
	}
	if total != 113 {
		t.Fatalf("got %d frames, want 113", total)
	}
}

func TestOggStream_TruncatedStreamFailsOnClose(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "sample.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := OggOpusEncoder{}.NewStream(tts.FormatOggOpus)
	if _, err := s.Write(data[:len(data)-10]); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("Close accepted a truncated stream")
	}
}

func TestOggStream_RejectsOtherFormats(t *testing.T) {
	if _, err := (OggOpusEncoder{}).NewStream(tts.FormatMP3); err == nil {
		t.Fatal("expected an error for MP3")
	}
}

// The OpusHead input sample rate is informational: any value is accepted.
func TestOggStream_AcceptsAnyInputSampleRate(t *testing.T) {
	for _, rate := range []uint32{0, 8000, 16000, 24000, 44100, 48000, 96000} {
		head := makeOpusHead(1, rate)
		tags := makeOpusTags()
		stream := makeOggPage(0x02, 0, []byte{byte(len(head))}, head)
		stream = append(stream, makeOggPage(0, 1, []byte{byte(len(tags))}, tags)...)
		audio := makeOggPage(0, 2, []byte{3}, []byte{0x0b, 1, 2})
		binary.LittleEndian.PutUint64(audio[6:14], 960+312)
		stream = append(stream, audio...)
		frames := streamFrames(t, [][]byte{stream})
		if len(frames) != 1 {
			t.Fatalf("rate %d: got %d frames", rate, len(frames))
		}
	}
}
