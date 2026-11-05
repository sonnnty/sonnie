// Package dashboard is the bot's HTTP server: a read-only monitoring
// dashboard behind Discord OAuth2 login, /healthz for Kubernetes probes,
// and the mount point of the webhooks. Each bot feature shows itself as a
// Panel; the page polls /api/panels and renders every panel client-side.
package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

const (
	sessionCookie = "sonnie_session"
	stateCookie   = "sonnie_oauth_state"
	// stateTTL is how long a login may take at Discord.
	stateTTL = 10 * time.Minute
	// DefaultAuthCacheTTL is how long an access decision is reused before
	// the user's guild permissions are checked again.
	DefaultAuthCacheTTL = 10 * time.Minute
	// staleAuthLimit is how long a cached decision may stand in when a
	// re-check fails (Discord unreachable).
	staleAuthLimit = time.Hour
)

// Config configures a Server.
type Config struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string

	// Login. When ClientSecret, PublicURL or SessionKey is empty the
	// dashboard pages explain what to set; /healthz and webhooks still work.
	PublicURL    string
	ClientID     func() string
	ClientSecret string
	SessionKey   []byte
	Access       Authorizer

	Panels []Panel
	// Healthy reports whether the Discord gateway is connected.
	Healthy func() bool
	// Webhooks, if set, serves POST /hooks/{name}.
	Webhooks http.Handler

	// Endpoints default to DiscordEndpoints; HTTPClient to a client with a
	// timeout. For tests.
	Endpoints  Endpoints
	HTTPClient *http.Client
	// AuthCacheTTL defaults to DefaultAuthCacheTTL.
	AuthCacheTTL time.Duration
	// Now defaults to time.Now.
	Now func() time.Time
}

type authEntry struct {
	allowed bool
	at      time.Time
}

// Server is the dashboard HTTP server.
type Server struct {
	cfg     Config
	login   bool
	secure  bool
	sealer  *sealer
	oauth   *oauthClient
	pages   *template.Template
	handler http.Handler
	srv     *http.Server
	docs    *docsStore

	mu        sync.Mutex
	authCache map[string]authEntry
}

// New builds the server; Start makes it listen.
func New(cfg Config) (*Server, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.AuthCacheTTL <= 0 {
		cfg.AuthCacheTTL = DefaultAuthCacheTTL
	}
	if cfg.Endpoints == (Endpoints{}) {
		cfg.Endpoints = DiscordEndpoints
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: oauthTimeout}
	}
	if cfg.ClientID == nil {
		cfg.ClientID = func() string { return "" }
	}
	if cfg.Healthy == nil {
		cfg.Healthy = func() bool { return false }
	}
	pages, err := template.New("").ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	docs, err := newDocsStore()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:       cfg,
		pages:     pages,
		docs:      docs,
		authCache: map[string]authEntry{},
		secure:    strings.HasPrefix(cfg.PublicURL, "https://"),
	}
	if cfg.ClientSecret != "" && cfg.PublicURL != "" && len(cfg.SessionKey) > 0 {
		if s.sealer, err = newSealer(cfg.SessionKey, sessionCookie); err != nil {
			return nil, err
		}
		s.login = true
		s.oauth = &oauthClient{
			endpoints:    cfg.Endpoints,
			clientID:     cfg.ClientID,
			clientSecret: cfg.ClientSecret,
			redirectURI:  strings.TrimRight(cfg.PublicURL, "/") + "/auth/callback",
			http:         cfg.HTTPClient,
		}
	}
	s.handler = securityHeaders(s.routes())
	return s, nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", noCache(http.FileServerFS(static))))
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /docs", s.handleDocsIndex)
	mux.HandleFunc("GET /docs/", s.handleDocsIndex)
	mux.HandleFunc("GET /docs/{slug}", s.handleDocsSlug)
	mux.HandleFunc("GET /auth/login", s.handleLogin)
	mux.HandleFunc("GET /auth/callback", s.handleCallback)
	mux.HandleFunc("GET /auth/logout", s.handleLogout)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/panels", s.handlePanels)
	if s.cfg.Webhooks != nil {
		mux.Handle("POST /hooks/{name}", s.cfg.Webhooks)
	}
	return mux
}

