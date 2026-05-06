package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLookupHostWithDoHReadsJSONAnswers(t *testing.T) {
	originalTransport := httpRoundTripper
	httpRoundTripper = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("name") != "sanwang.woainilzr.com" {
			t.Fatalf("query name = %q", r.URL.Query().Get("name"))
		}
		if r.URL.Path != "/resolve" {
			t.Fatalf("path = %q, want /resolve", r.URL.Path)
		}
		if r.URL.Query().Get("type") != "A" {
			t.Fatalf("query type = %q", r.URL.Query().Get("type"))
		}
		if r.Header.Get("Accept") != "application/dns-json" {
			t.Fatalf("accept = %q", r.Header.Get("Accept"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(`{
				"Status": 0,
				"Answer": [
					{"type": 5, "data": "sanwang.woainiliz.com."},
					{"type": 1, "data": "13.231.111.214"}
				]
			}`)),
			Request: r,
		}, nil
	})
	defer func() {
		httpRoundTripper = originalTransport
	}()

	ips, err := lookupHostWithDoH("sanwang.woainilzr.com", "https://dns.alidns.com/dns-query", time.Second)
	if err != nil {
		t.Fatalf("lookupHostWithDoH error: %v", err)
	}
	if len(ips) != 1 || ips[0] != "13.231.111.214" {
		t.Fatalf("ips = %+v, want 13.231.111.214", ips)
	}
}

func TestNormalizeDoHJSONEndpointMapsKnownProviders(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "https://dns.alidns.com/dns-query", want: "https://dns.alidns.com/resolve"},
		{raw: "https://dns.alidns.com/resolve", want: "https://dns.alidns.com/resolve"},
		{raw: "https://doh.pub/dns-query", want: "https://doh.pub/resolve"},
		{raw: "https://cloudflare-dns.com/dns-query", want: "https://cloudflare-dns.com/dns-query"},
	}
	for _, tt := range tests {
		if got := normalizeDoHJSONEndpoint(tt.raw); got != tt.want {
			t.Fatalf("normalizeDoHJSONEndpoint(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestMihomoNameserversPrefersProxyServerNameserver(t *testing.T) {
	got := mihomoNameservers(map[string]any{
		"proxy-server-nameserver": []any{"https://proxy.example/dns-query"},
		"nameserver":              []any{"https://normal.example/dns-query"},
		"fallback":                []any{"https://fallback.example/dns-query"},
	})
	want := []string{
		"https://proxy.example/dns-query",
		"https://normal.example/dns-query",
		"https://fallback.example/dns-query",
	}
	if len(got) != len(want) {
		t.Fatalf("nameservers = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("nameservers = %+v, want %+v", got, want)
		}
	}
}
