package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// nodeMihomoDNS returns the per-node DNS config carried in ExtraParams, used to
// group nodes by DNS when building persistent mihomo instances.
func nodeMihomoDNS(node NodeRecord) map[string]any {
	if node.ExtraParams == nil {
		return nil
	}
	dns, ok := node.ExtraParams["_mihomo_dns"].(map[string]any)
	if !ok || len(dns) == 0 {
		return nil
	}
	return dns
}

// validateDoHEndpoint enforces the SSRF guard advertised in the README: a DoH
// endpoint must be https and must not resolve to localhost or an internal /
// link-local address. It is applied when a node's DNS config is folded into a
// generated mihomo config (T4), so a malicious subscription cannot point the
// proxy resolver at an internal service.
func validateDoHEndpoint(endpoint *url.URL) error {
	if endpoint == nil || endpoint.Scheme != "https" || strings.TrimSpace(endpoint.Hostname()) == "" {
		return fmt.Errorf("invalid doh endpoint")
	}
	if isBlockedDoHHost(endpoint.Hostname()) {
		return fmt.Errorf("blocked doh endpoint host %q", endpoint.Hostname())
	}
	return nil
}

func isBlockedDoHHost(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func stringListFromAny(value any) []string {
	switch v := value.(type) {
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if text := strings.TrimSpace(asString(item)); text != "" {
				result = append(result, text)
			}
		}
		return result
	case []string:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if text := strings.TrimSpace(item); text != "" {
				result = append(result, text)
			}
		}
		return result
	case string:
		if text := strings.TrimSpace(v); text != "" {
			return []string{text}
		}
	}
	return nil
}

func dedupeStrings(items []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item)
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		result = append(result, text)
	}
	return result
}
