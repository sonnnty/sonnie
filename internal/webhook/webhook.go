// Package webhook is the generic incoming webhook: POST /hooks/{name} with
// the hook's secret token posts the body to the hook's Discord channel. It
// needs no Discord login; each hook has its own token, compared in constant
// time. Nothing posted can ping anyone.
package webhook

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	// MaxBodyBytes is the largest accepted request body.
	MaxBodyBytes = 64 << 10
	// DefaultRateLimit is how many calls a hook takes per minute.
	DefaultRateLimit = 30
	// recentCalls is how many calls the dashboard shows.
	recentCalls = 100
	// postTimeout bounds the Discord call.
	postTimeout = 15 * time.Second
	// maxShownName cuts names of unknown hooks in the call list.
	maxShownName = 32
)

// Hook is one configured webhook.
type Hook struct {
	Name      string
	Token     string
	ChannelID string
}

// Poster posts a message; *discordgo.Session implements it.
type Poster interface {
	ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// Config configures a Handler.
type Config struct {
	Hooks  []Hook
	Poster Poster
	// RateLimit is calls per minute per hook; 0 means DefaultRateLimit.
	RateLimit int
	// Now defaults to time.Now.
	Now func() time.Time
}

// Call is one request to a hook, as shown on the dashboard. It holds no
// token and no body.
type Call struct {
	At       time.Time `json:"at"`
	Hook     string    `json:"hook"`
	Known    bool      `json:"known"`
	Status   int       `json:"status"`
	SourceIP string    `json:"source_ip"`
	Bytes    int       `json:"bytes"`
	// MessageID is the posted Discord message, on success.
	MessageID string `json:"message_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// HookStatus is a configured hook without its token.
type HookStatus struct {
	Name      string     `json:"name"`
	ChannelID string     `json:"channel_id"`
	Calls     int        `json:"calls"`
	LastCall  *time.Time `json:"last_call,omitempty"`
	// LastStatus is the HTTP status of the last call.
	LastStatus int `json:"last_status,omitempty"`
}

// Status is what the dashboard shows.
type Status struct {
	RateLimit    int          `json:"rate_limit_per_minute"`
	MaxBodyBytes int          `json:"max_body_bytes"`
	Hooks        []HookStatus `json:"hooks"`
	// Recent calls, newest first.
	Recent []Call `json:"recent"`
}

type hookState struct {
	Hook
	tokenSum [sha256.Size]byte
	window   []time.Time // accepted calls in the last minute
	calls    int
	last     *Call
}

// Handler serves POST /hooks/{name}. It is safe for concurrent use.
type Handler struct {
	cfg   Config
	hooks map[string]*hookState
	order []string
	// dummy is compared against for unknown hooks, so they take as long as
	// a wrong token.
	dummy [sha256.Size]byte

	mu     sync.Mutex
	recent []Call // circular
	next   int
}

// New makes a Handler for cfg.Hooks.
func New(cfg Config) *Handler {
	if cfg.RateLimit <= 0 {
		cfg.RateLimit = DefaultRateLimit
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	h := &Handler{cfg: cfg, hooks: map[string]*hookState{}, dummy: sha256.Sum256([]byte("sonnie-webhook-dummy"))}
	for _, hk := range cfg.Hooks {
		h.hooks[hk.Name] = &hookState{Hook: hk, tokenSum: sha256.Sum256([]byte(hk.Token))}
		h.order = append(h.order, hk.Name)
	}
	return h
}

// Tokens returns the configured tokens, for log redaction.
func (h *Handler) Tokens() []string {
	out := make([]string, 0, len(h.hooks))
	for _, name := range h.order {
		out = append(out, h.hooks[name].Token)
	}
	return out
}

// ServeHTTP handles one call. The route must provide the {name} path value.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	call := Call{At: h.cfg.Now(), Hook: shownName(name), SourceIP: sourceIP(r)}
	status, body := h.serve(w, r, name, &call)
	call.Status = status
	h.record(call)
	log.Printf("webhook: %s -> %d from %s (%d bytes)%s", call.Hook, status, call.SourceIP, call.Bytes, logSuffix(call))
	writeJSON(w, status, body)
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, name string, call *Call) (int, map[string]any) {
	hk := h.authenticate(name, presentedToken(r))
	if hk == nil {
		// The same answer for an unknown hook and a wrong token, so the
		// response never says which hooks exist.
		return http.StatusUnauthorized, errBody("unauthorized")
	}
	call.Known = true
	if retry, ok := h.allow(hk); !ok {
		return http.StatusTooManyRequests, map[string]any{"ok": false, "error": "rate limited", "retry_after": retry}
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	call.Bytes = len(data)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			call.Error = "body too large"
			return http.StatusRequestEntityTooLarge, errBody("body larger than 64 KB")
		}
		call.Error = "read failed"
		return http.StatusBadRequest, errBody("could not read the body")
	}
	msg, err := parseMessage(r.Header.Get("Content-Type"), data, hk.Name)
	if err != nil {
		call.Error = err.Error()
		if errors.Is(err, errUnsupportedType) {
			return http.StatusUnsupportedMediaType, errBody(err.Error())
		}
		return http.StatusBadRequest, errBody(err.Error())
	}

	ctx, cancel := context.WithTimeout(r.Context(), postTimeout)
	defer cancel()
	m, err := h.cfg.Poster.ChannelMessageSendComplex(hk.ChannelID, msg, discordgo.WithContext(ctx))
	if err != nil {
		log.Printf("webhook: %s: posting to channel %s: %v", hk.Name, hk.ChannelID, err)
		call.Error = "Discord refused the message"
		return http.StatusBadGateway, errBody("Discord did not accept the message")
	}
	call.MessageID = m.ID
	return http.StatusOK, map[string]any{"ok": true, "message_id": m.ID, "channel_id": hk.ChannelID}
}

// authenticate returns the hook if token is its token.
func (h *Handler) authenticate(name, token string) *hookState {
	sum := sha256.Sum256([]byte(token))
	hk, ok := h.hooks[name]
	want := h.dummy
	if ok {
		want = hk.tokenSum
	}
	// Comparing fixed-size digests keeps the token length out of the timing.
	match := subtle.ConstantTimeCompare(sum[:], want[:]) == 1
	if !ok || !match || token == "" {
		return nil
	}
	return hk
}

// allow applies the per-hook rate limit: at most RateLimit accepted calls in
// any minute. retry is the seconds until the next call is allowed.
func (h *Handler) allow(hk *hookState) (retry int, ok bool) {
	now := h.cfg.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	kept := hk.window[:0]
	for _, t := range hk.window {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	hk.window = kept
	if len(hk.window) >= h.cfg.RateLimit {
		wait := hk.window[0].Add(time.Minute).Sub(now)
		return max(int((wait+time.Second-1)/time.Second), 1), false
	}
	hk.window = append(hk.window, now)
	return 0, true
}

func (h *Handler) record(c Call) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hk, ok := h.hooks[c.Hook]; ok && c.Known {
		hk.calls++
		cp := c
		hk.last = &cp
	}
	if len(h.recent) < recentCalls {
		h.recent = append(h.recent, c)
		return
	}
	h.recent[h.next] = c
	h.next = (h.next + 1) % recentCalls
}

// Status returns the hooks (without tokens) and the recent calls.
func (h *Handler) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := Status{RateLimit: h.cfg.RateLimit, MaxBodyBytes: MaxBodyBytes, Hooks: []HookStatus{}, Recent: []Call{}}
	for _, name := range h.order {
		hk := h.hooks[name]
		hs := HookStatus{Name: hk.Name, ChannelID: hk.ChannelID, Calls: hk.calls}
		if hk.last != nil {
			at := hk.last.At
			hs.LastCall, hs.LastStatus = &at, hk.last.Status
		}
		st.Hooks = append(st.Hooks, hs)
	}
	// Oldest first in the ring from next; reverse it for newest first.
	n := len(h.recent)
	for i := 0; i < n; i++ {
		st.Recent = append(st.Recent, h.recent[(h.next+n-1-i)%n])
	}
	return st
}

// presentedToken reads "Authorization: Bearer <token>" or X-Sonnie-Token.
func presentedToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if scheme, tok, ok := strings.Cut(auth, " "); ok && strings.EqualFold(scheme, "Bearer") {
			return strings.TrimSpace(tok)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Sonnie-Token"))
}

// sourceIP is the peer address, followed by the client the reverse proxy in
// front (sonnie.tapb.ch) reports in X-Forwarded-For. It is only shown and
// logged, never used to decide anything, so the header needs no trust.
func sourceIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		client := strings.TrimSpace(strings.Split(xff, ",")[0])
		if ip := net.ParseIP(client); ip != nil {
			return ip.String() + " via " + host
		}
	}
	return host
}

func shownName(name string) string {
	if len(name) > maxShownName {
		name = name[:maxShownName]
	}
	// Attacker-chosen text: escape control and invalid bytes.
	q := strconv.QuoteToGraphic(name)
	return q[1 : len(q)-1]
}

func logSuffix(c Call) string {
	switch {
	case c.MessageID != "":
		return " message " + c.MessageID
	case c.Error != "":
		return ": " + c.Error
	}
	return ""
}

func errBody(msg string) map[string]any {
	return map[string]any{"ok": false, "error": msg}
}

func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if retry, ok := body["retry_after"].(int); ok {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
