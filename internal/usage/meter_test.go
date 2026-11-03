package usage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/tts"
)

type alertRecord struct {
	text  string
	embed *discordgo.MessageEmbed
}

type fakeChannel struct {
	mu        sync.Mutex
	status    string
	isLegacy  bool
	findErr   error
	saveErr   error
	alertErr  error
	saves     []*discordgo.MessageEmbed
	alerts    []alertRecord
	alertFail int // fail this many Alert calls first
}

func (c *fakeChannel) FindStatus(context.Context) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	isLegacy := c.isLegacy
	if !isLegacy && c.status != "" && !strings.Contains(c.status, StatusFooterHuman) {
		isLegacy = true
	}
	return c.status, isLegacy, c.findErr
}

func (c *fakeChannel) SaveStatus(_ context.Context, embed *discordgo.MessageEmbed) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.saveErr != nil {
		return c.saveErr
	}
	c.status = embed.Footer.Text
	c.isLegacy = false
	c.saves = append(c.saves, embed)
	return nil
}

func (c *fakeChannel) Alert(_ context.Context, text string, embed *discordgo.MessageEmbed) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.alertFail > 0 {
		c.alertFail--
		return errors.New("discord down")
	}
	c.alerts = append(c.alerts, alertRecord{text: text, embed: embed})
	return nil
}

func (c *fakeChannel) Alerts() []alertRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]alertRecord(nil), c.alerts...)
}

func (c *fakeChannel) Saves() []*discordgo.MessageEmbed {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*discordgo.MessageEmbed(nil), c.saves...)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)} }

func text(n int) string { return strings.Repeat("ก", n) } // n runes, 3n bytes

func TestMeter_CountsRunesNotBytes(t *testing.T) {
	inner := &tts.Fake{}
	m := New(Config{Inner: inner, Limit: 1000, Now: newClock().Now})
	if _, err := m.Synthesize(context.Background(), "สวัสดี abc", "th"); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot().Used; got != 10 {
		t.Fatalf("used = %d, want 10 runes", got)
	}
	if reqs := inner.Requests(); len(reqs) != 1 || reqs[0].Text != "สวัสดี abc" {
		t.Fatalf("inner requests: %+v", reqs)
	}
}

func TestMeter_CountsFailedRequests(t *testing.T) {
	inner := &tts.Fake{Err: errors.New("boom")}
	m := New(Config{Inner: inner, Limit: 1000, Now: newClock().Now})
	if _, err := m.Synthesize(context.Background(), text(7), "th"); err == nil {
		t.Fatal("expected inner error")
	}
	if got := m.Snapshot().Used; got != 7 {
		t.Fatalf("used = %d, want 7 (failed request still counted)", got)
	}
}

func TestMeter_CancelledContextIsNotCounted(t *testing.T) {
	inner := &tts.Fake{}
	m := New(Config{Inner: inner, Limit: 1000, Now: newClock().Now})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Synthesize(ctx, "hello", "th"); err == nil {
		t.Fatal("expected error")
	}
	if got := m.Snapshot().Used; got != 0 {
		t.Fatalf("used = %d, want 0", got)
	}
}

func TestMeter_CapRefusesWithoutCallingInner(t *testing.T) {
	inner := &tts.Fake{}
	m := New(Config{Inner: inner, Limit: 10, Now: newClock().Now})
	ctx := context.Background()

	if _, err := m.Synthesize(ctx, text(6), "th"); err != nil {
		t.Fatal(err)
	}
	// 6 + 5 > 10: refused, and not counted.
	_, err := m.Synthesize(ctx, text(5), "th")
	var le *LimitError
	if !errors.Is(err, ErrLimitReached) || !errors.As(err, &le) {
		t.Fatalf("expected LimitError, got %v", err)
	}
	if le.Used != 6 || le.Limit != 10 || le.Month != "2026-11" {
		t.Fatalf("unexpected LimitError %+v", le)
	}
	if got := len(inner.Requests()); got != 1 {
		t.Fatalf("inner called %d times, want 1", got)
	}
	if s := m.Snapshot(); s.Used != 6 || !s.Paused {
		t.Fatalf("snapshot %+v, want used=6 paused", s)
	}
	// A smaller message that still fits is allowed: used + len == limit.
	if _, err := m.Synthesize(ctx, text(4), "th"); err != nil {
		t.Fatalf("message that exactly fits was refused: %v", err)
	}
	if s := m.Snapshot(); s.Used != 10 || s.Percent != 100 {
		t.Fatalf("snapshot %+v", s)
	}
	if _, err := m.Synthesize(ctx, "x", "th"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("expected limit error at 100%%, got %v", err)
	}
}

