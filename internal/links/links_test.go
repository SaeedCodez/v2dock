package links

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

const (
	vlessReality = "vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@de1.example.com:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=SbVKOEMjK0sIlbwg4akyBg5mL5KZwwB-ed4eEE7YnRc&sid=6ba85179e30d4fc2&type=tcp&headerType=none#%F0%9F%87%A9%F0%9F%87%AA%20Germany-1"
	vlessWS      = "vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@1.2.3.4:8443?encryption=none&security=tls&sni=cdn.example.com&type=ws&host=cdn.example.com&path=%2Fws%3Fed%3D2048#WS"
	trojanGRPC   = "trojan://s3cr3t@tr.example.com:443?security=tls&type=grpc&serviceName=gun&sni=tr.example.com#Trojan"
	ss2022       = "ss://2022-blake3-aes-128-gcm:Y2hhbmdlbWVjaGFuZ2VtZQ%3D%3D@ss.example.com:8388#SS-2022"
	hy2          = "hysteria2://pass@hy.example.com:4433?sni=hy.example.com#HY2"
)

func vmessLink() string {
	j := `{"v":"2","ps":"VMess WS","add":"vm.example.com","port":"443","id":"0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e","aid":"0","scy":"auto","net":"ws","type":"none","host":"vm.example.com","path":"/vm","tls":"tls","sni":"vm.example.com"}`
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(j))
}

func ssLegacy() string {
	return "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pw@5.6.7.8:8388")) + "#Legacy"
}

func TestParseLinks(t *testing.T) {
	cases := []struct {
		link, proto, addr, transport, security, name string
		port                                         int
	}{
		{vlessReality, "vless", "de1.example.com", "raw", "reality", "🇩🇪 Germany-1", 443},
		{vlessWS, "vless", "1.2.3.4", "ws", "tls", "WS", 8443},
		{trojanGRPC, "trojan", "tr.example.com", "grpc", "tls", "Trojan", 443},
		{vmessLink(), "vmess", "vm.example.com", "ws", "tls", "VMess WS", 443},
		{ss2022, "shadowsocks", "ss.example.com", "raw", "none", "SS-2022", 8388},
		{ssLegacy(), "shadowsocks", "5.6.7.8", "raw", "none", "Legacy", 8388},
		{hy2, "hysteria2", "hy.example.com", "quic", "tls", "HY2", 4433},
	}
	for _, c := range cases {
		s, err := ParseLink(c.link)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Protocol != c.proto || s.Address != c.addr || s.Port != c.port || s.Transport != c.transport || s.Security != c.security || s.Name != c.name {
			t.Errorf("%s: got %+v", c.name, s)
		}
		if len(s.ID) != 12 {
			t.Errorf("%s: id %q", c.name, s.ID)
		}
		if _, err := json.Marshal(s.Outbound); err != nil {
			t.Errorf("%s: outbound does not marshal: %v", c.name, err)
		}
	}
}

func TestStableIDIgnoresName(t *testing.T) {
	a, _ := ParseLink(strings.Replace(vlessWS, "#WS", "#Other", 1))
	b, _ := ParseLink(vlessWS)
	if a.ID != b.ID {
		t.Fatal("renaming a server changed its id")
	}
}

func TestLinkNeverSerialised(t *testing.T) {
	s, _ := ParseLink(trojanGRPC)
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "s3cr3t") {
		t.Fatal("credentials leaked into JSON")
	}
}

func TestRejects(t *testing.T) {
	bad := []string{
		"vless://@host:443", // no id
		"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@host", // no port
		"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@host:443?type=h2",
		"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@host:443?security=reality", // no pbk
		"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@ho$t;rm:443",
		"vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@host:99999",
		"ss://YWVzLTI1Ni1nY206cHc@host:8388?plugin=obfs-local",
		"hysteria2://pw@host:443?obfs=salamander",
		"socks://host:1080",
		"https://example.com",
	}
	for _, l := range bad {
		if _, err := ParseLink(l); err == nil {
			t.Errorf("accepted %q", l)
		}
	}
}

func TestParseSubscription(t *testing.T) {
	body := strings.Join([]string{vlessReality, "garbage", vmessLink(), vlessReality, "", trojanGRPC}, "\n")
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	for _, in := range []string{body, enc, strings.TrimRight(enc, "=")} {
		res, err := ParseSubscription([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Servers) != 3 {
			t.Fatalf("want 3 servers (duplicate dropped), got %d", len(res.Servers))
		}
		if len(res.Skipped) != 1 {
			t.Fatalf("want 1 skipped, got %v", res.Skipped)
		}
	}
	if _, err := ParseSubscription([]byte("proxies:\n  - name: a")); err == nil {
		t.Fatal("accepted clash yaml")
	}
	if _, err := ParseSubscription([]byte("")); err == nil {
		t.Fatal("accepted empty")
	}
}

func TestCleanName(t *testing.T) {
	if got := cleanName("a\x00b‮c\n", 10); got != "abc" {
		t.Fatalf("got %q", got)
	}
	if got := cleanName(strings.Repeat("ا", 200), 120); len([]rune(got)) != 120 {
		t.Fatal("not truncated")
	}
}

func TestXHTTPExtraIsWhitelisted(t *testing.T) {
	evil := `{"xPaddingBytes":"100-1000","xmux":{"maxConcurrency":"16-32","evil":1},` +
		`"downloadSettings":{"address":"127.0.0.1","port":22,"tlsSettings":{"masterKeyLog":"/data/v2dock.db"},"sockopt":{"mark":1}},` +
		`"headers":{"X-A":"b","X-Bad":"a\r\nb"}}`
	link := "vless://0b4e7a9e-6b1f-4d3a-9c2e-1f2a3b4c5d6e@x.example.com:443?security=tls&type=xhttp&path=%2Fx&extra=" + url.QueryEscape(evil) + "#X"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s.Outbound)
	out := string(b)
	for _, bad := range []string{"downloadSettings", "masterKeyLog", "sockopt", "evil", "X-Bad"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s leaked into the outbound: %s", bad, out)
		}
	}
	for _, good := range []string{"xPaddingBytes", "maxConcurrency", "X-A"} {
		if !strings.Contains(out, good) {
			t.Errorf("%s was dropped: %s", good, out)
		}
	}
	if s.Warning == "" {
		t.Error("dropping options should leave a warning")
	}
}

func TestBidiAndEscapesStripped(t *testing.T) {
	in := "a‭b⁦c⁩\x1b[31md؜e"
	if got := cleanName(in, 50); got != "abc[31mde" {
		t.Fatalf("got %q", got)
	}
}
