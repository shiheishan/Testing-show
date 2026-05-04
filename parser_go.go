package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

var (
	errClashConfigNoProxies = errors.New("clash config contains no proxies")
	errNoSupportedNodes     = errors.New("subscription contains no supported nodes")
)

type ParsedNode struct {
	Name        string         `json:"name"`
	Server      string         `json:"server"`
	Port        int            `json:"port"`
	Protocol    string         `json:"protocol"`
	ExtraParams map[string]any `json:"extra_params"`
}

type proxyMap map[string]any

func ParseSubscription(content string) ([]ParsedNode, error) {
	report, err := ParseSubscriptionDetailed(content)
	if err != nil {
		return nil, err
	}
	return report.Nodes, nil
}

func parseClashYAML(content string) ([]ParsedNode, error) {
	report, err := parseClashYAMLDetailed(content)
	if err != nil {
		return nil, err
	}
	return report.Nodes, nil
}

func parseURILines(content string) ([]ParsedNode, error) {
	report, err := parseURILinesDetailed(content)
	if err != nil {
		return nil, err
	}
	return report.Nodes, nil
}

func parseSSURI(raw string) (ParsedNode, error) {
	body := strings.TrimPrefix(raw, "ss://")
	fragment := ""
	if parts := strings.SplitN(body, "#", 2); len(parts) == 2 {
		body = parts[0]
		decodedFragment, _ := url.QueryUnescape(parts[1])
		fragment = decodedFragment
	}
	query := ""
	if parts := strings.SplitN(body, "?", 2); len(parts) == 2 {
		body = parts[0]
		query = parts[1]
	}
	decoded, err := safeBase64Decode(body)
	if err == nil && strings.Contains(decoded, "@") {
		body = decoded
	}
	segments := strings.SplitN(body, "@", 2)
	if len(segments) != 2 {
		return ParsedNode{}, fmt.Errorf("invalid ss uri")
	}
	credentials := strings.SplitN(segments[0], ":", 2)
	if len(credentials) != 2 {
		return ParsedNode{}, fmt.Errorf("invalid ss credentials")
	}
	hostParts := strings.Split(segments[1], ":")
	if len(hostParts) < 2 {
		return ParsedNode{}, fmt.Errorf("invalid ss address")
	}
	port, _ := strconv.Atoi(hostParts[len(hostParts)-1])
	server := strings.Join(hostParts[:len(hostParts)-1], ":")
	queryValues, _ := url.ParseQuery(query)
	method := credentials[0]
	protocol := "ss"
	if isSS2022Cipher(method) {
		protocol = "ss2022"
	}
	return normalizeNode(fragment, server, port, protocol, map[string]any{
		"method":   credentials[0],
		"password": credentials[1],
		"plugin":   firstQuery(queryValues, "plugin"),
	})
}

func parseSSRURI(raw string) (ParsedNode, error) {
	decoded, err := safeBase64Decode(strings.TrimPrefix(raw, "ssr://"))
	if err != nil {
		return ParsedNode{}, err
	}
	mainPart := decoded
	query := ""
	if parts := strings.SplitN(decoded, "/?", 2); len(parts) == 2 {
		mainPart = parts[0]
		query = parts[1]
	}
	parts := strings.Split(mainPart, ":")
	if len(parts) < 6 {
		return ParsedNode{}, fmt.Errorf("invalid ssr uri")
	}
	port, _ := strconv.Atoi(parts[1])
	queryValues, _ := url.ParseQuery(query)
	name := fmt.Sprintf("%s:%d", parts[0], port)
	if encoded := firstQuery(queryValues, "remarks"); encoded != "" {
		if decodedName, err := safeBase64Decode(encoded); err == nil && decodedName != "" {
			name = decodedName
		}
	}
	password, _ := safeBase64Decode(parts[5])
	protocolParam := ""
	if encoded := firstQuery(queryValues, "protoparam"); encoded != "" {
		protocolParam, _ = safeBase64Decode(encoded)
	}
	obfsParam := ""
	if encoded := firstQuery(queryValues, "obfsparam"); encoded != "" {
		obfsParam, _ = safeBase64Decode(encoded)
	}
	return normalizeNode(name, parts[0], port, "ssr", map[string]any{
		"method":         parts[3],
		"protocol":       parts[2],
		"protocol_param": protocolParam,
		"obfs":           parts[4],
		"obfs_param":     obfsParam,
		"password":       password,
	})
}

