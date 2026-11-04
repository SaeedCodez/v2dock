// Package web is the internet-facing gateway: it serves the UI and forwards
// /api to the agent's unix socket. It runs unprivileged, holds no state and
// has no access to Docker or the host network; authentication is decided by
// the agent.
package web

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/SaeedCodez/v2dock/internal/wire"
)

//go:embed all:dist
var dist embed.FS

type Config struct {
	Listen         string
	AgentSocket    string
	TrustedProxies []netip.Prefix
}

// DefaultTrustedProxies covers the private ranges Docker networks (and so
// Coolify's Traefik) use.
func DefaultTrustedProxies() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "::1/128", "fc00::/7"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

type gateway struct {
	cfg    Config
	proxy  *httputil.ReverseProxy
	static http.Handler
	agent  *http.Client
}

func agentTransport(socket string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 150 * time.Second,
	}
}

func Handler(cfg Config) http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	g := &gateway{cfg: cfg, agent: &http.Client{Timeout: 5 * time.Second, Transport: agentTransport(cfg.AgentSocket)}}
	g.static = staticHandler(sub)
	g.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "agent"
			pr.Out.Host = "agent"
			h := pr.Out.Header
			for k := range h {
				lk := strings.ToLower(k)
				if strings.HasPrefix(lk, "x-v2dock-") && lk != strings.ToLower(wire.HeaderCSRF) {
					h.Del(k)
				}
				if strings.HasPrefix(lk, "x-forwarded-") || lk == "forwarded" || lk == "x-real-ip" {
					h.Del(k)
				}
			}
			h.Set(wire.HeaderGateway, "1")
			h.Set(wire.HeaderClientIP, g.clientIP(pr.In).String())
			if g.isHTTPS(pr.In) {
				h.Set(wire.HeaderProto, "https")
			} else {
				h.Set(wire.HeaderProto, "http")
			}
		},
		Transport: agentTransport(cfg.AgentSocket),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("agent unreachable", "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"ok":false,"code":"agent_down","error":"the v2dock agent is not running"}`))
		},
	}
	return g
}

func (g *gateway) trusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range g.cfg.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func remoteAddr(r *http.Request) netip.Addr {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

// clientIP walks X-Forwarded-For from the right, skipping trusted proxies,
// so a client cannot spoof its address by sending the header itself.
func (g *gateway) clientIP(r *http.Request) netip.Addr {
	remote := remoteAddr(r)
	if !g.trusted(remote) {
		return remote
	}
	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			break
		}
		a = a.Unmap()
		if !g.trusted(a) {
			return a
		}
		remote = a
	}
	return remote
}

func (g *gateway) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return g.trusted(remoteAddr(r)) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (g *gateway) securityHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if g.isHTTPS(r) {
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
}

// sameOrigin rejects state-changing requests initiated by another site.
// X-Forwarded-Host is honoured only from a trusted proxy.
func (g *gateway) sameOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser client (CLI with a token)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" && g.trusted(remoteAddr(r)) {
		host = fh
	}
	return strings.EqualFold(u.Host, host)
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.securityHeaders(w, r)
	switch {
	case r.URL.Path == "/healthz":
		g.health(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/"):
		if r.Method == http.MethodOptions {
			// No CORS: the API is same-origin only.
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !g.sameOrigin(r) {
			http.Error(w, `{"ok":false,"code":"cross_origin","error":"cross-origin request refused"}`, http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		g.proxy.ServeHTTP(w, r)
	default:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		g.static.ServeHTTP(w, r)
	}
}

func (g *gateway) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent/api/healthz", nil)
	req.Header.Set(wire.HeaderGateway, "1")
	resp, err := g.agent.Do(req)
	w.Header().Set("Content-Type", "application/json")
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"ok":false,"agent":false}`))
		return
	}
	resp.Body.Close()
	_, _ = w.Write([]byte(`{"ok":true,"agent":true}`))
}

func staticHandler(root fs.FS) http.Handler {
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(p, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if p != "/" {
			if _, err := fs.Stat(root, strings.TrimPrefix(p, "/")); err != nil {
				// Hash routing means every page is index.html.
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}

// Serve runs the gateway until ctx ends.
func Serve(ctx context.Context, cfg Config) error {
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           Handler(cfg),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("web gateway listening", "addr", cfg.Listen, "agent", cfg.AgentSocket)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
