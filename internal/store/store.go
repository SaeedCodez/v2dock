// Package store is the agent's SQLite database. Only the agent opens it.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type DB struct {
	*sql.DB
}

// Open opens (and migrates) the database file. The file and its directory
// are private to the agent (0700/0600): they hold subscription URLs, server
// credentials and the admin password hash.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; avoids SQLITE_BUSY entirely
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(f, 0o600)
	}
	return &DB{db}, nil
}

func now() int64 { return time.Now().Unix() }

// ---------------------------------------------------------------- settings

// Settings is the whole mutable configuration, stored as one JSON document.
type Settings struct {
	Connected      bool           `json:"connected"`
	ActiveServerID string         `json:"activeServerId"`
	ActiveName     string         `json:"activeName"`
	ActiveProtocol string         `json:"activeProtocol"`
	ActiveAddress  string         `json:"activeAddress"`
	ActiveSubID    string         `json:"activeSubscriptionId"`
	ActiveOutbound map[string]any `json:"activeOutbound,omitempty"`
	ConnectedAt    int64          `json:"connectedAt"`

	BypassIran        bool   `json:"bypassIran"`
	RemoteDNS         string `json:"remoteDns"`
	DomesticDNS       string `json:"domesticDns"`
	TestURL           string `json:"testUrl"`
	AutoFailover      bool   `json:"autoFailover"`
	FailoverThreshold int    `json:"failoverThreshold"`
	FailoverScope     string `json:"failoverScope"` // "subscription" | "all"
	HealthIntervalSec int    `json:"healthIntervalSec"`
}

func DefaultSettings() Settings {
	return Settings{
		BypassIran:        true,
		RemoteDNS:         "https://1.1.1.1/dns-query",
		DomesticDNS:       "localhost",
		TestURL:           "https://www.gstatic.com/generate_204",
		AutoFailover:      true,
		FailoverThreshold: 3,
		FailoverScope:     "all",
		HealthIntervalSec: 30,
	}
}

