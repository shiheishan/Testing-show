package main

import (
	"path/filepath"
	"testing"
	"time"
)

// Regression: T9 — InsertCheckResults stopped persisting the transport_* track.
// Single source of truth is now the real proxy delay, so every new row must
// store transport_status='unknown' and transport_latency_ms=NULL regardless of
// what the in-memory CheckResult still carries. The columns are kept (old
// SQLite can't DROP COLUMN) but no longer written from live data.
// Found while shipping refactor/persistent-mihomo-speedtest on 2026-06-04.
// Report: .gstack/qa-reports/qa-report-nodepanel-2026-06-04.md
func TestInsertCheckResultsStopsPersistingTransportColumns(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	// Seed a subscription + node so the check_results node_id FK is satisfied
	// (execSQL enables PRAGMA foreign_keys = ON).
	now := time.Now().UTC().Format(time.RFC3339)
	if err := store.execSQL(`
INSERT INTO subscriptions (id, name, url, created_at) VALUES (1, 'sub', 'https://example.com/sub', ` + sqlText(now) + `);
INSERT INTO nodes (id, subscription_id, name, server, port, protocol, created_at, updated_at)
VALUES (1, 1, 'n1', 'h1.example.com', 443, 'vmess', ` + sqlText(now) + `, ` + sqlText(now) + `);
`); err != nil {
		t.Fatalf("seed error: %v", err)
	}

	// Deliberately set old-style transport_* data on the struct; it must be
	// ignored on write.
	staleTransportLatency := 999
	proxyLatency := 142
	results := []CheckResult{{
		NodeID:             1,
		Status:             "online",
		LatencyMS:          &proxyLatency,
		TransportStatus:    "online",
		TransportLatencyMS: &staleTransportLatency,
		ProxyStatus:        "online",
		ProxyLatencyMS:     &proxyLatency,
		StatusSource:       "proxy",
		CheckedAt:          now,
	}}
	if err := store.InsertCheckResults(results, 24*time.Hour); err != nil {
		t.Fatalf("InsertCheckResults error: %v", err)
	}

	var rows []struct {
		TransportStatus    string `json:"transport_status"`
		TransportLatencyMS *int   `json:"transport_latency_ms"`
		ProxyStatus        string `json:"proxy_status"`
		ProxyLatencyMS     *int   `json:"proxy_latency_ms"`
		Status             string `json:"status"`
		StatusSource       string `json:"status_source"`
	}
	if err := store.queryJSON(&rows, `
SELECT transport_status, transport_latency_ms, proxy_status, proxy_latency_ms, status, status_source
FROM check_results WHERE node_id = 1;`); err != nil {
		t.Fatalf("queryJSON error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]

	// The behavior the T9 diff changed: transport_* is no longer persisted.
	if r.TransportStatus != "unknown" {
		t.Errorf("transport_status = %q, want \"unknown\" (T9: transport track no longer persisted)", r.TransportStatus)
	}
	if r.TransportLatencyMS != nil {
		t.Errorf("transport_latency_ms = %d, want NULL (T9)", *r.TransportLatencyMS)
	}

	// The real (proxy) source-of-truth columns must still round-trip intact.
	if r.Status != "online" {
		t.Errorf("status = %q, want \"online\"", r.Status)
	}
	if r.StatusSource != "proxy" {
		t.Errorf("status_source = %q, want \"proxy\"", r.StatusSource)
	}
	if r.ProxyStatus != "online" {
		t.Errorf("proxy_status = %q, want \"online\"", r.ProxyStatus)
	}
	if r.ProxyLatencyMS == nil || *r.ProxyLatencyMS != 142 {
		t.Errorf("proxy_latency_ms = %v, want 142", r.ProxyLatencyMS)
	}
}
