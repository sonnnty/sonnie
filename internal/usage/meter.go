// Package usage meters Google Cloud Text-to-Speech, enforces a monthly cap,
// raises threshold alerts and persists the counter.
//
// A Meter counts in one unit, chosen by Config.Mode: characters sent (Chirp
// and Neural2 voices, which are priced per character) or money spent
// (Gemini-TTS, which is priced per token of text in and audio out).
//
// The deployment has no persistent volume, so the counter is kept in Discord:
// one status message in the alert channel holds a machine-readable marker
// (see FormatMarker) that the Meter restores on startup and edits as usage
// grows. Discord itself is behind the Channel interface.
package usage

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
	_ "time/tzdata" // the distroless image has no zoneinfo

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/tts"
)

const (
	// billingZone is Google Cloud's billing time zone.
	billingZone = "America/Los_Angeles"

	// DefaultFlushInterval is how often a changed counter is written back.
	DefaultFlushInterval = time.Minute

	channelTimeout = 15 * time.Second
)

// Thresholds are the usage percentages that trigger an alert, once each per
// month.
var Thresholds = []int{50, 80, 100}

// ErrLimitReached matches every *LimitError with errors.Is.
var ErrLimitReached = errors.New("usage: monthly TTS limit reached")

// LimitError is returned by Meter.Synthesize when the monthly limit is used
// up. The text is not sent to Google. Used and Limit are in Unit.
type LimitError struct {
	Month string
	Unit  string
	Used  int64
	Limit int64
}

func (e *LimitError) Error() string {
	if e.Unit == UnitTHBMicro {
		return fmt.Sprintf("usage: monthly TTS budget reached (%s/%s in %s)", FormatTHB(e.Used), FormatTHB(e.Limit), e.Month)
	}
	return fmt.Sprintf("usage: monthly TTS character limit reached (%d/%d chars in %s)", e.Used, e.Limit, e.Month)
}

func (e *LimitError) Is(target error) bool { return target == ErrLimitReached }

// Channel is the Discord side of the meter: the alert channel.
type Channel interface {
	// FindStatus returns the newest status marker the bot wrote in the
	// channel, or "" if there is none. isLegacy is true when found in an
	// old plain-text message so the meter can migrate it to the embed format.
	FindStatus(ctx context.Context) (marker string, isLegacy bool, err error)
	// SaveStatus creates the status message with an embed, or edits it if it exists.
	SaveStatus(ctx context.Context, embed *discordgo.MessageEmbed) error
	// Alert posts a new message in the channel with notification preview text and an embed.
	Alert(ctx context.Context, text string, embed *discordgo.MessageEmbed) error
}

// Mode selects what a Meter counts.
type Mode int

const (
	// ModeChars counts characters sent and refuses a request that would
	// exceed the cap. It is the default.
	ModeChars Mode = iota
	// ModeMoney prices every successful request (Gemini-TTS) and refuses
	// once the spend so far has reached the budget. The cost of a request is
	// unknown until the audio comes back, so the last message may overshoot.
	ModeMoney
)

// Unit is the persisted unit of the mode.
func (m Mode) Unit() string {
	if m == ModeMoney {
		return UnitTHBMicro
	}
	return UnitChars
}

// Config configures a Meter.
type Config struct {
	// Inner is the real synthesizer.
	Inner tts.Synthesizer
	// Mode selects characters (default) or money.
	Mode Mode
	// Limit is the monthly cap, in characters or (ModeMoney) micro-baht; 0
	// means no cap.
	Limit int64
	// USDToTHB converts the USD price to baht; required for ModeMoney.
	USDToTHB float64
	// Prompt is the style prompt the inner synthesizer sends; it is billed
	// as input tokens in ModeMoney.
	Prompt string
	// Channel persists the counter and receives alerts. If nil the meter
	// is in-memory only.
	Channel Channel
	// FlushInterval defaults to DefaultFlushInterval.
	FlushInterval time.Duration
	// Now defaults to time.Now.
	Now func() time.Time
}

// Snapshot is a point-in-time view of the meter.
type Snapshot struct {
	Month   string
	Unit    string // UnitChars or UnitTHBMicro: what Used and Limit are in
	Used    int64
	Limit   int64 // 0 means no cap
	Percent int   // 0 when there is no cap, otherwise rounded down and at most 100
	Paused  bool  // the cap is reached and reading is paused until next month
	AudioMs int64 // audio generated this month; only metered in ModeMoney
}

