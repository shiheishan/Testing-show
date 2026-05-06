package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type dnsAnswer struct {
	Type int    `json:"type"`
	Data string `json:"data"`
}

type dnsJSONResponse struct {
	Status int         `json:"Status"`
	Answer []dnsAnswer `json:"Answer"`
}

func resolveNodeServer(node NodeRecord, timeout time.Duration) ([]string, error) {
	if net.ParseIP(node.Server) != nil {
		return []string{node.Server}, nil
	}
	dns := nodeMihomoDNS(node)
	if len(dns) == 0 {
		return nil, nil
	}
	nameservers := mihomoNameservers(dns)
	if len(nameservers) == 0 {
		return nil, nil
	}
	return lookupHostWithNameservers(node.Server, nameservers, timeout)
}

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

func mihomoNameservers(dns map[string]any) []string {
	var result []string
	for _, key := range []string{"proxy-server-nameserver", "nameserver", "fallback", "default-nameserver"} {
		result = append(result, stringListFromAny(dns[key])...)
	}
	return dedupeStrings(result)
}

func lookupHostWithNameservers(host string, nameservers []string, timeout time.Duration) ([]string, error) {
	var lastErr error
	for _, nameserver := range nameservers {
		items, err := lookupHostWithNameserver(host, nameserver, timeout)
		if err == nil && len(items) > 0 {
			return items, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func lookupHostWithNameserver(host string, nameserver string, timeout time.Duration) ([]string, error) {
	nameserver = strings.TrimSpace(nameserver)
	if nameserver == "" {
		return nil, nil
	}
	if strings.HasPrefix(nameserver, "http://") {
		return nil, fmt.Errorf("insecure doh nameserver %q is not supported", nameserver)
	}
	if strings.HasPrefix(nameserver, "https://") {
		return lookupHostWithDoH(host, nameserver, timeout)
	}
	return lookupHostWithUDP(host, nameserver, timeout)
}

func lookupHostWithDoH(host string, endpoint string, timeout time.Duration) ([]string, error) {
	endpoint = normalizeDoHJSONEndpoint(endpoint)
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if err := validateDoHEndpoint(requestURL); err != nil {
		return nil, err
	}
	query := requestURL.Query()
	query.Set("name", host)
	query.Set("type", "A")
	requestURL.RawQuery = query.Encode()

	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	request, err := http.NewRequest(http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/dns-json")
	client := &http.Client{Timeout: timeout, Transport: httpRoundTripper}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("doh %s returned %s", endpoint, response.Status)
	}

	var payload dnsJSONResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return ipsFromDNSAnswers(payload.Answer), nil
}

func normalizeDoHJSONEndpoint(endpoint string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if strings.Contains(trimmed, "dns.alidns.com") || strings.Contains(trimmed, "alidns.com") {
		return strings.TrimSuffix(strings.TrimSuffix(trimmed, "/dns-query"), "/resolve") + "/resolve"
	}
	if strings.Contains(trimmed, "doh.pub") {
		return strings.TrimSuffix(strings.TrimSuffix(trimmed, "/dns-query"), "/resolve") + "/resolve"
	}
	return strings.TrimSuffix(trimmed, "/dns-query") + "/dns-query"
}

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

func ipsFromDNSAnswers(answers []dnsAnswer) []string {
	var result []string
	for _, answer := range answers {
		if answer.Type != 1 {
			continue
		}
		if ip := net.ParseIP(strings.TrimSpace(answer.Data)); ip != nil && ip.To4() != nil {
			result = append(result, ip.String())
		}
	}
	return dedupeStrings(result)
}

func lookupHostWithUDP(host string, nameserver string, timeout time.Duration) ([]string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network string, address string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: timeout}
			return dialer.DialContext(ctx, network, netJoinHostPort(nameserverHost(nameserver), nameserverPort(nameserver)))
		},
	}
	ctx, cancel := contextWithTimeout(contextBackground(), timeout)
	defer cancel()
	return resolver.LookupHost(ctx, host)
}

func nameserverHost(nameserver string) string {
	host, _, err := net.SplitHostPort(nameserver)
	if err == nil {
		return host
	}
	return nameserver
}

func nameserverPort(nameserver string) string {
	_, port, err := net.SplitHostPort(nameserver)
	if err == nil && strings.TrimSpace(port) != "" {
		return port
	}
	return "53"
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
