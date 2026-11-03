package tts

import (
	"encoding/binary"
	"testing"
	"time"
)

// oggPage builds one Ogg page with the given granule position; each payload
// is a single packet (< 255 bytes).
func oggPage(seq uint32, granule int64, packets ...[]byte) []byte {
	var seg, body []byte
	for _, p := range packets {
		seg = append(seg, byte(len(p)))
		body = append(body, p...)
	}
	page := make([]byte, oggHeaderSize+len(seg)+len(body))
	copy(page[0:4], "OggS")
	binary.LittleEndian.PutUint64(page[6:14], uint64(granule))
	binary.LittleEndian.PutUint32(page[18:22], seq)
	page[26] = byte(len(seg))
	copy(page[oggHeaderSize:], seg)
	copy(page[oggHeaderSize+len(seg):], body)
	return page
}

func opusHead(preSkip uint16) []byte {
	b := make([]byte, 19)
	copy(b, "OpusHead")
	b[8], b[9] = 1, 1
	binary.LittleEndian.PutUint16(b[10:12], preSkip)
	binary.LittleEndian.PutUint32(b[12:16], 48000)
	return b
}

// oggOpusStream returns a valid Ogg/Opus stream of audioSamples 48 kHz
// samples (after pre-skip), split over two audio pages. Shared by tests in
// this package only.
func oggOpusStream(preSkip uint16, audioSamples int64) []byte {
	var out []byte
	out = append(out, oggPage(0, 0, opusHead(preSkip))...)
	out = append(out, oggPage(1, 0, append([]byte("OpusTags"), make([]byte, 8)...))...)
	out = append(out, oggPage(2, audioSamples/2+int64(preSkip), []byte{0xfc, 1, 2})...)
	out = append(out, oggPage(3, audioSamples+int64(preSkip), []byte{0xfc, 3, 4})...)
	return out
}

func TestOggOpusDuration(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    time.Duration
		wantErr bool
	}{
		{"2.5 s with pre-skip", oggOpusStream(312, 120000), 2500 * time.Millisecond, false},
		{"no pre-skip", oggOpusStream(0, 48000), time.Second, false},
		{"granule below pre-skip clamps to 0", oggPage(0, 0, opusHead(312)), 0, false},
		{"empty", nil, 0, true},
		{"not ogg", []byte("RIFF....WAVEfmt "), 0, true},
		{"first page not OpusHead", oggPage(0, 100, []byte("hello world, not opus")), 0, true},
		{"truncated", oggOpusStream(312, 48000)[:60], 0, true},
		{"last granule -1", append(oggOpusStream(312, 48000), oggPage(4, -1, []byte{1})...), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := OggOpusDuration(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
