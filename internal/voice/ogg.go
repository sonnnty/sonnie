package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/Sonty15/SONNIE/internal/tts"
)

const (
	oggPageHeaderSize = 27
	oggFlagContinued  = 0x01
	opusHeadMinSize   = 19
	opusTagsMinSize   = 16
)

var (
	oggCapturePattern = []byte("OggS")
	opusHeadMagic     = []byte("OpusHead")
	opusTagsMagic     = []byte("OpusTags")
)

// OggOpusEncoder demuxes an Ogg/Opus bitstream (RFC 3533, RFC 7845) into raw
// Opus audio packets without transcoding or cgo.
//
// Google Cloud Text-to-Speech emits mono 48 kHz Ogg/Opus (20 ms frames).
// Discord negotiates opus/48000/2 per RFC 7587, and every Opus packet carries
// a self-describing TOC byte (RFC 6716 §3.1) whose stereo bit tells the
// listener's stereo libopus decoder whether the frame is mono or stereo; DAVE
// encrypts the opaque Opus payload unchanged, so mono packets play natively in
// Discord clients.
type OggOpusEncoder struct{}

// Encode parses Ogg pages from audio, validates and skips the OpusHead and
// OpusTags headers, and returns the remaining Opus audio packets.
func (OggOpusEncoder) Encode(ctx context.Context, audio tts.Audio) ([][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if audio.Format != tts.FormatOggOpus {
		return nil, fmt.Errorf("voice: ogg: unsupported audio format %q (want %q)", audio.Format, tts.FormatOggOpus)
	}
	if len(audio.Data) == 0 {
		return nil, errors.New("voice: ogg: empty input")
	}

	packets, err := demuxOggPackets(audio.Data)
	if err != nil {
		return nil, err
	}
	if len(packets) < 2 {
		return nil, errors.New("voice: ogg: missing Opus header packets")
	}
	if err := validateOpusHead(packets[0]); err != nil {
		return nil, err
	}
	if err := validateOpusTags(packets[1]); err != nil {
		return nil, err
	}

	frames := make([][]byte, 0, len(packets)-2)
	for _, pkt := range packets[2:] {
		if len(pkt) == 0 {
			return nil, errors.New("voice: ogg: empty Opus audio packet")
		}
		frames = append(frames, pkt)
	}
	return frames, nil
}

func demuxOggPackets(data []byte) ([][]byte, error) {
	var (
		packets   [][]byte
		curPacket []byte
		inPacket  bool
		offset    int
	)

	for offset < len(data) {
		if len(data)-offset < oggPageHeaderSize {
			return nil, errors.New("voice: ogg: truncated page header")
		}
		hdr := data[offset : offset+oggPageHeaderSize]
		if !bytes.Equal(hdr[0:4], oggCapturePattern) {
			return nil, errors.New("voice: ogg: missing OggS capture pattern")
		}
		if version := hdr[4]; version != 0 {
			return nil, fmt.Errorf("voice: ogg: unsupported page version %d", version)
		}

		continued := hdr[5]&oggFlagContinued != 0
		if inPacket && !continued {
			return nil, errors.New("voice: ogg: missing continuation flag for split packet")
		}
		if !inPacket && continued {
			return nil, errors.New("voice: ogg: unexpected continuation flag at start of packet")
		}

		numSegments := int(hdr[26])
		offset += oggPageHeaderSize
		if len(data)-offset < numSegments {
			return nil, errors.New("voice: ogg: truncated segment table")
		}
		segTable := data[offset : offset+numSegments]
		offset += numSegments

		for _, lace := range segTable {
			segLen := int(lace)
			if len(data)-offset < segLen {
				return nil, errors.New("voice: ogg: truncated segment data")
			}
			curPacket = append(curPacket, data[offset:offset+segLen]...)
			offset += segLen
			inPacket = true

			if lace < 255 {
				pkt := make([]byte, len(curPacket))
				copy(pkt, curPacket)
				packets = append(packets, pkt)
				curPacket = curPacket[:0]
				inPacket = false
			}
		}
	}

	if inPacket {
		return nil, errors.New("voice: ogg: unfinished packet at end of stream")
	}
	return packets, nil
}

func validateOpusHead(pkt []byte) error {
	if !bytes.HasPrefix(pkt, opusHeadMagic) {
		return errors.New("voice: ogg: first packet is not OpusHead")
	}
	if len(pkt) < opusHeadMinSize {
		return errors.New("voice: ogg: truncated OpusHead header")
	}
	if version := pkt[8]; version != 1 {
		return fmt.Errorf("voice: ogg: unsupported OpusHead version %d", version)
	}
	channels := int(pkt[9])
	if channels < 1 || channels > Channels {
		return fmt.Errorf("voice: ogg: unsupported channel count %d (want 1 or 2)", channels)
	}
	sampleRate := binary.LittleEndian.Uint32(pkt[12:16])
	if sampleRate != SampleRate {
		return fmt.Errorf("voice: ogg: unsupported sample rate %d Hz (want %d)", sampleRate, SampleRate)
	}
	if mappingFamily := pkt[18]; mappingFamily != 0 {
		return fmt.Errorf("voice: ogg: unsupported channel mapping family %d", mappingFamily)
	}
	return nil
}

func validateOpusTags(pkt []byte) error {
	if !bytes.HasPrefix(pkt, opusTagsMagic) {
		return errors.New("voice: ogg: second packet is not OpusTags")
	}
	if len(pkt) < opusTagsMinSize {
		return errors.New("voice: ogg: truncated OpusTags header")
	}
	return nil
}
