package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*Store, *SubscriptionService, http.Handler) {
	t.Helper()

	store, service := newTestSubscriptionService(t)
	cache := NewResultCache()
	handler := NewServer(ServerDeps{
		Config:              service.config,
		Store:               store,
		Cache:               cache,
		SubscriptionService: service,
		CheckService:        NewCheckService(store, cache, service.config),
		StaticFS:            os.DirFS("static"),
	})
	return store, service, handler
}

func mustFileURL(t *testing.T, filePath string) string {
	t.Helper()
	return (&url.URL{Scheme: "file", Path: filePath}).String()
}

func TestLoadSubscriptionNodesEmptyClashConfigReturnsEmptySubscriptionError(t *testing.T) {
	_, service := newTestSubscriptionService(t)

	filePath := filepath.Join(t.TempDir(), "empty-clash.yaml")
	content := "mixed-port: 7890\nproxies: []\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	result, apiErr := service.loadSubscriptionNodes(mustFileURL(t, filePath))
	if apiErr.Code != "empty_subscription" || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("unexpected api error: %+v", apiErr)
	}
	if result.Format != "clash_yaml" || result.ImportedCount != 0 || result.SkippedCount != 0 {
		t.Fatalf("unexpected import result: %+v", result)
	}
}

func TestSubscriptionsHandlerReturnsReadOnlyStatusView(t *testing.T) {
	store, service, handler := newTestServer(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := mustFileURL(t, filepath.Join(wd, "samples", "sample-clash.yaml"))
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}}); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/subscriptions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Subscriptions []Subscription `json:"subscriptions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(payload.Subscriptions) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(payload.Subscriptions))
	}
	if payload.Subscriptions[0].Status != "ok" {
		t.Fatalf("unexpected status payload: %+v", payload.Subscriptions[0])
	}
	if payload.Subscriptions[0].LastRefreshedAt == nil {
		t.Fatalf("expected last_refreshed_at in payload: %+v", payload.Subscriptions[0])
	}
	if payload.Subscriptions[0].URL != "file://.../sample-clash.yaml" {
		t.Fatalf("expected masked file URL, got %q", payload.Subscriptions[0].URL)
	}

	httpSubscription, err := store.CreateSubscription("tokenized", "https://example.com/subscription?token=super-secret")
	if err != nil {
		t.Fatalf("CreateSubscription error: %v", err)
	}
	if err := store.MarkSubscriptionSuccess(httpSubscription.ID); err != nil {
		t.Fatalf("MarkSubscriptionSuccess error: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/subscriptions", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	foundMaskedHTTP := false
	for _, subscription := range payload.Subscriptions {
		if subscription.Name != "tokenized" {
			continue
		}
		foundMaskedHTTP = true
		if strings.Contains(subscription.URL, "super-secret") {
			t.Fatalf("expected token to be hidden, got %q", subscription.URL)
		}
		if subscription.URL != "https://example.com/[hidden]" {
			t.Fatalf("expected masked HTTP URL, got %q", subscription.URL)
		}
	}
	if !foundMaskedHTTP {
		t.Fatal("expected masked HTTP subscription in response")
	}
}

func TestSubscriptionMutationEndpointsAreRemoved(t *testing.T) {
	_, _, handler := newTestServer(t)

	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/subscriptions"},
		{method: http.MethodDelete, path: "/api/subscriptions/1"},
		{method: http.MethodPut, path: "/api/subscriptions/1/refresh"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, body = %s", tt.method, tt.path, rec.Code, rec.Body.String())
		}
	}
}

func TestNodesHandlerDoesNotExposeExtraParams(t *testing.T) {
	_, service, handler := newTestServer(t)

	filePath := filepath.Join(t.TempDir(), "secret.yaml")
	content := `
proxies:
  - name: secret-node
    type: ss
    server: 203.0.113.1
    port: 8388
    cipher: chacha20-ietf-poly1305
    password: super-secret-password
`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "secret", URL: mustFileURL(t, filePath)}}); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "extra_params") || strings.Contains(body, "super-secret-password") {
		t.Fatalf("node response exposed sensitive params: %s", body)
	}
}
