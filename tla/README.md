# tla: the models of nova-tools' state machines, and their runners

The TLA+ modules here are the specifications of the state machines this repo implements (rowan-new SPEC-COORDINATOR section 8: the backend is the state machine, the verbs are its actions; Glenn 2026-09-27: TLA+ for every state machine, every project). The findings each model produced, verified against the code by hand, are in rowan-new `specs/tla/FINDINGS.md`; the model documents (`TABLE-MODEL.md`, `MEMBER-TABLE-MODEL.md`) are copied here beside the modules they describe.

| Module | Instance | What it is |
|---|---|---|
| `CardMachine.tla` | `MCCardMachine` | the card's life over cells (the copy model of 02_card_move.lua), the corrected design after its three findings |
| `LandWatch.tla` | `MCLandWatch` | the land watch's watcher (land_watch.lua): stamps, slow and wall, one note per stay (findings L1 to L3) |
| `TableMachine.tla` | `MCTable*` | nova-table as table.lua is today at f7745885, with its actual gaps (Stella; the strict gate fails on purpose) |
| `MemberTable.tla`, `EpochMemberTable.tla` | `MCMember*`, `MCEpochMember*` | the corrected member placement and epoch protocol (Stella): one place per table inside the epoch, lossless shape, no owned alias, stale writers refused |
| `BatchMemberTable.tla` | `MCBatchMemberTable`, `MCBatchSecondEpoch`, `MCBatchBroken*` | atomic member batches over the existing placement/epoch model, shared revisions, guards, operation replay and complete receipts |
| `TableEdit.tla`, `TableOrder.tla` | `MCTableEdit*`, `MCTableOrder*` | nova-table's edit verbs and the order of its rows and columns, with reversed witnesses |
| `TableSession.tla` | `MCTableSession*` | `nova-table shell`: lines, one connection, the store coming and going, a stop signal, the exit code (the design #4458 is held to) |
| `RedisFn.tla` | `MCRedisFn*` | the function libraries of one Redis under several loaders (internal/redisfn: Check, Load, Ensure, LoadMissing): one holder to a function name, a refusal that writes nothing, no moment without the library, a LoadMissing that never replaces |
| `FirstConn.tla` | `MCFirstConn*` | `internal/redisconn`'s first connection: the probe Open sends, taken and answered in the store's place only after HELLO was accepted, with seven reversed witnesses |
| `TableFirstContact.tla` | `MCTableFirstContact*` | nova-table's first contact with a store (cmd/nova-table/library.go): a verb that meets "Function not found" loads the library with LoadMissing at most once per process and is sent again once, only when its first send ran nothing, with three reversed witnesses |
| `FuseBox.tla` | `MCFuseBox*` | nova-fuse's box: the gate answers only from a box it read and from every box named, only a lift, init or your person's hand makes a surface clear, init never replaces a box, a lockdown always blows; five reversed witnesses |

Runners. `tools/tlacheck` (Go, over `internal/tlc`, `internal/tablemodel` and `internal/batchmodel`) runs the checks. Run it on a bench that has java and, for the replays, redis-server: neither belongs on a working machine. Every run is bounded by `--timeout` (a timeout is a failure, never a green), downloads nothing, and runs TLC in a private copy of the models under `--dir`, so the checkout never gains the error-trace files TLC writes beside a spec. The jar is `--jar`, or the environment variable `TLC_JAR`; java and redis-server are found on PATH, or named with `--java` and `--redis-server`, and the path found is echoed. `tlacheck help` and `tlacheck <verb> -h` say the rest.

| Verb | What it runs |
|---|---|
| `run` | the declared cases of `CASES.tsv` (one group, or a shard), each held to the result the plan declares, under a 110 s budget, writing `RUNS.tsv` (`make tlc`) |
| `groups` | the required groups of the plan, as JSON (`make tlc-groups`); with `--stale`, the groups that hold a case whose record is missing or no longer current |
| `merge` | the `RUNS.tsv` of the group runs, joined in plan order into the committed `tla/RUNS.tsv`; with `--keep`, the current records of the cases no run measured again stay |
| `inputs` | the files a case's TLC run reads and the hash of each, with the case's fingerprint |
| `table` | the table model: contracts, the five findings, the cross-table scope control (`--mode`), 120 s |
| `member` | the member and epoch protocol and its four mutation controls (`--suite`), 120 s |
| `replay` | the execution replay of a `table.lua` against `EpochMemberTable`, 120 s |
| `witnesses` | the table model's findings replayed against a pinned `table.lua` in a disposable Redis |
| `batch-replay` | real batch receipts and independent Redis snapshots checked against `BatchMemberTable`, including a corrupted observation control, under one 110 s budget |

```sh
go run ./tools/tlacheck groups --root .
go run ./tools/tlacheck inputs --root . --case MCEpochMemberFixedPoint
go run ./tools/tlacheck run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-out --group tablefirstcontact
go run ./tools/tlacheck table --root . --jar /path/to/tla2tools.jar --dir /tmp/table-results --mode all
go run ./tools/tlacheck member --root . --jar /path/to/tla2tools.jar --dir /tmp/member-results
go run ./tools/tlacheck replay --root . --jar /path/to/tla2tools.jar --dir /tmp/member-replay --source internal/nsprint/fn/lua/table.lua
go run ./tools/tlacheck batch-replay --root . --jar /path/to/tla2tools.jar --dir /tmp/batch-replay --source internal/nsprint/fn/lua/table.lua
git show f77458853af46fdbbafd6881a4b46006431f266f:internal/nsprint/fn/lua/table.lua > /tmp/table-pinned.lua
go run ./tools/tlacheck witnesses /tmp/table-pinned.lua
timeout 120 java -cp /path/to/tla2tools.jar tlc2.TLC -workers 8 -deadlock tla/MCCardMachine.tla
timeout 120 java -cp /path/to/tla2tools.jar tlc2.TLC -workers 2 -deadlock tla/MCLandWatch.tla
timeout 120 java -cp /path/to/tla2tools.jar tlc2.TLC -workers 2 -deadlock -config tla/MCFuseBox.cfg tla/MCFuseBox.tla
```

`-deadlock` on the java commands turns TLC's deadlock check off: these models end in a terminal stutter by design, and the safety and liveness properties are what they check; the bare java commands carry no cap of their own, so they are wrapped in `timeout`. The runners live here and not in the self repo because the self repo is text only.

`RUNS.tsv` holds one record per declared case. Its `input_sha256` is the fingerprint of the inputs that case was measured on, and its `input_files` is how many inputs that is. The record also names the jar (`jar_sha256`), the java version (`java_version`), the platform (`host`), the logical CPUs of the machine that ran TLC (`cpus`) and the TLC workers the case ran with (`workers`); none of them is an input. `host` holds a platform label the tool computes, `<goos>-<goarch>` of the machine that ran TLC (`linux-amd64`), from the closed list `Platforms` in `internal/tlc/records.go` (TLC runs on Linux only); it never holds a machine's name, and no flag or environment variable sets it. `cpus` and `java_version` say what capacity and runtime measured the record (`java_version` is the quoted version of the first line of `java -version` that starts with `openjdk version "` or `java version "`, so a `Picked up JAVA_TOOL_OPTIONS` line before it changes nothing); `merge` refuses records of more than one `jar_sha256` and accepts records that differ in `java_version`, `host` or `cpus`, which are observations of the machine and not the identity of the checker; the class test refuses a `host` cell that is not in the list. A case's inputs are exactly what its TLC run reads:

- its configuration (`MCFoo.cfg`);
- the module `CASES.tsv` names for it, and every module that one `EXTENDS` or `INSTANCE`s, transitively (`EXTENDS A, B`, `INSTANCE M`, `LOCAL INSTANCE M` and `F(x) == INSTANCE M WITH ...` are read from the module text at every depth of nested modules, not from comments or strings). A name with no file under `tla/` must be one of the ten modules the TLC jar bundles (`Bags`, `FiniteSets`, `Integers`, `Naturals`, `Randomization`, `RealTime`, `Reals`, `Sequences`, `TLC`, `Toolbox`), which read nothing from the tree. That list is `standardModules` in `internal/tlc/inputs.go`; it is bookkeeping, so extending it stales nothing. Any other name refuses the case, and the refusal says the two ways out: add the module file under `tla/`, or add the name to that list when the jar bundles it;
- the case's own row of `CASES.tsv`, under the file's header, and no other row;
- the runner's result files, as the binary was built: `outcome.go`, `plan.go`, `run.go` and `suite.go` of `internal/tlc`, which decide how a result is produced and read (the command line, flags, workers and timeouts of a TLC run, the reading of its output into pass or fail, and the reading of a row of `CASES.tsv` into the case a run is judged by: its expected outcome, property and deadlock policy). The package's other non-test files (`cases.go`, `doc.go`, `fingerprint.go`, `inputs.go`, `jar.go`, `records.go`) are bookkeeping and are in no fingerprint; `ResultFiles` and `BookkeepingFiles` in `fingerprint.go` name each file in exactly one list, and a test refuses a file in neither. One bookkeeping file, `inputs.go`, computes the list of files a case reads, so the binary carries it as well and every verb that takes a fingerprint refuses a binary built from another `inputs.go` than the checkout's (`InputListFiles`).

The fingerprint is the SHA-256 over those inputs in path order, each as its path, a NUL, the hex SHA-256 of its bytes and a newline. It holds no timestamp, no host and no absolute path. The jar is not an input: the record names it in `jar_sha256`.

Editing one model therefore stales the records of the cases that read it and no other: a change to a module stales every case whose module extends or instantiates it, a change to a configuration or to a case's own row stales that case, and a change to a result file of the runner stales every case. `tlacheck inputs --case <config>` prints each input with its hash, then the fingerprint and the count (`inputs`, `groups --stale`, `merge` and `run` refuse a binary whose embedded result files or `inputs.go` differ from the ones under `--root`, and say to build `tlacheck` from that tree); a record is current when its two columns equal them and its `module`, `expected` and `property` cells equal the case's row (the row is hashed and the cells are not, so a merge refuses a record whose cells were edited, naming the cell and both values, and `groups --stale` counts its group stale). `TestTLCRecordsCoverCurrentModels` refuses a stale record by naming its case and the files it reads, a configuration with no record, a record with no configuration, and a case whose module or extended modules cannot be found.

### Refreshing the records after a model edit

The author of a model change refreshes only the records the change staled. Start from the branch with the change rebased on the base branch (`git rebase origin/dev`), including a branch that still holds records in an older column layout: the base branch's `tla/RUNS.tsv` replaces the branch's, and the runs below measure again whatever the edit staled. The commands run as they stand, in this order, from the checkout root.

1. Take the base branch's records, build `tlacheck` from this tree, and ask which groups are stale (the tool refuses a binary built from other runner files than this tree's, and records in another layout than its own, and says so):

