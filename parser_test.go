package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

func TestParseClashYAML(t *testing.T) {
	content := `
proxies:
  - name: 香港 01
    type: vmess
    server: hk.example.com
    port: 443
    uuid: 1234
    network: ws
    tls: true
`
	nodes, err := parseClashYAML(content)
	if err != nil {
		t.Fatalf("parseClashYAML error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Protocol != "vmess" {
		t.Fatalf("unexpected protocol: %s", nodes[0].Protocol)
	}
}

func TestParseClashYAMLHysteria(t *testing.T) {
	content := `
proxies:
  - name: 新加坡 Hysteria
    type: hysteria
    server: sg.example.com
    port: 443
    auth-str: hysteria-secret
    obfs: salamander
    up: 100
    down: 200
    sni: sg.example.com
`
	nodes, err := parseClashYAML(content)
	if err != nil {
		t.Fatalf("parseClashYAML error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Protocol != "hysteria" {
		t.Fatalf("unexpected protocol: %s", nodes[0].Protocol)
	}
	if nodes[0].ExtraParams["auth_str"] != "hysteria-secret" {
		t.Fatalf("missing auth_str in extras: %+v", nodes[0].ExtraParams)
	}
}

func TestParseVMessBase64(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"ps":   "Tokyo 02",
		"add":  "jp.example.com",
		"port": "443",
		"id":   "uuid-1",
		"aid":  "0",
		"net":  "ws",
		"tls":  "tls",
	})
	raw := "vmess://" + base64.StdEncoding.EncodeToString(payload)
	nodes, err := parseURILines(raw)
	if err != nil {
		t.Fatalf("parseURILines error: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "Tokyo 02" {
		t.Fatalf("unexpected nodes: %+v", nodes)
	}
}

func TestParseBase64SubscriptionBlob(t *testing.T) {
	raw := "trojan://password@example.com:443?sni=example.com#Example"
	blob := base64.StdEncoding.EncodeToString([]byte(raw))
	nodes, err := ParseSubscription(blob)
	if err != nil {
		t.Fatalf("ParseSubscription error: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Protocol != "trojan" {
		t.Fatalf("unexpected nodes: %+v", nodes)
	}
}

func TestParseHysteriaURI(t *testing.T) {
	raw := "hysteria://hysteria-secret@hysteria.example.com:8443?obfs=salamander&peer=cdn.example.com&upmbps=50&downmbps=100#Hysteria"
	nodes, err := parseURILines(raw)
	if err != nil {
		t.Fatalf("parseURILines error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Protocol != "hysteria" {
		t.Fatalf("unexpected protocol: %s", nodes[0].Protocol)
	}
	if nodes[0].ExtraParams["auth_str"] != "hysteria-secret" {
		t.Fatalf("unexpected extras: %+v", nodes[0].ExtraParams)
	}
}

func TestParseExtendedProtocolURIs(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		protocol  string
		nodeName  string
		server    string
		port      int
		extraKey  string
		extraWant string
	}{
		{
			name:      "VLESS Reality",
			raw:       "vless://uuid-123@example.com:443?security=reality&pbk=test-public-key&sid=abcd&sni=cdn.example.com#VLESS-Reality",
			protocol:  "vless",
			nodeName:  "VLESS-Reality",
			server:    "example.com",
			port:      443,
			extraKey:  "pbk",
			extraWant: "test-public-key",
		},
		{
			name:      "Trojan Go Alias",
			raw:       "trojan-go://secret@example.com:443?type=ws&sni=cdn.example.com#Trojan-Go",
			protocol:  "trojan",
			nodeName:  "Trojan-Go",
			server:    "example.com",
			port:      443,
			extraKey:  "type",
			extraWant: "ws",
		},
		{
			name:      "TUIC",
			raw:       "tuic://uuid-123:pass-456@tuic.example.com:443?congestion_control=bbr&alpn=h3#TUIC",
			protocol:  "tuic",
			nodeName:  "TUIC",
			server:    "tuic.example.com",
			port:      443,
			extraKey:  "uuid",
			extraWant: "uuid-123",
		},
		{
			name:      "AnyTLS",
			raw:       "anytls://secret@anytls.example.com:8443?sni=edge.example.com#AnyTLS",
			protocol:  "anytls",
			nodeName:  "AnyTLS",
			server:    "anytls.example.com",
			port:      8443,
			extraKey:  "password",
			extraWant: "secret",
		},
		{
			name:      "ShadowTLS",
			raw:       "shadowtls://secret@shadowtls.example.com:443?version=3&host=target.example.com#ShadowTLS",
			protocol:  "shadowtls",
			nodeName:  "ShadowTLS",
			server:    "shadowtls.example.com",
			port:      443,
			extraKey:  "version",
			extraWant: "3",
		},
		{
			name:      "SS2022",
			raw:       "ss://2022-blake3-aes-256-gcm:secret@example.com:8388#SS2022",
			protocol:  "ss2022",
			nodeName:  "SS2022",
			server:    "example.com",
			port:      8388,
			extraKey:  "method",
			extraWant: "2022-blake3-aes-256-gcm",
		},
		{
			name:      "NaiveProxy",
			raw:       "naive+https://demo:pass@naive.example.com:443#Naive",
			protocol:  "naiveproxy",
			nodeName:  "Naive",
			server:    "naive.example.com",
			port:      443,
			extraKey:  "username",
			extraWant: "demo",
		},
		{
			name:      "WireGuard",
			raw:       "wireguard://private-key@wg.example.com:51820?public_key=peer-key&reserved=1,2,3#WG",
			protocol:  "wireguard",
			nodeName:  "WG",
			server:    "wg.example.com",
			port:      51820,
			extraKey:  "private_key",
			extraWant: "private-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, err := parseURILines(tt.raw)
			if err != nil {
				t.Fatalf("parseURILines error: %v", err)
			}
			if len(nodes) != 1 {
				t.Fatalf("expected 1 node, got %d", len(nodes))
			}
			node := nodes[0]
			if node.Protocol != tt.protocol {
				t.Fatalf("protocol = %s, want %s", node.Protocol, tt.protocol)
			}
			if node.Name != tt.nodeName || node.Server != tt.server || node.Port != tt.port {
				t.Fatalf("unexpected node: %+v", node)
			}
			if node.ExtraParams[tt.extraKey] != tt.extraWant {
				t.Fatalf("extra %s = %v, want %s", tt.extraKey, node.ExtraParams[tt.extraKey], tt.extraWant)
			}
		})
	}
}

func TestParseClashYAMLExtendedProtocols(t *testing.T) {
	content := `
proxies:
  - name: TUIC 节点
    type: tuic
    server: tuic.example.com
    port: 443
    uuid: tuic-uuid
    password: tuic-pass
    congestion-controller: bbr
  - name: AnyTLS 节点
    type: anytls
    server: anytls.example.com
    port: 8443
    password: anytls-secret
    sni: edge.example.com
  - name: ShadowTLS 节点
    type: shadowtls
    server: shadowtls.example.com
    port: 443
    password: shadow-secret
    version: 3
  - name: SS2022 节点
    type: ss
    server: ss2022.example.com
    port: 8388
    cipher: 2022-blake3-aes-256-gcm
    password: ss2022-secret
  - name: WireGuard 节点
    type: wireguard
    server: wg.example.com
    port: 51820
    private-key: wg-private
    public-key: wg-public
  - name: HY2 节点
    type: hy2
    server: hy2.example.com
    port: 8443
    password: hy2-secret
    sni: hy2.example.com
  - name: Hysteria2 节点
    type: hysteria2
    server: hysteria2.example.com
    port: 8444
    password: h2-secret
`
	nodes, err := parseClashYAML(content)
	if err != nil {
		t.Fatalf("parseClashYAML error: %v", err)
	}
	if len(nodes) != 7 {
		t.Fatalf("expected 7 nodes, got %d", len(nodes))
	}

	wantProtocols := []string{"tuic", "anytls", "shadowtls", "ss2022", "wireguard", "hy2", "hy2"}
	for i, want := range wantProtocols {
		if nodes[i].Protocol != want {
			t.Fatalf("node %d protocol = %s, want %s", i, nodes[i].Protocol, want)
		}
	}
	if nodes[5].Protocol != "hy2" {
		t.Fatalf("node 5 protocol = %s, want hy2", nodes[5].Protocol)
	}
	if nodes[5].ExtraParams["password"] != "hy2-secret" {
		t.Fatalf("node 5 password = %v, want hy2-secret", nodes[5].ExtraParams["password"])
	}
	if nodes[6].Protocol != "hy2" {
		t.Fatalf("node 6 protocol = %s, want hy2", nodes[6].Protocol)
	}
	if nodes[0].ExtraParams["congestion_controller"] != "bbr" {
		t.Fatalf("missing TUIC extras: %+v", nodes[0].ExtraParams)
	}
	if nodes[4].ExtraParams["public_key"] != "wg-public" {
		t.Fatalf("missing WireGuard extras: %+v", nodes[4].ExtraParams)
	}
}

func TestParseSubscriptionEmptyClashConfig(t *testing.T) {
	content := `
mixed-port: 7890
proxies: []
proxy-groups:
  - { name: demo, type: select, proxies: [DIRECT] }
rules:
  - MATCH,DIRECT
`
	_, err := ParseSubscription(content)
	if !errors.Is(err, errClashConfigNoProxies) {
		t.Fatalf("expected errClashConfigNoProxies, got %v", err)
	}
}

func TestParseClashYAMLInlineMapProxies(t *testing.T) {
	content := `
mixed-port: 7890
proxies:
  - { name: 'Inline Trojan', type: trojan, server: trojan.example.com, port: 443, password: secret, udp: true, sni: bilibili.com, skip-cert-verify: true }
  - { name: Inline SS, type: ss, server: ss.example.com, port: 8388, cipher: chacha20-ietf-poly1305, password: secret, udp: true }
`
	nodes, err := parseClashYAML(content)
	if err != nil {
		t.Fatalf("parseClashYAML error: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].Protocol != "trojan" || nodes[0].Server != "trojan.example.com" {
		t.Fatalf("unexpected trojan node: %+v", nodes[0])
	}
	if nodes[0].ExtraParams["sni"] != "bilibili.com" {
		t.Fatalf("missing trojan extras: %+v", nodes[0].ExtraParams)
	}
	if nodes[1].Protocol != "ss" || nodes[1].Server != "ss.example.com" {
		t.Fatalf("unexpected ss node: %+v", nodes[1])
	}
	if nodes[1].ExtraParams["method"] != "chacha20-ietf-poly1305" {
		t.Fatalf("missing ss method: %+v", nodes[1].ExtraParams)
	}
}

func TestParseSubscriptionRealWorldInlineClash(t *testing.T) {
	content := `
mixed-port: 7890
dns:
  enable: true
proxies:
  - { name: '剩余流量：993.39 GB', type: trojan, server: 103.181.164.101, port: 59277, password: 1441138d-7233-4fd4-b03a-3257d51f463c, udp: true, sni: bilibili.com, skip-cert-verify: true }
  - { name: 🇺🇸美国H5, type: ss, server: 1125.le.gy.lanxingyun.cn, port: 17216, cipher: chacha20-ietf-poly1305, password: 1441138d-7233-4fd4-b03a-3257d51f463c, udp: true }
proxy-groups:
  - { name: demo, type: select, proxies: [DIRECT] }
`
	nodes, err := ParseSubscription(content)
	if err != nil {
		t.Fatalf("ParseSubscription error: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].Name != "剩余流量：993.39 GB" || nodes[0].Protocol != "trojan" {
		t.Fatalf("unexpected first node: %+v", nodes[0])
	}
	if nodes[1].Protocol != "ss" || nodes[1].Port != 17216 {
		t.Fatalf("unexpected second node: %+v", nodes[1])
	}
}

func TestParseClashYAMLDetailedPartialSuccess(t *testing.T) {
	content := `
proxies:
  - name: valid
    type: trojan
    server: trojan.example.com
    port: 443
    password: secret
  - name: missing-port
    type: vmess
    server: vmess.example.com
    uuid: uuid-1
  - name: unsupported
    type: socks5
    server: socks.example.com
    port: 1080
`
	report, err := parseClashYAMLDetailed(content)
	if err != nil {
		t.Fatalf("parseClashYAMLDetailed error: %v", err)
	}
	if report.ImportedCount != 1 || report.SkippedCount != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Nodes) != 1 || report.Nodes[0].Protocol != "trojan" {
		t.Fatalf("unexpected nodes: %+v", report.Nodes)
	}
	if report.Diagnostics[0].Code != "missing_required_fields" || report.Diagnostics[0].Count != 1 {
		t.Fatalf("unexpected first diagnostic: %+v", report.Diagnostics)
	}
	if report.Diagnostics[1].Code != "unsupported_protocol" || report.Diagnostics[1].Count != 1 {
		t.Fatalf("unexpected second diagnostic: %+v", report.Diagnostics)
	}
}

func TestParseURILinesDetailedPartialSuccess(t *testing.T) {
	content := `
trojan://secret@trojan.example.com:443#Valid
not-a-uri-line
vmess://invalid-base64
`
	report, err := parseURILinesDetailed(content)
	if err != nil {
		t.Fatalf("parseURILinesDetailed error: %v", err)
	}
	if report.ImportedCount != 1 || report.SkippedCount != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Nodes) != 1 || report.Nodes[0].Name != "Valid" {
		t.Fatalf("unexpected nodes: %+v", report.Nodes)
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Code != "invalid_uri" || report.Diagnostics[0].Count != 1 {
		t.Fatalf("unexpected diagnostics: %+v", report.Diagnostics)
	}
}
