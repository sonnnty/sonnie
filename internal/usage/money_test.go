package usage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
)

// oggPage builds one Ogg page holding the given single-packet payloads.
func oggPage(granule int64, packets ...[]byte) []byte {
	var seg, body []byte
	for _, p := range packets {
		seg = append(seg, byte(len(p)))
		body = append(body, p...)
	}
	page := make([]byte, 27+len(seg)+len(body))
	copy(page, "OggS")
	binary.LittleEndian.PutUint64(page[6:14], uint64(granule))
	page[26] = byte(len(seg))
	copy(page[27:], seg)
	copy(page[27+len(seg):], body)
	return page
}

// oggOpus returns a minimal Ogg/Opus stream that plays for d.
func oggOpus(d time.Duration) tts.Audio {
	const preSkip = 312
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8], head[9] = 1, 1
	binary.LittleEndian.PutUint16(head[10:12], preSkip)
	var data []byte
	data = append(data, oggPage(0, head)...)
	data = append(data, oggPage(0, append([]byte("OpusTags"), make([]byte, 8)...))...)
	data = append(data, oggPage(int64(d/time.Second)*48000+int64(d%time.Second)*48000/int64(time.Second)+preSkip, []byte{0xfc, 1})...)
	return tts.Audio{Format: tts.FormatOggOpus, Data: data}
}

// fakeGemini is an inner synthesizer that "generates" dur of audio per call.
type fakeGemini struct {
	mu    sync.Mutex
	dur   time.Duration
	err   error
	calls int
}

func (f *fakeGemini) Synthesize(ctx context.Context, text, lang string) (tts.Audio, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return tts.Audio{}, f.err
	}
	return oggOpus(f.dur), nil
}

func (f *fakeGemini) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func moneyMeter(inner tts.Synthesizer, budgetTHB, rate float64, ch Channel) *Meter {
	return New(Config{
		Inner: inner, Mode: ModeMoney, Limit: MicroTHB(budgetTHB), USDToTHB: rate,
		Prompt: strings.Repeat("p", 20), Channel: ch, Now: newClock().Now,
	})
}

