package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Regression: the /api/nodes/{id}/history endpoint serializes []CheckHistoryPoint
// directly. T9 (v0.2.7) retired the transport/proxy split and /api/nodes dropped
// the five fields, but CheckHistoryPoint kept emitting them, leaving the two
// endpoints' contracts inconsistent (new rows always serialized unknown/NULL).
// These fields must stay out of the JSON contract (DB columns are retained).
// Found in TODOS.md "/history 端点仍输出已删的 transport 字段" (P3, 2026-06-04).
func TestCheckHistoryPointDropsRetiredTransportFields(t *testing.T) {
	latency := 142
	point := CheckHistoryPoint{
		Status:             "online",
		LatencyMS:          &latency,
		TransportStatus:    "unknown",
		TransportLatencyMS: nil,
		ProxyStatus:        "online",
		ProxyLatencyMS:     &latency,
		StatusSource:       "proxy",
		CheckedAt:          "2026-06-05T00:00:00Z",
	}

	raw, err := json.Marshal(point)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	retired := []string{
		"transport_status",
		"transport_latency_ms",
		"proxy_status",
		"proxy_latency_ms",
		"status_source",
	}
	for _, field := range retired {
		if _, present := decoded[field]; present {
			t.Errorf("history JSON must not expose retired field %q: %s", field, raw)
		}
	}

	// The fields that remain part of the contract must still be present.
	for _, field := range []string{"status", "latency_ms", "checked_at"} {
		if _, present := decoded[field]; !present {
			t.Errorf("history JSON dropped contract field %q: %s", field, raw)
		}
	}

	// Belt-and-suspenders: the retired names should not appear anywhere in the
	// payload, even as substrings of an unexpected nesting.
	if strings.Contains(string(raw), "status_source") {
		t.Errorf("payload still mentions status_source: %s", raw)
	}
}
