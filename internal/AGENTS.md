# AGENTS.md — generated map of internal/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [CONTRIBUTING.md](../docs/CONTRIBUTING.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `adoption/` | evidence-based tool and verb adoption ledger | `go test ./internal/adoption` | `go test ./internal/adoption` |
| `benchcount/` | per-bench landed and useful counts | `go test ./internal/benchcount` | `go test ./internal/benchcount` |
| `benchsh/` | the one bench script runner: ssh bash -s, script on stdin | `go test ./internal/benchsh` | `go test ./internal/benchsh` |
| `board/` | board structures and view rendering | `go test ./internal/board` | `go test ./internal/board` |
| `bounded/` | bounded readers and byte buffers | `go test ./internal/bounded` | `go test ./internal/bounded` |
| `buildinfo/` | binary identity and version info | `go test ./internal/buildinfo` | `go test ./internal/buildinfo` |
| `bus/` | append-only coordination bus | `go test ./internal/bus` | `go test ./internal/bus` |
| `cairn/` | memory distillation and cairn builder | `go test ./internal/cairn` | `go test ./internal/cairn` |
| `chat/` | friend conversation protocol | `go test ./internal/chat` | `go test ./internal/chat` |
| `check/` | hygiene rules and tree checkers | `go test ./internal/check` | `go test ./internal/check` |
| `ci/` | class tests and CI budget invariants | `go test ./internal/ci` | `go test ./internal/ci` |
| `civerdict/` | reader of a commit's CI verdict record | `go test ./internal/civerdict` | `go test ./internal/civerdict` |
| `control/` | fleet control desired state, lock, and acks | `go test ./internal/control` | `go test ./internal/control` |
| `converge/` | convergence state and progress math | `go test ./internal/converge` | `go test ./internal/converge` |
| `ctxindex/` | per-repo spec, test and symbol index for cards | `go test ./internal/ctxindex` | `go test ./internal/ctxindex` |
| `decide/` | criteria evaluation and decisions | `go test ./internal/decide` | `go test ./internal/decide` |
| `dispatch/` | command dispatch and runner interface | `go test ./internal/dispatch` | `go test ./internal/dispatch` |
| `docs/` | documentation guards and map generator | `go test ./internal/docs` | `go test ./internal/docs` |
| `dogfood/` | dogfood self-test gates | `go test ./internal/dogfood` | `go test ./internal/dogfood` |
| `events/` | card event stream and SQLite fold | `go test ./internal/events` | `go test ./internal/events` |
| `fleet/` | runner fleet discovery and status | `go test ./internal/fleet` | `go test ./internal/fleet` |
| `fleetkube/` | Kubernetes fleet model: labels, jobs, volumes | `go test ./internal/fleetkube` | `go test ./internal/fleetkube` |
| `friendqueue/` | friend queue reader over dealer streams | `go test ./internal/friendqueue` | `go test ./internal/friendqueue` |
| `friendread/` | per-friend read done counts | `go test ./internal/friendread` | `go test ./internal/friendread` |
| `friendrow/` | one friend's sprint-table row from its beat | `go test ./internal/friendrow` | `go test ./internal/friendrow` |
| `friends/` | friend registry and signatures | `go test ./internal/friends` | `go test ./internal/friends` |
| `fuse/` | workspace isolation boundaries | `go test ./internal/fuse` | `go test ./internal/fuse` |
| `ghcapture/` | read-only GitHub issue adapter for nova-work | `go test ./internal/ghcapture/...` | `go test ./internal/ghcapture/...` |
| `ghevent/` | GitHub webhook to Redis stream | `go test ./internal/ghevent` | `go test ./internal/ghevent` |
| `goenv/` | Go environment scrubber for child processes | `go test ./internal/goenv` | `go test ./internal/goenv` |
| `harvest/` | card result harvester and aggregation | `go test ./internal/harvest` | `go test ./internal/harvest` |
| `hygiene/` | clean checkout and leak assertions | `go test ./internal/hygiene` | `go test ./internal/hygiene` |
| `jev/` | Jev's mechanical passes (lint, scope, base) as one JEV gate line | `go test ./internal/jev` | `go test ./internal/jev` |
| `jevcalib/` | Jev prompt files and the calibration rule | `go test ./internal/jevcalib` | `go test ./internal/jevcalib` |
| `jobs/` | background job queues and state | `go test ./internal/jobs` | `go test ./internal/jobs` |
| `keyshape/` | cryptographic key format verification | `go test ./internal/keyshape` | `go test ./internal/keyshape` |
| `landed/` | did the work land in the base branch | `go test ./internal/landed` | `go test ./internal/landed` |
| `lanes/` | lane cursor and dispatch isolation | `go test ./internal/lanes` | `go test ./internal/lanes` |
| `lifecycle/` | sprint and worker lifecycle states | `go test ./internal/lifecycle` | `go test ./internal/lifecycle` |
| `log/` | structured logging helpers | `go test ./internal/log` | `go test ./internal/log` |
| `memindex/` | memory vector and text index | `go test ./internal/memindex` | `go test ./internal/memindex` |
| `merge/` | batch merge queue and land operations | `go test ./internal/merge` | `go test ./internal/merge` |
| `metrics/` | Prometheus surface shared by the queue verbs | `go test ./internal/metrics/...` | `go test ./internal/metrics/...` |
| `nogh/` | the refusing gh put first on friend and card child PATHs | `go test ./internal/nogh` | `go test ./internal/nogh` |
| `nsprint/` | nova-sprint dealer, store, fold, and table | `go test ./internal/nsprint/...` | `go test ./internal/nsprint/...` |
| `onboarding/` | onboarding banner and doc verifier | `go test ./internal/onboarding` | `go test ./internal/onboarding` |
| `oneline/` | single-line log and output grammar | `go test ./internal/oneline` | `go test ./internal/oneline` |
| `outbound/` | outbound webhook dispatcher | `go test ./internal/outbound` | `go test ./internal/outbound` |
| `play/` | sandboxed code experiment runner | `go test ./internal/play` | `go test ./internal/play` |
| `post/` | GitHub PR and issue client | `go test ./internal/post` | `go test ./internal/post` |
| `prereview/` | mechanical first pass over one pull request | `go test ./internal/prereview` | `go test ./internal/prereview` |
| `presence/` | friend heartbeat keys with a TTL | `go test ./internal/presence` | `go test ./internal/presence` |
| `record/` | decision and execution records | `go test ./internal/record` | `go test ./internal/record` |
| `records/` | database models and record formats | `go test ./internal/records` | `go test ./internal/records` |
| `redisq/` | Redis transport queue | `go test ./internal/redisq` | `go test ./internal/redisq` |
| `release/` | release packaging and manifest gates | `go test ./internal/release` | `go test ./internal/release` |
| `review/` | peer review verdict parser and evaluator | `go test ./internal/review` | `go test ./internal/review` |
| `roadmap/` | roadmap epics, features, and criteria parser | `go test ./internal/roadmap` | `go test ./internal/roadmap` |
| `safepath/` | path sanitization and sandboxing | `go test ./internal/safepath` | `go test ./internal/safepath` |
| `sandbox/` | OS isolation primitives (seatbelt/landlock) | `go test ./internal/sandbox` | `go test ./internal/sandbox` |
| `scaffold/` | scaffolding engine for class rules and CLI verbs | `go test ./internal/scaffold` | `go test ./internal/scaffold` |
| `seatcred/` | a seat's Redis login read through the secrets library (--seat, NOVA_SEAT) | `go test ./internal/seatcred/...` | `go test ./internal/seatcred/...` |
| `secrets/` | zero-leak memory and file vault | `go test ./internal/secrets` | `go test ./internal/secrets` |
| `selftalk/` | agent self-talk journal stream | `go test ./internal/selftalk` | `go test ./internal/selftalk` |
| `specwork/` | worklang spec compliance checks | `go test ./internal/specwork` | `go test ./internal/specwork` |
| `sprintcol/` | one column of the unified sprint table | `go test ./internal/sprintcol` | `go test ./internal/sprintcol` |
| `sprintline/` | sprint x/y z% -> eta line | `go test ./internal/sprintline` | `go test ./internal/sprintline` |
| `sprinttable/` | sprint table publish and restart behaviour | `go test ./internal/sprinttable` | `go test ./internal/sprinttable` |
| `swarm/` | swarm worker pool and execution engine | `go test ./internal/swarm` | `go test ./internal/swarm` |
| `testbin/` | places a built program into a test dir | `go test ./internal/testbin` | `go test ./internal/testbin` |
| `testguard/` | host seam and leak interception | `go test ./internal/testguard` | `go test ./internal/testguard` |
| `tokens/` | token counter and budget tracker | `go test ./internal/tokens` | `go test ./internal/tokens` |
| `typedrec/` | typed RESULT record contract, parser and legacy adapter | `go test ./internal/typedrec` | `go test ./internal/typedrec` |
| `update/` | binary updater and checksum verifier | `go test ./internal/update` | `go test ./internal/update` |
| `wake/` | slot leases and heartbeat monitors | `go test ./internal/wake` | `go test ./internal/wake` |
| `workclient/` | client bindings for nova-work daemon | `go test ./internal/workclient` | `go test ./internal/workclient` |
| `worklang/` | worklang s-expression evaluator | `go test ./internal/worklang` | `go test ./internal/worklang` |
| `workreconcile/` | GitHub issue import and work reconcile | `go test ./internal/workreconcile` | `go test ./internal/workreconcile` |
