package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreExecSQLAcceptsLargeScripts(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	payload := strings.Repeat("x", 3*1024*1024)
	if err := store.execSQL(`
CREATE TABLE large_payload_test (value TEXT NOT NULL);
INSERT INTO large_payload_test (value) VALUES (` + sqlText(payload) + `);
`); err != nil {
		t.Fatalf("execSQL large script error: %v", err)
	}

	var rows []struct {
		Length int `json:"length"`
	}
	if err := store.queryJSON(&rows, `SELECT length(value) AS length FROM large_payload_test;`); err != nil {
		t.Fatalf("queryJSON error: %v", err)
	}
	if len(rows) != 1 || rows[0].Length != len(payload) {
		t.Fatalf("stored payload length = %+v, want %d", rows, len(payload))
	}
}