// Meter wraps a tts.Synthesizer, counting what it costs: characters or money,
// depending on Config.Mode. It is safe for concurrent use.
type Meter struct {
	cfg Config
	loc *time.Location

	mu       sync.Mutex
	month    string
	used     int64
	audioMs  int64
	alerted  map[int]bool
	refused  bool   // a request was refused this month
	version  uint64 // bumped on every change worth persisting
	saved    uint64 // version last written to the channel
	closed   bool
	stopFlag chan struct{}

	flushMu sync.Mutex
	wg      sync.WaitGroup // the flush loop
	alertWG sync.WaitGroup // the alert worker

	alertQ       []alert // guarded by mu
	alertRunning bool    // guarded by mu
}

// New creates a Meter. Call Start to restore the persisted count and begin
// periodic saves.
func New(cfg Config) *Meter {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	loc, err := time.LoadLocation(billingZone)
	if err != nil {
		// Unreachable with time/tzdata embedded; fall back to PST.
		loc = time.FixedZone("PST", -8*60*60)
	}
	m := &Meter{cfg: cfg, loc: loc, alerted: map[int]bool{}, stopFlag: make(chan struct{})}
	m.month = m.currentMonth()
	return m
}

func (m *Meter) currentMonth() string {
	return m.cfg.Now().In(m.loc).Format("2006-01")
}

// Start restores the persisted counter (if the meter has a Channel) and starts
// the background saver. A failed restore is logged and the meter starts from
// zero rather than leaving the bot unable to run.
func (m *Meter) Start(ctx context.Context) {
	if m.cfg.Channel == nil {
		log.Println("usage: TTS_ALERT_CHANNEL_ID is not set; the monthly TTS usage count is kept in memory only and resets when the bot restarts")
		return
	}

	content, isLegacy, err := m.cfg.Channel.FindStatus(ctx)
	switch {
	case err != nil:
		log.Printf("usage: could not restore the usage count from the alert channel (starting from 0): %v", err)
	case content == "":
		log.Println("usage: no usage status message found in the alert channel; starting from 0")
	default:
		if st, ok := ParseMarker(content); ok {
			m.restore(st, isLegacy)
			if isLegacy {
				m.Flush(ctx)
			}
		} else {
			log.Println("usage: status message has no valid marker; starting from 0")
		}
	}

	m.wg.Add(1)
	go m.flushLoop()
}

func (m *Meter) restore(st State, isLegacy bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rolloverLocked()
	if st.Month != m.month {
		log.Printf("usage: stored count is for %s, current month is %s; starting from 0", st.Month, m.month)
		return
	}
	if st.Unit != m.cfg.Mode.Unit() {
		log.Printf("usage: stored count is in %q but this process meters %q (voice model changed or legacy v1 marker); starting from 0", st.Unit, m.cfg.Mode.Unit())
		return
	}
	m.used = st.Value
	m.audioMs = st.AudioMs
	m.alerted = map[int]bool{}
	for _, a := range st.Alerted {
		m.alerted[a] = true
	}
	if isLegacy {
		m.version++
	} else {
		// The restored state is already what is stored; nothing to flush.
		m.saved = m.version
	}
	log.Printf("usage: restored %s used in %s", formatAmount(m.cfg.Mode, m.used), m.month)
}

// rolloverLocked resets the counter when the billing month has changed.
func (m *Meter) rolloverLocked() {
	if cur := m.currentMonth(); cur != m.month {
		m.month = cur
		m.used = 0
		m.audioMs = 0
		m.alerted = map[int]bool{}
		m.refused = false
		m.version++
	}
}

// Synthesize implements tts.Synthesizer. See synthesizeChars and
// synthesizeMoney for the two metering modes.
func (m *Meter) Synthesize(ctx context.Context, text, lang string) (tts.Audio, error) {
	if err := ctx.Err(); err != nil {
		return tts.Audio{}, err
	}
	if m.cfg.Mode == ModeMoney {
		return m.synthesizeMoney(ctx, text, lang)
	}
	return m.synthesizeChars(ctx, text, lang)
}

// synthesizeChars counts the runes of text before calling the inner
// synthesizer, because a failed request may still be billed, and refuses with
// a *LimitError when the count would exceed the limit.
func (m *Meter) synthesizeChars(ctx context.Context, text, lang string) (tts.Audio, error) {
	n := int64(runeCount(text))

	m.mu.Lock()
	m.rolloverLocked()
	if m.cfg.Limit > 0 && m.used+n > m.cfg.Limit {
		limitErr := m.refuseLocked()
		alerts := m.claimAlertsLocked(100)
		m.mu.Unlock()
		m.sendAlerts(alerts)
		return tts.Audio{}, limitErr
	}
	m.used += n
	m.version++
	alerts := m.claimAlertsLocked(m.percentLocked())
	m.mu.Unlock()

	m.sendAlerts(alerts)
	return m.cfg.Inner.Synthesize(ctx, text, lang)
}

