# Changelog

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
