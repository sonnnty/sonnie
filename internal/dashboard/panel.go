package dashboard

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Panel is one feature's box on the dashboard. Every feature registers its
// own (see internal/bot/dashboard.go): Snapshot returns a JSON-encodable,
// read-only view of the feature, and the page renders it with the renderer
// registered under ID in static/app.js, or as plain data without one.
type Panel struct {
	// ID is stable and URL-safe, e.g. "tts_usage".
	ID string
	// Title is shown above the panel (Thai).
	Title string
	// Snapshot is called on every refresh (every few seconds per open
	// page), so it must be cheap and must not call Discord.
	Snapshot func(ctx context.Context) (any, error)
}

// panelTimeout bounds one panel's Snapshot.
const panelTimeout = 3 * time.Second

// PanelResult is one panel in the /api/panels answer.
type PanelResult struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// snapshotAll runs every panel's Snapshot concurrently. A failing, slow or
// panicking panel reports an error in its own box and does not hold up the
// others.
func snapshotAll(ctx context.Context, panels []Panel) []PanelResult {
	out := make([]PanelResult, len(panels))
	var wg sync.WaitGroup
	for i, p := range panels {
		out[i] = PanelResult{ID: p.ID, Title: p.Title}
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := runPanel(ctx, p)
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].Data = data
		}()
	}
	wg.Wait()
	return out
}

func runPanel(ctx context.Context, p Panel) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, panelTimeout)
	defer cancel()
	type result struct {
		data any
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("panel %s failed: %v", p.ID, r)}
			}
		}()
		data, err := p.Snapshot(ctx)
		ch <- result{data, err}
	}()
	select {
	case r := <-ch:
		return r.data, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("panel %s timed out", p.ID)
	}
}
