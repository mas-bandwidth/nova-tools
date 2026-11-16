# nova-reserve

nova-reserve coordinates shared resources on one machine across local commands,
continuous integration jobs, and background workers. Each caller takes a **hold**
naming the **tags** and whole-number **resources** (`cpu`, `memory`, and custom
devices) it needs; when resources do not fit, later arrivals wait in arrival
order. A machine's tags, usable cores, and resource totals live in
`capacity.json`.

## Install and try

[Install one tool](../../docs/README.md#install-one-tool), choosing `reserve`.
It needs no server or database. Callers that share a machine point `--dir` at
the same local directory (`/var/tmp/nova-reserve` by default). `nova-reserve help run`
prints the command syntax and changes nothing.

The examples use a POSIX shell on Linux or macOS with a disposable reservation
directory:

```sh
reserve_dir=$(mktemp -d)
```

The directory is created if absent, while its parent must already exist.

## Measure or set the capacity

Measure CPU units with the built-in compiler/test benchmark and read usable
memory (bounded by Linux cgroup `memory.max` when set, else physical GiB):

```sh
nova-reserve measure --force --dir "$reserve_dir"
```

```text
MEASURE OK cpu=8 memory=32 cores=8
```

`measure` holds the whole directory while running and refuses when background
CPU load exceeds 25% unless `--force` is given. On the default directory
`/var/tmp/nova-reserve`, the first `run` or `hold` runs `measure` automatically
when `capacity.json` is absent; custom `--dir` directories require `measure` or
`set` first.

Use `set` to record tags, custom whole-number devices, or explicit resource
totals atomically, even while holds are active:

```sh
nova-reserve set --dir "$reserve_dir" --cpu 8 --memory 32 --cores 8 --tags gpu --resources ps5=1
```

```text
CAPACITY SET cpu=8 memory=32 cores=8
```

Repeating the identical values prints `CAPACITY SET cpu=8 memory=32 cores=8 note=unchanged`.
Operating-system (`linux`, `mac`) and architecture (`amd64`, `arm64`) tags are
included automatically.

## Run a command

```sh
nova-reserve --dir "$reserve_dir" --needs "cpu 2, memory 4, gpu" --wait 15m --why "unit tests" -- sleep 1
```

```text
nova-reserve: HELD cpu=2 memory=4 cores=2 pid=811078
```

When `--needs` is omitted, a hold asks for `cpu 1, memory 1`. Bare resource
names mean `1` (`ps5` means `ps5 1`), and bare tag names require that tag on the
machine. A request for a missing tag or more of any resource than the machine
has in all exits 1 immediately without waiting.

Once the hold is granted, nova-reserve sets `NOVA_RESERVE_DIR`,
`NOVA_RESERVE_CPUS`, `GOMAXPROCS`, `CARGO_BUILD_JOBS`, `RAYON_NUM_THREADS`, and
`CMAKE_BUILD_PARALLEL_LEVEL` to the hold's core share (`cpu_held * usable_cores / total_cpu`,
at least 1). Nested `nova-reserve` calls targeting the same `NOVA_RESERVE_DIR`
run directly without taking a second hold.

On Linux, non-`ci` holds lower their scheduling priority (`SCHED_IDLE` and
`nice 19`) and pin to disjoint physical cores and SMT sibling groups from the
usable CPU mask (`cpus=0-1` on the `HELD` line); on macOS, non-`ci` holds enter
`PRIO_DARWIN_BG` (or `nice 19`). The pin is advisory steering, not a kernel
sandbox.

`--nowait` exits 1 immediately (`NO ROOM`) when resources are busy. `--wait D`
bounds how long a caller waits in line (`90s`, `15m`); the default waits until
room opens. `--exclusive` holds every resource on the machine.

## Hold over standard input

`hold` acquires the same reservation without spawning a child command, flushes
one `HELD` line on stdout, and keeps the hold until stdin reaches EOF or
`SIGINT`/`SIGTERM` arrives:

```sh
nova-reserve hold --dir "$reserve_dir" --needs "cpu 2, memory 4" </dev/null
```

```text
HELD cpu=2 memory=4 cores=2 pid=811080
```

## CI priority and between-command pause

A continuous integration job passes `--holder ci`:

```sh
nova-reserve --dir "$reserve_dir" --holder ci --needs "cpu 4, memory 8" -- sleep 1
```

