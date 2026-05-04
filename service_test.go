package main

import (
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

type timeoutStubError struct{}

func (timeoutStubError) Error() string   { return "timeout" }
func (timeoutStubError) Timeout() bool   { return true }
func (timeoutStubError) Temporary() bool { return false }

var _ net.Error = timeoutStubError{}
