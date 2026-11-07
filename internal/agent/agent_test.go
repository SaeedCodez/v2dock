package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SaeedCodez/v2dock/internal/docker"
	"github.com/SaeedCodez/v2dock/internal/store"
	"github.com/SaeedCodez/v2dock/internal/wire"
)

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	a, err := New(Config{DataDir: t.TempDir(), SocketPath: t.TempDir() + "/a.sock", DockerSocket: "/nonexistent", NoFirewall: true, SetupToken: "setup-token-for-tests"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.db.Close() })
	if err := a.auth.bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	return a
}

type req struct {
	method, path, body string
	gateway, csrf      bool
	local              bool // caller runs as the agent's own user
	cookie, bearer     string
}

func (a *Agent) do(t *testing.T, r req) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	h := a.routes()
	hr := httptest.NewRequest(r.method, "http://agent"+r.path, strings.NewReader(r.body))
	if r.body != "" {
		hr.Header.Set("Content-Type", "application/json")
	}
	if r.gateway {
		hr.Header.Set(wire.HeaderGateway, "1")
		hr.Header.Set(wire.HeaderClientIP, "203.0.113.7")
	}
	if r.csrf {
		hr.Header.Set(wire.HeaderCSRF, "1")
	}
	if r.cookie != "" {
		hr.AddCookie(&http.Cookie{Name: cookieInsecure, Value: r.cookie})
	}
	if r.bearer != "" {
		hr.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	uid := 65532 // the web gateway's user
	if r.local {
		uid = os.Geteuid()
	}
	hr = hr.WithContext(context.WithValue(hr.Context(), peerKey{}, uid))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, hr)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func TestGatewayRequiresLogin(t *testing.T) {
	a := newTestAgent(t)
	for _, p := range []string{"/api/status", "/api/servers", "/api/containers", "/api/settings", "/api/tokens", "/api/diagnostics"} {
		w, _ := a.do(t, req{method: "GET", path: p, gateway: true})
		if w.Code != 401 {
			t.Errorf("%s via gateway without login: got %d", p, w.Code)
		}
	}
	// The CLI, running as the agent's user, is trusted.
	if w, _ := a.do(t, req{method: "GET", path: "/api/settings", local: true}); w.Code != 200 {
		t.Errorf("local caller: got %d", w.Code)
	}
	// The gateway's user is not trusted even if it omits the gateway header.
	if w, _ := a.do(t, req{method: "GET", path: "/api/settings"}); w.Code != 401 {
		t.Errorf("unprivileged socket caller without header: got %d", w.Code)
	}
	if w, _ := a.do(t, req{method: "POST", path: "/api/auth/password", body: `{"currentPassword":"","newPassword":"aaaaaaaaaaaaaaaa"}`, csrf: true}); w.Code != 401 {
		t.Errorf("unprivileged caller changed the password: %d", w.Code)
	}
}

func TestSetupNeedsTokenAndCSRF(t *testing.T) {
	a := newTestAgent(t)
	body := `{"password":"correct horse battery staple","setupToken":"setup-token-for-tests"}`
	if w, _ := a.do(t, req{method: "POST", path: "/api/auth/setup", body: body, gateway: true}); w.Code != 403 {
		t.Fatalf("setup without CSRF header: %d", w.Code)
	}
	if w, _ := a.do(t, req{method: "POST", path: "/api/auth/setup", body: `{"password":"correct horse battery staple","setupToken":"nope"}`, gateway: true, csrf: true}); w.Code != 403 {
		t.Fatalf("setup with wrong token: %d", w.Code)
	}
	w, _ := a.do(t, req{method: "POST", path: "/api/auth/setup", body: body, gateway: true, csrf: true})
	if w.Code != 200 {
		t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "HttpOnly") || !strings.Contains(w.Header().Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatalf("cookie flags: %s", w.Header().Get("Set-Cookie"))
	}
	// A second setup is refused.
	if w, _ := a.do(t, req{method: "POST", path: "/api/auth/setup", body: body, gateway: true, csrf: true}); w.Code != 409 {
		t.Fatalf("second setup: %d", w.Code)
	}
}

