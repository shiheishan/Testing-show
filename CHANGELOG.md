# Changelog

## [0.2.1] - 2026-05-05

### Changed

- Changed the default automatic node check interval from 30 seconds to 1 minute.

### Fixed

- Improved Mihomo proxy check failure messages so nodes can show the returned 503/504 reason instead of only a generic offline status.
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
