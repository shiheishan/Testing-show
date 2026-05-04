package main

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// uriLinePattern matches URI schemes per RFC 3986
// Pattern validated: must start with letter, followed by letters/digits/+/-/.
var uriLinePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

type ImportResult struct {
	Format        string             `json:"format"`
	ImportedCount int                `json:"imported_count"`
	SkippedCount  int                `json:"skipped_count"`
	Diagnostics   []ImportDiagnostic `json:"diagnostics,omitempty"`
	Summary       string             `json:"summary"`
	Nodes         []ParsedNode       `json:"-"`
}

type ImportDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

func ParseSubscriptionDetailed(content string) (ImportResult, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ImportResult{}, fmt.Errorf("empty subscription content")
	}

	if looksLikeClashPayload(trimmed) {
		report, err := parseClashYAMLDetailed(trimmed)
		if report.ImportedCount > 0 || report.SkippedCount > 0 || errors.Is(err, errClashConfigNoProxies) {
			return report, err
		}
	}

	return parseURILinesDetailed(trimmed)
}

func parseClashYAMLDetailed(content string) (ImportResult, error) {
	report := ImportResult{Format: "clash_yaml"}

	var root map[string]any
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		report.addDiagnostic(ImportDiagnostic{Code: "invalid_yaml", Message: "YAML 格式无效"})
		return finalizeImportResult(report, errNoSupportedNodes)
	}

	proxiesValue, ok := root["proxies"]
	if !ok || proxiesValue == nil {
		if looksLikeClashPayload(content) {
			return finalizeImportResult(report, errClashConfigNoProxies)
		}
		return finalizeImportResult(report, errNoSupportedNodes)
	}

	proxies, ok := proxiesValue.([]any)
	if !ok {
		report.addDiagnostic(ImportDiagnostic{Code: "invalid_proxies_section", Message: "proxies 字段不是节点列表"})
		return finalizeImportResult(report, errNoSupportedNodes)
	}
	if len(proxies) == 0 {
		return finalizeImportResult(report, errClashConfigNoProxies)
	}

	report.Nodes = make([]ParsedNode, 0, len(proxies))
	for _, item := range proxies {
		proxy, ok := item.(map[string]any)
		if !ok {
			report.addDiagnostic(ImportDiagnostic{Code: "malformed_proxy", Message: "节点条目不是有效对象"})
			continue
		}
		node, err := normalizeClashProxy(proxy)
		if err != nil {
			report.addDiagnostic(clashDiagnosticFromError(err))
			continue
		}
		report.Nodes = append(report.Nodes, node)
	}

	if len(report.Nodes) == 0 {
		return finalizeImportResult(report, errNoSupportedNodes)
	}
	return finalizeImportResult(report, nil)
}

func parseURILinesDetailed(content string) (ImportResult, error) {
	report := ImportResult{Format: "uri_bundle"}
	payload := strings.TrimSpace(content)
	if !strings.Contains(payload, "://") {
		if decoded, err := safeBase64Decode(payload); err == nil && strings.Contains(decoded, "://") {
			payload = decoded
		}
	}

	lines := strings.Split(payload, "\n")
	report.Nodes = make([]ParsedNode, 0, len(lines))
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !uriLinePattern.MatchString(line) {
			continue
		}

		lower := strings.ToLower(line)
		if hasIgnoredScheme(line) {
			continue
		}
		var (
			node ParsedNode
			err  error
		)
		switch {
		case strings.HasPrefix(lower, "ss://"):
			node, err = parseSSURI(line)
		case strings.HasPrefix(lower, "ssr://"):
			node, err = parseSSRURI(line)
		case strings.HasPrefix(lower, "vmess://"):
			node, err = parseVMessURI(line)
		case strings.HasPrefix(lower, "trojan://"), strings.HasPrefix(lower, "trojan-go://"):
			node, err = parseGenericURI(line, "trojan")
		case strings.HasPrefix(lower, "vless://"):
			node, err = parseGenericURI(line, "vless")
		case strings.HasPrefix(lower, "hysteria://"):
			node, err = parseGenericURI(line, "hysteria")
		case strings.HasPrefix(lower, "hy2://"), strings.HasPrefix(lower, "hysteria2://"):
			node, err = parseGenericURI(line, "hy2")
		case strings.HasPrefix(lower, "tuic://"):
			node, err = parseGenericURI(line, "tuic")
		case strings.HasPrefix(lower, "anytls://"):
			node, err = parseGenericURI(line, "anytls")
		case strings.HasPrefix(lower, "shadowtls://"), strings.HasPrefix(lower, "shadow-tls://"):
			node, err = parseGenericURI(line, "shadowtls")
		case strings.HasPrefix(lower, "naive://"), strings.HasPrefix(lower, "naive+https://"):
			node, err = parseGenericURI(line, "naiveproxy")
		case strings.HasPrefix(lower, "wireguard://"), strings.HasPrefix(lower, "wg://"):
			node, err = parseGenericURI(line, "wireguard")
		default:
			report.addDiagnostic(ImportDiagnostic{Code: "unsupported_entry", Message: "条目格式暂不支持"})
			continue
		}
		if err != nil {
			report.addDiagnostic(uriDiagnosticFromError(err))
			continue
		}
		report.Nodes = append(report.Nodes, node)
	}

	if len(report.Nodes) == 0 {
		return finalizeImportResult(report, errNoSupportedNodes)
	}
	return finalizeImportResult(report, nil)
}

