// Package docker is a minimal, read-only Docker Engine API client, plus the
// filtering socket proxy ("guard") that is the only component allowed to touch
// the real Docker socket.
package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Client talks to a Docker API over a unix socket. It only issues GETs.
type Client struct {
	hc     *http.Client
	stream *http.Client
}

func New(socketPath string) *Client {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}
	return &Client{
		hc:     &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DialContext: dial, MaxIdleConns: 4}},
		stream: &http.Client{Transport: &http.Transport{DialContext: dial}},
	}
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("docker unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(v)
}

func (c *Client) Ping(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/_ping", nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker ping: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Container is the subset of /containers/json the app uses. The list
// endpoint never includes environment variables, so container secrets never
// reach this process.
type Container struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	State       string            `json:"state"`
	Status      string            `json:"status"`
	Created     int64             `json:"created"`
	Labels      map[string]string `json:"-"`
	NetworkMode string            `json:"networkMode"`
	Networks    []Network         `json:"networks"`
}

type Network struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// IPv4s returns the container's IPv4 addresses on all its networks.
func (c Container) IPv4s() []netip.Addr {
	var out []netip.Addr
	for _, n := range c.Networks {
		if a, err := netip.ParseAddr(n.IP); err == nil && a.Is4() {
			out = append(out, a)
		}
	}
	return out
}

type apiContainer struct {
	ID         string            `json:"Id"`
	Names      []string          `json:"Names"`
	Image      string            `json:"Image"`
	State      string            `json:"State"`
	Status     string            `json:"Status"`
	Created    int64             `json:"Created"`
	Labels     map[string]string `json:"Labels"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// List returns all containers, running or not.
func (c *Client) List(ctx context.Context) ([]Container, error) {
	var raw []apiContainer
	if err := c.get(ctx, "/containers/json?all=1", &raw); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		name := r.ID
		if len(r.Names) > 0 {
			name = strings.TrimPrefix(r.Names[0], "/")
		}
		ct := Container{
			ID: r.ID, Name: name, Image: r.Image, State: r.State, Status: r.Status,
			Created: r.Created, Labels: r.Labels, NetworkMode: r.HostConfig.NetworkMode,
		}
		for n, s := range r.NetworkSettings.Networks {
			ct.Networks = append(ct.Networks, Network{Name: n, IP: s.IPAddress})
		}
		out = append(out, ct)
	}
	return out, nil
}

// Stats is one sample of a container's resource usage.
type Stats struct {
	CPUTotal    uint64 // container CPU time, ns
	SystemCPU   uint64 // host CPU time, ns
	OnlineCPUs  int
	MemoryBytes uint64
	MemoryLimit uint64
	RxBytes     uint64
	TxBytes     uint64
	Read        time.Time
}

type apiStats struct {
	Read     time.Time `json:"read"`
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

var idPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

func (c *Client) Stats(ctx context.Context, id string) (Stats, error) {
	if !idPattern.MatchString(id) {
		return Stats{}, errors.New("invalid container id")
	}
	var a apiStats
	if err := c.get(ctx, "/containers/"+id+"/stats?stream=false&one-shot=true", &a); err != nil {
		return Stats{}, err
	}
	mem := a.MemoryStats.Usage
	// Match `docker stats`: exclude reclaimable page cache.
	if v, ok := a.MemoryStats.Stats["inactive_file"]; ok && v < mem {
		mem -= v
	} else if v, ok := a.MemoryStats.Stats["total_inactive_file"]; ok && v < mem {
		mem -= v
	}
	s := Stats{
		CPUTotal: a.CPUStats.CPUUsage.TotalUsage, SystemCPU: a.CPUStats.SystemCPUUsage,
		OnlineCPUs: a.CPUStats.OnlineCPUs, MemoryBytes: mem, MemoryLimit: a.MemoryStats.Limit, Read: a.Read,
	}
	for _, n := range a.Networks {
		s.RxBytes += n.RxBytes
		s.TxBytes += n.TxBytes
	}
	return s, nil
}

// Event is a container or network lifecycle event.
type Event struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
}

// Events streams container and network events until ctx ends or the stream
// breaks.
func (c *Client) Events(ctx context.Context, fn func(Event)) error {
	filters := url.QueryEscape(`{"type":["container","network"]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/events?filters="+filters, nil)
	if err != nil {
		return err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker events: HTTP %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			fn(ev)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}
