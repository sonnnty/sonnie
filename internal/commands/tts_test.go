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
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
)

type fakeTTSController struct {
	joinRes  readaloud.JoinResult
	joinErr  error
	leaveRes readaloud.LeaveResult
	leaveErr error

	joined []readaloud.JoinRequest
	left   []string
}

func (f *fakeTTSController) Join(_ context.Context, req readaloud.JoinRequest) (readaloud.JoinResult, error) {
	f.joined = append(f.joined, req)
	return f.joinRes, f.joinErr
}

func (f *fakeTTSController) Leave(guildID, userID string) (readaloud.LeaveResult, error) {
	f.left = append(f.left, guildID+"/"+userID)
	return f.leaveRes, f.leaveErr
}

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

func TestTTS_DescriptionsFitDiscordLimit(t *testing.T) {
	c, _ := Find("tts")
	check := func(name, desc string) {
		if n := len([]rune(desc)); n == 0 || n > 100 {
			t.Errorf("%s description is %d characters; Discord allows 1-100", name, n)
		}
	}
	check("tts", c.Definition.Description)
	for _, o := range c.Definition.Options {
		check(o.Name, o.Description)
		for _, sub := range o.Options {
			check(o.Name+"."+sub.Name, sub.Description)
		}
	}
}

type poolController struct {
	fakeTTSController
	pool []string
}

func (p *poolController) VoicePool() []string { return p.pool }

