# The functional run: one container per run

Status: design, not built. The verbs below do not exist yet; nothing in the
living docs names them. The state machine they implement is
[tla/FunctionalRun.tla](../tla/FunctionalRun.tla), checked with TLC (see
"The functional run (FunctionalRun)" in [tla/README.md](../tla/README.md)).
Where this document and the model disagree, one of them has a bug.

The functional tier (the tests behind `//go:build functional`) starts real
dependencies: `redis-server`, `postgres`, built binaries, child processes. Run
on a shared machine, those processes, their ports, their shared memory
segments and their temp directories outlive a test that is killed or times
out. This design moves the whole tier inside ONE container per run and makes
the container runtime, never the test and never the client, the owner of
everything the run creates. Unit tests do not change.

## 0. The rule the design stands on

A hard kill of the `podman run` client does not stop the run. The container
keeps running, held by the runtime's monitor (`conmon`), until a bound ends
it: the in-container `timeout`, or the runtime's own `--timeout`, which the
monitor enforces with the client dead. `podman kill` removes the container
at once. So:

- **the bound belongs to the runtime** (`podman run --timeout`), not to the
  client and not only to something inside the container, which a test can
  defeat (a child that ignores SIGTERM, a fixture that starts its server in a
  new session);
- **the removal belongs to the runtime** (`--rm`, run by the monitor) and to
  **a reaper** that selects by label and age, for every case the monitor
  cannot cover: a client killed between create and start, a monitor that
  died, a reboot;
- **the receipt is written exactly once**, by the client when it lives to see
  the removal, and by the reaper otherwise, and a run the reaper had to finish
  is reported to a person.

## 1. The runner verb

Today `nova-ci functional <package-dir>...` prints the selection for `make
test-functional`: the packages among its arguments that hold functional tests
and a `-run` pattern naming exactly those tests, or `CI FUNCTIONAL OK
packages=0 reason=...`; it takes no flags. The Makefile runs `go test -tags
functional -p $(GOTEST_P) -count=1 -timeout $(FUNCTIONAL_TIMEOUT) -run <pattern>
<packages>` on the host.

The design adds two verbs under the same path (names: open question 2):

```
nova-ci functional run  --image <ref@sha256:...> --src <dir> --receipts <dir>
                        --gomod-volume <name> --gocache-volume <name>
                        --deadline <duration> [--cpus <n>] [--memory <size>]
                        [--run-id <id>] <package-dir>...
nova-ci functional reap --receipts <dir> --grace <duration> [--dry-run]
```

Every path and name is a flag; nothing is guessed (SPEC.md, "no guessed
paths"). Exit codes are the repo's grammar: 0 the run passed (reap: nothing
reaped), 1 the tests failed or the run ended by a bound or a kill (reap: it
reaped or reported a run), 2 the verb could not run.

`run` does, in order:

1. **Select.** The same selection as `nova-ci functional` today: the
   packages with functional tests and the `-run` pattern. Nothing to run is
   `CI FUNCTIONAL OK packages=0` and exit 0, with no container.
2. **Check the inputs.** The image reference carries a digest, and the image
   declares no `VOLUME` (`podman image inspect`), so no anonymous volume can
   exist; the runtime is rootless and its runtime directory is on tmpfs
   (`podman info`), so a reboot is detected and handled (section 8). A failed
   check is exit 2 and nothing is created.
3. **Write the intent.** `<receipts>/<run-id>.intent`, created exclusively:
   the run id, the start time, the absolute deadline (start + `--deadline`),
   the machine's boot id, the image digest, the packages. From here on the run exists for the
   reaper, whatever happens to this process.
4. **Run the container**, attached, and stream its output:

   ```
   podman run --rm --timeout <seconds left before the deadline>
     --name nova-functional-<run-id>
     --label nova.functional.run=<run-id>
     --label nova.functional.start=<unix seconds>
     --label nova.functional.deadline=<unix seconds>
     --init --network none --ipc private
     --pids-limit 512 --memory <size> --memory-swap <size> --cpus <n>
     --read-only --tmpfs /tmp:rw,exec,size=2g --tmpfs <home>:rw,size=1g,mode=1777
     -v <src>:/src:ro -v <gomod-volume>:/gomodcache:ro -v <gocache-volume>:/gocache
     -e GOPROXY=off -e NOVA_FUNCTIONAL_RUN=<run-id> -w /src
     <image@digest>
     timeout -k 10 <deadline - 10 s> go test -tags functional -p <n> -count=1
       -timeout <deadline - 20 s> -run <pattern> <packages>
   ```

   The seconds left are computed when the container is created. When none
   are left the run is not started: the verb writes its receipt (outcome
   `late`) and exits 1. A `--timeout` of 0 is no timeout at all, so the verb
   never passes one.