func TestCostMicroTHB(t *testing.T) {
	tests := []struct {
		name   string
		audio  time.Duration
		tokens int64
		rate   float64
		want   int64
	}{
		// 4 s × 25 = 100 audio tokens × $10/1M = $0.001; 120 input tokens ×
		// $0.50/1M = $0.00006; total $0.00106 × 33 = ฿0.03498.
		{"audio and input", 4 * time.Second, 120, 33, 34980},
		{"audio only", 2500 * time.Millisecond, 0, 33, 20625}, // 62.5 tokens → $0.000625
		{"input only", 0, 1000, 33, 16500},                    // $0.0005
		{"1e6 audio seconds = 25M tokens = $250", 1000000 * time.Second, 0, 1, 250000000},
		{"nothing", 0, 0, 33, 0},
		{"rate scales linearly", 4 * time.Second, 120, 10, 10600},
	}
	for _, tt := range tests {
		if got := CostMicroTHB(tt.audio, tt.tokens, tt.rate); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestEstimateInputTokens_CountsRunesOfTextAndPrompt(t *testing.T) {
	// "สวัสดี" is 6 runes (18 bytes).
	if got := EstimateInputTokens("สวัสดี", "พูดเบา"); got != 12 {
		t.Fatalf("got %d, want 12", got)
	}
	if got := EstimateInputTokens("abc", ""); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}

func TestFormatTHB(t *testing.T) {
	for micro, want := range map[int64]string{
		0: "฿0.00", 34980: "฿0.03", 35000: "฿0.04", 240000000: "฿240.00", 300000000: "฿300.00", 1234500000: "฿1,234.50",
	} {
		if got := FormatTHB(micro); got != want {
			t.Errorf("FormatTHB(%d) = %q, want %q", micro, got, want)
		}
	}
}

func TestMeterMoney_ChargesSuccessfulRequest(t *testing.T) {
	inner := &fakeGemini{dur: 4 * time.Second}
	m := moneyMeter(inner, 300, 33, nil)
	audio, err := m.Synthesize(context.Background(), strings.Repeat("ก", 100), "th")
	if err != nil {
		t.Fatal(err)
	}
	if audio.Format != tts.FormatOggOpus {
		t.Fatalf("audio not passed through: %+v", audio.Format)
	}
	// 100 runes of text + 20 runes of prompt = 120 tokens (see TestCostMicroTHB).
	s := m.Snapshot()
	if s.Unit != UnitTHBMicro || s.Used != 34980 || s.AudioMs != 4000 || s.Limit != 300000000 || s.Percent != 0 || s.Paused {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestMeterMoney_FailedRequestIsNotCharged(t *testing.T) {
	inner := &fakeGemini{err: errors.New("boom"), dur: time.Second}
	m := moneyMeter(inner, 300, 33, nil)
	if _, err := m.Synthesize(context.Background(), "hello", "th"); err == nil {
		t.Fatal("expected the inner error")
	}
	if s := m.Snapshot(); s.Used != 0 || s.AudioMs != 0 {
		t.Fatalf("snapshot %+v, want nothing charged", s)
	}
}

func TestMeterMoney_CancelledContextIsNotCounted(t *testing.T) {
	inner := &fakeGemini{dur: time.Second}
	m := moneyMeter(inner, 300, 33, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Synthesize(ctx, "hello", "th"); err == nil || inner.Calls() != 0 {
		t.Fatalf("err = %v, calls = %d", err, inner.Calls())
	}
}

func TestMeterMoney_RefusesOnceBudgetIsReached(t *testing.T) {
	ch := &fakeChannel{}
	inner := &fakeGemini{dur: 4 * time.Second}
	// Budget ฿0.05; each 100-rune message costs ฿0.03498.
	m := moneyMeter(inner, 0.05, 33, ch)
	ctx := context.Background()
	msg := strings.Repeat("ก", 100)

	if _, err := m.Synthesize(ctx, msg, "th"); err != nil { // ฿0.035 < ฿0.05: allowed
		t.Fatal(err)
	}
	// Spend so far is below budget, so this one runs even though it will
	// overshoot (we cannot know the cost in advance).
	if _, err := m.Synthesize(ctx, msg, "th"); err != nil {
		t.Fatalf("a request that starts below the budget must run: %v", err)
	}
	if s := m.Snapshot(); s.Used != 69960 || !s.Paused || s.Percent != 100 {
		t.Fatalf("snapshot %+v, want 69960 µTHB, paused, 100%%", s)
	}

	_, err := m.Synthesize(ctx, msg, "th")
	var le *LimitError
	if !errors.Is(err, ErrLimitReached) || !errors.As(err, &le) {
		t.Fatalf("expected *LimitError, got %v", err)
	}
	if le.Unit != UnitTHBMicro || le.Used != 69960 || le.Limit != 50000 || !strings.Contains(le.Error(), "฿0.07/฿0.05") {
		t.Fatalf("limit error %+v: %v", le, le)
	}
	if inner.Calls() != 2 {
		t.Fatalf("inner called %d times, want 2 (refused request must not reach Google)", inner.Calls())
	}

	m.alertWG.Wait()
	m.Close()
	alerts := ch.Alerts()
	if len(alerts) != 3 || !strings.Contains(alerts[2].text, "🛑") || !strings.Contains(alerts[2].embed.Description, "฿0.07 / ฿0.05") {
		t.Fatalf("alerts: %v", alerts)
	}
}

func TestMeterMoney_ExactBudgetRefuses(t *testing.T) {
	inner := &fakeGemini{dur: 4 * time.Second}
	// ฿0.03498 is exactly one message.
	m := New(Config{Inner: inner, Mode: ModeMoney, Limit: 34980, USDToTHB: 33, Prompt: strings.Repeat("p", 20), Now: newClock().Now})
	if _, err := m.Synthesize(context.Background(), strings.Repeat("ก", 100), "th"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Synthesize(context.Background(), "x", "th"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("spend == budget must refuse, got %v", err)
	}
}

func TestMeterMoney_NoBudgetCountsWithoutCap(t *testing.T) {
	inner := &fakeGemini{dur: 10 * time.Second}
	m := New(Config{Inner: inner, Mode: ModeMoney, USDToTHB: 33, Now: newClock().Now})
	for i := 0; i < 3; i++ {
		if _, err := m.Synthesize(context.Background(), "", "th"); err != nil {
			t.Fatal(err)
		}
	}
	if s := m.Snapshot(); s.Used != 3*82500 || s.Percent != 0 || s.Paused || s.AudioMs != 30000 {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestMeterMoney_AlertsInBaht(t *testing.T) {
	ch := &fakeChannel{}
	inner := &fakeGemini{}
	// With a rate of 1, micro-baht == micro-USD == 250 per audio second.
	m := moneyMeter(inner, 300, 1, ch)
	m.cfg.Prompt = "" // keep the arithmetic to audio only; text is empty below
	ctx := context.Background()

	inner.dur = 480000 * time.Second // ฿120
	if _, err := m.Synthesize(ctx, "", "th"); err != nil {
		t.Fatal(err)
	}
	inner.dur = 480000 * time.Second // ฿240 total = 80%: crosses 50 and 80
	if _, err := m.Synthesize(ctx, "", "th"); err != nil {
		t.Fatal(err)
	}
	inner.dur = 240000 * time.Second // ฿300 total = 100%
	if _, err := m.Synthesize(ctx, "", "th"); err != nil {
		t.Fatal(err)
	}
	m.Close()

	got := ch.Alerts()
	if len(got) != 3 {
		t.Fatalf("alerts: %v", got)
	}
	if !strings.Contains(got[0].text, "50%") || !strings.Contains(got[0].embed.Description, "฿240.00 / ฿300.00") || got[0].embed.Color != ColorOrange { // posted at 80%: colour follows the bar
		t.Errorf("alert 0: %+v", got[0])
	}
	if !strings.Contains(got[1].text, "80%") || !strings.Contains(got[1].embed.Description, "฿240.00 / ฿300.00") || got[1].embed.Color != ColorOrange {
		t.Errorf("alert 1: %+v", got[1])
	}
	if !strings.Contains(got[2].text, "🛑") || !strings.Contains(got[2].embed.Description, "฿300.00 / ฿300.00") || got[2].embed.Color != ColorRed {
		t.Errorf("alert 2: %+v", got[2])
	}
}

func TestMeterMoney_FallbackWhenAudioIsNotOgg(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	// 6000 bytes at the 6 kbit/s floor is at most 8 s of audio.
	inner := synthFunc(func() (tts.Audio, error) {
		return tts.Audio{Format: tts.FormatOggOpus, Data: make([]byte, 6000)}, nil
	})
	m := New(Config{Inner: inner, Mode: ModeMoney, USDToTHB: 1, Now: newClock().Now})
	if _, err := m.Synthesize(context.Background(), "", "th"); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.AudioMs != 8000 || s.Used != 8*250 {
		t.Fatalf("snapshot %+v", s)
	}
	if !strings.Contains(buf.String(), "upper bound") {
		t.Fatalf("expected a log line, got %q", buf.String())
	}
}

type synthFunc func() (tts.Audio, error)

func (f synthFunc) Synthesize(context.Context, string, string) (tts.Audio, error) { return f() }

func TestMeterMoney_PersistsMoneyMarker(t *testing.T) {
	ch := &fakeChannel{}
	m := moneyMeter(&fakeGemini{dur: 4 * time.Second}, 300, 33, ch)
	if _, err := m.Synthesize(context.Background(), strings.Repeat("ก", 100), "th"); err != nil {
		t.Fatal(err)
	}
	m.Close()
	saves := ch.Saves()
	if len(saves) != 1 {
		t.Fatalf("saves: %v", saves)
	}
	st, ok := ParseMarkerFromEmbed(saves[0])
	if !ok || st.Unit != UnitTHBMicro || st.Value != 34980 || st.AudioMs != 4000 || st.Month != "2026-11" {
		t.Fatalf("saved %+v -> %+v", saves[0], st)
	}
	if !strings.Contains(saves[0].Description, "฿0.03 / ฿300.00") || !strings.Contains(saves[0].Fields[0].Value, "4.0 วินาที") {
		t.Fatalf("status summary: %+v", saves[0])
	}
}

func startWithStatus(t *testing.T, status string, mode Mode) (*Meter, string) {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)
	m := New(Config{
		Inner: &fakeGemini{dur: time.Second}, Mode: mode, Limit: 300000000, USDToTHB: 33,
		Channel: &fakeChannel{status: status}, Now: newClock().Now,
	})
	m.Start(context.Background())
	t.Cleanup(m.Close)
	return m, buf.String()
}

func TestMeterMoney_RestoreV2SameUnit(t *testing.T) {
	status := FormatStatus(State{Month: "2026-11", Unit: UnitTHBMicro, Value: 240000000, AudioMs: 90500, Alerted: []int{50, 80}}, 300000000)
	m, logs := startWithStatus(t, status, ModeMoney)
	s := m.Snapshot()
	if s.Used != 240000000 || s.AudioMs != 90500 || s.Percent != 80 {
		t.Fatalf("snapshot %+v", s)
	}
	if !strings.Contains(logs, "restored") {
		t.Fatalf("logs: %q", logs)
	}
}

func TestMeterMoney_RestoreV1StartsAtZero(t *testing.T) {
	m, logs := startWithStatus(t, "sonnie-tts-usage v1 month=2026-11 chars=900000 alerted=50,80", ModeMoney)
	if s := m.Snapshot(); s.Used != 0 || s.AudioMs != 0 || s.Paused {
		t.Fatalf("snapshot %+v, want 0", s)
	}
	if !strings.Contains(logs, "starting from 0") || !strings.Contains(logs, `"chars"`) {
		t.Fatalf("expected a log line about the unit, got %q", logs)
	}
	// Thresholds restored from the old unit must not suppress new alerts.
	if m.alerted[50] || m.alerted[80] {
		t.Fatal("alerted thresholds leaked from the v1 marker")
	}
}

func TestMeterMoney_RestoreV2DifferentUnitStartsAtZero(t *testing.T) {
	status := FormatStatus(State{Month: "2026-11", Unit: UnitChars, Value: 900000, Alerted: []int{50, 80}}, 1000000)
	m, logs := startWithStatus(t, status, ModeMoney)
	if s := m.Snapshot(); s.Used != 0 {
		t.Fatalf("snapshot %+v, want 0", s)
	}
	if !strings.Contains(logs, "starting from 0") {
		t.Fatalf("logs: %q", logs)
	}
}

func TestMeterChars_RestoreMoneyMarkerStartsAtZero(t *testing.T) {
	status := FormatStatus(State{Month: "2026-11", Unit: UnitTHBMicro, Value: 240000000, Alerted: []int{50, 80}}, 300000000)
	m, logs := startWithStatus(t, status, ModeChars)
	if s := m.Snapshot(); s.Used != 0 || s.Unit != UnitChars {
		t.Fatalf("snapshot %+v, want 0 chars", s)
	}
	if !strings.Contains(logs, "starting from 0") {
		t.Fatalf("logs: %q", logs)
	}
}

func TestMeterChars_RestoresV1Marker(t *testing.T) {
	m, _ := startWithStatus(t, "sonnie-tts-usage v1 month=2026-11 chars=250 alerted=50", ModeChars)
	if s := m.Snapshot(); s.Used != 250 {
		t.Fatalf("snapshot %+v, want the v1 count restored", s)
	}
}

func TestMeterMoney_MonthRolloverResetsAudio(t *testing.T) {
	clk := newClock()
	m := New(Config{Inner: &fakeGemini{dur: time.Second}, Mode: ModeMoney, USDToTHB: 33, Limit: 1000, Now: clk.Now})
	if _, err := m.Synthesize(context.Background(), "", "th"); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); !s.Paused || s.AudioMs != 1000 {
		t.Fatalf("snapshot %+v", s)
	}
	clk.Set(time.Date(2026, 12, 2, 12, 0, 0, 0, time.UTC))
	if s := m.Snapshot(); s.Used != 0 || s.AudioMs != 0 || s.Paused || s.Month != "2026-12" {
		t.Fatalf("snapshot %+v, want a fresh month", s)
	}
}
