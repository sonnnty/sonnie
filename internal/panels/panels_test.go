package panels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/cover"
	"github.com/Sonty15/SONNIE/internal/dashboard"
	"github.com/Sonty15/SONNIE/internal/events"
	"github.com/Sonty15/SONNIE/internal/logring"
	"github.com/Sonty15/SONNIE/internal/readaloud"
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
	"github.com/Sonty15/SONNIE/internal/webhook"
)

var now = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func clock() time.Time { return now }

type fakeNames map[string]string

func (n fakeNames) Guild(id string) string           { return n["g:"+id] }
func (n fakeNames) Channel(id string) string         { return n["c:"+id] }
func (n fakeNames) Member(guild, user string) string { return n["u:"+guild+"/"+user] }

var names = fakeNames{
	"g:g1": "บ้านเพื่อน", "c:v1": "ห้องคุย", "c:t1": "แชท", "c:t2": "บอท",
	"u:g1/u1": "สมชาย", "c:remind": "ประกาศ", "c:111": "alerts",
}

// snapshotJSON runs the panel and returns its JSON decoded generically, the
// way the page sees it.
func snapshotJSON(t *testing.T, p dashboard.Panel) map[string]any {
	t.Helper()
	data, err := p.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("%s: %v", p.ID, err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("%s: marshal: %v", p.ID, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func get(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("at %v: not an object: %#v", k, v)
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				t.Fatalf("at [%d]: not an array of that length: %#v", k, v)
			}
			v = a[k]
		}
	}
	return v
}

func TestBotPanel(t *testing.T) {
	p := Bot(now.Add(-90*time.Minute), func() BotInfo {
		return BotInfo{Online: true, Latency: 42 * time.Millisecond, User: "SONNIE", Guilds: []GuildInfo{{ID: "g1", Name: "บ้านเพื่อน", Members: 12}}}
	}, clock)
	if p.ID != "bot" || p.Title == "" {
		t.Fatalf("panel %+v", p)
	}
	d := snapshotJSON(t, p)
	if d["online"] != true || d["latency_ms"] != float64(42) || d["uptime_seconds"] != float64(5400) || d["user"] != "SONNIE" {
		t.Fatalf("got %v", d)
	}
	if d["version"] == "" || !strings.HasPrefix(d["go_version"].(string), "go") || d["goroutines"].(float64) < 1 || d["heap_alloc_bytes"].(float64) <= 0 {
		t.Fatalf("runtime %v", d)
	}
	if get(t, d, "guilds", 0, "name") != "บ้านเพื่อน" || get(t, d, "guilds", 0, "members") != float64(12) {
		t.Fatalf("guilds %v", d["guilds"])
	}

	offline := snapshotJSON(t, Bot(now, func() BotInfo { return BotInfo{Latency: time.Second} }, clock))
	if offline["online"] != false || offline["latency_ms"] != float64(0) || offline["guilds"] == nil {
		t.Fatalf("offline %v", offline)
	}
}

type fakeSessions []readaloud.SessionStatus

func (f fakeSessions) Status() []readaloud.SessionStatus { return f }

func TestTTSSessionsPanel(t *testing.T) {
	off := snapshotJSON(t, TTSSessions(nil, names))
	if off["enabled"] != false || len(off["sessions"].([]any)) != 0 {
		t.Fatalf("disabled %v", off)
	}
	none := snapshotJSON(t, TTSSessions(fakeSessions{}, names))
	if none["enabled"] != true || len(none["sessions"].([]any)) != 0 {
		t.Fatalf("no session %v", none)
	}

	src := fakeSessions{{
		GuildID: "g1", VoiceChannelID: "v1", TextChannels: []string{"t1", "t2"},
		Participants: []readaloud.ParticipantStatus{{UserID: "u1", ChannelID: "t1", Voice: "Charon"}, {UserID: "u2", ChannelID: "t2", Voice: "Puck"}},
		Started:      now.Add(-time.Hour), Queue: 2, Read: 7, Deleted: 5,
	}}
	d := snapshotJSON(t, TTSSessions(src, names))
	s := get(t, d, "sessions", 0)
	if get(t, s, "guild", "name") != "บ้านเพื่อน" || get(t, s, "voice_channel", "name") != "ห้องคุย" || get(t, s, "text_channels", 1, "name") != "บอท" {
		t.Fatalf("names %v", s)
	}
	if get(t, s, "queue") != float64(2) || get(t, s, "read") != float64(7) || get(t, s, "deleted") != float64(5) || get(t, s, "started_at") != "2026-11-05T09:00:00Z" {
		t.Fatalf("counts %v", s)
	}
	if get(t, s, "participants", 0, "user", "name") != "สมชาย" || get(t, s, "participants", 0, "voice") != "Charon" || get(t, s, "participants", 0, "channel", "id") != "t1" {
		t.Fatalf("participant 0 %v", get(t, s, "participants", 0))
	}
	// An uncached name falls back to the ID on the page: the JSON keeps it.
	if get(t, s, "participants", 1, "user", "id") != "u2" || get(t, s, "participants", 1, "user", "name") != nil {
		t.Fatalf("participant 1 %v", get(t, s, "participants", 1))
	}
}

