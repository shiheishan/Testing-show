# VPS 节点监控

一个自用节点状态板：后端读取根目录 `subscriptions.yaml` 里的订阅链接，解析节点并由部署机器本地检测 TCP 延迟；前端只展示订阅、节点状态和最近检测结果。

## 功能

- 从 `subscriptions.yaml` 配置订阅来源，不在前端暴露导入入口
- 支持 Clash YAML 和常见 URI 订阅格式
- SQLite 本地持久化订阅、节点和检测结果
- 默认每 30 秒自动检测节点延迟
- 前端可手动触发全部测速或单节点测速
- 手动测速有后端去重和冷却，多个客户同时点击不会重复开启多轮检测

## 配置

复制主配置示例：

```bash
cp config.yaml.example config.yaml
```

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
