package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	mihomoMinRestartBackoff = 500 * time.Millisecond
	mihomoMaxRestartBackoff = 30 * time.Second
	mihomoIsolatedMessage   = "节点未被测速引擎加载（配置可能无效），已隔离"
)

// MihomoDelayRunner keeps one long-lived mihomo process per DNS group (a
// persistent external controller) and issues /proxies/{name}/delay queries
// against it. Each Check reuses the running instances, restarting only when the
// group's proxy/DNS config changes or the process has died. Groups that refuse
// to start persistently fall back to the ephemeral per-check path so a single
// bad node can still be isolated (folded into the persistent path by T8).
type MihomoDelayRunner struct {
	path         string
	delayURLs    []string
	startTimeout time.Duration
	concurrency  int
	warmup       bool

	mu        sync.Mutex
	instances map[string]*mihomoInstance
	backoff   map[string]*mihomoBackoff
	nextGen   uint64
	closed    bool
}

// mihomoBackoff rate-limits persistent restarts for one DNS group so a
// crash-looping config does not get re-spawned on every manual check (T5).
type mihomoBackoff struct {
	failures    int
	nextAttempt time.Time
}

// mihomoInstance is a single long-lived mihomo process serving one DNS group.
// All mutable fields are written only under MihomoDelayRunner.mu (no background
// supervisor mutates them), so probeInstance can read baseURL/secret/loaded
// without a per-instance lock once ensureInstance has returned the instance.
type mihomoInstance struct {
	key        string
	generation uint64
	configHash string
	secret     string
	tempDir    string
	baseURL    string
	loaded     map[int]string // nodeID -> proxy name actually loaded by the controller (T8 reconcile)
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	done       chan struct{}

	deadMu sync.Mutex
	dead   bool
}

type unavailableProxyDelayRunner struct {
	message string
}

type mihomoProxyCandidate struct {
	nodeID int
	name   string
	proxy  map[string]any
	dns    map[string]any
}

func NewProxyDelayRunner(config Config) ProxyDelayRunner {
	if !config.ProxyCheckEnabled {
		return unavailableProxyDelayRunner{message: "真实代理测速已关闭（proxy_enabled: false），测速引擎不可用"}
	}
	path := strings.TrimSpace(config.MihomoPath)
	if path == "" {
		path = findMihomoExecutable()
	}
	if path == "" {
		return unavailableProxyDelayRunner{message: "未找到 mihomo、clash-meta 或 clash，可安装 Mihomo 或配置 check.mihomo_path 启用真实代理测速"}
	}
	delayURLs := normalizeProxyCheckURLs(config.ProxyCheckURL, config.ProxyCheckURLs)
	startTimeout := config.MihomoStartTimeout
	if startTimeout <= 0 {
		startTimeout = 8 * time.Second
	}
	concurrency := config.ProxyCheckConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	return &MihomoDelayRunner{
		path:         path,
		delayURLs:    delayURLs,
		startTimeout: startTimeout,
		concurrency:  concurrency,
		warmup:       config.ProxyCheckWarmup,
		instances:    map[string]*mihomoInstance{},
		backoff:      map[string]*mihomoBackoff{},
	}
}

func (r unavailableProxyDelayRunner) Check(nodes []NodeRecord, timeout time.Duration) ProxyCheckOutcome {
	results := make(map[int]ProbeResult, len(nodes))
	for _, node := range nodes {
		results[node.ID] = ProbeResult{Status: "unknown", Message: r.message}
	}
	return ProxyCheckOutcome{Results: results, EngineAvailable: false}
}

func findMihomoExecutable() string {
	for _, name := range []string{"mihomo", "clash-meta", "clash"} {
		path, err := execLookPath(name)
		if err == nil && strings.TrimSpace(path) != "" {
			return path
		}
	}
	return ""
}

func (r *MihomoDelayRunner) Check(nodes []NodeRecord, timeout time.Duration) ProxyCheckOutcome {
	results := make(map[int]ProbeResult, len(nodes))
	candidates := make([]mihomoProxyCandidate, 0, len(nodes))
	for _, node := range nodes {
		proxy, err := nodeToMihomoProxy(node)
		if err != nil {
			results[node.ID] = ProbeResult{Status: "unknown", Message: err.Error()}
			continue
		}
		candidates = append(candidates, mihomoProxyCandidate{
			nodeID: node.ID,
			name:   asString(proxy["name"]),
			proxy:  proxy,
			dns:    nodeMihomoDNS(node),
		})
	}
	if len(candidates) == 0 {
		return ProxyCheckOutcome{Results: results, EngineAvailable: r.hasReadyInstance()}
	}
	groups := groupMihomoCandidatesByDNS(candidates)
	for _, group := range groups {
		key := mihomoDNSKey(group[0].dns)
		for nodeID, result := range r.checkGroup(key, group, timeout) {
			results[nodeID] = result
		}
	}
	// Deliberately NOT reaping here: Check also runs for scoped requests (a
	// single-node "test" button or a per-subscription check), whose node set is
	// not the full active set. Reaping against a scoped set would tear down every
	// other group's persistent instance and force a cold restart next round,
	// defeating the point of persistence. Reaping is done by ReapAbsent, called
	// only from a full check (see CheckService.RunCheck).
	return ProxyCheckOutcome{
		Results:         results,
		EngineAvailable: anyRealProxyStatus(results) || r.hasReadyInstance(),
	}
}

