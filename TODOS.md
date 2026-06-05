# TODOS

Deferred work, captured during reviews. Each item has enough context to pick up cold.

## App.tsx 组件化拆分
- **What:** 把 1529 行的单文件 `src/App.tsx` 拆成组件(节点卡片、历史延迟图、订阅面板、统计区等)。
- **Why:** 单文件随功能增长越来越难维护,加新功能和定位 bug 都费劲。
- **Pros:** 后续维护省力;组件可单独测试;减少改动的 blast radius。
- **Cons:** 纯结构重构、收益不立刻可见;前端目前无测试,拆分时最好顺带补组件测试。
- **Context:** 前端只有 `src/App.tsx` + `src/main.tsx` + `src/index.css`,所有 UI 挤在 App.tsx。测速重构 PR 只会改其中的状态显示那块(去掉 transport 双状态),不做整体拆分。拆分时从最大的可独立块(节点列表/卡片)开始。
- **Depends on / blocked by:** 最好在测速重构 PR 合并之后做,避免和那次的 App.tsx 改动冲突。

## 前端目录分离到 web/
- **What:** 把前端(`src/`、`package.json`、`node_modules`、`vite`/`tsconfig`/`eslint` 配置)挪进 `web/` 子目录,Go 后端留在根目录。
- **Why:** 现在 Go 源码和前端 Vite 工程共用根目录;`node_modules` 里混进了第三方 Go 示例包,导致不能用 `go test ./...`(见 README:155,现在只能 `go test .`)。分离后这个坑自然消失。
- **Pros:** 修掉 `go test ./...` 的坑;目录职责清晰;前后端构建互不干扰。
- **Cons:** 要动 `go build`/`npm run package:release` 脚本、GitHub Actions 工作流、Go 后端托管 `dist/` 的嵌入/路径;`go:embed` 或 `os.DirFS("dist")` 的路径要跟着改。
- **Context:** `main.go:57` 用 `os.DirFS("dist")` 托管前端构建产物。打包脚本在 `scripts/`,发布走 GitHub Actions。挪目录后这些路径都要同步更新并重测打包。
- **Depends on / blocked by:** 独立于测速重构,但同样建议错开 PR,避免大范围路径改动和测速逻辑混在一起评审。

## ensureInstance 持全局锁跨阻塞 spawn
- **Priority:** P2
- **What:** `mihomo_checker.go:216` 的 `ensureInstance` 在 `defer r.mu.Unlock()` 后调用 `startInstance`→`waitMihomoReady`(轮询 `/proxies`,最长 `startTimeout` 默认 8s),全程持全局锁。
- **Why:** 多个 DNS 组冷启动严格串行,且并发的 `hasReadyInstance`/`reapInstances`/`Close` 会被阻塞数秒。
- **Context:** 目前检测被 `store.LockMaintenance()` 单飞、分组本就在 for 循环顺序冷启,所以实际影响低 —— 但一旦将来引入并发检测就会暴露。修法:持锁只用来认领 map slot(插占位),阻塞 spawn+readiness 在锁外做,再重新拿锁发布就绪实例(处理竞争)。由 /ship pre-landing review 发现(2026-06-04)。

## SSRF guard 不解析主机名 + 漏 CGNAT
- **Priority:** P2
- **What:** `dns_resolver.go` 的 `isBlockedDoHHost`/SSRF 守卫只拦 IP 字面量和 `*.localhost`,不解析主机名;DoH/DoT/DoQ nameserver 指向能解析到内网的主机名(`127.0.0.1.nip.io`、`metadata.google.internal`、攻击者 rebind 域)会通过;也漏了 CGNAT `100.64.0.0/10`。
- **Why:** 这是 README 宣传的 SSRF 守卫(折进生成的 mihomo 配置)。绕过后恶意订阅可把代理 resolver 指向内网服务。
- **Context:** 利用门槛较高(需内网目标有 HTTP(S)/DoT 响应,且请求由 spawned mihomo 而非 app 本身发出),故评审定为 INFORMATIONAL。修法:对 https/tls/quic/h3 nameserver 解析主机名(或要求 IP 字面量),命中 loopback/private/link-local/unspecified/CGNAT 则拒;补 `ip.IsPrivate()` + `100.64.0.0/10` + metadata 主机名黑名单。由 /ship security 专家发现(2026-06-04)。

## DRY:ephemeral fallback 与持久路径重复
- **Priority:** P3
- **What:** `mihomo_checker.go` 的 `runCandidateBatch`(ephemeral fallback)几乎逐字重复 `probeInstance` 的并发探测循环(sem/wg/mu 扇出);`waitMihomoController`(只被 fallback 用)与 `waitMihomoReady` 是近乎相同的轮询循环(前者只验 HTTP-up)。
- **Why:** 两份会漂移,且 fallback 路径覆盖率偏低(~55%)。
- **Context:** 可抽 `probeProxiesConcurrently(...)` 共享助手;或把 ephemeral 折到 `waitMihomoReady` 上删掉 `waitMihomoController`。由 /ship maintainability 专家发现(2026-06-04)。

## /history 端点仍输出已删的 transport 字段
- **Priority:** P3
- **What:** `/api/nodes/{id}/history` 的 `CheckHistoryPoint`(store.go)仍序列化 `transport_status`/`transport_latency_ms`/`proxy_status`/`proxy_latency_ms`/`status_source`,而 `/api/nodes` 和前端 `CheckHistoryPoint` 接口已删这 5 个字段。
- **Why:** 无害(前端忽略多余 JSON 字段),但契约不一致 —— 同样 5 个字段一个端点删了、另一个还发(新行恒为 unknown/NULL)。
- **Context:** 若要彻底退役 transport 线索,给未用字段加 `json:"-"`(保留 DB 列);否则注释说明 history 为历史行有意保留。由 /ship api-contract 专家发现(2026-06-04)。
