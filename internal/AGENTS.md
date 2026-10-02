# AGENTS.md — generated map of internal/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](../docs/STANDARD.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `atomicfile/` | atomic file write: standard-library rename beside the target | `go test ./internal/atomicfile` | `go test ./internal/atomicfile` |
| `binstamp/` | a binary file's stamp: a loop stops when its own binary was replaced | `go test ./internal/binstamp` | `go test ./internal/binstamp` |
| `bounded/` | bounded readers and byte buffers | `go test ./internal/bounded` | `go test ./internal/bounded` |
| `buildinfo/` | binary identity and version info | `go test ./internal/buildinfo` | `go test ./internal/buildinfo` |
| `bus/` | append-only coordination bus | `go test ./internal/bus` | `go test ./internal/bus` |
| `cairn/` | memory distillation and cairn builder | `go test ./internal/cairn` | `go test ./internal/cairn` |
| `cardcontract/` | the frame around a card's task: the frame file, JOB.md, the result shape, and the shims of each model family's profile | `go test ./internal/cardcontract` | `go test -tags functional ./internal/cardcontract` |
| `cardcost/` | what a card cost: a route's price sheet, a run's tokens by class, the predicted cost and a consumer card's usage record, in exact decimal arithmetic | `go test ./internal/cardcost` | `go test ./internal/cardcost` |
| `cardhdr/` | card header vocabulary and one-invariant lint | `go test ./internal/cardhdr` | `go test ./internal/cardhdr` |
| `cardlimits/` | a card brief's refusal bound and the lint's size advice: two constants, no dependencies | `go test ./internal/cardlimits` | `go test ./internal/cardlimits` |
| `check/` | hygiene rules and tree checkers | `go test ./internal/check` | `go test ./internal/check` |
| `ci/` | class tests and CI budget invariants | `go test ./internal/ci` | `go test ./internal/ci` |
| `cireceipt/` | ci-ok's run receipt: one ev:github row of the workflow_run shape | `go test ./internal/cireceipt` | `go test -tags functional ./internal/cireceipt` |
| `config/` | nova-config library: kind descriptors, the Postgres store and history, apply into Redis, the Ansible inventory of the applied state | `go test ./internal/config` | `go test ./internal/config` |
| `converge/` | convergence state and progress math | `go test ./internal/converge` | `go test ./internal/converge` |
| `delayproxy/` | a TCP proxy that holds each write of its clients back by a fixed delay: a store at a distance, made on the loopback, for testredis.Far and tools/fardelay | `go test ./internal/delayproxy` | `go test ./internal/delayproxy` |
| `docs/` | documentation guards and map generator | `go test ./internal/docs` | `go test ./internal/docs` |
| `dogfood/` | dogfood self-test gates | `go test ./internal/dogfood` | `go test ./internal/dogfood` |
| `filelock/` | process-exclusive file locks with holder stamps and state probing | `go test ./internal/filelock` | `go test ./internal/filelock` |
| `fleet/` | runner fleet discovery and status | `go test ./internal/fleet` | `go test ./internal/fleet` |
| `fuse/` | workspace isolation boundaries | `go test ./internal/fuse` | `go test ./internal/fuse` |
| [ghevent/](ghevent/AGENTS.md) | the GitHub event Redis stream: append and read | `go test ./internal/ghevent` | `go test ./internal/ghevent` |
| `gitrun/` | the one runner for a one-shot git child: a deadline, WaitDelay and the caller's environment choice | `go test ./internal/gitrun` | `go test ./internal/gitrun` |
| `goenv/` | Go environment scrubber for child processes | `go test ./internal/goenv` | `go test ./internal/goenv` |
| `hostload/` | a machine's load: CPU busy percent of all its cores, else the load average over them | `go test ./internal/hostload` | `go test ./internal/hostload` |
| `hygiene/` | clean checkout and leak assertions | `go test ./internal/hygiene` | `go test ./internal/hygiene` |
| `keyshape/` | cryptographic key format verification | `go test ./internal/keyshape` | `go test ./internal/keyshape` |
| `log/` | structured logging helpers | `go test ./internal/log` | `go test ./internal/log` |
| `member/` | a sprint fleet member's loop: beat, queue, push and judge the finish, take to width, each card a child | `go test ./internal/member` | `go test ./internal/member` |
| `memindex/` | memory vector and text index | `go test ./internal/memindex` | `go test ./internal/memindex` |
| `nogh/` | the refusing gh command installed first on child shell PATHs | `go test ./internal/nogh` | `go test ./internal/nogh` |
| `nsprint/` | the shared Redis store and login, its Functions, and the verb flags the living tools use | `go test ./internal/nsprint/...` | `go test ./internal/nsprint/...` |
| `ntable/` | a general Redis-backed table: ordered sets per cell, projections, folds, the render | `go test ./internal/ntable` | `go test ./internal/ntable` |
| `onboarding/` | onboarding banner and doc verifier | `go test ./internal/onboarding` | `go test ./internal/onboarding` |
| `oneline/` | single-line log and output grammar | `go test ./internal/oneline` | `go test ./internal/oneline` |
| `pkgselect/` | package selection, the shard deal and the test fan-out that CI and nova-ci local share | `go test ./internal/pkgselect` | `go test ./internal/pkgselect` |
| `record/` | decision and execution records | `go test ./internal/record` | `go test ./internal/record` |
| `redisacl/` | the fleet store's ACL users rendered from the function library and the key families, and compared with a store's live ACL | `go test ./internal/redisacl` | `go test ./internal/redisacl` |
| `redisconn/` | the one way a nova tool opens its Redis connection: options from the environment, one bounded dial, trips counted, errors classified, no secret shown | `go test ./internal/redisconn` | `go test ./internal/redisconn` |
| `redisfn/` | build, load and check a Redis function library from embedded Lua source | `go test ./internal/redisfn` | `go test ./internal/redisfn` |
| `release/` | release packaging and manifest gates | `go test ./internal/release` | `go test ./internal/release` |
| `safepath/` | path sanitization and sandboxing | `go test ./internal/safepath` | `go test ./internal/safepath` |
| `sandbox/` | OS isolation primitives (seatbelt/landlock) | `go test ./internal/sandbox` | `go test ./internal/sandbox` |
| `scaffold/` | scaffolding engine for class rules and CLI verbs | `go test ./internal/scaffold` | `go test ./internal/scaffold` |
| `seatcred/` | a seat's Redis login read through the secrets library (--seat, NOVA_SEAT) | `go test ./internal/seatcred/...` | `go test ./internal/seatcred/...` |
| `secrets/` | zero-leak memory and file vault | `go test ./internal/secrets` | `go test ./internal/secrets` |
| `selftalk/` | agent self-talk journal stream | `go test ./internal/selftalk` | `go test ./internal/selftalk` |
| `shippedsmoke/` | the smoke test of a shipped nova-check binary, behind the shippedsmoke build tag, run by the certification workflow | `go test -tags shippedsmoke ./internal/shippedsmoke` | `NOVA_SHIPPED_BIN=<binary> go test -tags shippedsmoke -v ./internal/shippedsmoke` |
| `sprint/` | the sprint table's pure core (lifecycle, steps, check, inbox) and its binding to the table layer (store) and its driver (play) | `go test ./internal/sprint/...` | `go test ./internal/sprint/...` |
| `sprintwire/` | how a worker talks to the sprint's server (nova-sprint run --listen): the request and reply of a batch of verbs, the client, and the member's sprint over it | `go test ./internal/sprintwire` | `go test ./internal/sprintwire` |
| `subproc/` | the one door a child process goes through: a named deadline per kind, WaitDelay, and a cancellable context for long-lived children | `go test ./internal/subproc` | `go test ./internal/subproc` |
| `swarm/` | the native card runner: staging, the wall, budgets, slot leases and card lint | `go test ./internal/swarm` | `go test ./internal/swarm` |
| `tablemodel/` | the table model's checks: the suites, the finding witnesses and the receipt replay against EpochMemberTable | `go test ./internal/tablemodel` | `go test -tags functional ./internal/tablemodel` |
| `testbin/` | places a built program into a test dir | `go test ./internal/testbin` | `go test ./internal/testbin` |
| `testguard/` | host seam and leak interception | `go test ./internal/testguard` | `go test ./internal/testguard` |
| `testkit/` | test rigs shared by every package: a tool's entry point run in process with both streams captured, and the files a test writes and reads | `go test ./internal/testkit` | `go test ./internal/testkit` |
| `testredis/` | a throwaway redis-server for one test: loopback only, nothing kept, never outlives its test binary; Far puts a store at a distance | `go test ./internal/testredis` | `go test ./internal/testredis` |
| `testverbhelp/` | per-tool check that every verb answers -h at exit 0 and touches nothing | `go test ./internal/testverbhelp` | `go test ./internal/testverbhelp` |
| `textbody/` | shared line-oriented message body filtering | `go test ./internal/textbody` | `go test ./internal/textbody` |
| `tlc/` | TLC runner: the jar, the run, its results read, the case plan and the run records | `go test ./internal/tlc` | `go test ./internal/tlc` |
| `tokens/` | token counter and budget tracker | `go test ./internal/tokens` | `go test ./internal/tokens` |
| `tool/` | the one shape of a command: verbs, banner, help, version, refusals, and the output envelope rendered as lines or JSON | `go test ./internal/tool` | `go test ./internal/tool` |
| `tty/` | whether a file is a terminal and how large its screen is | `go test ./internal/tty` | `go test ./internal/tty` |
| `typedrec/` | typed RESULT record contract, its format and the disposition line | `go test ./internal/typedrec` | `go test ./internal/typedrec` |
| `update/` | binary updater and checksum verifier | `go test ./internal/update` | `go test ./internal/update` |
| `workfile/` | nova-work tree file: the model, its canonical writer, strict reader and field-for-field diff | `go test ./internal/workfile` | `go test ./internal/workfile` |
| `workgh/` | nova-work read-only GitHub issue capture over GraphQL, every call counted | `go test ./internal/workgh` | `go test ./internal/workgh` |
| `worklang/` | bounded reader for nova-work's restricted s-expression tree file | `go test ./internal/worklang` | `go test ./internal/worklang` |
| `yield/` | CI over work: a copy, a local test run or a sprint card's native launch steps itself to nice 15 before it execs (nova-tools#4293) | `go test ./internal/yield` | `go test ./internal/yield` |
