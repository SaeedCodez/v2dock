package links

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var uuidLike = regexp.MustCompile(`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`)

func validUserID(id string) bool {
	// Xray also accepts any 1-30 byte string, mapped to a UUID.
	if uuidLike.MatchString(id) {
		return true
	}
	return len(id) >= 1 && len(id) <= 30 && !strings.ContainsAny(id, " \t\r\n")
}

var vlessFlows = map[string]bool{"": true, "xtls-rprx-vision": true, "xtls-rprx-vision-udp443": true}

func parseVLESS(link string) (Server, error) {
	u, err := url.Parse(link)
	if err != nil {
		return Server{}, errors.New("malformed vless link")
	}
	id := u.User.Username()
	if !validUserID(id) {
		return Server{}, errors.New("vless link without a valid user id")
	}
	host, port, err := splitHostPort(u)
	if err != nil {
		return Server{}, err
	}
	q := u.Query()
	p := paramsFromQuery(q)
	ss, warning, err := buildStream(p, host)
	if err != nil {
		return Server{}, err
	}
	enc := orDefault(q.Get("encryption"), "none")
	flow := q.Get("flow")
	if !vlessFlows[flow] {
		return Server{}, fmt.Errorf("%w flow %q", ErrUnsupported, cleanName(flow, 32))
	}
	settings := map[string]any{"address": host, "port": port, "id": id, "encryption": enc}
	if flow != "" {
		settings["flow"] = flow
	}
	ob := map[string]any{
		"protocol":       "vless",
		"settings":       settings,
		"streamSettings": ss,
	}
	n, s := transportLabel(ss)
	return Server{Name: fragmentName(u), Protocol: "vless", Address: host, Port: port, Transport: n, Security: s, Warning: warning, Outbound: ob}, nil
}

func parseTrojan(link string) (Server, error) {
	u, err := url.Parse(link)
	if err != nil {
		return Server{}, errors.New("malformed trojan link")
	}
	password := u.User.Username()
	if password == "" || len(password) > 256 {
		return Server{}, errors.New("trojan link without a password")
	}
	host, port, err := splitHostPort(u)
	if err != nil {
		return Server{}, err
	}
	p := paramsFromQuery(u.Query())
	if p.security == "" {
		p.security = "tls"
	}
	ss, warning, err := buildStream(p, host)
	if err != nil {
		return Server{}, err
	}
	ob := map[string]any{
		"protocol": "trojan",
		"settings": map[string]any{"servers": []any{map[string]any{
			"address": host, "port": port, "password": password,
		}}},
		"streamSettings": ss,
	}
	n, s := transportLabel(ss)
	return Server{Name: fragmentName(u), Protocol: "trojan", Address: host, Port: port, Transport: n, Security: s, Warning: warning, Outbound: ob}, nil
}

// vmessJSON is the de-facto vmess:// payload (v2rayN format). Port and aid
// arrive as either numbers or strings, so they are decoded loosely.
type vmessJSON struct {
	PS   string          `json:"ps"`
	Add  string          `json:"add"`
	Port json.RawMessage `json:"port"`
	ID   string          `json:"id"`
	Aid  json.RawMessage `json:"aid"`
	Scy  string          `json:"scy"`
	Net  string          `json:"net"`
	Type string          `json:"type"`
	Host string          `json:"host"`
	Path string          `json:"path"`
	TLS  string          `json:"tls"`
	SNI  string          `json:"sni"`
	ALPN string          `json:"alpn"`
	FP   string          `json:"fp"`
	Mode string          `json:"mode"`
}

func looseInt(raw json.RawMessage) int {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	n, _ := strconv.Atoi(s)
	return n
}

var vmessCiphers = map[string]bool{"": true, "auto": true, "aes-128-gcm": true, "chacha20-poly1305": true, "none": true, "zero": true}

func parseVMess(link string) (Server, error) {
	payload := strings.TrimSpace(link[len("vmess://"):])
	if i := strings.IndexByte(payload, '#'); i >= 0 {
		payload = payload[:i]
	}
	decoded, ok := decodeBase64(payload)
	if !ok {
		return Server{}, errors.New("vmess link is not base64 JSON")
	}
	var v vmessJSON
	if err := json.Unmarshal([]byte(decoded), &v); err != nil {
		return Server{}, errors.New("vmess link has invalid JSON")
	}
	if !validUserID(v.ID) {
		return Server{}, errors.New("vmess link without a valid user id")
	}
	port := looseInt(v.Port)
	host := strings.TrimSpace(v.Add)
	if !vmessCiphers[strings.ToLower(v.Scy)] {
		return Server{}, fmt.Errorf("%w vmess cipher %q", ErrUnsupported, cleanName(v.Scy, 24))
	}
	p := streamParams{
		network:    strings.ToLower(v.Net),
		security:   strings.ToLower(v.TLS),
		sni:        v.SNI,
		fp:         strings.ToLower(v.FP),
		alpn:       v.ALPN,
		host:       v.Host,
		path:       v.Path,
		headerType: strings.ToLower(v.Type),
		mode:       v.Mode,
	}
	if p.network == "grpc" {
		p.serviceName = v.Path
		if v.Type == "multi" {
			p.mode = "multi"
		}
	}
	if p.network == "kcp" {
		p.seed = v.Path
	}
	ss, warning, err := buildStream(p, host)
	if err != nil {
		return Server{}, err
	}
	ob := map[string]any{
		"protocol": "vmess",
		"settings": map[string]any{"vnext": []any{map[string]any{
			"address": host, "port": port,
			"users": []any{map[string]any{"id": v.ID, "security": orDefault(strings.ToLower(v.Scy), "auto")}},
		}}},
		"streamSettings": ss,
	}
	n, s := transportLabel(ss)
	return Server{Name: cleanName(v.PS, 120), Protocol: "vmess", Address: host, Port: port, Transport: n, Security: s, Warning: warning, Outbound: ob}, nil
}

