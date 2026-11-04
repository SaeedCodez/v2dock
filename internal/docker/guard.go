package docker

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"time"
)

// guardAllow is the complete list of Docker API calls the agent may make:
// listing containers (which never includes environment variables), one-shot
// stats, the event stream, and ping/version. Everything else — inspect, exec,
// create, start/stop, images, volumes, any POST — is refused, so a
// compromised agent cannot read container secrets or control containers.
var guardAllow = regexp.MustCompile(`^(/v1\.[0-9]{1,2})?/(_ping|version|containers/json|containers/[a-f0-9]{12,64}/stats|events)$`)

// GuardAllowed reports whether a request may pass the guard.
func GuardAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.RawPath != "" { // no encoded path tricks
		return false
	}
	return guardAllow.MatchString(r.URL.Path)
}

// RunGuard serves a filtered view of the Docker API on listenPath.
func RunGuard(ctx context.Context, dockerSocket, listenPath string, gid int) error {
	if _, err := os.Stat(dockerSocket); err != nil {
		return errors.New("docker socket not mounted at " + dockerSocket)
	}
	_ = os.Remove(listenPath)
	ln, err := net.Listen("unix", listenPath)
	if err != nil {
		return err
	}
	// The agent runs as root; nobody else needs this socket.
	if err := os.Chmod(listenPath, 0o600); err != nil {
		ln.Close()
		return err
	}
	if gid > 0 {
		_ = os.Chown(listenPath, 0, gid)
		_ = os.Chmod(listenPath, 0o660)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "docker"
			pr.Out.Host = "docker"
			// Forward nothing from the caller but the request line.
			pr.Out.Header = http.Header{"User-Agent": {"v2dock-guard"}}
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", dockerSocket)
			},
		},
		FlushInterval: -1, // the events endpoint streams
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("guard: docker request failed", "path", r.URL.Path, "err", err)
			http.Error(w, "docker unavailable", http.StatusBadGateway)
		},
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !GuardAllowed(r) {
			slog.Warn("guard: refused", "method", r.Method, "path", r.URL.Path)
			http.Error(w, "forbidden by v2dock guard", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("guard: serving filtered docker API", "socket", listenPath)
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
