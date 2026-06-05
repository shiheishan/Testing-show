package main

import (
	"errors"
	"net"
	"testing"
)

// stubResolver swaps lookupHostIPs for the duration of a test. Pass a map of
// host->IPs; unmapped hosts return a resolution error (fail-closed path).
func stubResolver(t *testing.T, table map[string][]string) {
	t.Helper()
	restore := lookupHostIPs
	lookupHostIPs = func(host string) ([]net.IP, error) {
		raw, ok := table[host]
		if !ok {
			return nil, errors.New("no such host")
		}
		ips := make([]net.IP, 0, len(raw))
		for _, s := range raw {
			ips = append(ips, net.ParseIP(s))
		}
		return ips, nil
	}
	t.Cleanup(func() { lookupHostIPs = restore })
}

// Regression: TODOS.md "SSRF guard 不解析主机名 + 漏 CGNAT" (P2, 2026-06-04).
// The guard used to only block IP literals and *.localhost, so a hostname that
// resolves to an internal address (rebind names, metadata fronts) sailed
// through, and CGNAT (100.64.0.0/10) was missing from the IP block list.
func TestIsBlockedDoHHostBlocksInternalIPLiterals(t *testing.T) {
	blocked := []string{
		"127.0.0.1",          // loopback
		"::1",                // loopback v6
		"10.0.0.1",           // RFC1918
		"192.168.1.1",        // RFC1918
		"172.16.0.1",         // RFC1918
		"169.254.169.254",    // link-local (cloud metadata)
		"100.64.0.1",         // CGNAT — the newly covered range
		"100.127.255.255",    // CGNAT upper edge
		"0.0.0.0",            // unspecified
		"fc00::1",            // ULA (private v6)
		"fe80::1",            // link-local v6
		"::ffff:127.0.0.1",   // v4-mapped loopback
		"::ffff:169.254.0.1", // v4-mapped link-local
	}
	for _, host := range blocked {
		if !isBlockedDoHHost(host) {
			t.Errorf("expected %q to be blocked", host)
		}
	}
}

func TestIsBlockedDoHHostAllowsPublicIPLiterals(t *testing.T) {
	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"2606:4700:4700::1111", // Cloudflare v6
		"100.63.255.255",       // just below CGNAT
		"100.128.0.0",          // just above CGNAT
	}
	for _, host := range allowed {
		if isBlockedDoHHost(host) {
			t.Errorf("expected public IP %q to be allowed", host)
		}
	}
}

func TestIsBlockedDoHHostBlocksMetadataHostnames(t *testing.T) {
	// Metadata hostnames are blocked by name regardless of resolution result.
	stubResolver(t, map[string][]string{
		"metadata.google.internal": {"8.8.8.8"}, // even if it "resolves" public
	})
	for _, host := range []string{"metadata", "metadata.google.internal", "metadata.goog", "MeTaData.Google.Internal"} {
		if !isBlockedDoHHost(host) {
			t.Errorf("expected metadata host %q to be blocked", host)
		}
	}
}

func TestIsBlockedDoHHostResolvesHostnames(t *testing.T) {
	stubResolver(t, map[string][]string{
		"rebind.nip.io":      {"127.0.0.1"},           // rebind to loopback
		"internal.corp":      {"10.1.2.3"},            // resolves to RFC1918
		"cgnat.example":      {"100.100.0.1"},         // resolves to CGNAT
		"mixed.example":      {"1.1.1.1", "10.0.0.1"}, // any-internal => blocked
		"dns.public.example": {"1.1.1.1"},             // fully public
	})

	blocked := []string{"rebind.nip.io", "internal.corp", "cgnat.example", "mixed.example"}
	for _, host := range blocked {
		if !isBlockedDoHHost(host) {
			t.Errorf("expected %q (resolves internal) to be blocked", host)
		}
	}
	if isBlockedDoHHost("dns.public.example") {
		t.Error("expected host resolving only to a public IP to be allowed")
	}
}

func TestIsBlockedDoHHostFailsClosedOnResolutionError(t *testing.T) {
	// Unmapped host => resolver returns an error => host cannot be proven safe.
	stubResolver(t, map[string][]string{})
	if !isBlockedDoHHost("unresolvable.example") {
		t.Error("expected resolution failure to fail closed (blocked)")
	}
}

func TestIsSafeMihomoNameserverWithResolution(t *testing.T) {
	stubResolver(t, map[string][]string{
		"rebind.nip.io": {"127.0.0.1"},
		"dns.google":    {"8.8.8.8"},
		"internal.dot":  {"10.0.0.9"},
	})
	cases := []struct {
		entry string
		want  bool
	}{
		{"https://rebind.nip.io/dns-query", false}, // DoH host resolves loopback
		{"https://dns.google/dns-query", true},     // DoH host resolves public
		{"tls://internal.dot", false},              // DoT host resolves RFC1918
		{"tls://dns.google", true},                 // DoT host resolves public
		{"quic://internal.dot", false},             // DoQ likewise
		{"http://dns.google/dns-query", false},     // plaintext DoH always rejected
		{"223.5.5.5", true},                        // plain UDP nameserver kept
		{"system", true},                           // pseudo-resolver kept
	}
	for _, c := range cases {
		if got := isSafeMihomoNameserver(c.entry); got != c.want {
			t.Errorf("isSafeMihomoNameserver(%q) = %v, want %v", c.entry, got, c.want)
		}
	}
}
