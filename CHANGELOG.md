# Changelog

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
