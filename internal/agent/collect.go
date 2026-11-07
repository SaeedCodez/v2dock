package agent

import (
	"context"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SaeedCodez/v2dock/internal/docker"
	"github.com/SaeedCodez/v2dock/internal/store"
)

// RoleLabel marks v2dock's own containers so they are never routed.
const RoleLabel = "io.v2dock.role"

// ContainerKey is a container's stable identity: it survives redeploys, so a
// Coolify app keeps its routing and quota after every deployment.
//
// Coolify apps are identified by their application uuid. Their compose
// service is named after the container ("<uuid>-<deploy timestamp>"), so the
// compose key changes on every deploy and must not be used for them. Coolify
// services (compose stacks) and other compose projects keep stable
// project/service names, so those use the compose key.
func ContainerKey(c docker.Container) string {
	if k := coolifyAppKey(c); k != "" {
		return k
	}
	return legacyKey(c)
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

// coolifyAppKey is "coolify:app/<application uuid>" for a Coolify application
// container, or "" for anything else. Pull-request previews are separate
// deployments of the same app, so they keep their own (compose) key.
func coolifyAppKey(c docker.Container) string {
	if c.Labels["coolify.type"] != "application" {
		return ""
	}
	if pr := c.Labels["coolify.pullRequestId"]; pr != "" && pr != "0" {
		return ""
	}
	id := c.Labels["coolify.applicationUuid"]
	if !idPattern.MatchString(id) {
		return ""
	}
	return "coolify:app/" + id
}

// legacyKey is the compose/name key v2dock used for every container before
// Coolify apps got their own key.
func legacyKey(c docker.Container) string {
	p, s := c.Labels["com.docker.compose.project"], c.Labels["com.docker.compose.service"]
	if p != "" && s != "" {
		return "compose:" + p + "/" + s
	}
	return "name:" + c.Name
}

// isLegacyAppKey reports whether key is a pre-fix key of the Coolify app with
// this uuid ("compose:<uuid>/<uuid>-<timestamp>" or "name:<uuid>-<timestamp>").
func isLegacyAppKey(key, appUUID string) bool {
	return strings.HasPrefix(key, "compose:"+appUUID+"/") || strings.HasPrefix(key, "name:"+appUUID+"-")
}

var keyPattern = regexp.MustCompile(`^(compose|name|coolify):[A-Za-z0-9][A-Za-z0-9_.\-/]{0,200}$`)

func validKey(k string) bool { return keyPattern.MatchString(k) }

// ContainerLabel is a readable name, preferring Coolify's resource names.
func ContainerLabel(c docker.Container) string {
	if r := c.Labels["coolify.resourceName"]; r != "" {
		if s := c.Labels["coolify.serviceName"]; s != "" && s != r {
			return r + " / " + s
		}
		return r
	}
	if s := c.Labels["com.docker.compose.service"]; s != "" {
		if p := c.Labels["com.docker.compose.project"]; p != "" && p != s {
			return p + " / " + s
		}
		return s
	}
	return c.Name
}

func containerProject(c docker.Container) string {
	if p := c.Labels["coolify.projectName"]; p != "" {
		if e := c.Labels["coolify.environmentName"]; e != "" {
			return p + " · " + e
		}
		return p
	}
	return c.Labels["com.docker.compose.project"]
}

func isSelf(c docker.Container) bool { return c.Labels[RoleLabel] != "" }

// routable reports whether the container's traffic can be diverted: it needs
// its own network namespace with an IPv4 address.
func routable(c docker.Container) bool {
	if isSelf(c) {
		return false
	}
	switch {
	case c.NetworkMode == "host", c.NetworkMode == "none", strings.HasPrefix(c.NetworkMode, "container:"):
		return false
	}
	return len(c.IPv4s()) > 0 || c.State != "running"
}

func unroutableReason(c docker.Container) string {
	switch {
	case isSelf(c):
		return "v2dock itself"
	case c.NetworkMode == "host":
		return "uses the host network"
	case c.NetworkMode == "none":
		return "has no network"
	case strings.HasPrefix(c.NetworkMode, "container:"):
		return "shares another container's network"
	case c.State == "running" && len(c.IPv4s()) == 0:
		return "has no IPv4 address"
	}
	return ""
}

// refreshContainers reloads the container list and asks for a reconcile when
// the addresses of routed containers changed.
func (a *Agent) refreshContainers(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	list, err := a.docker.List(cctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.dockerErr = err.Error()
		return
	}
	before := ipFingerprint(a.containers)
	a.containers = list
	a.containersAt = time.Now()
	a.dockerErr = ""
	if ipFingerprint(list) != before {
		a.requestReconcile()
	}
}

func ipFingerprint(cs []docker.Container) string {
	var parts []string
	for _, c := range cs {
		if c.State != "running" {
			continue
		}
		ips := c.IPv4s()
		sort.Slice(ips, func(i, j int) bool { return ips[i].Less(ips[j]) })
		var s []string
		for _, ip := range ips {
			s = append(s, ip.String())
		}
		parts = append(parts, ContainerKey(c)+"="+strings.Join(s, ","))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func (a *Agent) dockerEventsLoop(ctx context.Context) {
	backoff := time.Second
	refresh := time.NewTicker(30 * time.Second)
	defer refresh.Stop()
	events := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			err := a.docker.Events(ctx, func(ev docker.Event) {
				switch ev.Action {
				case "start", "die", "stop", "destroy", "connect", "disconnect", "rename", "restart", "create":
					select {
					case events <- struct{}{}:
					default:
					}
				}
			})
			if ctx.Err() != nil {
				return
			}
			slog.Debug("docker event stream ended", "err", err)
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-events:
			time.Sleep(300 * time.Millisecond)
			a.refreshContainers(ctx)
		case <-refresh.C:
			a.refreshContainers(ctx)
		}
	}
}

// countersLoop moves Xray's per-container traffic counters into the database
// every 10 seconds and enforces quotas.
func (a *Agent) countersLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.flushCounters(ctx)
			a.rollQuotaPeriods(ctx)
		}
	}
}

