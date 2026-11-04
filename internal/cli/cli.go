package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/SaeedCodez/v2dock/internal/wire"
)

type globals struct {
	json   bool
	socket string
	url    string
	token  string
}

// args is a tiny flag parser that allows flags anywhere on the line.
type args struct {
	pos   []string
	flags map[string]string
	bools map[string]bool
}

// valueFlags take a value; everything else starting with -- is a boolean.
var valueFlags = map[string]bool{
	"socket": true, "url": true, "token": true, "name": true, "hours": true, "sub": true, "ids": true,
	"sort": true, "limit": true, "quota": true, "period": true, "action": true, "range": true,
	"container": true, "kind": true, "scope": true,
}

func parseArgs(in []string) (*args, error) {
	a := &args{flags: map[string]string{}, bools: map[string]bool{}}
	for i := 0; i < len(in); i++ {
		s := in[i]
		if s == "--" {
			a.pos = append(a.pos, in[i+1:]...)
			break
		}
		if !strings.HasPrefix(s, "-") || s == "-" {
			a.pos = append(a.pos, s)
			continue
		}
		name := strings.TrimLeft(s, "-")
		val, hasVal := "", false
		if k, v, ok := strings.Cut(name, "="); ok {
			name, val, hasVal = k, v, true
		}
		if valueFlags[name] {
			if !hasVal {
				if i+1 >= len(in) {
					return nil, fmt.Errorf("--%s needs a value", name)
				}
				i++
				val = in[i]
			}
			a.flags[name] = val
			continue
		}
		if hasVal {
			b, err := strconv.ParseBool(val)
			if err != nil {
				return nil, fmt.Errorf("--%s expects true or false", name)
			}
			a.bools[name] = b
		} else {
			a.bools[name] = true
		}
	}
	return a, nil
}

func (a *args) arg(i int) string {
	if i < len(a.pos) {
		return a.pos[i]
	}
	return ""
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, v ...any) error { return usageError{fmt.Sprintf(format, v...)} }

// Run executes a CLI command and returns the process exit code.
func Run(argv []string, stdout, stderr io.Writer) int {
	a, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return ExitUsage
	}
	g := &globals{
		json:   a.bools["json"],
		socket: firstNonEmpty(a.flags["socket"], os.Getenv("V2DOCK_SOCKET"), wire.DefaultAgentSocket),
		url:    firstNonEmpty(a.flags["url"], os.Getenv("V2DOCK_URL")),
		token:  firstNonEmpty(a.flags["token"], os.Getenv("V2DOCK_TOKEN")),
	}
	if len(a.pos) == 0 || a.bools["help"] || a.bools["h"] || a.arg(0) == "help" {
		fmt.Fprint(stdout, helpText)
		return ExitOK
	}
	c, err := newClient(g)
	if err != nil {
		return fail(g, stdout, stderr, usageError{err.Error()})
	}
	out := stdout
	if !g.json {
		out = safeWriter{stdout}
	}
	r := &runner{g: g, c: c, a: a, out: out}
	cmd := a.arg(0)
	sub := a.arg(1)
	switch cmd {
	case "status":
		err = r.status()
	case "sub", "subs", "subscription", "subscriptions":
		err = r.subscription(sub)
	case "server", "servers":
		err = r.server(sub)
	case "connect":
		err = r.connect()
	case "disconnect":
		err = r.simple("POST", "/api/disconnect", nil, "Disconnected. Routed containers use their normal network.")
	case "container", "containers":
		err = r.containers(sub)
	case "route", "routes":
		err = r.route(sub)
	case "usage":
		err = r.usage()
	case "settings", "setting":
		err = r.settings(sub)
	case "events", "activity":
		err = r.events()
	case "token", "tokens":
		err = r.tokens(sub)
	case "password":
		err = r.password(sub)
	case "diag", "diagnostics":
		err = r.raw("GET", "/api/diagnostics")
	default:
		err = usagef("unknown command %q (see `v2dock help`)", cmd)
	}
	if err != nil {
		return fail(g, stdout, stderr, err)
	}
	return ExitOK
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func fail(g *globals, stdout, stderr io.Writer, err error) int {
	code, ecode := ExitError, "error"
	var ae *apiErr
	var ue usageError
	switch {
	case errors.As(err, &ae):
		code, ecode = ae.exitCode(), ae.code
	case errors.As(err, &ue):
		code, ecode = ExitUsage, "usage"
	}
	if g.json {
		b, _ := json.Marshal(map[string]any{"ok": false, "code": ecode, "error": err.Error(), "exit": code})
		fmt.Fprintln(stdout, string(b))
	} else {
		fmt.Fprintln(safeWriter{stderr}, "error:", err)
	}
	return code
}