// hasReadyInstance reports whether any persistent mihomo instance is currently
// alive — used to keep EngineAvailable true even when every probe came back
// unknown (e.g. all nodes timed out but the engine itself is up).
func (r *MihomoDelayRunner) hasReadyInstance() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, inst := range r.instances {
		if inst.alive() {
			return true
		}
	}
	return false
}

// anyRealProxyStatus reports whether at least one node got a real delay verdict
// (online/offline/timeout), proving mihomo actually answered this round.
func anyRealProxyStatus(results map[int]ProbeResult) bool {
	for _, result := range results {
		switch normalizeCheckStatus(result.Status) {
		case "online", "offline", "timeout":
			return true
		}
	}
	return false
}

// checkGroup serves one DNS group from its persistent instance, falling back to
// the ephemeral per-check path when the group refuses to start persistently.
func (r *MihomoDelayRunner) checkGroup(key string, group []mihomoProxyCandidate, timeout time.Duration) map[int]ProbeResult {
	inst, err := r.ensureInstance(key, group, timeout)
	if err != nil {
		return r.checkCandidates(group, timeout)
	}
	return r.probeInstance(inst, group, timeout)
}

// ensureInstance returns a ready instance for the group, reusing the running
// process when it is alive and its config is unchanged, otherwise (re)starting
// under an exponential backoff so a crash-looping config is not re-spawned on
// every check (T5).
func (r *MihomoDelayRunner) ensureInstance(key string, group []mihomoProxyCandidate, timeout time.Duration) (*mihomoInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("mihomo manager closed")
	}
	hash := mihomoConfigHash(group)
	if inst := r.instances[key]; inst != nil {
		if inst.alive() && inst.configHash == hash {
			return inst, nil
		}
		inst.stop()
		delete(r.instances, key)
	}
	if bo := r.backoff[key]; bo != nil && time.Now().Before(bo.nextAttempt) {
		return nil, fmt.Errorf("mihomo restart backing off for group %q", key)
	}
	inst, err := r.startInstance(key, group, hash)
	if err != nil {
		r.recordStartFailure(key)
		return nil, err
	}
	delete(r.backoff, key)
	r.instances[key] = inst
	return inst, nil
}

// startInstance launches a long-lived mihomo process for the group, waits for
// its controller to actually load proxies (T6 readiness gate, not just "HTTP
// up"), and reconciles which nodes were loaded (T8). Ports are reallocated and
// the spawn retried once to ride out a port-bind race (T7). Must be called with
// r.mu held (it bumps r.nextGen).
func (r *MihomoDelayRunner) startInstance(key string, group []mihomoProxyCandidate, hash string) (*mihomoInstance, error) {
	proxies := make([]map[string]any, 0, len(group))
	for _, candidate := range group {
		proxies = append(proxies, candidate.proxy)
	}
	dns := mihomoDNSFromCandidates(group)

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		inst, stderr, err := r.spawnMihomo(key, hash, proxies, dns)
		if err != nil {
			lastErr = err
			continue
		}
		loaded, err := waitMihomoReady(inst.baseURL, inst.secret, group, inst.done, r.startTimeout)
		if err != nil {
			inst.stop()
			if message := strings.TrimSpace(stderr.String()); message != "" {
				lastErr = fmt.Errorf("%w: %s", err, message)
			} else {
				lastErr = err
			}
			continue
		}
		inst.loaded = loaded
		r.nextGen++
		inst.generation = r.nextGen
		return inst, nil
	}
	return nil, lastErr
}

// spawnMihomo writes a fresh config (with a per-instance controller secret) and
// starts the process, returning the instance and its captured stderr. The
// caller must call waitMihomoReady before trusting it, and inst.stop() on any
// failure.
func (r *MihomoDelayRunner) spawnMihomo(key, hash string, proxies []map[string]any, dns map[string]any) (*mihomoInstance, *bytes.Buffer, error) {
	secret, err := newMihomoSecret()
	if err != nil {
		return nil, nil, err
	}
	tempDir, err := osMkdirTemp("", "vps-monitor-mihomo-*")
	if err != nil {
		return nil, nil, err
	}
	mixedPort, apiPort, err := allocateMihomoPorts()
	if err != nil {
		_ = osRemoveAll(tempDir)
		return nil, nil, err
	}
	configPath := filepath.Join(tempDir, "config.yaml")
	if err := writeMihomoConfig(configPath, mixedPort, apiPort, secret, proxies, dns); err != nil {
		_ = osRemoveAll(tempDir)
		return nil, nil, err
	}

	ctx, cancel := context.WithCancel(contextBackground())
	cmd := execCommandContext(ctx, r.path, "-f", configPath, "-d", tempDir)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = osRemoveAll(tempDir)
		return nil, nil, err
	}

	inst := &mihomoInstance{
		key:        key,
		configHash: hash,
		secret:     secret,
		tempDir:    tempDir,
		baseURL:    fmt.Sprintf("http://127.0.0.1:%d", apiPort),
		cmd:        cmd,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		inst.markDead()
		close(inst.done)
	}()
	return inst, stderr, nil
}

