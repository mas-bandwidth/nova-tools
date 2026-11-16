# Resources

What each machine in a fleet has, and what work takes from it.

This page is the v3.0.0 specification for resources in `nova-tools`. It defines
the client/server resource arbitrator (`nova-reserve`), the machine tag and
resource model in `nova-config`, the OS steering and CI priority rules in
`nova-fleet`, and the wire protocol between peers and the arbitrator. It matches
the `nova-sprint` v3.0.0 specification (`docs/SPEC-V3.md` in `nova-sprint`) 100%.

Everything here belongs to `nova-tools` and functions standalone without
`nova-sprint`. Any friend, worker, CI runner, person, script, or `nova-sprint`
connects as a peer client, asks for resources, and gets a lease.

---

## 1. The idea

Work requires resources, and can run anywhere those resources are provided:
CPU, memory, GPU, dev tools, or attached devices.

Glenn:
- *"The actual pattern is that cards require resources, and can run anywhere
  those resources are provided. Be it CPU, memory, GPU, dev tools whatever..."*
- *"Yes the idea is that any friends (or even workers...) can grab resources"*
- *"the resource system must function in nova-tools, even without nova-sprint"*
- *"so it might be client/server, with the central server arbitrating over stuff"*
- *"but it must not only give resources to the coordinator, anybody can ask for
  a resource and get it"*

In earlier versions, each bench had a fixed slot count on local disk and CI was
invisible to card slots. Finding a machine across the fleet required probing
hosts one by one over SSH, queueing blindly on one chosen machine while another
sat idle, or gating fleet placement through a sprint coordinator.

In v3.0.0:
1. **Machines declare tags and calibrated whole-number resources** in
   `nova-config` (or locally in standalone single-machine mode).
2. **One central `nova-reserve serve` arbitrator** tracks every machine's
   capacity, active and paused leases, soft operator claims, and waiting
   requests over persistent connections — with **one local Unix-socket mode**
   (`nova-reserve serve --local`) running the exact same engine and protocol on
   a single machine with no fleet.
3. **Every caller is a first-class peer.** Resources are never reserved solely
   for a sprint coordinator: friends, bench workers, inner build steps, CI
   runners, human operators, scripts, and `nova-sprint` all speak the same
   protocol to the same ledger.
4. **CI and all other work share one ledger and two-layer OS priority,** so CI
   never waits for room, running commands are never killed or frozen mid-flight,
   and idle CI wastes zero compute.

---

## 2. Client/server architecture (`ONE PATH`)

`nova-reserve` (`cmd/nova-reserve`) is a standalone client/server resource
scheduler inside `nova-tools` with zero dependency on `nova-sprint`. One server
engine implements both fleet operation and isolated single-machine operation.

### 2.1 Central fleet server (`nova-reserve serve`)

- **Control-host daemon:** Runs as a managed service on the control host
  (`studio`), listening on Tailscale TCP (`NOVA_RESERVE_ADDR`, e.g.
  `studio:7680`) and authenticated via `NOVA_RESERVE_PASSWORD` supplied by
  `nova-secrets` (`nova-secrets exec --only NOVA_RESERVE_PASSWORD`).
- **Zero bench-to-bench SSH:** Tailscale ACLs permit outbound TCP from
  `tag:nova`, `tag:runner`, and `tag:control` to `studio`'s reserve port.
  Clients talk to `nova-reserve serve` over TCP; benches never need SSH access
  to one another, replacing `nova-fleet reserve` SSH fan-out probes.
- **Live `nova-config` inventory:** Reads machine rows (`tags` and `resources`)
  from `nova-config` (`config.machines`) at startup and reloads configuration
  edits live (`nova-reserve reload` or config refresh). If an edit lowers a
  machine's capacity below its currently active unpaused leases, in-flight work
  finishes undisturbed and new admissions wait until active usage drains below
  the new ceiling.

### 2.2 Standalone single-machine mode (`nova-reserve serve --local`)

- On an isolated machine with no fleet and no `nova-config`,
  `nova-reserve serve --local` listens on `/var/tmp/nova-reserve/sock` and reads
  local machine capacity from `/var/tmp/nova-reserve/capacity.json` (calibrated
  by `nova-reserve bench` or written by `nova-reserve set`).