// synthesizeMoney refuses when the spend so far has already reached the
// budget, otherwise calls the inner synthesizer and charges the cost of the
// audio it returned. Failed requests are not charged.
func (m *Meter) synthesizeMoney(ctx context.Context, text, lang string) (tts.Audio, error) {
	m.mu.Lock()
	m.rolloverLocked()
	if m.cfg.Limit > 0 && m.used >= m.cfg.Limit {
		limitErr := m.refuseLocked()
		alerts := m.claimAlertsLocked(100)
		m.mu.Unlock()
		m.sendAlerts(alerts)
		return tts.Audio{}, limitErr
	}
	m.mu.Unlock()

	audio, err := m.cfg.Inner.Synthesize(ctx, text, lang)
	if err != nil {
		return audio, err
	}

	dur := audioDuration(audio)
	cost := CostMicroTHB(dur, EstimateInputTokens(text, m.cfg.Prompt), m.cfg.USDToTHB)

	m.mu.Lock()
	m.rolloverLocked()
	m.used += cost
	m.audioMs += dur.Milliseconds()
	m.version++
	alerts := m.claimAlertsLocked(m.percentLocked())
	m.mu.Unlock()
	m.sendAlerts(alerts)
	return audio, nil
}

// refuseLocked records a refusal and builds its error.
func (m *Meter) refuseLocked() *LimitError {
	m.refused = true
	// Usage may never reach exactly 100% (the next message just does not
	// fit, or the last one overshot), so the refusal itself counts as
	// crossing it; callers claim the 100% alert.
	return &LimitError{Month: m.month, Unit: m.cfg.Mode.Unit(), Used: m.used, Limit: m.cfg.Limit}
}

// audioDuration reads the length of the returned audio from the Ogg
// granule position. If that fails (a malformed stream the player would also
// reject) it falls back to an upper bound: no speech Opus stream is below
// minOpusBitrate, so size/bitrate never under-bills.
func audioDuration(a tts.Audio) time.Duration {
	if a.Format == tts.FormatOggOpus {
		if d, err := tts.OggOpusDuration(a.Data); err == nil {
			return d
		}
	}
	d := time.Duration(len(a.Data)) * 8 * time.Second / minOpusBitrate
	log.Printf("usage: could not read the audio duration; billing an upper bound of %v from %d bytes", d, len(a.Data))
	return d
}

func (m *Meter) percentLocked() int {
	if m.cfg.Limit <= 0 {
		return 0
	}
	if m.used >= m.cfg.Limit {
		return 100
	}
	return int(m.used * 100 / m.cfg.Limit)
}

// alert is one threshold announcement.
type alert struct {
	threshold int
	text      string
	embed     *discordgo.MessageEmbed
}

// claimAlertsLocked marks every not-yet-announced threshold <= percent as
// alerted and returns the messages to post, so each fires once per month.
func (m *Meter) claimAlertsLocked(percent int) []alert {
	if m.cfg.Limit <= 0 {
		return nil
	}
	var msgs []alert
	for _, th := range Thresholds {
		if th > percent || m.alerted[th] {
			continue
		}
		m.alerted[th] = true
		m.version++
		msgs = append(msgs, alert{
			threshold: th,
			text:      AlertPreviewText(m.cfg.Mode, th),
			embed:     FormatAlertEmbed(m.cfg.Mode, th, m.used, m.cfg.Limit, m.month),
		})
	}
	return msgs
}

func alertText(mode Mode, threshold int, used, limit int64) string {
	if mode == ModeMoney {
		amounts := fmt.Sprintf("(%s / %s)", FormatTHB(used), FormatTHB(limit))
		if threshold >= 100 {
			return fmt.Sprintf("🛑 Google TTS ใช้ครบงบเดือนนี้แล้ว %s — หยุดอ่านออกเสียงจนกว่าจะถึงเดือนหน้า", amounts)
		}
		return fmt.Sprintf("⚠️ Google TTS ใช้ไป %d%% ของงบเดือนนี้ %s", threshold, amounts)
	}
	counts := fmt.Sprintf("(%s / %s ตัวอักษร)", formatInt(used), formatInt(limit))
	if threshold >= 100 {
		return fmt.Sprintf("🛑 Google TTS ใช้ครบโควตาเดือนนี้แล้ว %s — หยุดอ่านออกเสียงจนกว่าจะถึงเดือนหน้า", counts)
	}
	return fmt.Sprintf("⚠️ Google TTS ใช้ไป %d%% ของโควตาเดือนนี้ %s", threshold, counts)
}