// recordStartFailure advances the exponential backoff for a DNS group after a
// failed (re)start. Called with r.mu held.
func (r *MihomoDelayRunner) recordStartFailure(key string) {
	if r.backoff == nil {
		r.backoff = map[string]*mihomoBackoff{}
	}
	bo := r.backoff[key]
	if bo == nil {
		bo = &mihomoBackoff{}
		r.backoff[key] = bo
	}
	bo.failures++
	delay := mihomoMinRestartBackoff << uint(min(bo.failures-1, 6))
	if delay > mihomoMaxRestartBackoff || delay <= 0 {
		delay = mihomoMaxRestartBackoff
	}
	bo.nextAttempt = time.Now().Add(delay)
}

// probeInstance issues concurrent delay queries against a ready persistent
// instance, probing only the nodes the controller actually loaded and marking
// the rest isolated (T8).
func (r *MihomoDelayRunner) probeInstance(inst *mihomoInstance, group []mihomoProxyCandidate, timeout time.Duration) map[int]ProbeResult {
	baseURL := inst.baseURL
	secret := inst.secret
	loaded := inst.loaded

	results := make(map[int]ProbeResult, len(group))
	for _, candidate := range group {
		if _, ok := loaded[candidate.nodeID]; !ok {
			results[candidate.nodeID] = ProbeResult{Status: "unknown", Message: mihomoIsolatedMessage}
		}
	}
	if len(loaded) == 0 {
		return results
	}

	client := mihomoControllerClient(secret, timeout+2*time.Second)
	concurrency := r.concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(loaded) {
		concurrency = len(loaded)
	}
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for nodeID, proxyName := range loaded {
		wg.Add(1)
		go func(nodeID int, proxyName string) {
			defer wg.Done()
			sem <- struct{}{}
			result := probeMihomoDelayWithWarmup(client, baseURL, proxyName, r.delayURLs, timeout, r.warmup)
			<-sem
			mu.Lock()
			results[nodeID] = result
			mu.Unlock()
		}(nodeID, proxyName)
	}
	wg.Wait()
	return results
}

// newMihomoSecret generates a random bearer token for the external controller
// so only this process can drive it (defence in depth on top of the 127.0.0.1
// bind).
func newMihomoSecret() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// mihomoAuthTransport injects the controller secret on every request without
// mutating the caller's request.
type mihomoAuthTransport struct {
	secret string
	base   http.RoundTripper
}

func (t mihomoAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.secret == "" {
		return base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.secret)
	return base.RoundTrip(clone)
}

func mihomoControllerClient(secret string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: mihomoAuthTransport{secret: secret}}
}

// waitMihomoReady polls /proxies until at least one of the group's proxies is
// loaded (readiness gate, T6) and returns the nodeID->name set the controller
// actually loaded (reconcile, T8). A controller that is HTTP-up but has not yet
// loaded proxies is treated as not-ready so the first post-restart round does
// not come back mass-unknown.
func waitMihomoReady(baseURL, secret string, group []mihomoProxyCandidate, procExited <-chan struct{}, timeout time.Duration) (map[int]string, error) {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	want := make(map[string]int, len(group))
	for _, candidate := range group {
		want[candidate.name] = candidate.nodeID
	}
	client := mihomoControllerClient(secret, 500*time.Millisecond)
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if procExited != nil {
			select {
			case <-procExited:
				return nil, errors.New("mihomo process exited before becoming ready")
			default:
			}
		}
		present, err := fetchMihomoProxies(client, baseURL)
		if err != nil {
			lastErr = err
		} else {
			loaded := map[int]string{}
			for name, nodeID := range want {
				if _, ok := present[name]; ok {
					loaded[nodeID] = name
				}
			}
			if len(loaded) > 0 {
				return loaded, nil
			}
			lastErr = errors.New("mihomo controller is up but loaded none of the requested proxies")
		}
		if procExited != nil {
			select {
			case <-procExited:
				return nil, errors.New("mihomo process exited before becoming ready")
			case <-time.After(100 * time.Millisecond):
			}
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("mihomo controller not ready: %w", lastErr)
	}
	return nil, errors.New("mihomo controller not ready")
}

