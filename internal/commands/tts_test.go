package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sonty15/SONNIE/internal/readaloud"
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
	if got := strings.Join(names, ","); got != "join,leave" {
		t.Fatalf("expected subcommands join,leave, got %s", got)
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
	if got := ttsJoinReply(context.Background(), ctl, "g", "v", "t"); !strings.Contains(got, "Connect and Speak") {
		t.Fatalf("expected a permissions hint, got %q", got)
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
