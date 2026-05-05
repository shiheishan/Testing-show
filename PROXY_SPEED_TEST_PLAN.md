# 代理测速方案比较

结论先放前面：现有 `TCP dial` 继续保留，作为“入口端口能不能连上”的廉价健康检查；真正判断“这个节点在客户端里能不能用”，要再加一层代理 core 的 delay 测试。

FlClash 本身不是我们要直接嵌进去的测速库，它是一个基于 ClashMeta 的客户端。我们要借的是它背后的 core 思路，而不是 UI 壳。

## 当前现状

- `server_go.go:450` 现在只做 `net.DialTimeout("tcp", server:port, timeout)`。
- 这只能回答“端口通不通”，不能回答“代理协议握手、路由、上游链路是否可用”。
- `parser_go.go` 和 `import_parser.go` 已经把订阅节点解析成了足够完整的协议描述。
- `store.go` 已经保存了 `status`、`latency_ms`、`checked_at`，所以后端可以先保留旧指标，再加新指标，不用一次重做整个数据层。

## 方案对比

| 方案 | 结论 | 优点 | 缺点 |
|---|---|---|---|
| A. 保留当前 TCP 探活 | 只能做入口检查 | 依赖最少，成本最低，现有代码不用大改 | 不是代理可用性测试，和 FlClash 结果会继续分叉 |
| B. 接 Mihomo 的 `/proxies/{name}/delay` | 推荐 | 直接测真实代理路径，和 ClashMeta 生态一致；Mihomo API 明确提供 `delay` 接口 | 需要拉起 core、生成临时配置、管理生命周期 |
| C. 接 sing-box `urltest` / Clash API | 备选 | Go 生态友好，URLTest 设计很适合自动测速 | sing-box 官方文档已明确弃用 SSR，而我们当前解析器还支持 SSR |

## 为什么不直接“抄 FlClash”

FlClash 的 README 明确写的是“based on ClashMeta”。它是客户端，不是后端测速引擎。
所以可复用的不是它的 UI 或业务逻辑，而是它依赖的 core 能力。对我们来说，正确的边界是：

- 保留自己的后端和数据模型
- 把节点转换成 Mihomo 或 sing-box 可跑的临时配置
- 用 API 拉取 delay 结果
- 再把结果写回现有历史表

## 推荐路径

### 第一阶段

保留当前 TCP 检查，语义明确成“入口健康检查”。

### 第二阶段

新增“真实代理测速”通道，优先接 Mihomo：

1. 读取现有节点记录。
2. 生成临时 core 配置。
3. 启动本地 Mihomo 实例并开启 external controller。
4. 对每个节点调用 `/proxies/{name}/delay?url=...&timeout=5000`。
5. 把 delay 结果写入历史，失败则记录对应错误状态。

### 第三阶段

如果后面要继续压缩依赖，再评估 sing-box。但前提是我们愿意处理 SSR 兼容性收缩。

## 语义建议

不要把“入口 TCP 失败”和“代理不可用”混成一个状态。

建议拆成两层：

- `transport_status`：入口是否能连上
- `proxy_status`：代理是否真的能转发并返回 delay

如果暂时不拆字段，至少要在代码里把两种测试结果区分开，别再让 `timeout` 直接等于“节点死了”。

## 现有代码能复用什么

- `parser_go.go`：协议解析已经够用，能直接喂给 core 配置生成。
- `store.go`：历史表已经能存状态和延迟。
- `server_go.go`：check service 可以替换实现，不必先改前端接口。
- `src/App.tsx`：历史波形图已经在读状态序列，后面只要把新状态语义接进去即可。

## 这次比较后的取舍

我建议先做 B，再保留 A 作为兜底。

原因很简单：用户真正关心的是“这个节点在客户端里能不能用”，不是“端口能不能 `dial` 成功”。
如果只保留 A，网站和 FlClash 还是会长期互相打架。B 能把这件事收回来。

## 待实现的下一步

1. 定义新测速模式的数据结构。
2. 选定 Mihomo 配置生成方式。
3. 先做单节点 delay 测试。
4. 再接批量测试和历史曲线。
5. 最后再评估 sing-box 是否值得作为第二引擎。

## 参考来源

- FlClash README: https://github.com/chen08209/FlClash
- Mihomo API docs: https://wiki.metacubex.one/en/api/
- Mihomo proxy groups docs: https://wiki.metacubex.one/en/config/proxy-groups/
- sing-box URLTest: https://sing-box.sagernet.org/configuration/outbound/urltest/
- sing-box Clash API: https://sing-box.sagernet.org/configuration/experimental/clash-api/
- sing-box outbound list: https://sing-box.sagernet.org/configuration/outbound/
- sing-box deprecated SSR note: https://sing-box.sagernet.org/deprecated/
