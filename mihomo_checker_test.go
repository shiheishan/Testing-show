package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestNodeToMihomoProxyMapsCommonFields(t *testing.T) {
	node := NodeRecord{
		ID:       42,
		Name:     "VMess WS",
		Server:   "example.com",
		Port:     443,
		Protocol: "vmess",
		ExtraParams: map[string]any{
			"uuid":     "uuid-1",
			"security": "auto",
			"network":  "ws",
			"path":     "/ray",
			"host":     "cdn.example.com",
			"tls":      "tls",
			"sni":      "edge.example.com",
		},
	}

	proxy, err := nodeToMihomoProxy(node)
	if err != nil {
		t.Fatalf("nodeToMihomoProxy error: %v", err)
	}
	if proxy["type"] != "vmess" || proxy["cipher"] != "auto" || proxy["tls"] != true {
		t.Fatalf("unexpected proxy fields: %+v", proxy)
	}
	wsOpts, ok := proxy["ws-opts"].(map[string]any)
	if !ok {
		t.Fatalf("missing ws-opts: %+v", proxy)
	}
	if wsOpts["path"] != "/ray" {
		t.Fatalf("ws path = %v", wsOpts["path"])
	}
	headers, ok := wsOpts["headers"].(map[string]any)
	if !ok || headers["Host"] != "cdn.example.com" {
		t.Fatalf("unexpected ws headers: %+v", wsOpts["headers"])
	}
}

func TestNodeToMihomoProxyKeepsNonWSNetworkOptions(t *testing.T) {
	node := NodeRecord{
		ID:       43,
		Name:     "VMess gRPC",
		Server:   "grpc.example.com",
		Port:     443,
		Protocol: "vmess",
		ExtraParams: map[string]any{
			"uuid":               "uuid-1",
			"security":           "auto",
			"network":            "grpc",
			"service_name":       "grpc-service",
			"client_fingerprint": "chrome",
			"udp":                true,
		},
	}

	proxy, err := nodeToMihomoProxy(node)
	if err != nil {
		t.Fatalf("nodeToMihomoProxy error: %v", err)
	}
	if proxy["network"] != "grpc" {
		t.Fatalf("network = %v, want grpc", proxy["network"])
	}
	if _, ok := proxy["ws-opts"]; ok {
		t.Fatalf("unexpected ws-opts on grpc proxy: %+v", proxy["ws-opts"])
	}
	grpcOpts, ok := proxy["grpc-opts"].(map[string]any)
	if !ok || grpcOpts["grpc-service-name"] != "grpc-service" {
		t.Fatalf("unexpected grpc-opts: %+v", proxy["grpc-opts"])
	}
	if proxy["client-fingerprint"] != "chrome" || proxy["udp"] != true {
		t.Fatalf("missing passthrough fields: %+v", proxy)
	}
}

func TestNodeToMihomoProxyMapsHTTPNetworkOptions(t *testing.T) {
	node := NodeRecord{
		ID:       44,
		Name:     "VMess HTTP",
		Server:   "http.example.com",
		Port:     443,
		Protocol: "vmess",
		ExtraParams: map[string]any{
			"uuid":     "uuid-1",
			"security": "auto",
			"network":  "http",
			"path":     "/v2",
			"host":     "cdn.example.com",
		},
	}

	proxy, err := nodeToMihomoProxy(node)
	if err != nil {
		t.Fatalf("nodeToMihomoProxy error: %v", err)
	}
	if _, ok := proxy["ws-opts"]; ok {
		t.Fatalf("unexpected ws-opts on http proxy: %+v", proxy["ws-opts"])
	}
	httpOpts, ok := proxy["http-opts"].(map[string]any)
	if !ok {
		t.Fatalf("missing http-opts: %+v", proxy)
	}
	paths, ok := httpOpts["path"].([]string)
	if !ok || len(paths) != 1 || paths[0] != "/v2" {
		t.Fatalf("unexpected http paths: %+v", httpOpts["path"])
	}
	hosts, ok := httpOpts["host"].([]string)
	if !ok || len(hosts) != 1 || hosts[0] != "cdn.example.com" {
		t.Fatalf("unexpected http hosts: %+v", httpOpts["host"])
	}
}

func TestNodeToMihomoProxyMapsHy2Alias(t *testing.T) {
	node := NodeRecord{
		ID:       7,
		Name:     "HY2",
		Server:   "hy2.example.com",
		Port:     443,
		Protocol: "hy2",
		ExtraParams: map[string]any{
			"password": "secret",
			"sni":      "edge.example.com",
		},
	}

	proxy, err := nodeToMihomoProxy(node)
	if err != nil {
		t.Fatalf("nodeToMihomoProxy error: %v", err)
	}
	if proxy["type"] != "hysteria2" {
		t.Fatalf("type = %v, want hysteria2", proxy["type"])
	}
	if proxy["password"] != "secret" || proxy["sni"] != "edge.example.com" {
		t.Fatalf("unexpected proxy fields: %+v", proxy)
	}
}

