# Changelog

## [0.2.11] - 2026-06-05

### Internal

- CI: bumped the release workflow's GitHub Actions off the deprecated Node 20 runtime — `actions/checkout` v4→v6, `actions/setup-go` v5→v6, `actions/setup-node` v4→v6, `actions/upload-artifact` v4→v7, `actions/download-artifact` v4→v8. All now run natively on Node 24; no workflow behavior change. Release artifacts are unchanged.

## [0.2.10] - 2026-06-05

### Changed

- The frontend (`src/`, `package.json`, Vite/TS/ESLint config, `node_modules`) now lives in a `web/` subdirectory, with a nested `web/go.mod` so the Go tool skips the whole subtree. `go test ./...` works again (a stray Go file shipped inside `node_modules/flatted` previously polluted the module walk, forcing `go test .`). Vite writes its build to the repo-root `dist/` (`outDir: ../dist`), so the Go server's `os.DirFS("dist")` and the release packaging are unchanged. Frontend commands now run from `web/` (`cd web && npm run dev|build|lint`).

### Internal

- `src/App.tsx` was split from a single 1474-line file into focused modules (`web/src/types.ts`, `web/src/constants.ts`, `web/src/lib/*`, `web/src/components/*`); the page container is now ~323 lines. No behavior change.

## [0.2.9] - 2026-06-05

### Security

- The DoH/DoT/DoQ SSRF guard now resolves nameserver hostnames and blocks any host that resolves to an internal address, closing a bypass where names like `127.0.0.1.nip.io`, `metadata.google.internal`, or attacker rebind domains pointed the proxy resolver at internal services. The CGNAT range `100.64.0.0/10` (RFC6598) is now blocked alongside loopback/private/link-local, known metadata hostnames are blocked by name, and a host that cannot be resolved fails closed.

### Changed

- `ensureInstance` no longer holds the runner-wide lock across the up-to-`startTimeout` mihomo spawn. Instance starts are single-flighted via an in-flight marker, so a cold start no longer stalls `hasReadyInstance`, instance reaping, or shutdown for seconds (groundwork for concurrent checks).

### Fixed

- `/api/nodes/{id}/history` no longer serializes the retired `transport_status`, `transport_latency_ms`, `proxy_status`, `proxy_latency_ms`, and `status_source` fields, matching `/api/nodes`. The DB columns are retained for historical rows.

### Internal

- Deduplicated the ephemeral and persistent mihomo paths: a shared `probeProxiesConcurrently` for the bounded probe fan-out and a shared `pollMihomo` polling skeleton behind `waitMihomoController` and `waitMihomoReady`. No behavior change.

## [0.2.8] - 2026-06-05

### Fixed

- The proxy-disabled (`proxy_enabled: false`) status message no longer claims an entry "TCP 探活" runs. The TCP entry-probe track was removed in 0.2.7, so the message now reports the speed-test engine as unavailable. Updated the stale `config.yaml.example` comments to match.

## [0.2.7] - 2026-06-04

### Changed

- Real proxy speed testing now runs a long-lived mihomo instance per DNS group instead of spawning a fresh one every check round. The process is reused while its config is unchanged and restarted under exponential backoff on config change or crash, cutting repeated process churn on every cycle.
- Node status is sourced solely from the real proxy delay. The TCP entry-probe track was removed, and the dashboard shows a single status/latency per node instead of separate transport and proxy results.
- `/api/nodes` no longer returns the retired `transport_status`, `transport_latency_ms`, `proxy_status`, `proxy_latency_ms`, and `status_source` fields; `/api/nodes/stats` now includes `engine_available`. The frontend is updated in lockstep; the DB columns are kept for historical rows.
- The Mihomo controller now requires a per-instance bearer secret and binds to `127.0.0.1` only.

### Added

- Site-wide "测速引擎不可用" banner plus a per-node message when Mihomo is not installed or fails to start, so an unavailable speed-test engine is visible instead of silently showing every node as unknown.

### Fixed

- A single-node "测速" test (or a per-subscription check) no longer tears down every other DNS group's persistent Mihomo instance, which previously forced a cold restart on the next full check.
- `waitMihomoReady`/`waitMihomoController` now bail as soon as the Mihomo process exits instead of waiting out the full start timeout, cutting the crash path from ~20s to under 1s.
- Looping and decorative UI animations (including the engine-unavailable banner spinner) now honor `prefers-reduced-motion`.

## [0.2.6] - 2026-05-20

### Added

- Mobile node list is now a card layout with full-width rows, page scroll, and a 44px test action — no more horizontal scroll inside a nested scroll container.
- Node detail modal closes on Escape and on backdrop click, locks background scroll while open, and exposes `role="dialog"` + `aria-label` to screen readers.
- Mobile node cards are keyboard-accessible (`role="button"`, Enter/Space opens detail) with a visible focus ring, and ignore accidental taps that happen during momentum scrolling.

