package panels

import (
	"context"
	"runtime"
	"time"

	"github.com/Sonty15/SONNIE/internal/buildinfo"
	"github.com/Sonty15/SONNIE/internal/dashboard"
)

// BotInfo is the gateway side of the Bot panel.
type BotInfo struct {
	Online  bool
	Latency time.Duration
	// User is the bot's own username.
	User   string
	Guilds []GuildInfo
}

// GuildInfo is one guild the bot is in.
type GuildInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Members int    `json:"members,omitempty"`
}

// BotData is the Bot panel's JSON.
type BotData struct {
	Online        bool        `json:"online"`
	LatencyMs     int64       `json:"latency_ms"`
	User          string      `json:"user"`
	Version       string      `json:"version"`
	StartedAt     time.Time   `json:"started_at"`
	UptimeSeconds int64       `json:"uptime_seconds"`
	GoVersion     string      `json:"go_version"`
	Goroutines    int         `json:"goroutines"`
	HeapAlloc     uint64      `json:"heap_alloc_bytes"`
	Sys           uint64      `json:"sys_bytes"`
	NumGC         uint32      `json:"num_gc"`
	Guilds        []GuildInfo `json:"guilds"`
}

// Bot is the "is it alive" panel: gateway, uptime, version, guilds and Go
// runtime figures.
func Bot(started time.Time, info func() BotInfo, now func() time.Time) dashboard.Panel {
	if now == nil {
		now = time.Now
	}
	return dashboard.Panel{ID: "bot", Title: "บอท", Snapshot: func(context.Context) (any, error) {
		bi := info()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		d := BotData{
			Online:        bi.Online,
			User:          bi.User,
			Version:       buildinfo.String(),
			StartedAt:     started.UTC(),
			UptimeSeconds: int64(now().Sub(started) / time.Second),
			GoVersion:     runtime.Version(),
			Goroutines:    runtime.NumGoroutine(),
			HeapAlloc:     ms.HeapAlloc,
			Sys:           ms.Sys,
			NumGC:         ms.NumGC,
			Guilds:        bi.Guilds,
		}
		if bi.Online && bi.Latency > 0 {
			d.LatencyMs = bi.Latency.Milliseconds()
		}
		if d.Guilds == nil {
			d.Guilds = []GuildInfo{}
		}
		return d, nil
	}}
}
