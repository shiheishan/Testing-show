package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host                        string
	Port                        int
	DBPath                      string
	SubscriptionsPath           string
	CheckInterval               time.Duration
	CheckConcurrency            int
	CheckTimeout                time.Duration
	CheckRetention              time.Duration
	ManualCheckCooldown         time.Duration
	ProxyCheckEnabled           bool
	ProxyCheckURL               string
	MihomoPath                  string
	MihomoStartTimeout          time.Duration
	SubscriptionUA              string
	SubscriptionTimeout         time.Duration
	SubscriptionProxy           string
	SubscriptionRefreshInterval time.Duration
	Subscriptions               []ConfiguredSubscription
}

type yamlNode map[string]any

type ConfiguredSubscription struct {
	Name string `yaml:"name" json:"name"`
	URL  string `yaml:"url" json:"url"`
}

func LoadConfig(configPath string) (Config, error) {
	cfg := Config{
		Host:                "0.0.0.0",
		Port:                8080,
		DBPath:              "./data/nodes.db",
		SubscriptionsPath:   "subscriptions.yaml",
		CheckInterval:       time.Minute,
		CheckConcurrency:    50,
		CheckTimeout:        5 * time.Second,
		CheckRetention:      24 * time.Hour,
		ManualCheckCooldown: 15 * time.Second,
		ProxyCheckEnabled:   true,
		ProxyCheckURL:       "https://www.gstatic.com/generate_204",
		MihomoStartTimeout:  8 * time.Second,
		SubscriptionUA:      "ClashVerge",
		SubscriptionTimeout: 10 * time.Second,
		Subscriptions:       []ConfiguredSubscription{},
	}

	path := configPath
	if path == "" {
		path = "config.yaml"
	}
	configDir := filepath.Dir(path)
	if _, err := os.Stat(path); err == nil {
		root, err := parseSimpleYAML(path)
		if err != nil {
			return cfg, err
		}
		if value := getString(root, "server", "host"); value != "" {
			cfg.Host = value
		}
		if value := getInt(root, "server", "port"); value != 0 {
			cfg.Port = value
		}
		if value := getString(root, "db", "path"); value != "" {
			cfg.DBPath = value
		}
		if value := getString(root, "db_path"); value != "" {
			cfg.DBPath = value
		}
		if value := getString(root, "subscriptions_path"); value != "" {
			cfg.SubscriptionsPath = value
		}
		if value := getString(root, "subscription", "config_path"); value != "" {
			cfg.SubscriptionsPath = value
		}
		if value := getDuration(root, "check", "interval"); value != 0 {
			cfg.CheckInterval = value
		}
		if value := getInt(root, "check", "concurrency"); value != 0 {
			cfg.CheckConcurrency = value
		}
		if value := getDuration(root, "check", "timeout"); value != 0 {
			cfg.CheckTimeout = value
		}
		if value := getDuration(root, "check", "retention"); value != 0 {
			cfg.CheckRetention = value
		}
		if value := getDuration(root, "check", "manual_cooldown"); value != 0 {
			cfg.ManualCheckCooldown = value
		}
		if value := getBool(root, "check", "proxy_enabled"); value != nil {
			cfg.ProxyCheckEnabled = *value
		}
		if value := getString(root, "check", "proxy_url"); value != "" {
			cfg.ProxyCheckURL = value
		}
		if value := getString(root, "check", "mihomo_path"); value != "" {
			cfg.MihomoPath = value
		}
		if value := getDuration(root, "check", "mihomo_start_timeout"); value != 0 {
			cfg.MihomoStartTimeout = value
		}
		if value := getString(root, "subscription", "user_agent"); value != "" {
			cfg.SubscriptionUA = value
		}
		if value := getDuration(root, "subscription", "timeout"); value != 0 {
			cfg.SubscriptionTimeout = value
		}
		if value := getString(root, "subscription", "proxy"); value != "" {
			cfg.SubscriptionProxy = value
		}
		if value := getDuration(root, "subscription", "refresh_interval"); value != 0 {
			cfg.SubscriptionRefreshInterval = value
		}
		subscriptions, err := getConfiguredSubscriptions(root)
		if err != nil {
			return cfg, err
		}
		cfg.Subscriptions = subscriptions
	}

	overrideString(&cfg.Host, "HOST")
	overrideInt(&cfg.Port, "PORT")
	overrideString(&cfg.DBPath, "DB_PATH")
	overrideString(&cfg.SubscriptionsPath, "SUBSCRIPTIONS_PATH")
	overrideDuration(&cfg.CheckInterval, "CHECK_INTERVAL")
	overrideInt(&cfg.CheckConcurrency, "CHECK_CONCURRENCY")
	overrideDuration(&cfg.CheckTimeout, "CHECK_TIMEOUT")
	overrideDuration(&cfg.CheckRetention, "CHECK_RETENTION")
	overrideDuration(&cfg.ManualCheckCooldown, "MANUAL_CHECK_COOLDOWN")
	overrideBool(&cfg.ProxyCheckEnabled, "PROXY_CHECK_ENABLED")
	overrideString(&cfg.ProxyCheckURL, "PROXY_CHECK_URL")
	overrideString(&cfg.MihomoPath, "MIHOMO_PATH")
	overrideDuration(&cfg.MihomoStartTimeout, "MIHOMO_START_TIMEOUT")
	overrideString(&cfg.SubscriptionUA, "SUB_USER_AGENT")
	overrideDuration(&cfg.SubscriptionTimeout, "SUB_TIMEOUT")
	overrideDuration(&cfg.SubscriptionRefreshInterval, "SUB_REFRESH_INTERVAL")
	// HTTP_PROXY enables fetching subscriptions through a proxy
	// (e.g., http://127.0.0.1:7890 or http://user:pass@host:port)
	if strings.TrimSpace(cfg.SubscriptionProxy) == "" {
		overrideString(&cfg.SubscriptionProxy, "HTTP_PROXY")
	}

	subscriptionsPath := cfg.SubscriptionsPath
	if strings.TrimSpace(subscriptionsPath) != "" && !filepath.IsAbs(subscriptionsPath) {
		subscriptionsPath = filepath.Join(configDir, subscriptionsPath)
	}
	if externalSubscriptions, err := loadExternalSubscriptions(subscriptionsPath); err != nil {
		return cfg, err
	} else if len(externalSubscriptions) > 0 {
		cfg.Subscriptions = externalSubscriptions
	}

	if cfg.CheckConcurrency <= 0 {
		return cfg, fmt.Errorf("CHECK_CONCURRENCY must be positive")
	}
	if cfg.SubscriptionRefreshInterval <= 0 {
		cfg.SubscriptionRefreshInterval = 5 * time.Minute
	}
	if cfg.ManualCheckCooldown < 0 {
		cfg.ManualCheckCooldown = 0
	}
	if strings.TrimSpace(cfg.ProxyCheckURL) == "" {
		cfg.ProxyCheckURL = "https://www.gstatic.com/generate_204"
	}
	if cfg.MihomoStartTimeout <= 0 {
		cfg.MihomoStartTimeout = 8 * time.Second
	}

	cfg.DBPath = filepath.Clean(cfg.DBPath)
	cfg.SubscriptionsPath = filepath.Clean(cfg.SubscriptionsPath)
	return cfg, nil
}