```sh
git fetch origin
git checkout origin/dev -- tla/RUNS.tsv
go build -o /tmp/tlacheck ./tools/tlacheck
/tmp/tlacheck groups --root . --stale
```

`[]` means the edit staled nothing: `tla/RUNS.tsv` is already current, and steps 2 and 3 have nothing to do. Otherwise the output lists the groups to run.

2. On a Linux bench with java (never on a working machine), with the same tree, run each stale group into a clean directory of its own (a directory that holds an earlier run's records would be merged with them) and join the runs onto the base branch's records. Use the jar the kept records name (`cut -f5 tla/RUNS.tsv | sed 1d | sort -u`): one jar measures the whole file, and `merge` refuses a set of records with more than one. The tool downloads nothing and the records name the jar only by its SHA-256, so the jar comes from you: a `tla2tools.jar` of the TLA+ project (its releases are at github.com/tlaplus/tlaplus), checked by `sha256sum /path/to/tla2tools.jar`, which has to print the hash the records name. When no jar you can obtain has that hash, run every group with the one jar you have and merge without `--keep`, as below:

```sh
runs=$(mktemp -d)
stale=$(/tmp/tlacheck groups --root . --stale | tr -d '[]"' | tr ',' ' ')
for g in $stale; do
  /tmp/tlacheck run --root . --jar /path/to/tla2tools.jar --dir "$runs/$g" --group "$g"
done
test -z "$stale" || /tmp/tlacheck merge --root . --keep tla/RUNS.tsv --out tla/RUNS.tsv "$runs"/*/RUNS.tsv
```

A group that ends over its budget exits 1 and still writes its records (the cases with declared debt are recorded as the failed measurements they are); a case that is not a declared debt and fails is a defect in the model or the plan, and the class test refuses its record.

3. Commit `tla/RUNS.tsv` with the change. `merge --keep` keeps a record of the base branch's file only when its case was not measured again and the record is current; a stale record it cannot carry is named as stale with its group, and a kept record of a case the plan no longer declares is dropped and named.

To measure every case instead (a new jar, or a runner change that stales everything), run every group and merge without `--keep`:

```sh
runs=$(mktemp -d)
for g in $(cut -f6 tla/CASES.tsv | sed 1d | sort -u); do
  /tmp/tlacheck run --root . --jar /path/to/tla2tools.jar --dir "$runs/$g" --group "$g"
done
/tmp/tlacheck merge --root . --out tla/RUNS.tsv "$runs"/*/RUNS.tsv
```

## Table execution and receipt replay

`tlacheck replay` captures 32 controlled source-API transitions in a
disposable Redis, then replays their committed receipt arguments into a second
fresh store. It checks event counts, revision continuity, metadata, member
changes and affected cells independently against the observed store states.
Initial state, externally advanced epochs, writer epoch reads, refused attempts
and committed events are retained in `trace.json`, along with source/model hashes.

A generated linear TLC harness invokes the corresponding `EpochMemberTable`
actions and requires its state to match every observed record/set/shape/epoch
state. Its positive run retains the model's safety properties. Negative controls
must detect a corrupted observed record link, a receipt member change and a
revision gap. The output includes runnable generated modules/configuration files
and TLC logs. Both Redis servers disable TCP; all transient stores are discarded.

This checks the stated finite traces and abstraction mapping. It is not an
unbounded implementation-refinement proof or a live/day-long trace collector.
The model predeclares records, columns and future empty epoch namespaces;
creation/ID reuse, template removal, arbitrary definitions, raw corruption and
Redis error preflight remain concrete-code functional-test obligations. No-ops
map to unchanged abstract user state; the model does not encode the receipt
ledger, which the replay runner checks separately. The original pinned baseline
runner and its deliberately failing desired-contract gate remain unchanged.

## Shell execution traces

`tools/sessiontrace` captures 16 shell sessions (eight fixed random seeds,
both keep-going settings) in an owned Redis. Each executed line records its
output, refusal status and newly committed receipts. The relay loses selected
write replies after the store answers; the receipt ledger must still contain
exactly one effect. The harness also covers successful reads/writes, logical
refusals, usage errors, overlong lines, quit and EOF without timing assertions.

```sh
go run ./tools/sessiontrace --jar /path/to/tla2tools.jar --out /tmp/session-trace
```

The runner has one 120-second budget for capture, TLC and negative controls.
Go dependencies must already be cached; capture disables module downloads and
automatic toolchain selection.
It retains the trace, source/model/jar hashes, generated modules and TLC logs.
The generated module invokes `TableSession` actions and checks observed line
statuses, final exit, unread input and termination reason. Its input domain is
the captured sequences and their suffixes. Corrupted final exit and line status
must fail TLC; a duplicated durable effect must fail receipt validation.

Receipts witness effects separately because `TableSession` does not model the
ledger or table contents. This bounded replay covers the stated subset; signal
delivery, store outages and watch liveness retain their existing functional
and model controls. It is not a proof over arbitrary shell executions. The
per-tick runtime check remains `watch --check`.

## The edit verbs (TableEdit) and order (TableOrder)

Two bounded safety models of nova-table's edit surface, each an abstraction
with reversed witnesses, neither a refinement proof of table.lua. What each
leaves out is listed in its header. The functional tests hold the rest.

`TableEdit.tla`: `set` (footer, rename, columns), `row add` and `row
hide/show` over many rows, `row set` (text), column hide/show, the cell
verbs, and one outside event (a stored formula column whose fold the library
no longer reads). `Staged = FALSE` is table.lua at 109939a85.

`TableOrder.tla`: `row add`, `row del`, `row move`, `row order`, `row sort`
(once, `--keep`, `--manual`), `bind`, one `set` call that sorts and places,
`col add`, `col del`, `col move`. Every call records what it asked for, and
the invariants say the result is the one requested, not only a permutation.
`Broken` names the misimplementation a witness config turns on.

Run on a bench, never the Studio. Every config runs at once, each in its own
temp directory (TLC unpacks its standard modules into `java.io.tmpdir`, and
two runs sharing one collide), each under a 60 s cap; a timeout is a failure:

    for cfg in MCTableEdit*.cfg MCTableOrder*.cfg; do c=${cfg%.cfg}
      m=MCTableOrder.tla; case $c in MCTableEdit*) m=MCTableEdit.tla;; esac
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -deadlock -metadir /tmp/tlc-$c/meta -config $cfg $m > $c.log 2>&1 &
    done; wait