// Handler is the full HTTP handler, for tests.
func (s *Server) Handler() http.Handler { return s.handler }

// LoginEnabled reports whether Discord login is configured.
func (s *Server) LoginEnabled() bool { return s.login }

// Start listens on Config.Addr and serves in the background. It returns
// the listen error, if any.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	s.srv = &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("dashboard: server stopped: %v", err)
		}
	}()
	return nil
}

// Shutdown stops accepting connections and waits for requests in flight
// until ctx is done.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !s.cfg.Healthy() {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("gateway not connected\n"))
		return
	}
	w.Write([]byte("ok\n"))
}

// page is the data every template gets.
type page struct {
	Title       string
	User        string
	Nav         string
	Heading     string
	Text        string
	Hint        string
	Link        string
	LinkTo      string
	Panels      []Panel
	TOC         []DocItem
	DocsContent template.HTML
}

func (s *Server) render(w http.ResponseWriter, status int, name string, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.pages.ExecuteTemplate(w, name, p); err != nil {
		log.Printf("dashboard: render %s: %v", name, err)
	}
}

func (s *Server) message(w http.ResponseWriter, status int, user, heading, text string) {
	p := page{Title: heading, User: user, Heading: heading, Text: text, Link: "กลับหน้าแรก", LinkTo: "/"}
	s.render(w, status, "message", p)
}

func (s *Server) notConfigured(w http.ResponseWriter) {
	s.render(w, http.StatusServiceUnavailable, "message", page{
		Title:   "ยังไม่ได้ตั้งค่าแดชบอร์ด",
		Heading: "ยังไม่ได้ตั้งค่าการเข้าสู่ระบบ",
		Text:    "แดชบอร์ดต้องใช้การเข้าสู่ระบบด้วย Discord แต่ยังตั้งค่าไม่ครบ",
		Hint:    "ตั้งค่า DISCORD_CLIENT_SECRET, DASHBOARD_PUBLIC_URL และ DASHBOARD_SESSION_KEY แล้วรีสตาร์ตบอท (ดู README หัวข้อ Dashboard & webhooks)",
	})
}

func (s *Server) forbidden(w http.ResponseWriter, user string) {
	s.render(w, http.StatusForbidden, "message", page{
		Title:   "ไม่มีสิทธิ์เข้าถึง",
		User:    user,
		Heading: "ขออภัย คุณไม่มีสิทธิ์ดูแดชบอร์ดนี้",
		Text:    "แดชบอร์ดนี้เปิดให้เฉพาะผู้ดูแลเซิร์ฟเวอร์ที่มีสิทธิ์ Administrator หรือ Manage Server ในเซิร์ฟเวอร์ที่ SONNIE อยู่ หรือผู้ที่เจ้าของบอทอนุญาตไว้เท่านั้น",
		Hint:    "ถ้าคิดว่าควรเข้าได้ ลองติดต่อเจ้าของบอท หรือออกจากระบบแล้วเข้าด้วยบัญชีอื่น",
		Link:    "ออกจากระบบ",
		LinkTo:  "/auth/logout",
	})
}

