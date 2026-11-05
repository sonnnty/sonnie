package panels

import (
	"context"

	"github.com/Sonty15/SONNIE/internal/dashboard"
	"github.com/Sonty15/SONNIE/internal/logring"
)

// LogSource is the in-memory log ring.
type LogSource interface {
	Lines() []logring.Line
}

// LogsData is the Recent log panel's JSON.
type LogsData struct {
	Lines []logring.Line `json:"lines"`
}

// Logs shows the last lines of the process log, oldest first. The page
// filters them.
func Logs(src LogSource) dashboard.Panel {
	return dashboard.Panel{ID: "logs", Title: "Log ล่าสุด", Snapshot: func(context.Context) (any, error) {
		lines := src.Lines()
		if lines == nil {
			lines = []logring.Line{}
		}
		return LogsData{Lines: lines}, nil
	}}
}