func parseVMessURI(raw string) (ParsedNode, error) {
	decoded, err := safeBase64Decode(strings.TrimPrefix(raw, "vmess://"))
	if err != nil {
		return ParsedNode{}, err
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(decoded), &payload); err != nil {
		return ParsedNode{}, err
	}
	return normalizeNode(
		asString(payload["ps"]),
		asString(payload["add"]),
		asInt(payload["port"]),
		"vmess",
		map[string]any{
			"uuid":     payload["id"],
			"alterId":  payload["aid"],
			"security": payload["scy"],
			"network":  payload["net"],
			"path":     payload["path"],
			"host":     payload["host"],
			"tls":      payload["tls"],
			"sni":      payload["sni"],
		},
	)
}

func parseGenericURI(raw string, protocol string) (ParsedNode, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ParsedNode{}, err
	}
	port := defaultPortForURI(parsed)
	name, _ := url.QueryUnescape(parsed.Fragment)
	if name == "" {
		name = fmt.Sprintf("%s:%d", parsed.Hostname(), port)
	}
	extras := map[string]any{}
	if parsed.User != nil {
		addUserInfoExtras(protocol, parsed.User, extras)
	}
	query := parsed.Query()
	for key, values := range query {
		if len(values) == 0 {
			continue
		}
		normalizedKey := normalizeExtraKey(key)
		switch protocol {
		case "hysteria":
			switch normalizedKey {
			case "auth":
				normalizedKey = "auth_str"
			case "upmbps":
				normalizedKey = "up_mbps"
			case "downmbps":
				normalizedKey = "down_mbps"
			}
		case "hy2":
			if normalizedKey == "auth" {
				normalizedKey = "password"
			}
		case "tuic":
			switch normalizedKey {
			case "congestioncontroller":
				normalizedKey = "congestion_controller"
			case "udp_relay_mode":
				normalizedKey = "udp_relay_mode"
			}
		}
		extras[normalizedKey] = values[0]
	}
	return normalizeNode(name, parsed.Hostname(), port, protocol, extras)
}

func normalizeNode(name, server string, port int, protocol string, extras map[string]any) (ParsedNode, error) {
	protocol = detectProtocol(protocol, extras)
	if protocol == "" {
		return ParsedNode{}, fmt.Errorf("unsupported protocol")
	}
	if server == "" || port == 0 {
		return ParsedNode{}, fmt.Errorf("node missing server or port")
	}
	cleanExtras := make(map[string]any, len(extras))
	for key, value := range extras {
		if value == nil {
			continue
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				continue
			}
			cleanExtras[key] = v
		default:
			cleanExtras[key] = v
		}
	}
	if strings.TrimSpace(name) == "" {
		name = fmt.Sprintf("%s:%d", server, port)
	}
	return ParsedNode{
		Name:        name,
		Server:      server,
		Port:        port,
		Protocol:    protocol,
		ExtraParams: cleanExtras,
	}, nil
}

func detectProtocol(raw string, extras map[string]any) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "ss" {
		if isSS2022Cipher(asString(firstNonEmpty(extras["method"], extras["cipher"], extras["security"]))) {
			return "ss2022"
		}
	}
	return normalizeProtocol(raw)
}

func normalizeProtocol(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ss":
		return "ss"
	case "ss2022":
		return "ss2022"
	case "ssr":
		return "ssr"
	case "vmess":
		return "vmess"
	case "vless":
		return "vless"
	case "trojan":
		return "trojan"
	case "hysteria":
		return "hysteria"
	case "hy2", "hysteria2":
		return "hy2"
	case "tuic":
		return "tuic"
	case "anytls":
		return "anytls"
	case "shadowtls", "shadow-tls":
		return "shadowtls"
	case "naive", "naiveproxy", "naive+https":
		return "naiveproxy"
	case "wireguard", "wg":
		return "wireguard"
	default:
		return ""
	}
}

