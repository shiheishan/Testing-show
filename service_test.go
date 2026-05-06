package main

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestSubscriptionService(t *testing.T) (*Store, *SubscriptionService) {
	t.Helper()

	store, err := NewStore(filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	service := NewSubscriptionService(store, Config{
		SubscriptionUA:      "nodepanel-test",
		SubscriptionTimeout: 2 * time.Second,
		CheckConcurrency:    1,
		CheckTimeout:        time.Second,
		CheckRetention:      time.Hour,
	})

	return store, service
}

func requireAPIError(t *testing.T, err error) APIError {
	t.Helper()

	apiErr, ok := err.(APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T (%v)", err, err)
	}
	return apiErr
}

func TestAddSubscriptionRejectsInvalidURLWithoutPersisting(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	_, _, _, err := service.AddSubscription("abc", "")
	if err == nil {
		t.Fatal("expected error")
	}

	apiErr := requireAPIError(t, err)
	if apiErr.Code != "bad_request" || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("unexpected api error: %+v", apiErr)
	}
	if !strings.Contains(apiErr.Message, "订阅 URL 非法") {
		t.Fatalf("unexpected error message: %q", apiErr.Message)
	}

	subscriptions, err := store.ListSubscriptions()
	if err != nil {
		t.Fatalf("ListSubscriptions error: %v", err)
	}
	if len(subscriptions) != 0 {
		t.Fatalf("expected no subscriptions to persist, got %d", len(subscriptions))
	}
}

func TestRefreshSubscriptionMarksInvalidURLAsBadRequest(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	item, err := store.CreateSubscription("bad", "abc")
	if err != nil {
		t.Fatalf("CreateSubscription error: %v", err)
	}

	_, _, err = service.RefreshSubscription(item.ID)
	if err == nil {
		t.Fatal("expected error")
	}

	apiErr := requireAPIError(t, err)
	if apiErr.Code != "bad_request" || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("unexpected api error: %+v", apiErr)
	}

	updated, err := store.GetSubscription(item.ID)
	if err != nil {
		t.Fatalf("GetSubscription error: %v", err)
	}
	if updated == nil || updated.LastError == nil {
		t.Fatal("expected last_error to be recorded")
	}
	if !strings.Contains(*updated.LastError, "订阅 URL 非法") {
		t.Fatalf("unexpected last_error: %q", *updated.LastError)
	}
}

func TestAddSubscriptionFromLocalFileDerivesNameAndParsesNodes(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.Join(wd, "samples", "sample-clash.yaml"),
	}).String()

	item, stats, result, err := service.AddSubscription(fileURL, "")
	if err != nil {
		t.Fatalf("AddSubscription error: %v", err)
	}

	if item.Name != "sample-clash" {
		t.Fatalf("expected derived name sample-clash, got %q", item.Name)
	}
	if stats.Total != 3 || stats.Created != 3 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if result.ImportedCount != 3 || result.SkippedCount != 0 {
		t.Fatalf("unexpected import result: %+v", result)
	}

	nodes, err := store.ListNodes(&item.ID, true)
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}

	subscription, err := store.GetSubscription(item.ID)
	if err != nil {
		t.Fatalf("GetSubscription error: %v", err)
	}
	if subscription == nil || subscription.LastRefreshedAt == nil {
		t.Fatal("expected last_refreshed_at to be set")
	}
	if subscription.LastError != nil {
		t.Fatalf("expected last_error to be nil, got %q", *subscription.LastError)
	}
}

