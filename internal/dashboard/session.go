package dashboard

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// SessionTTL is how long a login lasts.
const SessionTTL = 7 * 24 * time.Hour

// Session is what the session cookie holds: who logged in and until when.
// No Discord token is kept.
type Session struct {
	UserID  string `json:"u"`
	Name    string `json:"n"`
	Issued  int64  `json:"i"`
	Expires int64  `json:"e"`
}

var errBadSession = errors.New("dashboard: invalid or expired session")

// sealer encrypts and authenticates session cookies with AES-256-GCM, so a
// cookie can be neither read nor changed without DASHBOARD_SESSION_KEY.
type sealer struct {
	aead cipher.AEAD
	// aad binds a sealed value to its purpose (the cookie name), so one
	// cookie cannot be replayed as another.
	aad []byte
}

func newSealer(key []byte, purpose string) (*sealer, error) {
	if len(key) < 32 {
		return nil, errors.New("dashboard: the session key must be at least 32 bytes")
	}
	// Derive the AES key so any key length works and the raw key is used for
	// nothing else.
	k := sha256.Sum256(append([]byte("sonnie-dashboard-session-v1\x00"), key...))
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead, aad: []byte(purpose)}, nil
}

func (s *sealer) seal(v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, plain, s.aad)
	return base64.RawURLEncoding.EncodeToString(out), nil
}

func (s *sealer) open(value string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < s.aead.NonceSize()+s.aead.Overhead() {
		return errBadSession
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], s.aad)
	if err != nil {
		return errBadSession
	}
	if err := json.Unmarshal(plain, v); err != nil {
		return errBadSession
	}
	return nil
}

func (s *sealer) sealSession(sess Session) (string, error) {
	return s.seal(sess)
}

// openSession returns the session in value if it is genuine and not expired.
func (s *sealer) openSession(value string, now time.Time) (Session, error) {
	var sess Session
	if err := s.open(value, &sess); err != nil {
		return Session{}, err
	}
	if sess.UserID == "" || now.Unix() >= sess.Expires {
		return Session{}, errBadSession
	}
	return sess, nil
}