func (a *Agent) flushCounters(ctx context.Context) {
	a.mu.Lock()
	xr, tags := a.xr, a.tagKeys
	a.mu.Unlock()
	if xr == nil {
		return
	}
	counters := xr.TakeCounters()
	if len(counters) == 0 {
		return
	}
	byKey := map[string]*store.UsageDelta{}
	for tag, tr := range counters {
		ref, ok := tags[tag]
		if !ok {
			continue
		}
		d := byKey[ref.key]
		if d == nil {
			d = &store.UsageDelta{Key: ref.key}
			byKey[ref.key] = d
		}
		if ref.vpn {
			d.VPNUp += tr.Up
			d.VPNDown += tr.Down
		} else {
			d.DirUp += tr.Up
			d.DirDown += tr.Down
		}
	}
	var deltas []store.UsageDelta
	for _, d := range byKey {
		deltas = append(deltas, *d)
	}
	dctx := context.WithoutCancel(ctx)
	if err := a.db.AddUsage(dctx, time.Now(), deltas); err != nil {
		slog.Warn("saving usage", "err", err)
	}
	for _, d := range deltas {
		vpn := d.VPNUp + d.VPNDown
		if vpn == 0 {
			continue
		}
		crossed, err := a.db.AddRouteUsage(dctx, d.Key, vpn)
		if err != nil {
			continue
		}
		if crossed {
			r, _ := a.db.GetRoute(dctx, d.Key)
			action := "international traffic is now blocked"
			if r.QuotaAction == "direct" {
				action = "it now uses the normal network (no VPN)"
			}
			a.event(dctx, "warn", "quota", r.Label+" reached its VPN quota of "+fmtBytes(r.QuotaBytes)+"; "+action, nil)
			a.requestReconcile()
		}
	}
}

