// Package oggopus demuxes Ogg/Opus bitstreams (RFC 3533, RFC 7845) into raw
// Opus audio packets, in pure Go and incrementally: bytes may arrive in
// chunks of any size, as they do from a streaming synthesis call, and each
// packet is returned as soon as the page that finishes it is complete.
package oggopus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	pageHeaderSize = 27
	flagContinued  = 0x01
	flagBOS        = 0x02
	headMinSize    = 19
	tagsMinSize    = 16

	// ClockHertz is the rate granule positions count in: Ogg Opus always
	// uses 48 kHz samples regardless of the input rate (RFC 7845 §4).
	ClockHertz = 48000
	// MaxChannels is the most channels accepted (mapping family 0).
	MaxChannels = 2
)

var (
	capturePattern = []byte("OggS")
	headMagic      = []byte("OpusHead")
	tagsMagic      = []byte("OpusTags")
)

// header states of the current logical stream.
const (
	wantHead = iota
	wantTags
	inAudio
)

// Demuxer reassembles Opus audio packets from an Ogg/Opus bitstream fed with
// Write. It validates and drops the OpusHead and OpusTags header packets and
// accepts chained streams (a new OpusHead on a beginning-of-stream page after
// audio), which is how concatenated Ogg/Opus files look. The zero value is
// ready to use. A Demuxer is not safe for concurrent use.
type Demuxer struct {
	buf      []byte // bytes of the page not yet complete
	packet   []byte // packet continued across pages
	inPacket bool
	state    int
	pages    int
	packets  int

	preSkip     int64
	granule     int64 // last granule position of the current link
	doneSamples int64 // samples of finished links in a chain

	err error
}

// Write consumes the next chunk of the bitstream and returns the audio
// packets completed by it, possibly none. The returned slices are owned by
// the caller. After an error every later call returns the same error.
//
// At most one incomplete page (under 64 KiB) is buffered between calls.
func (d *Demuxer) Write(p []byte) ([][]byte, error) {
	if d.err != nil {
		return nil, d.err
	}
	d.buf = append(d.buf, p...)

	var out [][]byte
	consumed := 0
	for {
		page := d.buf[consumed:]
		n, err := pageLen(page)
		if err != nil {
			d.err = err
			return out, err
		}
		if n == 0 {
			break // incomplete page: wait for more bytes
		}
		if out, err = d.page(page[:n], out); err != nil {
			d.err = err
			return out, err
		}
		consumed += n
	}
	// Keep only the incomplete tail, copied down so the buffer does not
	// grow with the whole stream.
	d.buf = append(d.buf[:0], d.buf[consumed:]...)
	return out, nil
}

// Close reports whether the bitstream ended cleanly: no partial page or
// packet is left and both Opus headers were seen.
func (d *Demuxer) Close() error {
	switch {
	case d.err != nil:
		return d.err
	case len(d.buf) > 0:
		return errors.New("oggopus: truncated page at end of stream")
	case d.inPacket:
		return errors.New("oggopus: unfinished packet at end of stream")
	case d.pages == 0:
		return errors.New("oggopus: empty stream")
	case d.state != inAudio:
		return errors.New("oggopus: missing Opus header packets")
	}
	return nil
}

// Packets returns how many audio packets have been returned so far.
func (d *Demuxer) Packets() int { return d.packets }

// Duration returns the playing time of the complete pages received so far:
// the granule position of the last page minus the pre-skip, summed over the
// links of a chained stream (RFC 7845 §4.1, §4.5).
func (d *Demuxer) Duration() time.Duration {
	samples := d.doneSamples + d.linkSamples()
	whole, rest := samples/ClockHertz, samples%ClockHertz
	return time.Duration(whole)*time.Second + time.Duration(rest)*time.Second/ClockHertz
}

func (d *Demuxer) linkSamples() int64 {
	if d.state != inAudio || d.granule <= d.preSkip {
		return 0
	}
	return d.granule - d.preSkip
}

