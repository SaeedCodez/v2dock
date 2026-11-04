// Package links turns V2Ray share links and subscription bodies into Xray
// outbound configurations.
//
// Everything here is pure: no network, no globals. Subscription content is
// untrusted input from a remote server, so every field that ends up in an Xray
// config or in the UI is validated or bounded here.
package links

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxSubscriptionBytes bounds a subscription body. Real subscriptions are a
// few hundred KB at most.
const MaxSubscriptionBytes = 4 << 20

// MaxServersPerSubscription bounds how many servers one subscription can add.
const MaxServersPerSubscription = 2000

// Server is one parsed share link.
type Server struct {
	// ID is stable for the same server (same endpoint, credentials and
	// transport) so selections survive a subscription refresh.
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Protocol  string         `json:"protocol"`
	Address   string         `json:"address"`
	Port      int            `json:"port"`
	Transport string         `json:"transport"`
	Security  string         `json:"security"`
	Warning   string         `json:"warning,omitempty"`
	Link      string         `json:"-"` // contains credentials; never serialised to clients
	Outbound  map[string]any `json:"-"`
}

// Result is the outcome of parsing a subscription body.
type Result struct {
	Servers []Server
	// Skipped lists lines that were not usable, with the reason. The line
	// itself is not included because it may contain credentials.
	Skipped []string
}

// ErrUnsupported marks links that parse but use something Xray cannot run.
var ErrUnsupported = errors.New("unsupported")

// ParseSubscription decodes a subscription body: a plain or base64-encoded
// list of share links, one per line.
func ParseSubscription(body []byte) (Result, error) {
	var res Result
	if len(body) > MaxSubscriptionBytes {
		return res, fmt.Errorf("subscription is larger than %d MB", MaxSubscriptionBytes>>20)
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	if text == "" {
		return res, errors.New("subscription is empty")
	}
	if !strings.Contains(text, "://") {
		decoded, ok := decodeBase64(text)
		if !ok {
			return res, errors.New("subscription is neither a list of links nor base64")
		}
		text = strings.TrimSpace(decoded)
	}
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return res, errors.New("JSON subscriptions are not supported; use the V2Ray (base64 links) format")
	}
	if strings.Contains(text, "proxies:") && !strings.Contains(text, "://") {
		return res, errors.New("Clash YAML subscriptions are not supported; use the V2Ray (base64 links) format")
	}
	seen := map[string]bool{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if len(res.Servers) >= MaxServersPerSubscription {
			res.Skipped = append(res.Skipped, fmt.Sprintf("line %d+: limit of %d servers reached", i+1, MaxServersPerSubscription))
			break
		}
		s, err := ParseLink(line)
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("line %d: %v", i+1, err))
			continue
		}
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		res.Servers = append(res.Servers, s)
	}
	if len(res.Servers) == 0 {
		if len(res.Skipped) > 0 {
			return res, fmt.Errorf("no usable servers (%s)", res.Skipped[0])
		}
		return res, errors.New("no servers found")
	}
	return res, nil
}

// ParseLink parses a single share link.
func ParseLink(link string) (Server, error) {
	link = strings.TrimSpace(link)
	if len(link) > 8192 {
		return Server{}, errors.New("link is too long")
	}
	scheme, _, ok := strings.Cut(link, "://")
	if !ok {
		return Server{}, errors.New("not a share link")
	}
	var (
		s   Server
		err error
	)
	switch strings.ToLower(scheme) {
	case "vless":
		s, err = parseVLESS(link)
	case "vmess":
		s, err = parseVMess(link)
	case "trojan":
		s, err = parseTrojan(link)
	case "ss":
		s, err = parseShadowsocks(link)
	case "hysteria2", "hy2":
		s, err = parseHysteria2(link)
	default:
		return Server{}, fmt.Errorf("%w protocol %q", ErrUnsupported, cleanName(scheme, 16))
	}
	if err != nil {
		return Server{}, err
	}
	if err := validateEndpoint(s.Address, s.Port); err != nil {
		return Server{}, err
	}
	s.Link = link
	if s.Name == "" {
		s.Name = net.JoinHostPort(s.Address, strconv.Itoa(s.Port))
	}
	s.ID = fingerprint(s.Outbound)
	return s, nil
}

