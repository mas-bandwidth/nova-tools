# tla: the models of nova-tools' state machines, and their runners

The TLA+ modules here are the specifications of the state machines this repo implements (rowan-new SPEC-COORDINATOR section 8: the backend is the state machine, the verbs are its actions; Glenn 2026-09-27: TLA+ for every state machine, every project). The findings each model produced, verified against the code by hand, are in rowan-new `specs/tla/FINDINGS.md`; the model documents (`TABLE-MODEL.md`, `MEMBER-TABLE-MODEL.md`) are copied here beside the modules they describe.

| Module | Instance | What it is |
|---|---|---|
| `CardMachine.tla` | `MCCardMachine` | the card's life over cells (the copy model of 02_card_move.lua), the corrected design after its three findings |
| `LandWatch.tla` | `MCLandWatch` | the land watch's watcher (land_watch.lua): stamps, slow and wall, one note per stay (findings L1 to L3) |
| `TableMachine.tla` | `MCTable*` | nova-table as table.lua is today at f7745885, with its actual gaps (Stella; the strict gate fails on purpose) |
| `MemberTable.tla`, `EpochMemberTable.tla` | `MCMember*`, `MCEpochMember*` | the corrected member placement and epoch protocol (Stella): one place per table inside the epoch, lossless shape, no owned alias, stale writers refused |

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

Measured on space, 2026-09-27, load 9, all twelve at once: 38 s wall.

| config | result | time |
|---|---|---|
| `MCTableEdit` | no error, 468,243 distinct states, depth 4: TypeOK, RefusalWritesNothing, ShapeLosesNothing, PlacedInShape, TextInTextColumns, HideKeepsData, RenameKeepsData | 37 s |
| `MCTableEditBrokenRefusal` | RefusalWritesNothing violated in 2 states: `row add 1 2`, row 2's key of the wrong type, row 1 left written (109939a85 lines 189-192) | 1 s |
| `MCTableEditBrokenText` | ShapeLosesNoText violated in 4 states: a text value set, `set --columns` without the column deletes it (line 341) | 2 s |
| `MCTableEditBrokenLegacy` | ShapeLosesNoMember violated in 5 states: a member placed, a formula column's stored fold stops parsing, `set --columns` drops the member's column with no OCCUPIED check (line 311) | 8 s |
| `MCTableOrder` | no error, 241,073 distinct states, depth 4, 3 rows, 3 columns: TypeOK, RefusalWritesNothing, RowMoveIsExact, RowOrderIsExact, ColMoveIsExact, AddIsExact, BindIsExact, StandingSortHolds, ReorderIsPermutation, HeldInShape, OnlyRowDelDrops, RowsAndColumnsApart | 19 s |
| `MCTableOrderBrokenBind` | StandingSortHolds violated: bind writes its input order under a standing sort (3ee97bea: bind never reached the standing-sort step; Stella's probe 1) | 2 s |
| `MCTableOrderBrokenCombined` | StandingSortHolds violated: one call sets `--keep` and moves a row (3ee97bea: the guard read the sort before the edit and exempted any call with row_sort; Stella's probe 2) | 2 s |
| `MCTableOrderBrokenSort` | StandingSortHolds violated: row add ignores the standing sort | 2 s |
| `MCTableOrderBrokenPrefix` | RowOrderIsExact violated: the named rows put last | 2 s |
| `MCTableOrderBrokenItem` | RowMoveIsExact violated: `--first` moves another row | 1 s |
| `MCTableOrderBrokenDel` | OnlyRowDelDrops violated: `col del` removes a column that holds a member | 2 s |
| `MCTableOrderBrokenBindLoss` | OnlyRowDelDrops violated: bind drops an omitted row that holds a member | 2 s |

The first four witnesses of the edit model and the Bind and Combined witnesses
of the order model are defects that were in the code, each checked by hand
against the lines named. The other five are misimplementations the invariants
are shown to catch. A depth-5 run of the edit model with one member (1,652,467
distinct states, no error) took 92 s on the same bench and is not in the set.