- **One path:** Local mode speaks the **exact same newline-delimited JSON
  protocol** and runs the exact same admission, nesting, pause, claim, and
  deadlock engine as fleet mode (targeting the single implicit host `local`). If
  `NOVA_RESERVE_ADDR` is unset and `/var/tmp/nova-reserve/sock` is requested on
  a standalone machine, `nova-reserve` connects to the local Unix socket (and
  may auto-start `nova-reserve serve --local` when configured for local use).

### 2.3 Peer clients (who asks for leases)

Every caller connects directly as a peer client:
- **Friends** (`emma`, `rowan`, `stella`, `johnny`, `freddy`, `aurora`)
  acquiring one or more machines for interactive research, cold reviews, TLA+
  model checking, benchmarks, or builds;
- **Bench workers** (`nova-worker`, or a subagent/script running inside a
  worker) requesting host-pinned or fleet-any capacity;
- **CI runners** (`job-started.sh` on Linux/macOS and WSL2 runner hooks on
  hybrid Windows hosts) taking `--holder ci` leases for the exact lifetime of
  each GitHub Actions job;
- **People** (`glenn` or an operator) running interactive commands or placing
  timed soft claims (`nova-reserve claim`);
- **Standalone scripts** and build harnesses;
- **`nova-sprint`**, which acts as one peer client among others when dispatching
  sprint cards and never monopolizes or gates fleet resources.

---

## 3. Connection lifetime, server restart reconnect, and CI fail-open

### 3.1 Connection-bound leases

Every lease (and every queued request) is bound to the client's open TCP or
Unix-socket connection:
- **Keepalive and expiry:** Clients and server exchange `ping`/`pong` heartbeats
  every **`5s`**. A connection silent for **`15s`** (or closed by TCP `FIN`/`RST`
  or Unix-socket EOF) expires immediately.
- **Crash-safe release:** Normal command exit, uncaught panic, `SIGKILL`, power
  loss, or bench reboot closes or expires the connection and releases every
  lease it held — leaving zero stale lock files on benches.

### 3.2 `15s` reconnect grace on server restart

Upgrading or restarting `nova-reserve serve` on `studio` must never drop active
holds or interrupt running builds:
- When `nova-reserve serve` starts up, it enters a **`15s` reconnect window**
  (`reconnect_until`) before admitting new non-CI requests (`--holder ci` is
  always admitted immediately).
- Connected `nova-reserve` client wrappers (`run`, `hold`, `ssh`, `claim`) keep
  their local child processes running undisturbed across a socket drop, dial
  back with jittered sub-second backoff, and send a `reconnect` frame
  re-presenting their in-flight `lease_id`s (`host`, `needs`, `parent_lease`,
  `cpus`, `cores`, metadata, and remaining TTL for claims).
- The server reconstructs the live ledger from reconnected clients; once all
  known hosts report in or `15s` elapses, normal queue admission resumes with
  **zero dropped leases**.

### 3.3 CI fail-open (`3s`)

GitHub Actions starts a job's timeout clock on dispatch; a transient control
plane restart or network blip must never fail CI:
- When `--holder ci` connects to `nova-reserve serve`, it bounds initial dial
  and grant wait to **`3s`**.
- If `nova-reserve serve` is unreachable within `3s`, `nova-reserve` logs one
  warning line to stderr, runs the CI job locally under its elevated OS slice
  priority (`CPUWeight=10000` / `Interactive`), and retries registration in the
  background so the ledger reflects the CI job if the server comes back mid-run.

---

## 4. Tags, resources, calibrated units, and the Unix boundary

### 4.1 Tags (inexhaustible routing attributes)

A **tag** is a capability name a machine either has or does not have: `linux`,
`mac`, `windows`, `amd64`, `arm64`, `gpu`, `metal`, `ps5-sdk`. Tags are never
consumed; any number of jobs may match a tag concurrently.