func loadExternalSubscriptions(path string) ([]ConfiguredSubscription, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	root, err := parseSimpleYAML(path)
	if err != nil {
		return nil, err
	}
	return getConfiguredSubscriptions(root)
}

func overrideString(target *string, key string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		*target = value
	}
}

func overrideInt(target *int, key string) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return
	}
	value, err := strconv.Atoi(raw)
	if err == nil {
		*target = value
	}
}

func overrideDuration(target *time.Duration, key string) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return
	}
	value, err := parseDuration(raw)
	if err == nil {
		*target = value
	}
}

func overrideBool(target *bool, key string) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return
	}
	value, ok := parseBool(raw)
	if ok {
		*target = value
	}
}

func parseDuration(raw string) (time.Duration, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return 0, nil
	}
	if strings.HasSuffix(value, "ms") {
		ms, err := strconv.Atoi(strings.TrimSuffix(value, "ms"))
		if err != nil {
			return 0, err
		}
		return time.Duration(ms) * time.Millisecond, nil
	}
	unit := value[len(value)-1]
	switch unit {
	case 's', 'm', 'h':
		return time.ParseDuration(value)
	case 'd':
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	default:
		seconds, err := strconv.Atoi(value)
		if err != nil {
			return 0, err
		}
		return time.Duration(seconds) * time.Second, nil
	}
}