var ssMethods = map[string]bool{
	"aes-128-gcm": true, "aes-256-gcm": true, "chacha20-poly1305": true, "chacha20-ietf-poly1305": true,
	"xchacha20-poly1305": true, "xchacha20-ietf-poly1305": true, "none": true, "plain": true,
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true, "2022-blake3-chacha20-poly1305": true,
}

func parseShadowsocks(link string) (Server, error) {
	rest := link[len("ss://"):]
	name := ""
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		if dec, err := url.PathUnescape(rest[i+1:]); err == nil {
			name = dec
		} else {
			name = rest[i+1:]
		}
		rest = rest[:i]
	}
	query := ""
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		query = rest[i+1:]
		rest = rest[:i]
	}
	rest = strings.TrimSuffix(rest, "/")
	q, _ := url.ParseQuery(query)
	if q.Get("plugin") != "" {
		return Server{}, fmt.Errorf("%w shadowsocks plugin", ErrUnsupported)
	}

	var method, password, hostport string
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		// SIP002: userinfo is base64(method:password) or percent-encoded.
		userinfo, hp := rest[:at], rest[at+1:]
		hostport = hp
		if dec, ok := decodeBase64(userinfo); ok && strings.Contains(dec, ":") {
			method, password, _ = strings.Cut(dec, ":")
		} else {
			un, err := url.PathUnescape(userinfo)
			if err != nil {
				return Server{}, errors.New("malformed shadowsocks user info")
			}
			method, password, _ = strings.Cut(un, ":")
		}
	} else {
		// Legacy: base64(method:password@host:port)
		dec, ok := decodeBase64(rest)
		if !ok {
			return Server{}, errors.New("malformed shadowsocks link")
		}
		at := strings.LastIndexByte(dec, '@')
		if at < 0 {
			return Server{}, errors.New("malformed shadowsocks link")
		}
		method, password, _ = strings.Cut(dec[:at], ":")
		hostport = dec[at+1:]
	}
	method = strings.ToLower(method)
	if !ssMethods[method] {
		return Server{}, fmt.Errorf("%w shadowsocks method %q", ErrUnsupported, cleanName(method, 32))
	}
	if password == "" && method != "none" && method != "plain" {
		return Server{}, errors.New("shadowsocks link without a password")
	}
	u, err := url.Parse("ss://" + hostport)
	if err != nil {
		return Server{}, errors.New("malformed shadowsocks address")
	}
	host, port, err := splitHostPort(u)
	if err != nil {
		return Server{}, err
	}
	ob := map[string]any{
		"protocol": "shadowsocks",
		"settings": map[string]any{"servers": []any{map[string]any{
			"address": host, "port": port, "method": method, "password": password,
		}}},
		"streamSettings": map[string]any{"network": "raw", "security": "none"},
	}
	return Server{Name: cleanName(name, 120), Protocol: "shadowsocks", Address: host, Port: port, Transport: "raw", Security: "none", Outbound: ob}, nil
}

func parseHysteria2(link string) (Server, error) {
	u, err := url.Parse(link)
	if err != nil {
		return Server{}, errors.New("malformed hysteria2 link")
	}
	auth := u.User.Username()
	if pw, ok := u.User.Password(); ok {
		auth = auth + ":" + pw
	}
	host, port, err := splitHostPort(u)
	if err != nil {
		return Server{}, err
	}
	q := u.Query()
	if q.Get("obfs") != "" && q.Get("obfs") != "none" {
		return Server{}, fmt.Errorf("%w hysteria2 obfs", ErrUnsupported)
	}
	if q.Get("mport") != "" || strings.Contains(u.Host, ",") {
		return Server{}, fmt.Errorf("%w hysteria2 port hopping", ErrUnsupported)
	}
	p := paramsFromQuery(q)
	tls := map[string]any{"serverName": orDefault(p.sni, host), "alpn": []string{"h3"}}
	if p.pcs != "" {
		tls["pinnedPeerCertSha256"] = p.pcs
	} else if v := q.Get("pinSHA256"); v != "" {
		tls["pinnedPeerCertSha256"] = v
	}
	warning := ""
	if p.insecure && tls["pinnedPeerCertSha256"] == nil {
		warning = "insecure=1 is no longer supported by Xray; the certificate will be verified"
	}
	ob := map[string]any{
		"protocol": "hysteria",
		"settings": map[string]any{"version": 2, "address": host, "port": port},
		"streamSettings": map[string]any{
			"network":          "hysteria",
			"hysteriaSettings": map[string]any{"version": 2, "auth": auth},
			"security":         "tls",
			"tlsSettings":      tls,
		},
	}
	return Server{Name: fragmentName(u), Protocol: "hysteria2", Address: host, Port: port, Transport: "quic", Security: "tls", Warning: warning, Outbound: ob}, nil
}
