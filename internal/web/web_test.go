package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func gw() *gateway {
	return &gateway{cfg: Config{TrustedProxies: DefaultTrustedProxies()}}
}

func TestClientIPCannotBeSpoofed(t *testing.T) {
	g := gw()
	// A direct (untrusted) client: XFF is ignored.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.9:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := g.clientIP(r); got != netip.MustParseAddr("198.51.100.9") {
		t.Fatalf("got %v", got)
	}
	// Behind Traefik: the rightmost untrusted address wins, so a client
	// prepending its own XFF value cannot choose its IP.
	r = httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.2:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.50")
	if got := g.clientIP(r); got != netip.MustParseAddr("203.0.113.50") {
		t.Fatalf("got %v", got)
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		method, origin, site, host string
		ok                         bool
	}{
		{"GET", "https://evil.example", "cross-site", "v2dock.example", true},
		{"POST", "", "", "v2dock.example", true},
		{"POST", "https://v2dock.example", "same-origin", "v2dock.example", true},
		{"POST", "https://evil.example", "", "v2dock.example", false},
		{"POST", "", "cross-site", "v2dock.example", false},
		{"DELETE", "null", "", "v2dock.example", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://"+c.host+"/api/x", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		if got := gw().sameOrigin(r); got != c.ok {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}

func TestForwardedHostOnlyFromProxy(t *testing.T) {
	g := gw()
	r := httptest.NewRequest("POST", "http://v2dock.example/api/x", nil)
	r.RemoteAddr = "198.51.100.9:4000" // not a proxy
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("X-Forwarded-Host", "evil.example")
	if g.sameOrigin(r) {
		t.Fatal("untrusted X-Forwarded-Host accepted")
	}
	r.RemoteAddr = "172.18.0.2:4000" // Traefik
	r.Header.Set("Origin", "https://v2dock.example")
	r.Header.Set("X-Forwarded-Host", "v2dock.example")
	if !g.sameOrigin(r) {
		t.Fatal("proxied same-origin request refused")
	}
}

func TestSecurityHeadersAndNoCORS(t *testing.T) {
	h := Handler(Config{AgentSocket: "/nonexistent.sock", TrustedProxies: DefaultTrustedProxies()})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	for _, k := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy"} {
		if w.Header().Get(k) == "" {
			t.Errorf("missing %s", k)
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Error("CSP must restrict scripts to self")
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("OPTIONS", "/api/status", nil)
	r.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
}