func parseSimpleYAML(path string) (yamlNode, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	root := yamlNode{}
	lines := strings.Split(string(content), "\n")
	var currentSection string
	var currentItem yamlNode

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent%2 != 0 {
			return nil, fmt.Errorf("config.yaml uses 2-space indentation")
		}

		switch indent {
		case 0:
			currentSection = ""
			currentItem = nil

			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid config line: %s", line)
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if value == "" {
				root[key] = yamlNode{}
				currentSection = key
				continue
			}
			root[key] = parseScalar(value)

		case 2:
			if currentSection == "" {
				return nil, fmt.Errorf("invalid indentation: %s", line)
			}
			if strings.HasPrefix(trimmed, "- ") {
				list, ok := root[currentSection].([]any)
				if !ok {
					list = []any{}
				}
				itemContent := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				if itemContent == "" {
					currentItem = yamlNode{}
					list = append(list, currentItem)
					root[currentSection] = list
					continue
				}
				parts := strings.SplitN(itemContent, ":", 2)
				if len(parts) != 2 {
					list = append(list, parseScalar(itemContent))
					root[currentSection] = list
					currentItem = nil
					continue
				}
				currentItem = yamlNode{
					strings.TrimSpace(parts[0]): parseScalar(strings.TrimSpace(parts[1])),
				}
				list = append(list, currentItem)
				root[currentSection] = list
				continue
			}

			section, ok := root[currentSection].(yamlNode)
			if !ok {
				return nil, fmt.Errorf("%s must be an object", currentSection)
			}
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid config line: %s", line)
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if value == "" {
				section[key] = yamlNode{}
				continue
			}
			section[key] = parseScalar(value)

		case 4:
			if currentItem == nil {
				return nil, fmt.Errorf("invalid list item indentation: %s", line)
			}
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid config line: %s", line)
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			currentItem[key] = parseScalar(value)

		default:
			return nil, fmt.Errorf("unsupported config indentation: %s", line)
		}
	}
	return root, nil
}

func parseScalar(raw string) any {
	value := strings.TrimSpace(raw)
	switch value {
	case "[]":
		return []any{}
	case "{}":
		return yamlNode{}
	}
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	lower := strings.ToLower(value)
	switch lower {
	case "true", "yes":
		return true
	case "false", "no":
		return false
	case "null", "~":
		return nil
	}
	if i, err := strconv.Atoi(value); err == nil {
		return i
	}
	return value
}

func getNested(root yamlNode, keys ...string) any {
	current := any(root)
	for _, key := range keys {
		node, ok := current.(yamlNode)
		if !ok {
			return nil
		}
		current = node[key]
	}
	return current
}

func getString(root yamlNode, keys ...string) string {
	if value, ok := getNested(root, keys...).(string); ok {
		return value
	}
	return ""
}

func getInt(root yamlNode, keys ...string) int {
	if value, ok := getNested(root, keys...).(int); ok {
		return value
	}
	return 0
}

func getBool(root yamlNode, keys ...string) *bool {
	value := getNested(root, keys...)
	switch v := value.(type) {
	case bool:
		return &v
	case string:
		parsed, ok := parseBool(v)
		if ok {
			return &parsed
		}
	}
	return nil
}

func getConfiguredSubscriptions(root yamlNode) ([]ConfiguredSubscription, error) {
	raw, ok := getNested(root, "subscriptions").([]any)
	if !ok || len(raw) == 0 {
		return []ConfiguredSubscription{}, nil
	}
	items := make([]ConfiguredSubscription, 0, len(raw))
	seen := map[string]struct{}{}
	for index, item := range raw {
		node, ok := item.(yamlNode)
		if !ok {
			return nil, fmt.Errorf("subscriptions[%d] must be an object", index)
		}
		urlValue := strings.TrimSpace(getString(node, "url"))
		if urlValue == "" {
			return nil, fmt.Errorf("subscriptions[%d].url is required", index)
		}
		if _, exists := seen[urlValue]; exists {
			return nil, fmt.Errorf("duplicate subscription url in config: %s", urlValue)
		}
		seen[urlValue] = struct{}{}
		items = append(items, ConfiguredSubscription{
			Name: strings.TrimSpace(getString(node, "name")),
			URL:  urlValue,
		})
	}
	return items, nil
}

func getDuration(root yamlNode, keys ...string) time.Duration {
	value := getNested(root, keys...)
	switch v := value.(type) {
	case string:
		duration, err := parseDuration(v)
		if err == nil {
			return duration
		}
	case int:
		return time.Duration(v) * time.Second
	}
	return 0
}

func parseBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "yes", "1", "on":
		return true, true
	case "false", "no", "0", "off":
		return false, true
	default:
		return false, false
	}
}
