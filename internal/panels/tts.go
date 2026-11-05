package panels

import (
	"context"
	"fmt"
	"time"

	"github.com/Sonty15/SONNIE/internal/dashboard"
	"github.com/Sonty15/SONNIE/internal/readaloud"
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
)

// SessionSource is the read-aloud manager.
type SessionSource interface {
	Status() []readaloud.SessionStatus
}

// SessionsData is the TTS sessions panel's JSON.
type SessionsData struct {
	Enabled  bool          `json:"enabled"`
	Sessions []SessionData `json:"sessions"`
}

// SessionData is one guild's session.
type SessionData struct {
	Guild        Ref               `json:"guild"`
	VoiceChannel *Ref              `json:"voice_channel"`
	TextChannels []*Ref            `json:"text_channels"`
	Participants []ParticipantData `json:"participants"`
	StartedAt    time.Time         `json:"started_at"`
	Queue        int               `json:"queue"`
	Read         int               `json:"read"`
	Deleted      int               `json:"deleted"`
}

// ParticipantData is one participant.
type ParticipantData struct {
	User    Ref    `json:"user"`
	Channel *Ref   `json:"channel"`
	Voice   string `json:"voice"`
}

// TTSSessions shows the read-aloud sessions. src is nil when the feature is
// off.
func TTSSessions(src SessionSource, names Names) dashboard.Panel {
	names = orNoNames(names)
	return dashboard.Panel{ID: "tts_sessions", Title: "เซสชันอ่านแชท", Snapshot: func(context.Context) (any, error) {
		d := SessionsData{Enabled: src != nil, Sessions: []SessionData{}}
		if src == nil {
			return d, nil
		}
		for _, st := range src.Status() {
			sd := SessionData{
				Guild:        guildRef(names, st.GuildID),
				VoiceChannel: channelRef(names, st.VoiceChannelID),
				TextChannels: []*Ref{},
				Participants: []ParticipantData{},
				StartedAt:    st.Started.UTC(),
				Queue:        st.Queue,
				Read:         st.Read,
				Deleted:      st.Deleted,
			}
			for _, c := range st.TextChannels {
				sd.TextChannels = append(sd.TextChannels, channelRef(names, c))
			}
			for _, p := range st.Participants {
				sd.Participants = append(sd.Participants, ParticipantData{
					User:    memberRef(names, st.GuildID, p.UserID),
					Channel: channelRef(names, p.ChannelID),
					Voice:   p.Voice,
				})
			}
			d.Sessions = append(d.Sessions, sd)
		}
		return d, nil
	}}
}

// UsageSource is the usage meter.
type UsageSource interface {
	Snapshot() usage.Snapshot
}

// UsageData is the TTS usage panel's JSON.
type UsageData struct {
	Enabled    bool          `json:"enabled"`
	Month      string        `json:"month,omitempty"`
	MonthLabel string        `json:"month_label,omitempty"`
	Unit       string        `json:"unit,omitempty"`
	Used       int64         `json:"used"`
	Limit      int64         `json:"limit"`
	UsedText   string        `json:"used_text,omitempty"`
	LimitText  string        `json:"limit_text,omitempty"`
	Percent    int           `json:"percent"`
	Color      string        `json:"color,omitempty"`
	Paused     bool          `json:"paused"`
	AudioMs    int64         `json:"audio_ms"`
	AudioText  string        `json:"audio_text,omitempty"`
	Alerted    []int         `json:"alerted"`
	NextReset  *time.Time    `json:"next_reset,omitempty"`
	Latencies  []LatencyData `json:"latencies"`
}

// LatencyData is one utterance's time to first audio.
type LatencyData struct {
	At           time.Time `json:"at"`
	Path         string    `json:"path"`
	FirstAudioMs int64     `json:"first_audio_ms"`
	TotalMs      int64     `json:"total_ms"`
}

// TTSUsage shows the monthly Google TTS meter, coloured like the Discord
// usage embed, and the recent latencies. src is nil when nothing is
// metered; timings may be nil.
func TTSUsage(src UsageSource, timings func() []tts.Timing, now func() time.Time) dashboard.Panel {
	if now == nil {
		now = time.Now
	}
	return dashboard.Panel{ID: "tts_usage", Title: "งบ Google TTS", Snapshot: func(context.Context) (any, error) {
		d := UsageData{Enabled: src != nil, Alerted: []int{}, Latencies: []LatencyData{}}
		if timings != nil {
			for _, t := range timings() {
				d.Latencies = append(d.Latencies, LatencyData{At: t.At.UTC(), Path: t.Path, FirstAudioMs: t.FirstAudio.Milliseconds(), TotalMs: t.Total.Milliseconds()})
			}
		}
		if src == nil {
			return d, nil
		}
		s := src.Snapshot()
		d.Month, d.Unit, d.Used, d.Limit, d.Percent, d.Paused, d.AudioMs = s.Month, s.Unit, s.Used, s.Limit, s.Percent, s.Paused, s.AudioMs
		d.Color = fmt.Sprintf("#%06X", usage.EmbedColor(s.Percent))
		d.MonthLabel = monthLabel(s.Month)
		d.AudioText = usage.FormatAudioDuration(s.AudioMs)
		if s.Unit == usage.UnitTHBMicro {
			d.UsedText, d.LimitText = usage.FormatTHB(s.Used), usage.FormatTHB(s.Limit)
		} else {
			d.UsedText, d.LimitText = usage.FormatCount(s.Used)+" ตัวอักษร", usage.FormatCount(s.Limit)+" ตัวอักษร"
		}
		if s.Alerted != nil {
			d.Alerted = s.Alerted
		}
		reset := time.Unix(usage.NextResetUnix(s.Month, now()), 0).UTC()
		d.NextReset = &reset
		return d, nil
	}}
}

func monthLabel(month string) string {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return month
	}
	return fmt.Sprintf("%s %d", usage.ThaiMonth(t.Month()), t.Year())
}
