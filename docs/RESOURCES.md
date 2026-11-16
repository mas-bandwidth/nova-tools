# Resources

What each machine in a fleet has, and what work takes from it.

This page is a design specification for resources in nova-tools. It states the
rules and mechanisms that govern what each machine in a fleet has, and what work
takes from it. It matches the nova-sprint v3.0.0 specification (`docs/SPEC-V3.md`
in nova-sprint) 100%.

Everything here belongs to nova-tools and works without nova-sprint.
nova-sprint builds on it: its dealer uses resources to decide where work runs.

## The idea

Work requires resources, and can run anywhere those resources are provided:
CPU, memory, GPU, dev tools, or attached devices.

In earlier systems a fleet machine had one number that limited work: how many
jobs it ran at once. A job cost one of them whatever it did (or all of them,
when it asked for the whole machine), and CI jobs were not counted at all.

With resources a machine declares what it has, work declares what it needs,
and how many jobs fit follows directly from what is available.

The first purpose is routing work to the right machine: work that needs a GPU,
Metal, Windows, or an attached device goes only where that exists. The second is
that CI and other work share one ledger, so neither can starve the other
unseen.

## Two words: tags and resources

A **tag** is a name a machine has or does not have: `linux`, `mac`, `windows`,
`gpu`, `metal`. A tag is never used up. Any amount of work can run on a `linux`
machine at once; the tag only says where work may go.
A machine's operating system (`linux`, `mac`, `windows`, with `darwin`
normalized to `mac`) and architecture (`amd64`, `arm64`) are automatically
derived as tags from `nova-config`.

A **resource** is a name and a whole number: `cpu 16`, `memory 64`, `ps5 1`.
Work holds an amount of it while it runs, and that amount is not available to
anything else until the work ends.

Amounts are whole numbers, never fractions, and the units are coarse on purpose.

