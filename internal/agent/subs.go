package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SaeedCodez/v2dock/internal/links"
	"github.com/SaeedCodez/v2dock/internal/store"
	"github.com/SaeedCodez/v2dock/internal/xcore"
)

// Panels pick the response format from the User-Agent; this one gets the
// standard base64 list of share links from Marzban, 3x-ui, Hiddify and others.
const subUserAgent = "v2rayN/7.10.0"

func validateSubURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2048 {
		return nil, errors.New("URL is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("not a valid URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("subscription URL must start with https:// or http://")
	}
	return u, nil
}

// maskURL hides the secret part of a subscription URL (usually a token in
// the path or query) for display.
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	path := u.Path
	if len(path) > 6 {
		path = path[:min(len(path), 6)] + "…"
	}
	return u.Scheme + "://" + u.Host + path
}

type fetchResult struct {
	body     []byte
	userInfo *store.UserInfo
	title    string
}

func fetchWith(ctx context.Context, client *http.Client, rawURL string) (fetchResult, error) {
	var res fetchResult
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("User-Agent", subUserAgent)
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return res, fmt.Errorf("download failed: %s", trimErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("server answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, links.MaxSubscriptionBytes+1))
	if err != nil {
		return res, fmt.Errorf("download failed: %s", trimErr(err))
	}
	res.body = body
	res.userInfo = parseUserInfo(resp.Header.Get("Subscription-Userinfo"))
	res.title = parseTitle(resp.Header.Get("Profile-Title"))
	return res, nil
}

func trimErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i > 4 {
		s = s[i+2:]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func parseUserInfo(h string) *store.UserInfo {
	if h == "" {
		return nil
	}
	var ui store.UserInfo
	found := false
	for _, part := range strings.Split(h, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || n < 0 {
			continue
		}
		found = true
		switch strings.ToLower(k) {
		case "upload":
			ui.Upload = int64(n)
		case "download":
			ui.Download = int64(n)
		case "total":
			ui.Total = int64(n)
		case "expire":
			ui.Expire = int64(n)
		}
	}
	if !found {
		return nil
	}
	return &ui
}

func parseTitle(h string) string {
	if strings.HasPrefix(h, "base64:") {
		if b, err := base64.StdEncoding.DecodeString(h[7:]); err == nil {
			h = string(b)
		}
	}
	h = strings.TrimSpace(h)
	if len(h) > 80 {
		h = h[:80]
	}
	return h
}

func directClient() *http.Client {
	return &http.Client{
		Timeout: 25 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return errors.New("redirect to a non-HTTP URL")
			}
			return nil
		},
	}
}

// refreshSubscription downloads and stores a subscription. When the direct
// download fails (as it will during an international shutdown) and a server
// is connected, it retries through that server.
func (a *Agent) refreshSubscription(ctx context.Context, sub store.Subscription, p *principal) (int, error) {
	if sub.Kind != "url" {
		return 0, errors.New("manual server lists have nothing to refresh")
	}
	res, err := fetchWith(ctx, directClient(), sub.URL)
	via := false
	if err != nil {
		if s, _ := a.db.LoadSettings(ctx); s.Connected && s.ActiveOutbound != nil {
			if r2, err2 := a.fetchViaServer(ctx, s.ActiveOutbound, sub.URL); err2 == nil {
				res, err, via = r2, nil, true
			} else {
				err = fmt.Errorf("%v; through the VPN: %v", err, err2)
			}
		}
	}
	if err != nil {
		_ = a.db.RecordFetch(ctx, sub.ID, false, err.Error(), nil, nil, false)
		a.event(ctx, "warn", "subscription", "Updating "+sub.Name+" failed: "+err.Error(), p)
		return 0, err
	}
	parsed, perr := links.ParseSubscription(res.body)
	if perr != nil {
		_ = a.db.RecordFetch(ctx, sub.ID, false, perr.Error(), nil, nil, via)
		a.event(ctx, "warn", "subscription", "Updating "+sub.Name+" failed: "+perr.Error(), p)
		return 0, perr
	}
	servers := make([]store.Server, 0, len(parsed.Servers))
	for _, s := range parsed.Servers {
		servers = append(servers, fromLink(s, sub.ID))
	}
	if err := a.db.ReplaceServers(ctx, sub.ID, servers); err != nil {
		return 0, err
	}
	_ = a.db.RecordFetch(ctx, sub.ID, true, "", res.userInfo, parsed.Skipped, via)
	if sub.Name == "" || strings.HasPrefix(sub.Name, "Subscription ") {
		if res.title != "" {
			_ = a.db.UpdateSubscription(ctx, sub.ID, res.title, sub.URL, sub.AutoUpdateHours)
		}
	}
	msg := fmt.Sprintf("Updated %s: %d servers", sub.Name, len(servers))
	if len(parsed.Skipped) > 0 {
		msg += fmt.Sprintf(", %d skipped", len(parsed.Skipped))
	}
	if via {
		msg += " (downloaded through the VPN)"
	}
	a.event(ctx, "info", "subscription", msg, p)
	return len(servers), nil
}

func (a *Agent) fetchViaServer(ctx context.Context, ob map[string]any, rawURL string) (fetchResult, error) {
	cfg, err := xcore.BuildProbe(ob)
	if err != nil {
		return fetchResult{}, err
	}
	inst, err := xcore.Start(cfg)
	if err != nil {
		return fetchResult{}, err
	}
	defer inst.Close()
	return fetchWith(ctx, inst.HTTPClient(30*time.Second), rawURL)
}

func fromLink(s links.Server, subID string) store.Server {
	return store.Server{
		ID: s.ID, SubscriptionID: subID, Name: s.Name, Protocol: s.Protocol, Address: s.Address,
		Port: s.Port, Transport: s.Transport, Security: s.Security, Warning: s.Warning,
		Link: s.Link, Outbound: s.Outbound,
	}
}

// subscriptionLoop refreshes subscriptions on their schedule.
func (a *Agent) subscriptionLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		subs, err := a.db.ListSubscriptions(ctx)
		if err != nil {
			continue
		}
		for _, s := range subs {
			if s.Kind != "url" || s.AutoUpdateHours <= 0 {
				continue
			}
			due := time.Unix(s.LastFetchAt, 0).Add(time.Duration(s.AutoUpdateHours) * time.Hour)
			if time.Now().After(due) {
				fctx, cancel := context.WithTimeout(ctx, 90*time.Second)
				_, _ = a.refreshSubscription(fctx, s, nil)
				cancel()
			}
		}
	}
}
