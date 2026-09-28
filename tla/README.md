# tla: the models of nova-tools' state machines, and their runners

The TLA+ modules here are the specifications of the state machines this repo implements (rowan-new SPEC-COORDINATOR section 8: the backend is the state machine, the verbs are its actions; Glenn 2026-09-27: TLA+ for every state machine, every project). The findings each model produced, verified against the code by hand, are in rowan-new `specs/tla/FINDINGS.md`; the model documents (`TABLE-MODEL.md`, `MEMBER-TABLE-MODEL.md`) are copied here beside the modules they describe.

| Module | Instance | What it is |
|---|---|---|
| `CardMachine.tla` | `MCCardMachine` | the card's life over cells (the copy model of 02_card_move.lua), the corrected design after its three findings |
| `LandWatch.tla` | `MCLandWatch` | the land watch's watcher (land_watch.lua): stamps, slow and wall, one note per stay (findings L1 to L3) |
| `TableMachine.tla` | `MCTable*` | nova-table as table.lua is today at f7745885, with its actual gaps (Stella; the strict gate fails on purpose) |
| `MemberTable.tla`, `EpochMemberTable.tla` | `MCMember*`, `MCEpochMember*` | the corrected member placement and epoch protocol (Stella): one place per table inside the epoch, lossless shape, no owned alias, stale writers refused |
| `TableEdit.tla`, `TableOrder.tla` | `MCTableEdit*`, `MCTableOrder*` | nova-table's edit verbs and the order of its rows and columns, with reversed witnesses |
| `TableSession.tla` | `MCTableSession*` | `nova-table shell`: lines, one connection, the store coming and going, a stop signal, the exit code (the design #4458 is held to) |
| `RedisFn.tla` | `MCRedisFn*` | the function libraries of one Redis under several loaders (internal/redisfn: Check, Load, Ensure, LoadMissing): one holder to a function name, a refusal that writes nothing, no moment without the library, a LoadMissing that never replaces |
| `FirstConn.tla` | `MCFirstConn*` | `internal/redisconn`'s first connection: the probe Open sends, taken and answered in the store's place only after HELLO was accepted, with seven reversed witnesses |
| `TableFirstContact.tla` | `MCTableFirstContact*` | nova-table's first contact with a store (cmd/nova-table/library.go): a verb that meets "Function not found" loads the library with LoadMissing at most once per process and is sent again once, only when its first send ran nothing, with three reversed witnesses |

Runners. The table/member model checkers and the member execution replay each run their whole suite under one 120 s budget (a timeout is a failure, never a green); the bare java commands and the Lua replay carry no cap of their own, so wrap them (`timeout 120 ...`) when a bound matters. `-deadlock` on the java commands turns TLC's deadlock check OFF: these models end in a terminal stutter by design, and the safety and liveness properties are what they check.

```sh
python3 tla/check_table.py  --jar /path/to/tla2tools.jar --mode all --out /tmp/table-results
python3 tla/check_member.py --jar /path/to/tla2tools.jar --out /tmp/member-results
python3 tla/check_member_replay.py --source internal/nsprint/fn/lua/table.lua --jar /path/to/tla2tools.jar --out /tmp/member-replay
python3 tla/check_lua_witnesses.py <(git show f77458853af46fdbbafd6881a4b46006431f266f:internal/nsprint/fn/lua/table.lua)
timeout 120 java -cp /path/to/tla2tools.jar tlc2.TLC -workers 8 -deadlock tla/MCCardMachine.tla
timeout 120 java -cp /path/to/tla2tools.jar tlc2.TLC -workers 2 -deadlock tla/MCLandWatch.tla
```

tla2tools v1.7.4 (TLC 2.19); the fleet keeps a copy on space at `~/tla/tla2tools.jar`. The runners live here and not in rowan-new because the self repo is text only (its hook refuses machinery); the CI functional leg will run them under the two-minute cap once the runners carry the jar (owed).

## Table execution and receipt replay

`check_member_replay.py` captures 32 controlled source-API transitions in a
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

The failure edge after Open is classified outside the model, and adds no
state or transition to it: the hook `redisconn.Open` installs once the
handshake is done (`internal/redisconn/explainer.go`) reads a command's failure
by where it stands against the command, Unreachable before anything of it was
written (a dial refused, a lost setup), AuthRefused for a refused login, and
Unconfirmed after it was written (the reply lost; the write may have
committed), and rewrites the error, sending and reading nothing. The pool's
size, the dial's bound, a password handed over from memory and the count of
the handshake's round trips (read from `firstConn`'s record that the probe
was taken, the model's `taken`) change no action either; TLC was not rerun
for a change of comments only.

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
