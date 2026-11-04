package events

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// syncBuffer is a log destination the race detector accepts.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) lines(substr string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, l := range strings.Split(b.buf.String(), "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

// captureLog sends the standard logger to a buffer for the test.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	b := &syncBuffer{}
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(b)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(flags)
	})
	return b
}

func TestReject_LogsOneLinePerInput(t *testing.T) {
	logs := captureLog(t)
	hs := newHarness()
	long := "วันนี้ 10:00 " + strings.Repeat("ก", 200)
	hs.h.HandleCommand(command("42", permCreate, "create", strOpt("name", "x"), strOpt("when", "วันนี้ 10:00"), strOpt("location", "y")))
	hs.h.HandleCommand(command("42", permCreate, "create", strOpt("name", "x"), strOpt("when", long), strOpt("location", "y")))
	hs.h.HandleCommand(command("43", permCreate, "create", strOpt("name", "x"), strOpt("when", "พรุ่งนี้ 20:00")))
	hs.h.HandleCommand(command("44", permManage, "edit", strOpt("event", "nope"), strOpt("name", "y")))

	got := logs.lines("rejected /event")
	if len(got) != 4 {
		t.Fatalf("%d log lines:\n%s", len(got), strings.Join(got, "\n"))
	}
	if want := `rejected /event create input from user 42: field when, value "วันนี้ 10:00": เวลาเริ่ม: เวลานี้ผ่านไปแล้ว`; !strings.Contains(got[0], want) {
		t.Errorf("past time:\n got %s\nwant %s", got[0], want)
	}
	value := strings.SplitN(strings.SplitN(got[1], `value "`, 2)[1], `"`, 2)[0]
	if n := len([]rune(value)); n != maxLoggedValue || !strings.HasSuffix(value, "…") || len([]rune(got[1])) > 400 {
		t.Errorf("long value not truncated to 80 characters: %s", got[1])
	}
	if !strings.Contains(got[2], "user 43") || !strings.Contains(got[2], "field place/location") {
		t.Errorf("no place: %s", got[2])
	}
	if !strings.Contains(got[3], "/event edit input from user 44: field event, value \"nope\"") {
		t.Errorf("unknown event: %s", got[3])
	}
}

func TestReject_MentionInWhenGetsHint(t *testing.T) {
	logs := captureLog(t)
	hs := newHarness()
	hs.h.HandleCommand(command("42", permCreate, "create", strOpt("name", "x"), strOpt("when", "<@123> พรุ่งนี้ 20:00"), strOpt("location", "y")))
	reply := ephemeralText(t, hs.lastResponse(t))
	if !strings.Contains(reply, "ช่อง when ใส่เวลาเท่านั้น") || !strings.Contains(reply, "เวลาเริ่ม") {
		t.Fatalf("reply %q", reply)
	}
	if l := logs.lines("rejected"); len(l) != 1 || strings.Contains(l[0], "\n") || !strings.Contains(l[0], `value "<@123> พรุ่งนี้ 20:00"`) {
		t.Fatalf("log %v", l)
	}

	// Without a mention there is no hint.
	hs.h.HandleCommand(command("42", permCreate, "create", strOpt("name", "x"), strOpt("when", "เร็วๆนี้"), strOpt("location", "y")))
	if reply := ephemeralText(t, hs.lastResponse(t)); strings.Contains(reply, "ใส่เวลาเท่านั้น") {
		t.Fatalf("hint without a mention: %q", reply)
	}
	// The end option gets its own hint, so does /event edit.
	seed(hs, "5001", "77")
	hs.h.HandleCommand(command("u", permManage, "edit", strOpt("event", "5001"), strOpt("end", "@everyone 23:00")))
	if reply := ephemeralText(t, hs.lastResponse(t)); !strings.Contains(reply, "ช่อง end ใส่เวลาเท่านั้น") {
		t.Fatalf("edit end reply %q", reply)
	}
}

func TestReject_AcceptedInputIsNotLogged(t *testing.T) {
	logs := captureLog(t)
	hs := newHarness()
	hs.h.HandleCommand(command("42", permCreate, "create", strOpt("name", "x"), strOpt("when", "พรุ่งนี้ 20:00"), strOpt("location", "y")))
	if l := logs.lines("rejected"); len(l) != 0 {
		t.Fatalf("logged %v", l)
	}
	if hs.api.responses[0].Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatal("not created")
	}
}