5. **Check for leftovers** once `podman run` has returned: containers
   carrying the run's label (`podman ps --all --filter
   label=nova.functional.run=<run-id>`), volumes carrying it (`podman volume
   ls --filter label=...`), and processes still in the container's cgroup
   (`/proc/*/cgroup` naming `libpod-<container-id>.scope`). The verb waits a
   bounded time for the `--rm` removal to finish before it counts.
6. **Write the receipt**, `<receipts>/<run-id>.receipt`, created exclusively
   (section 5), and print it as one line. If the reaper wrote one first, the
   verb prints the reaper's receipt, says it lost the race, and exits 1.

The podman process is started with the CI runner's `RUNNER_TRACKING_ID`
removed from its environment (section 7), and the container gets no host
environment (`--env-host` is never used): only the variables named above.

### Each flag, and why

| flag | why |
|---|---|
| `--rm` | the runtime's monitor removes the exited container with its anonymous volumes, with or without the client |
| `--timeout <left>` | the runtime's hard bound, enforced by the monitor outside the container, with the client dead; the seconds left, so the runtime's clock and the label's deadline agree |
| `--name nova-functional-<run-id>` | for people reading `podman ps`; unique per run, so two runs never collide on a name. Never selected on |
| `--label nova.functional.run/start/deadline` | what the reaper and the leftover check select on: the run id, and the deadline it is judged by |
| `--init` | a PID 1 (`catatonit`) that reaps zombies and forwards signals, so a killed fixture's children neither linger nor eat the pid limit |
| own PID namespace (the default; never `--pid host`) | when PID 1 exits, the kernel kills every process in the namespace: this is what ends a `postgres` that `pg_ctl` started in a new session, which the inner `timeout` cannot signal |
| `--network none` | loopback only: fixtures bind 127.0.0.1 and unix sockets, nothing dials out, and no port is taken on the host |
| `--ipc private` | a private IPC namespace: Postgres's System V segment lives and dies with the container and never counts against the host's limit |
| `--pids-limit 512` | a runaway fixture or fork loop is bounded |
| `--memory`, `--memory-swap` equal, `--cpus` | the run's share of the machine; with the swap limit equal to the memory limit the container does not swap. Go reads the CPU limit from the cgroup and sets `GOMAXPROCS` to it |
| `--read-only` | nothing writes into the image's layers, so the container's writable layer stays empty |
| `--tmpfs /tmp`, `--tmpfs <home>` | temp files and `HOME` in memory, gone when the container stops; `mode=1777` because podman refuses a `uid=` tmpfs option, so the image's non-root user can write its home |
| `-v <src>:/src:ro` | the source is an input and nothing in the run may change it |
| `-v <gomod-volume>:/gomodcache:ro` and `-e GOPROXY=off` | the module cache is complete before the run; a missing module fails at once with a clear line instead of dialling a network that is not there |
| `-v <gocache-volume>:/gocache` | the build cache, one volume per shard so parallel runs never share a writer |
| `-e NOVA_FUNCTIONAL_RUN=<run-id>` | the run marker the fixture guard reads (section 6) |
| `timeout -k 10 ...` inside | a clean end with exit 124 before the runtime's bound, so the common hang gets a receipt that says so; `-k` sends KILL to a child that ignores TERM. A courtesy, not the guarantee |
| `go test -timeout` below that | `go test` prints the stack of the hung test before anything kills it |

### The two caches, and the networked step

The module cache is filled by a separate step that has the network, before
any run: `podman run --rm -v <src>:/src:ro -v <gomod-volume>:/gomodcache -w
/src <image@digest> go mod download`. Every run mounts it read-only. The
build cache is one named volume per shard, read-write. Both are named
volumes, not host directories, so the rootless user namespace's uid mapping
never touches host file ownership. They persist by design: they are not a
run's resources, carry no run label, are never counted as leftovers and are
never removed by the reaper (S5).

## 2. The model's actions, and what does each

| action in FunctionalRun.tla | what does it |
|---|---|
| `Request` | `run` writes `<run-id>.intent`, exclusively |
| `Create`, `Start` | `podman run --rm --timeout <left>` (create and start in one exec), only while the deadline is ahead |
| `Finish` | `go test` exits 0 (pass) or not (fail) |
| `InnerTimeout` | the in-container `timeout` expires: exit 124 (137 when `-k` fires) |
| `RuntimeDeadline` | the monitor kills the container at `--timeout` |
| `AutoRemove` | `--rm`: the monitor's cleanup removes the exited container and its anonymous volumes |
| `WriteReceipt` | `run` checks for leftovers, then creates `<run-id>.receipt` exclusively |
| `ClientDies` | the verb or its podman client is killed: `kill -9`, a cancelled CI job, the job's cap |
| `OperatorKill` | a person runs `podman kill` |
| `MonitorDies` | `conmon` is killed: by a person, or by the CI runner's job-end sweep of processes carrying the job's tracking id |
| `RuntimeLoss` | the machine reboots, or the user's session ends without lingering: every process of the user dies, records and storage stay |
| `Refresh` | podman's first command after the loss resets the records and removes the `--rm` containers that had run (section 8); it may not happen |
| `TickLate`, `TickOverdue` | the clock passes the deadline, then the deadline plus the reaper's grace |
| `ReapRemove` | `reap`: `podman rm --force --volumes` of a labelled container past its deadline plus the grace, in any state |
| `ReapReport` | `reap`: for an intent past its deadline plus the grace with no receipt, creates the receipt exclusively (outcome `reaped`) and reports it |
| `Daemonise` | never: a fixture that starts a dependency outside the container; the fixture guard refuses it |
| `ClientRemove` | never: cleanup owned by the client is a reversed witness |

## 3. The properties, how the code keeps each, and how a test shows it

| property | the code keeps it by | a test shows it by |
|---|---|---|
| S1 `RemovedHoldsNothing`: a removed run holds nothing | `--rm`; the reaper's `--volumes`; `--read-only` and tmpfs; refusing an image that declares a `VOLUME` | on a bench, every end (pass, fail, inner timeout, deadline, `podman kill`, client `kill -9`, reaped) followed by the leftover check at zero; a unit test that the reaper's argv carries `--volumes` and that an image with a `VOLUME` is refused |
| S2 `NothingOutside`: nothing of a run lives outside its container | the fixture guard (section 5), the code-side enforcement of S2 | a fixture called without the run marker refuses and starts nothing; a class test that every fixture that starts a dependency goes through the guard |
| S3 `CleanIsTrue`: a receipt says clean only when the run left nothing and its container was gone | the receipt is written only after `podman run` returned and the leftover check ran; clean is computed from the counts, never assumed | a unit test with a fake runtime that still lists the container: the receipt is not clean |
| S4 `NoTrespass`: nothing takes a run's container before it is overdue | the reaper selects by the `nova.functional.run` label and the deadline label plus the grace, never by name; container names carry the run id | a unit test: a young labelled container and an unlabelled `nova-functional-x` container are both left alone; the reaper's argv never holds `--filter name=` |
| S5 `CachesKept`: caches are never removed and never counted | caches carry no run label; the reaper never runs `volume rm` or `prune`; the leftover check lists volumes by the run's label only | unit tests over the argv of both; on a bench, the cache volumes survive a reap |
| S6 `OneReceipt`: at most one receipt per run | the receipt file is created with `O_CREAT\|O_EXCL`; the loser reads the winner's | a unit test racing the verb and the reaper over one run: one receipt |
| S7 `BoundHolds`: no container runs past its deadline plus the grace while its monitor lives | `--timeout` is the seconds left, never 0; a run with none left is not started | a unit test of the argv; on a bench, `kill -9` of the client under a hanging test: the container is gone by the deadline |
| L1 `RemovedEventually`: every created run is removed, whatever happens to its client | `--rm` and `--timeout` for the usual ends; the reaper for the rest (a client killed between create and start, a dead monitor, a reboot) | on a bench, a container created and never started (`podman create` with the labels, the client killed): `reap` removes it after the grace |
| L2 `ReceiptOrReport`: every run has a receipt or is reported to a person | the intent is written before anything is created; the reaper writes the receipt of every overdue intent that has none | a unit test with a fake clock: an intent with no container and no receipt, past its grace, gets a `reaped` receipt and a report |

The model's fairness, in words, and why each is reasonable:

- **time passes**: the clock is the machine's;
- **the runtime's deadline fires while its monitor lives**: `conmon`
  enforces `--timeout` outside the container, whatever the tests do;
- **the monitor's `--rm` cleanup runs once the container exits**: it is the
  monitor's exit action;
- **the reaper runs**: it is a timer outside any run (open question 4).

Nothing is assumed of the client (it may stall or die anywhere), of the tests
(they may hang and defeat the inner timeout), of a person, of the monitor's
survival, of a reboot, or of podman's refresh. The model's witnesses show
that each mechanism is needed: without the reaper, a client killed between
create and start, or a reboot while a container is created, leaves the
container for ever even with `--rm` and `--timeout`.

## 4. The reaper

`nova-ci functional reap --receipts <dir> --grace <duration> [--dry-run]`.

It selects **by label, never by a name pattern**:

1. `podman ps --all --filter label=nova.functional.run --format ...`: every
   container of any run, in any state (created, running, exited, configured
   after a reboot), with its `nova.functional.run` and
   `nova.functional.deadline` labels. A container whose deadline plus the
   grace is still ahead is left alone. One that is past it is removed with
   `podman rm --force --volumes <id>`. A container carrying the run label
   without a readable deadline label is reported and left alone.
2. Every `<run-id>.intent` in `--receipts` without a `<run-id>.receipt`,
   whose deadline plus the grace is past: the reaper creates the receipt
   exclusively, outcome `reaped`, with its own leftover counts after step 1.

It never removes a volume by itself, never prunes, never touches a container
without the run label, and never kills a process (a process outside any
container is reported, never killed: open question 11). It is bounded: every
podman command runs under a timeout, and the whole reap under one.

Its receipt is one line per run it reaped or reported (`REAPED run=<id>
state=<state> removed=<0|1> receipt=<written|present> containers=<n>
volumes=<n> processes=<n>`) and a summary line (`REAP containers=<n>
intents=<n> reaped=<n> left=<n> ms=<n>`). A reaped run is a judgment point:
something ended without its client's receipt, so the reaper exits 1 when it
reaped or reported anything, and the report goes to a person (open question
3). `--dry-run` prints what it would do and changes nothing.

It runs on a timer outside any run, and every `run` may also call it first
for the same receipts directory; both are safe because the selection is by
label and deadline only.

## 5. The receipt

`<receipts>/<run-id>.receipt`, created exclusively, one line of `key=value`
fields, the same line printed on stdout:

| field | value |
|---|---|
| `run` | the run id |
| `image` | the image digest the run used |
| `packages` | the packages, space-joined, quoted |
| `outcome` | `pass`, `fail`, `late` (never started), `reaped` |
| `ended` | how the container ended: `finished`, `inner-timeout`, `deadline`, `killed`, `lost`, `reaped`, `not-started` |
| `exit` | the container's exit code, `-` when unknown |
| `wall` | seconds from the intent to the receipt |
| `peak_memory`, `peak_pids` | from the container's cgroup (`memory.peak`, `pids.peak`) when the kernel provides them, `-` otherwise (open question 8) |
| `containers`, `volumes`, `processes` | the leftover check's counts |
| `clean` | `yes` only when all three counts are 0 and the container was gone when counted |
| `by` | `run` or `reap` |

How `ended` is read: exit 124 is `inner-timeout` (137 with the inner `-k`
firing after it); an exit by signal with the wall time at the deadline is
`deadline`; before it, `killed`; a container the reaper removed is `reaped`;
a receipt the reaper wrote for a run whose container was already gone is
`lost` when the machine rebooted since the intent (the boot id is in the
intent) and `reaped` otherwise. A test pins each.

## 6. The fixture guard (a later layer)

A functional fixture (the helpers that start `redis-server`, `postgres` or a
built binary for a test) refuses to start a dependency unless the run marker
is present: `NOVA_FUNCTIONAL_RUN` set, and the process inside a container
(`/run/.containerenv` present). The refusal names the verb to use. The marker
guards against accident, not malice.

An explicit override (`NOVA_FUNCTIONAL_OUTSIDE=1`, named in the refusal)
allows the legs that cannot be Linux containers: macOS (the darwin sandbox
and its volume tests) and Windows. The functional CI legs are Linux today,
so no CI leg sets it. Where it is set, process supervision applies instead,
and it must guarantee that **a dependency exits when its parent dies**: the
fixture never daemonises; the dependency is started in the foreground as a
child holding a pipe from the test process, and a watcher exits it when the
pipe closes (Linux adds `PR_SET_PDEATHSIG`). Supervision **bounds lifetime,
not consumption**: a supervised Postgres still takes a shared memory
segment, ports and disk on the host while it lives, which only a container
bounds.

The guard is the code-side enforcement of S2. It comes after the verb and the
reaper are secured, and nothing below it depends on it.

## 7. CI: the two-minute cap and the shards

`.github/workflows/ci.yml`'s `functional` job runs on merge_group, the
nightly schedule and by hand, never on a pull request. It deals the selected
packages (less the darwin-only ones) into four Linux shards, each a job with
`timeout-minutes: 2`, `GOMAXPROCS` at most 2, and steps that clean the
workspace, borrow the bench mirror, check out, pick the Go toolchain, install
`redis-server` and Postgres, and run `make test-functional
PKGS=<shard>` (`go test -p 2 -timeout 100s`).

Mapped onto container runs:

- one container per shard job; four shards are four concurrent containers,
  each `--cpus 2`, keeping the two-cores-a-leg rule, and each with its own
  build-cache volume;
- the two install steps go: the image carries `redis-server`, Postgres, git
  and every binary the functional tier execs, pinned to the fleet's major
  versions, so the job's setup is shorter;
- the module-cache step runs before the run with the network, and is
  seconds when the volume is warm;
- `--deadline` is the cap less the setup and a margin (about 90 s of the 120
  s), the inner `timeout` 10 s under it, `go test -timeout` 20 s under it;
- when the job hits its cap the runner kills the step: the client dies, and
  the container ends at its `--timeout`, at most the deadline after it
  started. The runner's job-end sweep kills every process carrying the job's
  `RUNNER_TRACKING_ID`; the verb starts podman without it, so the sweep never
  takes the runtime's monitor. Whether `conmon` would otherwise carry it is
  not measured; the model treats a dead monitor as an outside event the
  reaper covers.

Cost, measured on a 32-core Linux bench: the container adds about 0 to 0.1 s
of start-up and teardown to a warm run. A cold compile inside a 4-CPU limit
is slower than on the whole machine (21.9 s against 14.5 s, and 36.9 s
against 19.3 s, for the two packages measured), so a cold build cache under
`--cpus 2` risks the cap: the build caches persist per shard, and a cold one
is warmed outside the capped job (open question 6).

## 8. The runtime: what rootless podman does

| event | what happens | how it is known |
|---|---|---|
| `kill -9` of the `podman run` client | the container runs on, held by `conmon`, until the inner `timeout` or `--timeout`; `--rm` then removes it | measured (the kill tests: client killed 8 s in; the container up at 9, 19, 29 and 40 s; gone at the inner timeout; with `--timeout 15`, gone by 15 s) |
| `podman kill` | the container is gone at once (92 ms) and `--rm` removes it | measured |
| the inner `timeout` expires | `go test` ends with 124; PID 1 exits; the kernel kills the PID namespace (a `postgres` in its own session with it); `--rm` removes the container | measured |
| a reboot | no process survives. podman notices on its first command (the `alive` file in its tmp directory, which is on tmpfs, is gone) and refreshes: every container that had run becomes `exited`, a created one becomes `configured`; an exited `--rm` container is removed with its volumes; a configured one is kept; nothing is restarted without a restart policy and a unit | read in the source: libpod `runtime.go` (`refresh`: `AutoRemove() && State == Exited` removed, `RemoveVolume: true`) and `container_internal.go` (`resetContainerState`) at podman v5.7.0 |
| a reboot with the tmp directory not on tmpfs | podman refuses every command: "current system boot ID differs from cached boot ID; an unhandled reboot has occurred" | read in the source: `runtime_linux.go` `checkBootID` at v5.7.0. The verb's preflight refuses a runtime directory off tmpfs |
| the user's session ends, lingering on | nothing: the user's systemd instance and the containers' scopes live on | podman's troubleshooting guide (item 17: "rootless containers exit once the user session exits"; the fix is `loginctl enable-linger`) |
| the user's session ends, lingering off | the user's systemd instance stops and every container of the user exits with it. Whether `--rm` removes them is not established: the monitor that runs the removal dies too, but the runtime directory under `/run/user/<uid>` is removed with the session, so the next podman command refreshes as after a reboot and removes the exited `--rm` ones. The model takes both | the same guide; the refresh path as above; the removal not measured |
| `conmon` killed alone | not measured. The container's processes live in their own scope and are expected to run on, with nothing left to enforce `--timeout` or run `--rm` | the model's `MonitorDies`; the reaper covers it |

The runtime's role for the runners installs rootless podman, turns lingering
on for the runner user and checks it, and checks that the runtime directory
is on tmpfs.

## 9. Open questions

1. **Where this design lives.** The living docs describe only what is built
   and the tools that ship, and a spec is normative (where code and spec
   disagree, one has a bug), so a spec of an unbuilt verb cannot sit in
   `docs/`. Options: `future/` (designs of what is not built yet; the docs do
   not name it), `docs/PROPOSAL-*.md` (the older convention), a comment on
   the issue. Recommendation: `future/`, moved to `docs/SPEC-FUNCTIONAL-RUN.md`
   in the change that builds the verb.
2. **The verb names.** Options: `nova-ci functional run` and `nova-ci
   functional reap` beside the existing selection (`run` and `reap` become
   words a package argument cannot be), or `nova-ci functional-run`, or a new
   tool. Recommendation: the nested verbs, with the selection verb unchanged
   (package arguments are paths such as `./cmd/...`).
3. **How a reaped run reaches a person.** Options: the reaper's exit 1 under
   a timer whose failure the fleet's alerting carries; a message on the
   coordination bus; a row in a table. Recommendation: exit 1 and the REAPED
   line in the reaper's log now, and a message to a person once a
   notification path is chosen for the fleet; the receipt file stays the
   record either way.
4. **Who runs the reaper.** Options: a systemd user timer installed by the
   runtime's role; a pre-step of every `run`; both. Recommendation: both. The
   timer is what the liveness properties rest on; the pre-step makes a
   stale container go before the next run on the same runner.
5. **The deadline, the grace and the timer's period.** Recommendation: the
   deadline 90 s for a CI shard (flag, no default), the grace 120 s (above the
   runtime's worst delay in killing and removing), the timer every 5 minutes;
   measured before they are fixed.
6. **The build cache.** Options: one volume per shard (no shared writer), one
   shared volume, none. Recommendation: one per shard, as the design says,
   and a warm-up run outside the capped job when the toolchain or `go.sum`
   changes.
7. **The runner's job-end sweep and the monitor.** Recommendation: start
   podman without `RUNNER_TRACKING_ID`, and measure on a bench whether
   `conmon` carries the variable otherwise; a verb test pins that the
   variable is removed.
8. **Peak memory and pids.** Options: read `memory.peak` and `pids.peak`
   inside the container at the end of a run that finished (absent on a kill),
   or sample the container's cgroup from outside while it runs.
   Recommendation: inside, `-` when absent; the leftover counts matter more
   than the peaks.
9. **Whole packages or the functional tests only.** Recommendation: the
   functional tests only, with the `-run` pattern the selection prints, as
   `make test-functional` does.
10. **Tests that need `.git`.** Some tests read `.git` and `origin/dev`.
    Options: mount the job's checkout (with `.git`) read-only; declare those
    tests as not container tests. Recommendation: mount the checkout
    read-only; `git` inside works with no network and an empty `HOME`.
11. **A host-wide sweep for dependencies outside any container.** Until the
    guard lands, a fixture run outside a container can still leave a server
    behind. Options: the reaper also lists the user's `redis-server` and
    `postgres` processes that are in no container's cgroup and reports them;
    nothing. Recommendation: report, never kill; a process the reaper did not
    start is a person's to stop.
12. **Docker.** The runtime is podman, rootless and daemonless. Supporting
    docker too would need its own facts for every row of section 8.
    Recommendation: podman only, until a runner that has only docker exists.
