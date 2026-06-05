package main

import (
	"net/http"
	"testing"
)

func TestValidateDoHEndpointBlocksLocalHosts(t *testing.T) {
	tests := []string{
		"https://127.0.0.1/dns-query",
		"https://10.0.0.1/dns-query",
		"https://localhost/dns-query",
	}
	for _, raw := range tests {
		endpoint, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatalf("NewRequest(%q) error: %v", raw, err)
		}
		if err := validateDoHEndpoint(endpoint.URL); err == nil {
			t.Fatalf("expected %q to be blocked", raw)
		}
	}
}

func TestValidateDoHEndpointAllowsPublicProviders(t *testing.T) {
	tests := []string{
		"https://dns.alidns.com/dns-query",
		"https://doh.pub/dns-query",
		"https://cloudflare-dns.com/dns-query",
	}
	for _, raw := range tests {
		endpoint, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatalf("NewRequest(%q) error: %v", raw, err)
		}
		if err := validateDoHEndpoint(endpoint.URL); err != nil {
			t.Fatalf("expected %q to be allowed, got %v", raw, err)
		}
	}
}

func TestValidateDoHEndpointRejectsNonHTTPS(t *testing.T) {
	endpoint, err := http.NewRequest(http.MethodGet, "http://dns.example.com/dns-query", nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	if err := validateDoHEndpoint(endpoint.URL); err == nil {
		t.Fatal("expected non-https DoH endpoint to be rejected")
	}
}