type runner struct {
	g   *globals
	c   *client
	a   *args
	out io.Writer
}

// emit prints the raw JSON in --json mode, otherwise calls human().
func (r *runner) emit(raw json.RawMessage, human func()) {
	if r.g.json {
		var v map[string]any
		if json.Unmarshal(raw, &v) == nil {
			if _, ok := v["ok"]; !ok {
				v["ok"] = true
			}
			b, _ := json.Marshal(v)
			fmt.Fprintln(r.out, string(b))
			return
		}
		fmt.Fprintln(r.out, string(raw))
		return
	}
	human()
}

func (r *runner) raw(method, path string) error {
	raw, err := r.c.do(method, path, nil, nil)
	if err != nil {
		return err
	}
	if r.g.json {
		r.emit(raw, nil)
		return nil
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(r.out, string(b))
	return nil
}

func (r *runner) simple(method, path string, body any, msg string) error {
	raw, err := r.c.do(method, path, body, nil)
	if err != nil {
		return err
	}
	r.emit(raw, func() { fmt.Fprintln(r.out, msg) })
	return nil
}

func (r *runner) table() *tabwriter.Writer {
	return tabwriter.NewWriter(r.out, 0, 2, 2, ' ', 0)
}

// ------------------------------------------------------------------ status

type statusResp struct {
	Version   string `json:"version"`
	Connected bool   `json:"connected"`
	Running   bool   `json:"running"`
	Error     string `json:"error"`
	FwError   string `json:"firewallError"`
	Server    *struct {
		ID, Name, Protocol, Address string
	} `json:"server"`
	ConnectedAt int64 `json:"connectedAt"`
	Health      struct {
		OK       bool   `json:"ok"`
		DelayMs  int    `json:"delayMs"`
		Error    string `json:"error"`
		At       int64  `json:"at"`
		Failures int    `json:"failures"`
	} `json:"health"`
	BypassIran    bool             `json:"bypassIran"`
	AutoFailover  bool             `json:"autoFailover"`
	Routes        int              `json:"routes"`
	RoutedRunning int              `json:"routedRunning"`
	QuotaExceeded int              `json:"quotaExceeded"`
	Today         map[string]int64 `json:"today"`
	Docker        struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		Containers int    `json:"containers"`
	} `json:"docker"`
}

func (r *runner) status() error {
	var s statusResp
	raw, err := r.c.do("GET", "/api/status", nil, &s)
	if err != nil {
		return err
	}
	r.emit(raw, func() {
		w := r.table()
		state := "disconnected"
		if s.Connected && s.Running {
			state = "connected"
		} else if s.Connected {
			state = "connecting / error"
		}
		fmt.Fprintf(w, "State:\t%s\n", state)
		if s.Server != nil {
			fmt.Fprintf(w, "Server:\t%s  (%s, %s, id %s)\n", s.Server.Name, s.Server.Protocol, s.Server.Address, s.Server.ID)
		}
		if s.Connected {
			h := "not checked yet"
			if s.Health.At > 0 {
				if s.Health.OK {
					h = fmt.Sprintf("ok, %d ms", s.Health.DelayMs)
				} else {
					h = fmt.Sprintf("failing (%d in a row): %s", s.Health.Failures, s.Health.Error)
				}
			}
			fmt.Fprintf(w, "Health:\t%s\n", h)
		}
		fmt.Fprintf(w, "Iran bypass:\t%s\n", onOff(s.BypassIran))
		fmt.Fprintf(w, "Auto failover:\t%s\n", onOff(s.AutoFailover))
		fmt.Fprintf(w, "Routed containers:\t%d (%d running, %d over quota)\n", s.Routes, s.RoutedRunning, s.QuotaExceeded)
		fmt.Fprintf(w, "Today:\t%s via VPN, %s direct\n", human(s.Today["vpn"]), human(s.Today["direct"]))
		if !s.Docker.OK {
			fmt.Fprintf(w, "Docker:\tunavailable: %s\n", s.Docker.Error)
		}
		if s.Error != "" {
			fmt.Fprintf(w, "Error:\t%s\n", s.Error)
		}
		if s.FwError != "" {
			fmt.Fprintf(w, "Firewall:\t%s\n", s.FwError)
		}
		fmt.Fprintf(w, "Version:\t%s\n", s.Version)
		w.Flush()
	})
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// ------------------------------------------------------------ subscriptions

type subResp struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	Kind            string `json:"kind"`
	AutoUpdateHours int    `json:"autoUpdateHours"`
	LastSuccessAt   int64  `json:"lastSuccessAt"`
	LastError       string `json:"lastError"`
	ServerCount     int    `json:"serverCount"`
	UserInfo        *struct {
		Upload, Download, Total, Expire int64
	} `json:"userInfo"`
}

