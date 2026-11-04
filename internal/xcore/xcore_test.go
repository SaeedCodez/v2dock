package xcore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SaeedCodez/v2dock/internal/links"
)

var sampleLinks = []string{
	"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@de1.example.com:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=SbVKOEMjK0sIlbwg4akyBg5mL5KZwwB-ed4eEE7YnRc&sid=6ba85179e30d4fc2&type=tcp#R",
	"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@1.2.3.4:8443?encryption=none&security=tls&sni=cdn.example.com&type=ws&host=cdn.example.com&path=%2Fws&allowInsecure=1#WS",
	"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@1.2.3.4:443?encryption=none&security=tls&sni=x.example.com&type=xhttp&path=%2Fx&mode=auto#XHTTP",
	"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@10.0.0.5:443?type=httpupgrade&path=%2Fhu&security=none#HU",
	"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@192.168.1.5:80?type=tcp&headerType=http&host=a.com&path=%2F#HTTPHDR",
	"trojan://s3cr3t@tr.example.com:443?security=tls&type=grpc&serviceName=gun&sni=tr.example.com#T",
	"ss://2022-blake3-aes-128-gcm:Y2hhbmdlbWVjaGFuZ2VtZQ%3D%3D@ss.example.com:8388#SS",
	"ss://" + base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw")) + "@5.6.7.8:8388#SS2",
	"hysteria2://pass@hy.example.com:4433?sni=hy.example.com#HY2",
	"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"VM","add":"vm.example.com","port":443,"id":"0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e","aid":0,"net":"grpc","path":"svc","tls":"tls"}`)),
	"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"VMK","add":"vm.example.com","port":"8080","id":"0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e","aid":"0","net":"kcp","type":"none","tls":""}`)),
}

func TestOutboundsBuild(t *testing.T) {
	for _, l := range sampleLinks {
		s, err := links.ParseLink(l)
		if err != nil {
			t.Fatalf("parse %s: %v", l[:30], err)
		}
		cfg, err := BuildProbe(s.Outbound)
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(cfg); err != nil {
			t.Errorf("%s (%s/%s/%s): %v", s.Name, s.Protocol, s.Transport, s.Security, err)
		}
	}
}

// Xray refuses unencrypted VLESS to public addresses; make sure that
// surfaces as an error rather than silently connecting in plaintext.
func TestPlaintextVLESSToPublicRefused(t *testing.T) {
	s, err := links.ParseLink("vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@8.8.8.8:80?security=none#P")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := BuildProbe(s.Outbound)
	if err := Validate(cfg); err == nil {
		t.Fatal("plaintext VLESS to a public address was accepted")
	}
}

func assetsDir(t *testing.T) string {
	dir, _ := filepath.Abs("../../dev/assets")
	if _, err := os.Stat(filepath.Join(dir, "geosite.dat")); err != nil {
		t.Skip("geo data not downloaded (see dev/README.md)")
	}
	return dir
}

func TestMainConfigBuilds(t *testing.T) {
	t.Setenv("XRAY_LOCATION_ASSET", assetsDir(t))
	s, _ := links.ParseLink(sampleLinks[0])
	for _, bypass := range []bool{true, false} {
		cfg, err := BuildMain(Plan{
			Outbound:   s.Outbound,
			TProxyPort: 12701,
			DNSPort:    12702,
			BypassIran: bypass,
			Routes: []RoutePlan{
				{Key: "compose:site/web", IPs: []netip.Addr{netip.MustParseAddr("172.18.0.5")}, Mode: ModeVPN},
				{Key: "name:db", IPs: []netip.Addr{netip.MustParseAddr("172.18.0.6")}, Mode: ModeBlock},
				{Key: "name:x", IPs: []netip.Addr{netip.MustParseAddr("172.18.0.7")}, Mode: ModeDirect},
				{Key: "name:noip", Mode: ModeVPN},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(cfg); err != nil {
			t.Fatalf("bypass=%v: %v\n%s", bypass, err, cfg)
		}
	}
}

// TestProbeThroughFreedom checks the in-process dial path end to end with a
// "freedom" outbound against a local HTTP server.
func TestProbeThroughFreedom(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}
	go srv.Serve(ln)
	defer srv.Close()

	d, err := RealDelay(context.Background(), map[string]any{"protocol": "freedom"}, "http://"+ln.Addr().String()+"/generate_204", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if d <= 0 {
		t.Fatal("no delay measured")
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	var p int
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	if _, err := TCPPing(context.Background(), host, p, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestCountersCollected(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(make([]byte, 5000)) })}
	go srv.Serve(ln)
	defer srv.Close()
	cfg := []byte(`{"stats":{},"policy":{"system":{"statsOutboundUplink":true,"statsOutboundDownlink":true}},"outbounds":[{"tag":"t1","protocol":"freedom"}]}`)
	inst, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close()
	resp, err := inst.HTTPClient(5 * time.Second).Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10000)
	for {
		if _, err := resp.Body.Read(buf); err != nil {
			break
		}
	}
	resp.Body.Close()
	time.Sleep(100 * time.Millisecond)
	c := inst.TakeCounters()
	if c["t1"].Down < 5000 || c["t1"].Up == 0 {
		t.Fatalf("counters %+v", c)
	}
	if again := inst.TakeCounters(); again["t1"].Down != 0 {
		t.Fatal("counters not reset")
	}
}

// DNS and TCP from addresses that are not selected containers must be
// dropped, never relayed.
func TestUnknownSourcesBlocked(t *testing.T) {
	s, _ := links.ParseLink(sampleLinks[0])
	cfg, err := BuildMain(Plan{
		Outbound: s.Outbound, TProxyPort: 12701, DNSPort: 12702,
		Routes: []RoutePlan{
			{Key: "name:a", IPs: []netip.Addr{netip.MustParseAddr("172.18.0.5")}, Mode: ModeVPN},
			{Key: "name:b", IPs: []netip.Addr{netip.MustParseAddr("172.18.0.6")}, Mode: ModeDirect},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	rules := c.Routing.Rules
	last := rules[len(rules)-1]
	if last["outboundTag"] != "block" || len(last) != 2 {
		t.Fatalf("last rule must block everything else: %v", last)
	}
	for _, r := range rules {
		tags, _ := r["inboundTag"].([]any)
		if len(tags) == 1 && tags[0] == "dns-in" && r["outboundTag"] == "dns-out" {
			src, _ := r["source"].([]any)
			if len(src) != 1 || src[0] != "172.18.0.5" {
				t.Fatalf("DNS must be answered only for VPN clients, got %v", r)
			}
		}
	}
}
