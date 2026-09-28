# AGENTS.md — generated map of cmd/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [CONTRIBUTING.md](../docs/CONTRIBUTING.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `nova-bus/` | coordination bus inbox, send, and wait CLI | `go test ./cmd/nova-bus` | `go test ./cmd/nova-bus` |
| `nova-cairn/` | dusk memory distillation CLI | `go test ./cmd/nova-cairn` | `go test ./cmd/nova-cairn` |
| `nova-check/` | codebase hygiene and constraint check CLI | `go test ./cmd/nova-check` | `go test ./cmd/nova-check` |
| `nova-ci/` | CI slowtests budget and check CLI, and ci-ok's run receipt | `go test ./cmd/nova-ci` | `go test ./cmd/nova-ci` |
| `nova-config/` | permanent configuration store (Postgres), friends and machines, applied into Redis | `go test ./cmd/nova-config` | `go test ./cmd/nova-config` |
| `nova-fuse/` | workspace isolation and boundary CLI | `go test ./cmd/nova-fuse` | `go test ./cmd/nova-fuse` |
| `nova-memory/` | memory indexing and search CLI | `go test ./cmd/nova-memory` | `go test ./cmd/nova-memory` |
| `nova-redis/` | Redis scratch spill/recall CLI | `go test ./cmd/nova-redis` | `go test ./cmd/nova-redis` |
| `nova-sandbox/` | OS-level process sandbox CLI | `go test ./cmd/nova-sandbox` | `go test ./cmd/nova-sandbox` |
| `nova-secrets/` | zero-leak secrets store CLI | `go test ./cmd/nova-secrets` | `go test ./cmd/nova-secrets` |
| `nova-self-talk/` | internal dialogue recording CLI | `go test ./cmd/nova-self-talk` | `go test ./cmd/nova-self-talk` |
| `nova-sprint/` | sprint dealer, table, and CI-card CLI | `go test ./cmd/nova-sprint` | `go test ./cmd/nova-sprint` |
| `nova-table/` | a table over Redis, every cell an ordered set; the sprint's stream block is its first table | `go test ./cmd/nova-table` | `go test ./cmd/nova-table` |
| `nova-tokens/` | token consumption metering and budgeting CLI | `go test ./cmd/nova-tokens` | `go test ./cmd/nova-tokens` |
| `nova-update/` | binary release update CLI | `go test ./cmd/nova-update` | `go test ./cmd/nova-update` |
| `nova-version/` | build identity and version CLI | `go test ./cmd/nova-version` | `go test ./cmd/nova-version` |
