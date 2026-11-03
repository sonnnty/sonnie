package tts

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"
)

const (
	oggHeaderSize  = 27
	opusClockHertz = 48000 // granule positions in Ogg Opus always count 48 kHz samples (RFC 7845 §4)
)

// OggOpusDuration returns the playing time of an Ogg/Opus stream: the granule
// position of the last page minus the pre-skip in OpusHead, at 48 kHz
// (RFC 7845 §4.1, §4.5). It only walks the page headers, so it is cheap.
//
// This is preferred over counting packets × 20 ms because it stays right if
// the encoder ever emits frames of another size. Gemini-TTS currently emits
// single 20 ms frames, for which both methods agree (see the cross-check
// test in package voice).
func OggOpusDuration(data []byte) (time.Duration, error) {
	var (
		offset   int
		preSkip  int64
		granule  int64
		havePage bool
	)
	for offset < len(data) {
		if len(data)-offset < oggHeaderSize || !bytes.Equal(data[offset:offset+4], []byte("OggS")) {
			return 0, errors.New("tts: ogg: malformed page header")
		}
		hdr := data[offset : offset+oggHeaderSize]
		numSegments := int(hdr[26])
		if len(data)-offset < oggHeaderSize+numSegments {
			return 0, errors.New("tts: ogg: truncated segment table")
		}
		bodyLen := 0
		for _, lace := range data[offset+oggHeaderSize : offset+oggHeaderSize+numSegments] {
			bodyLen += int(lace)
		}
		pageLen := oggHeaderSize + numSegments + bodyLen
		if len(data)-offset < pageLen {
			return 0, errors.New("tts: ogg: truncated page body")
		}

		if !havePage {
			body := data[offset+oggHeaderSize+numSegments : offset+pageLen]
			// OpusHead: magic(8) version(1) channels(1) pre-skip(2, LE).
			if len(body) < 12 || !bytes.Equal(body[:8], []byte("OpusHead")) {
				return 0, errors.New("tts: ogg: first page is not OpusHead")
			}
			preSkip = int64(binary.LittleEndian.Uint16(body[10:12]))
			havePage = true
		}
		granule = int64(binary.LittleEndian.Uint64(hdr[6:14]))
		offset += pageLen
	}
	if !havePage {
		return 0, errors.New("tts: ogg: empty stream")
	}
	if granule < 0 {
		// -1 means "no packet finishes on this page"; a finished stream
		// never ends like that.
		return 0, errors.New("tts: ogg: last page has no granule position")
	}
	samples := granule - preSkip
	if samples < 0 {
		samples = 0
	}
	// Split to keep the multiplication from overflowing on absurd granules.
	whole, rest := samples/opusClockHertz, samples%opusClockHertz
	return time.Duration(whole)*time.Second + time.Duration(rest)*time.Second/opusClockHertz, nil
}