func TestTTS_JoinVoiceOptionChoices(t *testing.T) {
	c, ok := Find("tts")
	if !ok {
		t.Fatal("tts command not found")
	}
	joinOpt := c.Definition.Options[0]
	if joinOpt.Name != "join" || len(joinOpt.Options) != 1 {
		t.Fatalf("expected 1 option on /tts join, got %+v", joinOpt.Options)
	}
	voiceOpt := joinOpt.Options[0]
	if voiceOpt.Name != "voice" || voiceOpt.Type != discordgo.ApplicationCommandOptionString || voiceOpt.Required {
		t.Fatalf("unexpected voice option: %+v", voiceOpt)
	}
	if !voiceOpt.Autocomplete || len(voiceOpt.Choices) != 0 {
		t.Fatalf("voice must use autocomplete (30 voices exceed 25 fixed choices): %+v", voiceOpt)
	}
	if c.Autocomplete == nil {
		t.Fatal("/tts has no autocomplete handler")
	}

	all := len(tts.DefaultVoicePool) + len(tts.FemaleVoices)
	if len(pickableVoices) != all {
		t.Fatalf("%d pickable voices, want %d", len(pickableVoices), all)
	}
	if got := voiceSuggestions(""); len(got) != 25 {
		t.Fatalf("empty query: %d suggestions, want the 25 maximum", len(got))
	}
	female := voiceSuggestions("หญิง")
	if len(female) != len(tts.FemaleVoices) || female[0].Name != "Achernar (หญิง)" || female[0].Value != "Achernar" {
		t.Fatalf("female suggestions %d, first %+v", len(female), female[0])
	}
	if got := voiceSuggestions("pu"); len(got) != 2 { // Puck, Pulcherrima
		t.Fatalf("'pu': %d suggestions", len(got))
	}
	if got := voiceSuggestions("ชาย"); len(got) != len(tts.DefaultVoicePool) {
		t.Fatalf("male suggestions %d", len(got))
	}
	for in, want := range map[string]string{"kore": "Kore", "Puck (ชาย)": "Puck", " charon ": "Charon"} {
		if got, ok := matchVoice(in); !ok || got != want {
			t.Errorf("matchVoice(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := matchVoice("Nobody"); ok {
		t.Error("unknown voice accepted")
	}

	// A custom pool comes first; female voices are still offered.
	SetTTS(&poolController{pool: []string{"Orus", "Custom"}})
	t.Cleanup(func() { SetTTS(nil) })
	if len(pickableVoices) != 2+len(tts.FemaleVoices) || pickableVoices[1].Label != "Custom" || pickableVoices[0].Label != "Orus (ชาย)" {
		t.Fatalf("custom pool: %+v", pickableVoices[:3])
	}
}

func TestTTSJoinReply(t *testing.T) {
	req := readaloud.JoinRequest{GuildID: "g", UserID: "u", VoiceChannelID: "v", TextChannelID: "t"}
	tests := []struct {
		name string
		ctl  *fakeTTSController
		want []string
		not  []string
	}{
		{"started", &fakeTTSController{joinRes: readaloud.JoinResult{Started: true, Voice: "Puck", Participants: 1, CanManage: true}},
			[]string{"✅ เข้าร่วมแล้ว", "<#t>", "อ่านแล้วลบ", "เสียง Puck"}, []string{"ไม่มีสิทธิ์", "<#v>"}},
		{"added", &fakeTTSController{joinRes: readaloud.JoinResult{Voice: "Orus", Participants: 3, CanManage: true}},
			[]string{"✅ เข้าร่วมแล้ว", "<#t>", "อ่านแล้วลบ", "เสียง Orus"}, []string{"เริ่มอ่าน"}},
		{"already in", &fakeTTSController{joinRes: readaloud.JoinResult{AlreadyIn: true, Voice: "Puck", Participants: 2, CanManage: true}},
			[]string{"อยู่ในการอ่านนี้แล้ว", "<#t>", "เสียง Puck"}, []string{"ลบข้อความ"}},
		{"voice changed", &fakeTTSController{joinRes: readaloud.JoinResult{VoiceChanged: true, Voice: "Fenrir", Participants: 2, CanManage: true}},
			[]string{"เปลี่ยนเสียงแล้ว", "<#t>", "เสียง Fenrir"}, []string{"ไม่มีสิทธิ์"}},
		{"no manage messages", &fakeTTSController{joinRes: readaloud.JoinResult{Started: true, Voice: "Charon", Participants: 1}},
			[]string{"ไม่มีสิทธิ์ลบข้อความ", "เสียง Charon"}, []string{"อ่านแล้วลบ"}},
		{"busy elsewhere", &fakeTTSController{joinErr: &readaloud.BusyError{VoiceChannelID: "v9"}},
			[]string{"<#v9>", "ยังมีคนอยู่"}, nil},
		{"moved", &fakeTTSController{joinRes: readaloud.JoinResult{Moved: true, PreviousChannelID: "t0", Voice: "Puck", Participants: 2, CanManage: true}},
			[]string{"ย้ายแล้ว", "<#t>", "<#t0>", "เสียง Puck"}, []string{"เข้าร่วมแล้ว"}},
		{"moved without manage", &fakeTTSController{joinRes: readaloud.JoinResult{Moved: true, PreviousChannelID: "t0", Voice: "Puck", Participants: 2}},
			[]string{"ย้ายแล้ว", "<#t>", "ไม่มีสิทธิ์ลบข้อความ", "เสียง Puck"}, nil},
		{"session message failed", &fakeTTSController{joinErr: fmt.Errorf("%w: boom", readaloud.ErrSessionMessage)},
			[]string{"Send Messages", "Embed Links"}, nil},
		{"voice join failed", &fakeTTSController{joinErr: errors.New("boom")},
			[]string{"Connect และ Speak"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			origLog := log.Writer()
			log.SetOutput(&logBuf)
			defer log.SetOutput(origLog)

			got := ttsJoinReply(context.Background(), tt.ctl, req)

			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("reply %q missing %q", got, w)
				}
			}
			for _, w := range tt.not {
				if strings.Contains(got, w) {
					t.Errorf("reply %q must not contain %q", got, w)
				}
			}
			if len(tt.ctl.joined) != 1 || tt.ctl.joined[0] != req {
				t.Errorf("controller got %v, want %v", tt.ctl.joined, req)
			}
		})
	}

	t.Run("timeout logs the real error", func(t *testing.T) {
		var logBuf bytes.Buffer
		origLog := log.Writer()
		log.SetOutput(&logBuf)
		defer log.SetOutput(origLog)

		ctl := &fakeTTSController{joinErr: fmt.Errorf("voice: join: %w", context.DeadlineExceeded)}
		got := ttsJoinReply(context.Background(), ctl, req)
		if !strings.Contains(got, "ไม่ทันเวลา") || !strings.Contains(got, "Connect และ Speak") {
			t.Fatalf("expected timeout and permissions hint, got %q", got)
		}
		if !strings.Contains(logBuf.String(), context.DeadlineExceeded.Error()) {
			t.Fatalf("expected real error in log, got %q", logBuf.String())
		}
	})
}

func TestTTSLeaveReply(t *testing.T) {
	tests := []struct {
		name string
		ctl  *fakeTTSController
		want string
	}{
		{"others remain", &fakeTTSController{leaveRes: readaloud.LeaveResult{Participants: 2}}, "เหลืออีก 2 คน"},
		{"last one", &fakeTTSController{leaveRes: readaloud.LeaveResult{Ended: true}}, "จบการอ่าน"},
		{"not bound", &fakeTTSController{leaveErr: readaloud.ErrNotBound}, "ไม่ได้อ่านออกเสียงในเซิร์ฟเวอร์นี้"},
		{"not a participant", &fakeTTSController{leaveErr: readaloud.ErrNotParticipant}, "คุณไม่ได้อยู่ในการอ่านนี้"},
		{"voice leave failed", &fakeTTSController{leaveRes: readaloud.LeaveResult{Ended: true}, leaveErr: errors.New("boom")}, "ออกจากห้องเสียงไม่สำเร็จ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ttsLeaveReply(tt.ctl, "g", "u"); !strings.Contains(got, tt.want) {
				t.Fatalf("reply %q missing %q", got, tt.want)
			}
			if len(tt.ctl.left) != 1 || tt.ctl.left[0] != "g/u" {
				t.Fatalf("controller got %v", tt.ctl.left)
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
