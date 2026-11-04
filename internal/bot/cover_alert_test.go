package bot

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type fakeSender struct {
	channel, content string
	calls            int
	err              error
}

func (f *fakeSender) ChannelMessageSend(channelID, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.calls++
	f.channel, f.content = channelID, content
	return &discordgo.Message{}, f.err
}

func TestBraveNotifier_PostsThaiMessageWithResetTime(t *testing.T) {
	f := &fakeSender{}
	until := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	braveNotifier{s: f, channelID: "123", fallback: "Unsplash"}.post(until)
	want := "⚠️ เครดิต Brave Search เดือนนี้หมดแล้ว — ใช้รูปจาก Unsplash แทนจนถึง <t:1796083200:F>"
	if f.channel != "123" || f.content != want {
		t.Fatalf("posted %q in %q, want %q in 123", f.content, f.channel, want)
	}
	// A failed post is only logged.
	f.err = errors.New("403")
	braveNotifier{s: f, channelID: "123", fallback: "Openverse"}.post(until)
	if !strings.Contains(f.content, "Openverse") {
		t.Errorf("fallback not named: %q", f.content)
	}
}

func TestBraveNotifier_NoChannelStaysQuiet(t *testing.T) {
	f := &fakeSender{}
	braveNotifier{s: f}.BraveQuotaExhausted(time.Now())
	time.Sleep(10 * time.Millisecond)
	if f.calls != 0 {
		t.Fatal("posted without a channel")
	}
}
