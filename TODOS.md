# TODOS

Deferred work, captured during reviews. Each item has enough context to pick up cold.

Resolved in v0.2.9 (see CHANGELOG): `/history` transport-field contract, ephemeral/persistent probe-path dedup, DoH/DoT/DoQ SSRF guard (hostname resolution + CGNAT), and the `ensureInstance` lock-across-spawn.

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