func TestLoadSubscriptionNodesAllowsPartialSuccess(t *testing.T) {
	_, service := newTestSubscriptionService(t)

	filePath := filepath.Join(t.TempDir(), "partial.yaml")
	content := `
proxies:
  - name: ok
    type: trojan
    server: trojan.example.com
    port: 443
    password: secret
  - name: broken
    type: vmess
    server: broken.example.com
`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	fileURL := (&url.URL{Scheme: "file", Path: filePath}).String()
	result, apiErr := service.loadSubscriptionNodes(fileURL)
	if apiErr.Status != 0 {
		t.Fatalf("unexpected api error: %+v", apiErr)
	}
	if result.ImportedCount != 1 || result.SkippedCount != 1 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	if result.Summary != "已导入 1 个节点，跳过 1 个（1 个缺少 server 或 port）" {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
}

func TestLoadSubscriptionNodesPlainTextDoesNotInventSkippedEntries(t *testing.T) {
	_, service := newTestSubscriptionService(t)

	filePath := filepath.Join(t.TempDir(), "plain.txt")
	content := "this is not a subscription\njust regular text\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	fileURL := (&url.URL{Scheme: "file", Path: filePath}).String()
	_, apiErr := service.loadSubscriptionNodes(fileURL)
	if apiErr.Status != http.StatusBadRequest {
		t.Fatalf("unexpected api error: %+v", apiErr)
	}
	if strings.Contains(apiErr.Message, "跳过") {
		t.Fatalf("unexpected message: %q", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, "订阅内容里没有可识别的节点") {
		t.Fatalf("unexpected message: %q", apiErr.Message)
	}
}

func TestLoadSubscriptionNodesDocumentationLinksDoNotInventSkippedEntries(t *testing.T) {
	_, service := newTestSubscriptionService(t)

	filePath := filepath.Join(t.TempDir(), "docs.txt")
	content := "https://example.com/subscription\nhttp://127.0.0.1:8080/api/subscriptions\nfile:///tmp/sample.yaml\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	fileURL := (&url.URL{Scheme: "file", Path: filePath}).String()
	_, apiErr := service.loadSubscriptionNodes(fileURL)
	if apiErr.Status != http.StatusBadRequest {
		t.Fatalf("unexpected api error: %+v", apiErr)
	}
	if strings.Contains(apiErr.Message, "跳过") {
		t.Fatalf("unexpected message: %q", apiErr.Message)
	}
}

func TestUpsertNodesPreservesSubscriptionOrder(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	subscription, err := store.CreateSubscription("order", "file:///tmp/order.yaml")
	if err != nil {
		t.Fatalf("CreateSubscription error: %v", err)
	}

	first := []ParsedNode{
		{Name: "Charlie", Server: "charlie.example.com", Port: 443, Protocol: "trojan", ExtraParams: map[string]any{"password": "secret"}},
		{Name: "Alpha", Server: "alpha.example.com", Port: 443, Protocol: "trojan", ExtraParams: map[string]any{"password": "secret"}},
		{Name: "Bravo", Server: "bravo.example.com", Port: 443, Protocol: "trojan", ExtraParams: map[string]any{"password": "secret"}},
	}
	if _, err := store.UpsertNodes(subscription.ID, first); err != nil {
		t.Fatalf("UpsertNodes first error: %v", err)
	}
	nodes, err := store.ListNodes(&subscription.ID, false)
	if err != nil {
		t.Fatalf("ListNodes first error: %v", err)
	}
	assertNodeOrder(t, nodes, []string{"Charlie", "Alpha", "Bravo"})

	second := []ParsedNode{first[2], first[0], first[1]}
	if _, err := store.UpsertNodes(subscription.ID, second); err != nil {
		t.Fatalf("UpsertNodes second error: %v", err)
	}
	nodes, err = store.ListNodes(&subscription.ID, false)
	if err != nil {
		t.Fatalf("ListNodes second error: %v", err)
	}
	assertNodeOrder(t, nodes, []string{"Bravo", "Charlie", "Alpha"})
	for index, node := range nodes {
		if node.DisplayOrder != index {
			t.Fatalf("node %s display_order = %d, want %d", node.Name, node.DisplayOrder, index)
		}
	}
}

func TestStoreMigratesDisplayOrderBeforeCreatingOrderIndex(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	legacy := &Store{dbPath: dbPath}
	if err := legacy.execSQL(`
CREATE TABLE subscriptions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	url TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	last_refreshed_at TEXT,
	last_error TEXT
);
CREATE TABLE nodes (
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
CREATE TABLE check_results (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	node_id INTEGER NOT NULL,
	status TEXT NOT NULL,
	latency_ms INTEGER,
	checked_at TEXT NOT NULL,
	FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);
INSERT INTO subscriptions (id, name, url, created_at) VALUES (1, 'legacy', 'file:///tmp/legacy.yaml', '2026-01-01T00:00:00Z');
INSERT INTO nodes (id, subscription_id, name, server, port, protocol, extra_params, created_at, updated_at)
VALUES (7, 1, 'Legacy', 'legacy.example.com', 443, 'trojan', '{}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
`); err != nil {
		t.Fatalf("create legacy schema error: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore migration error: %v", err)
	}
	nodes, err := store.ListNodes(nil, false)
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}
	if len(nodes) != 1 || nodes[0].DisplayOrder != 7 {
		t.Fatalf("unexpected migrated nodes: %+v", nodes)
	}

	var indexes []struct {
		Name string `json:"name"`
	}
	if err := store.queryJSON(&indexes, `PRAGMA index_list(nodes);`); err != nil {
		t.Fatalf("index list error: %v", err)
	}
	for _, index := range indexes {
		if index.Name == "idx_nodes_subscription_order" {
			return
		}
	}
	t.Fatalf("idx_nodes_subscription_order was not created: %+v", indexes)
}

func assertNodeOrder(t *testing.T, nodes []NodeRecord, want []string) {
	t.Helper()
	if len(nodes) != len(want) {
		t.Fatalf("nodes = %d, want %d", len(nodes), len(want))
	}
	for index, name := range want {
		if nodes[index].Name != name {
			t.Fatalf("node %d = %q, want %q; got %+v", index, nodes[index].Name, name, nodes)
		}
	}
}

func TestDeleteSubscriptionRemovesNodes(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.Join(wd, "samples", "sample-clash.yaml"),
	}).String()

	item, _, _, err := service.AddSubscription(fileURL, "")
	if err != nil {
		t.Fatalf("AddSubscription error: %v", err)
	}

	deleted, err := store.DeleteSubscription(item.ID)
	if err != nil {
		t.Fatalf("DeleteSubscription error: %v", err)
	}
	if !deleted {
		t.Fatal("expected subscription to be deleted")
	}

	nodes, err := store.ListNodes(nil, true)
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected nodes to be deleted with subscription, got %d", len(nodes))
	}
}

