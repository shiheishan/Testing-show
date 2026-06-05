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

// cgnatNet is the RFC6598 carrier-grade NAT range (100.64.0.0/10). Go's
// net.IP.IsPrivate covers RFC1918 / RFC4193 but not CGNAT, which is routable to
// internal infra and is a known metadata-fronting path, so it is checked
// explicitly.
var cgnatNet = mustParseCIDR("100.64.0.0/10")

func mustParseCIDR(cidr string) *net.IPNet {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR %q: %v", cidr, err))
	}
	return network
}

// blockedMetadataHosts are cloud metadata / internal service hostnames that must
// never be used as a proxy DNS resolver target, regardless of what they resolve
// to (or whether resolution is even available).
var blockedMetadataHosts = map[string]struct{}{
	"metadata":                 {},
	"metadata.google.internal": {},
	"metadata.goog":            {},
}

// isBlockedIP reports whether an IP must never be used as a resolver target:
// loopback, private (RFC1918/RFC4193), CGNAT (RFC6598), link-local, multicast,
// or unspecified. IPv4-mapped IPv6 addresses are unwrapped by net.IP's methods.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsUnspecified() ||
		cgnatNet.Contains(ip)
}

// isBlockedDoHHost reports whether a DoH/DoT/DoQ host is an SSRF risk. IP
// literals are checked directly; hostnames are resolved and blocked if ANY
// resolved address is internal (catching rebind-style names like
// 127.0.0.1.nip.io and metadata fronts). Resolution failure fails closed: a host
// we cannot prove is safe is not used as a resolver target.
func isBlockedDoHHost(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if host == "" {
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if _, blocked := blockedMetadataHosts[host]; blocked {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isBlockedIP(ip)
	}
	addrs, err := lookupHostIPs(host)
	if err != nil || len(addrs) == 0 {
		return true
	}
	for _, ip := range addrs {
		if isBlockedIP(ip) {
			return true
		}
	}
	return false
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