func addUserInfoExtras(protocol string, user *url.Userinfo, extras map[string]any) {
	if user == nil {
		return
	}
	username := user.Username()
	password, hasPassword := user.Password()
	switch protocol {
	case "trojan":
		if hasPassword {
			addIfPresent(extras, "password", password)
			return
		}
		addIfPresent(extras, "password", username)
	case "vless":
		addIfPresent(extras, "uuid", username)
	case "hysteria":
		addIfPresent(extras, "auth_str", username)
	case "hy2", "anytls", "shadowtls", "ss2022":
		addIfPresent(extras, "password", username)
	case "tuic":
		addIfPresent(extras, "uuid", username)
		if hasPassword {
			addIfPresent(extras, "password", password)
		}
	case "naiveproxy":
		addIfPresent(extras, "username", username)
		if hasPassword {
			addIfPresent(extras, "password", password)
		}
	case "wireguard":
		addIfPresent(extras, "private_key", username)
		if hasPassword {
			addIfPresent(extras, "public_key", password)
		}
	default:
		if hasPassword {
			addIfPresent(extras, "password", password)
			return
		}
		addIfPresent(extras, "password", username)
	}
}

func defaultPortForURI(parsed *url.URL) int {
	if parsed == nil {
		return 0
	}
	if portText := parsed.Port(); portText != "" {
		port, _ := strconv.Atoi(portText)
		return port
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "naive+https":
		return 443
	case "http":
		return 80
	default:
		return 0
	}
}

func normalizeExtraKey(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	key = strings.ReplaceAll(key, "-", "_")
	return key
}

func isReservedProxyField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "name", "type", "server", "port":
		return true
	default:
		return false
	}
}

func isSS2022Cipher(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "2022-")
}

func looksLikeClashConfig(content string) bool {
	if strings.Contains(content, "proxy-groups:") || strings.Contains(content, "rules:") || strings.Contains(content, "dns:") {
		return true
	}
	return false
}

func safeBase64Decode(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	value = strings.ReplaceAll(value, "-", "+")
	value = strings.ReplaceAll(value, "_", "/")
	if mod := len(value) % 4; mod != 0 {
		value += strings.Repeat("=", 4-mod)
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func parseYAMLScalar(raw string) any {
	value := strings.TrimSpace(raw)
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

func parseInlineYAMLMap(raw string) proxyMap {
	body := strings.TrimSpace(raw)
	body = strings.TrimPrefix(body, "{")
	body = strings.TrimSuffix(body, "}")
	result := proxyMap{}
	for _, part := range splitTopLevel(body, ',') {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		keyValue := splitFirstTopLevel(item, ':')
		if len(keyValue) != 2 {
			continue
		}
		key := strings.TrimSpace(keyValue[0])
		value := strings.TrimSpace(keyValue[1])
		if key == "" {
			continue
		}
		result[key] = parseYAMLScalar(value)
	}
	return result
}

func splitFirstTopLevel(raw string, sep rune) []string {
	parts := splitTopLevel(raw, sep)
	if len(parts) <= 1 {
		return parts
	}
	return []string{parts[0], strings.Join(parts[1:], string(sep))}
}

func splitTopLevel(raw string, sep rune) []string {
	var (
		parts       []string
		current     strings.Builder
		singleQuote bool
		doubleQuote bool
		braces      int
		brackets    int
	)
	for _, ch := range raw {
		switch ch {
		case '\'':
			if !doubleQuote {
				singleQuote = !singleQuote
			}
		case '"':
			if !singleQuote {
				doubleQuote = !doubleQuote
			}
		case '{':
			if !singleQuote && !doubleQuote {
				braces++
			}
		case '}':
			if !singleQuote && !doubleQuote && braces > 0 {
				braces--
			}
		case '[':
			if !singleQuote && !doubleQuote {
				brackets++
			}
		case ']':
			if !singleQuote && !doubleQuote && brackets > 0 {
				brackets--
			}
		}
		if ch == sep && !singleQuote && !doubleQuote && braces == 0 && brackets == 0 {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(ch)
	}
	parts = append(parts, current.String())
	return parts
}

func asString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func asInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		i, _ := strconv.Atoi(v)
		return i
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func addIfPresent(target map[string]any, key string, value any) {
	if value == nil {
		return
	}
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return
		}
		target[key] = v
	default:
		target[key] = value
	}
}

func firstNonEmpty(values ...any) any {
	for _, value := range values {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			if strings.TrimSpace(text) == "" {
				continue
			}
		}
		return value
	}
	return nil
}

func firstQuery(values url.Values, key string) string {
	if items := values[key]; len(items) > 0 {
		return items[0]
	}
	return ""
}