func TestMeter_NoCapWhenLimitZero(t *testing.T) {
	ch := &fakeChannel{}
	m := New(Config{Inner: &tts.Fake{}, Limit: 0, Channel: ch, Now: newClock().Now})
	for i := 0; i < 3; i++ {
		if _, err := m.Synthesize(context.Background(), text(500000), "th"); err != nil {
			t.Fatal(err)
		}
	}
	s := m.Snapshot()
	if s.Used != 1500000 || s.Paused || s.Percent != 0 {
		t.Fatalf("snapshot %+v", s)
	}
	m.Close()
	if a := ch.Alerts(); len(a) != 0 {
		t.Fatalf("no alerts expected without a cap, got %v", a)
	}
}

func TestMeter_MonthRollover(t *testing.T) {
	clk := newClock()
	inner := &tts.Fake{}
	m := New(Config{Inner: inner, Limit: 10, Now: clk.Now})
	ctx := context.Background()
	if _, err := m.Synthesize(ctx, text(10), "th"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Synthesize(ctx, "x", "th"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("expected cap, got %v", err)
	}

	clk.Set(time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC))
	if s := m.Snapshot(); s.Used != 0 || s.Paused || s.Month != "2026-12" {
		t.Fatalf("after rollover: %+v", s)
	}
	if _, err := m.Synthesize(ctx, text(3), "th"); err != nil {
		t.Fatalf("reading should resume in the new month: %v", err)
	}
}

func TestMeter_RolloverUsesUSPacificCalendar(t *testing.T) {
	clk := &clock{}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100, Now: clk.Now})
	ctx := context.Background()

	// 2026-12-01 07:00 UTC is still 2026-11-30 23:00 in Pacific (PST, UTC-8).
	clk.Set(time.Date(2026, 12, 1, 7, 0, 0, 0, time.UTC))
	if _, err := m.Synthesize(ctx, text(5), "th"); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.Month != "2026-11" || s.Used != 5 {
		t.Fatalf("before Pacific midnight: %+v", s)
	}
	// 08:00 UTC is Pacific midnight.
	clk.Set(time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC))
	if s := m.Snapshot(); s.Month != "2026-12" || s.Used != 0 {
		t.Fatalf("after Pacific midnight: %+v", s)
	}
}

func TestMeter_ThresholdsFireOncePerMonth(t *testing.T) {
	clk := newClock()
	ch := &fakeChannel{}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: clk.Now})
	ctx := context.Background()

	say := func(n int) {
		t.Helper()
		if _, err := m.Synthesize(ctx, text(n), "th"); err != nil && !errors.Is(err, ErrLimitReached) {
			t.Fatal(err)
		}
	}
	say(49)
	say(1) // 50%
	say(10)
	say(19)
	say(1) // 80%
	say(10)
	say(10) // 100%
	say(1)  // refused
	say(1)  // refused again
	m.alertWG.Wait()

	alerts := ch.Alerts()
	if len(alerts) != 3 {
		t.Fatalf("expected 3 alerts, got %d: %v", len(alerts), alerts)
	}
	if !strings.Contains(alerts[0].text, "50%") || !strings.Contains(alerts[0].embed.Description, "50 / 100 ตัวอักษร") || alerts[0].embed.Color != ColorYellow {
		t.Errorf("50%% alert: %+v", alerts[0])
	}
	if !strings.Contains(alerts[1].text, "80%") || !strings.Contains(alerts[1].embed.Description, "80 / 100 ตัวอักษร") || alerts[1].embed.Color != ColorOrange {
		t.Errorf("80%% alert: %+v", alerts[1])
	}
	if !strings.Contains(alerts[2].text, "หยุดอ่าน") || alerts[2].embed.Color != ColorRed {
		t.Errorf("100%% alert should say reading is paused: %+v", alerts[2])
	}

	// A new month fires them again.
	clk.Set(time.Date(2026, 12, 2, 12, 0, 0, 0, time.UTC))
	say(60)
	m.alertWG.Wait()
	if got := len(ch.Alerts()); got != 4 {
		t.Fatalf("expected the 50%% alert again next month, got %d alerts", got)
	}
}

