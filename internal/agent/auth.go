package agent

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/SaeedCodez/v2dock/internal/store"
)

const (
	minPasswordLen = 12
	maxPasswordLen = 256
	sessionTTL     = 7 * 24 * time.Hour
	sessionIdle    = 24 * time.Hour

	kvPassword  = "auth.password"
	kvTOTP      = "auth.totp"
	kvTOTPPend  = "auth.totp_pending"
	kvTOTPLast  = "auth.totp_last"
	tokenPrefix = "v2d_"
	scopeAdmin  = "admin"
	scopeRead   = "read"
)

// principal is who is making an API request.
type principal struct {
	kind        string // local | session | token | anon
	scope       string
	name        string
	ip          string
	sessionHash string
}

func (p *principal) actor() string {
	if p == nil {
		return "system"
	}
	switch p.kind {
	case "local":
		return "cli (local)"
	case "session":
		return "admin (web)"
	case "token":
		return "token:" + p.name
	}
	return p.kind
}

type attempt struct {
	fails       int
	lockedUntil time.Time
	last        time.Time
}

type authState struct {
	a        *Agent
	mu       sync.Mutex
	attempts map[string]*attempt
	recent   []time.Time // failed logins from anywhere, for a global brake
	hashSem  chan struct{}
}

func newAuthState(a *Agent) *authState {
	return &authState{a: a, attempts: map[string]*attempt{}, hashSem: make(chan struct{}, 2)}
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ------------------------------------------------------------- passwords

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
)

// hashPassword uses Argon2id. At most two hashes run at once, which bounds
// memory use under a flood of login attempts.
func (s *authState) hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	s.hashSem <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-s.hashSem
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (s *authState) verifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || m > 256*1024 || t > 10 || p == 0 || p > 8 {
		return false
	}
	s.hashSem <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-s.hashSem
	return subtle.ConstantTimeCompare(got, want) == 1
}

func validatePassword(pw string) error {
	if len(pw) < minPasswordLen {
		return fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(pw) > maxPasswordLen {
		return errors.New("password is too long")
	}
	return nil
}

func (s *authState) passwordSet(ctx context.Context) bool {
	_, ok, _ := s.a.db.GetKV(ctx, kvPassword)
	return ok
}

// SetPassword stores a new password and ends every session.
func (s *authState) SetPassword(ctx context.Context, pw string) error {
	if err := validatePassword(pw); err != nil {
		return err
	}
	h, err := s.hashPassword(pw)
	if err != nil {
		return err
	}
	if err := s.a.db.SetKV(ctx, kvPassword, h); err != nil {
		return err
	}
	return s.a.db.DeleteAllSessions(ctx)
}

// bootstrap sets the first password from the environment, or prints a
// one-time setup token. Without a token, whoever reached a fresh deployment
// first could claim it.
func (s *authState) bootstrap(ctx context.Context) error {
	if s.passwordSet(ctx) {
		return nil
	}
	if pw := s.a.cfg.AdminPassword; pw != "" {
		if err := s.SetPassword(ctx, pw); err != nil {
			return fmt.Errorf("V2DOCK_ADMIN_PASSWORD: %w", err)
		}
		slog.Info("admin password set from V2DOCK_ADMIN_PASSWORD")
		return nil
	}
	tok := s.a.cfg.SetupToken
	if tok == "" {
		tok = randomID(12)
	}
	s.a.mu.Lock()
	s.a.setupToken = tok
	s.a.mu.Unlock()
	slog.Warn("no admin password yet: open the web UI and use this one-time setup token", "setupToken", tok)
	return nil
}

func (s *authState) checkSetupToken(tok string) bool {
	s.a.mu.Lock()
	want := s.a.setupToken
	s.a.mu.Unlock()
	return want != "" && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(tok)), []byte(want)) == 1
}

func (s *authState) clearSetupToken() {
	s.a.mu.Lock()
	s.a.setupToken = ""
	s.a.mu.Unlock()
}

// ----------------------------------------------------------- rate limiting

// allowAttempt reports whether ip may try to log in now, and if not, how
// long it has to wait.
func (s *authState) allowAttempt(ip string) (bool, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if at := s.attempts[ip]; at != nil && now.Before(at.lockedUntil) {
		return false, at.lockedUntil.Sub(now)
	}
	// Global brake: many failures from many addresses means a distributed
	// guessing attempt; slow everyone down.
	cut := now.Add(-10 * time.Minute)
	n := 0
	for _, t := range s.recent {
		if t.After(cut) {
			n++
		}
	}
	if n >= 100 {
		return false, time.Minute
	}
	return true, 0
}

