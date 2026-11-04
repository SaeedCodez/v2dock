package xcore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/stats"

	// Register every Xray protocol, transport and the JSON loader.
	_ "github.com/xtls/xray-core/main/distro/all"
)

// Instance is a running Xray instance.
type Instance struct {
	mu    sync.Mutex
	inst  *core.Instance
	stats stats.Manager
}

// Start builds and starts an instance from a JSON config.
func Start(config []byte) (*Instance, error) {
	cfg, err := core.LoadConfig("json", bytes.NewReader(config))
	if err != nil {
		return nil, cleanErr(err)
	}
	inst, err := core.New(cfg)
	if err != nil {
		return nil, cleanErr(err)
	}
	if err := inst.Start(); err != nil {
		_ = inst.Close()
		return nil, cleanErr(err)
	}
	i := &Instance{inst: inst}
	if sm, ok := inst.GetFeature(stats.ManagerType()).(stats.Manager); ok {
		i.stats = sm
	}
	return i, nil
}

// Validate reports whether config builds, without starting anything.
func Validate(config []byte) error {
	cfg, err := core.LoadConfig("json", bytes.NewReader(config))
	if err != nil {
		return cleanErr(err)
	}
	inst, err := core.New(cfg)
	if err != nil {
		return cleanErr(err)
	}
	return inst.Close()
}

// Close stops the instance. It is safe to call more than once.
func (i *Instance) Close() error {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.inst == nil {
		return nil
	}
	err := i.inst.Close()
	i.inst = nil
	return err
}

// Traffic is bytes since the last read for one outbound tag.
type Traffic struct {
	Up, Down int64
}

// TakeCounters returns the outbound traffic counters and resets them.
func (i *Instance) TakeCounters() map[string]Traffic {
	out := map[string]Traffic{}
	if i == nil {
		return out
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.inst == nil || i.stats == nil {
		return out
	}
	type visitor interface {
		VisitCounters(func(string, stats.Counter) bool)
	}
	v, ok := i.stats.(visitor)
	if !ok {
		return out
	}
	v.VisitCounters(func(name string, c stats.Counter) bool {
		// outbound>>>TAG>>>traffic>>>uplink
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[0] != "outbound" || parts[2] != "traffic" {
			return true
		}
		n := c.Set(0)
		if n == 0 {
			return true
		}
		t := out[parts[1]]
		if parts[3] == "uplink" {
			t.Up += n
		} else {
			t.Down += n
		}
		out[parts[1]] = t
		return true
	})
	return out
}

// Dial opens a connection through the instance's default outbound.
func (i *Instance) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	i.mu.Lock()
	inst := i.inst
	i.mu.Unlock()
	if inst == nil {
		return nil, errors.New("xray instance is closed")
	}
	dest, err := xnet.ParseDestination(network + ":" + addr)
	if err != nil {
		return nil, err
	}
	return core.Dial(ctx, inst, dest)
}

// HTTPClient returns a client whose connections go through the instance.
func (i *Instance) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           i.Dial,
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// RealDelay measures one HTTP request to testURL through outbound ob. This is
// the "real delay" other clients show: it includes the proxy handshake.
func RealDelay(ctx context.Context, ob map[string]any, testURL string, timeout time.Duration) (time.Duration, error) {
	cfg, err := BuildProbe(ob)
	if err != nil {
		return 0, err
	}
	inst, err := Start(cfg)
	if err != nil {
		return 0, err
	}
	defer inst.Close()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := inst.HTTPClient(timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, simplifyNetErr(err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	elapsed := time.Since(start)
	if resp.StatusCode >= 500 {
		return 0, fmt.Errorf("test URL answered HTTP %d", resp.StatusCode)
	}
	return elapsed, nil
}

// TCPPing measures a TCP handshake to the server itself (no proxy protocol).
// DNS resolution happens first and is not counted.
func TCPPing(ctx context.Context, host string, port int, timeout time.Duration) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addrs) == 0 {
			return 0, errors.New("DNS lookup failed")
		}
		ip = addrs[0].IP
		for _, a := range addrs {
			if a.IP.To4() != nil {
				ip = a.IP
				break
			}
		}
	}
	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), fmt.Sprint(port)))
	if err != nil {
		return 0, simplifyNetErr(err)
	}
	elapsed := time.Since(start)
	conn.Close()
	return elapsed, nil
}

func simplifyNetErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return errors.New("timeout")
	case strings.Contains(msg, "connection refused"):
		return errors.New("connection refused")
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "host is down"):
		return errors.New("no route to host")
	case strings.Contains(msg, "network is unreachable"):
		return errors.New("network unreachable")
	case strings.Contains(msg, "no such host"):
		return errors.New("DNS lookup failed")
	case strings.Contains(msg, "EOF"):
		return errors.New("connection closed by server")
	case strings.Contains(msg, "certificate"):
		return errors.New("TLS certificate error")
	}
	if len(msg) > 160 {
		msg = msg[:160]
	}
	return errors.New(msg)
}

// cleanErr trims Xray's long wrapped error chains to their useful tail.
func cleanErr(err error) error {
	msg := err.Error()
	if i := strings.LastIndex(msg, " > "); i >= 0 {
		msg = msg[i+3:]
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return errors.New(msg)
}
