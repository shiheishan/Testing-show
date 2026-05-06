# VPS 节点监控

一个自用节点状态板：后端读取根目录 `subscriptions.yaml` 里的订阅链接，解析节点并由部署机器本地检测状态；TCP 探活作为入口诊断，安装 Mihomo 后会用真实代理 delay 作为主状态。

## 功能

- 从 `subscriptions.yaml` 配置订阅来源，不在前端暴露导入入口
- 支持 Clash YAML 和常见 URI 订阅格式
- SQLite 本地持久化订阅、节点和检测结果
- 默认每 6 分钟自动检测节点延迟
- 支持可选 Mihomo 真实代理测速，避免只测入口导致 HY2/TUIC/Hysteria 等节点误判
- 会复用 Clash 订阅里的 `dns` 配置解析节点入口域名，减少 VPS 系统 DNS 与客户端 DNS 不一致导致的离线误判
- 前端可手动触发全部测速或单节点测速
- 手动测速有后端去重和冷却，多个客户同时点击不会重复开启多轮检测

## 配置

运行时需要机器上已有 `sqlite3` 命令：

```bash
sqlite3 --version
```

Ubuntu/Debian 可安装：

```bash
sudo apt-get install sqlite3
```

复制主配置示例：

```bash
cp config.yaml.example config.yaml
```

真实代理测速需要机器上能执行 `mihomo`、`clash-meta` 或 `clash`。程序会自动从 `PATH` 查找，也可以在 `config.yaml` 里指定：

```yaml
check:
  proxy_enabled: true
  mihomo_path: /usr/local/bin/mihomo
  proxy_url: https://www.gstatic.com/generate_204
  proxy_concurrency: 10
  proxy_warmup: true
```

如果没有安装 Mihomo，程序会自动降级为原来的 TCP 入口探活。

如果订阅是 Clash YAML，程序会保存其中的 `dns.nameserver`、`proxy-server-nameserver`、`fallback` 和 `default-nameserver`，用于 TCP 入口探活和 Mihomo 真实代理测速的域名解析。HTTP DoH 和本地/内网 DoH 地址会被拒绝，只使用安全的 HTTPS DoH 或普通 DNS 服务器。

订阅链接单独放在根目录 `subscriptions.yaml`：

```yaml
subscriptions:
  - name: my-subscription
    url: https://example.com/sub
```

本地文件订阅也支持：

```yaml
subscriptions:
  - name: sample-clash
    url: file:///ABSOLUTE/PATH/TO/samples/sample-clash.yaml
```

## 开发

前端开发：

```bash
npm run dev
```

生产构建并启动 Go 后端：

```bash
npm run build
go run .
```

默认监听 `http://0.0.0.0:8080`。Go 后端会托管 `dist/` 里的 React 构建产物。

## 下载部署

发布页会提供按系统和 CPU 架构打好的 `.tar.gz` 包，例如：

```text
vps-monitor_0.2.4_linux_amd64.tar.gz
vps-monitor_0.2.4_linux_arm64.tar.gz
vps-monitor_0.2.4_darwin_arm64.tar.gz
```

下载后解压：

```bash
tar -xzf vps-monitor_0.2.4_linux_amd64.tar.gz
cd vps-monitor_0.2.4_linux_amd64
cp config.yaml.example config.yaml
cp subscriptions.yaml.example subscriptions.yaml
./vps-monitor
```

然后访问 `http://服务器 IP:8080`。

可选：Linux 服务器可以参考包内 `deploy/systemd/vps-monitor.service.example` 配置 systemd 常驻运行。默认示例假设程序解压到 `/opt/vps-monitor`，并使用 `vps-monitor` 用户运行。

## 发布打包

本地打包当前机器平台：

```bash
npm ci
npm run package:release
```

交叉打包指定平台：

```bash
npm ci
TARGET_OS=linux TARGET_ARCH=amd64 npm run package:release
```

产物会写到 `release/`，包括 `.tar.gz` 和对应的 `.sha256`。

创建 GitHub Release：

```bash
git tag v0.2.4
git push origin v0.2.4
```

推送 tag 后，GitHub Actions 会自动构建 Linux/macOS 的 amd64/arm64 tarball 并上传到 release。

## API

- `GET /api/subscriptions`
- `GET /api/nodes`
- `GET /api/nodes/stats`
- `POST /api/nodes/check`
- `POST /api/nodes/{id}/check`

## 测试

```bash
npm run build
GOCACHE="$PWD/.gocache" go test .
```

不要用 `go test ./...`，当前项目的 `node_modules` 里有第三方 Go 示例包，会被一起扫描。

发布前建议跑一次密钥扫描：

```bash
gitleaks detect --source . --config .gitleaks.toml
```
