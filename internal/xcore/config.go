// Package xcore runs Xray-core inside the agent process: the long-lived
// transparent-proxy instance and the short-lived instances used to test
// servers. Running in-process means no Xray API port or SOCKS port is ever
// opened on the host.
package xcore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
)

// Route modes for a routed container.
const (
	ModeVPN    = "vpn"    // international traffic through the VPN
	ModeBlock  = "block"  // quota exceeded: international traffic blocked
	ModeDirect = "direct" // quota exceeded: everything goes direct
)

// RoutePlan describes one routed container (or group of containers sharing a
// key, e.g. replicas).
type RoutePlan struct {
	Key  string
	IPs  []netip.Addr
	Mode string
}

// Plan is everything the main instance needs.
type Plan struct {
	Outbound    map[string]any // the active server; required
	TProxyPort  int            // receives redirected TCP connections
	DNSPort     int            // receives redirected DNS (UDP and TCP)
	BypassIran  bool
	RemoteDNS   string // resolver used for international names, through the VPN
	DomesticDNS string // resolver for Iranian names, direct ("localhost" = system)
	Routes      []RoutePlan
	LogLevel    string
}

// RouteTag is the short, stable outbound suffix for a route key.
func RouteTag(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:5])
}

// VPNTag and DirectTag are the per-container outbound tags whose traffic
// counters give the VPN / direct split for that container.
func VPNTag(key string) string    { return "vpn-" + RouteTag(key) }
func DirectTag(key string) string { return "dir-" + RouteTag(key) }

// Iranian destinations. geosite:ir and geoip:ir come from the
// Iran-v2ray-rules data files shipped in the image.
var (
	iranDomains = []string{"regexp:\\.ir$", "geosite:ir"}
	iranIPs     = []string{"geoip:ir"}
)

func cloneOutbound(ob map[string]any, tag string) map[string]any {
	b, _ := json.Marshal(ob)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	out["tag"] = tag
	return out
}

// BuildMain renders the Xray JSON config for the transparent proxy.
func BuildMain(p Plan) ([]byte, error) {
	if p.Outbound == nil {
		return nil, fmt.Errorf("no server selected")
	}
	if p.TProxyPort <= 0 || p.TProxyPort > 65535 || p.DNSPort <= 0 || p.DNSPort > 65535 || p.DNSPort == p.TProxyPort {
		return nil, fmt.Errorf("invalid proxy ports")
	}
	logLevel := p.LogLevel
	if logLevel == "" {
		logLevel = "warning"
	}
	outbounds := []any{
		map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "AsIs"}},
		map[string]any{"tag": "block", "protocol": "blackhole"},
		// A/AAAA queries are answered by the DNS module below; other types
		// (MX, TXT...) are forwarded, through the VPN.
		map[string]any{"tag": "dns-out", "protocol": "dns", "settings": map[string]any{"nonIPQuery": "skip"},
			"streamSettings": map[string]any{"sockopt": map[string]any{"dialerProxy": "proxy"}}},
		cloneOutbound(p.Outbound, "proxy"),
	}
	rules := []any{
		map[string]any{"inboundTag": []string{"dns-in"}, "outboundTag": "dns-out"},
		map[string]any{"inboundTag": []string{"dns-internal"}, "outboundTag": "proxy"},
	}

	routes := append([]RoutePlan(nil), p.Routes...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].Key < routes[j].Key })
	for _, r := range routes {
		if len(r.IPs) == 0 {
			continue
		}
		src := make([]string, 0, len(r.IPs))
		for _, ip := range r.IPs {
			if !ip.Is4() {
				continue
			}
			src = append(src, ip.String())
		}
		if len(src) == 0 {
			continue
		}
		vpnTag, dirTag := VPNTag(r.Key), DirectTag(r.Key)
		outbounds = append(outbounds,
			cloneOutbound(p.Outbound, vpnTag),
			map[string]any{"tag": dirTag, "protocol": "freedom", "settings": map[string]any{"domainStrategy": "AsIs"}},
		)
		if r.Mode == ModeDirect {
			rules = append(rules, map[string]any{"source": src, "outboundTag": dirTag})
			continue
		}
		if p.BypassIran {
			rules = append(rules,
				map[string]any{"source": src, "domain": iranDomains, "outboundTag": dirTag},
				map[string]any{"source": src, "ip": iranIPs, "outboundTag": dirTag},
			)
		}
		// Destinations on private networks never reach the proxy (nftables
		// skips them), but keep a rule as a second line of defence.
		rules = append(rules, map[string]any{"source": src, "ip": []string{"geoip:private"}, "outboundTag": dirTag})
		target := vpnTag
		if r.Mode == ModeBlock {
			target = "block"
		}
		rules = append(rules, map[string]any{"source": src, "outboundTag": target})
	}
	// Anything else that reaches the transparent proxy did not come from a
	// selected container (nftables should make that impossible): drop it
	// rather than relay it.
	rules = append(rules, map[string]any{"inboundTag": []string{"tproxy-in"}, "outboundTag": "block"})

	remote := p.RemoteDNS
	if remote == "" {
		remote = "https://1.1.1.1/dns-query"
	}
	domestic := p.DomesticDNS
	if domestic == "" {
		domestic = "localhost"
	}
	cfg := map[string]any{
		"log":   map[string]any{"loglevel": logLevel, "access": "none", "dnsLog": false},
		"stats": map[string]any{},
		"policy": map[string]any{
			"system": map[string]any{"statsOutboundUplink": true, "statsOutboundDownlink": true},
		},
		"dns": map[string]any{
			"tag":           "dns-internal",
			"queryStrategy": "UseIPv4",
			"servers": []any{
				remote,
				map[string]any{"address": domestic, "domains": iranDomains, "skipFallback": true},
			},
		},
		// Both ports receive connections that nftables redirected (DNAT) to
		// the docker bridge address, so they listen on all addresses; nftables
		// drops anything that reaches them without having been redirected,
		// and unknown sources hit the final "block" rule.
		"inbounds": []any{
			map[string]any{
				"tag":      "tproxy-in",
				"listen":   "0.0.0.0",
				"port":     p.TProxyPort,
				"protocol": "dokodemo-door",
				"settings": map[string]any{"network": "tcp", "followRedirect": true},
				"streamSettings": map[string]any{
					"sockopt": map[string]any{"tproxy": "redirect"},
				},
				"sniffing": map[string]any{
					"enabled":      true,
					"destOverride": []string{"http", "tls"},
					"routeOnly":    true,
				},
			},
			map[string]any{
				"tag":      "dns-in",
				"listen":   "0.0.0.0",
				"port":     p.DNSPort,
				"protocol": "dokodemo-door",
				"settings": map[string]any{"network": "tcp,udp", "address": "1.1.1.1", "port": 53},
			},
		},
		"outbounds": outbounds,
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules":          rules,
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// BuildProbe renders a config with a single outbound and nothing else; it is
// used to test a server and to fetch a subscription through a server.
func BuildProbe(ob map[string]any) ([]byte, error) {
	if ob == nil {
		return nil, fmt.Errorf("no outbound")
	}
	cfg := map[string]any{
		"outbounds": []any{cloneOutbound(ob, "probe")},
	}
	return json.Marshal(cfg)
}
