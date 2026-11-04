// Command netcheck is a small test site for v2dock.
//
// It runs as its own container. Once v2dock routes that container, the page
// shows which services the container can reach. Every outbound request is
// made by this server (never by the browser) and only to the fixed list of
// URLs below; nothing in an incoming request can change the targets.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed index.html
var indexHTML string

const (
	listenAddr   = ":8080"
	userAgent    = "Mozilla/5.0 (v2dock netcheck)"
	checkTimeout = 8 * time.Second  // per request
	runBudget    = 12 * time.Second // whole /api/checks run
	egressBudget = 11 * time.Second // whole egress lookup, fallback included
	cacheTTL     = 5 * time.Second
	maxBody      = 64 << 10
	maxRedirects = 3

	ipifyURL = "https://api.ipify.org?format=json"
	ipAPIURL = "http://ip-api.com/json/?fields=status,country,countryCode,query,isp"
	ipwhoURL = "https://ipwho.is/"

	csp = "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; " +
		"connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'"
)

type target struct {
	ID   string
	Name string
	URL  string
}

// Host is the URL's host, shown on the page.
func (t target) Host() string {
	u, err := url.Parse(t.URL)
	if err != nil {
		return t.URL
	}
	return u.Host
}

type group struct {
	Name    string
	Desc    string
	Targets []target
}

// groups is the complete, fixed list of checks.
var groups = []group{
	{
		Name: "Iranian services",
		Desc: "Should go direct when Iran bypass is on.",
		Targets: []target{
			{"digikala", "Digikala", "https://digikala.com/"},
			{"aparat", "Aparat", "https://aparat.com/"},
			{"divar", "Divar", "https://divar.ir/"},
			{"snapp", "Snapp", "https://snapp.ir/"},
			{"shaparak", "Shaparak", "https://www.shaparak.ir/"},
			{"eitaa", "Eitaa", "https://eitaa.com/"},
			{"varzesh3", "Varzesh3", "https://varzesh3.com/"},
		},
	},
	{
		Name: "Filtered in Iran",
		Desc: "Only reachable through the VPN from Iran.",
		Targets: []target{
			{"youtube", "YouTube", "https://youtube.com/"},
			{"telegram", "Telegram", "https://api.telegram.org/"},
			{"x", "X", "https://x.com/"},
			{"instagram", "Instagram", "https://instagram.com/"},
			{"facebook", "Facebook", "https://facebook.com/"},
			{"wikipedia", "Wikipedia", "https://www.wikipedia.org/"},
		},
	},
	{
		Name: "International / sanctioned",
		Desc: "Often blocked for Iranian IPs. A 401 or 403 still means the network path works.",
		Targets: []target{
			{"docker-hub", "Docker Hub", "https://registry-1.docker.io/v2/"},
			{"github", "GitHub", "https://api.github.com/"},
			{"google", "Google", "https://www.google.com/generate_204"},
			{"openai", "OpenAI", "https://api.openai.com/v1/models"},
			{"cloudflare", "Cloudflare", "https://1.1.1.1/cdn-cgi/trace"},
		},
	},
}

// ---------------------------------------------------------------------------
// Results

type checkResult struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	URL    string `json:"url"`
	OK     bool   `json:"ok"`
	Status int    `json:"status"`
	MS     int64  `json:"ms"`
	Error  string `json:"error"`
	IP     string `json:"ip"`
}

