package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/SaeedCodez/v2dock/internal/store"
	"github.com/SaeedCodez/v2dock/internal/xcore"
)

// testJob is a batch of TCP or real-delay tests. One runs at a time.
type testJob struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // tcp | real
	Total    int    `json:"total"`
	Done     int    `json:"done"`
	OK       int    `json:"ok"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished,omitempty"`
	cancel   context.CancelFunc
}

var errBusy = errors.New("another test is running")

func (a *Agent) jobSnapshot() (cur, last *testJob) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.job != nil {
		c := *a.job
		cur = &c
	}
	if a.lastJob != nil {
		l := *a.lastJob
		last = &l
	}
	return
}

// startTests runs tests in the background and returns immediately.
func (a *Agent) startTests(ctx context.Context, kind string, servers []store.Server) (*testJob, error) {
	if len(servers) == 0 {
		return nil, errors.New("no servers to test")
	}
	s, _ := a.db.LoadSettings(ctx)
	jctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	job := &testJob{ID: randomID(6), Kind: kind, Total: len(servers), Started: time.Now().Unix(), cancel: cancel}
	a.mu.Lock()
	if a.job != nil {
		a.mu.Unlock()
		cancel()
		return nil, errBusy
	}
	a.job = job
	a.mu.Unlock()

	go func() {
		defer cancel()
		conc := 24
		if kind == "real" {
			conc = 10
		}
		a.runTests(jctx, kind, servers, conc, s.TestURL, func(ok bool) {
			a.mu.Lock()
			job.Done++
			if ok {
				job.OK++
			}
			a.mu.Unlock()
		})
		a.mu.Lock()
		job.Finished = time.Now().Unix()
		a.lastJob = job
		a.job = nil
		a.mu.Unlock()
	}()
	c := *job
	return &c, nil
}

func (a *Agent) cancelTests() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.job == nil {
		return false
	}
	a.job.cancel()
	return true
}

type testResult struct {
	server store.Server
	ms     int
	err    error
}

// runTests tests servers with bounded concurrency and stores each result.
func (a *Agent) runTests(ctx context.Context, kind string, servers []store.Server, conc int, testURL string, progress func(bool)) []testResult {
	results := make([]testResult, len(servers))
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(i int, srv store.Server) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = testResult{server: srv, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			var d time.Duration
			var err error
			if kind == "tcp" {
				d, err = xcore.TCPPing(ctx, srv.Address, srv.Port, 5*time.Second)
			} else {
				d, err = xcore.RealDelay(ctx, srv.Outbound, testURL, 10*time.Second)
			}
			if ctx.Err() != nil && err != nil {
				results[i] = testResult{server: srv, err: ctx.Err()}
				return
			}
			ms := int(d.Milliseconds())
			if err == nil && ms == 0 {
				ms = 1
			}
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			dctx := context.WithoutCancel(ctx)
			if kind == "tcp" {
				_ = a.db.RecordTCP(dctx, srv.ID, ms, err == nil, errMsg)
			} else {
				_ = a.db.RecordDelay(dctx, srv.ID, ms, err == nil, errMsg)
			}
			results[i] = testResult{server: srv, ms: ms, err: err}
			if progress != nil {
				progress(err == nil)
			}
		}(i, srv)
	}
	wg.Wait()
	return results
}

// connect makes server the active one and applies it.
func (a *Agent) connect(ctx context.Context, srv store.Server, p *principal, reason string) error {
	cfg, err := xcore.BuildProbe(srv.Outbound)
	if err == nil {
		err = xcore.Validate(cfg)
	}
	if err != nil {
		return fmt.Errorf("this server's configuration is not usable: %w", err)
	}
	s, err := a.db.LoadSettings(ctx)
	if err != nil {
		return err
	}
	prev := s.ActiveName
	wasConnected := s.Connected
	s.Connected = true
	s.ActiveServerID = srv.ID
	s.ActiveName = srv.Name
	s.ActiveProtocol = srv.Protocol
	s.ActiveAddress = srv.Address
	s.ActiveSubID = srv.SubscriptionID
	s.ActiveOutbound = srv.Outbound
	s.ConnectedAt = time.Now().Unix()
	if err := a.db.SaveSettings(ctx, s); err != nil {
		return err
	}
	a.mu.Lock()
	a.health = health{}
	a.mu.Unlock()
	if err := a.Reconcile(ctx); err != nil {
		return err
	}
	msg := "Connected to " + srv.Name
	if reason != "" {
		msg = fmt.Sprintf("Switched from %s to %s (%s)", prev, srv.Name, reason)
	} else if wasConnected && prev != "" && prev != srv.Name {
		msg = fmt.Sprintf("Switched from %s to %s", prev, srv.Name)
	}
	a.event(ctx, "info", "connection", msg, p)
	go a.checkHealth(context.WithoutCancel(ctx))
	return nil
}

