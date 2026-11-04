// Package netfw installs the host rules that send selected containers'
// traffic into the transparent proxy.
//
// It owns exactly one nftables table ("ip v2dock") and never touches anything
// else, except optional ACCEPT rules in the iptables INPUT chain, limited to
// v2dock's own redirected connections, for hosts whose firewall drops input
// by default (UFW).
//
// Why REDIRECT and not TPROXY: Docker enables br_netfilter, which runs the IP
// prerouting hooks while the packet is still on the bridge; the socket a
// TPROXY rule assigns there is dropped again when the packet reaches the IP
// layer, so the kernel answers with a reset. Destination NAT survives that
// path (Docker's own published ports rely on it), and conntrack keeps the
// original destination for Xray (SO_ORIGINAL_DST).
//
// Rules are rendered from validated netip.Addr values only, so nothing a user
// or a subscription supplies can reach the nft parser as text.
package netfw

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	tableName = "v2dock"
	ruleTag   = "v2dock"
)

// skipNets are destinations that are never proxied: local, private, link-local,
// CGNAT, multicast and reserved ranges.
var skipNets = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15",
	"224.0.0.0/4", "240.0.0.0/4",
}

// Firewall applies and removes the rules. TCPPort receives redirected TCP
// connections, DNSPort receives DNS (UDP and TCP).
type Firewall struct {
	mu        sync.Mutex
	TCPPort   int
	DNSPort   int
	iptBinary string // iptables binary whose INPUT chain got our accept rules
}

func New(tcpPort, dnsPort int) *Firewall {
	return &Firewall{TCPPort: tcpPort, DNSPort: dnsPort}
}

func run(ctx context.Context, stdin []byte, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(out.String())
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return out.String(), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, msg)
	}
	return out.String(), nil
}

// Render returns the nft script for the given client addresses. Exposed for
// tests and diagnostics.
func (f *Firewall) Render(clients []netip.Addr) string {
	var ips []string
	seen := map[string]bool{}
	for _, a := range clients {
		if !a.Is4() || a.IsUnspecified() || a.IsLoopback() {
			continue
		}
		s := a.String()
		if !seen[s] {
			seen[s] = true
			ips = append(ips, s)
		}
	}
	sort.Strings(ips)
	var b strings.Builder
	// "add" then "delete" makes the replacement atomic and idempotent.
	fmt.Fprintf(&b, "add table ip %s\n", tableName)
	fmt.Fprintf(&b, "delete table ip %s\n", tableName)
	fmt.Fprintf(&b, "table ip %s {\n", tableName)
	b.WriteString("  set clients {\n    type ipv4_addr\n")
	if len(ips) > 0 {
		fmt.Fprintf(&b, "    elements = { %s }\n", strings.Join(ips, ", "))
	}
	b.WriteString("  }\n")
	fmt.Fprintf(&b, "  set skip {\n    type ipv4_addr\n    flags interval\n    elements = { %s }\n  }\n", strings.Join(skipNets, ", "))
	// Before Docker's own DNAT (dstnat = -100).
	b.WriteString("  chain prerouting {\n    type nat hook prerouting priority dstnat - 10; policy accept;\n")
	b.WriteString("    ip saddr != @clients return\n")
	// DNS is always taken, whatever resolver the container uses: resolvers on
	// private addresses (common in Iranian data centres) answer filtered
	// names with poisoned 10.10.34.x addresses. Xray resolves foreign names
	// over DoH through the VPN and Iranian names with the host's resolver.
	fmt.Fprintf(&b, "    meta l4proto { tcp, udp } th dport 53 redirect to :%d\n", f.DNSPort)
	b.WriteString("    ip daddr @skip return\n")
	b.WriteString("    fib daddr type local return\n")
	fmt.Fprintf(&b, "    meta l4proto tcp redirect to :%d\n", f.TCPPort)
	b.WriteString("  }\n")
	// Nobody may talk to the proxy ports directly; only redirected flows.
	b.WriteString("  chain input {\n    type filter hook input priority filter - 10; policy accept;\n")
	// "& dnat != dnat" tests the bit; "ct status != dnat" would compare the
	// whole flag set and drop everything.
	fmt.Fprintf(&b, "    iifname != \"lo\" meta l4proto { tcp, udp } th dport { %d, %d } ct status & dnat != dnat drop\n", f.TCPPort, f.DNSPort)
	b.WriteString("  }\n}\n")
	return b.String()
}