func (r *runner) subscription(sub string) error {
	switch sub {
	case "", "list", "ls":
		var resp struct {
			Subscriptions []subResp `json:"subscriptions"`
		}
		raw, err := r.c.do("GET", "/api/subscriptions", nil, &resp)
		if err != nil {
			return err
		}
		r.emit(raw, func() {
			if len(resp.Subscriptions) == 0 {
				fmt.Fprintln(r.out, "No subscriptions. Add one: v2dock sub add <url>")
				return
			}
			w := r.table()
			fmt.Fprintln(w, "ID\tNAME\tSERVERS\tUPDATED\tUSAGE\tURL")
			for _, s := range resp.Subscriptions {
				usage := "-"
				if s.UserInfo != nil && s.UserInfo.Total > 0 {
					usage = fmt.Sprintf("%s / %s", human(s.UserInfo.Upload+s.UserInfo.Download), human(s.UserInfo.Total))
				}
				updated := ago(s.LastSuccessAt)
				if s.LastError != "" {
					updated += " (last try failed)"
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", s.ID, s.Name, s.ServerCount, updated, usage, s.URL)
			}
			w.Flush()
		})
		return nil
	case "add":
		u := r.a.arg(2)
		if u == "" {
			return usagef("usage: v2dock sub add <url> [--name NAME] [--hours 12]")
		}
		body := map[string]any{"url": u, "name": r.a.flags["name"]}
		if h := r.a.flags["hours"]; h != "" {
			n, err := strconv.Atoi(h)
			if err != nil {
				return usagef("--hours must be a number")
			}
			body["autoUpdateHours"] = n
		}
		var resp struct {
			Servers int    `json:"servers"`
			Warning string `json:"warning"`
			Sub     struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"subscription"`
		}
		raw, err := r.c.do("POST", "/api/subscriptions", body, &resp)
		if err != nil {
			return err
		}
		r.emit(raw, func() {
			fmt.Fprintf(r.out, "Added %s (id %s) with %d servers.\n", resp.Sub.Name, resp.Sub.ID, resp.Servers)
			if resp.Warning != "" {
				fmt.Fprintln(r.out, "warning:", resp.Warning)
			}
		})
		return nil
	case "remove", "rm", "delete":
		id := r.a.arg(2)
		if id == "" {
			return usagef("usage: v2dock sub remove <id>")
		}
		return r.simple("DELETE", "/api/subscriptions/"+url.PathEscape(id), nil, "Removed.")
	case "refresh", "update":
		id := r.a.arg(2)
		if id == "" && !r.a.bools["all"] {
			return usagef("usage: v2dock sub refresh <id> | --all")
		}
		ids := []string{id}
		if r.a.bools["all"] {
			var resp struct {
				Subscriptions []subResp `json:"subscriptions"`
			}
			if _, err := r.c.do("GET", "/api/subscriptions", nil, &resp); err != nil {
				return err
			}
			ids = nil
			for _, s := range resp.Subscriptions {
				if s.Kind == "url" {
					ids = append(ids, s.ID)
				}
			}
		}
		results := []map[string]any{}
		var firstErr error
		for _, id := range ids {
			var resp struct {
				Servers int `json:"servers"`
			}
			_, err := r.c.do("POST", "/api/subscriptions/"+url.PathEscape(id)+"/refresh", map[string]any{}, &resp)
			res := map[string]any{"id": id, "ok": err == nil, "servers": resp.Servers}
			if err != nil {
				res["error"] = err.Error()
				if firstErr == nil {
					firstErr = err
				}
			}
			results = append(results, res)
			if !r.g.json {
				if err != nil {
					fmt.Fprintf(r.out, "%s: failed: %v\n", id, err)
				} else {
					fmt.Fprintf(r.out, "%s: %d servers\n", id, resp.Servers)
				}
			}
		}
		if r.g.json {
			b, _ := json.Marshal(map[string]any{"ok": firstErr == nil, "results": results})
			fmt.Fprintln(r.out, string(b))
		}
		if firstErr != nil && len(ids) == 1 {
			return firstErr
		}
		return nil
	case "rename":
		id, name := r.a.arg(2), strings.Join(r.a.pos[min(3, len(r.a.pos)):], " ")
		if id == "" || name == "" {
			return usagef("usage: v2dock sub rename <id> <new name>")
		}
		return r.simple("PUT", "/api/subscriptions/"+url.PathEscape(id), map[string]any{"name": name}, "Renamed.")
	}
	return usagef("usage: v2dock sub list|add|remove|refresh|rename")
}

// ------------------------------------------------------------------ servers

type serverResp struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscriptionId"`
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	Address        string `json:"address"`
	Port           int    `json:"port"`
	Transport      string `json:"transport"`
	Security       string `json:"security"`
	TCPms          *int   `json:"tcpMs"`
	TCPError       string `json:"tcpError"`
	DelayMs        *int   `json:"delayMs"`
	DelayError     string `json:"delayError"`
}

func msText(v *int, errMsg string) string {
	if v != nil {
		return fmt.Sprintf("%d ms", *v)
	}
	if errMsg != "" {
		if len(errMsg) > 24 {
			errMsg = errMsg[:24]
		}
		return "✗ " + errMsg
	}
	return "-"
}

func (r *runner) server(sub string) error {
	switch sub {
	case "", "list", "ls":
		q := ""
		if s := r.a.flags["sub"]; s != "" {
			q = "?subscription=" + url.QueryEscape(s)
		}
		var resp struct {
			Servers  []serverResp `json:"servers"`
			ActiveID string       `json:"activeId"`
		}
		raw, err := r.c.do("GET", "/api/servers"+q, nil, &resp)
		if err != nil {
			return err
		}
		sortServers(resp.Servers, r.a.flags["sort"])
		if l, _ := strconv.Atoi(r.a.flags["limit"]); l > 0 && l < len(resp.Servers) {
			resp.Servers = resp.Servers[:l]
		}
		if r.g.json {
			b, _ := json.Marshal(map[string]any{"ok": true, "servers": resp.Servers, "activeId": resp.ActiveID})
			fmt.Fprintln(r.out, string(b))
			return nil
		}
		_ = raw
		if len(resp.Servers) == 0 {
			fmt.Fprintln(r.out, "No servers. Add a subscription: v2dock sub add <url>")
			return nil
		}
		w := r.table()
		fmt.Fprintln(w, "\tID\tNAME\tTYPE\tADDRESS\tTCP\tREAL DELAY")
		for _, s := range resp.Servers {
			mark := ""
			if s.ID == resp.ActiveID {
				mark = "●"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s/%s/%s\t%s:%d\t%s\t%s\n", mark, s.ID, s.Name, s.Protocol, s.Transport, s.Security, s.Address, s.Port,
				msText(s.TCPms, s.TCPError), msText(s.DelayMs, s.DelayError))
		}
		w.Flush()
		return nil
	case "add":
		link := r.a.arg(2)
		if link == "" {
			return usagef("usage: v2dock server add '<vless://...>' [--name NAME]")
		}
		return r.simple("POST", "/api/servers", map[string]any{"link": link, "name": r.a.flags["name"]}, "Added to the Manual list.")
	case "remove", "rm":
		id := r.a.arg(2)
		if id == "" {
			return usagef("usage: v2dock server remove <id>")
		}
		return r.simple("DELETE", "/api/servers/"+url.PathEscape(id), nil, "Removed.")
	case "test":
		return r.serverTest()
	}
	return usagef("usage: v2dock server list|test|add|remove")
}

func sortServers(s []serverResp, by string) {
	key := func(v *int) int {
		if v == nil {
			return 1 << 30
		}
		return *v
	}
	switch by {
	case "delay", "real":
		sort.SliceStable(s, func(i, j int) bool { return key(s[i].DelayMs) < key(s[j].DelayMs) })
	case "tcp":
		sort.SliceStable(s, func(i, j int) bool { return key(s[i].TCPms) < key(s[j].TCPms) })
	case "name":
		sort.SliceStable(s, func(i, j int) bool { return strings.ToLower(s[i].Name) < strings.ToLower(s[j].Name) })
	}
}

type jobResp struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Total    int    `json:"total"`
	Done     int    `json:"done"`
	OK       int    `json:"ok"`
	Finished int64  `json:"finished"`
}

func (r *runner) serverTest() error {
	kind := "real"
	if r.a.bools["tcp"] {
		kind = "tcp"
	}
	body := map[string]any{"kind": kind, "subscription": r.a.flags["sub"]}
	if ids := r.a.flags["ids"]; ids != "" {
		body["ids"] = strings.Split(ids, ",")
	}
	var start struct {
		Job jobResp `json:"job"`
	}
	raw, err := r.c.do("POST", "/api/servers/test", body, &start)
	if err != nil {
		return err
	}
	if !r.a.bools["wait"] {
		r.emit(raw, func() {
			fmt.Fprintf(r.out, "Testing %d servers (%s). Follow with: v2dock server test --wait, or list with: v2dock server list --sort %s\n", start.Job.Total, kind, map[string]string{"tcp": "tcp", "real": "delay"}[kind])
		})
		return nil
	}
	for {
		time.Sleep(time.Second)
		var st struct {
			Job  *jobResp `json:"job"`
			Last *jobResp `json:"last"`
		}
		if _, err := r.c.do("GET", "/api/servers/test", nil, &st); err != nil {
			return err
		}
		if st.Job == nil {
			if !r.g.json && st.Last != nil {
				fmt.Fprintf(r.out, "\rDone: %d of %d servers answered.            \n", st.Last.OK, st.Last.Total)
			}
			break
		}
		if !r.g.json {
			fmt.Fprintf(r.out, "\r%d / %d tested, %d ok", st.Job.Done, st.Job.Total, st.Job.OK)
		}
	}
	if r.g.json {
		// Hand back the fresh results sorted fastest first.
		r.a.flags["sort"] = map[string]string{"tcp": "tcp", "real": "delay"}[kind]
		return r.server("list")
	}
	return nil
}

func (r *runner) connect() error {
	body := map[string]any{}
	if r.a.bools["best"] {
		body["best"] = true
		body["subscription"] = r.a.flags["sub"]
	} else if id := r.a.arg(1); id != "" {
		body["serverId"] = id
	} else {
		return usagef("usage: v2dock connect <server-id> | --best [--sub ID]")
	}
	var resp struct {
		Server struct {
			ID, Name string
		} `json:"server"`
	}
	raw, err := r.c.do("POST", "/api/connect", body, &resp)
	if err != nil {
		return err
	}
	r.emit(raw, func() { fmt.Fprintf(r.out, "Connected to %s (%s).\n", resp.Server.Name, resp.Server.ID) })
	return nil
}

// --------------------------------------------------------------- containers

type containerResp struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	State       string   `json:"state"`
	NetworkMode string   `json:"networkMode"`
	IPs         []string `json:"ips"`
	Routable    bool     `json:"routable"`
	Reason      string   `json:"reason"`
	Routed      bool     `json:"routed"`
	Route       *struct {
		QuotaBytes  int64  `json:"quotaBytes"`
		QuotaPeriod string `json:"quotaPeriod"`
		QuotaAction string `json:"quotaAction"`
		QuotaUsed   int64  `json:"quotaUsed"`
		Exceeded    bool   `json:"exceeded"`
	} `json:"route"`
	Today struct {
		VPN    int64 `json:"vpn"`
		Direct int64 `json:"direct"`
	} `json:"today"`
}

func (r *runner) listContainers() ([]containerResp, json.RawMessage, error) {
	var resp struct {
		Containers []containerResp `json:"containers"`
	}
	raw, err := r.c.do("GET", "/api/containers", nil, &resp)
	return resp.Containers, raw, err
}

func (r *runner) containers(sub string) error {
	if sub != "" && sub != "list" && sub != "ls" {
		return usagef("usage: v2dock container list [--all]")
	}
	cs, raw, err := r.listContainers()
	if err != nil {
		return err
	}
	r.emit(raw, func() {
		w := r.table()
		fmt.Fprintln(w, "VPN\tNAME\tSTATE\tIPS\tTODAY VPN\tTODAY DIRECT\tQUOTA\tKEY")
		for _, c := range cs {
			if !r.a.bools["all"] && c.State != "running" && !c.Routed {
				continue
			}
			vpn := "-"
			if c.Routed {
				vpn = "on"
				if c.Route != nil && c.Route.Exceeded {
					vpn = "over quota"
				}
			} else if !c.Routable {
				vpn = "n/a"
			}
			quota := "-"
			if c.Route != nil && c.Route.QuotaBytes > 0 {
				quota = fmt.Sprintf("%s / %s %s", human(c.Route.QuotaUsed), human(c.Route.QuotaBytes), c.Route.QuotaPeriod)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", vpn, c.Label, c.State, strings.Join(c.IPs, ","), human(c.Today.VPN), human(c.Today.Direct), quota, c.Key)
		}
		w.Flush()
	})
	return nil
}

// resolveContainer accepts a key, name, id prefix or label.
func (r *runner) resolveContainer(q string) (containerResp, error) {
	cs, _, err := r.listContainers()
	if err != nil {
		return containerResp{}, err
	}
	lq := strings.ToLower(q)
	// Strongest match first: exact key / name / id prefix / label, then the
	// compose service name ("web" for compose:proj/web), then a name prefix.
	tiers := []func(c containerResp) bool{
		func(c containerResp) bool {
			return c.Key == q || c.Name == q || (len(q) >= 4 && strings.HasPrefix(c.ID, q)) || strings.ToLower(c.Label) == lq
		},
		func(c containerResp) bool { return strings.HasSuffix(c.Key, "/"+q) },
		func(c containerResp) bool { return strings.HasPrefix(strings.ToLower(c.Name), lq) },
	}
	var matches []containerResp
	for _, match := range tiers {
		for _, c := range cs {
			if match(c) {
				matches = append(matches, c)
			}
		}
		if len(matches) > 0 {
			break
		}
	}
	if len(matches) == 0 {
		return containerResp{}, &apiErr{status: 404, code: "not_found", msg: fmt.Sprintf("no container matches %q (see `v2dock container list --all`)", q)}
	}
	key := matches[0].Key
	for _, m := range matches[1:] {
		if m.Key != key {
			return containerResp{}, usagef("%q matches several containers; use the key from `v2dock container list`", q)
		}
	}
	return matches[0], nil
}

// parseSize accepts 500MB, 10GB, 1.5TB, or a byte count; 0 / unlimited = no quota.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" || s == "0" || s == "UNLIMITED" || s == "NONE" {
		return 0, nil
	}
	mult := float64(1)
	for _, u := range []struct {
		suf string
		m   float64
	}{{"TIB", 1 << 40}, {"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10}, {"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suf) {
			mult = u.m
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suf))
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid size %q (use e.g. 500MB, 10GB)", s)
	}
	return int64(f * mult), nil
}

