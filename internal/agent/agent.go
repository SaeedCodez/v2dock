// Package agent is the privileged daemon: it owns the database, runs Xray,
// installs the firewall rules, samples traffic and serves the API on a unix
// socket. It never listens on a TCP port.
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/SaeedCodez/v2dock/internal/docker"
	"github.com/SaeedCodez/v2dock/internal/netfw"
	"github.com/SaeedCodez/v2dock/internal/store"
	"github.com/SaeedCodez/v2dock/internal/xcore"
)

// Version is set at build time.
var Version = "dev"

type Config struct {
	DataDir       string
	SocketPath    string
	SocketGID     int
	DockerSocket  string
	AssetDir      string
	TProxyPort    int
	SetupToken    string
	AdminPassword string
	// NoFirewall runs everything except the nftables rules (development on
	// machines without NET_ADMIN).
	NoFirewall bool
}

// liveStats is the latest Docker sample for one container.
type liveStats struct {
	prev     docker.Stats
	CPU      float64
	Mem      uint64
	MemLimit uint64
	RxRate   float64 // bytes/s
	TxRate   float64
	At       time.Time
}

type health struct {
	OK         bool   `json:"ok"`
	DelayMs    int    `json:"delayMs,omitempty"`
	Error      string `json:"error,omitempty"`
	At         int64  `json:"at,omitempty"`
	Failures   int    `json:"failures"`
	Checking   bool   `json:"checking,omitempty"`
	LastSwitch int64  `json:"lastSwitch,omitempty"`
}

type Agent struct {
	cfg    Config
	db     *store.DB
	fw     *netfw.Firewall
	docker *docker.Client
	auth   *authState

	reconcileMu sync.Mutex // serialises reconcile (instance + firewall changes)
	kick        chan struct{}

	mu            sync.Mutex
	xr            *xcore.Instance
	tagKeys       map[string]tagRef // outbound tag -> route key
	appliedHash   string
	running       bool
	runError      string
	fwError       string
	containers    []docker.Container
	containersAt  time.Time
	dockerErr     string
	live          map[string]*liveStats // container id -> stats
	health        health
	failoverAfter time.Time
	job           *testJob
	lastJob       *testJob
	setupToken    string
}

type tagRef struct {
	key string
	vpn bool
}

func New(cfg Config) (*Agent, error) {
	if cfg.TProxyPort == 0 {
		cfg.TProxyPort = 12701
	}
	if cfg.AssetDir != "" {
		os.Setenv("XRAY_LOCATION_ASSET", cfg.AssetDir)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "v2dock.db"))
	if err != nil {
		return nil, err
	}
	a := &Agent{
		cfg:     cfg,
		db:      db,
		fw:      netfw.New(cfg.TProxyPort, cfg.TProxyPort+1),
		docker:  docker.New(cfg.DockerSocket),
		kick:    make(chan struct{}, 1),
		live:    map[string]*liveStats{},
		tagKeys: map[string]tagRef{},
	}
	a.auth = newAuthState(a)
	return a, nil
}

// DB exposes the store to the local CLI helpers (password reset).
func (a *Agent) DB() *store.DB { return a.db }

// Run starts the daemon and blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	defer a.db.Close()
	if !a.cfg.NoFirewall {
		if err := netfw.Available(ctx); err != nil {
			return err
		}
		// Rules from a previous run that crashed would blackhole routed
		// containers until we are ready; start clean.
		_ = a.fw.Remove(ctx)
	}
	if err := a.auth.bootstrap(ctx); err != nil {
		return err
	}

	ln, err := a.listen()
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           a.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ConnContext: func(c context.Context, conn net.Conn) context.Context {
			return context.WithValue(c, peerKey{}, peerUID(conn))
		},
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("api server stopped", "err", err)
		}
	}()
	slog.Info("agent ready", "socket", a.cfg.SocketPath, "version", Version)

	a.refreshContainers(ctx)
	a.Reconcile(ctx)

	var wg sync.WaitGroup
	loops := []func(context.Context){a.reconcileLoop, a.dockerEventsLoop, a.countersLoop, a.dockerStatsLoop, a.healthLoop, a.subscriptionLoop, a.maintenanceLoop}
	for _, loop := range loops {
		wg.Add(1)
		go func(f func(context.Context)) { defer wg.Done(); f(ctx) }(loop)
	}
	<-ctx.Done()

	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	wg.Wait()
	a.shutdown(sctx)
	return nil
}