func TestMeter_RefusalAtLessThanFullFiresPausedAlert(t *testing.T) {
	ch := &fakeChannel{}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: newClock().Now})
	ctx := context.Background()
	if _, err := m.Synthesize(ctx, text(95), "th"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Synthesize(ctx, text(10), "th"); !errors.Is(err, ErrLimitReached) {
		t.Fatal("expected refusal")
	}
	m.alertWG.Wait()
	alerts := ch.Alerts()
	if len(alerts) != 3 || !strings.Contains(alerts[2].text, "หยุดอ่าน") || !strings.Contains(alerts[2].embed.Description, "95 / 100") {
		t.Fatalf("alerts: %v", alerts)
	}
}

func TestMeter_FailedAlertIsRetried(t *testing.T) {
	ch := &fakeChannel{alertFail: 1}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: newClock().Now})
	ctx := context.Background()
	if _, err := m.Synthesize(ctx, text(50), "th"); err != nil {
		t.Fatal(err)
	}
	m.alertWG.Wait()
	if len(ch.Alerts()) != 0 {
		t.Fatal("first alert should have failed")
	}
	if _, err := m.Synthesize(ctx, text(1), "th"); err != nil {
		t.Fatal(err)
	}
	m.alertWG.Wait()
	if a := ch.Alerts(); len(a) != 1 || !strings.Contains(a[0].text, "50%") {
		t.Fatalf("expected retried 50%% alert, got %v", a)
	}
}

func TestMeter_RestoresFromChannelAcrossRestart(t *testing.T) {
	clk := newClock()
	ch := &fakeChannel{}
	ctx := context.Background()

	m1 := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: clk.Now})
	m1.Start(ctx)
	if _, err := m1.Synthesize(ctx, text(60), "th"); err != nil {
		t.Fatal(err)
	}
	m1.Close() // final flush
	if len(ch.Alerts()) != 1 {
		t.Fatalf("alerts: %v", ch.Alerts())
	}

	m2 := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: clk.Now})
	m2.Start(ctx)
	if s := m2.Snapshot(); s.Used != 60 {
		t.Fatalf("restored used = %d, want 60", s.Used)
	}
	// 50% was already announced before the restart: only 80% fires now.
	if _, err := m2.Synthesize(ctx, text(25), "th"); err != nil {
		t.Fatal(err)
	}
	m2.Close()
	alerts := ch.Alerts()
	if len(alerts) != 2 || !strings.Contains(alerts[1].text, "80%") {
		t.Fatalf("alerts after restart: %v", alerts)
	}
}

func TestMeter_RestoreIgnoresOtherMonth(t *testing.T) {
	ch := &fakeChannel{status: FormatStatus(State{Month: "2026-10", Unit: UnitChars, Value: 99999, Alerted: []int{50, 80, 100}}, 100000)}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100000, Channel: ch, Now: newClock().Now})
	m.Start(context.Background())
	defer m.Close()
	if s := m.Snapshot(); s.Used != 0 || s.Paused {
		t.Fatalf("snapshot %+v, want a fresh month", s)
	}
}

func TestMeter_RestoreErrorOrGarbageStartsAtZero(t *testing.T) {
	for _, ch := range []*fakeChannel{
		{findErr: errors.New("403")},
		{status: "something unrelated"},
		{},
	} {
		m := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: newClock().Now})
		m.Start(context.Background())
		if s := m.Snapshot(); s.Used != 0 {
			t.Fatalf("used = %d, want 0", s.Used)
		}
		m.Close()
	}
}

func TestMeter_RestoredFullCounterIsPaused(t *testing.T) {
	ch := &fakeChannel{status: FormatStatus(State{Month: "2026-11", Unit: UnitChars, Value: 100, Alerted: []int{50, 80, 100}}, 100)}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: newClock().Now})
	m.Start(context.Background())
	defer m.Close()
	if !m.Snapshot().Paused {
		t.Fatal("expected paused")
	}
	if _, err := m.Synthesize(context.Background(), "x", "th"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("expected refusal, got %v", err)
	}
	m.alertWG.Wait()
	if a := ch.Alerts(); len(a) != 0 {
		t.Fatalf("thresholds were already alerted, got %v", a)
	}
}