func (r *runner) route(sub string) error {
	target := r.a.arg(2)
	switch sub {
	case "add", "set", "on", "enable":
		if target == "" {
			return usagef("usage: v2dock route add <container> [--quota 10GB] [--period monthly] [--action block|direct]")
		}
		c, err := r.resolveContainer(target)
		if err != nil {
			return err
		}
		body := map[string]any{"key": c.Key}
		if c.Route != nil {
			body["quotaBytes"], body["quotaPeriod"], body["quotaAction"] = c.Route.QuotaBytes, c.Route.QuotaPeriod, c.Route.QuotaAction
		}
		if q, ok := r.a.flags["quota"]; ok {
			n, err := parseSize(q)
			if err != nil {
				return usageError{err.Error()}
			}
			body["quotaBytes"] = n
		}
		if p := r.a.flags["period"]; p != "" {
			body["quotaPeriod"] = p
		}
		if a := r.a.flags["action"]; a != "" {
			body["quotaAction"] = a
		}
		return r.simple("PUT", "/api/routes", body, fmt.Sprintf("%s now sends international traffic through the VPN.", c.Label))
	case "remove", "rm", "off", "disable":
		if target == "" {
			return usagef("usage: v2dock route remove <container>")
		}
		key := target
		if !strings.HasPrefix(target, "compose:") && !strings.HasPrefix(target, "name:") {
			c, err := r.resolveContainer(target)
			if err != nil {
				return err
			}
			key = c.Key
		}
		return r.simple("DELETE", "/api/routes?key="+url.QueryEscape(key), nil, "Removed; the container uses its normal network.")
	case "reset":
		if target == "" {
			return usagef("usage: v2dock route reset <container>")
		}
		c, err := r.resolveContainer(target)
		if err != nil {
			return err
		}
		return r.simple("POST", "/api/routes/reset", map[string]any{"key": c.Key}, "Quota usage reset.")
	case "", "list", "ls":
		cs, _, err := r.listContainers()
		if err != nil {
			return err
		}
		var routed []containerResp
		for _, c := range cs {
			if c.Routed {
				routed = append(routed, c)
			}
		}
		if r.g.json {
			b, _ := json.Marshal(map[string]any{"ok": true, "routes": routed})
			fmt.Fprintln(r.out, string(b))
			return nil
		}
		if len(routed) == 0 {
			fmt.Fprintln(r.out, "No container uses the VPN. Add one: v2dock route add <container>")
			return nil
		}
		w := r.table()
		fmt.Fprintln(w, "NAME\tSTATE\tQUOTA\tUSED\tWHEN EXCEEDED\tKEY")
		for _, c := range routed {
			q, used, act := "unlimited", "-", "-"
			if c.Route != nil {
				used = human(c.Route.QuotaUsed)
				if c.Route.QuotaBytes > 0 {
					q = human(c.Route.QuotaBytes) + " " + c.Route.QuotaPeriod
					act = c.Route.QuotaAction
					if c.Route.Exceeded {
						act += " (EXCEEDED)"
					}
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", c.Label, c.State, q, used, act, c.Key)
		}
		w.Flush()
		return nil
	}
	return usagef("usage: v2dock route list|add|remove|reset")
}

// -------------------------------------------------------------------- usage

func (r *runner) usage() error {
	rng := firstNonEmpty(r.a.flags["range"], "24h")
	q := "?range=" + url.QueryEscape(rng)
	if ct := firstNonEmpty(r.a.flags["container"], r.a.arg(1)); ct != "" {
		c, err := r.resolveContainer(ct)
		if err != nil {
			return err
		}
		q += "&key=" + url.QueryEscape(c.Key)
	}
	var resp struct {
		Containers []struct {
			Label  string  `json:"label"`
			Key    string  `json:"key"`
			VPN    int64   `json:"vpn"`
			Direct int64   `json:"direct"`
			Net    int64   `json:"net"`
			CPU    float64 `json:"cpu"`
			Mem    int64   `json:"mem"`
			Routed bool    `json:"routed"`
		} `json:"containers"`
	}
	raw, err := r.c.do("GET", "/api/usage"+q, nil, &resp)
	if err != nil {
		return err
	}
	r.emit(raw, func() {
		w := r.table()
		fmt.Fprintf(w, "Last %s\n", rng)
		fmt.Fprintln(w, "NAME\tVPN\tDIRECT\tTOTAL NET\tAVG CPU\tPEAK MEM")
		var tv, td int64
		for _, c := range resp.Containers {
			tv += c.VPN
			td += c.Direct
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.1f%%\t%s\n", c.Label, human(c.VPN), human(c.Direct), human(c.Net), c.CPU, human(c.Mem))
		}
		fmt.Fprintf(w, "TOTAL\t%s\t%s\t\t\t\n", human(tv), human(td))
		w.Flush()
	})
	return nil
}

// ----------------------------------------------------------------- settings

var settingKeys = map[string]string{
	"bypass-iran": "bypassIran", "bypassiran": "bypassIran",
	"remote-dns": "remoteDns", "domestic-dns": "domesticDns", "test-url": "testUrl",
	"auto-failover": "autoFailover", "failover-threshold": "failoverThreshold",
	"failover-scope": "failoverScope", "health-interval": "healthIntervalSec",
}

func (r *runner) settings(sub string) error {
	switch sub {
	case "", "get", "show":
		return r.raw("GET", "/api/settings")
	case "set":
		if len(r.a.pos) < 3 {
			return usagef("usage: v2dock settings set bypass-iran=true remote-dns=https://1.1.1.1/dns-query ...")
		}
		body := map[string]any{}
		for _, kv := range r.a.pos[2:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return usagef("expected key=value, got %q", kv)
			}
			field, known := settingKeys[strings.ToLower(k)]
			if !known {
				keys := make([]string, 0, len(settingKeys))
				for k := range settingKeys {
					if k != "bypassiran" {
						keys = append(keys, k)
					}
				}
				sort.Strings(keys)
				return usagef("unknown setting %q; known: %s", k, strings.Join(keys, ", "))
			}
			switch field {
			case "bypassIran", "autoFailover":
				b, err := strconv.ParseBool(v)
				if err != nil {
					return usagef("%s expects true or false", k)
				}
				body[field] = b
			case "failoverThreshold", "healthIntervalSec":
				n, err := strconv.Atoi(v)
				if err != nil {
					return usagef("%s expects a number", k)
				}
				body[field] = n
			default:
				body[field] = v
			}
		}
		return r.simple("PUT", "/api/settings", body, "Saved.")
	}
	return usagef("usage: v2dock settings [get] | set key=value ...")
}

// ------------------------------------------------------------------- events

func (r *runner) events() error {
	q := "?limit=" + url.QueryEscape(firstNonEmpty(r.a.flags["limit"], "30"))
	if k := r.a.flags["kind"]; k != "" {
		q += "&kind=" + url.QueryEscape(k)
	}
	var resp struct {
		Events []struct {
			TS      int64  `json:"ts"`
			Level   string `json:"level"`
			Kind    string `json:"kind"`
			Message string `json:"message"`
			Actor   string `json:"actor"`
		} `json:"events"`
	}
	raw, err := r.c.do("GET", "/api/events"+q, nil, &resp)
	if err != nil {
		return err
	}
	r.emit(raw, func() {
		w := r.table()
		for i := len(resp.Events) - 1; i >= 0; i-- {
			e := resp.Events[i]
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", time.Unix(e.TS, 0).Format("01-02 15:04:05"), e.Level, e.Kind, e.Message, e.Actor)
		}
		w.Flush()
	})
	return nil
}

// ------------------------------------------------------------------- tokens

func (r *runner) tokens(sub string) error {
	switch sub {
	case "", "list", "ls":
		var resp struct {
			Tokens []struct {
				ID, Name, Scope       string
				CreatedAt, LastUsedAt int64
			} `json:"tokens"`
		}
		raw, err := r.c.do("GET", "/api/tokens", nil, &resp)
		if err != nil {
			return err
		}
		r.emit(raw, func() {
			w := r.table()
			fmt.Fprintln(w, "ID\tNAME\tSCOPE\tCREATED\tLAST USED")
			for _, t := range resp.Tokens {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Scope, ago(t.CreatedAt), ago(t.LastUsedAt))
			}
			w.Flush()
		})
		return nil
	case "create", "add":
		name := r.a.arg(2)
		if name == "" {
			return usagef("usage: v2dock token create <name> [--scope read|admin]")
		}
		var resp struct {
			Token string `json:"token"`
		}
		raw, err := r.c.do("POST", "/api/tokens", map[string]any{"name": name, "scope": firstNonEmpty(r.a.flags["scope"], "read")}, &resp)
		if err != nil {
			return err
		}
		r.emit(raw, func() {
			fmt.Fprintln(r.out, "Token (shown once, store it safely):")
			fmt.Fprintln(r.out, resp.Token)
		})
		return nil
	case "revoke", "remove", "rm":
		id := r.a.arg(2)
		if id == "" {
			return usagef("usage: v2dock token revoke <id>")
		}
		return r.simple("DELETE", "/api/tokens/"+url.PathEscape(id), nil, "Revoked.")
	}
	return usagef("usage: v2dock token list|create|revoke")
}

