# AGENTS.md — generated map of pkg/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](../docs/STANDARD.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `atomicfile/` | atomic file write: standard-library rename beside the target | `go test ./pkg/atomicfile` | `go test ./pkg/atomicfile` |
| `bench/` | the one bench runner: copy a tree to a per-run directory on the Linux bench, run one command there under nice with the bench cache, remove the directory it made, fall back when the host does not answer | `go test ./pkg/bench` | `go test ./pkg/bench` |
| `binstamp/` | a binary file's stamp: a loop stops when its own binary was replaced | `go test ./pkg/binstamp` | `go test ./pkg/binstamp` |
| `bounded/` | bounded readers and byte buffers | `go test ./pkg/bounded` | `go test ./pkg/bounded` |
| `buildinfo/` | binary identity and version info | `go test ./pkg/buildinfo` | `go test ./pkg/buildinfo` |
| `bus/` | the message bus over Redis streams: the rules (send, recv, ack, peek, log) over a Store of the few commands used, the Redis store and the in-memory fake | `go test ./pkg/bus` | `go test -tags functional ./pkg/bus` |
| `cardcontract/` | the frame around a card's task: the frame file, JOB.md, the result shape, and the shims of each model family's profile | `go test ./pkg/cardcontract` | `go test -tags functional ./pkg/cardcontract` |
| `cardcost/` | what a card cost: a route's price sheet, a run's tokens by class, the predicted cost and a consumer card's usage record, in exact decimal arithmetic | `go test ./pkg/cardcost` | `go test ./pkg/cardcost` |
| `cardhdr/` | card header vocabulary and one-invariant lint | `go test ./pkg/cardhdr` | `go test ./pkg/cardhdr` |
| `cardlimits/` | a card brief's refusal bound and the lint's size advice: two constants, no dependencies | `go test ./pkg/cardlimits` | `go test ./pkg/cardlimits` |
| `cardtree/` | a card as a tree of steps: the step grammar, its lint, the script step the member runs with no model, the verdict per step and the remainder of a failed step | `go test ./pkg/cardtree` | `go test ./pkg/cardtree` |
| `ci/` | class tests and CI budget invariants | `go test ./pkg/ci/...` | `go test ./pkg/ci/...` |
| `config/` | nova-config library: kind descriptors, the Postgres store and history, apply into Redis, the Ansible inventory of the applied state | `go test ./pkg/config` | `go test ./pkg/config` |
| `decide/` | nova-decide's decision system: schemas (read, attempt, grade), backends (Jev, fixed), the record and calibration | `go test ./pkg/decide` | `go test ./pkg/decide` |
| `delayproxy/` | a TCP proxy that holds each write of its clients back by a fixed delay: a store at a distance, made on the loopback, for testredis.Far and tools/fardelay | `go test ./pkg/delayproxy` | `go test ./pkg/delayproxy` |
| `diffcheck/` | the lander's mechanical checks of a card's diff: files outside PATHS, stranded fragments | `go test ./pkg/diffcheck` | `go test ./pkg/diffcheck` |
| `dogfood/` | dogfood self-test gates | `go test ./pkg/dogfood` | `go test ./pkg/dogfood` |
| `filelock/` | process-exclusive file locks whose holder is named in the file | `go test ./pkg/filelock` | `go test ./pkg/filelock` |
| `fleet/` | runner fleet discovery and status | `go test ./pkg/fleet` | `go test ./pkg/fleet` |
| `friend/` | nova-friend's rules apart from the transport: the connection and challenge machine with an injected clock, the daemon loop over the bus, the deliver adapters per harness, the state files and the launchd agent | `go test ./pkg/friend` | `go test ./pkg/friend` |
| `gitrun/` | the one runner for a one-shot git child: a deadline, WaitDelay and the caller's environment choice | `go test ./pkg/gitrun` | `go test ./pkg/gitrun` |
| `gocache/` | Go build cache held under a size, least recently used first | `go test ./pkg/gocache` | `go test ./pkg/gocache` |
| `goenv/` | Go environment scrubber for child processes | `go test ./pkg/goenv` | `go test ./pkg/goenv` |
| `harness/` | the harness words: opencode and the headless subscription harnesses of the heavy tier | `go test ./pkg/harness` | `go test ./pkg/harness` |
| `hostload/` | a machine's load: CPU busy percent of all its cores, else the load average over them | `go test ./pkg/hostload` | `go test ./pkg/hostload` |
| `hygiene/` | clean checkout and leak assertions | `go test ./pkg/hygiene` | `go test ./pkg/hygiene` |
| `keyshape/` | cryptographic key format verification | `go test ./pkg/keyshape` | `go test ./pkg/keyshape` |
| `log/` | structured logging helpers | `go test ./pkg/log` | `go test ./pkg/log` |
| `member/` | a sprint fleet member's loop: beat, queue, push and judge the finish, take to width, each card a child | `go test ./pkg/member` | `go test ./pkg/member` |
| `nsprint/` | the shared Redis store and login, its Functions, and the verb flags the living tools use | `go test ./pkg/nsprint/...` | `go test ./pkg/nsprint/...` |
| `ntable/` | a general Redis-backed table: ordered sets per cell, projections, folds, the render | `go test ./pkg/ntable` | `go test ./pkg/ntable` |
| `onboarding/` | onboarding banner and doc verifier | `go test ./pkg/onboarding` | `go test ./pkg/onboarding` |
| `oneline/` | single-line log and output grammar | `go test ./pkg/oneline` | `go test ./pkg/oneline` |
| `pkgselect/` | package selection, the shard deal and the test fan-out that CI and nova-ci local share | `go test ./pkg/pkgselect` | `go test ./pkg/pkgselect` |
| `provbalance/` | a model provider's balance read through the seat's key | `go test ./pkg/provbalance` | `go test ./pkg/provbalance` |
| `readregular/` | the one way to read a file a tool did not write: regular files only (links followed), size capped, FIFOs and devices refused by name | `go test ./pkg/readregular` | `go test ./pkg/readregular` |
| `redisacl/` | the fleet store's ACL users rendered from the function library and the key families, and compared with a store's live ACL | `go test ./pkg/redisacl` | `go test ./pkg/redisacl` |
| `redisconn/` | the one way a nova tool opens its Redis connection: options from the environment, one bounded dial, trips counted, errors classified, no secret shown | `go test ./pkg/redisconn` | `go test ./pkg/redisconn` |
| `redisfn/` | build, load and check a Redis function library from embedded Lua source | `go test ./pkg/redisfn` | `go test ./pkg/redisfn` |
| `release/` | release packaging and manifest gates | `go test ./pkg/release` | `go test ./pkg/release` |
| `safepath/` | path sanitization and sandboxing | `go test ./pkg/safepath` | `go test ./pkg/safepath` |
| `sandbox/` | OS isolation primitives (seatbelt/landlock) | `go test ./pkg/sandbox` | `go test ./pkg/sandbox` |
| `seatcred/` | a seat's Redis login read through the secrets library (--seat, NOVA_SEAT) | `go test ./pkg/seatcred/...` | `go test ./pkg/seatcred/...` |
| `secretcheck/` | the harness of the rule that no secret reaches an error: secret-shaped inputs, a driver, and the table and allowlist comparisons | `go test ./pkg/secretcheck` | `go test ./pkg/secretcheck` |
| `secrets/` | zero-leak memory and file vault | `go test ./pkg/secrets` | `go test ./pkg/secrets` |
| `sprintwire/` | how a worker talks to the sprint's server (nova-sprint run --listen): the request and reply of a batch of verbs, the client, and the member's sprint over it | `go test ./pkg/sprintwire` | `go test ./pkg/sprintwire` |
| `subproc/` | the one door a child process goes through: a named deadline per kind, WaitDelay, and a cancellable context for long-lived children | `go test ./pkg/subproc` | `go test ./pkg/subproc` |
| `swarm/` | the native card runner: staging, the wall, budgets, slot leases and card lint | `go test ./pkg/swarm` | `go test ./pkg/swarm` |
| `testbin/` | places a built program into a test dir | `go test ./pkg/testbin` | `go test ./pkg/testbin` |
| `testgit/` | the one git identity for tests that commit in a scratch repository, and the hosted runner's identity-less git for reproducing it | `go test ./pkg/testgit` | `go test ./pkg/testgit` |
| `testguard/` | host seam and leak interception | `go test ./pkg/testguard` | `go test ./pkg/testguard` |
| `testkit/` | test rigs shared by every package: a tool's entry point run in process with both streams captured, and the files a test writes and reads | `go test ./pkg/testkit` | `go test ./pkg/testkit` |
| `testredis/` | a throwaway redis-server for one test: loopback only, nothing kept, never outlives its test binary; Far puts a store at a distance | `go test ./pkg/testredis` | `go test ./pkg/testredis` |
| `tlc/` | TLC runner: the jar, the run, its results read, the case plan and the run records | `go test ./pkg/tlc` | `go test ./pkg/tlc` |
| `tool/` | the one shape of a command: verbs, banner, help, version, refusals, and the output envelope rendered as lines or JSON | `go test ./pkg/tool` | `go test ./pkg/tool` |
| `tty/` | whether a file is a terminal and how large its screen is | `go test ./pkg/tty` | `go test ./pkg/tty` |
| `typedrec/` | typed RESULT record contract, its format and the disposition line | `go test ./pkg/typedrec` | `go test ./pkg/typedrec` |
| `units/` | the text and install of every unit a running sprint needs: a launchd agent or systemd user unit per kind, written by a verb and loaded through the caller's loader | `go test ./pkg/units` | `go test ./pkg/units` |