// fetchMihomoProxies returns the set of proxy names the controller currently
// has loaded.
func fetchMihomoProxies(client *http.Client, baseURL string) (map[string]struct{}, error) {
	resp, err := client.Get(baseURL + "/proxies")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mihomo /proxies returned %s", resp.Status)
	}
	var payload struct {
		Proxies map[string]json.RawMessage `json:"proxies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	names := make(map[string]struct{}, len(payload.Proxies))
	for name := range payload.Proxies {
		names[name] = struct{}{}
	}
	return names, nil
}

// ReapAbsent stops persistent instances whose DNS group is not represented in
// the given node set. It MUST be called only with the full active node set (a
// full scheduled check), never a scoped subset — otherwise it reaps groups that
// are still in use. The desired-key set is a safe superset (it includes keys
// for invalid nodes too), so it never reaps a group that still has live nodes.
func (r *MihomoDelayRunner) ReapAbsent(nodes []NodeRecord) {
	desired := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		desired[mihomoDNSKey(nodeMihomoDNS(node))] = struct{}{}
	}
	r.reapInstances(desired)
}

// reapInstances stops persistent instances whose DNS group is not in the desired
// set (e.g. after a subscription removed those nodes). Internal: callers must
// pass a desired set derived from the FULL active node set — see ReapAbsent.
func (r *MihomoDelayRunner) reapInstances(desired map[string]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, inst := range r.instances {
		if _, ok := desired[key]; ok {
			continue
		}
		inst.stop()
		delete(r.instances, key)
	}
}

// Close stops every persistent mihomo process and removes their temp dirs. Safe
// to call on shutdown; subsequent Check calls return the manager-closed error
// and fall back to the ephemeral path.
func (r *MihomoDelayRunner) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for key, inst := range r.instances {
		inst.stop()
		delete(r.instances, key)
	}
	return nil
}

func (i *mihomoInstance) markDead() {
	i.deadMu.Lock()
	i.dead = true
	i.deadMu.Unlock()
}

func (i *mihomoInstance) alive() bool {
	i.deadMu.Lock()
	defer i.deadMu.Unlock()
	return !i.dead
}

func (i *mihomoInstance) stop() {
	if i.cancel != nil {
		i.cancel()
	}
	if i.done != nil {
		select {
		case <-i.done:
		case <-time.After(500 * time.Millisecond):
			if i.cmd != nil && i.cmd.Process != nil {
				_ = i.cmd.Process.Kill()
			}
			<-i.done
		}
	}
	if i.tempDir != "" {
		_ = osRemoveAll(i.tempDir)
		i.tempDir = ""
	}
}

// mihomoConfigHash fingerprints a group's proxies + DNS so ensureInstance can
// detect when a subscription change requires restarting the instance.
func mihomoConfigHash(candidates []mihomoProxyCandidate) string {
	sorted := append([]mihomoProxyCandidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].nodeID < sorted[j].nodeID })
	h := sha256.New()
	for _, candidate := range sorted {
		fmt.Fprintf(h, "%d\x00%s\x00", candidate.nodeID, candidate.name)
		if encoded, err := json.Marshal(candidate.proxy); err == nil {
			h.Write(encoded)
		}
		h.Write([]byte{0})
	}
	if encoded, err := json.Marshal(mihomoDNSFromCandidates(sorted)); err == nil {
		h.Write(encoded)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func allocateMihomoPorts() (int, int, error) {
	mixedPort, err := freeLocalPort()
	if err != nil {
		return 0, 0, err
	}
	apiPort, err := freeLocalPort()
	if err != nil {
		return 0, 0, err
	}
	for attempts := 0; apiPort == mixedPort && attempts < 5; attempts++ {
		apiPort, err = freeLocalPort()
		if err != nil {
			return 0, 0, err
		}
	}
	if apiPort == mixedPort {
		return 0, 0, fmt.Errorf("could not allocate distinct mihomo ports")
	}
	return mixedPort, apiPort, nil
}

func groupMihomoCandidatesByDNS(candidates []mihomoProxyCandidate) [][]mihomoProxyCandidate {
	indexes := map[string]int{}
	groups := make([][]mihomoProxyCandidate, 0)
	for _, candidate := range candidates {
		key := mihomoDNSKey(candidate.dns)
		index, ok := indexes[key]
		if !ok {
			index = len(groups)
			indexes[key] = index
			groups = append(groups, []mihomoProxyCandidate{})
		}
		groups[index] = append(groups[index], candidate)
	}
	return groups
}

func mihomoDNSKey(dns map[string]any) string {
	if len(dns) == 0 {
		return ""
	}
	content, err := json.Marshal(dns)
	if err != nil {
		return fmt.Sprintf("%p", dns)
	}
	return string(content)
}

func (r *MihomoDelayRunner) checkCandidates(candidates []mihomoProxyCandidate, timeout time.Duration) map[int]ProbeResult {
	results, err := r.runCandidateBatch(candidates, timeout)
	if err == nil {
		return results
	}
	return isolateMihomoCandidates(candidates, func(items []mihomoProxyCandidate) (map[int]ProbeResult, error) {
		return r.runCandidateBatch(items, timeout)
	}, err)
}

func isolateMihomoCandidates(
	candidates []mihomoProxyCandidate,
	run func([]mihomoProxyCandidate) (map[int]ProbeResult, error),
	batchErr error,
) map[int]ProbeResult {
	results := map[int]ProbeResult{}
	if len(candidates) == 0 {
		return results
	}
	if len(candidates) == 1 {
		results[candidates[0].nodeID] = ProbeResult{
			Status:  "unknown",
			Message: fmt.Sprintf("Mihomo 配置启动失败，已隔离该节点: %v", batchErr),
		}
		return results
	}

	mid := len(candidates) / 2
	for _, group := range [][]mihomoProxyCandidate{candidates[:mid], candidates[mid:]} {
		groupResults, err := run(group)
		if err != nil {
			groupResults = isolateMihomoCandidates(group, run, err)
		}
		for nodeID, result := range groupResults {
			results[nodeID] = result
		}
	}
	return results
}

func (r *MihomoDelayRunner) runCandidateBatch(candidates []mihomoProxyCandidate, timeout time.Duration) (map[int]ProbeResult, error) {
	results := make(map[int]ProbeResult, len(candidates))
	proxies := make([]map[string]any, 0, len(candidates))
	proxyNames := map[int]string{}
	for _, candidate := range candidates {
		proxies = append(proxies, candidate.proxy)
		proxyNames[candidate.nodeID] = candidate.name
	}

	secret, err := newMihomoSecret()
	if err != nil {
		return nil, err
	}

	tempDir, err := osMkdirTemp("", "vps-monitor-mihomo-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = osRemoveAll(tempDir)
	}()

	mixedPort, apiPort, err := allocateMihomoPorts()
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(tempDir, "config.yaml")
	if err := writeMihomoConfig(configPath, mixedPort, apiPort, secret, proxies, mihomoDNSFromCandidates(candidates)); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(contextBackground())
	defer cancel()
	cmd := execCommandContext(ctx, r.path, "-f", configPath, "-d", tempDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	procExited := make(chan struct{})
	go func() {
		done <- cmd.Wait()
		close(procExited)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	if err := waitMihomoController(baseURL, secret, procExited, r.startTimeout); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("%w: %s", err, message)
		}
		return nil, err
	}

	client := mihomoControllerClient(secret, timeout+2*time.Second)
	concurrency := r.concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(proxyNames) {
		concurrency = len(proxyNames)
	}
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for nodeID, proxyName := range proxyNames {
		wg.Add(1)
		go func(nodeID int, proxyName string) {
			defer wg.Done()
			sem <- struct{}{}
			result := probeMihomoDelayWithWarmup(client, baseURL, proxyName, r.delayURLs, timeout, r.warmup)
			<-sem
			mu.Lock()
			results[nodeID] = result
			mu.Unlock()
		}(nodeID, proxyName)
	}
	wg.Wait()
	return results, nil
}

func writeMihomoConfig(path string, mixedPort int, apiPort int, secret string, proxies []map[string]any, dns map[string]any) error {
	names := make([]string, 0, len(proxies))
	for _, proxy := range proxies {
		names = append(names, asString(proxy["name"]))
	}
	payload := map[string]any{
		"mixed-port":          mixedPort,
		"allow-lan":           false,
		"bind-address":        "127.0.0.1",
		"mode":                "rule",
		"log-level":           "warning",
		"external-controller": fmt.Sprintf("127.0.0.1:%d", apiPort),
		"proxies":             proxies,
		"proxy-groups": []map[string]any{
			{
				"name":    "vps-monitor",
				"type":    "select",
				"proxies": names,
			},
		},
		"rules": []string{"MATCH,vps-monitor"},
	}
	if strings.TrimSpace(secret) != "" {
		payload["secret"] = secret
	}
	if len(dns) > 0 {
		payload["dns"] = sanitizeMihomoDNS(dns)
	}
	content, err := yaml.Marshal(payload)
	if err != nil {
		return err
	}
	return osWriteFile(path, content, 0o600)
}

func mihomoDNSFromCandidates(candidates []mihomoProxyCandidate) map[string]any {
	for _, candidate := range candidates {
		if len(candidate.dns) > 0 {
			return candidate.dns
		}
	}
	return nil
}

func sanitizeMihomoDNS(dns map[string]any) map[string]any {
	clean := cloneYAMLMap(dns)
	if len(clean) == 0 {
		return nil
	}
	clean["enabled"] = true
	delete(clean, "listen")
	if _, ok := clean["proxy-server-nameserver"]; !ok {
		if nameserver, ok := clean["nameserver"]; ok {
			clean["proxy-server-nameserver"] = cloneYAMLValue(nameserver)
		}
	}
	for _, key := range []string{"nameserver", "fallback", "default-nameserver", "proxy-server-nameserver"} {
		value, ok := clean[key]
		if !ok {
			continue
		}
		safe := filterSafeDNSNameservers(stringListFromAny(value))
		if len(safe) == 0 {
			delete(clean, key)
			continue
		}
		clean[key] = safe
	}
	return clean
}

// filterSafeDNSNameservers drops nameserver entries that would let a malicious
// subscription point the proxy resolver at an internal service: insecure
// (http://) DoH, or secure DoH/DoT/DoQ whose host is loopback / private /
// link-local. Plain UDP nameservers and public DoH endpoints are kept. This is
// the README-advertised SSRF guard, now applied to every DNS group as its
// config is folded into the generated mihomo config (the guard used to live in
// the Go resolver that the persistent-mihomo rewrite removed).
func filterSafeDNSNameservers(entries []string) []string {
	safe := make([]string, 0, len(entries))
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if isSafeMihomoNameserver(trimmed) {
			safe = append(safe, trimmed)
		}
	}
	return dedupeStrings(safe)
}

func isSafeMihomoNameserver(entry string) bool {
	lower := strings.ToLower(entry)
	switch {
	case strings.HasPrefix(lower, "http://"):
		return false
	case strings.HasPrefix(lower, "https://"):
		parsed, err := url.Parse(entry)
		if err != nil {
			return false
		}
		return validateDoHEndpoint(parsed) == nil
	case strings.HasPrefix(lower, "tls://"), strings.HasPrefix(lower, "quic://"), strings.HasPrefix(lower, "h3://"):
		parsed, err := url.Parse(entry)
		if err != nil || strings.TrimSpace(parsed.Hostname()) == "" {
			return false
		}
		return !isBlockedDoHHost(parsed.Hostname())
	default:
		// Plain UDP nameserver (ip[:port], system, dhcp, etc.) — kept; mihomo
		// uses it as a resolver, not an HTTP fetch target, so it is not the
		// advertised SSRF vector.
		return true
	}
}

func waitMihomoController(baseURL string, secret string, procExited <-chan struct{}, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	deadline := time.Now().Add(timeout)
	client := mihomoControllerClient(secret, 500*time.Millisecond)
	var lastErr error
	for time.Now().Before(deadline) {
		if procExited != nil {
			select {
			case <-procExited:
				return errors.New("mihomo process exited before the controller became ready")
			default:
			}
		}
		resp, err := client.Get(baseURL + "/proxies")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return nil
			}
			lastErr = fmt.Errorf("mihomo controller returned %s", resp.Status)
		} else {
			lastErr = err
		}
		if procExited != nil {
			select {
			case <-procExited:
				return errors.New("mihomo process exited before the controller became ready")
			case <-time.After(100 * time.Millisecond):
			}
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if lastErr != nil {
		return fmt.Errorf("mihomo controller did not start: %w", lastErr)
	}
	return errors.New("mihomo controller did not start")
}

func probeMihomoDelayWithWarmup(
	client *http.Client,
	baseURL string,
	proxyName string,
	targetURLs []string,
	timeout time.Duration,
	warmup bool,
) ProbeResult {
	targetURLs = normalizeProxyCheckURLs("", targetURLs)
	if len(targetURLs) == 1 {
		if warmup {
			_ = probeMihomoDelay(client, baseURL, proxyName, targetURLs[0], timeout)
		}
		return probeMihomoDelay(client, baseURL, proxyName, targetURLs[0], timeout)
	}

	failures := make([]string, 0, len(targetURLs))
	finalStatus := "offline"
	for _, targetURL := range targetURLs {
		if warmup {
			_ = probeMihomoDelay(client, baseURL, proxyName, targetURL, timeout)
		}
		result := probeMihomoDelay(client, baseURL, proxyName, targetURL, timeout)
		if result.Status == "online" {
			return result
		}
		if result.Status == "timeout" {
			finalStatus = "timeout"
		}
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = result.Status
		}
		failures = append(failures, fmt.Sprintf("%s => %s", targetURL, message))
	}
	return ProbeResult{Status: finalStatus, Message: strings.Join(failures, "；")}
}

func probeMihomoDelay(client *http.Client, baseURL string, proxyName string, targetURL string, timeout time.Duration) ProbeResult {
	timeoutMS := int(timeout.Milliseconds())
	if timeoutMS <= 0 {
		timeoutMS = 5000
	}
	endpoint := fmt.Sprintf(
		"%s/proxies/%s/delay?timeout=%d&url=%s",
		baseURL,
		url.PathEscape(proxyName),
		timeoutMS,
		url.QueryEscape(targetURL),
	)
	resp, err := client.Get(endpoint)
	if err != nil {
		if isTimeoutError(err) {
			return ProbeResult{Status: "timeout", Message: err.Error()}
		}
		return ProbeResult{Status: "offline", Message: err.Error()}
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := mihomoHTTPErrorMessage(resp)
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusGatewayTimeout {
			return ProbeResult{Status: "timeout", Message: message}
		}
		if isTimeoutMessage(message) {
			return ProbeResult{Status: "timeout", Message: message}
		}
		return ProbeResult{Status: "offline", Message: message}
	}
	var payload struct {
		Delay int `json:"delay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ProbeResult{Status: "offline", Message: err.Error()}
	}
	latency := payload.Delay
	return ProbeResult{Status: "online", LatencyMS: &latency}
}