func (s *authState) recordFailure(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	at := s.attempts[ip]
	if at == nil {
		at = &attempt{}
		s.attempts[ip] = at
	}
	if now.Sub(at.last) > time.Hour {
		at.fails = 0
	}
	at.fails++
	at.last = now
	if at.fails >= 5 {
		// 1, 2, 4, 8 ... minutes, capped at an hour.
		d := time.Minute << min(at.fails-5, 6)
		at.lockedUntil = now.Add(min(d, time.Hour))
	}
	s.recent = append(s.recent, now)
	if len(s.recent) > 1000 {
		s.recent = s.recent[len(s.recent)-500:]
	}
}

func (s *authState) recordSuccess(ip string) {
	s.mu.Lock()
	delete(s.attempts, ip)
	s.mu.Unlock()
}

func (s *authState) pruneLimiter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ip, at := range s.attempts {
		if time.Since(at.last) > 2*time.Hour {
			delete(s.attempts, ip)
		}
	}
}

// ----------------------------------------------------------------- sessions

// newSession returns the raw cookie value; only its hash is stored.
func (s *authState) newSession(ctx context.Context, ip, ua string) (string, error) {
	raw := randomID(32)
	now := time.Now()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	err := s.a.db.CreateSession(ctx, store.Session{
		IDHash: sha256hex(raw), CreatedAt: now.Unix(), ExpiresAt: now.Add(sessionTTL).Unix(),
		LastSeen: now.Unix(), IP: ip, UserAgent: ua,
	})
	return raw, err
}

func (s *authState) checkSession(ctx context.Context, raw string) (string, bool) {
	if len(raw) != 64 {
		return "", false
	}
	h := sha256hex(raw)
	sess, err := s.a.db.GetSession(ctx, h)
	if err != nil {
		return "", false
	}
	now := time.Now()
	if now.Unix() > sess.ExpiresAt || now.Sub(time.Unix(sess.LastSeen, 0)) > sessionIdle {
		_ = s.a.db.DeleteSession(ctx, h)
		return "", false
	}
	if now.Sub(time.Unix(sess.LastSeen, 0)) > time.Minute {
		_ = s.a.db.TouchSession(ctx, h, now)
	}
	return h, true
}

// ------------------------------------------------------------------- tokens

func (s *authState) createToken(ctx context.Context, name, scope string) (string, store.APIToken, error) {
	raw := tokenPrefix + randomID(24)
	t := store.APIToken{ID: randomID(4), Name: name, Hash: sha256hex(raw), Scope: scope}
	if err := s.a.db.CreateToken(ctx, t); err != nil {
		return "", t, err
	}
	t.CreatedAt = time.Now().Unix()
	return raw, t, nil
}

func (s *authState) checkToken(ctx context.Context, raw string) (store.APIToken, bool) {
	if !strings.HasPrefix(raw, tokenPrefix) || len(raw) != len(tokenPrefix)+48 {
		return store.APIToken{}, false
	}
	t, err := s.a.db.TokenByHash(ctx, sha256hex(raw))
	if err != nil {
		return t, false
	}
	if time.Since(time.Unix(t.LastUsedAt, 0)) > time.Minute {
		_ = s.a.db.TouchToken(ctx, t.ID)
	}
	return t, true
}

// --------------------------------------------------------------------- TOTP

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return b32.EncodeToString(b)
}

func totpCode(secret string, counter int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000), nil
}

// matchTOTP returns the matching time step (±1 step of clock skew), or -1.
func matchTOTP(secret, code string, t time.Time) int64 {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return -1
	}
	step := t.Unix() / 30
	for _, c := range []int64{step, step - 1, step + 1} {
		want, err := totpCode(secret, c)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return c
		}
	}
	return -1
}

func (s *authState) totpEnabled(ctx context.Context) bool {
	_, ok, _ := s.a.db.GetKV(ctx, kvTOTP)
	return ok
}

// verifyTOTP checks a code and refuses to accept the same step twice.
func (s *authState) verifyTOTP(ctx context.Context, code string) bool {
	secret, ok, _ := s.a.db.GetKV(ctx, kvTOTP)
	if !ok {
		return true
	}
	step := matchTOTP(secret, code, time.Now())
	if step < 0 {
		return false
	}
	last, _, _ := s.a.db.GetKV(ctx, kvTOTPLast)
	if n, err := strconv.ParseInt(last, 10, 64); err == nil && step <= n {
		return false
	}
	_ = s.a.db.SetKV(ctx, kvTOTPLast, strconv.FormatInt(step, 10))
	return true
}
