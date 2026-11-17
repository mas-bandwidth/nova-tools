# nova-reserve

nova-reserve arbitrates shared compute and hardware resources across peer
callers — friends, bench workers, CI runners, operators, scripts, and
`nova-sprint` — either across a fleet over Tailscale TCP (`nova-reserve serve`)
or on a standalone machine over a local Unix domain socket (`--local`,
auto-started on demand when `NOVA_RESERVE_ADDR` is unset).

Each request names the **tags** (`linux`, `mac`, `amd64`, `arm64`, `ps5-sdk`)
and whole-number **resources** (`cpu`, `memory`, `ps5`, `gpu`) it needs. Both
transports run the exact same deterministic arbitrator and newline-delimited
JSON protocol; see [docs/RESOURCES.md](../../docs/RESOURCES.md).

## Install and try

[Install one tool](../../docs/README.md#install-one-tool), choosing `reserve`.
On a standalone machine it needs no external database: local callers point
`--dir` at a shared directory (`/var/tmp/nova-reserve` by default) and
`nova-reserve` starts `nova-reserve serve --local` automatically. `nova-reserve help run`
prints the command syntax and changes nothing.

```sh
reserve_dir=$(mktemp -d)
```

## Measure or set the capacity

Measure CPU units with the built-in compiler/test benchmark and read usable
memory (bounded by Linux cgroup `memory.max` when set, else physical GiB):

```sh
nova-reserve bench --force --dir "$reserve_dir"
```

```text
BENCH OK cpu=8 memory=32 cores=8
```

`bench` refuses when background CPU load exceeds 25% unless `--force` is given;
pass `--write-config` on a fleet bench to record `--cpu`, `--memory`, and
`--cores` in `nova-config`. On `/var/tmp/nova-reserve`, the first `run` or
`hold` runs `bench` automatically if `capacity.json` is absent; custom `--dir`
paths require `bench` or `set` first.

Use `set` to record tags, custom whole-number devices, or explicit resource
totals atomically, even while leases are active:

```sh
nova-reserve set --dir "$reserve_dir" --cpu 8 --memory 32 --cores 8 --tags gpu --resources ps5=1
```

```text
CAPACITY SET cpu=8 memory=32 cores=8
```

Operating-system (`linux`, `mac`) and architecture (`amd64`, `arm64`) tags are
derived automatically.

## Fleet server and peer modes

In fleet mode, `nova-reserve serve` runs on the control host, reading
`config.machines` from `NOVA_PG_DSN` and authenticating TCP peers via
`NOVA_RESERVE_PASSWORD`:

```sh
nova-reserve serve --listen :7680
```

Peers set `NOVA_RESERVE_ADDR` and `NOVA_RESERVE_PASSWORD` and request leases
either **fleet-any** (no `--host`, routing to the best fitting machine or
waiting in one central fleet queue for whichever machine frees room first) or
**host-pinned** (`--host <machine>`, or `nova-reserve run` on the local host):

```sh
nova-reserve hold --needs "cpu 8, memory 16, linux" --holder friend --source alice --why "tlc check" </dev/null
nova-reserve ssh --needs "cpu 4, memory 8, linux" --holder friend --source alice -- make test
nova-reserve --dir "$reserve_dir" --needs "cpu 2, memory 4, gpu" --wait 15m --why "unit tests" -- sleep 1
```

```text
nova-reserve: HELD lease_id=L-1 host=local cpu=2 memory=4 cores=2 pid=811078
```

When `--needs` is omitted, a request asks for `cpu 1, memory 1`. Bare resource
names mean `1` (`ps5` means `ps5 1`), and bare tag names require that tag on the
machine. `--exclusive` requests the machine's entire resource vector.

Once granted, `nova-reserve` exports `NOVA_RESERVE_LEASE`, `NOVA_RESERVE_HOST`,
`NOVA_RESERVE_CPUS`, `GOMAXPROCS`, `CARGO_BUILD_JOBS`, `RAYON_NUM_THREADS`,
`CMAKE_BUILD_PARALLEL_LEVEL`, and `MAKEFLAGS=-j<cores>`. On Linux, non-`ci`
commands enter `SCHED_IDLE` + `nice 19` and pin to disjoint physical cores / SMT
sibling groups (`cpus=0,4`); on macOS, non-`ci` commands run under background
QoS (`PRIO_DARWIN_BG`).

## Nested leases and deadlock detection

Commands running inside an active lease inherit `NOVA_RESERVE_LEASE` (with an OS
`PPID`-chain walk fallback if an intermediate program scrubbed its environment):
- Same-host nested requests that fit within their parent's remaining un-subleased
  grant receive an immediate **`0`-debit sub-lease** (`sublease=true`).
- Nested requests that exceed their parent's grant check the directed wait-for
  graph; any request that would form a self-deadlock or multi-lease cycle exits
  `1` immediately (`RESERVE DEADLOCK`).

## CI priority, soft user claims, and fairness

CI jobs pass `--holder ci`:

```sh
nova-reserve --dir "$reserve_dir" --holder ci --needs "cpu 4, memory 8" -- sleep 1
```

- **CI wins immediately:** `--holder ci` never waits; overlapping non-`ci`
  leases transition to `paused` in lowest-priority / youngest-first order while
  finishing their in-flight command under low OS slice priority, yield at the
  next command boundary, and unpause ahead of all queued work as soon as CI
  finishes. If the fleet server is unreachable within `3s`, `--holder ci` logs a
  warning and fails open so CI never breaks on a control-plane blip.
- **Timed soft user claims:** Operators reserve or drain capacity gracefully
  without killing active work (default `--ttl 4h`):

```sh
nova-reserve claim --dir "$reserve_dir" --needs "cpu 4, memory 8" --ttl 4h --why "profiling"
nova-reserve release C-1 --dir "$reserve_dir"
```

- **Fairness and anti-starvation:** Queued requests schedule by priority class,
  alternate round-robin across `--source` callers within each priority tier, and
  reserve capacity on the draining host for large head-of-line waiters.
- **Server restart resilience:** On startup, `nova-reserve serve` opens a `15s`
  `reconnect` window while live clients re-present their active leases over
  keepalive reconnect loops before new non-`ci` queue grants proceed.

## Observe and subscribe

```sh
nova-reserve status --dir "$reserve_dir"
nova-reserve status --json --dir "$reserve_dir"
nova-reserve subscribe --dir "$reserve_dir"
```

`status` prints machine capacities, active/paused leases, timed claims, and per-host
free vectors; `--json` prints one `snapshot` JSON object; `subscribe` streams
the initial `snapshot` frame followed by live `event` frames.

## Refusals and what to do next

Every refusal is one line on stderr.

| You see | Exit | What to do |
|---|---|---|
| `no hold within 1s: ...` | 1 | Needed resources stayed busy for the whole `--wait`. Run `nova-reserve status`, then retry or wait longer. |
| `NO ROOM: ...` | 1 | `--nowait` found resources busy or paused. Retry later or drop `--nowait`. |
| `RESERVE DEADLOCK: ...` | 1 | A nested `nova-reserve` call asked for resources blocked by its own enclosing lease cycle. Size the outer lease to cover its children, or release before re-acquiring. |
| `needs cpu 64 exceeds machine capacity 8; run: nova-reserve status ...` | 1 | Request fits within the machine's capacity, or pick a larger machine. |
| `machine lacks tag or resource "gpu"; run: nova-reserve status ...` | 1 | Run on a machine that carries the tag or custom resource, or register it with `set`. |
| `machine is busy (60% CPU in use); stop other work or pass --force; run: nova-reserve bench --force ...` | 1 | Wait for background work to finish so measurement is clean, or pass `--force`. |
| `--dir DIR has no capacity.json; measure or set capacity first; run: nova-reserve bench --force --dir DIR` | 2 | Measure or set the custom directory's capacity first. |
| `NAME: command not found on PATH=...` | 127 | Fix PATH or give the full path. A name with a slash prints `NAME: no such file`. No lease was taken. |
| `NAME: not executable` | 126 | Fix the file's permissions. No lease was taken. |
| `--dir DIR: parent PARENT does not exist; run: mkdir -p PARENT && ...` | 2 | Run the printed `mkdir` and repeat the command. |
| `--dir DIR is a file, not a directory; run: ls -ld DIR` | 2 | Give a directory, or a path that does not exist yet. |
| `... is not a reservation directory ...` | 2 | `status` found no reservation directory there. Pass the `--dir` your jobs share. |

A bad or repeated flag, a missing command, a non-positive resource integer, or
an authentication failure exits 2; `nova-reserve help run` shows the usage. A
run that starts returns the command's own status; if a signal ended it, 128 plus
the signal number, and stderr says so:
`nova-reserve: make ended by signal SIGKILL; exit 137`.
