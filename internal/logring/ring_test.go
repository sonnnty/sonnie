package logring

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
)

func texts(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func TestRing_KeepsLastLinesInOrder(t *testing.T) {
	r := New(3)
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(r, "line %d\n", i)
	}
	got := r.Lines()
	if want := []string{"line 3", "line 4", "line 5"}; strings.Join(texts(got), "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", texts(got), want)
	}
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("unexpected sequence numbers %+v", got)
	}
}

func TestRing_JoinsPartialWrites(t *testing.T) {
	r := New(10)
	io.WriteString(r, "hel")
	io.WriteString(r, "lo\nwor")
	if got := texts(r.Lines()); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("got %q", got)
	}
	io.WriteString(r, "ld\r\nа\nb\n")
	if got := strings.Join(texts(r.Lines()), "|"); got != "hello|world|а|b" {
		t.Fatalf("got %q", got)
	}
}

func TestRing_BoundsLongLines(t *testing.T) {
	r := New(2)
	io.WriteString(r, strings.Repeat("x", 3*maxLineLen))
	io.WriteString(r, strings.Repeat("y", 3*maxLineLen)+"\n")
	got := r.Lines()
	if len(got) != 1 || len(got[0].Text) > maxLineLen+len("…") {
		t.Fatalf("line not bounded: %d bytes", len(got[0].Text))
	}
}

func TestRing_RedactsSecrets(t *testing.T) {
	r := New(10)
	r.SetSecrets("Bot-token-123456", "short", "")
	io.WriteString(r, "using Bot-token-123456 and short\n")
	if got := r.Lines()[0].Text; got != "using [redacted] and short" {
		t.Fatalf("got %q", got)
	}
}

func TestRing_TeesStandardLogger(t *testing.T) {
	var out bytes.Buffer
	r := New(10)
	l := log.New(io.MultiWriter(&out, r), "", 0)
	l.Println("first")
	l.Printf("second %d", 2)
	if out.String() != "first\nsecond 2\n" {
		t.Fatalf("stdout changed: %q", out.String())
	}
	if got := strings.Join(texts(r.Lines()), "|"); got != "first|second 2" {
		t.Fatalf("ring got %q", got)
	}
}

func TestRing_Concurrent(t *testing.T) {
	r := New(50)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				fmt.Fprintf(r, "g%d %d\n", g, i)
				_ = r.Lines()
			}
		}()
	}
	wg.Wait()
	got := r.Lines()
	if len(got) != 50 || got[49].Seq != 800 {
		t.Fatalf("got %d lines, last seq %d", len(got), got[len(got)-1].Seq)
	}
}