func TestMeter_FlushOnlyWhenChanged(t *testing.T) {
	ch := &fakeChannel{}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: newClock().Now})
	ctx := context.Background()
	m.Flush(ctx)
	if len(ch.Saves()) != 0 {
		t.Fatal("nothing changed; should not save")
	}
	if _, err := m.Synthesize(ctx, text(3), "th"); err != nil {
		t.Fatal(err)
	}
	m.Flush(ctx)
	m.Flush(ctx)
	saves := ch.Saves()
	if len(saves) != 1 {
		t.Fatalf("expected 1 save, got %d", len(saves))
	}
	st, ok := ParseMarkerFromEmbed(saves[0])
	if !ok || st.Value != 3 || st.Month != "2026-11" {
		t.Fatalf("saved %+v -> %+v", saves[0], st)
	}
}

func TestMeter_RestoresAndMigratesLegacyContentMarker(t *testing.T) {
	legacy := FormatStatus(State{Month: "2026-11", Unit: UnitChars, Value: 42, Alerted: []int{50}}, 100)
	ch := &fakeChannel{status: legacy}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: newClock().Now})
	m.Start(context.Background())
	defer m.Close()

	if s := m.Snapshot(); s.Used != 42 {
		t.Fatalf("restored used = %d, want 42", s.Used)
	}
	// Migrated to new embed immediately on startup
	saves := ch.Saves()
	if len(saves) != 1 {
		t.Fatalf("expected legacy status to be migrated immediately, got %d saves", len(saves))
	}
	st, ok := ParseMarkerFromEmbed(saves[0])
	if !ok || st.Value != 42 || st.Month != "2026-11" {
		t.Fatalf("migrated marker: %+v (ok=%v)", st, ok)
	}
	if !strings.Contains(saves[0].Footer.Text, StatusFooterHuman) {
		t.Fatalf("migrated footer missing human text: %q", saves[0].Footer.Text)
	}
	if !strings.Contains(saves[0].Description, "42 / 100 ตัวอักษร") {
		t.Fatalf("migrated description: %q", saves[0].Description)
	}
}

func TestMeter_FailedSaveIsRetried(t *testing.T) {
	ch := &fakeChannel{saveErr: errors.New("nope")}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: newClock().Now})
	ctx := context.Background()
	if _, err := m.Synthesize(ctx, text(3), "th"); err != nil {
		t.Fatal(err)
	}
	m.Flush(ctx)
	ch.mu.Lock()
	ch.saveErr = nil
	ch.mu.Unlock()
	m.Flush(ctx)
	if len(ch.Saves()) != 1 {
		t.Fatal("expected the save to be retried")
	}
}

func TestMeter_PeriodicFlush(t *testing.T) {
	ch := &fakeChannel{}
	m := New(Config{Inner: &tts.Fake{}, Limit: 100, Channel: ch, Now: newClock().Now, FlushInterval: 5 * time.Millisecond})
	m.Start(context.Background())
	defer m.Close()
	if _, err := m.Synthesize(context.Background(), text(3), "th"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(ch.Saves()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("periodic flush never saved")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMeter_InMemoryWithoutChannel(t *testing.T) {
	m := New(Config{Inner: &tts.Fake{}, Limit: 10, Now: newClock().Now})
	m.Start(context.Background())
	if _, err := m.Synthesize(context.Background(), text(10), "th"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Synthesize(context.Background(), "x", "th"); !errors.Is(err, ErrLimitReached) {
		t.Fatal("cap should still apply in memory")
	}
	m.Close()
	m.Close() // idempotent
}

func TestMeter_ConcurrentUse(t *testing.T) {
	m := New(Config{Inner: &tts.Fake{}, Limit: 1000, Channel: &fakeChannel{}, Now: newClock().Now})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.Synthesize(context.Background(), text(10), "th")
			m.Snapshot()
		}()
	}
	wg.Wait()
	if s := m.Snapshot(); s.Used != 500 {
		t.Fatalf("used = %d, want 500", s.Used)
	}
	m.Close()
}
