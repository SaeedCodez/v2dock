// Package cli is the v2dock command line. It talks to the agent over its unix
// socket (inside the containers, via `docker exec`) or to the web gateway
// with an API token (from anywhere).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/SaeedCodez/v2dock/internal/wire"
)

// Exit codes. Scripts and agents can rely on these.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitNotFound    = 3
	ExitAuth        = 4
	ExitConflict    = 5
	ExitUnreachable = 6
)

type client struct {
	base   string
	token  string
	hc     *http.Client
	remote bool
}

func newClient(g *globals) (*client, error) {
	if g.url != "" {
		u, err := url.Parse(g.url)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("--url must be like https://v2dock.example.com")
		}
		if g.token == "" {
			return nil, fmt.Errorf("remote use needs an API token: --token or V2DOCK_TOKEN (create one in Settings or with `v2dock token create`)")
		}
		if u.Scheme == "http" && !isLocalHost(u.Hostname()) {
			fmt.Fprintln(os.Stderr, "warning: sending an API token over plain http")
		}
		return &client{base: strings.TrimRight(u.String(), "/"), token: g.token, remote: true, hc: &http.Client{Timeout: 180 * time.Second}}, nil
	}
	sock := g.socket
	return &client{
		base: "http://agent",
		hc: &http.Client{Timeout: 180 * time.Second, Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}},
	}, nil
}

func isLocalHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// apiErr is a failed API call.
type apiErr struct {
	status int
	code   string
	msg    string
	body   json.RawMessage
}

func (e *apiErr) Error() string { return e.msg }

func (e *apiErr) exitCode() int {
	switch {
	case e.status == 0:
		return ExitUnreachable
	case e.status == 401 || e.status == 403:
		return ExitAuth
	case e.status == 404:
		return ExitNotFound
	case e.status == 409 || e.status == 429:
		return ExitConflict
	case e.status == 400:
		return ExitUsage
	}
	return ExitError
}

func (c *client) do(method, path string, in any, out any) (json.RawMessage, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set(wire.HeaderCSRF, "1")
	req.Header.Set("User-Agent", "v2dock-cli")
	resp, err := c.hc.Do(req)
	if err != nil {
		hint := "is the agent running? (inside the container the socket is " + wire.DefaultAgentSocket + ")"
		if c.remote {
			hint = "check --url"
		}
		return nil, &apiErr{status: 0, code: "unreachable", msg: fmt.Sprintf("cannot reach v2dock: %v; %s", err, hint)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
			if e.Error == "" {
				e.Error = resp.Status
			}
		}
		return raw, &apiErr{status: resp.StatusCode, code: e.Code, msg: e.Error, body: raw}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, fmt.Errorf("unexpected response: %w", err)
		}
	}
	return raw, nil
}