// sendAlerts queues alerts for a background worker so a slow Discord call
// never delays speech and alerts are posted in the order they were raised. A
// failed alert is un-marked so the next request retries it.
func (m *Meter) sendAlerts(msgs []alert) {
	if len(msgs) == 0 || m.cfg.Channel == nil {
		return
	}
	m.mu.Lock()
	m.alertQ = append(m.alertQ, msgs...)
	start := !m.alertRunning
	m.alertRunning = true
	m.mu.Unlock()
	if start {
		m.alertWG.Add(1)
		go m.alertWorker()
	}
}

func (m *Meter) alertWorker() {
	defer m.alertWG.Done()
	for {
		m.mu.Lock()
		if len(m.alertQ) == 0 {
			m.alertRunning = false
			m.mu.Unlock()
			return
		}
		msg := m.alertQ[0]
		m.alertQ = m.alertQ[1:]
		m.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), channelTimeout)
		err := m.cfg.Channel.Alert(ctx, msg.text, msg.embed)
		cancel()
		if err != nil {
			log.Printf("usage: failed to post %d%% alert (will retry on next request): %v", msg.threshold, err)
			m.mu.Lock()
			delete(m.alerted, msg.threshold)
			m.version++
			m.mu.Unlock()
		}
	}
}

// Snapshot returns the current month's usage.
func (m *Meter) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rolloverLocked()
	return Snapshot{
		Month:   m.month,
		Unit:    m.cfg.Mode.Unit(),
		AudioMs: m.audioMs,
		Used:    m.used,
		Limit:   m.cfg.Limit,
		Percent: m.percentLocked(),
		Paused:  m.cfg.Limit > 0 && (m.refused || m.used >= m.cfg.Limit),
	}
}

// pendingSave returns the state to persist, or false if it is already saved.
func (m *Meter) pendingSave() (State, uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rolloverLocked()
	if m.version == m.saved {
		return State{}, 0, false
	}
	st := State{Month: m.month, Unit: m.cfg.Mode.Unit(), Value: m.used, AudioMs: m.audioMs}
	for _, th := range Thresholds {
		if m.alerted[th] {
			st.Alerted = append(st.Alerted, th)
		}
	}
	return st, m.version, true
}

func (m *Meter) flushLoop() {
	defer m.wg.Done()
	t := time.NewTicker(m.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.Flush(context.Background())
		case <-m.stopFlag:
			return
		}
	}
}

// Flush writes the counter to the alert channel if it changed since the last
// write.
func (m *Meter) Flush(ctx context.Context) {
	if m.cfg.Channel == nil {
		return
	}
	m.flushMu.Lock()
	defer m.flushMu.Unlock()

	st, version, dirty := m.pendingSave()
	if !dirty {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, channelTimeout)
	defer cancel()
	embed := FormatStatusEmbed(st, m.cfg.Limit, m.cfg.Now())
	if err := m.cfg.Channel.SaveStatus(ctx, embed); err != nil {
		log.Printf("usage: failed to save usage status: %v", err)
		return
	}
	m.mu.Lock()
	m.saved = version
	m.mu.Unlock()
}

// Close stops the background saver, writes the final count and waits for
// pending alerts.
func (m *Meter) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()

	close(m.stopFlag)
	m.wg.Wait()
	m.alertWG.Wait()
	m.Flush(context.Background())
}

// FormatStatus renders the full status message: a short Thai summary and the
// marker line in a code block.
func FormatStatus(st State, limit int64) string {
	mode := ModeChars
	if st.Unit == UnitTHBMicro {
		mode = ModeMoney
	}
	var summary string
	if mode == ModeMoney {
		summary = fmt.Sprintf("📊 Google TTS เดือน %s — ใช้ไป %s", st.Month, FormatTHB(st.Value))
		if limit > 0 {
			summary += " / " + FormatTHB(limit)
		}
		summary += fmt.Sprintf(" (เสียง %s วินาที)", FormatSeconds(st.AudioMs))
	} else {
		summary = fmt.Sprintf("📊 Google TTS เดือน %s — ใช้ไป %s ตัวอักษร", st.Month, formatInt(st.Value))
		if limit > 0 {
			summary = fmt.Sprintf("📊 Google TTS เดือน %s — ใช้ไป %s / %s ตัวอักษร", st.Month, formatInt(st.Value), formatInt(limit))
		}
	}
	return summary + "\n```\n" + FormatMarker(st) + "\n```"
}

func formatAmount(mode Mode, v int64) string {
	if mode == ModeMoney {
		return FormatTHB(v)
	}
	return formatInt(v) + " characters"
}