// periodStart returns the start of the quota period containing t, in the
// container's local time zone (set TZ, e.g. Asia/Tehran).
func periodStart(period string, t time.Time, created int64) int64 {
	t = t.In(time.Local)
	y, m, d := t.Date()
	switch period {
	case "daily":
		return time.Date(y, m, d, 0, 0, 0, 0, time.Local).Unix()
	case "weekly":
		// Weeks start on Saturday, as in Iran.
		off := (int(t.Weekday()) + 1) % 7
		return time.Date(y, m, d-off, 0, 0, 0, 0, time.Local).Unix()
	case "monthly":
		return time.Date(y, m, 1, 0, 0, 0, 0, time.Local).Unix()
	default: // total
		return created
	}
}

func (a *Agent) rollQuotaPeriods(ctx context.Context) {
	routes, err := a.db.ListRoutes(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, r := range routes {
		if r.QuotaPeriod == "total" {
			continue
		}
		start := periodStart(r.QuotaPeriod, now, r.CreatedAt)
		if r.PeriodStart < start {
			if err := a.db.ResetRouteUsage(ctx, r.Key, start); err != nil {
				continue
			}
			if r.Exceeded {
				a.event(ctx, "info", "quota", "New "+r.QuotaPeriod+" quota period: "+r.Label+" uses the VPN again", nil)
				a.requestReconcile()
			}
		}
	}
}

// dockerStatsLoop samples CPU, memory and network counters of running
// containers every 30 seconds.
func (a *Agent) dockerStatsLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		a.sampleDockerStats(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *Agent) sampleDockerStats(ctx context.Context) {
	a.mu.Lock()
	var running []docker.Container
	for _, c := range a.containers {
		if c.State == "running" {
			running = append(running, c)
		}
	}
	a.mu.Unlock()
	if len(running) > 300 {
		running = running[:300]
	}
	type result struct {
		c docker.Container
		s docker.Stats
	}
	results := make(chan result, len(running))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, c := range running {
		wg.Add(1)
		go func(c docker.Container) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			s, err := a.docker.Stats(sctx, c.ID)
			if err == nil {
				results <- result{c, s}
			}
		}(c)
	}
	wg.Wait()
	close(results)

	byKey := map[string]*store.UsageDelta{}
	seen := map[string]bool{}
	a.mu.Lock()
	for r := range results {
		seen[r.c.ID] = true
		ls := a.live[r.c.ID]
		if ls == nil {
			ls = &liveStats{}
			a.live[r.c.ID] = ls
		}
		key := ContainerKey(r.c)
		d := byKey[key]
		if d == nil {
			d = &store.UsageDelta{Key: key}
			byKey[key] = d
		}
		prev := ls.prev
		ls.Mem, ls.MemLimit, ls.At = r.s.MemoryBytes, r.s.MemoryLimit, time.Now()
		if !prev.Read.IsZero() {
			secs := r.s.Read.Sub(prev.Read).Seconds()
			if r.s.CPUTotal >= prev.CPUTotal && r.s.SystemCPU > prev.SystemCPU {
				cpus := float64(max(1, r.s.OnlineCPUs))
				ls.CPU = float64(r.s.CPUTotal-prev.CPUTotal) / float64(r.s.SystemCPU-prev.SystemCPU) * cpus * 100
				d.CPU += ls.CPU
				d.HasCPU = true
			}
			if r.s.RxBytes >= prev.RxBytes && r.s.TxBytes >= prev.TxBytes {
				rx, tx := r.s.RxBytes-prev.RxBytes, r.s.TxBytes-prev.TxBytes
				d.NetRx += int64(rx)
				d.NetTx += int64(tx)
				if secs > 0 {
					ls.RxRate, ls.TxRate = float64(rx)/secs, float64(tx)/secs
				}
			}
		}
		d.Mem = max(d.Mem, int64(r.s.MemoryBytes))
		ls.prev = r.s
	}
	for id := range a.live {
		if !seen[id] {
			delete(a.live, id)
		}
	}
	a.mu.Unlock()

	var deltas []store.UsageDelta
	for _, d := range byKey {
		deltas = append(deltas, *d)
	}
	if err := a.db.AddUsage(context.WithoutCancel(ctx), time.Now(), deltas); err != nil {
		slog.Warn("saving container stats", "err", err)
	}
}

func (a *Agent) maintenanceLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := a.db.Prune(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("pruning old data", "err", err)
		}
		a.auth.pruneLimiter()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