// fingerprint hashes the outbound (which excludes the display name) so the
// same server keeps its ID when only its name changes.
func fingerprint(ob map[string]any) string {
	b, _ := json.Marshal(ob) // map keys are sorted, so this is canonical
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:6])
}

func validateEndpoint(host string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d", port)
	}
	if host == "" || len(host) > 253 {
		return errors.New("invalid server address")
	}
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	for _, r := range host {
		if !(r == '.' || r == '-' || r == '_' || r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r))) {
			return fmt.Errorf("invalid server address %q", cleanName(host, 64))
		}
	}
	return nil
}

// CleanName makes a display name safe: valid UTF-8, no control or bidi
// characters, bounded length.
func CleanName(s string, max int) string { return cleanName(s, max) }

func cleanName(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = strings.Map(func(r rune) rune {
		// Control characters (terminal escapes) and bidi controls (text that
		// renders reversed or reordered) never belong in a name.
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max])
	}
	return s
}

func decodeBase64(s string) (string, bool) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && utf8.Valid(b) {
			return string(b), true
		}
	}
	// Some panels pad incorrectly; retry without padding.
	trimmed := strings.TrimRight(s, "=")
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(trimmed); err == nil && utf8.Valid(b) {
			return string(b), true
		}
	}
	return "", false
}

func fragmentName(u *url.URL) string {
	name := u.Fragment
	if dec, err := url.PathUnescape(u.RawFragment); err == nil && u.RawFragment != "" {
		name = dec
	}
	return cleanName(name, 120)
}

func splitHostPort(u *url.URL) (string, int, error) {
	host := u.Hostname()
	portStr := u.Port()
	if portStr == "" {
		return "", 0, errors.New("missing port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, errors.New("invalid port")
	}
	return host, port, nil
}

// streamParams are the transport and security options shared by VLESS,
// VMess and Trojan links.
type streamParams struct {
	network     string // tcp, ws, grpc, xhttp, httpupgrade, kcp
	security    string // none, tls, reality
	sni         string
	fp          string
	alpn        string
	host        string
	path        string
	headerType  string
	serviceName string
	mode        string
	authority   string
	seed        string
	extra       string
	pbk         string
	sid         string
	spx         string
	pqv         string
	pcs         string
	vcn         string
	insecure    bool
}

func paramsFromQuery(q url.Values) streamParams {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(q.Get(k)); v != "" {
				return v
			}
		}
		return ""
	}
	p := streamParams{
		network:     strings.ToLower(get("type", "net")),
		security:    strings.ToLower(get("security")),
		sni:         get("sni", "peer", "serverName"),
		fp:          strings.ToLower(get("fp", "fingerprint")),
		alpn:        get("alpn"),
		host:        get("host"),
		path:        get("path"),
		headerType:  strings.ToLower(get("headerType")),
		serviceName: get("serviceName"),
		mode:        get("mode"),
		authority:   get("authority"),
		seed:        get("seed"),
		extra:       get("extra"),
		pbk:         get("pbk", "publicKey"),
		sid:         get("sid", "shortId"),
		spx:         get("spx", "spiderX"),
		pqv:         get("pqv"),
		pcs:         get("pcs"),
		vcn:         get("vcn"),
	}
	switch strings.ToLower(get("allowInsecure", "insecure", "allow_insecure")) {
	case "1", "true":
		p.insecure = true
	}
	return p
}