func (s *Server) checkAccessForPage(w http.ResponseWriter, r *http.Request, nav string) (Session, bool) {
	if !s.login {
		s.notConfigured(w)
		return Session{}, false
	}
	sess, ok := s.session(r)
	if !ok {
		s.render(w, http.StatusOK, "login", page{Title: "เข้าสู่ระบบ", Nav: nav})
		return Session{}, false
	}
	allowed, err := s.authorized(r.Context(), sess.UserID)
	switch {
	case err != nil:
		s.message(w, http.StatusServiceUnavailable, sess.Name, "ตรวจสอบสิทธิ์ไม่ได้ชั่วคราว", "ติดต่อ Discord เพื่อตรวจสอบสิทธิ์ไม่สำเร็จ กรุณาลองใหม่อีกครั้งในอีกสักครู่")
		return Session{}, false
	case !allowed:
		s.forbidden(w, sess.Name)
		return Session{}, false
	default:
		return sess, true
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.checkAccessForPage(w, r, "dashboard")
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "dashboard", page{Title: "แดชบอร์ด", User: sess.Name, Nav: "dashboard", Panels: s.cfg.Panels})
}

func (s *Server) handleDocsIndex(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.checkAccessForPage(w, r, "docs")
	if !ok {
		return
	}
	first := s.docs.firstSlug()
	if first == "" {
		s.message(w, http.StatusNotFound, sess.Name, "ไม่พบคู่มือ", "ยังไม่มีไฟล์คู่มือในระบบ")
		return
	}
	http.Redirect(w, r, "/docs/"+first, http.StatusSeeOther)
}

func (s *Server) handleDocsSlug(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.checkAccessForPage(w, r, "docs")
	if !ok {
		return
	}
	slug := r.PathValue("slug")
	if slug == "" {
		s.handleDocsIndex(w, r)
		return
	}
	content, item, ok := s.docs.render(slug)
	if !ok {
		s.message(w, http.StatusNotFound, sess.Name, "ไม่พบคู่มือหน้านี้", "ไม่พบเนื้อหาคู่มือสำหรับหัวข้อนี้ กรุณาเลือกจากสารบัญ")
		return
	}
	s.render(w, http.StatusOK, "docs", page{
		Title:       item.Title,
		User:        sess.Name,
		Nav:         "docs",
		TOC:         s.docs.toc(slug),
		DocsContent: content,
	})
}