func (db *DB) getKV(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (db *DB) setKV(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (db *DB) GetKV(ctx context.Context, key string) (string, bool, error) { return db.getKV(ctx, key) }
func (db *DB) SetKV(ctx context.Context, key, value string) error          { return db.setKV(ctx, key, value) }
func (db *DB) DeleteKV(ctx context.Context, key string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM kv WHERE key = ?`, key)
	return err
}

func (db *DB) LoadSettings(ctx context.Context) (Settings, error) {
	s := DefaultSettings()
	v, ok, err := db.getKV(ctx, "settings")
	if err != nil || !ok {
		return s, err
	}
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return DefaultSettings(), nil
	}
	return s, nil
}

func (db *DB) SaveSettings(ctx context.Context, s Settings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return db.setKV(ctx, "settings", string(b))
}

// ------------------------------------------------------------ subscriptions

type Subscription struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	URL             string    `json:"-"`
	Kind            string    `json:"kind"` // "url" | "manual"
	AutoUpdateHours int       `json:"autoUpdateHours"`
	LastFetchAt     int64     `json:"lastFetchAt"`
	LastSuccessAt   int64     `json:"lastSuccessAt"`
	LastError       string    `json:"lastError"`
	UserInfo        *UserInfo `json:"userInfo,omitempty"`
	Skipped         []string  `json:"skipped,omitempty"`
	CreatedAt       int64     `json:"createdAt"`
	ServerCount     int       `json:"serverCount"`
	FetchedViaVPN   bool      `json:"fetchedViaVpn"`
}

// UserInfo is the subscription-userinfo header most panels send.
type UserInfo struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"`
}

const subCols = `s.id, s.name, s.url, s.kind, s.auto_update_hours, s.last_fetch_at, s.last_success_at, s.last_error, s.userinfo, s.skipped, s.created_at, s.via_vpn,
	(SELECT COUNT(*) FROM servers v WHERE v.subscription_id = s.id)`

func scanSub(sc interface{ Scan(...any) error }) (Subscription, error) {
	var s Subscription
	var ui, skipped string
	var via int
	err := sc.Scan(&s.ID, &s.Name, &s.URL, &s.Kind, &s.AutoUpdateHours, &s.LastFetchAt, &s.LastSuccessAt, &s.LastError, &ui, &skipped, &s.CreatedAt, &via, &s.ServerCount)
	if err != nil {
		return s, err
	}
	s.FetchedViaVPN = via == 1
	if ui != "" {
		var u UserInfo
		if json.Unmarshal([]byte(ui), &u) == nil {
			s.UserInfo = &u
		}
	}
	if skipped != "" {
		_ = json.Unmarshal([]byte(skipped), &s.Skipped)
	}
	return s, nil
}

func (db *DB) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+subCols+` FROM subscriptions s ORDER BY s.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		s, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

var ErrNotFound = errors.New("not found")

func (db *DB) GetSubscription(ctx context.Context, id string) (Subscription, error) {
	s, err := scanSub(db.QueryRowContext(ctx, `SELECT `+subCols+` FROM subscriptions s WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func (db *DB) CreateSubscription(ctx context.Context, s Subscription) error {
	_, err := db.ExecContext(ctx, `INSERT INTO subscriptions (id, name, url, kind, auto_update_hours, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.URL, s.Kind, s.AutoUpdateHours, now())
	return err
}

func (db *DB) UpdateSubscription(ctx context.Context, id, name, url string, hours int) error {
	res, err := db.ExecContext(ctx, `UPDATE subscriptions SET name = ?, url = ?, auto_update_hours = ? WHERE id = ?`, name, url, hours, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) DeleteSubscription(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM subscriptions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) RecordFetch(ctx context.Context, id string, ok bool, errMsg string, ui *UserInfo, skipped []string, viaVPN bool) error {
	var uiJSON, skJSON string
	if ui != nil {
		b, _ := json.Marshal(ui)
		uiJSON = string(b)
	}
	if len(skipped) > 0 {
		if len(skipped) > 50 {
			skipped = skipped[:50]
		}
		b, _ := json.Marshal(skipped)
		skJSON = string(b)
	}
	via := 0
	if viaVPN {
		via = 1
	}
	if ok {
		_, err := db.ExecContext(ctx, `UPDATE subscriptions SET last_fetch_at = ?, last_success_at = ?, last_error = '', userinfo = CASE WHEN ? = '' THEN userinfo ELSE ? END, skipped = ?, via_vpn = ? WHERE id = ?`,
			now(), now(), uiJSON, uiJSON, skJSON, via, id)
		return err
	}
	_, err := db.ExecContext(ctx, `UPDATE subscriptions SET last_fetch_at = ?, last_error = ? WHERE id = ?`, now(), errMsg, id)
	return err
}

// ------------------------------------------------------------------ servers

type Server struct {
	ID             string         `json:"id"`
	SubscriptionID string         `json:"subscriptionId"`
	Position       int            `json:"position"`
	Name           string         `json:"name"`
	Protocol       string         `json:"protocol"`
	Address        string         `json:"address"`
	Port           int            `json:"port"`
	Transport      string         `json:"transport"`
	Security       string         `json:"security"`
	Warning        string         `json:"warning,omitempty"`
	Link           string         `json:"-"`
	Outbound       map[string]any `json:"-"`
	TCPms          *int           `json:"tcpMs"`
	TCPError       string         `json:"tcpError,omitempty"`
	TCPAt          int64          `json:"tcpAt,omitempty"`
	DelayMs        *int           `json:"delayMs"`
	DelayError     string         `json:"delayError,omitempty"`
	DelayAt        int64          `json:"delayAt,omitempty"`
}

const serverCols = `id, subscription_id, position, name, protocol, address, port, transport, security, warning, link, outbound, tcp_ms, tcp_error, tcp_at, delay_ms, delay_error, delay_at`

func scanServer(sc interface{ Scan(...any) error }) (Server, error) {
	var s Server
	var ob string
	var tcp, delay sql.NullInt64
	err := sc.Scan(&s.ID, &s.SubscriptionID, &s.Position, &s.Name, &s.Protocol, &s.Address, &s.Port, &s.Transport, &s.Security, &s.Warning, &s.Link, &ob, &tcp, &s.TCPError, &s.TCPAt, &delay, &s.DelayError, &s.DelayAt)
	if err != nil {
		return s, err
	}
	_ = json.Unmarshal([]byte(ob), &s.Outbound)
	if tcp.Valid {
		v := int(tcp.Int64)
		s.TCPms = &v
	}
	if delay.Valid {
		v := int(delay.Int64)
		s.DelayMs = &v
	}
	return s, nil
}

func (db *DB) ListServers(ctx context.Context, subID string) ([]Server, error) {
	q := `SELECT ` + serverCols + ` FROM servers`
	args := []any{}
	if subID != "" {
		q += ` WHERE subscription_id = ?`
		args = append(args, subID)
	}
	q += ` ORDER BY subscription_id, position`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *DB) GetServer(ctx context.Context, id string) (Server, error) {
	s, err := scanServer(db.QueryRowContext(ctx, `SELECT `+serverCols+` FROM servers WHERE id = ? LIMIT 1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// ReplaceServers sets a subscription's server list, keeping test results for
// servers that are still present.
func (db *DB) ReplaceServers(ctx context.Context, subID string, servers []Server) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := map[string]bool{}
	for i, s := range servers {
		keep[s.ID] = true
		ob, _ := json.Marshal(s.Outbound)
		_, err := tx.ExecContext(ctx, `INSERT INTO servers (id, subscription_id, position, name, protocol, address, port, transport, security, warning, link, outbound)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(subscription_id, id) DO UPDATE SET position = excluded.position, name = excluded.name, protocol = excluded.protocol,
			address = excluded.address, port = excluded.port, transport = excluded.transport, security = excluded.security,
			warning = excluded.warning, link = excluded.link, outbound = excluded.outbound`,
			s.ID, subID, i, s.Name, s.Protocol, s.Address, s.Port, s.Transport, s.Security, s.Warning, s.Link, string(ob))
		if err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM servers WHERE subscription_id = ?`, subID)
	if err != nil {
		return err
	}
	var drop []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && !keep[id] {
			drop = append(drop, id)
		}
	}
	rows.Close()
	for _, id := range drop {
		if _, err := tx.ExecContext(ctx, `DELETE FROM servers WHERE subscription_id = ? AND id = ?`, subID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AppendServer adds one server to a (manual) subscription.
func (db *DB) AppendServer(ctx context.Context, subID string, s Server) error {
	ob, _ := json.Marshal(s.Outbound)
	_, err := db.ExecContext(ctx, `INSERT INTO servers (id, subscription_id, position, name, protocol, address, port, transport, security, warning, link, outbound)
		VALUES (?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM servers WHERE subscription_id = ?), ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(subscription_id, id) DO UPDATE SET name = excluded.name`,
		s.ID, subID, subID, s.Name, s.Protocol, s.Address, s.Port, s.Transport, s.Security, s.Warning, s.Link, string(ob))
	return err
}

func (db *DB) DeleteServer(ctx context.Context, subID, id string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM servers WHERE subscription_id = ? AND id = ?`, subID, id)
	return err
}

func nullInt(ms int, ok bool) any {
	if !ok {
		return nil
	}
	return ms
}

func (db *DB) RecordTCP(ctx context.Context, id string, ms int, ok bool, errMsg string) error {
	_, err := db.ExecContext(ctx, `UPDATE servers SET tcp_ms = ?, tcp_error = ?, tcp_at = ? WHERE id = ?`, nullInt(ms, ok), errMsg, now(), id)
	return err
}

func (db *DB) RecordDelay(ctx context.Context, id string, ms int, ok bool, errMsg string) error {
	_, err := db.ExecContext(ctx, `UPDATE servers SET delay_ms = ?, delay_error = ?, delay_at = ? WHERE id = ?`, nullInt(ms, ok), errMsg, now(), id)
	return err
}

// ------------------------------------------------------------------- routes

type Route struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	QuotaBytes  int64  `json:"quotaBytes"`
	QuotaPeriod string `json:"quotaPeriod"` // daily | weekly | monthly | total
	QuotaAction string `json:"quotaAction"` // block | direct
	QuotaUsed   int64  `json:"quotaUsed"`
	PeriodStart int64  `json:"periodStart"`
	Exceeded    bool   `json:"exceeded"`
	CreatedAt   int64  `json:"createdAt"`
}

const routeCols = `key, label, quota_bytes, quota_period, quota_action, quota_used, period_start, exceeded, created_at`

func scanRoute(sc interface{ Scan(...any) error }) (Route, error) {
	var r Route
	var ex int
	err := sc.Scan(&r.Key, &r.Label, &r.QuotaBytes, &r.QuotaPeriod, &r.QuotaAction, &r.QuotaUsed, &r.PeriodStart, &ex, &r.CreatedAt)
	r.Exceeded = ex == 1
	return r, err
}

func (db *DB) ListRoutes(ctx context.Context) ([]Route, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+routeCols+` FROM routes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Route
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) GetRoute(ctx context.Context, key string) (Route, error) {
	r, err := scanRoute(db.QueryRowContext(ctx, `SELECT `+routeCols+` FROM routes WHERE key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// UpsertRoute creates or updates a route's settings. Usage is kept unless the
// period changes.
func (db *DB) UpsertRoute(ctx context.Context, r Route, periodStart int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO routes (key, label, quota_bytes, quota_period, quota_action, quota_used, period_start, exceeded, created_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, 0, ?)
		ON CONFLICT(key) DO UPDATE SET label = excluded.label, quota_bytes = excluded.quota_bytes, quota_action = excluded.quota_action,
		  quota_used = CASE WHEN routes.quota_period = excluded.quota_period THEN routes.quota_used ELSE 0 END,
		  period_start = CASE WHEN routes.quota_period = excluded.quota_period THEN routes.period_start ELSE excluded.period_start END,
		  quota_period = excluded.quota_period,
		  exceeded = CASE WHEN excluded.quota_bytes > 0 AND (CASE WHEN routes.quota_period = excluded.quota_period THEN routes.quota_used ELSE 0 END) >= excluded.quota_bytes THEN 1 ELSE 0 END`,
		r.Key, r.Label, r.QuotaBytes, r.QuotaPeriod, r.QuotaAction, periodStart, now())
	return err
}

// RenameRoute moves a route (and its usage history) to a new key. It does
// nothing, and reports false, when the new key already exists or the old one
// does not.
func (db *DB) RenameRoute(ctx context.Context, from, to string) (bool, error) {
	if from == to {
		return false, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE routes SET key = ? WHERE key = ? AND NOT EXISTS (SELECT 1 FROM routes WHERE key = ?)`, to, from, to)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	for _, table := range []string{"usage_min", "usage_hour"} {
		// OR IGNORE: if both keys already have a sample for the same time bucket, keep the new key's.
		if _, err := tx.ExecContext(ctx, `UPDATE OR IGNORE `+table+` SET key = ? WHERE key = ?`, to, from); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (db *DB) DeleteRoute(ctx context.Context, key string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM routes WHERE key = ?`, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) ResetRouteUsage(ctx context.Context, key string, periodStart int64) error {
	_, err := db.ExecContext(ctx, `UPDATE routes SET quota_used = 0, exceeded = 0, period_start = ? WHERE key = ?`, periodStart, key)
	return err
}

// AddRouteUsage adds VPN bytes and reports whether the route just crossed its
// quota.
func (db *DB) AddRouteUsage(ctx context.Context, key string, bytes int64) (crossed bool, err error) {
	var before Route
	before, err = db.GetRoute(ctx, key)
	if err != nil {
		return false, err
	}
	used := before.QuotaUsed + bytes
	exceeded := before.QuotaBytes > 0 && used >= before.QuotaBytes
	ex := 0
	if exceeded {
		ex = 1
	}
	_, err = db.ExecContext(ctx, `UPDATE routes SET quota_used = ?, exceeded = ? WHERE key = ?`, used, ex, key)
	return exceeded && !before.Exceeded, err
}

// ------------------------------------------------------------------- usage

// UsageDelta is what one sampling round adds for one container key.
type UsageDelta struct {
	Key                            string
	VPNUp, VPNDown, DirUp, DirDown int64
	NetRx, NetTx                   int64
	CPU                            float64 // percent, one sample
	HasCPU                         bool
	Mem                            int64
}

func (db *DB) AddUsage(ctx context.Context, ts time.Time, deltas []UsageDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	minute := ts.Unix() / 60 * 60
	hour := ts.Unix() / 3600 * 3600
	for _, d := range deltas {
		cpuN := 0
		if d.HasCPU {
			cpuN = 1
		}
		for _, t := range []struct {
			table string
			ts    int64
		}{{"usage_min", minute}, {"usage_hour", hour}} {
			_, err := tx.ExecContext(ctx, `INSERT INTO `+t.table+` (ts, key, vpn_up, vpn_down, dir_up, dir_down, net_rx, net_tx, cpu_sum, cpu_n, mem_max)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(ts, key) DO UPDATE SET vpn_up = vpn_up + excluded.vpn_up, vpn_down = vpn_down + excluded.vpn_down,
				dir_up = dir_up + excluded.dir_up, dir_down = dir_down + excluded.dir_down,
				net_rx = net_rx + excluded.net_rx, net_tx = net_tx + excluded.net_tx,
				cpu_sum = cpu_sum + excluded.cpu_sum, cpu_n = cpu_n + excluded.cpu_n,
				mem_max = MAX(mem_max, excluded.mem_max)`,
				t.ts, d.Key, d.VPNUp, d.VPNDown, d.DirUp, d.DirDown, d.NetRx, d.NetTx, d.CPU, cpuN, d.Mem)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// UsagePoint is one aggregated bucket.
type UsagePoint struct {
	TS      int64   `json:"ts"`
	VPN     int64   `json:"vpn"`
	VPNUp   int64   `json:"vpnUp"`
	VPNDown int64   `json:"vpnDown"`
	Net     int64   `json:"net"`
	Direct  int64   `json:"direct"`
	CPU     float64 `json:"cpu"`
	Mem     int64   `json:"mem"`
}

// UsageSeries aggregates usage since `since` into buckets of `bucket` seconds.
// key "" means all containers.
func (db *DB) UsageSeries(ctx context.Context, since time.Time, bucket int64, key string) ([]UsagePoint, error) {
	table := "usage_hour"
	if bucket < 3600 {
		table = "usage_min"
	}
	q := `SELECT (ts / ?) * ? AS b, SUM(vpn_up), SUM(vpn_down), SUM(net_rx + net_tx), SUM(cpu_sum), SUM(cpu_n), MAX(mem_max)
		FROM ` + table + ` WHERE ts >= ?`
	args := []any{bucket, bucket, since.Unix()}
	if key != "" {
		q += ` AND key = ?`
		args = append(args, key)
	}
	q += ` GROUP BY b ORDER BY b`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsagePoint
	for rows.Next() {
		var p UsagePoint
		var cpuSum float64
		var cpuN int64
		if err := rows.Scan(&p.TS, &p.VPNUp, &p.VPNDown, &p.Net, &cpuSum, &cpuN, &p.Mem); err != nil {
			return nil, err
		}
		p.VPN = p.VPNUp + p.VPNDown
		p.Direct = max(0, p.Net-p.VPN)
		if cpuN > 0 {
			p.CPU = cpuSum / float64(cpuN)
		}
		if key == "" {
			// Across containers CPU averages and memory maxima are not
			// meaningful; the UI shows them per container only.
			p.CPU, p.Mem = 0, 0
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UsageTotal is per-container totals over a window.
type UsageTotal struct {
	Key     string  `json:"key"`
	VPN     int64   `json:"vpn"`
	VPNUp   int64   `json:"vpnUp"`
	VPNDown int64   `json:"vpnDown"`
	Net     int64   `json:"net"`
	Direct  int64   `json:"direct"`
	CPU     float64 `json:"cpu"`
	Mem     int64   `json:"mem"`
}

func (db *DB) UsageTotals(ctx context.Context, since time.Time) ([]UsageTotal, error) {
	table := "usage_hour"
	if time.Since(since) <= 48*time.Hour {
		table = "usage_min"
	}
	rows, err := db.QueryContext(ctx, `SELECT key, SUM(vpn_up), SUM(vpn_down), SUM(net_rx + net_tx), SUM(cpu_sum), SUM(cpu_n), MAX(mem_max)
		FROM `+table+` WHERE ts >= ? GROUP BY key`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageTotal
	for rows.Next() {
		var t UsageTotal
		var cpuSum float64
		var cpuN int64
		if err := rows.Scan(&t.Key, &t.VPNUp, &t.VPNDown, &t.Net, &cpuSum, &cpuN, &t.Mem); err != nil {
			return nil, err
		}
		t.VPN = t.VPNUp + t.VPNDown
		t.Direct = max(0, t.Net-t.VPN)
		if cpuN > 0 {
			t.CPU = cpuSum / float64(cpuN)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Prune drops minute data older than 2 days and hourly data older than 90.
func (db *DB) Prune(ctx context.Context) error {
	n := time.Now()
	if _, err := db.ExecContext(ctx, `DELETE FROM usage_min WHERE ts < ?`, n.Add(-48*time.Hour).Unix()); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM usage_hour WHERE ts < ?`, n.Add(-90*24*time.Hour).Unix()); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM events WHERE ts < ?`, n.Add(-90*24*time.Hour).Unix()); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, n.Unix())
	return err
}

// ------------------------------------------------------------------- events

type Event struct {
	ID      int64  `json:"id"`
	TS      int64  `json:"ts"`
	Level   string `json:"level"` // info | warn | error
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Actor   string `json:"actor,omitempty"`
	IP      string `json:"ip,omitempty"`
}

func (db *DB) AddEvent(ctx context.Context, e Event) error {
	if len(e.Message) > 1000 {
		e.Message = e.Message[:1000]
	}
	_, err := db.ExecContext(ctx, `INSERT INTO events (ts, level, kind, message, actor, ip) VALUES (?, ?, ?, ?, ?, ?)`,
		now(), e.Level, e.Kind, e.Message, e.Actor, e.IP)
	return err
}

func (db *DB) ListEvents(ctx context.Context, limit int, before int64, kind string) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, ts, level, kind, message, actor, ip FROM events WHERE 1=1`
	args := []any{}
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Level, &e.Kind, &e.Message, &e.Actor, &e.IP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