func login(t *testing.T, a *Agent) string {
	t.Helper()
	if err := a.auth.SetPassword(context.Background(), "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	w, _ := a.do(t, req{method: "POST", path: "/api/auth/login", body: `{"password":"correct horse battery staple"}`, gateway: true, csrf: true})
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieInsecure {
			return c.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}

func TestSessionAndCSRF(t *testing.T) {
	a := newTestAgent(t)
	sess := login(t, a)
	if w, _ := a.do(t, req{method: "GET", path: "/api/status", gateway: true, cookie: sess}); w.Code != 200 {
		t.Fatalf("status with session: %d", w.Code)
	}
	// State changes with a cookie need the CSRF header.
	if w, _ := a.do(t, req{method: "PUT", path: "/api/settings", body: `{"bypassIran":false}`, gateway: true, cookie: sess}); w.Code != 403 {
		t.Fatalf("PUT without CSRF: %d", w.Code)
	}
	if w, _ := a.do(t, req{method: "PUT", path: "/api/settings", body: `{"bypassIran":false}`, gateway: true, cookie: sess, csrf: true}); w.Code != 200 {
		t.Fatalf("PUT with CSRF: %d %s", w.Code, w.Body)
	}
	// Unknown fields are rejected (strict decoding).
	if w, _ := a.do(t, req{method: "PUT", path: "/api/settings", body: `{"bypassIran":true,"evil":1}`, gateway: true, cookie: sess, csrf: true}); w.Code != 400 {
		t.Fatalf("unknown field: %d", w.Code)
	}
	// Logout kills the session.
	a.do(t, req{method: "POST", path: "/api/auth/logout", gateway: true, cookie: sess, csrf: true})
	if w, _ := a.do(t, req{method: "GET", path: "/api/status", gateway: true, cookie: sess}); w.Code != 401 {
		t.Fatalf("after logout: %d", w.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	a := newTestAgent(t)
	_ = a.auth.SetPassword(context.Background(), "correct horse battery staple")
	var last int
	for i := 0; i < 6; i++ {
		w, _ := a.do(t, req{method: "POST", path: "/api/auth/login", body: `{"password":"wrong password!!"}`, gateway: true, csrf: true})
		last = w.Code
	}
	if last != 429 {
		t.Fatalf("expected lockout after repeated failures, got %d", last)
	}
	// Even the right password is refused while locked.
	if w, _ := a.do(t, req{method: "POST", path: "/api/auth/login", body: `{"password":"correct horse battery staple"}`, gateway: true, csrf: true}); w.Code != 429 {
		t.Fatalf("locked login: %d", w.Code)
	}
}

func TestLimiterBlocksParallelAndGroupsIPv6(t *testing.T) {
	s := newAuthState(nil)
	done, _, ok := s.beginAttempt("2001:db8::1")
	if !ok {
		t.Fatal("first attempt refused")
	}
	// Same /64, different address: still the same client while one runs.
	if _, _, ok := s.beginAttempt("2001:db8::ffff"); ok {
		t.Fatal("parallel attempt from the same /64 allowed")
	}
	done()
	if _, _, ok := s.beginAttempt("2001:db8::ffff"); !ok {
		t.Fatal("attempt after the first finished refused")
	}
}

func TestGlobalBrakeSparesKnownClients(t *testing.T) {
	s := newAuthState(nil)
	s.recordSuccess("198.51.100.1")
	for i := 0; i < 120; i++ {
		s.recordFailure("203.0.113." + strconv.Itoa(i%250))
	}
	if _, _, ok := s.beginAttempt("192.0.2.77"); ok {
		t.Fatal("unknown client allowed during a distributed attack")
	}
	if done, _, ok := s.beginAttempt("198.51.100.1"); !ok {
		t.Fatal("known admin client locked out by the global brake")
	} else {
		done()
	}
}

func TestTokenScopes(t *testing.T) {
	a := newTestAgent(t)
	ctx := context.Background()
	readTok, _, _ := a.auth.createToken(ctx, "reader", scopeRead)
	adminTok, _, _ := a.auth.createToken(ctx, "admin", scopeAdmin)

	if w, _ := a.do(t, req{method: "GET", path: "/api/status", gateway: true, bearer: readTok}); w.Code != 200 {
		t.Fatalf("read token GET: %d", w.Code)
	}
	if w, _ := a.do(t, req{method: "PUT", path: "/api/settings", body: `{"bypassIran":false}`, gateway: true, bearer: readTok}); w.Code != 403 {
		t.Fatalf("read token PUT: %d", w.Code)
	}
	if w, _ := a.do(t, req{method: "PUT", path: "/api/settings", body: `{"bypassIran":false}`, gateway: true, bearer: adminTok}); w.Code != 200 {
		t.Fatalf("admin token PUT: %d %s", w.Code, w.Body)
	}
	// No token may manage credentials.
	if w, _ := a.do(t, req{method: "POST", path: "/api/tokens", body: `{"name":"x"}`, gateway: true, bearer: adminTok}); w.Code != 403 {
		t.Fatalf("token creating tokens: %d", w.Code)
	}
	if w, _ := a.do(t, req{method: "POST", path: "/api/auth/password", body: `{"currentPassword":"","newPassword":"aaaaaaaaaaaaaaa"}`, gateway: true, bearer: adminTok}); w.Code != 403 {
		t.Fatalf("token changing password: %d", w.Code)
	}
	if w, _ := a.do(t, req{method: "GET", path: "/api/status", gateway: true, bearer: "v2d_" + strings.Repeat("0", 48)}); w.Code != 401 {
		t.Fatalf("bogus token: %d", w.Code)
	}
}

func TestSubscriptionURLNotExposed(t *testing.T) {
	for _, u := range []string{"https://panel.example.com/sub/S3CR3T-TOKEN-1234?x=1", "https://panel.example.com/S3?token=S3CR3T"} {
		if got := maskURL(u); strings.Contains(got, "S3") || strings.Contains(got, "x=1") || got != "https://panel.example.com/…" {
			t.Fatalf("mask leaked secret: %s", got)
		}
	}
}

func TestSubscriptionFetchRefusesHostServices(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "169.254.169.254", "0.0.0.0", "fe80::1", "100.100.100.200", "224.0.0.1"} {
		if !blockedFetchIP(net.ParseIP(ip)) {
			t.Errorf("%s should be refused", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "192.168.1.10", "172.31.250.11"} {
		if blockedFetchIP(net.ParseIP(ip)) {
			t.Errorf("%s should be allowed", ip)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := fetchWith(context.Background(), directClient(), srv.URL); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("loopback fetch was not refused: %v", err)
	}
}

func TestTitleSanitised(t *testing.T) {
	if got := parseTitle("base64:" + base64.StdEncoding.EncodeToString([]byte("My]0;pwned Sub‮"))); got != "My]0;pwned Sub" {
		t.Fatalf("got %q", got)
	}
}

func TestTOTP(t *testing.T) {
	secret := newTOTPSecret()
	now := time.Now()
	code, _ := totpCode(secret, now.Unix()/30)
	if matchTOTP(secret, code, now) < 0 {
		t.Fatal("current code rejected")
	}
	if matchTOTP(secret, "000000", now) >= 0 && code != "000000" {
		t.Fatal("wrong code accepted")
	}
	old, _ := totpCode(secret, now.Unix()/30-5)
	if matchTOTP(secret, old, now) >= 0 && old != code {
		t.Fatal("stale code accepted")
	}
}

func TestPasswordHash(t *testing.T) {
	s := newAuthState(nil)
	h, err := s.hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") || !s.verifyPassword(h, "correct horse battery staple") || s.verifyPassword(h, "wrong") {
		t.Fatal("argon2id round trip failed")
	}
}

func TestContainerKey(t *testing.T) {
	c := docker.Container{Name: "abc-123", Labels: map[string]string{"com.docker.compose.project": "x1y2", "com.docker.compose.service": "web", "coolify.resourceName": "my-shop"}}
	if k := ContainerKey(c); k != "compose:x1y2/web" || !validKey(k) {
		t.Fatalf("key %q", k)
	}
	if l := ContainerLabel(c); l != "my-shop" {
		t.Fatalf("label %q", l)
	}
	if k := ContainerKey(docker.Container{Name: "plain"}); k != "name:plain" {
		t.Fatalf("key %q", k)
	}
	for _, bad := range []string{"compose:../x", "name:a b", "other:x", "name:", "name:a;rm"} {
		if validKey(bad) && !strings.Contains(bad, "..") {
			t.Errorf("accepted %q", bad)
		}
	}
	if routable(docker.Container{NetworkMode: "host", State: "running"}) {
		t.Fatal("host-network container must not be routable")
	}
	if routable(docker.Container{Labels: map[string]string{RoleLabel: "web"}, State: "running"}) {
		t.Fatal("v2dock's own container must not be routable")
	}
}

func TestPeriodStart(t *testing.T) {
	ts := time.Date(2026, 11, 4, 15, 30, 0, 0, time.Local) // a Wednesday
	if got := time.Unix(periodStart("daily", ts, 0), 0); got.Hour() != 0 || got.Day() != 4 {
		t.Fatalf("daily %v", got)
	}
	if got := time.Unix(periodStart("weekly", ts, 0), 0); got.Weekday() != time.Saturday {
		t.Fatalf("weekly starts %v", got.Weekday())
	}
	if got := time.Unix(periodStart("monthly", ts, 0), 0); got.Day() != 1 {
		t.Fatalf("monthly %v", got)
	}
}

func coolifyApp(uuid, ts string) docker.Container {
	return docker.Container{
		Name: uuid + "-" + ts,
		Labels: map[string]string{
			"com.docker.compose.project": uuid,
			"com.docker.compose.service": uuid + "-" + ts,
			"coolify.type":               "application",
			"coolify.applicationUuid":    uuid,
			"coolify.pullRequestId":      "0",
		},
	}
}

func TestCoolifyAppKeyIsStableAcrossDeploys(t *testing.T) {
	a, b := coolifyApp("ab59unrtws", "20261107T123316"), coolifyApp("ab59unrtws", "20261107T124001")
	if ContainerKey(a) != "coolify:app/ab59unrtws" || ContainerKey(a) != ContainerKey(b) || !validKey(ContainerKey(a)) {
		t.Fatalf("keys %q %q", ContainerKey(a), ContainerKey(b))
	}
	if legacyKey(a) == legacyKey(b) {
		t.Fatal("test premise: the compose key does change per deploy")
	}
	// Pull-request previews and Coolify services keep their compose keys.
	pr := coolifyApp("ab59unrtws", "x")
	pr.Labels["coolify.pullRequestId"] = "12"
	svc := docker.Container{Name: "n", Labels: map[string]string{"com.docker.compose.project": "svc1", "com.docker.compose.service": "web", "coolify.type": "service"}}
	if !strings.HasPrefix(ContainerKey(pr), "compose:") || ContainerKey(svc) != "compose:svc1/web" {
		t.Fatalf("pr %q svc %q", ContainerKey(pr), ContainerKey(svc))
	}
	bad := coolifyApp("a/../b", "x")
	if strings.HasPrefix(ContainerKey(bad), "coolify:") {
		t.Fatalf("unsafe uuid accepted: %q", ContainerKey(bad))
	}
}

func TestMigrateAppKeysKeepsTheNewestOldRoute(t *testing.T) {
	a := newTestAgent(t)
	ctx := context.Background()
	for i, k := range []string{
		"compose:ab59unrtws/ab59unrtws-20261107T120000", // oldest
		"compose:ab59unrtws/ab59unrtws-20261107T123316", // newest
		"compose:other/other-20261107T120000",           // another app: untouched
	} {
		if err := a.db.UpsertRoute(ctx, store.Route{Key: k, Label: k, QuotaPeriod: "monthly", QuotaAction: "block"}, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.ExecContext(ctx, `UPDATE routes SET created_at = ? WHERE key = ?`, 1000+i, k); err != nil {
			t.Fatal(err)
		}
	}
	running := []docker.Container{coolifyApp("ab59unrtws", "20261107T124001")}
	a.migrateAppKeys(ctx, running)

	if _, err := a.db.GetRoute(ctx, "coolify:app/ab59unrtws"); err != nil {
		t.Fatalf("stable route missing: %v", err)
	}
	if _, err := a.db.GetRoute(ctx, "compose:ab59unrtws/ab59unrtws-20261107T123316"); err == nil {
		t.Fatal("newest old route should have been renamed")
	}
	if _, err := a.db.GetRoute(ctx, "compose:ab59unrtws/ab59unrtws-20261107T120000"); err != nil {
		t.Fatal("older ghost route must be left alone")
	}
	if _, err := a.db.GetRoute(ctx, "compose:other/other-20261107T120000"); err != nil {
		t.Fatal("other apps must not be touched")
	}
	// Running it again is a no-op.
	a.migrateAppKeys(ctx, running)
	rs, _ := a.db.ListRoutes(ctx)
	if len(rs) != 3 {
		t.Fatalf("routes %d", len(rs))
	}
}
