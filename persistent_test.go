package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestFakeMihomoHelper is not a real test: when the parent spawns the test
// binary with GO_FAKE_MIHOMO=1 it stands in for a mihomo process, reading the
// generated config and serving a tiny external controller (/proxies +
// /proxies/{name}/delay) so the persistent manager can be exercised without a
// real mihomo binary. Run normally it returns immediately.
func TestFakeMihomoHelper(t *testing.T) {
	if os.Getenv("GO_FAKE_MIHOMO") != "1" {
		return
	}

	behavior := os.Getenv("GO_FAKE_MIHOMO_BEHAVIOR")
	if behavior == "crash" {
		os.Exit(1)
	}

	configPath := ""
	args := os.Args
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-f" {
			configPath = args[i+1]
		}
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		os.Exit(2)
	}
	var cfg struct {
		ExternalController string           `yaml:"external-controller"`
		Secret             string           `yaml:"secret"`
		Proxies            []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		os.Exit(3)
	}

	names := []string{"GLOBAL", "DIRECT"}
	for index, proxy := range cfg.Proxies {
		// "missing-one" drops the first proxy so the manager's reconcile must
		// isolate that node.
		if behavior == "missing-one" && index == 0 {
			continue
		}
		if name, ok := proxy["name"].(string); ok {
			names = append(names, name)
		}
	}

	authOK := func(r *http.Request) bool {
		if cfg.Secret == "" {
			return true
		}
		return r.Header.Get("Authorization") == "Bearer "+cfg.Secret
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/proxies", func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		proxies := map[string]any{}
		for _, name := range names {
			proxies[name] = map[string]any{"name": name}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"proxies": proxies})
	})
	mux.HandleFunc("/proxies/", func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"delay": 42})
	})

	srv := &http.Server{Addr: cfg.ExternalController, Handler: mux}
	go func() { _ = srv.ListenAndServe() }()
	// Block until the parent kills us; bounded so a leaked helper can't linger.
	time.Sleep(60 * time.Second)
	os.Exit(0)
}

// newFakeMihomoRunner builds a persistent runner whose spawned "mihomo" is the
// fake helper above, and returns a teardown that restores the exec shim and
// stops the runner.
func newFakeMihomoRunner(t *testing.T, behavior string) (*MihomoDelayRunner, func()) {
	t.Helper()
	original := execCommandContext
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=TestFakeMihomoHelper", "--", name}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), "GO_FAKE_MIHOMO=1", "GO_FAKE_MIHOMO_BEHAVIOR="+behavior)
		return cmd
	}
	runner := &MihomoDelayRunner{
		path:         "fake-mihomo",
		delayURLs:    []string{"https://example.com/generate_204"},
		startTimeout: 4 * time.Second,
		concurrency:  4,
		warmup:       false,
		instances:    map[string]*mihomoInstance{},
		backoff:      map[string]*mihomoBackoff{},
	}
	return runner, func() {
		_ = runner.Close()
		execCommandContext = original
	}
}

func fakeNodes(ids ...int) []NodeRecord {
	nodes := make([]NodeRecord, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, NodeRecord{
			ID:          id,
			Name:        "node",
			Server:      "203.0.113.1",
			Port:        443,
			Protocol:    "trojan",
			ExtraParams: map[string]any{"password": "secret"},
		})
	}
	return nodes
}

func instanceGeneration(r *MihomoDelayRunner, key string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.instances[key]
	if !ok {
		return 0, false
	}
	return inst.generation, true
}

func TestPersistentRunnerProbesAndReusesInstance(t *testing.T) {
	runner, teardown := newFakeMihomoRunner(t, "ok")
	defer teardown()

	nodes := fakeNodes(1, 2)
	outcome := runner.Check(nodes, time.Second)
	if !outcome.EngineAvailable {
		t.Fatalf("engine should be available, got %+v", outcome)
	}
	for _, node := range nodes {
		if got := outcome.Results[node.ID]; got.Status != "online" || got.LatencyMS == nil || *got.LatencyMS != 42 {
			t.Fatalf("node %d result = %+v, want online/42", node.ID, got)
		}
	}

	gen1, ok := instanceGeneration(runner, "")
	if !ok {
		t.Fatal("expected a persistent instance after first check")
	}

	// Second check with the same nodes must REUSE the running process, not
	// restart it — so the generation stays the same.
	outcome = runner.Check(nodes, time.Second)
	if !outcome.EngineAvailable {
		t.Fatalf("engine should still be available on reuse, got %+v", outcome)
	}
	gen2, ok := instanceGeneration(runner, "")
	if !ok {
		t.Fatal("expected the persistent instance to still exist")
	}
	if gen1 != gen2 {
		t.Fatalf("instance was restarted (gen %d -> %d); expected reuse", gen1, gen2)
	}
}

