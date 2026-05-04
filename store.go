package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Store struct {
	dbPath        string
	mu            sync.Mutex
	maintenanceMu sync.Mutex
}

type Subscription struct {
	ID              int     `json:"id"`
	Name            string  `json:"name"`
	URL             string  `json:"url"`
	CreatedAt       string  `json:"created_at"`
	LastRefreshedAt *string `json:"last_refreshed_at"`
	LastError       *string `json:"last_error"`
	Status          string  `json:"status"`
}

type NodeRecord struct {
	ID             int            `json:"id"`
	SubscriptionID int            `json:"subscription_id"`
	Name           string         `json:"name"`
	Server         string         `json:"server"`
	Port           int            `json:"port"`
	Protocol       string         `json:"protocol"`
	ExtraParamsRaw string         `json:"extra_params"`
	ExtraParams    map[string]any `json:"-"`
	StaleSince     *string        `json:"stale_since"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

type CheckState struct {
	Status      string `json:"status"`
	LatencyMS   *int   `json:"latency_ms"`
	LastChecked string `json:"last_checked"`
}

type CheckResult struct {
	NodeID    int
	Status    string
	LatencyMS *int
	CheckedAt string
}

type CheckHistoryPoint struct {
	Status    string `json:"status"`
	LatencyMS *int   `json:"latency_ms"`
	CheckedAt string `json:"checked_at"`
}

type UpsertStats struct {
	Created     int `json:"created"`
	Updated     int `json:"updated"`
	StaleMarked int `json:"stale_marked"`
	Total       int `json:"total"`
}

func NewStore(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	store := &Store{dbPath: dbPath}
	if err := store.initialize(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) initialize() error {
	schema := `
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS subscriptions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	url TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	last_refreshed_at TEXT,
	last_error TEXT
);
CREATE TABLE IF NOT EXISTS nodes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	subscription_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	server TEXT NOT NULL,
	port INTEGER NOT NULL,
	protocol TEXT NOT NULL,
	extra_params TEXT NOT NULL DEFAULT '{}',
	stale_since TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY (subscription_id) REFERENCES subscriptions(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS check_results (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	node_id INTEGER NOT NULL,
	status TEXT NOT NULL,
	latency_ms INTEGER,
	checked_at TEXT NOT NULL,
	FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_nodes_subscription ON nodes (subscription_id);
CREATE INDEX IF NOT EXISTS idx_nodes_active ON nodes (subscription_id, stale_since);
CREATE INDEX IF NOT EXISTS idx_check_results_node_time ON check_results (node_id, checked_at DESC);
`
	return s.execSQL(schema)
}

func (s *Store) ListSubscriptions() ([]Subscription, error) {
	var items []Subscription
	err := s.queryJSON(&items, `
SELECT id, name, url, created_at, last_refreshed_at, last_error
FROM subscriptions
ORDER BY name COLLATE NOCASE ASC, id ASC;
`)
	applySubscriptionStatuses(items)
	return items, err
}

func (s *Store) GetSubscription(id int) (*Subscription, error) {
	var items []Subscription
	err := s.queryJSON(&items, fmt.Sprintf(`
SELECT id, name, url, created_at, last_refreshed_at, last_error
FROM subscriptions
WHERE id = %d;
`, id))
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	applySubscriptionStatuses(items)
	return &items[0], nil
}

func (s *Store) GetSubscriptionByURL(rawURL string) (*Subscription, error) {
	var items []Subscription
	err := s.queryJSON(&items, fmt.Sprintf(`
SELECT id, name, url, created_at, last_refreshed_at, last_error
FROM subscriptions
WHERE url = %s
LIMIT 1;
`, sqlText(rawURL)))
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	applySubscriptionStatuses(items)
	return &items[0], nil
}

func (s *Store) ListSubscriptionsByName(name string) ([]Subscription, error) {
	var items []Subscription
	err := s.queryJSON(&items, fmt.Sprintf(`
SELECT id, name, url, created_at, last_refreshed_at, last_error
FROM subscriptions
WHERE name = %s
ORDER BY id ASC;
`, sqlText(name)))
	if err != nil {
		return nil, err
	}
	applySubscriptionStatuses(items)
	return items, nil
}

func (s *Store) CreateSubscription(name, rawURL string) (*Subscription, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var items []Subscription
	err := s.queryJSON(&items, fmt.Sprintf(`
INSERT INTO subscriptions (name, url, created_at)
VALUES (%s, %s, %s);
SELECT id, name, url, created_at, last_refreshed_at, last_error
FROM subscriptions
WHERE id = last_insert_rowid();
`, sqlText(name), sqlText(rawURL), sqlText(now)))
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("subscription not created")
	}
	applySubscriptionStatuses(items)
	return &items[0], nil
}

func (s *Store) UpsertSubscription(name, rawURL string) (*Subscription, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var items []Subscription
	err := s.queryJSON(&items, fmt.Sprintf(`
INSERT INTO subscriptions (name, url, created_at)
VALUES (%s, %s, %s)
ON CONFLICT(url) DO UPDATE SET
	name = excluded.name;
SELECT id, name, url, created_at, last_refreshed_at, last_error
FROM subscriptions
WHERE url = %s;
`, sqlText(name), sqlText(rawURL), sqlText(now), sqlText(rawURL)))
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("subscription not created")
	}
	applySubscriptionStatuses(items)
	return &items[0], nil
}

func (s *Store) UpdateSubscriptionIdentity(id int, name, rawURL string) (*Subscription, error) {
	var items []Subscription
	err := s.queryJSON(&items, fmt.Sprintf(`
UPDATE subscriptions
SET name = %s, url = %s
WHERE id = %d;
SELECT id, name, url, created_at, last_refreshed_at, last_error
FROM subscriptions
WHERE id = %d;
`, sqlText(name), sqlText(rawURL), id, id))
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("subscription not found")
	}
	applySubscriptionStatuses(items)
	return &items[0], nil
}

func (s *Store) DeleteSubscription(id int) (bool, error) {
	var items []struct {
		Changed int `json:"changed"`
	}
	err := s.queryJSON(&items, fmt.Sprintf(`
DELETE FROM subscriptions WHERE id = %d;
SELECT changes() AS changed;
`, id))
	if err != nil {
		return false, err
	}
	return len(items) > 0 && items[0].Changed > 0, nil
}

func (s *Store) DeleteSubscriptionsNotInIDs(ids []int) error {
	if len(ids) == 0 {
		return s.execSQL(`DELETE FROM subscriptions;`)
	}
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, strconv.Itoa(id))
	}
	return s.execSQL(fmt.Sprintf(`
DELETE FROM subscriptions
WHERE id NOT IN (%s);
`, strings.Join(quoted, ", ")))
}

func (s *Store) MarkSubscriptionError(id int, message string) error {
	return s.execSQL(fmt.Sprintf(`
UPDATE subscriptions
SET last_error = %s
WHERE id = %d;
`, sqlText(message), id))
}

func (s *Store) MarkSubscriptionSuccess(id int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.execSQL(fmt.Sprintf(`
UPDATE subscriptions
SET last_refreshed_at = %s, last_error = NULL
WHERE id = %d;
`, sqlText(now), id))
}

func (s *Store) UpsertNodes(subscriptionID int, nodes []ParsedNode) (UpsertStats, error) {
	var existing []NodeRecord
	err := s.queryJSON(&existing, fmt.Sprintf(`
SELECT id, subscription_id, name, server, port, protocol, extra_params, stale_since, created_at, updated_at
FROM nodes
WHERE subscription_id = %d;
`, subscriptionID))
	if err != nil {
		return UpsertStats{}, err
	}

	type key struct {
		Server   string
		Port     int
		Protocol string
		Name     string
	}
	existingByEndpoint := map[key]NodeRecord{}
	for _, item := range existing {
		existingByEndpoint[key{Server: item.Server, Port: item.Port, Protocol: item.Protocol, Name: item.Name}] = item
	}

	now := time.Now().UTC().Format(time.RFC3339)
	cleanupBefore := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	var script strings.Builder
	script.WriteString("BEGIN;\n")
	stats := UpsertStats{Total: len(nodes)}
	seen := map[key]struct{}{}

	for _, node := range nodes {
		endpoint := key{Server: node.Server, Port: node.Port, Protocol: node.Protocol, Name: node.Name}
		seen[endpoint] = struct{}{}
		payload, _ := json.Marshal(node.ExtraParams)
		if current, ok := existingByEndpoint[endpoint]; ok {
			script.WriteString(fmt.Sprintf(`
UPDATE nodes
SET name = %s,
    protocol = %s,
    extra_params = %s,
    stale_since = NULL,
    updated_at = %s
WHERE id = %d;
`, sqlText(node.Name), sqlText(node.Protocol), sqlText(string(payload)), sqlText(now), current.ID))
			stats.Updated++
			continue
		}
		script.WriteString(fmt.Sprintf(`
INSERT INTO nodes (
	subscription_id, name, server, port, protocol, extra_params,
	stale_since, created_at, updated_at
) VALUES (%d, %s, %s, %d, %s, %s, NULL, %s, %s);
`, subscriptionID, sqlText(node.Name), sqlText(node.Server), node.Port, sqlText(node.Protocol), sqlText(string(payload)), sqlText(now), sqlText(now)))
		stats.Created++
	}

	for endpoint, current := range existingByEndpoint {
		if _, ok := seen[endpoint]; ok || current.StaleSince != nil {
			continue
		}
		script.WriteString(fmt.Sprintf(`
UPDATE nodes
SET stale_since = %s, updated_at = %s
WHERE id = %d;
`, sqlText(now), sqlText(now), current.ID))
		stats.StaleMarked++
	}

	script.WriteString(fmt.Sprintf(`
DELETE FROM nodes
WHERE subscription_id = %d
  AND stale_since IS NOT NULL
  AND stale_since < %s;
COMMIT;
`, subscriptionID, sqlText(cleanupBefore)))

	if err := s.execSQL(script.String()); err != nil {
		return UpsertStats{}, err
	}
	return stats, nil
}

func (s *Store) ListNodes(subscriptionID *int, includeStale bool) ([]NodeRecord, error) {
	conditions := []string{}
	if subscriptionID != nil {
		conditions = append(conditions, fmt.Sprintf("subscription_id = %d", *subscriptionID))
	}
	if !includeStale {
		conditions = append(conditions, "stale_since IS NULL")
	}
	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}
	var items []NodeRecord
	err := s.queryJSON(&items, fmt.Sprintf(`
SELECT id, subscription_id, name, server, port, protocol, extra_params, stale_since, created_at, updated_at
FROM nodes
%s
ORDER BY subscription_id ASC, name COLLATE NOCASE ASC, id ASC;
`, whereClause))
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].ExtraParams = map[string]any{}
		if strings.TrimSpace(items[index].ExtraParamsRaw) != "" {
			_ = json.Unmarshal([]byte(items[index].ExtraParamsRaw), &items[index].ExtraParams)
		}
	}
	return items, nil
}

func (s *Store) CountActiveNodes(subscriptionID *int) (int, error) {
	where := "WHERE stale_since IS NULL"
	if subscriptionID != nil {
		where += fmt.Sprintf(" AND subscription_id = %d", *subscriptionID)
	}
	var items []struct {
		Total int `json:"total"`
	}
	err := s.queryJSON(&items, fmt.Sprintf(`
SELECT COUNT(*) AS total
FROM nodes
%s;
`, where))
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}
	return items[0].Total, nil
}

func (s *Store) InsertCheckResults(results []CheckResult, retention time.Duration) error {
	if len(results) == 0 {
		return nil
	}
	var script strings.Builder
	script.WriteString("BEGIN;\n")
	for _, result := range results {
		latencyValue := "NULL"
		if result.LatencyMS != nil {
			latencyValue = strconv.Itoa(*result.LatencyMS)
		}
		script.WriteString(fmt.Sprintf(`
INSERT INTO check_results (node_id, status, latency_ms, checked_at)
VALUES (%d, %s, %s, %s);
`, result.NodeID, sqlText(result.Status), latencyValue, sqlText(result.CheckedAt)))
	}
	cutoff := time.Now().UTC().Add(-retention).Format(time.RFC3339)
	script.WriteString(fmt.Sprintf(`
DELETE FROM check_results WHERE checked_at < %s;
COMMIT;
`, sqlText(cutoff)))
	return s.execSQL(script.String())
}

func (s *Store) LoadLatestResults() (map[int]CheckState, error) {
	var rows []struct {
		NodeID      int    `json:"node_id"`
		Status      string `json:"status"`
		LatencyMS   *int   `json:"latency_ms"`
		LastChecked string `json:"last_checked"`
	}
	err := s.queryJSON(&rows, `
SELECT node_id, status, latency_ms, checked_at AS last_checked
FROM check_results
WHERE id IN (
	SELECT MAX(id)
	FROM check_results
	GROUP BY node_id
);
`)
	if err != nil {
		return nil, err
	}
	items := make(map[int]CheckState, len(rows))
	for _, row := range rows {
		items[row.NodeID] = CheckState{
			Status:      row.Status,
			LatencyMS:   row.LatencyMS,
			LastChecked: row.LastChecked,
		}
	}
	return items, nil
}

func (s *Store) ListCheckHistory(nodeID int, since time.Time) ([]CheckHistoryPoint, error) {
	var rows []CheckHistoryPoint
	err := s.queryJSON(&rows, fmt.Sprintf(`
SELECT status, latency_ms, checked_at
FROM check_results
WHERE node_id = %d
  AND checked_at >= %s
ORDER BY checked_at ASC;
`, nodeID, sqlText(since.UTC().Format(time.RFC3339))))
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) runSQLite(jsonOutput bool, sql string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	args := []string{}
	if jsonOutput {
		args = append(args, "-json")
	}
	script := "PRAGMA foreign_keys = ON;\n" + sql
	args = append(args, s.dbPath, script)

	cmd := exec.Command("sqlite3", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return stdout.Bytes(), nil
}

func (s *Store) execSQL(sql string) error {
	_, err := s.runSQLite(false, sql)
	return err
}

func (s *Store) queryJSON(dest any, sql string) error {
	out, err := s.runSQLite(true, sql)
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		trimmed = []byte("[]")
	}
	return json.Unmarshal(trimmed, dest)
}

func sqlText(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func applySubscriptionStatuses(items []Subscription) {
	for index := range items {
		items[index].Status = "ok"
		if items[index].LastError != nil && strings.TrimSpace(*items[index].LastError) != "" {
			items[index].Status = "failed"
		}
	}
}

func (s *Store) LockMaintenance() {
	s.maintenanceMu.Lock()
}

func (s *Store) UnlockMaintenance() {
	s.maintenanceMu.Unlock()
}