### Fixed

- Node detail modal now refreshes from the live node list during the 5-second poll; previously it showed a stale snapshot from when it was opened — possibly minutes out of date for monitoring purposes.
- `apiRequest` reports the real HTTP status (e.g. `请求失败 (HTTP 502)`) when the backend returns non-JSON; previously a `JSON.parse` error masked the actual error.
- Polling cancels prior in-flight requests via `AbortController`, so a slow `/api/nodes` response can no longer arrive after a newer one and overwrite the dashboard with stale state.
- Concurrent per-node test actions now track in-flight ids as a Set, so testing node B no longer clears node A's spinner mid-flight.
- Mobile detail page no longer clips long protocol names (`hysteria` rendered fully instead of truncated).
- Subscription selector stacks vertically on mobile, so real subscription names (`白菜机场 Hong Kong 专线`) show in full instead of truncating to `白菜机场 Hon...`.
- Modal close (X) and test (refresh) buttons meet the 44px mobile touch-target minimum.
- History chart no longer renders phantom `1ms 1ms 1ms 0ms 0ms` y-axis labels when every sample is null; the empty-state message now stands alone.
- Functional micro-labels (sync status, stat trends, subscription names, server:port, history captions) lifted to readable contrast across the dashboard.

### Changed

- Frontend uses `useMediaQuery` to render either the desktop `<NodeTableRow>` table or the mobile `<NodeMobileCard>` card list, not both — eliminates double React reconciliation on every poll.
- Extracted `NodeTableRow` / `NodeMobileCard` `React.memo` components for future per-row update skipping.
- Synced `package-lock.json` version field (was stuck at `0.2.2` while `package.json` shipped `0.2.5`).

## [0.2.5] - 2026-05-06

### Added

- Added `check.proxy_urls` so Mihomo real proxy checks can try multiple target URLs and mark a node online if any target succeeds.

## [0.2.4] - 2026-05-06

### Fixed

- Fixed large subscription refreshes failing with `fork/exec /usr/bin/sqlite3: argument list too long` after subscription DNS metadata increased node payload size.

## [0.2.3] - 2026-05-06

### Fixed

- Resolved subscription-provided DNS servers during VPS entry checks so nodes with provider-specific DoH records can be tested without manually pinning IP addresses.
- Passed subscription DNS settings into Mihomo proxy delay checks, including per-DNS batching for mixed subscription imports.
- Blocked insecure or local DoH endpoints from subscription DNS configs while preserving HTTPS DoH providers such as AliDNS and DNSPod.

## [0.2.2] - 2026-05-05

### Fixed

- Preserved Hysteria2 port-hopping options when converting subscription nodes for Mihomo real proxy checks.
- Added transport failure details to proxy-offline status messages so VPS network reachability problems are visible in node diagnostics.

## [0.2.1] - 2026-05-05

### Changed

- Changed the default automatic node check interval from 30 seconds to 6 minutes.
- Added a separate default concurrency limit for Mihomo real proxy checks so TCP entry checks can stay fast while proxy checks avoid overloading small VPS hosts.

### Fixed

- Improved Mihomo proxy check failure messages so nodes can show the returned 503/504 reason instead of only a generic offline status.
- Added a warmup request before recording Mihomo proxy delay so cold handshakes have less impact on the displayed latency.
- Preserved successful Mihomo delay responses while reading error bodies, keeping online nodes from being misclassified during diagnostics.

## [0.2.0] - 2026-05-05

### Added

- Added Mihomo-backed real proxy delay checks so node status reflects actual proxy availability, while retaining TCP entry checks as transport diagnostics.
- Added separate transport and proxy status fields in API responses, history records, node details, and docs.
- Added visual history markers for timeout/offline periods so recovery back to online is visible in the node detail waveform.

### Changed

- Changed the node list default sort to preserve subscription order instead of status order.
- Improved mobile toolbar layout, subscription dropdown closing animation, long node-name rendering, and table header outlines.

### Fixed

- Fixed legacy database startup when adding `display_order` by creating the subscription-order index after the column migration.
- Isolated invalid Mihomo proxy configs during batch checks so one bad node no longer prevents valid nodes from being tested.

## [0.1.0] - 2026-05-04

### Added

- Added a self-hosted VPS node monitoring panel that loads configured subscriptions, parses Clash and common URI formats, and stores node state in SQLite.
- Added Go API endpoints for subscription status, node lists, aggregate stats, manual checks, and per-node latency history.
- Added a React monitoring UI with subscription filtering, status cards, searchable node table, manual latency checks, and node detail charts.
- Added examples, documentation, tests, secret-scanning configuration, and release tarball packaging for release readiness.