type egressResult struct {
	IP          string `json:"ip"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	ISP         string `json:"isp"`
	Error       string `json:"error"`
}

type checksResponse struct {
	Egress egressResult  `json:"egress"`
	Checks []checkResult `json:"checks"`
	RanAt  int64         `json:"ranAt"`
}

// ---------------------------------------------------------------------------
// Outbound requests

// nonPublic are destinations a public service never resolves to. A filtered
// domain resolving into one of them (Iran's resolvers answer 10.10.34.x) is
// reported instead of dialled; it also keeps redirects off internal networks.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func isPublic(a netip.Addr) bool {
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

type nonPublicError struct {
	host string
	addr netip.Addr
}

func (e *nonPublicError) Error() string {
	return fmt.Sprintf("DNS answered %s with non-public address %s (DNS filtering?)", e.host, e.addr)
}

// resolve looks the host up with the default resolver. When the host has
// IPv4 addresses only those are returned: v2dock steers IPv4 traffic, so an
// IPv6 fallback could reach a service while bypassing it.
func resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	var v4, v6 []netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if a.Is4() {
			v4 = append(v4, a)
		} else {
			v6 = append(v6, a)
		}
	}
	if len(v4) > 0 {
		return v4, nil
	}
	if len(v6) > 0 {
		return v6, nil
	}
	return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
}

// stage records how far a request got, so errors can say where it failed.
type stage struct {
	mu sync.Mutex
	s  string
}

func (p *stage) set(s string) { p.mu.Lock(); p.s = s; p.mu.Unlock() }
func (p *stage) get() string  { p.mu.Lock(); defer p.mu.Unlock(); return p.s }

type fetchResult struct {
	status int
	body   []byte
	ip     string // address the URL's own host resolved to (the one dialled, if any)
	ms     int64  // until response headers, or until failure
	err    string // human-readable; empty when any HTTP response arrived
}

func limitRedirects(_ *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return http.ErrUseLastResponse
	}
	return nil
}

// fetch GETs rawURL (one of the fixed URLs) with its own 8 s timeout. The
// host is resolved with net.DefaultResolver first and the resolved address
// is then dialled, so the result can report which IP was used.
func fetch(parent context.Context, rawURL string, keepBody bool) (res fetchResult) {
	ctx, cancel := context.WithTimeout(parent, checkTimeout)
	defer cancel()
	start := time.Now()
	st := &stage{s: "while resolving"}
	fail := func(err error) fetchResult {
		res.ms = time.Since(start).Milliseconds()
		res.err = describeErr(err, st.get())
		return res
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fail(err)
	}
	host := u.Hostname()
	addrs, err := resolve(ctx, host)
	if err != nil {
		return fail(err)
	}
	res.ip = addrs[0].String()

	var (
		mu     sync.Mutex
		dialed string
	)
	dialer := &net.Dialer{}
	tr := &http.Transport{
		Proxy: nil, // v2dock is transparent; ignore any proxy environment
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			h, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips := addrs
			if h != host { // a redirect to another host
				st.set("while resolving")
				if ips, err = resolve(ctx, h); err != nil {
					return nil, err
				}
			}
			st.set("while connecting")
			var lastErr error
			for _, ip := range ips {
				if !isPublic(ip) {
					lastErr = &nonPublicError{host: h, addr: ip}
					continue
				}
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					if h == host {
						mu.Lock()
						if dialed == "" {
							dialed = ip.String()
						}
						mu.Unlock()
					}
					return c, nil
				}
				lastErr = err
				if ctx.Err() != nil {
					break
				}
			}
			return nil, lastErr
		},
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:      true,
		DisableKeepAlives:      true,
		TLSHandshakeTimeout:    checkTimeout,
		ResponseHeaderTimeout:  checkTimeout,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: limitRedirects}

	trace := &httptrace.ClientTrace{
		TLSHandshakeStart: func() { st.set("during TLS handshake") },
		GotConn:           func(httptrace.GotConnInfo) { st.set("waiting for response") },
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, rawURL, nil)
	if err != nil {
		return fail(err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	mu.Lock()
	if dialed != "" {
		res.ip = dialed
	}
	mu.Unlock()
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	res.ms = time.Since(start).Milliseconds()
	res.status = resp.StatusCode
	body := io.LimitReader(resp.Body, maxBody)
	if keepBody {
		res.body, _ = io.ReadAll(body)
	} else {
		_, _ = io.Copy(io.Discard, body)
	}
	return res
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// describeErr turns a transport error into a short message for the page.
func describeErr(err error, during string) string {
	var (
		nonPub  *nonPublicError
		dnsErr  *net.DNSError
		certErr *tls.CertificateVerificationError
		recErr  tls.RecordHeaderError
	)
	switch {
	case errors.As(err, &nonPub):
		return nonPub.Error()
	case errors.As(err, &dnsErr):
		switch {
		case dnsErr.IsTimeout:
			return "DNS lookup for " + dnsErr.Name + " timed out"
		case dnsErr.IsNotFound:
			return "DNS: no such host " + dnsErr.Name
		}
		return "DNS lookup for " + dnsErr.Name + " failed: " + dnsErr.Err
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return "timed out " + during
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset " + during
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "network unreachable"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed " + during
	case errors.As(err, &certErr):
		return "TLS certificate rejected: " + certErr.Err.Error()
	case errors.As(err, &recErr):
		return "server did not answer with TLS " + during
	}
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) {
		msg = ue.Err.Error()
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		msg = oe.Err.Error()
	}
	return msg + " (" + during + ")"
}

func runCheck(ctx context.Context, groupName string, t target) checkResult {
	r := fetch(ctx, t.URL, false)
	res := checkResult{
		ID: t.ID, Name: t.Name, Group: groupName, URL: t.URL,
		Status: r.status, MS: r.ms, Error: r.err, IP: r.ip,
	}
	if r.err == "" {
		// Any HTTP answer below 500 proves the path works; 401/403 from a
		// sanctioned API is still "reachable" network-wise.
		res.OK = r.status < 500
		if !res.OK {
			res.Error = fmt.Sprintf("server error (HTTP %d)", r.status)
		}
	}
	return res
}

func getJSON(ctx context.Context, rawURL string, v any) error {
	r := fetch(ctx, rawURL, true)
	if r.err != "" {
		return errors.New(r.err)
	}
	if r.status != http.StatusOK {
		return fmt.Errorf("HTTP %d", r.status)
	}
	return json.Unmarshal(r.body, v)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// lookupEgress asks the outside world which public IP and country this
// container's traffic comes from.
func lookupEgress() egressResult {
	ctx, cancel := context.WithTimeout(context.Background(), egressBudget)
	defer cancel()

	var (
		wg    sync.WaitGroup
		ipify struct {
			IP string `json:"ip"`
		}
		ipifyErr error
		geo      struct {
			Status      string `json:"status"`
			Message     string `json:"message"`
			Country     string `json:"country"`
			CountryCode string `json:"countryCode"`
			Query       string `json:"query"`
			ISP         string `json:"isp"`
		}
		geoErr error
	)
	wg.Go(func() { ipifyErr = getJSON(ctx, ipifyURL, &ipify) })
	wg.Go(func() {
		geoErr = getJSON(ctx, ipAPIURL, &geo)
		if geoErr == nil && geo.Status != "success" {
			geoErr = errors.New(orDefault(geo.Message, "lookup failed"))
		}
	})
	wg.Wait()

	e := egressResult{IP: ipify.IP}
	var errs []string
	if ipifyErr != nil {
		errs = append(errs, "ipify: "+ipifyErr.Error())
	}
	if geoErr == nil {
		e.Country, e.CountryCode, e.ISP = geo.Country, geo.CountryCode, geo.ISP
		if e.IP == "" {
			e.IP = geo.Query
		}
	} else {
		errs = append(errs, "ip-api: "+geoErr.Error())
		var who struct {
			Success     bool   `json:"success"`
			Message     string `json:"message"`
			IP          string `json:"ip"`
			Country     string `json:"country"`
			CountryCode string `json:"country_code"`
			Connection  struct {
				ISP string `json:"isp"`
			} `json:"connection"`
		}
		err := getJSON(ctx, ipwhoURL, &who)
		if err == nil && !who.Success {
			err = errors.New(orDefault(who.Message, "lookup failed"))
		}
		if err == nil {
			e.Country, e.CountryCode, e.ISP = who.Country, who.CountryCode, who.Connection.ISP
			if e.IP == "" {
				e.IP = who.IP
			}
		} else {
			errs = append(errs, "ipwho.is: "+err.Error())
		}
	}
	if e.IP == "" || e.Country == "" {
		e.Error = strings.Join(errs, "; ")
	}
	log.Printf("egress: ip=%q country=%q error=%q", e.IP, e.CountryCode, e.Error)
	return e
}

// ---------------------------------------------------------------------------
// One run at a time, result reused for a few seconds

// cache runs fn at most once at a time and reuses its result for ttl.
// Concurrent callers wait for the run in flight and then share its result,
// so parallel page loads never fan out into parallel runs.
type cache[T any] struct {
	ttl time.Duration
	fn  func() T
	sem chan struct{} // capacity 1; holding it guards at and val
	at  time.Time
	val T
}

func newCache[T any](ttl time.Duration, fn func() T) *cache[T] {
	return &cache[T]{ttl: ttl, fn: fn, sem: make(chan struct{}, 1)}
}

// get returns the cached value or runs fn. ctx only bounds the wait for a
// run in flight; the run itself has its own budget so a client going away
// does not cancel (and poison) a result others are waiting for.
func (c *cache[T]) get(ctx context.Context) (T, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
	defer func() { <-c.sem }()
	if !c.at.IsZero() && time.Since(c.at) < c.ttl {
		return c.val, nil
	}
	c.val = c.fn()
	c.at = time.Now()
	return c.val, nil
}

type server struct {
	page   []byte
	checks *cache[checksResponse]
	egress *cache[egressResult]
}

func (s *server) runChecks() checksResponse {
	ctx, cancel := context.WithTimeout(context.Background(), runBudget)
	defer cancel()
	start := time.Now()

	n := 0
	for _, g := range groups {
		n += len(g.Targets)
	}
	results := make([]checkResult, 0, n)
	for _, g := range groups {
		for _, t := range g.Targets {
			results = append(results, checkResult{ID: t.ID, Name: t.Name, Group: g.Name, URL: t.URL})
		}
	}

	var (
		wg sync.WaitGroup
		eg egressResult
	)
	i := 0
	for _, g := range groups {
		for _, t := range g.Targets {
			slot := &results[i]
			wg.Go(func() { *slot = runCheck(ctx, g.Name, t) })
			i++
		}
	}
	wg.Go(func() {
		r, err := s.egress.get(ctx)
		if err != nil {
			r = egressResult{Error: "egress lookup did not finish in time"}
		}
		eg = r
	})
	wg.Wait()

	ok := 0
	for _, r := range results {
		if r.OK {
			ok++
		}
	}
	log.Printf("checks: %d/%d reachable in %s", ok, len(results), time.Since(start).Round(time.Millisecond))
	return checksResponse{Egress: eg, Checks: results, RanAt: time.Now().Unix()}
}

// ---------------------------------------------------------------------------
// HTTP

func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encoding failed", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write(append(b, '\n'))
}

func (s *server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(s.page)
}

func (s *server) handleChecks(w http.ResponseWriter, r *http.Request) {
	res, err := s.checks.get(r.Context())
	if err != nil {
		http.Error(w, "request cancelled", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, res)
}

func (s *server) handleEgress(w http.ResponseWriter, r *http.Request) {
	res, err := s.egress.get(r.Context())
	if err != nil {
		http.Error(w, "request cancelled", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, res)
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// validateTargets fails fast if the hard-coded list is malformed.
func validateTargets() error {
	seen := map[string]bool{}
	for _, g := range groups {
		for _, t := range g.Targets {
			if seen[t.ID] {
				return fmt.Errorf("duplicate check id %q", t.ID)
			}
			seen[t.ID] = true
			u, err := url.Parse(t.URL)
			if err != nil {
				return fmt.Errorf("check %q: %w", t.ID, err)
			}
			if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
				return fmt.Errorf("check %q: bad URL %q", t.ID, t.URL)
			}
		}
	}
	return nil
}

func newServer() (*server, error) {
	if err := validateTargets(); err != nil {
		return nil, err
	}
	tmpl, err := template.New("index").Parse(indexHTML)
	if err != nil {
		return nil, err
	}
	var page bytes.Buffer
	if err := tmpl.Execute(&page, groups); err != nil {
		return nil, err
	}
	s := &server{page: page.Bytes()}
	s.egress = newCache(cacheTTL, lookupEgress)
	s.checks = newCache(cacheTTL, s.runChecks)
	return s, nil
}

func main() {
	s, err := newServer()
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/checks", s.handleChecks)
	mux.HandleFunc("GET /api/egress", s.handleEgress)
	mux.HandleFunc("GET /healthz", handleHealthz)

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// A request may wait for a run in flight and then start its own.
		WriteTimeout:   2*runBudget + 10*time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("netcheck listening on %s", listenAddr)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server: %v", err)
			stop()
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
}
