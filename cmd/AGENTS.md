# AGENTS.md — generated map of cmd/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](../docs/STANDARD.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `nova-bus/` | coordination bus inbox, send, and wait CLI | `go test ./cmd/nova-bus` | `go test ./cmd/nova-bus` |
| `nova-cairn/` | dusk memory distillation CLI | `go test ./cmd/nova-cairn` | `go test ./cmd/nova-cairn` |
| `nova-check/` | record and repository check CLI | `go test ./cmd/nova-check` | `go test ./cmd/nova-check` |
| `nova-dev/` | this repository's own development process: dogfood, convergence, hygiene CLI | `go test ./cmd/nova-dev` | `go test ./cmd/nova-dev` |
| `nova-ci/` | CI slowtests budget and check CLI, and ci-ok's run receipt | `go test ./cmd/nova-ci` | `go test ./cmd/nova-ci` |
| `nova-config/` | permanent configuration store (Postgres), friends and machines, applied into Redis | `go test ./cmd/nova-config` | `go test ./cmd/nova-config` |
| `nova-fuse/` | workspace isolation and boundary CLI | `go test ./cmd/nova-fuse` | `go test ./cmd/nova-fuse` |
| `nova-memory/` | memory indexing and search CLI | `go test ./cmd/nova-memory` | `go test ./cmd/nova-memory` |
| `nova-redis/` | Redis instance owner: serve, scratch spill/recall, and fn load/check of the function library | `go test ./cmd/nova-redis` | `go test ./cmd/nova-redis` |
| `nova-sandbox/` | OS-level process sandbox CLI | `go test ./cmd/nova-sandbox` | `go test ./cmd/nova-sandbox` |
| `nova-secrets/` | zero-leak secrets store CLI | `go test ./cmd/nova-secrets` | `go test ./cmd/nova-secrets` |
| `nova-self-talk/` | internal dialogue recording CLI | `go test ./cmd/nova-self-talk` | `go test ./cmd/nova-self-talk` |
| `nova-sprint/` | the sprint table: four tables on nova-table, the moves between them, the coordinator's inbox, and the driver that plays the world | `go test ./cmd/nova-sprint` | `go test ./cmd/nova-sprint` |
| `nova-swarm/` | native card runner, bench slot leases and card lint CLI | `go test ./cmd/nova-swarm` | `go test ./cmd/nova-swarm` |
| `nova-table/` | tables of ordered sets, text and percentages over Redis | `go test ./cmd/nova-table` | `go test ./cmd/nova-table` |
| `nova-tokens/` | token consumption metering and budgeting CLI | `go test ./cmd/nova-tokens` | `go test ./cmd/nova-tokens` |
| `nova-update/` | binary release update CLI | `go test ./cmd/nova-update` | `go test ./cmd/nova-update` |
| `nova-version/` | build identity and version CLI | `go test ./cmd/nova-version` | `go test ./cmd/nova-version` |
| `nova-work/` | every issue of every repository of an organization in one tree file, imported read-only and verified field for field | `go test ./cmd/nova-work` | `go test ./cmd/nova-work` |