func TestUnavailableProxyDelayRunnerReportsReason(t *testing.T) {
	runner := unavailableProxyDelayRunner{message: "missing mihomo"}
	results, err := runner.Check([]NodeRecord{{ID: 1}}, 0)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	got := results[1]
	if got.Status != "unknown" || got.Message != "missing mihomo" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestProbeMihomoDelayIncludesErrorBody(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status:     "503 Service Unavailable",
			Body:       io.NopCloser(strings.NewReader(`{"message":"dial tcp 203.0.113.1:443: connect: connection refused"}`)),
			Request:    r,
		}, nil
	})}

	result := probeMihomoDelay(client, "http://mihomo.local", "bad-node", "https://example.com/generate_204", 5*time.Second)
	if result.Status != "offline" {
		t.Fatalf("status = %s, want offline", result.Status)
	}
	if !strings.Contains(result.Message, "503 Service Unavailable") || !strings.Contains(result.Message, "connection refused") {
		t.Fatalf("message did not include status and body: %q", result.Message)
	}
}

func TestProbeMihomoDelayKeepsSuccessBodyReadable(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"delay":123}`)),
			Request:    r,
		}, nil
	})}

	result := probeMihomoDelay(client, "http://mihomo.local", "good-node", "https://example.com/generate_204", 5*time.Second)
	if result.Status != "online" {
		t.Fatalf("status = %s, want online; result = %+v", result.Status, result)
	}
	if result.LatencyMS == nil || *result.LatencyMS != 123 {
		t.Fatalf("latency = %v, want 123", result.LatencyMS)
	}
}

func TestProbeMihomoDelayWithWarmupRecordsSecondResult(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		delay := 900
		if calls == 2 {
			delay = 120
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"delay":%d}`, delay))),
			Request:    r,
		}, nil
	})}

	result := probeMihomoDelayWithWarmup(client, "http://mihomo.local", "warm-node", "https://example.com/generate_204", 5*time.Second, true)
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if result.Status != "online" {
		t.Fatalf("status = %s, want online; result = %+v", result.Status, result)
	}
	if result.LatencyMS == nil || *result.LatencyMS != 120 {
		t.Fatalf("latency = %v, want 120", result.LatencyMS)
	}
}

func TestProbeMihomoDelayWithWarmupDisabledRecordsFirstResult(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"delay":333}`)),
			Request:    r,
		}, nil
	})}

	result := probeMihomoDelayWithWarmup(client, "http://mihomo.local", "cold-node", "https://example.com/generate_204", 5*time.Second, false)
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if result.LatencyMS == nil || *result.LatencyMS != 333 {
		t.Fatalf("latency = %v, want 333", result.LatencyMS)
	}
}

func TestProbeMihomoDelayClassifiesTimeoutBody(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status:     "503 Service Unavailable",
			Body:       io.NopCloser(strings.NewReader(`{"message":"Get \"https://example.com\": i/o timeout"}`)),
			Request:    r,
		}, nil
	})}

	result := probeMihomoDelay(client, "http://mihomo.local", "slow-node", "https://example.com/generate_204", 5*time.Second)
	if result.Status != "timeout" {
		t.Fatalf("status = %s, want timeout; result = %+v", result.Status, result)
	}
	if !strings.Contains(result.Message, "i/o timeout") {
		t.Fatalf("message did not include timeout body: %q", result.Message)
	}
}

func TestIsolateMihomoCandidatesSplitsBadNode(t *testing.T) {
	candidates := []mihomoProxyCandidate{
		{nodeID: 1, name: "one", proxy: map[string]any{"name": "one"}},
		{nodeID: 2, name: "two", proxy: map[string]any{"name": "two"}},
		{nodeID: 3, name: "three", proxy: map[string]any{"name": "three"}},
	}

	runs := 0
	results := isolateMihomoCandidates(candidates, func(items []mihomoProxyCandidate) (map[int]ProbeResult, error) {
		runs++
		for _, item := range items {
			if item.nodeID == 2 {
				return nil, fmt.Errorf("bad proxy config")
			}
		}
		payload := map[int]ProbeResult{}
		for _, item := range items {
			latency := 11
			payload[item.nodeID] = ProbeResult{Status: "online", LatencyMS: &latency}
		}
		return payload, nil
	}, fmt.Errorf("batch failed"))

	if runs < 3 {
		t.Fatalf("expected recursive splitting, got %d runs", runs)
	}
	if results[1].Status != "online" || results[3].Status != "online" {
		t.Fatalf("expected valid nodes to remain online: %+v", results)
	}
	if results[2].Status != "unknown" || results[2].Message == "" {
		t.Fatalf("expected bad node to be isolated with message: %+v", results[2])
	}
}