func TestSyncConfiguredSubscriptionsImportsAndPrunesByURL(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.Join(wd, "samples", "sample-clash.yaml"),
	}).String()

	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}}); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
	}

	subscriptions, err := store.ListSubscriptions()
	if err != nil {
		t.Fatalf("ListSubscriptions error: %v", err)
	}
	if len(subscriptions) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subscriptions))
	}
	if subscriptions[0].Status != "ok" {
		t.Fatalf("unexpected subscription status: %+v", subscriptions[0])
	}

	nodes, err := store.ListNodes(nil, true)
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}

	if err := service.SyncConfiguredSubscriptions(nil); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions empty error: %v", err)
	}

	subscriptions, err = store.ListSubscriptions()
	if err != nil {
		t.Fatalf("ListSubscriptions error: %v", err)
	}
	if len(subscriptions) != 0 {
		t.Fatalf("expected subscriptions to be pruned, got %d", len(subscriptions))
	}
}

func TestSyncConfiguredSubscriptionsKeepsSnapshotOnRefreshFailure(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	workingPath := filepath.Join(t.TempDir(), "working.yaml")
	workingContent := `
proxies:
  - name: ok
    type: trojan
    server: trojan.example.com
    port: 443
    password: secret
`
	if err := os.WriteFile(workingPath, []byte(workingContent), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	fileURL := (&url.URL{Scheme: "file", Path: workingPath}).String()
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}}); err != nil {
		t.Fatalf("initial SyncConfiguredSubscriptions error: %v", err)
	}

	if err := os.Remove(workingPath); err != nil {
		t.Fatalf("Remove error: %v", err)
	}

	err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}})
	if err == nil {
		t.Fatal("expected refresh failure")
	}

	subscriptions, err := store.ListSubscriptions()
	if err != nil {
		t.Fatalf("ListSubscriptions error: %v", err)
	}
	if len(subscriptions) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subscriptions))
	}
	if subscriptions[0].Status != "failed" || subscriptions[0].LastError == nil {
		t.Fatalf("expected failed subscription with last_error, got %+v", subscriptions[0])
	}

	nodes, err := store.ListNodes(nil, true)
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected previous node snapshot to remain, got %d", len(nodes))
	}
}

