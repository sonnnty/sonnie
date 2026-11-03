package oggopus

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func head(preSkip uint16) []byte {
	b := make([]byte, 19)
	copy(b, "OpusHead")
	b[8] = 1
	b[9] = 1
	binary.LittleEndian.PutUint16(b[10:12], preSkip)
	binary.LittleEndian.PutUint32(b[12:16], 24000)
	return b
}

func tags() []byte {
	b := make([]byte, 16)
	copy(b, "OpusTags")
	return b
}

// page builds a page holding whole packets (each under 255 bytes).
func page(flags byte, granule int64, packets ...[]byte) []byte {
	var lacing, body []byte
	for _, p := range packets {
		lacing = append(lacing, byte(len(p)))
		body = append(body, p...)
	}
	b := make([]byte, pageHeaderSize, pageHeaderSize+len(lacing)+len(body))
	copy(b, "OggS")
	b[5] = flags
	binary.LittleEndian.PutUint64(b[6:14], uint64(granule))
	b[26] = byte(len(lacing))
	return append(append(b, lacing...), body...)
}

func frame(n byte) []byte { return []byte{0x0b, n} }

func stream(preSkip uint16, frames int) []byte {
	s := append(page(flagBOS, 0, head(preSkip)), page(0, 0, tags())...)
	for i := 1; i <= frames; i++ {
		s = append(s, page(0, int64(preSkip)+int64(i)*960, frame(byte(i)))...)
	}
	return s
}

func TestDemuxer_DurationOfPartialStream(t *testing.T) {
	var d Demuxer
	s := stream(312, 50) // 1 s of audio after pre-skip
	half := len(s) / 2
	if _, err := d.Write(s[:half]); err != nil {
		t.Fatal(err)
	}
	if got := d.Duration(); got <= 0 || got >= time.Second {
		t.Fatalf("partial duration %v, want between 0 and 1 s", got)
	}
	if got, want := d.Duration(), time.Duration(d.Packets())*20*time.Millisecond; got != want {
		t.Fatalf("partial duration %v, want %d packets × 20 ms = %v", got, d.Packets(), want)
	}
	if _, err := d.Write(s[half:]); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if got := d.Duration(); got != time.Second {
		t.Fatalf("duration %v, want 1s", got)
	}
}

func TestDemuxer_HeadersOnlyHaveNoDuration(t *testing.T) {
	var d Demuxer
	if _, err := d.Write(stream(312, 0)); err != nil {
		t.Fatal(err)
	}
	if d.Duration() != 0 || d.Packets() != 0 {
		t.Fatalf("got %v, %d packets", d.Duration(), d.Packets())
	}
}

func TestDemuxer_ChainedStreams(t *testing.T) {
	s := append(stream(312, 10), stream(0, 5)...)
	var d Demuxer
	got, err := d.Write(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 15 {
		t.Fatalf("got %d packets, want 15", len(got))
	}
	if want := 300 * time.Millisecond; d.Duration() != want {
		t.Fatalf("duration %v, want %v", d.Duration(), want)
	}
}

func TestDemuxer_ErrorIsSticky(t *testing.T) {
	var d Demuxer
	if _, err := d.Write([]byte("NotOgg")); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := d.Write(stream(0, 1)); err == nil {
		t.Fatal("expected the error to stick")
	}
	if d.Close() == nil {
		t.Fatal("expected Close to report the error")
	}
}

func TestDemuxer_RejectsGarbageEarly(t *testing.T) {
	var d Demuxer
	// Two bytes are enough to see this is not a page.
	if _, err := d.Write([]byte("Og")); err != nil {
		t.Fatalf("valid prefix rejected: %v", err)
	}
	if _, err := d.Write([]byte("x")); err == nil {
		t.Fatal("bad capture pattern accepted")
	}
}

func TestDemuxer_ReturnedPacketsAreNotAliased(t *testing.T) {
	var d Demuxer
	s := stream(0, 2)
	got, err := d.Write(s)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(got[0])
	for i := range s {
		s[i] = 0
	}
	if !bytes.Equal(got[0], want) {
		t.Fatal("packet changed when the input buffer was reused")
	}
}

// Granule duration agrees with the 113 × 20 ms frames of the Google fixture.
func TestDemuxer_GoogleFixtureDuration(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "voice", "testdata", "google-th-24k.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	var d Demuxer
	for i := range data {
		if _, err := d.Write(data[i : i+1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	byPackets := time.Duration(d.Packets()) * 20 * time.Millisecond
	if diff := byPackets - d.Duration(); diff < 0 || diff > 20*time.Millisecond {
		t.Fatalf("granule %v vs packets %v", d.Duration(), byPackets)
	}
}
