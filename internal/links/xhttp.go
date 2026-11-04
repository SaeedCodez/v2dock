package links

import (
	"encoding/json"
	"strings"
)

// xhttpScalar are the xhttp "extra" options a share link may set: tuning
// values only. Anything else, notably downloadSettings (a whole second stream
// config that can name TLS key-log files, raw socket options and other
// endpoints), is dropped, because subscription content is untrusted.
var xhttpScalar = map[string]bool{
	"xPaddingBytes": true, "noGRPCHeader": true, "noSSEHeader": true,
	"scMaxEachPostBytes": true, "scMinPostsIntervalMs": true, "scMaxBufferedPosts": true,
	"scStreamUpServerSecs": true, "uplinkHTTPMethod": true, "mode": true,
}

var xmuxKeys = map[string]bool{
	"maxConcurrency": true, "maxConnections": true, "cMaxReuseTimes": true,
	"hMaxRequestTimes": true, "hMaxReusableSecs": true, "hKeepAlivePeriod": true,
}

func isScalar(v any) bool {
	switch t := v.(type) {
	case string:
		return len(t) <= 64 && !strings.ContainsAny(t, "\r\n")
	case float64, bool:
		return true
	}
	return false
}

// safeXHTTPExtra keeps only whitelisted, scalar xhttp options. It reports
// whether anything was dropped.
func safeXHTTPExtra(raw string) (map[string]any, bool) {
	if len(raw) > 4096 {
		return nil, true
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil, true
	}
	out := map[string]any{}
	dropped := false
	for k, v := range in {
		switch {
		case xhttpScalar[k] && isScalar(v):
			out[k] = v
		case k == "xmux":
			m, ok := v.(map[string]any)
			if !ok {
				dropped = true
				continue
			}
			xm := map[string]any{}
			for mk, mv := range m {
				if xmuxKeys[mk] && isScalar(mv) {
					xm[mk] = mv
				} else {
					dropped = true
				}
			}
			out[k] = xm
		case k == "headers":
			m, ok := v.(map[string]any)
			if !ok || len(m) > 16 {
				dropped = true
				continue
			}
			h := map[string]any{}
			for hk, hv := range m {
				sv, ok := hv.(string)
				if ok && len(hk) <= 64 && len(sv) <= 256 && !strings.ContainsAny(hk+sv, "\r\n") {
					h[hk] = sv
				} else {
					dropped = true
				}
			}
			out[k] = h
		default:
			dropped = true
		}
	}
	return out, dropped
}