`--holder ci` acquires immediately without waiting behind queued work. When
active non-`ci` holds plus the CI hold would exceed capacity on any resource,
nova-reserve marks the lowest-priority active holds (`low`, `normal`, `high`,
`read`, `critical`, `blocker`; youngest first on ties) as `paused` until
unpaused holds plus CI fit within every resource. In-flight child processes are
never stopped or killed; they finish their running step at low OS priority.

A multi-step caller passes `--consumer ID` across steps. When a paused step
finishes while CI still holds or waiters are lined up, its next step waits
before starting and resumes ahead of queued waiters (highest priority first) as
soon as CI finishes and its needed resources fit. While any hold is `paused`,
queued waiters do not cut ahead.

## Read the state

```sh
nova-reserve status --dir "$reserve_dir"
nova-reserve status --json --dir "$reserve_dir"
```

`status` prints the capacity line, each active or paused hold, each queued
waiter in arrival order, and free amounts per resource (`free cpu=8/8 memory=32/32 ps5=1/1 paused=0 waiting=0`).
`--json` prints machine-readable totals, `held_sprint`, `held_other`,
`held_by_label`, `paused_by_label`, `any_paused`, `sprint_consumers`, and
`paused_consumers`.

## Refusals and what to do next

Every refusal is one line on stderr.

| You see | Exit | What to do |
|---|---|---|
| `no hold within 1s: ...` | 1 | Needed resources stayed busy for the whole `--wait`. Run `nova-reserve status`, then retry or wait longer. |
| `NO ROOM: ...` | 1 | `--nowait` found resources busy or a hold paused. Retry later or drop `--nowait`. |
| `needs cpu 64 exceeds machine capacity 8; run: nova-reserve status ...` | 1 | Request fits within the machine's capacity, or pick a larger machine. |
| `machine lacks tag or resource "gpu"; run: nova-reserve status ...` | 1 | Run on a machine that carries the tag or custom resource, or register it with `set`. |
| `machine is busy (60% CPU in use); stop other work or pass --force; run: nova-reserve measure --force ...` | 1 | Wait for background work to finish so measurement is clean, or pass `--force`. |
| `--dir DIR has no capacity.json; measure or set capacity first; run: nova-reserve measure --force --dir DIR` | 2 | Measure or set the custom directory's capacity first. |
| `NAME: command not found on PATH=...` | 127 | Fix PATH or give the full path. A name with a slash prints `NAME: no such file`. No hold was taken. |
| `NAME: not executable` | 126 | Fix the file's permissions. No hold was taken. |
| `--dir DIR: parent PARENT does not exist; run: mkdir -p PARENT && ...` | 2 | Run the printed `mkdir` and repeat the command. |
| `--dir DIR is a file, not a directory; run: ls -ld DIR` | 2 | Give a directory, or a path that does not exist yet. |
| `... status could not show this hold; run: sudo rm -f FILE` | 2 | A holder file another user owns cannot be replaced or written. Run the printed command. No hold is kept. |
| `... is not a reservation directory ...` | 2 | `status` found no reservation directory there. Pass the `--dir` your jobs share. |
| `capacity=? # corrupt: ...` | 0 | `capacity.json` is damaged. Run `measure --force` or `set` to replace it. |

A bad or repeated flag, a missing command, a non-positive resource integer, or a
`--wait` not above zero exits 2; `nova-reserve help run` (or `hold`, `measure`,
`set`, `status`) shows the usage. A run that starts returns the command's own
status; if a signal ended it, 128 plus the signal number, and stderr says so:
`nova-reserve: make ended by signal SIGKILL; exit 137`.

## Guarantees and limits

Guarantees:
- Unpaused holds plus CI holds never exceed the machine's capacity on any
  resource, and an exclusive hold takes all of them.
- Each hold and queued waiter owns an open kernel `flock` descriptor, so the
  operating system releases it automatically on exit or `SIGKILL` without PID
  reuse races or a background daemon.
- Waiters wake on `inotify` (Linux) or `kqueue` (macOS/BSD) events and advance
  in arrival order behind any unpausing holds.
- `set` replaces `capacity.json` atomically while holds are active.

Limits:
- Only cooperating commands that share `--dir` on a local filesystem participate
  in the ledger.
- Resource counts and CPU affinity steer build parallelism and scheduling
  priority; they do not enforce memory limits or sandbox untrusted code.
- On Linux, macOS, and the BSDs, an internal watchdog process and Linux
  `Pdeathsig` clean up the child process group if `nova-reserve` dies by
  `SIGKILL`.
- Release builds cover Linux amd64 and arm64 and macOS arm64; on Windows hosts,
  invoke the Linux binary inside WSL2 so one Unix ledger governs the machine.