// pageLen returns the length of the page at the start of b, 0 if b does not
// hold a whole page yet, or an error if b cannot start a page.
func pageLen(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if !bytes.HasPrefix(b, capturePattern[:min(len(b), len(capturePattern))]) {
		return 0, errors.New("oggopus: missing OggS capture pattern")
	}
	if len(b) > 4 && b[4] != 0 {
		return 0, fmt.Errorf("oggopus: unsupported page version %d", b[4])
	}
	if len(b) < pageHeaderSize {
		return 0, nil
	}
	numSegments := int(b[26])
	if len(b) < pageHeaderSize+numSegments {
		return 0, nil
	}
	n := pageHeaderSize + numSegments
	for _, lace := range b[pageHeaderSize : pageHeaderSize+numSegments] {
		n += int(lace)
	}
	if len(b) < n {
		return 0, nil
	}
	return n, nil
}

// page processes one complete page, appending finished audio packets to out.
func (d *Demuxer) page(page []byte, out [][]byte) ([][]byte, error) {
	flags := page[5]
	continued := flags&flagContinued != 0
	if d.inPacket && !continued {
		return out, errors.New("oggopus: missing continuation flag for split packet")
	}
	if !d.inPacket && continued {
		return out, errors.New("oggopus: unexpected continuation flag at start of packet")
	}
	if flags&flagBOS != 0 && d.state == inAudio {
		// The next link of a chain: bank the finished link's samples and
		// expect a fresh pair of headers.
		d.doneSamples += d.linkSamples()
		d.state, d.preSkip, d.granule = wantHead, 0, 0
	}
	d.pages++

	numSegments := int(page[26])
	body := page[pageHeaderSize+numSegments:]
	for _, lace := range page[pageHeaderSize : pageHeaderSize+numSegments] {
		d.packet = append(d.packet, body[:lace]...)
		body = body[lace:]
		d.inPacket = true
		if lace == 255 {
			continue
		}
		pkt := bytes.Clone(d.packet)
		d.packet = d.packet[:0]
		d.inPacket = false
		var err error
		if out, err = d.handlePacket(pkt, out); err != nil {
			return out, err
		}
	}

	// -1 means no packet finishes on this page.
	if g := int64(binary.LittleEndian.Uint64(page[6:14])); g >= 0 && d.state == inAudio {
		d.granule = g
	}
	return out, nil
}

func (d *Demuxer) handlePacket(pkt []byte, out [][]byte) ([][]byte, error) {
	switch d.state {
	case wantHead:
		preSkip, err := parseHead(pkt)
		if err != nil {
			return out, err
		}
		d.preSkip = int64(preSkip)
		d.state = wantTags
	case wantTags:
		if err := validateTags(pkt); err != nil {
			return out, err
		}
		d.state = inAudio
	default:
		if len(pkt) == 0 {
			return out, errors.New("oggopus: empty Opus audio packet")
		}
		d.packets++
		out = append(out, pkt)
	}
	return out, nil
}

// parseHead validates an OpusHead packet and returns its pre-skip.
func parseHead(pkt []byte) (uint16, error) {
	if !bytes.HasPrefix(pkt, headMagic) {
		return 0, errors.New("oggopus: first packet is not OpusHead")
	}
	if len(pkt) < headMinSize {
		return 0, errors.New("oggopus: truncated OpusHead header")
	}
	if version := pkt[8]; version != 1 {
		return 0, fmt.Errorf("oggopus: unsupported OpusHead version %d", version)
	}
	channels := int(pkt[9])
	if channels < 1 || channels > MaxChannels {
		return 0, fmt.Errorf("oggopus: unsupported channel count %d (want 1 or 2)", channels)
	}
	// Bytes 12-15 hold the input sample rate, which RFC 7845 §5.1 says is
	// informational only: Opus always decodes at 48 kHz. Google TTS writes
	// 24000 there even when asked for 48 kHz, so it must not be checked.
	if mappingFamily := pkt[18]; mappingFamily != 0 {
		return 0, fmt.Errorf("oggopus: unsupported channel mapping family %d", mappingFamily)
	}
	return binary.LittleEndian.Uint16(pkt[10:12]), nil
}

func validateTags(pkt []byte) error {
	if !bytes.HasPrefix(pkt, tagsMagic) {
		return errors.New("oggopus: second packet is not OpusTags")
	}
	if len(pkt) < tagsMinSize {
		return errors.New("oggopus: truncated OpusTags header")
	}
	return nil
}