func TestSyncConfiguredSubscriptionsKeepsSnapshotWhenURLChanges(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	workingPath := filepath.Join(t.TempDir(), "working.yaml")
	workingContent := `
proxies:
  - name: ok
    type: trojan
    server: trojan.example.com
    port: 443
    password: secret
`
	if err := os.WriteFile(workingPath, []byte(workingContent), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	originalURL := (&url.URL{Scheme: "file", Path: workingPath}).String()
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: originalURL}}); err != nil {
		t.Fatalf("initial SyncConfiguredSubscriptions error: %v", err)
	}

	rotatedURL := (&url.URL{Scheme: "file", Path: filepath.Join(t.TempDir(), "rotated.yaml")}).String()
	err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: rotatedURL}})
	if err == nil {
		t.Fatal("expected refresh failure after URL rotation")
	}

	subscriptions, err := store.ListSubscriptions()
	if err != nil {
		t.Fatalf("ListSubscriptions error: %v", err)
	}
	if len(subscriptions) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subscriptions))
	}
	if subscriptions[0].URL != rotatedURL {
		t.Fatalf("expected URL to rotate in-place, got %q", subscriptions[0].URL)
	}
	if subscriptions[0].Status != "failed" || subscriptions[0].LastError == nil {
		t.Fatalf("expected failed subscription with last_error, got %+v", subscriptions[0])
	}

	nodes, err := store.ListNodes(nil, true)
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected previous node snapshot to remain after URL change, got %d", len(nodes))
	}
}

func TestRunCheckWaitsForSubscriptionSyncWindow(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.Join(wd, "samples", "sample-clash.yaml"),
	}).String()
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}}); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
	}

	cache := NewResultCache()
	checkService := NewCheckService(store, cache, service.config)

	started := make(chan struct{})
	release := make(chan struct{})
	originalDial := netDialTimeout
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return nil, timeoutStubError{}
	}
	defer func() {
		netDialTimeout = originalDial
	}()

	checkDone := make(chan error, 1)
	go func() {
		_, err := checkService.RunCheck(nil, nil)
		checkDone <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("check did not start dialing")
	}

	syncDone := make(chan error, 1)
	go func() {
		syncDone <- service.SyncConfiguredSubscriptions(nil)
	}()

	select {
	case err := <-syncDone:
		t.Fatalf("sync should wait for check to finish, got %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-checkDone:
		if err != nil {
			t.Fatalf("RunCheck error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("check did not finish")
	}

	select {
	case err := <-syncDone:
		if err != nil {
			t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not finish")
	}
}

func TestStartAsyncCheckReusesRunningCheck(t *testing.T) {
	store, service := newTestSubscriptionService(t)
	service.config.ManualCheckCooldown = 15 * time.Second

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.Join(wd, "samples", "sample-clash.yaml"),
	}).String()
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}}); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
	}

	cache := NewResultCache()
	checkService := NewCheckService(store, cache, service.config)

	started := make(chan struct{})
	release := make(chan struct{})
	originalDial := netDialTimeout
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return nil, timeoutStubError{}
	}
	defer func() {
		netDialTimeout = originalDial
	}()

	first, err := checkService.StartAsyncCheck(nil, nil)
	if err != nil {
		t.Fatalf("StartAsyncCheck first error: %v", err)
	}
	if first.Status != "started" {
		t.Fatalf("first status = %q, want started", first.Status)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("check did not start dialing")
	}

	second, err := checkService.StartAsyncCheck(nil, nil)
	if err != nil {
		t.Fatalf("StartAsyncCheck second error: %v", err)
	}
	if second.Status != "running" {
		t.Fatalf("second status = %q, want running", second.Status)
	}
	close(release)
}