func mihomoHTTPErrorMessage(resp *http.Response) string {
	status := resp.Status
	if resp.Body == nil {
		return status
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return status
	}
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return status
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err == nil {
		for _, key := range []string{"message", "error"} {
			if value := strings.TrimSpace(asString(payload[key])); value != "" {
				return fmt.Sprintf("%s: %s", status, value)
			}
		}
	}
	return fmt.Sprintf("%s: %s", status, body)
}

func isTimeoutMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded")
}

func freeLocalPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = listener.Close()
	}()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %q", listener.Addr().String())
	}
	return addr.Port, nil
}

func nodeToMihomoProxy(node NodeRecord) (map[string]any, error) {
	protocol := normalizeProtocol(node.Protocol)
	if protocol == "" {
		return nil, fmt.Errorf("unsupported protocol %q", node.Protocol)
	}
	proxyType := protocol
	if protocol == "ss2022" {
		proxyType = "ss"
	}
	if protocol == "hy2" {
		proxyType = "hysteria2"
	}
	proxy := map[string]any{
		"name":   mihomoProxyName(node),
		"type":   proxyType,
		"server": node.Server,
		"port":   node.Port,
	}
	extras := node.ExtraParams
	if extras == nil {
		extras = map[string]any{}
	}

	switch protocol {
	case "ss", "ss2022":
		copyFirst(proxy, "cipher", extras, "method", "cipher", "security")
		copyFirst(proxy, "password", extras, "password")
		copyFirst(proxy, "plugin", extras, "plugin")
		copyFirst(proxy, "plugin-opts", extras, "plugin_opts")
	case "ssr":
		copyFirst(proxy, "cipher", extras, "method", "cipher")
		copyFirst(proxy, "password", extras, "password")
		copyFirst(proxy, "protocol", extras, "protocol")
		copyFirst(proxy, "protocol-param", extras, "protocol_param")
		copyFirst(proxy, "obfs", extras, "obfs")
		copyFirst(proxy, "obfs-param", extras, "obfs_param")
	case "vmess":
		copyFirst(proxy, "uuid", extras, "uuid")
		copyFirst(proxy, "alterId", extras, "alterId", "alterid")
		copyFirst(proxy, "cipher", extras, "security", "cipher")
		copyNetworkOptions(proxy, extras)
	case "trojan":
		copyFirst(proxy, "password", extras, "password")
		copyNetworkOptions(proxy, extras)
	case "vless":
		copyFirst(proxy, "uuid", extras, "uuid")
		copyFirst(proxy, "flow", extras, "flow")
		copyNetworkOptions(proxy, extras)
		switch strings.ToLower(firstExtra(extras, "security")) {
		case "tls":
			proxy["tls"] = true
		case "reality":
			proxy["tls"] = true
			copyFirst(proxy, "servername", extras, "sni", "servername", "server_name")
			reality := map[string]any{}
			copyFirst(reality, "public-key", extras, "pbk", "public_key", "public-key")
			copyFirst(reality, "short-id", extras, "sid", "short_id", "short-id")
			if len(reality) > 0 {
				proxy["reality-opts"] = reality
			}
		}
	case "hysteria":
		copyFirst(proxy, "auth-str", extras, "auth_str", "auth")
		copyFirst(proxy, "obfs", extras, "obfs")
		copyFirst(proxy, "obfs-password", extras, "obfs_password")
		copyFirst(proxy, "sni", extras, "sni", "peer")
		copyFirst(proxy, "up", extras, "up_mbps", "up")
		copyFirst(proxy, "down", extras, "down_mbps", "down")
	case "hy2":
		copyFirst(proxy, "password", extras, "password")
		copyFirst(proxy, "obfs", extras, "obfs")
		copyFirst(proxy, "obfs-password", extras, "obfs_password")
		copyFirst(proxy, "sni", extras, "sni")
		copyFirst(proxy, "ports", extras, "ports")
		copyFirst(proxy, "hop-interval", extras, "hop_interval")
	case "tuic":
		copyFirst(proxy, "uuid", extras, "uuid")
		copyFirst(proxy, "password", extras, "password")
		copyFirst(proxy, "congestion-controller", extras, "congestion_controller", "congestion-control")
		copyFirst(proxy, "udp-relay-mode", extras, "udp_relay_mode")
		copyFirst(proxy, "sni", extras, "sni")
	case "anytls":
		copyFirst(proxy, "password", extras, "password")
		copyFirst(proxy, "sni", extras, "sni")
	case "shadowtls":
		copyFirst(proxy, "password", extras, "password", "shadow_tls_password")
		copyFirst(proxy, "version", extras, "version")
		copyFirst(proxy, "host", extras, "host", "sni")
	case "naiveproxy":
		copyFirst(proxy, "username", extras, "username")
		copyFirst(proxy, "password", extras, "password")
	case "wireguard":
		copyFirst(proxy, "private-key", extras, "private_key")
		copyFirst(proxy, "public-key", extras, "public_key")
		copyFirst(proxy, "reserved", extras, "reserved")
	default:
		return nil, fmt.Errorf("unsupported protocol %q", node.Protocol)
	}
	copyFirst(proxy, "skip-cert-verify", extras, "insecure", "skip_cert_verify")
	copyFirst(proxy, "alpn", extras, "alpn")
	copyCommonMihomoOptions(proxy, extras)
	return proxy, nil
}

