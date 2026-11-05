package panels

import (
	"context"
	"time"

	"github.com/Sonty15/SONNIE/internal/dashboard"
	"github.com/Sonty15/SONNIE/internal/webhook"
)

// WebhookSource is the webhook handler.
type WebhookSource interface {
	Status() webhook.Status
}

// WebhooksData is the Webhooks panel's JSON. It never holds a token.
type WebhooksData struct {
	RateLimit    int            `json:"rate_limit_per_minute"`
	MaxBodyBytes int            `json:"max_body_bytes"`
	Hooks        []HookData     `json:"hooks"`
	Recent       []webhook.Call `json:"recent"`
}

// HookData is one configured hook.
type HookData struct {
	Name       string     `json:"name"`
	Channel    *Ref       `json:"channel"`
	Calls      int        `json:"calls"`
	LastCall   *time.Time `json:"last_call,omitempty"`
	LastStatus int        `json:"last_status,omitempty"`
}

// Webhooks shows the configured hooks and their recent calls. src is nil
// when no hook is configured.
func Webhooks(src WebhookSource, names Names) dashboard.Panel {
	names = orNoNames(names)
	return dashboard.Panel{ID: "webhooks", Title: "Webhooks", Snapshot: func(context.Context) (any, error) {
		d := WebhooksData{Hooks: []HookData{}, Recent: []webhook.Call{}, RateLimit: webhook.DefaultRateLimit, MaxBodyBytes: webhook.MaxBodyBytes}
		if src == nil {
			return d, nil
		}
		st := src.Status()
		d.RateLimit, d.MaxBodyBytes = st.RateLimit, st.MaxBodyBytes
		for _, h := range st.Hooks {
			d.Hooks = append(d.Hooks, HookData{Name: h.Name, Channel: channelRef(names, h.ChannelID), Calls: h.Calls, LastCall: h.LastCall, LastStatus: h.LastStatus})
		}
		if st.Recent != nil {
			d.Recent = st.Recent
		}
		return d, nil
	}}
}