func normalizeClashProxy(proxy map[string]any) (ParsedNode, error) {
	protocol := detectProtocol(asString(proxy["type"]), proxy)
	if protocol == "" {
		return ParsedNode{}, fmt.Errorf("unsupported protocol")
	}

	extras := map[string]any{}
	addIfPresent(extras, "method", firstNonEmpty(proxy["cipher"], proxy["method"]))
	addIfPresent(extras, "plugin", proxy["plugin"])
	addIfPresent(extras, "plugin_opts", proxy["plugin-opts"])
	addIfPresent(extras, "password", proxy["password"])
	addIfPresent(extras, "protocol", proxy["protocol"])
	addIfPresent(extras, "protocol_param", proxy["protocol-param"])
	addIfPresent(extras, "obfs", proxy["obfs"])
	addIfPresent(extras, "obfs_param", proxy["obfs-param"])
	addIfPresent(extras, "uuid", proxy["uuid"])
	addIfPresent(extras, "alterId", proxy["alterId"])
	addIfPresent(extras, "security", proxy["cipher"])
	addIfPresent(extras, "network", proxy["network"])
	addIfPresent(extras, "path", proxy["path"])
	addIfPresent(extras, "host", proxy["host"])
	addIfPresent(extras, "tls", proxy["tls"])
	addIfPresent(extras, "sni", proxy["sni"])
	addIfPresent(extras, "flow", proxy["flow"])
	addIfPresent(extras, "alpn", proxy["alpn"])
	addIfPresent(extras, "obfs_password", proxy["obfs-password"])
	addIfPresent(extras, "auth_str", firstNonEmpty(proxy["auth-str"], proxy["auth_str"], proxy["auth"]))
	addIfPresent(extras, "obfs_param", firstNonEmpty(proxy["obfs-param"], proxy["obfs_param"]))
	addIfPresent(extras, "up_mbps", firstNonEmpty(proxy["up"], proxy["up_mbps"]))
	addIfPresent(extras, "down_mbps", firstNonEmpty(proxy["down"], proxy["down_mbps"]))
	addIfPresent(extras, "peer", proxy["peer"])
	addIfPresent(extras, "insecure", proxy["skip-cert-verify"])
	addIfPresent(extras, "version", proxy["version"])
	addIfPresent(extras, "shadow_tls_password", proxy["shadow-tls-password"])
	for key, value := range proxy {
		if isReservedProxyField(key) {
			continue
		}
		normalizedKey := normalizeExtraKey(key)
		if _, exists := extras[normalizedKey]; exists {
			continue
		}
		addIfPresent(extras, normalizedKey, value)
	}

	return normalizeNode(
		asString(proxy["name"]),
		asString(proxy["server"]),
		asInt(proxy["port"]),
		protocol,
		extras,
	)
}

func looksLikeClashPayload(content string) bool {
	return strings.Contains(content, "proxies:") || looksLikeClashConfig(content)
}

func clashDiagnosticFromError(err error) ImportDiagnostic {
	switch {
	case err == nil:
		return ImportDiagnostic{}
	case strings.Contains(err.Error(), "missing server or port"):
		return ImportDiagnostic{Code: "missing_required_fields", Message: "缺少 server 或 port"}
	case strings.Contains(err.Error(), "unsupported protocol"):
		return ImportDiagnostic{Code: "unsupported_protocol", Message: "协议暂不支持"}
	default:
		return ImportDiagnostic{Code: "invalid_proxy", Message: "节点配置无效"}
	}
}

func uriDiagnosticFromError(err error) ImportDiagnostic {
	switch {
	case err == nil:
		return ImportDiagnostic{}
	case strings.Contains(err.Error(), "missing server or port"):
		return ImportDiagnostic{Code: "missing_required_fields", Message: "缺少 server 或 port"}
	case strings.Contains(err.Error(), "unsupported protocol"):
		return ImportDiagnostic{Code: "unsupported_protocol", Message: "协议暂不支持"}
	default:
		return ImportDiagnostic{Code: "invalid_uri", Message: "URI 格式无效"}
	}
}

func finalizeImportResult(report ImportResult, err error) (ImportResult, error) {
	report.ImportedCount = len(report.Nodes)
	report.SkippedCount = 0
	for _, diagnostic := range report.Diagnostics {
		report.SkippedCount += diagnostic.Count
	}
	report.Summary = buildImportSummary(report)
	return report, err
}

func buildImportSummary(report ImportResult) string {
	switch {
	case report.ImportedCount > 0 && report.SkippedCount == 0:
		return fmt.Sprintf("已导入 %d 个节点", report.ImportedCount)
	case report.ImportedCount > 0:
		return fmt.Sprintf(
			"已导入 %d 个节点，跳过 %d 个（%s）",
			report.ImportedCount,
			report.SkippedCount,
			describeDiagnostics(report.Diagnostics),
		)
	case report.SkippedCount > 0:
		return fmt.Sprintf("没有成功导入任何节点，已跳过 %d 个（%s）", report.SkippedCount, describeDiagnostics(report.Diagnostics))
	default:
		return "订阅内容里没有可识别的节点"
	}
}

func describeDiagnostics(items []ImportDiagnostic) string {
	if len(items) == 0 {
		return "没有可识别的节点"
	}

	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Count <= 0 || strings.TrimSpace(item.Message) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d 个%s", item.Count, item.Message))
	}
	if len(parts) == 0 {
		return "没有可识别的节点"
	}
	return strings.Join(parts, "，")
}

func (r *ImportResult) addDiagnostic(diagnostic ImportDiagnostic) {
	if diagnostic.Count <= 0 {
		diagnostic.Count = 1
	}
	for i := range r.Diagnostics {
		if r.Diagnostics[i].Code == diagnostic.Code && r.Diagnostics[i].Message == diagnostic.Message {
			r.Diagnostics[i].Count += diagnostic.Count
			return
		}
	}
	r.Diagnostics = append(r.Diagnostics, diagnostic)
}

func hasIgnoredScheme(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https", "file":
		return true
	default:
		return false
	}
}