func (a *Agent) listen() (net.Listener, error) {
	path := a.cfg.SocketPath
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// Only root (the CLI via docker exec) and the web container's group may
	// connect. The compose file runs the agent with that group as its primary
	// group, so the socket already belongs to it and no CAP_CHOWN is needed.
	mode := os.FileMode(0o600)
	if gid := a.cfg.SocketGID; gid > 0 {
		if os.Getegid() == gid || os.Chown(path, os.Geteuid(), gid) == nil {
			mode = 0o660
		} else {
			slog.Warn("cannot give the web gateway access to the agent socket; run the agent with group "+fmt.Sprint(gid), "socket", path)
		}
	}
	if err := os.Chmod(path, mode); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// shutdown flushes counters and removes the firewall rules so routed
// containers fall back to their normal network instead of a dead proxy.
func (a *Agent) shutdown(ctx context.Context) {
	a.reconcileMu.Lock()
	defer a.reconcileMu.Unlock()
	a.flushCounters(ctx)
	if !a.cfg.NoFirewall {
		if err := a.fw.Remove(ctx); err != nil {
			slog.Warn("removing firewall rules", "err", err)
		}
	}
	a.mu.Lock()
	xr := a.xr
	a.xr = nil
	a.running = false
	a.mu.Unlock()
	_ = xr.Close()
	slog.Info("agent stopped; routed containers use their normal network again")
}

// requestReconcile schedules a reconcile soon (coalesced).
func (a *Agent) requestReconcile() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

func (a *Agent) reconcileLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.kick:
			// Coalesce bursts (e.g. a compose project starting).
			time.Sleep(700 * time.Millisecond)
			select {
			case <-a.kick:
			default:
			}
		case <-t.C:
		}
		a.Reconcile(ctx)
	}
}

// migrateAppKeys moves routes saved under the old per-deploy key of a Coolify
// app ("compose:<uuid>/<uuid>-<timestamp>") to the app's stable key, so VPN
// selections made before the fix, and the ones lost by later deploys, come back.
// When several old keys exist for one app (one per deploy it survived), the
// newest wins.
func (a *Agent) migrateAppKeys(ctx context.Context, containers []docker.Container) {
	routes, err := a.db.ListRoutes(ctx)
	if err != nil || len(routes) == 0 {
		return
	}
	for _, c := range containers {
		nk := coolifyAppKey(c)
		if nk == "" {
			continue
		}
		uuid := c.Labels["coolify.applicationUuid"]
		from, have := "", false
		for _, r := range routes { // ordered by created_at: the last match is the newest
			if r.Key == nk {
				have = true
			} else if isLegacyAppKey(r.Key, uuid) {
				from = r.Key
			}
		}
		if have || from == "" {
			continue
		}
		if ok, err := a.db.RenameRoute(ctx, from, nk); err != nil {
			slog.Warn("route key migration failed", "from", from, "to", nk, "err", err)
		} else if ok {
			slog.Info("route key migrated to a stable Coolify app key", "from", from, "to", nk)
		}
	}
}

// desired computes the plan for the current settings, routes and containers.
type fwPlan struct {
	clients []netip.Addr // redirected into the proxy
	blocked []netip.Addr // over quota with action "block"
}

func (a *Agent) desired(ctx context.Context) (store.Settings, []xcore.RoutePlan, fwPlan, error) {
	var fp fwPlan
	s, err := a.db.LoadSettings(ctx)
	if err != nil {
		return s, nil, fp, err
	}
	a.mu.Lock()
	containers := a.containers
	a.mu.Unlock()
	a.migrateAppKeys(ctx, containers)
	routes, err := a.db.ListRoutes(ctx)
	if err != nil {
		return s, nil, fp, err
	}
	byKey := map[string][]netip.Addr{}
	for _, c := range containers {
		if c.State != "running" || !routable(c) {
			continue
		}
		k := ContainerKey(c)
		byKey[k] = append(byKey[k], c.IPv4s()...)
	}
	var plans []xcore.RoutePlan
	for _, r := range routes {
		ips := byKey[r.Key]
		sort.Slice(ips, func(i, j int) bool { return ips[i].Less(ips[j]) })
		mode := xcore.ModeVPN
		if r.Exceeded {
			if r.QuotaAction == "direct" {
				mode = xcore.ModeDirect
			} else {
				mode = xcore.ModeBlock
			}
		}
		plans = append(plans, xcore.RoutePlan{Key: r.Key, IPs: ips, Mode: mode})
		if mode != xcore.ModeDirect {
			// Containers over a "direct" quota bypass the proxy entirely.
			fp.clients = append(fp.clients, ips...)
		}
		if mode == xcore.ModeBlock {
			fp.blocked = append(fp.blocked, ips...)
		}
	}
	return s, plans, fp, nil
}