type fakeUsage usage.Snapshot

func (f fakeUsage) Snapshot() usage.Snapshot { return usage.Snapshot(f) }

func TestTTSUsagePanel(t *testing.T) {
	timings := func() []tts.Timing {
		return []tts.Timing{{At: now, Path: "stream", FirstAudio: 412 * time.Millisecond, Total: 2310 * time.Millisecond, Bytes: 9000}}
	}
	off := snapshotJSON(t, TTSUsage(nil, timings, clock))
	if off["enabled"] != false || len(off["latencies"].([]any)) != 1 {
		t.Fatalf("disabled %v", off)
	}

	money := fakeUsage{Month: "2026-11", Unit: usage.UnitTHBMicro, Used: 240_000_000, Limit: 300_000_000, Percent: 80, AudioMs: 754_000, Alerted: []int{50, 80}}
	d := snapshotJSON(t, TTSUsage(money, timings, clock))
	if d["enabled"] != true || d["percent"] != float64(80) || d["color"] != fmt.Sprintf("#%06X", usage.ColorOrange) || d["paused"] != false {
		t.Fatalf("money %v", d)
	}
	if d["used_text"] != usage.FormatTHB(240_000_000) || d["limit_text"] != usage.FormatTHB(300_000_000) || d["month_label"] != "พฤศจิกายน 2026" {
		t.Fatalf("texts %v", d)
	}
	if !strings.Contains(d["audio_text"].(string), "นาที") || len(d["alerted"].([]any)) != 2 || d["next_reset"] == nil {
		t.Fatalf("details %v", d)
	}
	if get(t, d, "latencies", 0, "first_audio_ms") != float64(412) || get(t, d, "latencies", 0, "path") != "stream" {
		t.Fatalf("latencies %v", d["latencies"])
	}

	paused := snapshotJSON(t, TTSUsage(fakeUsage{Month: "2026-11", Unit: usage.UnitChars, Used: 1_000_000, Limit: 1_000_000, Percent: 100, Paused: true}, nil, clock))
	if paused["paused"] != true || paused["color"] != fmt.Sprintf("#%06X", usage.ColorRed) || paused["used_text"] != "1,000,000 ตัวอักษร" {
		t.Fatalf("paused %v", paused)
	}
	if paused["alerted"] == nil || paused["latencies"] == nil {
		t.Fatalf("lists must be [] not null: %v", paused)
	}
}

type fakeSched events.SchedulerStatus

func (f fakeSched) Status() events.SchedulerStatus { return events.SchedulerStatus(f) }

type fakeCovers []cover.ProviderStatus

func (f fakeCovers) Status() []cover.ProviderStatus { return f }