func TestStartAsyncCheckUsesManualCooldown(t *testing.T) {
	store, service := newTestSubscriptionService(t)
	service.config.ManualCheckCooldown = time.Minute

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.Join(wd, "samples", "sample-clash.yaml"),
	}).String()
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}}); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
	}

	cache := NewResultCache()
	checkService := NewCheckService(store, cache, service.config)

	originalDial := netDialTimeout
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, timeoutStubError{}
	}
	defer func() {
		netDialTimeout = originalDial
	}()

	first, err := checkService.StartAsyncCheck(nil, nil)
	if err != nil {
		t.Fatalf("StartAsyncCheck first error: %v", err)
	}
	if first.Status != "started" {
		t.Fatalf("first status = %q, want started", first.Status)
	}

	deadline := time.After(2 * time.Second)
	for {
		checkService.mu.Lock()
		active := checkService.active
		checkService.mu.Unlock()
		if !active {
			break
		}
		select {
		case <-deadline:
			t.Fatal("check did not finish")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	second, err := checkService.StartAsyncCheck(nil, nil)
	if err != nil {
		t.Fatalf("StartAsyncCheck second error: %v", err)
	}
	if second.Status != "cached" {
		t.Fatalf("second status = %q, want cached", second.Status)
	}
}

func TestRunCheckUsesProxyStatusAsPrimaryWhenTransportTimesOut(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.Join(wd, "samples", "sample-clash.yaml"),
	}).String()
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}}); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
	}
	nodes, err := store.ListNodes(nil, false)
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected sample nodes")
	}

	cache := NewResultCache()
	checkService := NewCheckService(store, cache, service.config)
	proxyLatency := 123
	checkService.proxyRunner = stubProxyRunner{
		results: map[int]ProbeResult{
			nodes[0].ID: {Status: "online", LatencyMS: &proxyLatency},
		},
	}

	originalDial := netDialTimeout
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, timeoutStubError{}
	}
	defer func() {
		netDialTimeout = originalDial
	}()

	results, err := checkService.RunCheck(nil, &nodes[0].ID)
	if err != nil {
		t.Fatalf("RunCheck error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	got := results[0]
	if got.Status != "online" || got.StatusSource != "proxy" {
		t.Fatalf("status/source = %s/%s, want online/proxy", got.Status, got.StatusSource)
	}
	if got.LatencyMS == nil || *got.LatencyMS != proxyLatency {
		t.Fatalf("latency = %v, want %d", got.LatencyMS, proxyLatency)
	}
	if got.TransportStatus != "timeout" {
		t.Fatalf("transport status = %s, want timeout", got.TransportStatus)
	}

	state, ok := cache.Get(nodes[0].ID)
	if !ok {
		t.Fatal("expected cache state")
	}
	if state.Status != "online" || state.TransportStatus != "timeout" || state.ProxyStatus != "online" {
		t.Fatalf("unexpected cached state: %+v", state)
	}
}

func TestRunCheckIncludesTransportFailureWhenProxyFails(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.Join(wd, "samples", "sample-clash.yaml"),
	}).String()
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}}); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
	}
	nodes, err := store.ListNodes(nil, false)
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected sample nodes")
	}

	cache := NewResultCache()
	checkService := NewCheckService(store, cache, service.config)
	checkService.proxyRunner = stubProxyRunner{
		results: map[int]ProbeResult{
			nodes[0].ID: {Status: "offline", Message: "503 Service Unavailable: delay failed"},
		},
	}

	originalDial := netDialTimeout
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, timeoutStubError{}
	}
	defer func() {
		netDialTimeout = originalDial
	}()

	results, err := checkService.RunCheck(nil, &nodes[0].ID)
	if err != nil {
		t.Fatalf("RunCheck error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	got := results[0]
	if got.Status != "offline" || got.StatusSource != "proxy" {
		t.Fatalf("status/source = %s/%s, want offline/proxy", got.Status, got.StatusSource)
	}
	if got.StatusMessage == nil {
		t.Fatal("expected status message")
	}
	if !strings.Contains(*got.StatusMessage, "delay failed") || !strings.Contains(*got.StatusMessage, "入口探活") || !strings.Contains(*got.StatusMessage, "timeout") {
		t.Fatalf("message = %q, want proxy and transport failure detail", *got.StatusMessage)
	}
}

func TestRunCheckFallsBackToTransportWhenProxyIsUnknown(t *testing.T) {
	store, service := newTestSubscriptionService(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	fileURL := (&url.URL{
		Scheme: "file",
		Path:   filepath.Join(wd, "samples", "sample-clash.yaml"),
	}).String()
	if err := service.SyncConfiguredSubscriptions([]ConfiguredSubscription{{Name: "sample", URL: fileURL}}); err != nil {
		t.Fatalf("SyncConfiguredSubscriptions error: %v", err)
	}
	nodes, err := store.ListNodes(nil, false)
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected sample nodes")
	}

	cache := NewResultCache()
	checkService := NewCheckService(store, cache, service.config)
	checkService.proxyRunner = stubProxyRunner{
		results: map[int]ProbeResult{
			nodes[0].ID: {Status: "unknown", Message: "mihomo unavailable"},
		},
	}

	originalDial := netDialTimeout
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, timeoutStubError{}
	}
	defer func() {
		netDialTimeout = originalDial
	}()

	results, err := checkService.RunCheck(nil, &nodes[0].ID)
	if err != nil {
		t.Fatalf("RunCheck error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	got := results[0]
	if got.Status != "timeout" || got.StatusSource != "transport" {
		t.Fatalf("status/source = %s/%s, want timeout/transport", got.Status, got.StatusSource)
	}
	if got.ProxyStatus != "unknown" {
		t.Fatalf("proxy status = %s, want unknown", got.ProxyStatus)
	}
}

func TestCheckTransportUsesSubscriptionDoHForEntryProbe(t *testing.T) {
	store, service := newTestSubscriptionService(t)
	cache := NewResultCache()
	checkService := NewCheckService(store, cache, service.config)

	node := NodeRecord{
		ID:       1,
		Name:     "DNS Node",
		Server:   "sanwang.woainilzr.com",
		Port:     49501,
		Protocol: "anytls",
		ExtraParams: map[string]any{
			"_mihomo_dns": map[string]any{
				"nameserver": []any{"https://dns.alidns.com/dns-query"},
			},
		},
	}

	originalTransport := httpRoundTripper
	httpRoundTripper = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(`{
				"Status": 0,
				"Answer": [
					{"type": 5, "data": "sanwang.woainiliz.com."},
					{"type": 1, "data": "13.231.111.214"}
				]
			}`)),
			Request: r,
		}, nil
	})
	defer func() {
		httpRoundTripper = originalTransport
	}()

	var dialedAddress string
	originalDial := netDialTimeout
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		dialedAddress = address
		return nil, timeoutStubError{}
	}
	defer func() {
		netDialTimeout = originalDial
	}()

	result := checkService.checkTransport(node)
	if dialedAddress != "13.231.111.214:49501" {
		t.Fatalf("dialed address = %q, want resolved IP", dialedAddress)
	}
	if result.Status != "timeout" {
		t.Fatalf("status = %s, want timeout", result.Status)
	}
}

type stubProxyRunner struct {
	results map[int]ProbeResult
	err     error
}

func (r stubProxyRunner) Check(nodes []NodeRecord, timeout time.Duration) (map[int]ProbeResult, error) {
	return r.results, r.err
}

type timeoutStubError struct{}

func (timeoutStubError) Error() string   { return "timeout" }
func (timeoutStubError) Timeout() bool   { return true }
func (timeoutStubError) Temporary() bool { return false }

var _ net.Error = timeoutStubError{}