// Reconcile makes the running proxy and firewall match the desired state.
func (a *Agent) Reconcile(ctx context.Context) error {
	a.reconcileMu.Lock()
	defer a.reconcileMu.Unlock()

	s, plans, fp, err := a.desired(ctx)
	if err != nil {
		a.setRunError(err.Error())
		return err
	}
	if !s.Connected || s.ActiveOutbound == nil {
		a.stopLocked(ctx)
		return nil
	}

	plan := xcore.Plan{
		Outbound:    s.ActiveOutbound,
		TProxyPort:  a.cfg.TProxyPort,
		DNSPort:     a.cfg.TProxyPort + 1,
		BypassIran:  s.BypassIran,
		RemoteDNS:   s.RemoteDNS,
		DomesticDNS: s.DomesticDNS,
		Routes:      plans,
	}
	cfg, err := xcore.BuildMain(plan)
	if err != nil {
		a.setRunError(err.Error())
		a.stopLocked(ctx)
		return err
	}
	fpJSON, _ := json.Marshal([][]netip.Addr{fp.clients, fp.blocked})
	sum := sha256.Sum256(append(cfg, fpJSON...))
	hash := hex.EncodeToString(sum[:])

	a.mu.Lock()
	same := hash == a.appliedHash && a.xr != nil
	a.mu.Unlock()
	if same {
		if !a.cfg.NoFirewall {
			// Re-assert rules in case something on the host flushed them.
			if st := a.fw.Status(ctx); !st.Table {
				slog.Warn("firewall rules were missing; reinstalling")
				if err := a.fw.Apply(ctx, fp.clients, fp.blocked); err != nil {
					a.setFwError(err.Error())
				}
			}
		}
		return nil
	}

	a.flushCounters(ctx)
	a.mu.Lock()
	old := a.xr
	a.xr = nil
	a.mu.Unlock()
	_ = old.Close()

	// Rules first: they include the drop rule that keeps the proxy port
	// closed to everything except diverted packets.
	if !a.cfg.NoFirewall {
		if err := a.fw.Apply(ctx, fp.clients, fp.blocked); err != nil {
			slog.Error("applying firewall rules", "err", err)
			a.setFwError(err.Error())
			a.mu.Lock()
			a.appliedHash, a.running = "", false
			a.mu.Unlock()
			return err
		}
	}
	inst, err := xcore.Start(cfg)
	if err != nil {
		slog.Error("xray failed to start", "err", err)
		a.setRunError("Xray failed to start: " + err.Error())
		a.mu.Lock()
		a.appliedHash = ""
		a.running = false
		a.mu.Unlock()
		if !a.cfg.NoFirewall {
			_ = a.fw.Remove(ctx)
		}
		return err
	}
	tags := map[string]tagRef{}
	for _, p := range plans {
		tags[xcore.VPNTag(p.Key)] = tagRef{key: p.Key, vpn: true}
		tags[xcore.DirectTag(p.Key)] = tagRef{key: p.Key}
	}
	a.mu.Lock()
	a.xr = inst
	a.tagKeys = tags
	a.appliedHash = hash
	a.running = true
	a.runError = ""
	a.mu.Unlock()

	a.setFwError("")
	slog.Info("proxy applied", "server", s.ActiveName, "routes", len(plans), "clients", len(fp.clients), "bypassIran", s.BypassIran)
	return nil
}

func (a *Agent) stopLocked(ctx context.Context) {
	a.flushCounters(ctx)
	a.mu.Lock()
	old := a.xr
	wasRunning := a.running
	a.xr = nil
	a.running = false
	a.appliedHash = ""
	a.tagKeys = map[string]tagRef{}
	a.mu.Unlock()
	if !a.cfg.NoFirewall && (wasRunning || old != nil) {
		if err := a.fw.Remove(ctx); err != nil {
			a.setFwError(err.Error())
		}
	}
	_ = old.Close()
}

func (a *Agent) setRunError(msg string) {
	a.mu.Lock()
	a.runError = msg
	a.mu.Unlock()
}

func (a *Agent) setFwError(msg string) {
	a.mu.Lock()
	a.fwError = msg
	a.mu.Unlock()
}

// event records an activity entry and logs it.
func (a *Agent) event(ctx context.Context, level, kind, msg string, p *principal) {
	e := store.Event{Level: level, Kind: kind, Message: msg}
	if p != nil {
		e.Actor, e.IP = p.actor(), p.ip
	}
	if err := a.db.AddEvent(context.WithoutCancel(ctx), e); err != nil {
		slog.Warn("recording event", "err", err)
	}
	switch level {
	case "error":
		slog.Error(msg, "kind", kind, "actor", e.Actor)
	case "warn":
		slog.Warn(msg, "kind", kind, "actor", e.Actor)
	default:
		slog.Info(msg, "kind", kind, "actor", e.Actor)
	}
}

func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