func (s *Server) handlePanels(w http.ResponseWriter, r *http.Request) {
	if !s.login {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "login is not configured"})
		return
	}
	sess, ok := s.session(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not logged in"})
		return
	}
	allowed, err := s.authorized(r.Context(), sess.UserID)
	switch {
	case err != nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "access check failed"})
	case !allowed:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"generated_at": s.cfg.Now().UTC(),
			"panels":       snapshotAll(r.Context(), s.cfg.Panels),
		})
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.login {
		s.notConfigured(w)
		return
	}
	if s.cfg.ClientID() == "" {
		s.message(w, http.StatusServiceUnavailable, "", "บอทยังไม่พร้อม", "ยังไม่รู้ Application ID ของบอท (รอให้บอทเชื่อมต่อ Discord ก่อน หรือตั้ง DISCORD_CLIENT_ID)")
		return
	}
	state, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: state, Path: "/auth", MaxAge: int(stateTTL / time.Second),
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.oauth.authorizeURL(state), http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if !s.login {
		s.notConfigured(w)
		return
	}
	// The state cookie is single-use, whatever happens next.
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/auth", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})

	q := r.URL.Query()
	if q.Get("error") != "" {
		s.message(w, http.StatusBadRequest, "", "ยกเลิกการเข้าสู่ระบบแล้ว", "Discord ไม่ได้อนุญาตการเข้าสู่ระบบ กดกลับหน้าแรกเพื่อลองใหม่")
		return
	}
	c, err := r.Cookie(stateCookie)
	got := q.Get("state")
	if err != nil || c.Value == "" || got == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(got)) != 1 {
		s.message(w, http.StatusBadRequest, "", "ลิงก์เข้าสู่ระบบไม่ถูกต้องหรือหมดอายุ", "กรุณากลับหน้าแรกแล้วกดเข้าสู่ระบบใหม่อีกครั้ง")
		return
	}
	code := q.Get("code")
	if code == "" {
		s.message(w, http.StatusBadRequest, "", "ลิงก์เข้าสู่ระบบไม่ถูกต้อง", "ไม่พบรหัสจาก Discord กรุณาลองใหม่")
		return
	}

	token, err := s.oauth.exchange(r.Context(), code)
	if err != nil {
		log.Printf("dashboard: login failed: %v", err)
		s.message(w, http.StatusBadGateway, "", "เข้าสู่ระบบไม่สำเร็จ", "แลกรหัสกับ Discord ไม่สำเร็จ ตรวจสอบ DISCORD_CLIENT_SECRET และ Redirect URI ใน Developer Portal แล้วลองใหม่")
		return
	}
	user, err := s.oauth.user(r.Context(), token)
	if err != nil {
		log.Printf("dashboard: login failed: %v", err)
		s.message(w, http.StatusBadGateway, "", "เข้าสู่ระบบไม่สำเร็จ", "อ่านข้อมูลผู้ใช้จาก Discord ไม่สำเร็จ กรุณาลองใหม่")
		return
	}

	// A fresh check at login, whatever is cached.
	allowed, err := s.checkAccess(r.Context(), user.ID)
	if err != nil && !allowed {
		log.Printf("dashboard: access check for user %s: %v", user.ID, err)
		s.message(w, http.StatusServiceUnavailable, "", "ตรวจสอบสิทธิ์ไม่ได้ชั่วคราว", "ติดต่อ Discord เพื่อตรวจสอบสิทธิ์ไม่สำเร็จ กรุณาลองใหม่อีกครั้งในอีกสักครู่")
		return
	}
	if !allowed {
		log.Printf("dashboard: login refused for user %s (not allowed)", user.ID)
		s.forbidden(w, user.DisplayName())
		return
	}

	now := s.cfg.Now()
	value, err := s.sealer.sealSession(Session{UserID: user.ID, Name: user.DisplayName(), Issued: now.Unix(), Expires: now.Add(SessionTTL).Unix()})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", MaxAge: int(SessionTTL / time.Second),
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
	log.Printf("dashboard: user %s logged in", user.ID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := s.session(r); ok {
		s.mu.Lock()
		delete(s.authCache, sess.UserID)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// session returns the logged-in user, if the cookie is genuine and current.
func (s *Server) session(r *http.Request) (Session, bool) {
	if s.sealer == nil {
		return Session{}, false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return Session{}, false
	}
	sess, err := s.sealer.openSession(c.Value, s.cfg.Now())
	return sess, err == nil
}

// authorized reuses a decision younger than AuthCacheTTL, else checks again.
// When the check fails, a decision up to staleAuthLimit old stands in.
func (s *Server) authorized(ctx context.Context, userID string) (bool, error) {
	now := s.cfg.Now()
	s.mu.Lock()
	e, ok := s.authCache[userID]
	s.mu.Unlock()
	if ok && now.Sub(e.at) < s.cfg.AuthCacheTTL {
		return e.allowed, nil
	}
	allowed, err := s.checkAccess(ctx, userID)
	if err != nil && !allowed {
		log.Printf("dashboard: access check for user %s: %v", userID, err)
		if ok && now.Sub(e.at) < staleAuthLimit {
			return e.allowed, nil
		}
		return false, err
	}
	return allowed, nil
}

// checkAccess asks the Authorizer and caches a definite answer.
func (s *Server) checkAccess(ctx context.Context, userID string) (bool, error) {
	if s.cfg.Access == nil {
		return false, nil
	}
	allowed, err := s.cfg.Access.Authorize(ctx, userID)
	if err != nil && !allowed {
		return false, err
	}
	s.mu.Lock()
	s.authCache[userID] = authEntry{allowed: allowed, at: s.cfg.Now()}
	s.mu.Unlock()
	return allowed, nil
}

// securityHeaders sets the headers every answer carries. The pages load
// script and style only from /static, so the CSP needs no 'unsafe-inline'.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// noCache makes browsers revalidate the static files: embedded files have
// no modification time, and a new bot version must not run old script.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("dashboard: encode JSON: %v", err)
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