// buildStream produces Xray streamSettings. It returns a warning for options
// that had to be dropped.
func buildStream(p streamParams, address string) (map[string]any, string, error) {
	network := p.network
	switch network {
	case "", "tcp", "raw":
		network = "raw"
	case "ws", "websocket":
		network = "ws"
	case "grpc", "gun":
		network = "grpc"
	case "xhttp", "splithttp":
		network = "xhttp"
	case "httpupgrade":
	case "kcp", "mkcp":
		network = "kcp"
	case "h2", "http", "h3", "quic":
		return nil, "", fmt.Errorf("%w transport %q (removed from Xray)", ErrUnsupported, network)
	default:
		return nil, "", fmt.Errorf("%w transport %q", ErrUnsupported, cleanName(network, 16))
	}
	ss := map[string]any{"network": network}
	path := p.path
	warning := ""
	switch network {
	case "raw":
		if p.headerType == "http" {
			req := map[string]any{"path": splitList(orDefault(path, "/"))}
			if p.host != "" {
				req["headers"] = map[string]any{"Host": splitList(p.host)}
			}
			ss["rawSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	case "ws":
		ws := map[string]any{"path": orDefault(path, "/")}
		if p.host != "" {
			ws["host"] = p.host
		}
		ss["wsSettings"] = ws
	case "httpupgrade":
		hu := map[string]any{"path": orDefault(path, "/")}
		if p.host != "" {
			hu["host"] = p.host
		}
		ss["httpupgradeSettings"] = hu
	case "grpc":
		g := map[string]any{"serviceName": orDefault(p.serviceName, strings.TrimPrefix(path, "/"))}
		if p.mode == "multi" {
			g["multiMode"] = true
		}
		if p.authority != "" {
			g["authority"] = p.authority
		}
		ss["grpcSettings"] = g
	case "xhttp":
		x := map[string]any{"path": orDefault(path, "/")}
		if p.host != "" {
			x["host"] = p.host
		}
		if p.mode != "" {
			x["mode"] = p.mode
		}
		if p.extra != "" {
			extra, dropped := safeXHTTPExtra(p.extra)
			if len(extra) > 0 {
				x["extra"] = extra
			}
			if dropped {
				warning = "some xhttp extra options were ignored"
			}
		}
		ss["xhttpSettings"] = x
	case "kcp":
		if (p.headerType != "" && p.headerType != "none") || p.seed != "" {
			return nil, "", fmt.Errorf("%w mKCP header/seed (moved to finalmask in Xray)", ErrUnsupported)
		}
		ss["kcpSettings"] = map[string]any{}
	}

	switch p.security {
	case "", "none":
		ss["security"] = "none"
	case "tls", "xtls":
		ss["security"] = "tls"
		t := map[string]any{"serverName": orDefault(p.sni, orDefault(p.host, address))}
		if p.fp != "" && p.fp != "none" {
			t["fingerprint"] = p.fp
		}
		if p.alpn != "" {
			t["alpn"] = splitList(p.alpn)
		}
		if p.pcs != "" {
			t["pinnedPeerCertSha256"] = p.pcs
		}
		if p.vcn != "" {
			t["verifyPeerCertByName"] = p.vcn
		}
		if p.insecure && p.pcs == "" {
			// Xray removed allowInsecure; the certificate is verified normally.
			warning = "allowInsecure is no longer supported by Xray; the certificate will be verified"
		}
		ss["tlsSettings"] = t
	case "reality":
		if p.pbk == "" {
			return nil, "", errors.New("REALITY link without a public key (pbk)")
		}
		r := map[string]any{
			"serverName":  orDefault(p.sni, address),
			"fingerprint": orDefault(p.fp, "chrome"),
			"password":    p.pbk,
		}
		if p.sid != "" {
			r["shortId"] = p.sid
		}
		if p.spx != "" {
			r["spiderX"] = p.spx
		}
		if p.pqv != "" {
			r["mldsa65Verify"] = p.pqv
		}
		ss["security"] = "reality"
		ss["realitySettings"] = r
	default:
		return nil, "", fmt.Errorf("%w security %q", ErrUnsupported, cleanName(p.security, 16))
	}
	return ss, warning, nil
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func transportLabel(ss map[string]any) (string, string) {
	n, _ := ss["network"].(string)
	s, _ := ss["security"].(string)
	return n, s
}