func (a *Agent) disconnect(ctx context.Context, p *principal) error {
	s, err := a.db.LoadSettings(ctx)
	if err != nil {
		return err
	}
	s.Connected = false
	if err := a.db.SaveSettings(ctx, s); err != nil {
		return err
	}
	a.mu.Lock()
	a.health = health{}
	a.mu.Unlock()
	if err := a.Reconcile(ctx); err != nil {
		return err
	}
	a.event(ctx, "info", "connection", "Disconnected; routed containers use their normal network", p)
	return nil
}

// healthLoop checks the active server and fails over when it stops working.
func (a *Agent) healthLoop(ctx context.Context) {
	for {
		s, _ := a.db.LoadSettings(ctx)
		interval := time.Duration(max(10, s.HealthIntervalSec)) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		a.checkHealth(ctx)
	}
}

func (a *Agent) checkHealth(ctx context.Context) {
	s, err := a.db.LoadSettings(ctx)
	if err != nil || !s.Connected || s.ActiveOutbound == nil {
		return
	}
	a.mu.Lock()
	if a.health.Checking {
		a.mu.Unlock()
		return
	}
	a.health.Checking = true
	a.mu.Unlock()

	d, err := xcore.RealDelay(ctx, s.ActiveOutbound, s.TestURL, 10*time.Second)

	a.mu.Lock()
	h := &a.health
	h.Checking = false
	h.At = time.Now().Unix()
	if err == nil {
		recovered := h.Failures >= max(1, s.FailoverThreshold)
		h.OK, h.DelayMs, h.Error, h.Failures = true, max(1, int(d.Milliseconds())), "", 0
		a.mu.Unlock()
		if recovered {
			a.event(ctx, "info", "health", s.ActiveName+" is reachable again", nil)
		}
		return
	}
	h.OK, h.Error = false, err.Error()
	h.Failures++
	failures := h.Failures
	canFailover := s.AutoFailover && time.Now().After(a.failoverAfter)
	a.mu.Unlock()

	threshold := max(1, s.FailoverThreshold)
	if failures == threshold {
		a.event(ctx, "warn", "health", fmt.Sprintf("%s failed %d health checks in a row (%s)", s.ActiveName, failures, err.Error()), nil)
	}
	if failures >= threshold && canFailover {
		a.failover(ctx, s)
	}
}

// failover tests candidate servers and switches to the fastest working one.
func (a *Agent) failover(ctx context.Context, s store.Settings) {
	all, err := a.db.ListServers(ctx, "")
	if err != nil {
		return
	}
	var cands []store.Server
	for _, srv := range all {
		if srv.ID == s.ActiveServerID {
			continue
		}
		if s.FailoverScope == "subscription" && srv.SubscriptionID != s.ActiveSubID {
			continue
		}
		cands = append(cands, srv)
	}
	if len(cands) == 0 {
		a.mu.Lock()
		a.failoverAfter = time.Now().Add(5 * time.Minute)
		a.mu.Unlock()
		a.event(ctx, "warn", "failover", "Active server is down and there is no other server to switch to", nil)
		return
	}
	// Prefer the same subscription, then servers that worked recently.
	sort.SliceStable(cands, func(i, j int) bool {
		si, sj := cands[i].SubscriptionID == s.ActiveSubID, cands[j].SubscriptionID == s.ActiveSubID
		if si != sj {
			return si
		}
		di, dj := cands[i].DelayMs, cands[j].DelayMs
		if (di != nil) != (dj != nil) {
			return di != nil
		}
		if di != nil && dj != nil {
			return *di < *dj
		}
		return false
	})
	if len(cands) > 40 {
		cands = cands[:40]
	}
	a.event(ctx, "warn", "failover", fmt.Sprintf("Active server %s is down; testing %d other servers", s.ActiveName, len(cands)), nil)
	results := a.runTests(ctx, "real", cands, 10, s.TestURL, nil)
	var best *testResult
	for i := range results {
		r := &results[i]
		if r.err == nil && (best == nil || r.ms < best.ms) {
			best = r
		}
	}
	if best == nil {
		a.mu.Lock()
		a.failoverAfter = time.Now().Add(5 * time.Minute)
		a.mu.Unlock()
		a.event(ctx, "error", "failover", "No working server found; will retry in 5 minutes", nil)
		return
	}
	if err := a.connect(ctx, best.server, nil, fmt.Sprintf("automatic failover, %d ms", best.ms)); err != nil {
		a.event(ctx, "error", "failover", "Switching to "+best.server.Name+" failed: "+err.Error(), nil)
		return
	}
	a.mu.Lock()
	a.health.LastSwitch = time.Now().Unix()
	a.failoverAfter = time.Now().Add(2 * time.Minute)
	a.mu.Unlock()
}