| Resource | Unit | On a machine |
| --- | --- | --- |
| `cpu` | CPU units ([below](#cpu-units)) | `cpu 16` |
| `memory` | gigabytes (GiB) | `memory 64` |
| a thing the machine has one of | 1 | `ps5 1` |

A thing is a resource with amount 1. It cannot be split: work holds all of it
or does not name it.

The same hardware name can be a tag or a resource, and the choice says whether
work shares it. Within a fleet a name is one or the other, on every machine.
Naming convention:
- Shareable toolchains or SDKs that allow concurrent builds use a tag with the
  `-sdk` suffix (e.g. `ps5-sdk` tag allows multiple compile jobs to run at once).
- Exclusive physical devices use an amount-1 resource (e.g. `ps5 1` ensures only
  one job accesses the physical devkit at a time).

## Machines

A machine's tags and resources are configuration, kept in nova-config beside
the machine (`config.machines` table). The link runs both ways: from a machine
to what it has, and from a tag or resource to the machines that have it
(`nova-config resources`).

| Machine | Tags | Resources |
| --- | --- | --- |
| captain | `linux`, `amd64`, `gpu` | `cpu 32`, `memory 128` |
| studio | `mac`, `arm64`, `gpu`, `metal` | `cpu 24`, `memory 64` |
| mini-m5 | `mac`, `arm64`, `gpu`, `metal` | `cpu 16`, `memory 32` |

Work that names `gpu` can go to any of the three. Work that names `gpu` and
`metal` can go to studio or mini-m5, and never to captain.

The default set of tools is installed on every machine with Ansible, so the
fleet is as uniform as it can be made. Tags and resources name what is counted
(CPU, memory) and what only some machines have.

CPU units and memory are measured by `nova-reserve` ([below](#cpu-units)). Tags
and custom resources are written by the machine's owner.

## Work

A piece of work states what it needs as one list of names, some with numbers:

    cpu 4, memory 8, mac, metal, ps5

Here `cpu 4` and `memory 8` are amounts, and `mac` and `metal` are tags. `ps5`
is a resource with no number, and a resource with no number means 1. The writer
does not have to know which names are tags and which are resources: each
machine's configuration says what it has, and a bare name reads correctly
either way.

Work that names no `cpu` holds `cpu 1`, and work that names no `memory` holds
`memory 1`. Everything that runs uses some of both.

Work fits on a machine when the machine has every tag the work names, and for
every resource the work names, the amount already held by unpaused work plus the
amount asked is at most what the machine has:

    sum(unpaused holds on r) + needed(r) <= machine's amount(r)

All resources are identical: CPU, memory, and devices obey this same rule. If
any needed resource is not available, the work cannot start.

Work that names something a machine does not have, or more of a resource than
the machine has in all, can never fit there. It is refused at once and does not
wait.

Work acquires everything it needs before it starts, holds it while it runs,
and releases all of it when it ends, however it ends. Together that is a
**hold**. A hold carries metadata:
- `--holder <bench|friend|ci|person>`
- `--consumer <id>`
- `--label <ci|blocker|critical|fix|read|work|custom>`
- `--priority <blocker|critical|read|high|normal|low>`
- `--why <text>`

## What takes holds

Everything that uses a machine goes through holds:
- **An AI or external worker.** It acquires an atomic reservation through
  `nova-fleet reserve` before connecting over SSH.
- **Work placed by nova-sprint.** The sprint dealer assigns cards based on
  heartbeat resource capacity and held amounts.
- **A CI runner.** A runner has tags (the labels a workflow selects it by) and
  takes a hold for each job. It holds only while it runs a job; an idle runner
  holds nothing, so no compute is wasted while CI is idle.
- **A person or a script** running a command on a machine through `nova-reserve`.

## Holds on a machine: nova-reserve

nova-reserve is how a machine holds its resources. It works on a single machine
with no fleet and no sprint.

A machine's capacity is its tags and resources. A command states what it needs
by name, and nova-reserve holds exactly that.

A hold belongs to one running command on the machine. There are no tokens and no
leases. When the command ends, for any reason, the operating system frees the
hold.

Asking for the whole machine (`--exclusive`) holds everything the machine has.

Rules governing nova-reserve:
- **One authority and one directory.** `nova-reserve` on a machine is the one
  authority on what is held there. All callers (bench workers, CI runners,
  SSH sessions, scripts) use the single shared directory `/var/tmp/nova-reserve`
  with shared permissions.
- **Unix and server boundary.** Server tools run strictly on Linux, macOS, and
  WSL2. There are no native Windows benches and no `nova-reserve.exe`. On WSL2
  hosts, Windows CI runner hooks invoke `wsl.exe ... nova-reserve hold --holder ci`
  so one Unix ledger governs the physical host.
- **A hold steers process environment.** It exports `NOVA_RESERVE_CPUS=<cores>`,
  `GOMAXPROCS=<cores>`, `CARGO_BUILD_JOBS=<cores>`, `RAYON_NUM_THREADS=<cores>`,
  `CMAKE_BUILD_PARALLEL_LEVEL=<cores>`, and `MAKEFLAGS`.
  On Linux it sets OS priority (`CPUWeight=1`, `IOWeight=1`, `SCHED_IDLE`) for
  card holds, while CI runs at `CPUWeight=10000`, `IOWeight=10000`, `Nice=-10`.
  On macOS it sets `setpriority` `PRIO_DARWIN_BG` for card holds.
- **Strict arrival order.** Work that fits the machine but finds no room now
  waits in a strict FIFO line (`line/<seq>.lock`). A later, smaller request does
  not pass an earlier one.
- **Re-entrancy guard.** If `NOVA_RESERVE_DIR` in the environment matches the
  target directory, `nova-reserve` executes the command directly without taking
  a nested hold, preventing self-deadlock.

## CI wins

CI comes ahead of all other work on a machine. GitHub Actions starts the job's
clock when it hands the job over; a CI job that waited for room would fail on
timeout. Therefore, a CI job never waits.

- **It takes its hold at once,** even on a full machine.
- **Pause only between commands.** Running processes are never killed and never
  frozen mid-command with `SIGSTOP` or `cgroup.freeze`.
  Instead, active holds whose needed resources plus CI exceed the machine's
  capacity become logically **paused** on the ledger: lowest priority first, and
  youngest first on ties.
  Paused holds finish their already-running command at low OS priority
  (`CPUWeight=1` + `SCHED_IDLE` on Linux, `PRIO_DARWIN_BG` on macOS), and
  **cannot start their next command** until CI finishes and all their needed
  resources fit again. While paused, a hold counts for zero resources on the
  ledger.
- **The machine is never counted over what it has.** For all resources, what CI
  holds and what unpaused holds hold come to at most the machine's capacity:

      sum(unpaused holds on r) + sum(ci holds on r) <= machine's amount(r)

- **Paused work comes back first.** When a CI job ends, paused holds unpause
  (highest priority first) as soon as all their needed resources fit again,
  before any waiting or new work gets them. Continuing consumers unpause ahead
  of `line/` waiters.
- **All resources are identical.** In-flight commands are never killed for
  memory or devices. If all needed resources are not available, work yields at
  its command boundary and waits to start the next command.
- **Runner scopes fit the machine.** CI runners are sized so that all runner
  scopes fit within the machine's total capacity:

      sum over scopes s (runners_s * needs_s) <= machine's amount(r)

  so a CI job never has to wait for another CI job.
- **Deadlines exclude paused time.** Time spent paused or waiting in line is
  excluded from worker and step execution deadlines.

## Atomic fleet reservation

To send work to the right machine without check-then-act races, nova-tools
provides **atomic fleet reservation**:

    nova-fleet reserve --needs "<spec>" [--holder friend] [--consumer <id>] [--wait <dur>]

1. `nova-fleet` queries `nova-config` for machines matching the requested tags
   and resource amounts.
2. It probes candidates with `ssh <addr> nova-reserve hold --nowait --needs ...`.
3. The first machine to atomically grant the hold wins.
4. `nova-fleet` holds the SSH reservation open over a background session or FD
   (auto-releasing on disconnect or process exit) and prints the assigned host.
5. The worker connects to the assigned host knowing the reservation is already
   held and locked.

Fleet resource queries across machines are answered in parallel by:

    nova-fleet resources [--json]

## What can be asked

nova-tools answers questions for workers and tools:
- What tags and resources exist, and on which machines (`nova-config resources`).
- What is held now, and by what, on a machine (`nova-reserve status --json`).
- Atomic fleet reservation and fleet-wide status (`nova-fleet reserve`, `nova-fleet resources`).

The answers cover everything that holds, CI included. nova-sprint's board
visualizes this under the Resources table.

## CPU units

CPU is measured in CPU units, not cores, because cores differ in power from
machine to machine.

A machine has one CPU number: how much compute it has. Work says how much
compute it needs, and the machine it lands on works out how many of its own
cores that is.

### What a CPU unit is for

A CPU unit has one job: to let work say how much compute it needs, once, in a
way that means the same thing on every machine.
- Work can be sent to any machine with enough free, without its author knowing
  the hardware details.
- The machine it lands on turns the request into the right number of cores.
- What a machine hands out can be added up, and never exceeds what it has.

It is coarse, whole-number, and approximate.

### How big it is

One CPU unit is one standard core: about one core of an ordinary machine.
`cpu 4` reads as "about four cores". A machine with fast cores is worth more CPU
units than it has cores, and a machine with slow cores or hyperthreads is worth
fewer.

### How a machine gets its number

`nova-reserve measure` measures it with an embedded benchmark: a fixed mix of
compiler and test execution, running for a few seconds with every usable core
busy. Hyperthreads and slow cores are inside the measurement and need no rule of
their own.

The standard core is a constant inside the benchmark, the same in every copy of
the tools. A machine's CPU units are its score divided by the constant, rounded
down, at least 1.

A machine is measured when it joins a fleet. The benchmark auto-runs on
`run`/`hold` only when `<dir>` is the system default `/var/tmp/nova-reserve` and
`capacity.json` is missing; it never auto-runs on custom or temporary
directories.
The benchmark holds `--exclusive` while it runs. Usable memory is measured at
the same time from cgroup v2 `memory.max` if bounded, else physical GiB. If a CI
job arrives during calibration, the benchmark aborts with non-zero exit rather
than saving an invalid score.

### From CPU units to cores

CPU is counted in CPU units. Cores are only what the work is told to use:

    cores to use = units held x usable cores / machine's CPU units, rounded down, at least 1

Where nova-reserve can pin (Linux), it dynamically assigns disjoint physical
cores and SMT pairs across currently unpaused holds. Elsewhere the core count is
communicated via environment variables (`NOVA_RESERVE_CPUS`, `GOMAXPROCS`, etc.).

### Work that cannot be split

Work that cannot be split across multiple cores asks for `cpu 1` and runs on
one core. `fast-core` is excluded from v3.0.0.

## The check

Each machine still measures its real CPU use. The measurement is kept internal
for health checking and is not drawn on the sprint board.

What is held is a plan; what the machine measures is what happened. A machine
far busier than its holds has unheld processes running. Work that uses far more
or far less than it held was given inaccurate specifications.

## Building v3.0.0

Order of implementation in nova-tools:
1. `modules/config`: tags, resources, auto-derived OS/arch tags, and PostgreSQL schema migration.
2. `cmd/nova-reserve`: named holds, measurement, `/var/tmp/nova-reserve`, FIFO line, paused holds, and OS priority steering.
3. `cmd/nova-fleet`: atomic reservation (`nova-fleet reserve`), fleet resources status, and GitHub runner integration.

## Settled decisions in v3.0.0

1. **CI runner holds.** Detached process spawned in `job-started.sh` watching
   `Runner.Worker` via PID/kqueue, releasing in `job-completed.sh`.
2. **Fleet reservation.** Atomic reservation via `nova-fleet reserve --needs ...`.
3. **Pausing behaviour.** Pause only between commands; no mid-command process
   freezing or killing; OS priority steering during command execution.
4. **Operating system and architecture as tags.** Automatically derived from
   `nova-config` rows (`linux`, `mac`, `windows`, `amd64`, `arm64`).
5. **Unix and server boundary.** Strictly no native Windows benches and no
   `nova-reserve.exe`. Server tools run on Linux, macOS, and WSL2.
6. **Unified fit rule.** All resources are identical. If any needed resource
   does not fit, work yields at command boundaries and waits to resume.