func TestPersistentRunnerIsolatesMissingProxy(t *testing.T) {
	runner, teardown := newFakeMihomoRunner(t, "missing-one")
	defer teardown()

	nodes := fakeNodes(1, 2)
	outcome := runner.Check(nodes, time.Second)
	if !outcome.EngineAvailable {
		t.Fatalf("engine should be available (one node still loaded), got %+v", outcome)
	}
	// Node 1's proxy was dropped by the controller, so it must be isolated, not
	// silently reported as offline.
	if got := outcome.Results[1]; got.Status != "unknown" || !strings.Contains(got.Message, "已隔离") {
		t.Fatalf("node 1 = %+v, want isolated unknown", got)
	}
	if got := outcome.Results[2]; got.Status != "online" {
		t.Fatalf("node 2 = %+v, want online", got)
	}
}

func TestPersistentRunnerReportsEngineUnavailableOnCrash(t *testing.T) {
	runner, teardown := newFakeMihomoRunner(t, "crash")
	defer teardown()

	nodes := fakeNodes(1, 2)
	outcome := runner.Check(nodes, time.Second)
	if outcome.EngineAvailable {
		t.Fatalf("engine should be reported unavailable when mihomo crashes, got %+v", outcome)
	}
	for _, node := range nodes {
		if got := outcome.Results[node.ID]; got.Status != "unknown" {
			t.Fatalf("node %d = %+v, want unknown when engine down", node.ID, got)
		}
	}
}

func TestPersistentRunnerCloseStopsInstances(t *testing.T) {
	runner, teardown := newFakeMihomoRunner(t, "ok")
	defer teardown()

	if outcome := runner.Check(fakeNodes(1), time.Second); !outcome.EngineAvailable {
		t.Fatalf("engine should be available, got %+v", outcome)
	}
	if _, ok := instanceGeneration(runner, ""); !ok {
		t.Fatal("expected a running instance before Close")
	}
	_ = runner.Close()
	runner.mu.Lock()
	remaining := len(runner.instances)
	runner.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("Close left %d instances running", remaining)
	}
}

// TestRealMihomoIntegration exercises the whole persistent path against a real
// mihomo binary when one is installed; otherwise it is skipped. The node points
// at an unreachable server, so the verdict is offline/timeout — the point is to
// prove the engine actually ran (a real delay verdict, not unknown).
func TestRealMihomoIntegration(t *testing.T) {
	if os.Getenv("RUN_REAL_MIHOMO") != "1" {
		t.Skip("set RUN_REAL_MIHOMO=1 to run the real-mihomo integration test (makes real network calls)")
	}
	path := findMihomoExecutable()
	if path == "" {
		t.Skip("no mihomo/clash-meta/clash binary on PATH; skipping real integration")
	}
	runner := &MihomoDelayRunner{
		path:         path,
		delayURLs:    []string{"https://www.gstatic.com/generate_204"},
		startTimeout: 8 * time.Second,
		concurrency:  2,
		warmup:       false,
		instances:    map[string]*mihomoInstance{},
		backoff:      map[string]*mihomoBackoff{},
	}
	defer func() { _ = runner.Close() }()

	nodes := []NodeRecord{{
		ID:          1,
		Name:        "unreachable",
		Server:      "203.0.113.1",
		Port:        443,
		Protocol:    "trojan",
		ExtraParams: map[string]any{"password": "secret"},
	}}
	outcome := runner.Check(nodes, 3*time.Second)
	if !outcome.EngineAvailable {
		t.Fatalf("real mihomo should report engine available, got %+v", outcome)
	}
	got := outcome.Results[1]
	switch got.Status {
	case "online", "offline", "timeout":
		// engine produced a real verdict — good
	default:
		t.Fatalf("expected a real delay verdict from mihomo, got %+v", got)
	}
}
