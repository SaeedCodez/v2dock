package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/SaeedCodez/v2dock/internal/links"
	"github.com/SaeedCodez/v2dock/internal/store"
	"github.com/SaeedCodez/v2dock/internal/wire"
	"github.com/SaeedCodez/v2dock/internal/xcore"
)

type ctxKey struct{}

func who(r *http.Request) *principal {
	p, _ := r.Context().Value(ctxKey{}).(*principal)
	return p
}

type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(msg string) error   { return &apiError{http.StatusBadRequest, "invalid", msg} }
func notFound(msg string) error     { return &apiError{http.StatusNotFound, "not_found", msg} }
func conflict(code, m string) error { return &apiError{http.StatusConflict, code, m} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		writeJSON(w, ae.status, map[string]any{"ok": false, "code": ae.code, "error": ae.msg})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "code": "not_found", "error": "not found"})
	case errors.Is(err, errBusy):
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "code": "busy", "error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "code": "error", "error": err.Error()})
	}
}

// decode reads a JSON body strictly: bounded size, no unknown fields.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return badRequest("request body is empty")
		}
		return badRequest("invalid JSON: " + err.Error())
	}
	return nil
}

type handler func(w http.ResponseWriter, r *http.Request) error

func (a *Agent) wrap(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeErr(w, err)
		}
	}
}

// public endpoints need no login when reached through the gateway.
var publicPaths = map[string]bool{
	"GET /api/auth/status": true,
	"POST /api/auth/login": true,
	"POST /api/auth/setup": true,
	"GET /api/healthz":     true,
}

// authenticate resolves the caller. Requests without the gateway header were
// made over the unix socket by a local process (the CLI via docker exec) and
// are trusted: anyone who can reach that socket is already root on the host.
func (a *Agent) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(wire.HeaderGateway) == "" {
			ctx := context.WithValue(r.Context(), ctxKey{}, &principal{kind: "local", scope: scopeAdmin})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		ip := r.Header.Get(wire.HeaderClientIP)
		if _, err := netip.ParseAddr(ip); err != nil {
			ip = "unknown"
		}
		p := &principal{kind: "anon", ip: ip}
		if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, "Bearer ") {
			t, ok := a.auth.checkToken(r.Context(), strings.TrimSpace(authz[7:]))
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "code": "unauthorized", "error": "invalid API token"})
				return
			}
			p = &principal{kind: "token", scope: t.Scope, name: t.Name, ip: ip}
		} else if raw := sessionCookie(r); raw != "" {
			if h, ok := a.auth.checkSession(r.Context(), raw); ok {
				p = &principal{kind: "session", scope: scopeAdmin, ip: ip, sessionHash: h}
			}
		}
		pattern := r.Method + " " + r.URL.Path
		if p.kind == "anon" && !publicPaths[pattern] {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "code": "unauthorized", "error": "log in first"})
			return
		}
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead
		if unsafe && p.kind != "token" && r.Header.Get(wire.HeaderCSRF) != "1" {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "code": "csrf", "error": "missing CSRF header"})
			return
		}
		if unsafe && p.kind == "token" && p.scope != scopeAdmin {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "code": "forbidden", "error": "this token is read-only"})
			return
		}
		// Credentials management is for the logged-in admin only.
		if p.kind == "token" && (strings.HasPrefix(r.URL.Path, "/api/tokens") || strings.HasPrefix(r.URL.Path, "/api/auth/")) && r.URL.Path != "/api/auth/status" {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "code": "forbidden", "error": "API tokens cannot manage credentials"})
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, p)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

const (
	cookieSecure   = "__Host-v2dock"
	cookieInsecure = "v2dock"
)

func sessionCookie(r *http.Request) string {
	for _, n := range []string{cookieSecure, cookieInsecure} {
		if c, err := r.Cookie(n); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	secure := r.Header.Get(wire.HeaderProto) == "https"
	name := cookieInsecure
	if secure {
		name = cookieSecure
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

func (a *Agent) routes() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn handler) { mux.Handle(pattern, a.wrap(fn)) }

	h("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, 200, map[string]any{"ok": true})
		return nil
	})
	h("GET /api/auth/status", a.hAuthStatus)
	h("POST /api/auth/setup", a.hSetup)
	h("POST /api/auth/login", a.hLogin)
	h("POST /api/auth/logout", a.hLogout)
	h("POST /api/auth/password", a.hChangePassword)
	h("POST /api/auth/sessions/revoke", a.hRevokeSessions)
	h("POST /api/auth/totp/setup", a.hTOTPSetup)
	h("POST /api/auth/totp/enable", a.hTOTPEnable)
	h("POST /api/auth/totp/disable", a.hTOTPDisable)
	h("GET /api/tokens", a.hListTokens)
	h("POST /api/tokens", a.hCreateToken)
	h("DELETE /api/tokens/{id}", a.hDeleteToken)

	h("GET /api/status", a.hStatus)
	h("GET /api/diagnostics", a.hDiagnostics)
	h("GET /api/settings", a.hGetSettings)
	h("PUT /api/settings", a.hPutSettings)
	h("GET /api/events", a.hEvents)

	h("GET /api/subscriptions", a.hListSubs)
	h("POST /api/subscriptions", a.hCreateSub)
	h("PUT /api/subscriptions/{id}", a.hUpdateSub)
	h("DELETE /api/subscriptions/{id}", a.hDeleteSub)
	h("POST /api/subscriptions/{id}/refresh", a.hRefreshSub)

	h("GET /api/servers", a.hListServers)
	h("POST /api/servers", a.hAddServer)
	h("DELETE /api/servers/{id}", a.hDeleteServer)
	h("POST /api/servers/test", a.hStartTest)
	h("GET /api/servers/test", a.hTestStatus)
	h("DELETE /api/servers/test", a.hCancelTest)
	h("POST /api/connect", a.hConnect)
	h("POST /api/disconnect", a.hDisconnect)

	h("GET /api/containers", a.hContainers)
	h("PUT /api/routes", a.hPutRoute)
	h("DELETE /api/routes", a.hDeleteRoute)
	h("POST /api/routes/reset", a.hResetRoute)
	h("GET /api/usage", a.hUsage)

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "code": "not_found", "error": "no such endpoint"})
	}))
	return a.authenticate(mux)
}