- **Auto-derived OS and architecture tags:** `nova-config` automatically derives
  OS (`linux`, `mac`, `windows`, normalizing `darwin` to `mac`) and architecture
  (`amd64`, `arm64`) tags from each machine row.
- **SDK vs physical device naming convention:**
  - Shareable toolchains or SDKs that permit concurrent builds use a tag ending
    in **`-sdk`** (e.g. `ps5-sdk`).
  - Exclusive physical hardware uses a countable **resource** of amount `1`
    (e.g. `ps5 1`), guaranteeing single-job access to the physical devkit.
- Within a fleet, any identifier is either a tag or a resource across every
  machine, never both.

### 4.2 Resources (whole-number consumable quantities)

A **resource** is a name and a positive integer capacity on a machine:

| Resource | Unit | Example on a machine |
| --- | --- | --- |
| `cpu` | Calibrated CPU units ([§4.4](#44-cpu-units-and-core-steering)) | `cpu 24` |
| `memory` | Usable GiB (slice-bounded) | `memory 54` |
| Exclusive / countable device | Integer count (`1` for a single unit) | `ps5 1`, `gpu 2` |

Amounts are whole numbers, never fractions. In a `--needs` specification:
- `cpu 4, memory 8, linux, metal, ps5` requests `4` CPU units, `8` GiB of RAM,
  `1` unit of `ps5` (a bare resource name means `1`), on a machine carrying both
  `linux` and `metal` tags (contradictory tags match no machine and refuse
  immediately).
- If `cpu` is omitted, `cpu 1` is assumed. If `memory` is omitted, `memory 1` is
  assumed.
- `--exclusive` requests the target machine's entire resource vector.

### 4.3 Unix server boundary

Benches and `nova-reserve` run strictly on **`linux`**, **`mac`**, and
**`wsl2`**. There are no native Windows benches and no `nova-reserve.exe`. On
hybrid Windows/WSL2 runner hosts, Windows GitHub Actions hooks (`nova-ci-rules.ps1`)
invoke Unix `nova-reserve` inside WSL2 (`wsl.exe ... nova-reserve hold --holder ci ...`)
so one Unix client and one central ledger govern the physical host.

### 4.4 CPU units and core steering

Cores differ widely in single-thread and SMT throughput across server x86_64,
mini PCs, and Apple Silicon. `nova-reserve bench` measures each machine with a
built-in deterministic compiler/test benchmark across all usable logical cores
for a few seconds while holding the machine idle/exclusive:

- **CPU units:** `max(1, floor(measured_rate / standard_core_rate))`. One CPU
  unit equals roughly one standard server core. `fast-core` is excluded in
  v3.0.0; single-threaded work asks for `cpu 1`.
- **Usable memory (`memory` GiB):** Measured simultaneously from the bench work
  slice's effective RAM ceiling — cgroup v2 `memory.max` when bounded (accounting
  for Linux bench `MemoryMax=85%` so full-capacity leases never drive the host
  into kernel OOM), else physical GiB.
- **Core count conversion:** A lease holding `cpu_held` on a machine with
  `usable_cores` and calibrated `total_cpu` receives:

      cores = max(1, floor(cpu_held * usable_cores / total_cpu))

- **Physical-core / SMT pinning and build environment:**
  - On Linux, `nova-reserve` reads `/sys/devices/system/cpu/cpu<N>/topology/thread_siblings_list`
    and reports SMT sibling groups to the arbitrator, which assigns disjoint
    physical cores across concurrent unpaused leases on that host; the local
    `nova-reserve` wrapper applies `sched_setaffinity(2)` (excluding kernel
    `isolated` CPUs).
  - On all Unix platforms, `nova-reserve` exports:
    - `NOVA_RESERVE_LEASE=<lease_id>`
    - `NOVA_RESERVE_HOST=<host>`
    - `NOVA_RESERVE_CPUS=<cores>`
    - `GOMAXPROCS=<cores>`
    - `CARGO_BUILD_JOBS=<cores>`
    - `RAYON_NUM_THREADS=<cores>`
    - `CMAKE_BUILD_PARALLEL_LEVEL=<cores>`
    - `MAKEFLAGS=-j<cores>`
  - `nova-sandbox` automatically allows positive-integer (`^[1-9][0-9]*$`)
    values of the thread-count variables and validated `NOVA_RESERVE_LEASE` /
    `NOVA_RESERVE_HOST` tokens through its clean-environment wall.

---

## 5. Two request modes, CLI surface, and vector admission

### 5.1 Fleet-any vs host-pinned requests

1. **Fleet-any (no `--host`):**

       nova-reserve hold --needs "cpu 8, memory 16, linux" --holder friend --source emma --why "tlc check"
       nova-reserve ssh  --needs "cpu 8, memory 16, linux" --holder friend --source emma --why "go test" -- go test ./...

   - Evaluates every online machine carrying all requested tags and sufficient
     total capacities.
   - If one or more machines fit right now, grants immediately on the best
     fitting machine (most available headroom / fewest active leases) and
     returns `{lease_id, host, cpus, cores, env}`.
   - If all matching machines are currently busy, places the request in **one
     central fleet queue** and grants whichever matching machine frees room
     **first** — eliminating blind per-host queue stalls and check-then-SSH
     races.
   - `nova-reserve ssh` is a convenience wrapper for callers with SSH access
     (friends, operators): it acquires a fleet-any lease, opens `ssh <host>`,
     propagates `NOVA_RESERVE_LEASE` and thread-count steering, runs `<cmd>`,
     and releases the lease when `<cmd>` exits.

2. **Host-pinned (`--host <machine>`, or `nova-reserve run` on the local host):**

       nova-reserve run --needs "cpu 4, memory 8" --holder bench --source worker-b1 -- make test
       nova-reserve hold --host hulk --needs "cpu 16, memory 32" --holder person --source glenn

   - `nova-reserve run` defaults `--host` to the local machine's hostname.
   - Admits or queues strictly against `<machine>`.

### 5.2 CLI verbs and metadata flags

```text
nova-reserve serve   [--local] [--listen ADDR] [--dir DIR]
nova-reserve run     [--host M] [--needs SPEC | --exclusive] [--nowait | --wait D] [meta...] -- <cmd> [args...]
nova-reserve hold    [--host M] [--needs SPEC | --exclusive] [--nowait | --wait D] [meta...] [--json]
nova-reserve ssh     [--needs SPEC | --exclusive] [--nowait | --wait D] [meta...] -- <cmd> [args...]
nova-reserve claim   [--host M] [--needs SPEC | --exclusive] [--ttl D] [--why TEXT] [--json]
nova-reserve release <lease_id>
nova-reserve status  [--host M] [--json]
nova-reserve bench   [--force] [--write-config] [--json] [--dir DIR]
```

Metadata flags (`[meta...]`):
- `--holder <friend|bench|ci|person|sprint|script>` (default `person`)
- `--source <caller-name>` (e.g. `emma`, `rowan`, `glenn`, `sprint-v3`,
  `worker-b1-1`; drives per-caller round-robin fairness and status attribution)
- `--consumer <id>` (workflow/card continuity ID across sequential commands)
- `--label <ci|claim|blocker|critical|fix|read|work|custom>` (default `ci` for
  `--holder ci`, `claim` for `claim`, else `work`)
- `--priority <blocker|critical|read|high|normal|low>` (default `normal`)
- `--why <text>` (human note shown in `status`, queue blockers, and deadlock
  messages)

### 5.3 All-or-nothing vector admission

A request fits on candidate machine `M` when `M` has every requested tag and,
across **every** resource `r` (`cpu`, `memory`, `gpu`, `ps5`, custom):

    sum(unpaused leases on M, r) + needed(r) <= capacity(M, r)

(Active soft claims via `nova-reserve claim` also reserve their claimed amount
against `capacity(M, r)` for new admissions; see [§7.3](#73-timed-soft-user-claims-nova-reserve-claim).)

- **Identical rule for all resources:** CPU, memory, and physical devices obey
  the exact same vector inequality. Partial grants never happen.
- **Immediate `impossible` refusal (`exit 1`):** If no machine in the target
  scope (`--host M` or the configured fleet) carries all requested tags and has
  total `capacity(M, r) >= needed(r)` for every `r`, `nova-reserve` refuses
  immediately (`refused: impossible`) without waiting.
- **`--nowait` and `--wait D`:** `--nowait` exits `1` (`NO ROOM`) immediately if
  the request cannot be granted right now; `--wait D` exits `1` on timeout,
  naming the blocking leases on stderr.

---

## 6. Nested leases and deadlock detection

Real engineering workflows nest: a worker or friend holds `cpu 8, memory 16` on
`space` and runs a test script or subagent that itself invokes `nova-reserve`.
Naive re-entrancy bypasses silently overcommit RAM or devices when a child asks
for more than its parent held; naive independent queueing deadlocks when the
parent holds the very resource the child waits for.

### 6.1 Parent lease discovery (`NOVA_RESERVE_LEASE` + PPID fallback)

1. `nova-reserve` exports `NOVA_RESERVE_LEASE=<lease_id>` and
   `NOVA_RESERVE_HOST=<host>` into every child environment, and `nova-sandbox`
   forwards safe `NOVA_RESERVE_LEASE` / `NOVA_RESERVE_HOST` tokens.
2. Each `request` also reports the client's local PID/ancestor PID chain on
   `<host>`. If an intermediate program scrubbed its environment before invoking
   `nova-reserve` on the same machine, the client/server matches the OS `PPID`
   chain against active local leases on `<host>` to recover `parent_lease`.

### 6.2 Sub-leases (`0` extra host debit)

When a request carries `parent_lease` `P` on the **same host** `M`:
- Let `ancestor_free(P, r) = granted(P, r) - sum(active subleases of P, r)`.
- If `needed(r) <= ancestor_free(P, r)` for every resource `r` (and `M` satisfies
  any requested tags), the server grants an immediate **sub-lease** (`sublease:
  true`) with **`0` additional debit** against `M`'s host ledger.
- Multiple parallel sub-commands inside `P` safely carve up `P`'s allocation;
  once `P`'s internal budget is full, further sibling sub-commands wait on one
  another inside `P` or spill into host admission safely.

### 6.3 Wait-for graph and `RESERVE DEADLOCK` refusal

When a nested request needs resources **beyond** `ancestor_free(P, r)` on the
same host (or requests resources on another host while `P` stays open waiting
for it), the server checks the directed **wait-for graph** before queueing:
- **Dependency edges:**
  1. Blocked waiter `W ->` every active lease `H` whose held resources prevent
     `W` from starting (on `W`'s pinned host, or across every matching candidate
     host if fleet-any has only one viable host `M` blocked by `H`).
  2. Active parent lease `P ->` every blocked descendant waiter `W` rooted
     under `P` (because `P` will not finish and release its resources while
     synchronously waiting on `W`).
- **Immediate cycle refusal (`exit 1`):** If adding `W` forms a cycle — whether
  a single-process self-deadlock (`P` holds `ps5 1` on `b1` and its child `W`
  asks for another `ps5 1` or `--exclusive` on `b1`) or a two-party AB-BA cycle
  across two leases — the server refuses `W` immediately with `exit 1`:

      nova-reserve: RESERVE DEADLOCK: request by <source> (<needs>) waits on lease <L1> (holder=<h>, why=<w>) which waits on descendant <W>

  Nothing hangs forever, and nothing silently overcommits memory or hardware
  past machine capacity.

---

## 7. CI wins, soft between-command pause, user claims, and fairness

### 7.1 CI wins immediately (`--holder ci`)

CI comes ahead of all other work on a machine:
- **Zero wait:** A `--holder ci` request on `--host <machine>` is granted
  immediately, even if `<machine>` is 100% full.
- **Logical pause on the ledger:** If `sum(unpaused leases on M, r) + ci_needs(r) > capacity(M, r)`,
  the server transitions overlapping active non-CI leases on `M` to **`paused`**
  one by one until the invariant holds:

      sum(unpaused non-CI leases on M, r) + sum(CI leases on M, r) <= capacity(M, r)

  Victim selection order: lowest priority first (`low` < `normal` < `high` <
  `read` < `critical` < `blocker`), breaking ties by youngest lease first (most
  recently started).
- **Paused work resumes first:** While `paused`, a lease contributes `0` to the
  unpaused sum. The instant CI releases its lease, `paused` leases on `M`
  unpause in priority order (highest priority first, oldest first on ties) as
  soon as their full resource vector fits — **ahead of every queued or newly
  arriving request**.

### 7.2 Never freeze (`SIGSTOP`) or kill in-flight commands

Running processes hold open TCP/SSH/database sockets, file locks, and compiler
pipelines; freezing (`SIGSTOP` / `cgroup.freeze`) or killing them mid-command
causes timeouts and partial writes.
- **Finish in flight under OS priority steering:**
  - **Linux cross-slice cgroup v2 weights:** `/system.slice` (where
    `github-runner-*.service` runs) is configured with `CPUWeight=10000`,
    `IOWeight=10000` (and unit `Nice=-10`), while `/user.slice` and bench worker
    units are configured with `CPUWeight=1`, `IOWeight=1`, and non-CI
    `nova-reserve` children additionally enter `SCHED_IDLE` + `nice 19`. While
    CI is idle, bench/friend work uses 100% of its pinned cores; the microsecond
    CI wakes up, the Linux scheduler gives CI ~99.99% CPU and IO share without
    stopping or killing bench processes.
  - **macOS QoS steering:** CI runs at normal/`Interactive` QoS while non-CI
    `nova-reserve` children run under background QoS (`taskpolicy -b` /
    `PRIO_DARWIN_BG`).
- **Yield at the next command boundary:** When a multi-step caller (carrying
  `--consumer <id>` or polling its lease `state` stream) finishes its current
  command while its lease is `paused`, the next `nova-reserve` invocation holds
  at the gate until the server transitions that lease/consumer back to `active`.
  Time spent waiting or paused is excluded from step and worker timeouts.
- **Runner sizing invariant:** Runner scopes on each machine are configured so
  that concurrent CI jobs on that host fit within machine capacity:

      sum over scopes s (runners_s * needs_s) <= capacity(M, r)

### 7.3 Timed soft user claims (`nova-reserve claim`)

When a person wants to reserve part or all of a bench for interactive testing or
drain a host gracefully without killing active work:

    nova-reserve claim --host space --needs "cpu 16, memory 32" --ttl 4h --why "kernel profiling"
    nova-reserve release <lease_id>

- **Immediate soft grant:** `--holder person` claims take effect immediately.
  Available capacity on `<machine>` may temporarily go negative while already
  running commands finish; running work is never killed, and **no new leases
  start** on the claimed resources until active usage drops below the remaining
  unclaimed capacity.
- **Bounded TTL (default `4h`):** Every claim has a mandatory expiry (`--ttl`,
  default `4h`, extendable by re-claiming or releasable early via
  `nova-reserve release <lease_id>`) so a forgotten claim never stalls a bench
  overnight.

### 7.4 Queue order, per-caller round-robin, and anti-starvation

Queued requests are scheduled by three rules:
1. **Priority class:** `ci` / `person` > `blocker` > `critical` > `read` >
   `high` > `normal` > `low` (with `paused` leases always resuming ahead of the
   queue).
2. **Round-robin across `--source` callers within each priority class:** Within
   the same priority tier, the arbitrator alternates grants across distinct
   `--source` identities (`emma`, `rowan`, `stella`, `sprint-v3`, `worker-b1`,
   etc.) rather than pure FIFO arrival, so a 100-card burst from `nova-sprint`
   never starves a friend or worker asking at the same priority.
3. **Head-of-line capacity reservation:** When the head waiter for a host (or
   the designated draining host for a fleet-any waiter) requires a large vector
   such as `cpu 16, memory 32` or `--exclusive`, freed resources on that host
   are held for the head waiter rather than leaked indefinitely to smaller
   `cpu 1` requests behind it — preventing starvation of large holds.

---

## 8. Wire protocol (newline-delimited JSON)

Both `nova-reserve serve` (Tailscale TCP, authenticated on the first frame via
`NOVA_RESERVE_PASSWORD`) and `nova-reserve serve --local` (Unix domain socket
`/var/tmp/nova-reserve/sock`, peer credentials verified via OS socket creds)
speak **newline-delimited UTF-8 JSON (`\n`-terminated objects, version `v3`)**.

### 8.1 Client-to-server messages

- **`auth`** (required first frame on TCP):
  `{"op":"auth","version":3,"password":"...","client":"emma@studio"}`
- **`request`** (acquire a fleet-any or host-pinned lease):
  `{"op":"request","req_id":"r1","mode":"any","host":"","needs":"cpu 8, memory 16, linux","exclusive":false,"nowait":false,"wait_ms":0,"parent_lease":"","holder":"friend","source":"emma","consumer":"c1","label":"work","priority":"normal","why":"tlc check","pid":48210}`
- **`claim`** (place a timed soft operator claim):
  `{"op":"claim","req_id":"r2","host":"space","needs":"cpu 16, memory 32","exclusive":false,"ttl_ms":14400000,"source":"glenn","why":"profiling"}`
- **`release`** (release a lease or cancel a queued `req_id` early):
  `{"op":"release","lease_id":"L-9041"}`
- **`reconnect`** (re-present live leases during the `15s` server restart window):
  `{"op":"reconnect","leases":[{"lease_id":"L-9041","host":"space","needs":{"cpu":8,"memory":16},"exclusive":false,"parent_lease":"","holder":"friend","source":"emma","consumer":"c1","label":"work","priority":"normal","why":"tlc check","cores":8,"cpus":[0,1,2,3,16,17,18,19],"pid":48210}]}`
- **`subscribe`** (stream initial `snapshot` + live `event` transitions for
  `nova-reserve status`, `nova-fleet resources`, and `nova-sprint` board):
  `{"op":"subscribe","host":""}`
- **`ping`** (every `5s` keepalive):
  `{"op":"ping","ts":1794869000000}`

### 8.2 Server-to-client messages

- **`queued`** (request accepted into queue, updated when position changes):
  `{"op":"queued","req_id":"r1","position":2,"reason":"waiting for cpu 8 on linux","blockers":["L-9038 (sprint:card-42)","L-9039 (ci:unit)"]}`
- **`granted`** (lease or claim active):
  `{"op":"granted","req_id":"r1","lease_id":"L-9041","sublease":false,"host":"space","needs":{"cpu":8,"memory":16},"cores":8,"cpus":[0,1,2,3,16,17,18,19],"env":{"NOVA_RESERVE_LEASE":"L-9041","NOVA_RESERVE_HOST":"space","NOVA_RESERVE_CPUS":"8","GOMAXPROCS":"8","CARGO_BUILD_JOBS":"8","RAYON_NUM_THREADS":"8","CMAKE_BUILD_PARALLEL_LEVEL":"8","MAKEFLAGS":"-j8"},"expires_at":""}`
- **`state`** (pushed asynchronously when CI pauses or unpauses a held lease, or
  when a claim TTL expires):
  `{"op":"state","lease_id":"L-9041","state":"paused","reason":"displaced by ci (L-9050 on space)"}`
- **`snapshot`** (full fleet/host state on `subscribe` or `status --json`):
  `{"op":"snapshot","reconnect_until":"","machines":[{"host":"space","online":true,"tags":["linux","amd64"],"usable_cores":32,"resources":{"cpu":24,"memory":54},"free":{"cpu":16,"memory":38},"leases":[...],"claims":[...],"queue":[...]}]}`
- **`event`** (incremental state transition after `snapshot`):
  `{"op":"event","type":"granted|paused|resumed|released|claimed|queued","lease_id":"L-9041","host":"space","summary":"granted cpu=8 memory=16 to emma","at":"2026-11-16T22:55:00Z"}`
- **`refused`** (terminal rejection):
  `{"op":"refused","req_id":"r1","code":"impossible|no_room|timeout|deadlock|unauthorized|bad_request","message":"...","cycle":["L-9041","r9"]}`
- **`pong`**:
  `{"op":"pong","ts":1794869000000}`

---

## 9. Observability and health check

Each machine still measures its real CPU and memory utilization internally for
health checking:
- `nova-config resources` shows configured tags and resource capacities in both
  directions (machine `->` resources and tag/resource `->` machines).
- `nova-reserve status [--host <machine>] [--json]` and
  `nova-fleet resources [--json]` query `nova-reserve serve` in sub-millisecond
  single-RPC snapshots (no SSH fan-out), reporting capacity, free amounts,
  active/paused leases, soft claims with remaining TTLs, and queued waiters.
- `nova-sprint` subscribes to `nova-reserve serve` (`subscribe`) as a peer
  observer to render the fleet Resources view live alongside active sprint
  cards.
- Comparing observed host load against active leases surfaces unheld runaway
  processes (host far busier than leases) or mis-sized resource declarations
  (work consistently using far more or far less than requested).

---

## 10. Settled decisions in v3.0.0

1. **Standalone client/server in `nova-tools` (`ONE PATH`).** `nova-reserve`
   arbitrates fleet resources over Tailscale TCP (`nova-reserve serve` on
   `studio`) and single-machine resources over `/var/tmp/nova-reserve/sock`
   (`nova-reserve serve --local`) using one engine and one NDJSON protocol,
   with zero dependency on `nova-sprint` and zero bench-to-bench SSH.
2. **True peer access.** Friends, bench workers, CI runners, people, scripts,
   and `nova-sprint` all request leases directly as peers; resources are never
   gated through a sprint coordinator.
3. **Connection-bound lifetime + `15s` reconnect + `3s` CI fail-open.** Socket
   closure/expiry frees leases automatically; server restarts preserve live
   leases via a `15s` reconnect window; CI fails open after `3s` if the server
   is unreachable.
4. **Fleet-any central queue + host-pinned mode.** Omitting `--host` waits in
   one central queue for the first matching machine to free up; passing `--host`
   (or `run`) pins to a specific machine.
5. **Nested sub-leases + cycle refusal (`RESERVE DEADLOCK`).** Descendants fit
   inside their ancestor's un-subleased grant at `0` extra host debit; excess
   requests check the wait-for graph and refuse cycles immediately (`exit 1`).
6. **CI wins + between-command pause + two-layer OS priority.** CI never waits;
   displaced leases finish their current command under `CPUWeight=1`/`SCHED_IDLE`
   (Linux) or `taskpolicy -b` (macOS), yield at the next command boundary, and
   resume before any queued or new work.
7. **Timed soft user claims + per-source round-robin + HoL reservation.**
   `nova-reserve claim` drains/reserves capacity immediately with a default `4h`
   TTL; queues schedule by priority class, round-robin across `--source`, and
   reserve capacity for large head-of-line waiters.
8. **Unix server boundary & auto-derived tags.** OS/arch tags come from
   `nova-config`; `-sdk` tags vs amount-1 device resources distinguish shared
   SDKs from exclusive hardware; benches and `nova-reserve` are strictly Linux,
   macOS, and WSL2 (`fast-core` excluded in v3.0.0).

---

## 11. Future (`v3.1`)

Designed hooks deferred out of v3.0.0 to keep the initial client/server rollout
tight:
1. **GNU Make / Ninja / Cargo FIFO jobserver (`--jobserver-auth=fifo:<path>`):**
   Dynamic mid-build token elasticity on each bench so `cargo`, `cmake`,
   `ninja`, and `make` shed inner compile worker tokens smoothly when a CI lease
   arrives and reclaim tokens as soon as CI finishes, complementing kernel
   `CPUWeight` scheduling.
2. **Hold-duration history and non-delaying backfill (EASY backfilling):**
   Recording observed lease durations by `(label, needs)` so short, small
   requests can backfill into idle capacity held for a head-of-line large waiter
   strictly when predicted completion will not delay the head waiter's start.
3. **Named resource presets and multi-machine gang leases:** Reusable resource
   presets (e.g. `@rust-heavy`, `@tlc-large`) in `nova-config`, plus atomic
   all-or-nothing multi-host leases (acquired in canonical host order so
   distributed client/server tests never deadlock across machines).