// password set reads the new password from stdin (never from argv, which
// other users can see in the process list).
func (r *runner) password(sub string) error {
	if sub != "set" && sub != "reset" {
		return usagef("usage: echo 'new password' | v2dock password set")
	}
	if r.c.remote {
		return usagef("password set only works on the server: docker exec -i <agent> v2dock password set")
	}
	fmt.Fprintln(os.Stderr, "New password (min 12 characters), then Enter:")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return usagef("no password on stdin")
	}
	pw := strings.TrimRight(line, "\r\n")
	return r.simple("POST", "/api/auth/password", map[string]any{"currentPassword": "", "newPassword": pw}, "Password changed; all web sessions were logged out.")
}

// ------------------------------------------------------------------ helpers

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func ago(ts int64) string {
	if ts == 0 {
		return "never"
	}
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

const helpText = `v2dock — route Docker containers through V2Ray/Xray, keep servers online
during internet shutdowns.

USAGE
  v2dock <command> [flags]

DAEMONS (used by docker-compose)
  agent                 privileged agent (host network, NET_ADMIN)
  web                   web UI / API gateway
  guard                 read-only Docker API filter

CONNECTION
  status                                  what is connected and how it is doing
  connect <server-id>                     use this server
  connect --best [--sub ID]               test servers and use the fastest
  disconnect                              stop; containers use their normal network

SUBSCRIPTIONS & SERVERS
  sub list
  sub add <url> [--name N] [--hours 12]   add and download a subscription
  sub refresh <id> | --all
  sub rename <id> <name>
  sub remove <id>
  server list [--sub ID] [--sort delay|tcp|name] [--limit N]
  server test [--tcp] [--sub ID] [--ids a,b] [--wait]   real delay by default
  server add '<vless://...>' [--name N]   add one link to the Manual list
  server remove <id>                      (Manual list only)

CONTAINERS
  container list [--all]
  route list
  route add <container> [--quota 10GB] [--period daily|weekly|monthly|total]
                        [--action block|direct]
  route remove <container>
  route reset <container>                 reset quota usage
  usage [<container>] [--range 1h|24h|7d|30d]

SETTINGS & SECURITY
  settings [get]
  settings set bypass-iran=true auto-failover=true remote-dns=https://1.1.1.1/dns-query
  events [--limit 30] [--kind connection|failover|quota|route|security|subscription]
  token create <name> [--scope read|admin] | token list | token revoke <id>
  password set                            (reads stdin; on the server only)
  diag                                    firewall / docker / routing diagnostics

GLOBAL FLAGS
  --json                machine-readable output: one JSON object with "ok"
  --socket PATH         agent socket (default ` + wire.DefaultAgentSocket + `)
  --url URL --token T   use a remote v2dock over HTTPS with an API token
                        (env: V2DOCK_URL, V2DOCK_TOKEN, V2DOCK_SOCKET)

EXIT CODES
  0 ok · 1 error · 2 usage/invalid input · 3 not found · 4 auth · 5 busy/conflict · 6 unreachable

EXAMPLES
  docker exec v2dock-agent v2dock --json status
  docker exec v2dock-agent v2dock sub add 'https://panel.example.com/sub/TOKEN'
  docker exec v2dock-agent v2dock server test --wait && v2dock connect --best
  docker exec v2dock-agent v2dock route add my-app --quota 20GB --period monthly
`