func mihomoProxyName(node NodeRecord) string {
	return fmt.Sprintf("%d-%s", node.ID, strings.TrimSpace(node.Name))
}

func copyNetworkOptions(proxy map[string]any, extras map[string]any) {
	copyFirst(proxy, "network", extras, "network", "type")
	copyFirst(proxy, "servername", extras, "sni", "servername", "server_name")
	if tlsValue := firstExtra(extras, "tls"); tlsValue != "" {
		switch strings.ToLower(tlsValue) {
		case "tls", "true", "reality":
			proxy["tls"] = true
		case "false", "none":
		default:
			proxy["tls"] = tlsValue
		}
	}
	network := strings.ToLower(asString(proxy["network"]))
	if network == "ws" || (network == "" && firstExtra(extras, "path", "host") != "") {
		wsOpts := map[string]any{}
		copyFirst(wsOpts, "path", extras, "path")
		if host := firstExtra(extras, "host"); host != "" {
			wsOpts["headers"] = map[string]any{"Host": host}
		}
		if len(wsOpts) > 0 {
			proxy["ws-opts"] = wsOpts
		}
	}
	if network == "grpc" && proxy["grpc-opts"] == nil {
		grpcOpts := map[string]any{}
		copyFirst(grpcOpts, "grpc-service-name", extras, "service_name", "servicename", "grpc_service_name")
		copyFirst(grpcOpts, "grpc-mode", extras, "grpc_mode")
		if len(grpcOpts) > 0 {
			proxy["grpc-opts"] = grpcOpts
		}
	}
	if (network == "http" || network == "h2") && proxy["h2-opts"] == nil && proxy["http-opts"] == nil {
		httpOpts := map[string]any{}
		if path := firstExtra(extras, "path"); path != "" {
			httpOpts["path"] = []string{path}
		}
		if host := firstExtra(extras, "host"); host != "" {
			httpOpts["host"] = []string{host}
		}
		if len(httpOpts) > 0 {
			if network == "h2" {
				proxy["h2-opts"] = httpOpts
			} else {
				proxy["http-opts"] = httpOpts
			}
		}
	}
}

