package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type MihomoDelayRunner struct {
	path         string
	delayURL     string
	startTimeout time.Duration
	concurrency  int
}

type unavailableProxyDelayRunner struct {
	message string
}

type mihomoProxyCandidate struct {
	nodeID int
	name   string
	proxy  map[string]any
}

func NewProxyDelayRunner(config Config) ProxyDelayRunner {
	if !config.ProxyCheckEnabled {
		return unavailableProxyDelayRunner{message: "真实代理测速已关闭，当前仅运行入口 TCP 探活"}
	}
	path := strings.TrimSpace(config.MihomoPath)
	if path == "" {
		path = findMihomoExecutable()
	}
	if path == "" {
		return unavailableProxyDelayRunner{message: "未找到 mihomo、clash-meta 或 clash，可安装 Mihomo 或配置 check.mihomo_path 启用真实代理测速"}
	}
	delayURL := strings.TrimSpace(config.ProxyCheckURL)
	if delayURL == "" {
		delayURL = "https://www.gstatic.com/generate_204"
	}
	startTimeout := config.MihomoStartTimeout
	if startTimeout <= 0 {
		startTimeout = 8 * time.Second
	}
	concurrency := config.CheckConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	return &MihomoDelayRunner{
		path:         path,
		delayURL:     delayURL,
		startTimeout: startTimeout,
		concurrency:  concurrency,
	}
}

func (r unavailableProxyDelayRunner) Check(nodes []NodeRecord, timeout time.Duration) (map[int]ProbeResult, error) {
	results := make(map[int]ProbeResult, len(nodes))
	for _, node := range nodes {
		results[node.ID] = ProbeResult{Status: "unknown", Message: r.message}
	}
	return results, nil
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

func (r *MihomoDelayRunner) Check(nodes []NodeRecord, timeout time.Duration) (map[int]ProbeResult, error) {
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
		})
	}
	if len(candidates) == 0 {
		return results, nil
	}
	for nodeID, result := range r.checkCandidates(candidates, timeout) {
		results[nodeID] = result
	}
	return results, nil
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

	tempDir, err := osMkdirTemp("", "vps-monitor-mihomo-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = osRemoveAll(tempDir)
	}()

	mixedPort, err := freeLocalPort()
	if err != nil {
		return nil, err
	}
	apiPort, err := freeLocalPort()
	if err != nil {
		return nil, err
	}
	for attempts := 0; apiPort == mixedPort && attempts < 5; attempts++ {
		apiPort, err = freeLocalPort()
		if err != nil {
			return nil, err
		}
	}
	if apiPort == mixedPort {
		return nil, fmt.Errorf("could not allocate distinct mihomo ports")
	}

	configPath := filepath.Join(tempDir, "config.yaml")
	if err := writeMihomoConfig(configPath, mixedPort, apiPort, proxies); err != nil {
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
	go func() {
		done <- cmd.Wait()
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
	if err := waitMihomoController(baseURL, r.startTimeout); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("%w: %s", err, message)
		}
		return nil, err
	}

	client := &http.Client{Timeout: timeout + 2*time.Second}
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
			result := probeMihomoDelay(client, baseURL, proxyName, r.delayURL, timeout)
			<-sem
			mu.Lock()
			results[nodeID] = result
			mu.Unlock()
		}(nodeID, proxyName)
	}
	wg.Wait()
	return results, nil
}

func writeMihomoConfig(path string, mixedPort int, apiPort int, proxies []map[string]any) error {
	names := make([]string, 0, len(proxies))
	for _, proxy := range proxies {
		names = append(names, asString(proxy["name"]))
	}
	payload := map[string]any{
		"mixed-port":          mixedPort,
		"allow-lan":           false,
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
	content, err := yaml.Marshal(payload)
	if err != nil {
		return err
	}
	return osWriteFile(path, content, 0o600)
}

func waitMihomoController(baseURL string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error
	for time.Now().Before(deadline) {
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
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("mihomo controller did not start: %w", lastErr)
	}
	return errors.New("mihomo controller did not start")
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