// ===================================================================== auth

func (a *Agent) hAuthStatus(w http.ResponseWriter, r *http.Request) error {
	p := who(r)
	a.mu.Lock()
	hasToken := a.setupToken != ""
	a.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"setupRequired": !a.auth.passwordSet(r.Context()),
		"setupToken":    hasToken,
		"authenticated": p.kind != "anon",
		"via":           p.kind,
		"totpEnabled":   a.auth.totpEnabled(r.Context()),
		"version":       Version,
	})
	return nil
}

func (a *Agent) hSetup(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Password   string `json:"password"`
		SetupToken string `json:"setupToken"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	p := who(r)
	if a.auth.passwordSet(r.Context()) {
		return conflict("already_setup", "a password is already set")
	}
	if p.kind != "local" {
		if ok, wait := a.auth.allowAttempt(p.ip); !ok {
			return &apiError{http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("too many attempts; try again in %s", wait.Round(time.Second))}
		}
		if !a.auth.checkSetupToken(in.SetupToken) {
			a.auth.recordFailure(p.ip)
			return &apiError{http.StatusForbidden, "bad_setup_token", "the setup token is wrong (it is printed in the agent's log)"}
		}
	}
	if err := a.auth.SetPassword(r.Context(), in.Password); err != nil {
		return badRequest(err.Error())
	}
	a.auth.clearSetupToken()
	a.event(r.Context(), "info", "security", "Admin password set", p)
	if p.kind != "local" {
		raw, err := a.auth.newSession(r.Context(), p.ip, r.UserAgent())
		if err != nil {
			return err
		}
		setSessionCookie(w, r, raw, int(sessionTTL.Seconds()))
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hLogin(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	p := who(r)
	if ok, wait := a.auth.allowAttempt(p.ip); !ok {
		return &apiError{http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("too many failed attempts; try again in %s", wait.Round(time.Second))}
	}
	enc, ok, err := a.db.GetKV(r.Context(), kvPassword)
	if err != nil {
		return err
	}
	if !ok {
		return conflict("setup_required", "no password is set yet")
	}
	if len(in.Password) > maxPasswordLen || !a.auth.verifyPassword(enc, in.Password) {
		a.auth.recordFailure(p.ip)
		a.event(r.Context(), "warn", "security", "Failed login", p)
		time.Sleep(400 * time.Millisecond)
		return &apiError{http.StatusUnauthorized, "bad_credentials", "wrong password"}
	}
	if a.auth.totpEnabled(r.Context()) {
		if in.Code == "" {
			return &apiError{http.StatusUnauthorized, "totp_required", "enter the code from your authenticator app"}
		}
		if !a.auth.verifyTOTP(r.Context(), in.Code) {
			a.auth.recordFailure(p.ip)
			a.event(r.Context(), "warn", "security", "Failed login (wrong 2FA code)", p)
			return &apiError{http.StatusUnauthorized, "bad_totp", "wrong or reused code"}
		}
	}
	a.auth.recordSuccess(p.ip)
	// Rotate: any session the browser already had is dropped.
	if old := sessionCookie(r); old != "" {
		_ = a.db.DeleteSession(r.Context(), sha256hex(old))
	}
	raw, err := a.auth.newSession(r.Context(), p.ip, r.UserAgent())
	if err != nil {
		return err
	}
	setSessionCookie(w, r, raw, int(sessionTTL.Seconds()))
	a.event(r.Context(), "info", "security", "Logged in", p)
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hLogout(w http.ResponseWriter, r *http.Request) error {
	if p := who(r); p.sessionHash != "" {
		_ = a.db.DeleteSession(r.Context(), p.sessionHash)
	}
	setSessionCookie(w, r, "", -1)
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hChangePassword(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Current string `json:"currentPassword"`
		Next    string `json:"newPassword"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	p := who(r)
	if p.kind != "local" {
		if ok, wait := a.auth.allowAttempt(p.ip); !ok {
			return &apiError{http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("try again in %s", wait.Round(time.Second))}
		}
		enc, _, _ := a.db.GetKV(r.Context(), kvPassword)
		if !a.auth.verifyPassword(enc, in.Current) {
			a.auth.recordFailure(p.ip)
			return badRequest("current password is wrong")
		}
	}
	if err := a.auth.SetPassword(r.Context(), in.Next); err != nil {
		return badRequest(err.Error())
	}
	a.event(r.Context(), "info", "security", "Password changed; all sessions ended", p)
	if p.kind == "session" {
		raw, err := a.auth.newSession(r.Context(), p.ip, r.UserAgent())
		if err == nil {
			setSessionCookie(w, r, raw, int(sessionTTL.Seconds()))
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hRevokeSessions(w http.ResponseWriter, r *http.Request) error {
	if err := a.db.DeleteAllSessions(r.Context()); err != nil {
		return err
	}
	a.event(r.Context(), "info", "security", "All sessions ended", who(r))
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hTOTPSetup(w http.ResponseWriter, r *http.Request) error {
	if a.auth.totpEnabled(r.Context()) {
		return conflict("totp_enabled", "two-factor authentication is already on")
	}
	secret := newTOTPSecret()
	if err := a.db.SetKV(r.Context(), kvTOTPPend, secret); err != nil {
		return err
	}
	uri := "otpauth://totp/v2dock:admin?secret=" + secret + "&issuer=v2dock&algorithm=SHA1&digits=6&period=30"
	resp := map[string]any{"secret": secret, "uri": uri}
	if code, err := qr.Encode(uri, qr.M); err == nil {
		code.Scale = 6
		resp["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
	}
	writeJSON(w, 200, resp)
	return nil
}

func (a *Agent) hTOTPEnable(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	secret, ok, _ := a.db.GetKV(r.Context(), kvTOTPPend)
	if !ok {
		return badRequest("start the setup first")
	}
	step := matchTOTP(secret, in.Code, time.Now())
	if step < 0 {
		return badRequest("that code is not right; check the time on your phone")
	}
	if err := a.db.SetKV(r.Context(), kvTOTP, secret); err != nil {
		return err
	}
	_ = a.db.SetKV(r.Context(), kvTOTPLast, strconv.FormatInt(step, 10))
	_ = a.db.DeleteKV(r.Context(), kvTOTPPend)
	a.event(r.Context(), "info", "security", "Two-factor authentication turned on", who(r))
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hTOTPDisable(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	p := who(r)
	if p.kind != "local" {
		if ok, wait := a.auth.allowAttempt(p.ip); !ok {
			return &apiError{http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("try again in %s", wait.Round(time.Second))}
		}
		enc, _, _ := a.db.GetKV(r.Context(), kvPassword)
		if !a.auth.verifyPassword(enc, in.Password) || !a.auth.verifyTOTP(r.Context(), in.Code) {
			a.auth.recordFailure(p.ip)
			return badRequest("password or code is wrong")
		}
	}
	_ = a.db.DeleteKV(r.Context(), kvTOTP)
	_ = a.db.DeleteKV(r.Context(), kvTOTPLast)
	a.event(r.Context(), "warn", "security", "Two-factor authentication turned off", p)
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hListTokens(w http.ResponseWriter, r *http.Request) error {
	ts, err := a.db.ListTokens(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"tokens": ts})
	return nil
}

func (a *Agent) hCreateToken(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 60 {
		return badRequest("name must be 1-60 characters")
	}
	if in.Scope == "" {
		in.Scope = scopeRead
	}
	if in.Scope != scopeRead && in.Scope != scopeAdmin {
		return badRequest("scope must be read or admin")
	}
	raw, t, err := a.auth.createToken(r.Context(), in.Name, in.Scope)
	if err != nil {
		return err
	}
	a.event(r.Context(), "info", "security", fmt.Sprintf("API token %q created (%s)", in.Name, in.Scope), who(r))
	writeJSON(w, 200, map[string]any{"ok": true, "token": raw, "info": t})
	return nil
}

func (a *Agent) hDeleteToken(w http.ResponseWriter, r *http.Request) error {
	if err := a.db.DeleteToken(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	a.event(r.Context(), "info", "security", "API token revoked", who(r))
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

// =================================================================== status

type serverRef struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	Address        string `json:"address"`
	SubscriptionID string `json:"subscriptionId"`
}

func (a *Agent) hStatus(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	s, err := a.db.LoadSettings(ctx)
	if err != nil {
		return err
	}
	routes, _ := a.db.ListRoutes(ctx)
	totals, _ := a.db.UsageTotals(ctx, startOfDay(time.Now()))
	var vpn, direct int64
	for _, t := range totals {
		vpn += t.VPN
		direct += t.Direct
	}
	job, last := a.jobSnapshot()
	a.mu.Lock()
	running, runErr, fwErr, h := a.running, a.runError, a.fwError, a.health
	dockerErr, nContainers := a.dockerErr, len(a.containers)
	routedRunning := 0
	routeKeys := map[string]bool{}
	for _, rt := range routes {
		routeKeys[rt.Key] = true
	}
	for _, c := range a.containers {
		if c.State == "running" && routeKeys[ContainerKey(c)] {
			routedRunning++
		}
	}
	a.mu.Unlock()
	var srv *serverRef
	if s.ActiveServerID != "" {
		srv = &serverRef{ID: s.ActiveServerID, Name: s.ActiveName, Protocol: s.ActiveProtocol, Address: s.ActiveAddress, SubscriptionID: s.ActiveSubID}
	}
	exceeded := 0
	for _, rt := range routes {
		if rt.Exceeded {
			exceeded++
		}
	}
	writeJSON(w, 200, map[string]any{
		"version":       Version,
		"connected":     s.Connected,
		"running":       running,
		"error":         runErr,
		"firewallError": fwErr,
		"server":        srv,
		"connectedAt":   s.ConnectedAt,
		"health":        h,
		"bypassIran":    s.BypassIran,
		"autoFailover":  s.AutoFailover,
		"routes":        len(routes),
		"routedRunning": routedRunning,
		"quotaExceeded": exceeded,
		"today":         map[string]int64{"vpn": vpn, "direct": direct},
		"docker":        map[string]any{"ok": dockerErr == "", "error": dockerErr, "containers": nContainers},
		"test":          job,
		"lastTest":      last,
	})
	return nil
}

func (a *Agent) hDiagnostics(w http.ResponseWriter, r *http.Request) error {
	out := map[string]any{"version": Version, "proxyPorts": []int{a.cfg.TProxyPort, a.cfg.TProxyPort + 1}, "firewallManaged": !a.cfg.NoFirewall}
	if !a.cfg.NoFirewall {
		out["firewall"] = a.fw.Status(r.Context())
	}
	if err := a.docker.Ping(r.Context()); err != nil {
		out["docker"] = err.Error()
	} else {
		out["docker"] = "ok"
	}
	s, plans, clients, err := a.desired(r.Context())
	if err == nil {
		out["clients"] = clients
		type rp struct {
			Key  string       `json:"key"`
			IPs  []netip.Addr `json:"ips"`
			Mode string       `json:"mode"`
		}
		var ps []rp
		for _, p := range plans {
			ps = append(ps, rp{p.Key, p.IPs, p.Mode})
		}
		out["routes"] = ps
		out["bypassIran"] = s.BypassIran
	}
	writeJSON(w, 200, out)
	return nil
}

func startOfDay(t time.Time) time.Time {
	t = t.In(time.Local)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// ================================================================= settings

func publicSettings(s store.Settings) map[string]any {
	return map[string]any{
		"bypassIran":        s.BypassIran,
		"remoteDns":         s.RemoteDNS,
		"domesticDns":       s.DomesticDNS,
		"testUrl":           s.TestURL,
		"autoFailover":      s.AutoFailover,
		"failoverThreshold": s.FailoverThreshold,
		"failoverScope":     s.FailoverScope,
		"healthIntervalSec": s.HealthIntervalSec,
	}
}

func (a *Agent) hGetSettings(w http.ResponseWriter, r *http.Request) error {
	s, err := a.db.LoadSettings(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, publicSettings(s))
	return nil
}

func validDNS(v string, allowLocal bool) bool {
	if len(v) > 200 {
		return false
	}
	if allowLocal && v == "localhost" {
		return true
	}
	if _, err := netip.ParseAddr(v); err == nil {
		return true
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https", "tcp", "quic+local", "https+local", "tcp+local":
		return true
	}
	return false
}

func (a *Agent) hPutSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		BypassIran        *bool   `json:"bypassIran"`
		RemoteDNS         *string `json:"remoteDns"`
		DomesticDNS       *string `json:"domesticDns"`
		TestURL           *string `json:"testUrl"`
		AutoFailover      *bool   `json:"autoFailover"`
		FailoverThreshold *int    `json:"failoverThreshold"`
		FailoverScope     *string `json:"failoverScope"`
		HealthIntervalSec *int    `json:"healthIntervalSec"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	s, err := a.db.LoadSettings(ctx)
	if err != nil {
		return err
	}
	var changes []string
	needReconcile := false
	if in.BypassIran != nil && *in.BypassIran != s.BypassIran {
		s.BypassIran = *in.BypassIran
		needReconcile = true
		changes = append(changes, fmt.Sprintf("Iran bypass %s", onOff(s.BypassIran)))
	}
	if in.RemoteDNS != nil {
		v := strings.TrimSpace(*in.RemoteDNS)
		if !validDNS(v, false) {
			return badRequest("remote DNS must be an IP address or an https:// or tcp:// resolver")
		}
		if v != s.RemoteDNS {
			s.RemoteDNS, needReconcile = v, true
			changes = append(changes, "remote DNS "+v)
		}
	}
	if in.DomesticDNS != nil {
		v := strings.TrimSpace(*in.DomesticDNS)
		if !validDNS(v, true) {
			return badRequest("domestic DNS must be localhost, an IP address or an https:// resolver")
		}
		if v != s.DomesticDNS {
			s.DomesticDNS, needReconcile = v, true
			changes = append(changes, "domestic DNS "+v)
		}
	}
	if in.TestURL != nil {
		v := strings.TrimSpace(*in.TestURL)
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(v) > 300 {
			return badRequest("test URL must be an http(s) URL")
		}
		s.TestURL = v
		changes = append(changes, "test URL")
	}
	if in.AutoFailover != nil && *in.AutoFailover != s.AutoFailover {
		s.AutoFailover = *in.AutoFailover
		changes = append(changes, "auto failover "+onOff(s.AutoFailover))
	}
	if in.FailoverThreshold != nil {
		if *in.FailoverThreshold < 1 || *in.FailoverThreshold > 20 {
			return badRequest("failover threshold must be 1-20")
		}
		s.FailoverThreshold = *in.FailoverThreshold
	}
	if in.FailoverScope != nil {
		if *in.FailoverScope != "all" && *in.FailoverScope != "subscription" {
			return badRequest("failover scope must be all or subscription")
		}
		s.FailoverScope = *in.FailoverScope
	}
	if in.HealthIntervalSec != nil {
		if *in.HealthIntervalSec < 10 || *in.HealthIntervalSec > 600 {
			return badRequest("health check interval must be 10-600 seconds")
		}
		s.HealthIntervalSec = *in.HealthIntervalSec
	}
	if err := a.db.SaveSettings(ctx, s); err != nil {
		return err
	}
	if len(changes) > 0 {
		a.event(ctx, "info", "settings", "Settings changed: "+strings.Join(changes, ", "), who(r))
	}
	if needReconcile {
		if err := a.Reconcile(ctx); err != nil {
			return err
		}
	}
	writeJSON(w, 200, publicSettings(s))
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (a *Agent) hEvents(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	evs, err := a.db.ListEvents(r.Context(), limit, before, q.Get("kind"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"events": evs})
	return nil
}

// ============================================================ subscriptions

type subView struct {
	store.Subscription
	URLMasked string `json:"url"`
}

func (a *Agent) hListSubs(w http.ResponseWriter, r *http.Request) error {
	subs, err := a.db.ListSubscriptions(r.Context())
	if err != nil {
		return err
	}
	out := make([]subView, 0, len(subs))
	for _, s := range subs {
		out = append(out, subView{s, maskURL(s.URL)})
	}
	writeJSON(w, 200, map[string]any{"subscriptions": out})
	return nil
}

func cleanLabel(s string, max int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s))
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func (a *Agent) hCreateSub(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name            string `json:"name"`
		URL             string `json:"url"`
		AutoUpdateHours *int   `json:"autoUpdateHours"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	u, err := validateSubURL(in.URL)
	if err != nil {
		return badRequest(err.Error())
	}
	hours := 12
	if in.AutoUpdateHours != nil {
		hours = *in.AutoUpdateHours
	}
	if hours < 0 || hours > 24*30 {
		return badRequest("auto update must be 0-720 hours")
	}
	name := cleanLabel(in.Name, 60)
	if name == "" {
		name = "Subscription " + u.Hostname()
	}
	sub := store.Subscription{ID: randomID(4), Name: name, URL: u.String(), Kind: "url", AutoUpdateHours: hours}
	if err := a.db.CreateSubscription(r.Context(), sub); err != nil {
		return err
	}
	a.event(r.Context(), "info", "subscription", "Added subscription "+name, who(r))
	n, ferr := a.refreshSubscription(r.Context(), sub, who(r))
	got, _ := a.db.GetSubscription(r.Context(), sub.ID)
	resp := map[string]any{"ok": true, "subscription": subView{got, maskURL(got.URL)}, "servers": n}
	if ferr != nil {
		resp["warning"] = ferr.Error()
	}
	writeJSON(w, 200, resp)
	return nil
}

func (a *Agent) hUpdateSub(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name            *string `json:"name"`
		URL             *string `json:"url"`
		AutoUpdateHours *int    `json:"autoUpdateHours"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	sub, err := a.db.GetSubscription(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if in.Name != nil {
		if n := cleanLabel(*in.Name, 60); n != "" {
			sub.Name = n
		}
	}
	if in.URL != nil && sub.Kind == "url" {
		u, err := validateSubURL(*in.URL)
		if err != nil {
			return badRequest(err.Error())
		}
		sub.URL = u.String()
	}
	if in.AutoUpdateHours != nil {
		if *in.AutoUpdateHours < 0 || *in.AutoUpdateHours > 720 {
			return badRequest("auto update must be 0-720 hours")
		}
		sub.AutoUpdateHours = *in.AutoUpdateHours
	}
	if err := a.db.UpdateSubscription(r.Context(), sub.ID, sub.Name, sub.URL, sub.AutoUpdateHours); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hDeleteSub(w http.ResponseWriter, r *http.Request) error {
	sub, err := a.db.GetSubscription(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := a.db.DeleteSubscription(r.Context(), sub.ID); err != nil {
		return err
	}
	a.event(r.Context(), "info", "subscription", "Removed subscription "+sub.Name, who(r))
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hRefreshSub(w http.ResponseWriter, r *http.Request) error {
	sub, err := a.db.GetSubscription(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	n, err := a.refreshSubscription(r.Context(), sub, who(r))
	if err != nil {
		return &apiError{http.StatusBadGateway, "fetch_failed", err.Error()}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "servers": n})
	return nil
}

// ================================================================== servers

func (a *Agent) hListServers(w http.ResponseWriter, r *http.Request) error {
	servers, err := a.db.ListServers(r.Context(), r.URL.Query().Get("subscription"))
	if err != nil {
		return err
	}
	if servers == nil {
		servers = []store.Server{}
	}
	s, _ := a.db.LoadSettings(r.Context())
	writeJSON(w, 200, map[string]any{"servers": servers, "activeId": s.ActiveServerID, "connected": s.Connected})
	return nil
}

const manualSubID = "manual"

func (a *Agent) hAddServer(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Link string `json:"link"`
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ls, err := links.ParseLink(in.Link)
	if err != nil {
		return badRequest(err.Error())
	}
	if n := cleanLabel(in.Name, 120); n != "" {
		ls.Name = n
	}
	if cfg, err := xcore.BuildProbe(ls.Outbound); err == nil {
		if err := xcore.Validate(cfg); err != nil {
			return badRequest("Xray cannot use this link: " + err.Error())
		}
	}
	ctx := r.Context()
	if _, err := a.db.GetSubscription(ctx, manualSubID); errors.Is(err, store.ErrNotFound) {
		if err := a.db.CreateSubscription(ctx, store.Subscription{ID: manualSubID, Name: "Manual", Kind: "manual"}); err != nil {
			return err
		}
	}
	if err := a.db.AppendServer(ctx, manualSubID, fromLink(ls, manualSubID)); err != nil {
		return err
	}
	a.event(ctx, "info", "subscription", "Added server "+ls.Name, who(r))
	writeJSON(w, 200, map[string]any{"ok": true, "id": ls.ID})
	return nil
}

func (a *Agent) hDeleteServer(w http.ResponseWriter, r *http.Request) error {
	srv, err := a.db.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if srv.SubscriptionID != manualSubID {
		return badRequest("servers from a subscription are managed by the subscription")
	}
	if err := a.db.DeleteServer(r.Context(), manualSubID, srv.ID); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) selectServers(ctx context.Context, subID string, ids []string) ([]store.Server, error) {
	all, err := a.db.ListServers(ctx, subID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return all, nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []store.Server
	for _, s := range all {
		if want[s.ID] {
			out = append(out, s)
			delete(want, s.ID) // same server in two subscriptions: test once
		}
	}
	return out, nil
}

func (a *Agent) hStartTest(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Kind         string   `json:"kind"`
		Subscription string   `json:"subscription"`
		IDs          []string `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Kind != "tcp" && in.Kind != "real" {
		return badRequest("kind must be tcp or real")
	}
	if len(in.IDs) > 5000 {
		return badRequest("too many ids")
	}
	servers, err := a.selectServers(r.Context(), in.Subscription, in.IDs)
	if err != nil {
		return err
	}
	job, err := a.startTests(r.Context(), in.Kind, servers)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "job": job})
	return nil
}

func (a *Agent) hTestStatus(w http.ResponseWriter, r *http.Request) error {
	cur, last := a.jobSnapshot()
	writeJSON(w, 200, map[string]any{"job": cur, "last": last})
	return nil
}

func (a *Agent) hCancelTest(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, map[string]any{"ok": a.cancelTests()})
	return nil
}

func (a *Agent) hConnect(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ServerID     string `json:"serverId"`
		Best         bool   `json:"best"`
		Subscription string `json:"subscription"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	var srv store.Server
	var err error
	switch {
	case in.ServerID != "":
		srv, err = a.db.GetServer(ctx, in.ServerID)
		if err != nil {
			return notFound("no server with that id")
		}
	case in.Best:
		srv, err = a.pickBest(ctx, in.Subscription)
		if err != nil {
			return err
		}
	default:
		return badRequest("give serverId, or best: true")
	}
	if err := a.connect(ctx, srv, who(r), ""); err != nil {
		return &apiError{http.StatusBadGateway, "connect_failed", err.Error()}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "server": serverRef{srv.ID, srv.Name, srv.Protocol, srv.Address, srv.SubscriptionID}})
	return nil
}

// pickBest real-delay tests up to 40 servers and returns the fastest.
func (a *Agent) pickBest(ctx context.Context, subID string) (store.Server, error) {
	servers, err := a.db.ListServers(ctx, subID)
	if err != nil {
		return store.Server{}, err
	}
	if len(servers) == 0 {
		return store.Server{}, badRequest("there are no servers; add a subscription first")
	}
	sort.SliceStable(servers, func(i, j int) bool {
		di, dj := servers[i].DelayMs, servers[j].DelayMs
		if (di != nil) != (dj != nil) {
			return di != nil
		}
		return di != nil && *di < *dj
	})
	if len(servers) > 40 {
		servers = servers[:40]
	}
	s, _ := a.db.LoadSettings(ctx)
	results := a.runTests(ctx, "real", servers, 10, s.TestURL, nil)
	var best *testResult
	for i := range results {
		if results[i].err == nil && (best == nil || results[i].ms < best.ms) {
			best = &results[i]
		}
	}
	if best == nil {
		return store.Server{}, &apiError{http.StatusBadGateway, "no_working_server", "none of the tested servers worked"}
	}
	return best.server, nil
}

func (a *Agent) hDisconnect(w http.ResponseWriter, r *http.Request) error {
	if err := a.disconnect(r.Context(), who(r)); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

// =============================================================== containers

type containerView struct {
	ID          string           `json:"id"`
	Key         string           `json:"key"`
	Name        string           `json:"name"`
	Label       string           `json:"label"`
	Project     string           `json:"project,omitempty"`
	Image       string           `json:"image"`
	State       string           `json:"state"`
	Status      string           `json:"status"`
	NetworkMode string           `json:"networkMode"`
	IPs         []string         `json:"ips"`
	Routable    bool             `json:"routable"`
	Reason      string           `json:"reason,omitempty"`
	Routed      bool             `json:"routed"`
	Route       *store.Route     `json:"route,omitempty"`
	Live        map[string]any   `json:"live,omitempty"`
	Today       store.UsageTotal `json:"today"`
}

func (a *Agent) hContainers(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if r.URL.Query().Get("refresh") == "1" {
		a.refreshContainers(ctx)
	}
	routes, err := a.db.ListRoutes(ctx)
	if err != nil {
		return err
	}
	byKey := map[string]store.Route{}
	for _, rt := range routes {
		byKey[rt.Key] = rt
	}
	totals, _ := a.db.UsageTotals(ctx, startOfDay(time.Now()))
	tot := map[string]store.UsageTotal{}
	for _, t := range totals {
		tot[t.Key] = t
	}
	a.mu.Lock()
	cs := append(a.containers[:0:0], a.containers...)
	live := map[string]liveStats{}
	for id, l := range a.live {
		live[id] = *l
	}
	dockerErr := a.dockerErr
	a.mu.Unlock()

	seenKeys := map[string]bool{}
	out := make([]containerView, 0, len(cs))
	for _, c := range cs {
		k := ContainerKey(c)
		seenKeys[k] = true
		v := containerView{
			ID: c.ID[:min(12, len(c.ID))], Key: k, Name: c.Name, Label: ContainerLabel(c), Project: containerProject(c),
			Image: c.Image, State: c.State, Status: c.Status, NetworkMode: c.NetworkMode,
			Routable: routable(c), Reason: unroutableReason(c), Today: tot[k],
		}
		v.Today.Key = ""
		for _, ip := range c.IPv4s() {
			v.IPs = append(v.IPs, ip.String())
		}
		if rt, ok := byKey[k]; ok {
			rt := rt
			v.Routed, v.Route = true, &rt
		}
		if l, ok := live[c.ID]; ok {
			v.Live = map[string]any{"cpu": l.CPU, "mem": l.Mem, "memLimit": l.MemLimit, "rxRate": l.RxRate, "txRate": l.TxRate}
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Routed != out[j].Routed {
			return out[i].Routed
		}
		if (out[i].State == "running") != (out[j].State == "running") {
			return out[i].State == "running"
		}
		return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
	})
	var missing []store.Route
	for _, rt := range routes {
		if !seenKeys[rt.Key] {
			missing = append(missing, rt)
		}
	}
	writeJSON(w, 200, map[string]any{"containers": out, "missingRoutes": missing, "dockerError": dockerErr})
	return nil
}

func (a *Agent) findContainer(key string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.containers {
		if ContainerKey(c) == key {
			if !routable(c) {
				return unroutableReason(c), false
			}
			return ContainerLabel(c), true
		}
	}
	return "", false
}

var (
	validPeriods = map[string]bool{"daily": true, "weekly": true, "monthly": true, "total": true}
	validActions = map[string]bool{"block": true, "direct": true}
)

func (a *Agent) hPutRoute(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Key         string `json:"key"`
		QuotaBytes  int64  `json:"quotaBytes"`
		QuotaPeriod string `json:"quotaPeriod"`
		QuotaAction string `json:"quotaAction"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !validKey(in.Key) {
		return badRequest("invalid container key")
	}
	ctx := r.Context()
	existing, getErr := a.db.GetRoute(ctx, in.Key)
	label := existing.Label
	if getErr != nil {
		l, ok := a.findContainer(in.Key)
		if !ok {
			if l != "" {
				return badRequest("this container cannot be routed: it " + l)
			}
			return notFound("no container with that key")
		}
		label = l
	}
	if in.QuotaPeriod == "" {
		in.QuotaPeriod = "monthly"
	}
	if in.QuotaAction == "" {
		in.QuotaAction = "block"
	}
	if !validPeriods[in.QuotaPeriod] || !validActions[in.QuotaAction] {
		return badRequest("quotaPeriod must be daily/weekly/monthly/total and quotaAction block/direct")
	}
	if in.QuotaBytes < 0 || in.QuotaBytes > 1<<50 {
		return badRequest("quotaBytes out of range")
	}
	created := time.Now().Unix()
	if getErr == nil {
		created = existing.CreatedAt
	}
	rt := store.Route{Key: in.Key, Label: label, QuotaBytes: in.QuotaBytes, QuotaPeriod: in.QuotaPeriod, QuotaAction: in.QuotaAction}
	if err := a.db.UpsertRoute(ctx, rt, periodStart(in.QuotaPeriod, time.Now(), created)); err != nil {
		return err
	}
	if getErr != nil {
		msg := label + " now uses the VPN"
		if in.QuotaBytes > 0 {
			msg += fmt.Sprintf(" (quota %s %s)", fmtBytes(in.QuotaBytes), in.QuotaPeriod)
		}
		a.event(ctx, "info", "route", msg, who(r))
	} else {
		a.event(ctx, "info", "route", fmt.Sprintf("%s quota set to %s %s (%s when exceeded)", label, quotaText(in.QuotaBytes), in.QuotaPeriod, in.QuotaAction), who(r))
	}
	if err := a.Reconcile(ctx); err != nil {
		return &apiError{http.StatusBadGateway, "apply_failed", err.Error()}
	}
	got, _ := a.db.GetRoute(ctx, in.Key)
	writeJSON(w, 200, map[string]any{"ok": true, "route": got})
	return nil
}

func quotaText(b int64) string {
	if b <= 0 {
		return "unlimited"
	}
	return fmtBytes(b)
}

func (a *Agent) hDeleteRoute(w http.ResponseWriter, r *http.Request) error {
	key := r.URL.Query().Get("key")
	rt, err := a.db.GetRoute(r.Context(), key)
	if err != nil {
		return err
	}
	if err := a.db.DeleteRoute(r.Context(), key); err != nil {
		return err
	}
	a.event(r.Context(), "info", "route", rt.Label+" no longer uses the VPN", who(r))
	if err := a.Reconcile(r.Context()); err != nil {
		return &apiError{http.StatusBadGateway, "apply_failed", err.Error()}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *Agent) hResetRoute(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Key string `json:"key"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	rt, err := a.db.GetRoute(r.Context(), in.Key)
	if err != nil {
		return err
	}
	if err := a.db.ResetRouteUsage(r.Context(), in.Key, time.Now().Unix()); err != nil {
		return err
	}
	a.event(r.Context(), "info", "quota", "Quota usage reset for "+rt.Label, who(r))
	if rt.Exceeded {
		if err := a.Reconcile(r.Context()); err != nil {
			return &apiError{http.StatusBadGateway, "apply_failed", err.Error()}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

// ==================================================================== usage

func (a *Agent) hUsage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	rng := q.Get("range")
	var since time.Time
	var bucket int64
	now := time.Now()
	switch rng {
	case "", "24h":
		rng, since, bucket = "24h", now.Add(-24*time.Hour), 900
	case "1h":
		since, bucket = now.Add(-time.Hour), 60
	case "7d":
		since, bucket = now.Add(-7*24*time.Hour), 3600
	case "30d":
		since, bucket = now.Add(-30*24*time.Hour), 6*3600
	default:
		return badRequest("range must be 1h, 24h, 7d or 30d")
	}
	key := q.Get("key")
	if key != "" && !validKey(key) {
		return badRequest("invalid key")
	}
	ctx := r.Context()
	since = time.Unix(since.Unix()/bucket*bucket, 0)
	series, err := a.db.UsageSeries(ctx, since, bucket, key)
	if err != nil {
		return err
	}
	totals, err := a.db.UsageTotals(ctx, since)
	if err != nil {
		return err
	}
	labels := map[string]string{}
	routes, _ := a.db.ListRoutes(ctx)
	routed := map[string]bool{}
	for _, rt := range routes {
		labels[rt.Key] = rt.Label
		routed[rt.Key] = true
	}
	a.mu.Lock()
	for _, c := range a.containers {
		labels[ContainerKey(c)] = ContainerLabel(c)
	}
	a.mu.Unlock()
	type row struct {
		store.UsageTotal
		Label  string `json:"label"`
		Routed bool   `json:"routed"`
	}
	rows := []row{}
	for _, t := range totals {
		if key != "" && t.Key != key {
			continue
		}
		l := labels[t.Key]
		if l == "" {
			l = strings.SplitN(t.Key, ":", 2)[len(strings.SplitN(t.Key, ":", 2))-1]
		}
		rows = append(rows, row{t, l, routed[t.Key]})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].VPN != rows[j].VPN {
			return rows[i].VPN > rows[j].VPN
		}
		return rows[i].Net > rows[j].Net
	})
	if series == nil {
		series = []store.UsagePoint{}
	}
	writeJSON(w, 200, map[string]any{"range": rng, "bucket": bucket, "since": since.Unix(), "series": series, "containers": rows})
	return nil
}