func TestEventsPanel(t *testing.T) {
	end := now.Add(3 * time.Hour)
	sched := fakeSched{
		Running: true, ReminderChannel: "remind", RemindBefore: 15 * time.Minute, AutoStart: true, NextWake: now.Add(45 * time.Minute),
		Events: []events.EventStatus{
			{ID: "e1", GuildID: "g1", Name: "เตะบอล", Start: now.Add(time.Hour), End: &end, Status: discordgo.GuildScheduledEventStatusScheduled,
				EntityType: discordgo.GuildScheduledEventEntityTypeExternal, Location: "สนาม", Interested: 5, RemindAt: now.Add(45 * time.Minute), Managed: true},
			{ID: "e2", GuildID: "g1", Name: "เล่นเกม", Start: now.Add(-time.Minute), Status: discordgo.GuildScheduledEventStatusActive,
				EntityType: discordgo.GuildScheduledEventEntityTypeVoice, ChannelID: "v1", Failures: 3, GaveUp: true},
		},
	}
	covers := fakeCovers{{Name: "brave", PausedUntil: now.Add(48 * time.Hour)}, {Name: "unsplash"}, {Name: "openverse"}}
	d := snapshotJSON(t, Events(sched, covers, names))

	s := d["scheduler"]
	if get(t, s, "running") != true || get(t, s, "reminder_channel", "name") != "ประกาศ" || get(t, s, "remind_before_minutes") != float64(15) || get(t, s, "next_wake") == nil {
		t.Fatalf("scheduler %v", s)
	}
	e1 := get(t, d, "events", 0)
	if get(t, e1, "status") != "scheduled" || get(t, e1, "kind") != "external" || get(t, e1, "where") != "📍 สนาม" || get(t, e1, "interested") != float64(5) ||
		get(t, e1, "remind_at") == nil || get(t, e1, "managed") != true || get(t, e1, "url") != "https://discord.com/events/g1/e1" || get(t, e1, "end") == nil {
		t.Fatalf("e1 %v", e1)
	}
	e2 := get(t, d, "events", 1)
	if get(t, e2, "status") != "active" || get(t, e2, "where") != "🔊 ห้องคุย" || get(t, e2, "gave_up") != true || get(t, e2, "remind_at") != nil {
		t.Fatalf("e2 %v", e2)
	}
	if get(t, d, "covers", 0, "name") != "brave" || get(t, d, "covers", 0, "paused_until") != "2026-11-07T10:00:00Z" || get(t, d, "covers", 1, "paused_until") != nil {
		t.Fatalf("covers %v", d["covers"])
	}

	empty := snapshotJSON(t, Events(fakeSched{}, nil, nil))
	if len(empty["events"].([]any)) != 0 || len(empty["covers"].([]any)) != 0 || get(t, empty, "scheduler", "remind_before_minutes") != float64(0) {
		t.Fatalf("empty %v", empty)
	}
}

func TestLogsPanel(t *testing.T) {
	ring := logring.New(3)
	empty := snapshotJSON(t, Logs(ring))
	if lines := empty["lines"].([]any); len(lines) != 0 {
		t.Fatalf("empty %v", empty)
	}
	for i := 1; i <= 4; i++ {
		io.WriteString(ring, fmt.Sprintf("line %d\n", i))
	}
	d := snapshotJSON(t, Logs(ring))
	if get(t, d, "lines", 0, "text") != "line 2" || get(t, d, "lines", 2, "text") != "line 4" || get(t, d, "lines", 2, "seq") != float64(4) {
		t.Fatalf("lines %v", d["lines"])
	}
}

type fakePoster struct{}

func (fakePoster) ChannelMessageSendComplex(channelID string, _ *discordgo.MessageSend, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	return &discordgo.Message{ID: "m1", ChannelID: channelID}, nil
}

func TestWebhooksPanel(t *testing.T) {
	off := snapshotJSON(t, Webhooks(nil, names))
	if len(off["hooks"].([]any)) != 0 || len(off["recent"].([]any)) != 0 || off["rate_limit_per_minute"] != float64(webhook.DefaultRateLimit) {
		t.Fatalf("no hooks %v", off)
	}

	const token = "a-very-secret-token-123"
	h := webhook.New(webhook.Config{Hooks: []webhook.Hook{{Name: "grafana", Token: token, ChannelID: "111"}}, Poster: fakePoster{}, Now: clock})
	d := snapshotJSON(t, Webhooks(h, names))
	if get(t, d, "hooks", 0, "name") != "grafana" || get(t, d, "hooks", 0, "channel", "name") != "alerts" || get(t, d, "hooks", 0, "calls") != float64(0) {
		t.Fatalf("hooks %v", d["hooks"])
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), token) {
		t.Fatalf("panel leaks the token: %s", raw)
	}
}
