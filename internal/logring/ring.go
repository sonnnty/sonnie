// Package logring keeps the most recent log lines in memory for the
// dashboard. main tees the standard logger into a Ring, so what goes to the
// process output is unchanged.
package logring

import (
	"bytes"
	"strings"
	"sync"
)

const (
	// DefaultSize is how many lines the dashboard shows.
	DefaultSize = 300
	// maxLineLen bounds the memory a single runaway line can take.
	maxLineLen = 4096
	// minSecretLen keeps short values (which would also match ordinary
	// words) out of the redaction list.
	minSecretLen = 8
	redacted     = "[redacted]"
)

// Line is one log line. Seq increases by one for every line written, so a
// reader can tell how many lines it missed.
type Line struct {
	Seq  uint64 `json:"seq"`
	Text string `json:"text"`
}

// Ring is an io.Writer that keeps the last lines written to it. It is safe
// for concurrent use.
type Ring struct {
	mu      sync.Mutex
	lines   []Line // circular, len == cap once full
	next    int    // where the next line goes once full
	seq     uint64
	partial []byte // a line without its newline yet
	redact  *strings.Replacer
}

// New makes a Ring holding up to size lines (DefaultSize if size <= 0).
func New(size int) *Ring {
	if size <= 0 {
		size = DefaultSize
	}
	return &Ring{lines: make([]Line, 0, size)}
}

// SetSecrets makes the ring replace every occurrence of each secret with
// "[redacted]". The bot never logs its secrets; this is a second line of
// defence so the dashboard cannot show one if a library ever did. Values
// shorter than 8 bytes are ignored.
func (r *Ring) SetSecrets(secrets ...string) {
	var pairs []string
	for _, s := range secrets {
		if len(s) >= minSecretLen {
			pairs = append(pairs, s, redacted)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(pairs) == 0 {
		r.redact = nil
		return
	}
	r.redact = strings.NewReplacer(pairs...)
}

// Write implements io.Writer. The standard logger writes one whole line per
// call; anything after the last newline waits for the next Write.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	data := p
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if room := maxLineLen - len(r.partial); room > 0 {
				r.partial = append(r.partial, data[:min(len(data), room)]...)
			}
			break
		}
		line := data[:i]
		if len(r.partial) > 0 {
			line = append(r.partial, line...)
			r.partial = nil
		}
		r.addLocked(string(line))
		data = data[i+1:]
	}
	return len(p), nil
}

func (r *Ring) addLocked(text string) {
	text = strings.TrimRight(text, "\r")
	if len(text) > maxLineLen {
		text = text[:maxLineLen] + "…"
	}
	if r.redact != nil {
		text = r.redact.Replace(text)
	}
	r.seq++
	l := Line{Seq: r.seq, Text: text}
	if len(r.lines) < cap(r.lines) {
		r.lines = append(r.lines, l)
		return
	}
	r.lines[r.next] = l
	r.next = (r.next + 1) % len(r.lines)
}

// Lines returns the kept lines, oldest first.
func (r *Ring) Lines() []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Line, 0, len(r.lines))
	out = append(out, r.lines[r.next:]...)
	return append(out, r.lines[:r.next]...)
}