func copyCommonMihomoOptions(proxy map[string]any, extras map[string]any) {
	mappings := []struct {
		target string
		source []string
	}{
		{target: "udp", source: []string{"udp"}},
		{target: "client-fingerprint", source: []string{"client_fingerprint", "fingerprint", "fp"}},
		{target: "ws-opts", source: []string{"ws_opts"}},
		{target: "grpc-opts", source: []string{"grpc_opts"}},
		{target: "h2-opts", source: []string{"h2_opts"}},
		{target: "http-opts", source: []string{"http_opts"}},
		{target: "reality-opts", source: []string{"reality_opts"}},
		{target: "packet-encoding", source: []string{"packet_encoding"}},
		{target: "packet-addr", source: []string{"packet_addr"}},
	}
	for _, mapping := range mappings {
		if _, exists := proxy[mapping.target]; exists {
			continue
		}
		copyFirst(proxy, mapping.target, extras, mapping.source...)
	}
}

func copyFirst(target map[string]any, targetKey string, extras map[string]any, sourceKeys ...string) {
	for _, key := range sourceKeys {
		value, ok := extras[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		if numberText := asString(value); targetKey == "alterId" && numberText != "" {
			target[targetKey] = asInt(value)
			return
		}
		target[targetKey] = normalizeMihomoValue(value)
		return
	}
}

func firstExtra(extras map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := extras[key]
		if !ok {
			continue
		}
		text := strings.TrimSpace(asString(value))
		if text != "" {
			return text
		}
	}
	return ""
}

func normalizeMihomoValue(value any) any {
	text := strings.TrimSpace(asString(value))
	switch strings.ToLower(text) {
	case "true":
		return true
	case "false":
		return false
	}
	if strings.Contains(text, ",") {
		parts := strings.Split(text, ",")
		items := make([]string, 0, len(parts))
		for _, part := range parts {
			item := strings.TrimSpace(part)
			if item != "" {
				items = append(items, item)
			}
		}
		if len(items) > 1 {
			return items
		}
	}
	if i, err := strconv.Atoi(text); err == nil && text == strconv.Itoa(i) {
		return i
	}
	return value
}