// Apply installs (or atomically replaces) the rules for these clients.
func (f *Firewall) Apply(ctx context.Context, clients []netip.Addr) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := run(ctx, []byte(f.Render(clients)), "nft", "-f", "-"); err != nil {
		return err
	}
	f.ensureInputAccept(ctx)
	return nil
}

// Remove deletes everything Apply installed. Missing pieces are not errors.
func (f *Firewall) Remove(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var errs []error
	if _, err := run(ctx, nil, "nft", "list", "table", "ip", tableName); err == nil {
		if _, err := run(ctx, nil, "nft", "delete", "table", "ip", tableName); err != nil {
			errs = append(errs, err)
		}
	}
	f.removeInputAccept(ctx)
	return errors.Join(errs...)
}

// inputRules are the iptables INPUT rules (without -I/-D) that let
// redirected connections through a default-drop host firewall. They only
// match connections v2dock's own rules redirected (ctstate DNAT) to its ports.
func (f *Firewall) inputRules() [][]string {
	ports := fmt.Sprintf("%d,%d", f.TCPPort, f.DNSPort)
	common := []string{"-m", "conntrack", "--ctstate", "DNAT", "-m", "comment", "--comment", ruleTag, "-j", "ACCEPT"}
	return [][]string{
		append([]string{"INPUT", "-p", "tcp", "-m", "multiport", "--dports", ports}, common...),
		append([]string{"INPUT", "-p", "udp", "--dport", fmt.Sprint(f.DNSPort)}, common...),
	}
}

// ensureInputAccept is best-effort and uses whichever iptables backend Docker
// itself uses.
func (f *Firewall) ensureInputAccept(ctx context.Context) {
	if f.iptBinary != "" {
		return
	}
	bin := detectIptables(ctx)
	if bin == "" {
		return
	}
	ok := true
	for _, r := range f.inputRules() {
		if _, err := run(ctx, nil, bin, append([]string{"-w", "5", "-C"}, r...)...); err == nil {
			continue
		}
		if _, err := run(ctx, nil, bin, append([]string{"-w", "5", "-I"}, r...)...); err != nil {
			ok = false
		}
	}
	if ok {
		f.iptBinary = bin
	}
}

func (f *Firewall) removeInputAccept(ctx context.Context) {
	bin := f.iptBinary
	if bin == "" {
		bin = detectIptables(ctx)
	}
	if bin == "" {
		return
	}
	for _, r := range f.inputRules() {
		for i := 0; i < 4; i++ {
			if _, err := run(ctx, nil, bin, append([]string{"-w", "5", "-D"}, r...)...); err != nil {
				break
			}
		}
	}
	f.iptBinary = ""
}

// detectIptables returns the iptables flavour that holds Docker's chains.
func detectIptables(ctx context.Context) string {
	for _, bin := range []string{"iptables-nft", "iptables-legacy", "iptables"} {
		if _, err := exec.LookPath(bin); err != nil {
			continue
		}
		out, err := run(ctx, nil, bin, "-w", "5", "-S")
		if err == nil && strings.Contains(out, "DOCKER") {
			return bin
		}
	}
	return ""
}

// Status describes what is installed right now (for diagnostics).
type Status struct {
	Table     bool   `json:"table"`
	InputRule string `json:"inputRule,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (f *Firewall) Status(ctx context.Context) Status {
	var st Status
	out, err := run(ctx, nil, "nft", "list", "table", "ip", tableName)
	st.Table = err == nil && strings.Contains(out, "redirect")
	if err != nil && !strings.Contains(err.Error(), "No such file") {
		st.Error = err.Error()
	}
	f.mu.Lock()
	st.InputRule = f.iptBinary
	f.mu.Unlock()
	return st
}

// Available checks that the tools and kernel features exist.
func Available(ctx context.Context) error {
	if _, err := exec.LookPath("nft"); err != nil {
		return errors.New("nft is not installed")
	}
	if _, err := run(ctx, nil, "nft", "list", "tables"); err != nil {
		return fmt.Errorf("cannot use nftables (is the agent running with network_mode: host and cap NET_ADMIN?): %w", err)
	}
	return nil
}