Measured on space, 2026-09-27, load 9, all thirteen at once: 38 s wall.

| config | result | time |
|---|---|---|
| `MCTableEdit` | no error, 468,243 distinct states, depth 4: TypeOK, RefusalWritesNothing, ShapeLosesNothing, PlacedInShape, TextInTextColumns, HideKeepsData, RenameKeepsData | 37 s |
| `MCTableEditBrokenRefusal` | RefusalWritesNothing violated in 2 states: `row add 1 2`, row 2's key of the wrong type, row 1 left written (109939a85 lines 189-192) | 1 s |
| `MCTableEditBrokenText` | ShapeLosesNoText violated in 4 states: a text value set, `set --columns` without the column deletes it (line 341) | 2 s |
| `MCTableEditBrokenLegacy` | ShapeLosesNoMember violated in 5 states: a member placed, a formula column's stored fold stops parsing, `set --columns` drops the member's column with no OCCUPIED check (line 311) | 8 s |
| `MCTableOrder` | no error, 241,073 distinct states, depth 4, 3 rows, 3 columns: TypeOK, RefusalWritesNothing, RowMoveIsExact, RowOrderIsExact, ColMoveIsExact, AddIsExact, BindIsExact, RowSortIsExact, StandingSortHolds, ReorderIsPermutation, HeldInShape, OnlyRowDelDrops, RowsAndColumnsApart | 19 s |
| `MCTableOrderBrokenBind` | StandingSortHolds violated: bind writes its input order under a standing sort (3ee97bea: bind never reached the standing-sort step; Stella's probe 1) | 2 s |
| `MCTableOrderBrokenCombined` | StandingSortHolds violated: one call sets `--keep` and moves a row (3ee97bea: the guard read the sort before the edit and exempted any call with row_sort; Stella's probe 2) | 2 s |
| `MCTableOrderBrokenOnce` | RowSortIsExact violated: a sort without `--keep` leaves the rows as they were | 2 s |
| `MCTableOrderBrokenSort` | StandingSortHolds violated: row add ignores the standing sort | 2 s |
| `MCTableOrderBrokenPrefix` | RowOrderIsExact violated: the named rows put last | 2 s |
| `MCTableOrderBrokenItem` | RowMoveIsExact violated: `--first` moves another row | 1 s |
| `MCTableOrderBrokenDel` | OnlyRowDelDrops violated: `col del` removes a column that holds a member | 2 s |
| `MCTableOrderBrokenBindLoss` | OnlyRowDelDrops violated: bind drops an omitted row that holds a member | 2 s |

The three witnesses of the edit model and the Bind and Combined witnesses of
the order model are defects that were in the code, each checked by hand
against the lines named. The other six are misimplementations the invariants
are shown to catch. The order model also found one defect by disagreeing with
the code: it refuses a bind that omits a row holding a text value, and the
kernel at 6b3346174 deleted the text (Stella's read, stella-9a49e4eda437); the
kernel was changed to refuse, the model was not. Bounds of the instance: a
combined sort-and-move places at `--first` or `--last`, a combined
sort-and-order names one row. A depth-5 run of the edit model with one member (1,652,467
distinct states, no error) took 92 s on the same bench and is not in the set.
## The file lock (FileLock)

`FileLock.tla`: a lock on a file across processes, the design of the shared
module `internal/filelock`: one holder at a time, and a way to ask who holds
it. Written from the locks the tools already carry (`internal/bus`, `merge`,
`tokens`, `swarm`, `wake`, `update`) and against the first candidate,
nova-tools#4473 at d653eb53e. A bounded design model with reversed witnesses,
not a refinement proof. What it leaves out is listed in its header.

The rule it stands on is `internal/merge/lock.go`'s: the kernel releases the
lock when its holder dies, so there is nothing to break and no age to compute.
The lock is the kernel's lock on the file and nothing else. What is written in
the file is a note and never what decides. The file is never removed.

What it holds the module to:

- the lock is never handed to two callers (`MutualExclusion`), and who holds it
  holds the file at the path by the kernel's lock (`HolderHoldsThePath`);
- the path names one file for ever (`OneFileForEver`);
- the file names its holder (`HolderIsNamed`);
- "held" is said only of a holder and "free" never of one, by a probe or by a
  refusal (`HeldIsTrue`): a probe asks for a shared lock, and a refused taker
  asks for one too before it says "held", so an asker is never taken for a
  holder; a taker that only askers kept out answers "busy";
- who takes the lock is told, truly, whether the last holder released it
  (`UncleanIsTold`): release clears the note, so a note found is a holder that
  never released, whatever became of its pid;
- the kernel's lock is only ever with a live process that knows it has it, so
  a death leaves nothing for anybody to clear (`NothingToClear`).

Run as the other table models are, every config at once, each in its own temp
directory under a 60 s cap:

    for cfg in MCFileLock*.cfg; do c=${cfg%.cfg}
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -deadlock -metadir /tmp/tlc-$c/meta -config $cfg MCFileLock.tla > $c.log 2>&1 &
    done; wait

Measured on space, 2026-09-27, load 10, all nine at once: 5 s wall.

| config | result | time |
|---|---|---|
| `MCFileLock` | no error, 37,611 distinct states, three processes, each pid reused twice: TypeOK, MutualExclusion, HolderHoldsThePath, HolderIsNamed, OneFileForEver, HeldIsTrue, UncleanIsTold, NothingToClear | 3 s |
| `MCFileLockFour` | no error, 160,832 distinct states, four processes, each pid reused once: the same eight | 5 s |
| `MCFileLockBrokenStale` | MutualExclusion violated in 21 states: a holder dies; two processes find the lock stale and both clear it; the first removes the file and takes a new one at the path; the second opens that new file, finds no name in it, and removes it; both then create a file and hold (d653eb53e filelock_unix.go: 167, 197, 206, 217, 221, then 57, 64, 70) | 2 s |
| `MCFileLockBrokenExProbe` | HeldIsTrue violated in 6 states: a probe has the exclusive lock for an instant, and a taker is refused as "held" with nobody holding (d653eb53e filelock_unix.go: 171, then 64, 96, 104; `internal/wake/lockprobe_unix.go` 49 on dev) | 2 s |
| `MCFileLockBrokenSentinel` | NothingToClear violated in 4 states: the holder dies and the lock stays taken (`lock_other.go` of `internal/bus`, `tokens`, `swarm`; `internal/wake/lockprobe_other.go` says so of itself) | 2 s |
| `MCFileLockCandidate` | the candidate as it is ("stale" and "exprobe" together): HeldIsTrue violated in 6 states, MutualExclusion as above | 2 s |
| `MCFileLockBrokenPidLive` | HeldIsTrue violated: a probe that reads the note and asks whether the pid answers says "free" of a taker that has not yet written its name | 2 s |
| `MCFileLockBrokenUnlink` | MutualExclusion violated in 11 states: release removes the file under a taker that has it open | 2 s |
| `MCFileLockBrokenKeepStamp` | UncleanIsTold violated: release leaves the name, and the next taker is told the last holder never released | 2 s |

Stale, ExProbe and Sentinel are in code that exists, each checked by hand
against the lines named. The first Stale counterexample TLC gave did not
survive that check (it counted as a holder a process the code makes give up),
so the invariant was tightened to locks handed to a caller, and the trace above
is the one that holds. PidLive, Unlink and KeepStamp are misimplementations the
invariants are shown to catch.

## The shell (TableSession)

`TableSession.tla`: `nova-table shell` as a state machine, the design that
nova-tools#4458 is held to. The session owns its input, where its reader is
(at a line, in a verb, in a watch, ended), one connection (none, live, or dead
and not yet used again), the dial error its pool keeps, and its exit code. The
outside is the store (up; refusing; gone from its socket path) and two
signals. A bounded design model with reversed witnesses, not a refinement
proof of session.go. What it leaves out is listed in its header.

The signals (Stella, stella-ba91222b58ce): SIGTERM is a stop wherever it
arrives. SIGINT inside a watch is how a watch is left, and the reader goes on
to the next line; at the prompt or inside a verb it is a stop. A session ended
by a stop reports the signal (143, 130), not the codes of its lines.

What it holds the shell to:

- after a stop no new line starts (an in-flight write may complete)
  (`NothingStartsAfterStop`); SIGTERM ends the session (`TermEnds`); only a
  stop ends the session as one (`StopOnlyWhenStopped`), and SIGINT leaves a
  watch (`IntLeavesWatch`);
- a line fails for the connection only when a dial made for that line failed
  (`NoFalseAlarm`);
- a store that could not be reached is code 2, whatever the dial said
  (`ConnectionFailureIsTwo`);
- a write is sent once: when its reply is lost the line ends with code 2 and
  the write is not sent again, because it may have been done (`AtMostOnce`);
- without `--keep-going` nothing is read after the first failed line
  (`StopsAtFirstFailure`, `EndOfInputMeansNoFailure`); with it every line is
  read unless the session was told to end (`KeepGoingReadsEveryLine`);
- the exit code is the highest code of any line, unless a stop ended the
  session (`ExitIsHighest`);
- a verb ends: the session is never stuck inside a line (`VerbEnds`).

The reader waits for input as long as the outside likes, so nothing here says
a session must end: a shell left open at its prompt is not stuck, and a watch
draws until it is told to stop. Fairness is on what the session owes (a verb
in hand, a signal received), never on the arrival of a line.

Run as the edit and order models are, every config at once, each in its own
temp directory under a 60 s cap:

    for cfg in MCTableSession*.cfg; do c=${cfg%.cfg}
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -deadlock -metadir /tmp/tlc-$c/meta -config $cfg MCTableSession.tla > $c.log 2>&1 &
    done; wait

Rowan reported the exact-head run on space on 2026-09-27. At `a03a52655`,
all nine configurations passed their expected outcomes in 8 s: the positive
model retained 87,925 states and the stronger cached-error witness failed in
5 states. The strengthened `NoFalseAlarm` rejects a cached error even while
the store remains down, matching the fresh-dial requirement.

At model commit `b0107f910`, the added lost-reply transition and `AtMostOnce`
invariant bring the positive run to 92,331 distinct states, thirteen invariants
and three liveness properties. All ten configurations ran in 9 s; all eight
negative witnesses were caught. These are Rowan's bench measurements, not a
new run on the reader's machine. Inputs have up to four lines over six kinds
of line, with and without `--keep-going`, from each state of the store.

| config | result |
|---|---|
| `MCTableSession` | no error, 92,331 distinct states: TypeOK, NothingStartsAfterStop, StopOnlyWhenStopped, NoFalseAlarm, ConnectionFailureIsTwo, AtMostOnce, StopsAtFirstFailure, ExitIsHighest, KeepGoingReadsEveryLine, EndOfInputMeansNoFailure, EndsForAReason, LineInHand, LiveMeansUp; TermEnds, IntLeavesWatch and VerbEnds under weak fairness of what the session owes |
| `MCTableSessionBrokenTerm` | NothingStartsAfterStop violated in 5 states: `watch`, SIGTERM, the watch returns 0, the next line `ok` is run (ed959e1a3: watch.go:75, watchLoop, session.go:126) |
| `MCTableSessionBrokenTermLive` | TermEnds violated: `watch`, SIGTERM, the watch returns 0 and the session is still there |
| `MCTableSessionBrokenStale` | NoFalseAlarm violated in 5 states: a line answers the cached error without making a fresh dial, whether or not the store recovered (ed959e1a3: session.go:88, a pool of one; go-redis v9.22.0 pool.go:692) |
| `MCTableSessionBrokenClass` | ConnectionFailureIsTwo violated in 3 states: the store gone from its socket path, the line ends with code 1 (ed959e1a3: main.go:303) |
| `MCTableSessionBrokenLong` | KeepGoingReadsEveryLine violated in 2 states: a line too long ends a `--keep-going` session with a line unread (ed959e1a3: session.go:135) |
| `MCTableSessionBrokenReplay` | AtMostOnce violated in 3 states: a write's reply is lost and the write is sent again (ed959e1a3: session.go:88 opens with go-redis's command retries; v9.22.0 error.go shouldRetry answers true for io.EOF; found by Stella: code 0 and a second receipt) |
| `MCTableSessionBrokenOn` | StopsAtFirstFailure violated: a line is read after a failed one without `--keep-going` |
| `MCTableSessionBrokenLast` | ExitIsHighest violated: `usage` then `ok` exits 0 |
| `MCTableSessionBrokenInt` | StopOnlyWhenStopped violated: SIGINT inside a watch ends the session |

Term, Stale, Class, Long and Replay are defects of the shell at ed959e1a3,
reproduced on a store by the second reader or Stella and checked against
the lines named. On, Last and Int are misimplementations the
invariants are shown to catch; the code at ed959e1a3 has none of them. What a
stop does to a verb in flight is left open: the verb may finish, or the
process may end inside it. The model says only that no line starts afterwards.

## The function library loader (RedisFn)

`RedisFn.tla` is the store of internal/redisfn: under each library name one
build or none, a function name held by one library, and loaders that each
carry one build. A loader outside `Missers` runs Ensure, the deployer's load
(a read, then a load when the store does not hold its build, then a second
read to name the holder when the store refuses the load for a function
another library holds); a deployer runs it on every pass. A loader in
`Missers` runs LoadMissing once: a read, and only when the store holds no
build of its library, FUNCTION LOAD without REPLACE, which the store refuses
when a build is there by then. `Atomic = TRUE` is the code, FUNCTION LOAD
REPLACE; `Atomic = FALSE` is FUNCTION DELETE followed by FUNCTION LOAD.
`MissReplaces = TRUE` is a LoadMissing that sends REPLACE, the load of
nova-tools #3620. What it leaves out is listed in its header.

Run on space, every config at once, each in its own temp directory under a
60 s cap (no `-deadlock`: the terminal stutter is an action of the spec):

    for c in MCRedisFn MCRedisFnDeleteThenLoad MCRedisFnHolderGone MCRedisFnTwoDeployers MCRedisFnOneDeployer MCRedisFnLoadMissing MCRedisFnMissReplaces; do
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -metadir /tmp/tlc-$c/meta -config $c.cfg MCRedisFn.tla > $c.log 2>&1 &
    done; wait

Rowan ran it on space at 2026-09-28 00:22 UTC (tla2tools v1.7.4, TLC 2.19),
the modules and configs matching these files by sha256, logs in
`space:~/tla/redisfn-2/`. All seven ran in under a second. The distinct
states of a run that stops at a violation are what the two workers had found
by then, and vary from run to run; the length of the counterexample does not.
The first five configs, before `Missers` was added, gave the same outcomes at
2026-09-27 23:22 UTC (`space:~/tla/redisfn/`).

| config | result |
|---|---|
| `MCRedisFn` | no error, 54 distinct states. The migration: `old` on the store registers f and g, loader a carries the `old` that registers g alone, loader b carries the `new` that registers f, both deploy on every pass. TypeOK, OneHolder, NoGap, RefusalWritesNothing, HolderHeld, Settles, MCMigrated |
| `MCRedisFnDeleteThenLoad` | NoGap violated, a counterexample of 3 states (8 distinct found): a reads, a deletes `old`, and the store holds no `old` until a's second command. The reversed witness for Load being one FUNCTION LOAD REPLACE |
| `MCRedisFnHolderGone` | HolderFound violated, a counterexample of 6 states (44 distinct found): b is refused for f, a loads the `old` that lets f go, b looks for the holder and there is none. The reversed witness for CollisionError's line for a function with no holder (store.go, `CollisionError.Error`) |
| `MCRedisFnTwoDeployers` | Settles violated (48 distinct states), a counterexample of 10 states that goes back to its state 3 for ever: two deployers carry two builds of one library and each replaces the other's on every pass, `store.old` going 1, 2, 1 for ever. Not a witness of a misimplementation: the hazard of two deployers, which is why Ensure is for the one place that deploys and every other caller runs LoadMissing |
| `MCRedisFnOneDeployer` | no error, 14 distinct states: the same two builds, b running Ensure once and a deploying; the library comes to rest at a's build. TypeOK, OneHolder, NoGap, RefusalWritesNothing, HolderHeld, Settles |
| `MCRedisFnLoadMissing` | no error, 25 distinct states: the rivals on a store that starts empty, a deploying and b, the older binary, running LoadMissing once. TypeOK, OneHolder, NoGap, RefusalWritesNothing, HolderHeld, MissNeverReplaces, Settles, MCDeployed (the store comes to rest at the deployer's build) |
| `MCRedisFnMissReplaces` | MissNeverReplaces violated, a counterexample of 5 states (23 distinct found): b reads the name free, a deploys build 1, b's load with REPLACE puts build 2 over it. The reversed witness for LoadMissing's FUNCTION LOAD without REPLACE (#3620); the unit test `TestLoadMissingNeverReplacesALibraryTheStoreHolds` holds the same two cases against the code, with Ensure as its own reversed witness |

## nova-table's first contact (TableFirstContact)

`TableFirstContact.tla`: every nova-table verb is an FCALL into the
nova_sprint library, and a store that holds none answers "Function not
found". The model is the store's library (none, an older build that lacks the
verb's function, this build's) and, per process, whether a load reached an
outcome and where each verb is. The outside events are the deployer's load, an
older binary's LoadMissing, a load that fails and a reply lost after the store
ran the command. `Broken` turns on a misimplementation: `resend-lost` sends a
verb whose reply was lost again, `replace` loads with REPLACE, `every-miss`
loads on every miss. What it leaves out is listed in its header; RedisFn.tla
holds LoadMissing's read and load under racing loaders.

Run on space, every config at once, each under a 60 s cap:

    for c in MCTableFirstContactFresh MCTableFirstContact MCTableFirstContactBrokenResendLost MCTableFirstContactBrokenReplace MCTableFirstContactBrokenEveryMiss; do
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -metadir /tmp/tlc-$c/meta -config $c.cfg MCTableFirstContact.tla > $c.log 2>&1 &
    done; wait

Logs in `space:~/tla/firstcontact/`, the modules and configs matching these
files by sha256. Each ran in under a second. The distinct states of a run that
stops at a violation vary from run to run; the counterexample does not.

| config | result |
|---|---|
| `MCTableFirstContactFresh` | no error, 37 distinct states: a fresh store, no outside event, two processes of two verbs. TypeOK, AtMostOnce, SentTwiceOnlyAfterAMiss, NeverReplaced, LoadOnce, FreshStoreWorks (every verb ends ok) |
| `MCTableFirstContact` | no error, 771 distinct states: every outside event. TypeOK, AtMostOnce, SentTwiceOnlyAfterAMiss, NeverReplaced, LoadOnce |
| `MCTableFirstContactBrokenResendLost` | AtMostOnce violated, a counterexample of 5 states: a miss, the load, the verb run with its reply lost, sent again and run twice. The reversed witness for sending again only on "Function not found", which a verb that ran never answers |
| `MCTableFirstContactBrokenReplace` | NeverReplaced violated, a counterexample of 4 states: a miss, the deployer loads this build, the miss's load replaces it. The reversed witness for LoadMissing's FUNCTION LOAD without REPLACE |
| `MCTableFirstContactBrokenEveryMiss` | LoadOnce violated, a counterexample of 7 states: a miss on a store an older binary loads first, the load leaves it (UNCHANGED), the verb is refused; the next verb's miss loads again. The reversed witness for firstContact.ensure's once per process |

## The first connection (FirstConn)

`FirstConn.tla`: the connection `redisconn.Open` dials (internal/redisconn/open.go
at f6ec9e2b8, `firstConn`), as a state machine over the seven events of
`firstconn_test.go`: the store sends a reply that begins `%` (HELLO accepted)
or `-` (refused); the client reads with room to spare, or a few bytes at a
time; the client writes the probe, or another command; Open returns. go-redis
shakes hands inside the first command on a connection, so Open sends a probe
(PING); when, and only when, the first byte from the store was `%`, the probe
is taken and answered here (+PONG) and never written, so Open costs one
exchange and not two. A bounded design model with reversed witnesses, not a
refinement proof of open.go; its header lists what it leaves out.

The rules are the test's, stated on what went in and what came out and not
on the states the code keeps: a write is taken only when it is the probe, the
first byte read was `%`, nothing but the handshake was written before, no
write was taken before and Open has not returned, and then it is taken
(`TakenOnlyWhenDue`, `TakenWhenDue`); every other write reaches the store
whole and in order (`TheRestTravels`); the client reads the store's bytes in
order (`StoreBytesInOrder`), with the answer whole, once, first and alone after
the taken write, and never otherwise (`AnswerStandsInPlace`); inert is for
good (`InertStays`); a taken write is answered, so the client is never left
waiting for it (`AnswerDelivered`, under weak fairness of the client's reads).

Run as the others are, every config at once, each in its own temp directory
under a 60 s cap:

    for cfg in MCFirstConn*.cfg; do c=${cfg%.cfg}
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -deadlock -metadir /tmp/tlc-$c/meta -config $cfg MCFirstConn.tla > $c.log 2>&1 &
    done; wait

Rowan ran it on space on 2026-09-28 00:40 UTC (load 11 of 32 cores), all
eight configs at once, the positive one with 4 workers: 3 s wall for the set.
The instance: six counted events (sends, writes, Open's return; reads are
uncounted, each consumes what it reads), a reply of three bytes, an answer of
three bytes, a short read of two. The same positive model with eight events
ran to 303,578 distinct states in 10 s on 8 workers, no error; it is not in
the set because of the cap.

| config | result |
|---|---|
| `MCFirstConn` | no error, 30,832 distinct states, depth 14: TypeOK, TakenOnlyWhenDue, TakenWhenDue, TheRestTravels, StoreBytesInOrder, AnswerStandsInPlace, InertStays; AnswerDelivered under weak fairness of the reads |
| `MCFirstConnBrokenRefused` | TakenOnlyWhenDue violated in 4 states: `-` read, the connection armed, the probe taken (open.go:268 without the `%` test) |
| `MCFirstConnBrokenAny` | TakenOnlyWhenDue violated in 4 states: `%` read, a write that is not the probe taken (:282 without bytes.Equal) |
| `MCFirstConnBrokenMisaligned` | TakenOnlyWhenDue violated in 5 states: `%` read, another write travels and leaves the connection armed, the probe after it is taken (:285 missing) |
| `MCFirstConnBrokenTwice` | TakenOnlyWhenDue violated in 6 states: the answer read whole, the connection armed again, a second probe taken (:261 storing armed) |
| `MCFirstConnBrokenShort` | AnswerStandsInPlace violated in 6 states: two of the answer's three bytes read, the connection inert, the store's next bytes read where the third should be (:260 without the count) |
| `MCFirstConnBrokenLate` | TakenOnlyWhenDue violated in 5 states: `%` read, Open returns, the probe written after it is taken (:293 missing) |
| `MCFirstConnBrokenHang` | AnswerDelivered violated: the probe taken, no read is possible, the client waits for an answer that never comes (:257 reading the store) |

None of the seven was a defect of the code at f6ec9e2b8: `firstconn_test.go`
holds the same rules over every order of the seven events up to six and over
long orders. Each is a misimplementation the model is shown to catch. The
Misaligned trace was read against the code by hand: state 3, `%` read,
`Read` at :267-271 swaps watching for armed; state 4, a write that is not
the probe, `Write` at :281-285 finds armed, `bytes.Equal` false, and swaps
armed for inert, which the witness omits; state 5, the probe, the code at
:287 passes it to the store because the connection is inert, and the test's
named order `writeOther, sendAccepted, readAll, writeOther, writeProbe`
(firstconn_test.go:223) says the same: not taken. The Hang trace: states 7
and 8, `%` read and the probe taken, then no read is enabled because the
witness reads the store, which sent nothing; the code at :257-263 reads the
answer from `probeAnswer` and never touches the store while answering.


## Atomic member batches

`BatchMemberTable` extends `EpochMemberTable`; existing per-verb configurations
remain separate. The required `batchmembertable` group contains twelve positive
configurations and twenty-seven deliberately faulty variants. All positive instances
retain three members, two rows, two columns and two epochs. The primary instance
explores request lengths one through three, a guard-only request, and interacting
cross-row changes for up to three actions. The second instance retains an old-epoch
member while advancing, refreshing, binding and accepting a new-epoch create in
four actions. Separate immutable member-epoch assignments make both paths explicit.
Two further three-action traces advance a member revision with a synthetic field writer,
then accept a cross-row score change with its member revision guard omitted. One
trace removes its placement while retaining the member record; the other accepts
a same-cell, same-score no-op without a member revision increment. Both still
record one receipt and table revision for each accepted batch.
An additional three-action trace removes a placed member and then changes an
application field on the retained unplaced record. Another three-action trace
shows that removing an already-unplaced member refuses. A two-action trace proves
that explicit `remove:false` and set/unset of the same field both refuse.
A four-action trace removes a member and tries moving the retained unplaced record
back into a cell, first without a score and then with one; both requests refuse.
The main instance uses a revision bound of three. A full nondeterministic
instance uses a revision limit of two with three actions. Two directed traces
reach that limit before attempting another batch: one uses two effective moves,
and the other uses two guard-only no-ops, leaving every member revision at zero.
Both require refusal, and refusal/replay read-only checks preserve the store.
A reversed overflow case forces the third move and violates `RevisionWithinBounds`;
separate reversed cases check `UnplacedMoveRequiresPlacement` and
`OnePlacePerDimension`. A separate input fixture seeds the existing member at
its revision limit while leaving the table revision zero, then attempts a batch.
Its positive case requires refusal; its reversed case violates
`RevisionWithinBounds`. This tests the member guard and makes no
claim that the seeded counter relation is reachable from the all-zero fixture.
The runtime stores these counters separately. Score lookup returns the no-score
sentinel for an unplaced member, so the forced omitted-score move reaches
the named property failure instead of a function-domain error.
Absent member records carry no application fields, and create starts from that
empty field image before applying its explicit set; every positive configuration
checks the absent-record invariant and the create receipt's empty before image.

The model represents field equality, absence and membership guards, including the
difference between an absent field and an empty string. An existing member may
omit its revision guard while retaining its table revision, source placement and
field checks. An explicit move score replaces the old score; an omitted one
preserves it. A removal clears placement but keeps the member record. Effective
placement, score or application-field changes increment the member revision once;
a same-cell move with unchanged score and fields is an accepted no-op. Receipt
deltas carry before/after placement, score, revision and application fields.
The `guardCount` counts entries with no requested mutation, while `changedCount`
counts effective changes; their sum can be below `selectedCount` when a requested
move proves to be a no-op.
Ordinary add/remove/move actions share member and table revision increments.
Request bytes are abstract identities; byte equality, rather than digest equality,
controls replay. Accepted batches produce one complete receipt and one table
revision; refusals and replay leave the modeled store unchanged.

**Field-writer scope.** `OrdinaryFieldWrite` and `OrdinarySetEmpty` are
synthetic cooperating writers that advance both revisions. `table.lua` has no such
field-write verb; a direct application `HSET` does not advance these revisions.
These actions model protocol interference hypotheses, not `HSET` implementations.
The main exploration's field branches and empty-field guard scenario,
`MCBatchRevisionBound`, `ExtendedNoop`, `ExtendedRemove`, `BrokenStaleMember`,
`BrokenOrdinaryRevision`, `BrokenMissingRevision`, `BrokenScorePreserved`,
`BrokenRemoveLeavesCell`, `BrokenRemoveDeletesRecord` and
`BrokenSameCellRevision` depend on this assumption.
They do not establish that a revision guard detects direct application field edits.
Unversioned `HSET` interference and its guard consequences remain a follow-up.
The overflow and unplaced-move traces use only batch actions.

The negative configurations cover late guard/type/permission failure after an
initial write, post-write guard evaluation, duplicate placement, stale epoch/table/
member expectations, stale member expectations after an ordinary move, omitted
ordinary field/move revision increments, lost-reply double effects, digest collision,
incomplete receipt effects and stale replay reported as a new acceptance. The
reversed cases reject a valid omitted-revision request after an ordinary
revision advance, preserve a score that was explicitly replaced, leave a removed
member in its old cell, delete its record on removal, or spuriously increment its
revision on a same-cell no-op. Other reversed cases force acceptance of
the malformed remove and set/unset forms; a third forces removal of an already
unplaced member. `CASES.tsv` names each exact expected property.
`BATCH-SOURCES.tsv` records the batch model, configurations, inherited models and
runner source hashes, including the per-case input parser and case-plan parser.
`RUNS.tsv` records each case's measured result, its own input fingerprint and the
TLC executable hash; timeouts and unexpected diagnostics are failures. The runner
emits the platform label `linux-amd64` rather than a machine name.

On 2026-09-29, after integrating dev `abdc7a23026316fc4e31435c75ff5df9d959eb54`,
frozen source `8fc37bcf7620695f5903891d8a5516b890fea035` ran all 39 required batch
cases in an isolated Linux container. All reached their declared outcomes: 12
positive, 22 action rejections and 5 invariant rejections. Each used one TLC worker
under the shared 110-second budget. The container had a two-CPU quota and 4 GiB
memory limit; the records retain the runtime's reported logical CPU count.
The TLC JAR hash is
`936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`.

The per-case runner is now integrated. Its `merge --keep` joined those 39 fresh
rows with the 76 unchanged current rows from dev, yielding 115 records. The older
global-fingerprint batch measurements remain in Git history; none was relabelled
as a fresh per-case run. All 4,480 source-file hashes matched before and after the
container runs, and the owned containers were removed.

This is a finite request-template model, not a JSON parser, Redis rollback proof or
implementation refinement proof. Its three-action main bound is explicit; the
second positive trace separately exercises successful new-epoch mutation and old
image preservation. Additional named follow-ups are reversed witnesses for the remaining invariants,
a distinct legacy missing-revision state (currently normalized to zero),
a model property that detects omitted field unsets, automatic verification of
`BATCH-SOURCES.tsv` and `BATCH-REPLAY-RUNS.tsv`, and a structured first-bad-step
diagnostic (currently present only in the TLC log).
The remaining runtime, replay, real-use and interactive gates
in `docs/SPEC-NOVA-TABLE.md` still apply after the model checks pass.

When TLC reports an invariant violation in the initial state without aggregate
statistics, the runner records the single reported counterexample state as 1/1.
That normalization requires the exact expected invariant and exit 12; it does not
turn parse failures or unexpected outcomes into passing records. Raw bench logs
retain the original diagnostic. Printed aggregate counts, when available, use the
last pair TLC reports.

### Batch execution replay

On a Linux bench, `tlacheck batch-replay` loads the supplied `table.lua` into
disposable Redis instances and captures consecutive calls. Each batch sends its
exact request bytes in one FCALL. The decoder checks the reply against independent
pre/post snapshots, the durable operation record and the stream event. It retains
absent fields separately from present empty strings and checks sparse field
deltas against the requested set/unset fields. Refusals and retries must preserve
the complete store image, including Redis stream metadata. Accepted writes may
change only their exact physical key set. The finite projection also checks row
scores, definition and operation-record metadata, and every earlier stream event
including its field order; consumer groups are unsupported and refused.

The generated harness applies the corresponding model action and compares every
observed state through `MatchesExecution`. It renders captured receipt values as
literals, rather than deriving observed values from the model's transition.
A separate negative case changes one observed member field and must fail
`MatchesExecution`; a parser error or an unrelated invariant failure does not
count as the expected rejection.

Functional fixtures run in an isolated container on a Linux bench. Redis Unix
socket paths must fit the platform limit; the macOS limit is 100 bytes, so a
fixture path of 104–106 bytes refuses before replay. Fixture directories use a
short private `TMPDIR` inside the run environment. The `tlacheck batch-replay`
command itself is Linux-only.

The suite covers move/set with a guard, exact retry and operation conflict;
same-cell no-op, removal and unset on the retained record; creation and
preventive refusals; an epoch advance/read/bind/create history; and an ordinary
remove followed by a batch with an omitted member-revision guard. Each history
uses the declared finite fixture. Its table definition, row metadata and member
fields must fit that fixture; unsupported state prevents a replay packet.

The command writes input hashes, captured evidence, generated model/configuration
files and TLC logs into a new `--dir`. The shared Go TLC executor enforces one
budget across capture and checking. It refuses to run on non-Linux hosts, downloads
nothing and leaves the source checkout untouched. Help lists its input flags and
exit codes. These bounded histories complement the exhaustive finite model cases;
they do not replace the runtime property corpus, maximum-size checks, CLI review
or real-use acceptance required by `docs/SPEC-NOVA-TABLE.md`.

`BATCH-REPLAY-RUNS.tsv` records measured results for five passing histories and
the named corrupted-observation rejection. Each row pins the Lua, generated trace,
configuration and captured evidence hashes. The run directory retains capture
packets, logs and `suite.json`. Preserve the executed binary and TLC JAR hashes
alongside those outputs. A record applies only to its captured inputs and finite
histories.

From the same frozen source `8fc37bcf7620695f5903891d8a5516b890fea035`, the
2026-09-29 `linux-amd64` container run completed all six declared histories on
Redis 8.0.5; `corrupt-observation` failed exactly `MatchesExecution` as expected.
The captured Lua hash is
`e97d6a1528a4cfb6762c10ee404004919a7b954dd4db32fb851ac3bd26caf85b`,
the TLC JAR hash is
`936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`,
and the generated `suite.json` hash is
`227778b4f7303fb4e019e93edf1d52d3ff530e09ab592e37e2a3db008c4a62d0`.
The standalone `tlacheck` executable was retained with SHA-256
`b99c07886cb789e827b68579d33eb256e85371db102c4466304a098f4ad2d011`;
both the model and replay containers built the same executable bytes.
These six finite captures establish only the listed histories, not general
runtime refinement or behavior at the 16 MiB batch field-value bound.
