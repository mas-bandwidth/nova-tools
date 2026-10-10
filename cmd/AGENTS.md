# AGENTS.md — generated map of cmd/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](../docs/STANDARD.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `nova-bus/` | messages between AIs over Redis streams: one stream per recipient under a consumer group, one log, pending until acked | `go test ./cmd/nova-bus` | `go test ./cmd/nova-bus` |
| `nova-cairn/` | session checkpoints: a session's exact words kept as plain files, with an index and receipts | `go test ./cmd/nova-cairn` | `go test ./cmd/nova-cairn` |
| `nova-card/` | writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help, for nova-sprint add --brief-dir | `go test ./cmd/nova-card` | `go test ./cmd/nova-card` |
| `nova-check/` | checks over markdown records and repositories: links, kernel budget, no-code, floors, corpus, hygiene, dogfood, spelling | `go test ./cmd/nova-check` | `go test ./cmd/nova-check` |
| `nova-ci/` | CI slowtests budget and check CLI, and ci-ok's run receipt | `go test ./cmd/nova-ci` | `go test ./cmd/nova-ci` |
| `nova-config/` | permanent configuration store (Postgres), friends and machines, applied into Redis | `go test ./cmd/nova-config` | `go test ./cmd/nova-config` |
| `nova-decide/` | typed decisions with probabilities through a backend, recorded and calibrated against their outcomes | `go test ./cmd/nova-decide` | `go test ./cmd/nova-decide` |
| `nova-doctor/` | one command that says what is missing and how to fix it: every registered dependency check, exit by the worst | `go test ./cmd/nova-doctor` | `go test ./cmd/nova-doctor` |
| `nova-friend/` | what a friend runs to be part of the team: the wake loop over nova-bus, the beat to the sprint server, and the proof of life, as one launchd daemon | `go test ./cmd/nova-friend` | `go test -tags functional ./cmd/nova-friend` |
| `nova-fuse/` | the ingestion fuse: a recorded decision to stop reading an untrusted source, checked before each read | `go test ./cmd/nova-fuse` | `go test ./cmd/nova-fuse` |
| `nova-local/` | run local models: what an engine has, one model served at a chosen context, and a worker description nova-swarm accepts | `go test ./cmd/nova-local` | `go test ./cmd/nova-local` |
| `nova-memory/` | memory indexing and search CLI | `go test ./cmd/nova-memory` | `go test ./cmd/nova-memory` |
| `nova-redis/` | Redis instance owner: serve, scratch spill/recall, and fn load/check of the function library | `go test ./cmd/nova-redis` | `go test ./cmd/nova-redis` |
| `nova-runner/` | one-shot friend harness runner that fills her configured sprint width | `go test ./cmd/nova-runner` | `go test ./cmd/nova-runner` |
| `nova-sandbox/` | OS-level process sandbox CLI | `go test ./cmd/nova-sandbox` | `go test ./cmd/nova-sandbox` |
| `nova-secrets/` | zero-leak secrets store CLI | `go test ./cmd/nova-secrets` | `go test ./cmd/nova-secrets` |
| `nova-self-talk/` | flags sentences where a writer passes a standing verdict on themselves | `go test ./cmd/nova-self-talk` | `go test ./cmd/nova-self-talk` |
| `nova-sprint/` | the sprint table: four tables on nova-table, the moves between them, the coordinator's inbox, and the driver that plays the world | `go test ./cmd/nova-sprint` | `go test ./cmd/nova-sprint` |
| `nova-swarm/` | native card runner, bench slot leases and card lint CLI | `go test ./cmd/nova-swarm` | `go test ./cmd/nova-swarm` |
| `nova-table/` | tables of ordered sets, text and percentages over Redis | `go test ./cmd/nova-table` | `go test ./cmd/nova-table` |
| `nova-tokens/` | token consumption metering and budgeting CLI | `go test ./cmd/nova-tokens` | `go test ./cmd/nova-tokens` |
| `nova-up/` | set nova up on one machine: plan every step, then apply, from nothing to a first sprint | `go test ./cmd/nova-up` | `go test ./cmd/nova-up` |
| `nova-update/` | binary release update CLI | `go test ./cmd/nova-update` | `go test ./cmd/nova-update` |
| `nova-version/` | build identity and version CLI | `go test ./cmd/nova-version` | `go test ./cmd/nova-version` |
| `nova-work/` | every issue of every repository of an organization in one tree file, imported read-only and verified field for field | `go test ./cmd/nova-work` | `go test ./cmd/nova-work` |
