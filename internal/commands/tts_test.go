package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/readaloud"
	"github.com/Sonty15/SONNIE/internal/usage"
)

type fakeTTSController struct {
	joinErr, leaveErr error
	joined            []string
}

func (f *fakeTTSController) Join(_ context.Context, guildID, voiceChannelID, textChannelID string) error {
	f.joined = append(f.joined, guildID+"/"+voiceChannelID+"/"+textChannelID)
	return f.joinErr
}

func (f *fakeTTSController) Leave(string) error { return f.leaveErr }

func TestTTS_IsRegisteredWithSubcommands(t *testing.T) {
	c, ok := Find("tts")
	if !ok {
		t.Fatal("expected \"tts\" command to be registered")
	}
	var names []string
	for _, o := range c.Definition.Options {
		names = append(names, o.Name)
	}
	if got := strings.Join(names, ","); got != "join,leave,usage" {
		t.Fatalf("expected subcommands join,leave,usage, got %s", got)
	}
}

func TestTTSJoinReply(t *testing.T) {
	ctl := &fakeTTSController{}
	got := ttsJoinReply(context.Background(), ctl, "g", "v", "t")
	if want := "Reading <#t> aloud in <#v>. Use /tts leave to stop."; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	if len(ctl.joined) != 1 || ctl.joined[0] != "g/v/t" {
		t.Fatalf("unexpected joins: %v", ctl.joined)
	}

	ctl.joinErr = errors.New("boom")
	if got := ttsJoinReply(context.Background(), ctl, "g", "v", "t"); !strings.Contains(got, "Connect and Speak") || !strings.Contains(got, "voice connection") {
		t.Fatalf("expected permissions and voice connection hint, got %q", got)
	}

	var logBuf bytes.Buffer
	origLog := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origLog)

	ctl.joinErr = fmt.Errorf("voice: join: %w", context.DeadlineExceeded)
	gotTimeout := ttsJoinReply(context.Background(), ctl, "g", "v", "t")
	if !strings.Contains(gotTimeout, "timed out") || !strings.Contains(gotTimeout, "Connect and Speak") {
		t.Fatalf("expected timeout and permissions hint, got %q", gotTimeout)
	}
	if !strings.Contains(logBuf.String(), context.DeadlineExceeded.Error()) {
		t.Fatalf("expected real error in log, got %q", logBuf.String())
	}
}

func TestTTSLeaveReply(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"ok", nil, "Stopped reading and left the voice channel."},
		{"not bound", readaloud.ErrNotBound, "I'm not reading any channel in this server."},
		{"other error", errors.New("boom"), "Stopped reading, but leaving the voice channel failed."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ttsLeaveReply(&fakeTTSController{leaveErr: tt.err}, "g"); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

type fakeUsage struct{ snap usage.Snapshot }

func (f fakeUsage) Snapshot() usage.Snapshot { return f.snap }

func TestTTSUsageReply(t *testing.T) {
	tests := []struct {
		name string
		u    UsageReporter
		want []string
	}{
		{"active", fakeUsage{usage.Snapshot{Month: "2026-11", Used: 800000, Limit: 1000000, Percent: 80}},
			[]string{"2026-11", "800,000 / 1,000,000", "(80%)", "Reading is active"}},
		{"paused", fakeUsage{usage.Snapshot{Month: "2026-11", Used: 1000000, Limit: 1000000, Percent: 100, Paused: true}},
			[]string{"1,000,000 / 1,000,000", "(100%)", "paused"}},
		{"no cap", fakeUsage{usage.Snapshot{Month: "2026-11", Used: 1234}},
			[]string{"1,234 characters", "No monthly cap"}},
		{"money active", fakeUsage{usage.Snapshot{Month: "2026-11", Unit: usage.UnitTHBMicro, Used: 240000000, Limit: 300000000, Percent: 80, AudioMs: 123400}},
			[]string{"spend for 2026-11", "฿240.00 / ฿300.00 budget", "(80%)", "Reading is active", "123.4 s"}},
		{"money paused", fakeUsage{usage.Snapshot{Month: "2026-11", Unit: usage.UnitTHBMicro, Used: 300100000, Limit: 300000000, Percent: 100, Paused: true, AudioMs: 5000}},
			[]string{"฿300.10 / ฿300.00", "(100%)", "paused", "5.0 s"}},
		{"money no budget", fakeUsage{usage.Snapshot{Month: "2026-11", Unit: usage.UnitTHBMicro, Used: 12340000}},
			[]string{"฿12.34", "No monthly budget"}},
		{"untracked", nil, []string{"not tracked"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ttsUsageReply(tt.u)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("reply %q missing %q", got, w)
				}
			}
		})
	}
}

func TestTTSUsageResponse(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	// Untracked usage: ephemeral text response
	respUntracked := ttsUsageResponse(nil, now)
	if respUntracked.Flags != discordgo.MessageFlagsEphemeral {
		t.Errorf("untracked flags = %v, want ephemeral (%v)", respUntracked.Flags, discordgo.MessageFlagsEphemeral)
	}
	if !strings.Contains(respUntracked.Content, "not tracked") {
		t.Errorf("untracked content = %q", respUntracked.Content)
	}
	if len(respUntracked.Embeds) != 0 {
		t.Errorf("untracked embeds = %v, want empty", respUntracked.Embeds)
	}

	// Active money mode: ephemeral embed response
	snap := usage.Snapshot{
		Month:   "2026-11",
		Unit:    usage.UnitTHBMicro,
		Used:    36200000,
		Limit:   300000000,
		Percent: 12,
		AudioMs: 6700,
	}
	respMoney := ttsUsageResponse(fakeUsage{snap}, now)
	if respMoney.Flags != discordgo.MessageFlagsEphemeral {
		t.Errorf("money flags = %v, want ephemeral", respMoney.Flags)
	}
	if len(respMoney.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(respMoney.Embeds))
	}
	emb := respMoney.Embeds[0]
	if emb.Title != "🔊 Google TTS · พฤศจิกายน 2026" {
		t.Errorf("title = %q", emb.Title)
	}
	if !strings.Contains(emb.Description, "▰▱▱▱▱▱▱▱▱▱  12%") || !strings.Contains(emb.Description, "฿36.20 / ฿300.00") {
		t.Errorf("description: %q", emb.Description)
	}
	if emb.Color != usage.ColorGreen {
		t.Errorf("color = %#x, want %#x", emb.Color, usage.ColorGreen)
	}
	if len(emb.Fields) != 3 {
		t.Fatalf("fields = %d, want 3", len(emb.Fields))
	}
	if emb.Fields[0].Value != "6.7 วินาที" {
		t.Errorf("audio field = %q", emb.Fields[0].Value)
	}
	if emb.Fields[1].Value != "🟢 กำลังอ่าน" {
		t.Errorf("status field = %q", emb.Fields[1].Value)
	}
	if !strings.HasPrefix(emb.Fields[2].Value, "<t:") {
		t.Errorf("reset field = %q", emb.Fields[2].Value)
	}
}
