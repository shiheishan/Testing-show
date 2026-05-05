package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{raw: "5s", want: 5 * time.Second},
		{raw: "5m", want: 5 * time.Minute},
		{raw: "24h", want: 24 * time.Hour},
		{raw: "2d", want: 48 * time.Hour},
	}
	for _, tt := range tests {
		got, err := parseDuration(tt.raw)
		if err != nil {
			t.Fatalf("parseDuration(%q) error: %v", tt.raw, err)
		}
		if got != tt.want {
			t.Fatalf("parseDuration(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestLoadConfigDefaultsToClashVergeUA(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "missing.yaml")

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir error: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Fatalf("restore wd error: %v", err)
		}
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.SubscriptionUA != "ClashVerge" {
		t.Fatalf("SubscriptionUA = %q, want %q", cfg.SubscriptionUA, "ClashVerge")
	}
}

func TestLoadConfigParsesConfiguredSubscriptions(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	content := `
server:
  port: 9090
subscription:
  refresh_interval: 12m
subscriptions:
  - name: sample
    url: https://example.com/subscription
  - url: file:///tmp/local.yaml
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.Port != 9090 {
		t.Fatalf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.SubscriptionRefreshInterval != 12*time.Minute {
		t.Fatalf("SubscriptionRefreshInterval = %v, want %v", cfg.SubscriptionRefreshInterval, 12*time.Minute)
	}
	if len(cfg.Subscriptions) != 2 {
		t.Fatalf("subscriptions = %d, want 2", len(cfg.Subscriptions))
	}
	if cfg.Subscriptions[0].Name != "sample" || cfg.Subscriptions[0].URL != "https://example.com/subscription" {
		t.Fatalf("unexpected first subscription: %+v", cfg.Subscriptions[0])
	}
	if cfg.Subscriptions[1].URL != "file:///tmp/local.yaml" {
		t.Fatalf("unexpected second subscription: %+v", cfg.Subscriptions[1])
	}
}

func TestLoadConfigParsesProxyCheckSettings(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	content := `
check:
  proxy_enabled: false
  proxy_url: https://example.com/health
  mihomo_path: /opt/mihomo
  mihomo_start_timeout: 3s
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.ProxyCheckEnabled {
		t.Fatal("ProxyCheckEnabled = true, want false")
	}
	if cfg.ProxyCheckURL != "https://example.com/health" {
		t.Fatalf("ProxyCheckURL = %q", cfg.ProxyCheckURL)
	}
	if cfg.MihomoPath != "/opt/mihomo" {
		t.Fatalf("MihomoPath = %q", cfg.MihomoPath)
	}
	if cfg.MihomoStartTimeout != 3*time.Second {
		t.Fatalf("MihomoStartTimeout = %v, want 3s", cfg.MihomoStartTimeout)
	}
}

func TestLoadConfigUsesExternalSubscriptionsFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	subscriptionsPath := filepath.Join(tempDir, "subscriptions.yaml")
	configContent := `
subscription:
  config_path: ` + subscriptionsPath + `
subscriptions:
  - name: stale-inline
    url: https://example.com/stale
`
	subscriptionsContent := `
subscriptions:
  - name: external
    url: https://example.com/external
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile config error: %v", err)
	}
	if err := os.WriteFile(subscriptionsPath, []byte(subscriptionsContent), 0o644); err != nil {
		t.Fatalf("WriteFile subscriptions error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if len(cfg.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(cfg.Subscriptions))
	}
	if cfg.Subscriptions[0].Name != "external" || cfg.Subscriptions[0].URL != "https://example.com/external" {
		t.Fatalf("unexpected external subscription: %+v", cfg.Subscriptions[0])
	}
}

func TestLoadConfigUsesEnvSubscriptionsPathBeforeLoadingExternalFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	fileSubscriptionsPath := filepath.Join(tempDir, "file-subscriptions.yaml")
	envSubscriptionsPath := filepath.Join(tempDir, "env-subscriptions.yaml")
	configContent := `
subscription:
  config_path: ` + fileSubscriptionsPath + `
`
	fileSubscriptionsContent := `
subscriptions:
  - name: file
    url: https://example.com/file
`
	envSubscriptionsContent := `
subscriptions:
  - name: env
    url: https://example.com/env
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile config error: %v", err)
	}
	if err := os.WriteFile(fileSubscriptionsPath, []byte(fileSubscriptionsContent), 0o644); err != nil {
		t.Fatalf("WriteFile file subscriptions error: %v", err)
	}
	if err := os.WriteFile(envSubscriptionsPath, []byte(envSubscriptionsContent), 0o644); err != nil {
		t.Fatalf("WriteFile env subscriptions error: %v", err)
	}
	t.Setenv("SUBSCRIPTIONS_PATH", envSubscriptionsPath)

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if len(cfg.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(cfg.Subscriptions))
	}
	if cfg.Subscriptions[0].Name != "env" || cfg.Subscriptions[0].URL != "https://example.com/env" {
		t.Fatalf("unexpected env subscription: %+v", cfg.Subscriptions[0])
	}
}

func TestLoadConfigRejectsDuplicateSubscriptionURLs(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	content := `
subscriptions:
  - url: https://example.com/subscription
  - url: https://example.com/subscription
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("expected duplicate url error")
	}
}
