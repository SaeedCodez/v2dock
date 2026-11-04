package netfw

import (
	"net/netip"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	f := New(12701, 12702)
	out := f.Render([]netip.Addr{
		netip.MustParseAddr("172.18.0.5"),
		netip.MustParseAddr("172.18.0.5"), // duplicate
		netip.MustParseAddr("10.0.1.2"),
		netip.MustParseAddr("::1"),       // IPv6 ignored
		netip.MustParseAddr("127.0.0.1"), // loopback ignored
	})
	if !strings.Contains(out, "elements = { 10.0.1.2, 172.18.0.5 }") {
		t.Fatalf("clients set wrong:\n%s", out)
	}
	if strings.Contains(out, "127.0.0.1 }") || strings.Contains(out, "::1") {
		t.Fatalf("loopback leaked into rules:\n%s", out)
	}
	for _, want := range []string{
		"add table ip v2dock\ndelete table ip v2dock\n", // atomic replace
		"type nat hook prerouting priority dstnat - 10",
		// DNS is taken before the private-range skip (poisoned answers).
		"ip saddr != @clients return\n    meta l4proto { tcp, udp } th dport 53 redirect to :12702\n    ip daddr @skip return",
		"fib daddr type local return",
		"meta l4proto tcp redirect to :12701",
		"th dport { 12701, 12702 } ct status & dnat != dnat drop",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(f.Render(nil), "elements = {  }") {
		t.Fatal("empty set must omit elements")
	}
}

func TestInputRulesOnlyMatchRedirected(t *testing.T) {
	for _, r := range New(12701, 12702).inputRules() {
		s := strings.Join(r, " ")
		if !strings.Contains(s, "--ctstate DNAT") || !strings.Contains(s, "--comment v2dock") {
			t.Fatalf("too broad: %s", s)
		}
	}
}
