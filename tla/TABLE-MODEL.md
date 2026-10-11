# nova-table: current implementation model and counterexamples

The table implementation is in `pkg/nsprint/fn/lua/table.lua`, with the public-call shape in `pkg/ntable/store.go`. The coordinator spec defines a table as a dimension and placement as one cell per member **per table**.

**Disposition: the desired ONE PLACE and lossless-bind contract is not implemented.** The model intentionally contains the current behavior. Passing the narrower current-contract suite is not approval of the desired foundation. The default `strict` runner fails on an outstanding invariant; `witnesses` verifies that each named known failure remains reproducible. No assumed repair has been inserted into the model.

## What was checked

`tla/TableMachine.tla` models live definitions, row membership, binding targets, physical member/score entries, and two writers choosing interleaved complete operations. Physical entries are separate from visible owned cells: changing metadata can hide a live entry without deleting it. A bound view can name an external set or, in the alias counterexamples, another table cell key.

| Operator | Lua correspondence |
| --- | --- |
| Create | `ns_table_create`, matching definition assumed |
| RowAdd | `ns_table_row_add`: rewrite metadata, retain physical sets |
| RowDelete / DeleteRows | `ns_table_row_del`, `T.remove` |
| Add / Remove / Move | `T.writecell`, with `T.cell` target checks |
| Clear / Drop | `T.delete`; clear refuses the entire operation when any bound cell exists |
| Bind | `ns_table_bind`: keep/rebind specified rows, delete omitted owned cells |

Move is only between columns in the same row, as in the current API. Moving across rows/tables, guarded edges, records and epochs are not implemented here. Scores use two representative values; display ordering, text projections, excluded display members and rendering are abstracted away. Read-only snapshots stutter with respect to these variables.

The model assumes valid compatible definitions, well-typed keys, sufficient ACL permission, and no unrelated process changing keys during a function. It models the function's successful state transition, not Lua command-by-command execution or Redis rollback. Redis function errors can leave earlier writes in place; the code's preflight and functional refusal tests must be reviewed separately. The model is not a refinement proof or evidence that batching alone gives atomicity.

The initial state has two already-created tables, both rows, one owned member, and one member in the external set. This is reachable by create/row-add/cell-add plus the external owner's setup. Subsequent create/drop/row-add/delete/clear/bind remain enabled. Both writers may choose every operation; there are no per-writer pending requests or lost replies. External-owner changes after initialization, crash recovery, key-name collisions, invalid arguments and score arithmetic are outside this revision.

## Instances and limits

- `MCTableMachine.cfg`: **2 tables, 2 rows per table, 3 columns, 3 members, 2 writers, 2 scores; all interleavings up to 3 API calls after the seeded initial state**. Bind/RowAdd inputs introduce at most one binding per call; calls can accumulate bindings in different rows. Bind can choose every subset of rows. Bind targets are disjoint external storage for the positive suite.
- `MCTableFixedPoint.cfg`: **1 table, 1 row, 2 columns, 1 member, 2 writers, 2 scores**, no call-depth bound (`MaxSteps = 0`). TLC exhausts this smaller finite reachable graph. This does not generalize the result to arbitrary table sizes or binding patterns.
- Named negative configurations use the requested larger universe and at most two calls, sufficient for their witnesses. `BindRetained` isolates reshaping with all rows retained, to distinguish hiding a member from deleting it.
- No symmetry reduction, state constraint or liveness claim is used. Positive-depth instances explicitly stop taking API calls at their bound and permit terminal stutter; deadlock checking remains enabled. The fixed-point instance permits all modeled operations indefinitely. No fairness assumption is needed for these safety checks.

The positive invariants are type/shape consistency, atomic move score/source postconditions, bound-set preservation by cell writes under disjoint ownership, successful clear retaining the definition while emptying its active shape, drop preserving disjoint bound sets, and external storage untouched by table operations. Sticky audit flags retain failed action postconditions. They check the specified transitions; they do not independently establish correspondence to the Lua.

## Counterexamples, checked against Lua

All start with `m1` at score 1 in `t1/r1/c1`; both tables have rows `r1,r2` and columns `c1,c2,c3`. The external set initially contains `m2` at score 2. The cross-table case is a scope control showing allowed behavior, not a defect under the updated per-table rule. Every described operation was replayed against the pinned function library in a fresh Redis with TCP disabled.

| Check | Short witness | Lua result and implication |
| --- | --- | --- |
| OnePlacePerTable | Add `m1` to `t1/r1/c2` | Both cells contain `m1`. `T.writecell(add)` checks only the target; there is no placement index. |
| Scope control: cross-table membership | Add `m1` to `t2/r1/c1` | Both tables contain `m1`, as intended. This rejects the overstrong global-uniqueness property; `strict` excludes this check. |
| BindPreservesOwned | Bind `t1` with no rows | `T.remove` deletes the owned key and `m1`. Lossless reshaping is not the current contract. |
| BindPreservesOwned, retained rows | Keep both rows, bind `t1/r1/c1` to external | Physical `m1` remains, but that cell now displays `m2`. Retaining the physical key alone is insufficient. |
| CellWritesPreserveBoundSets, alias | Row-add binds `t1/r1/c1` to owned key `t1/r1/c2`; add `m1` to `c2` | The write to the owner changes the bound view. Direct writes through the bound cell remain refused. A view does not freeze another cell's owner. |
| DropPreservesBound, alias | Row-add binds `t1/r1/c2` to owned key `t1/r1/c1`; drop `t1` | Removal of the unbound owner deletes the bound target too. The blanket “bound sets remain untouched” promise needs an alias/ownership rule. |

The last two are distinct from ordinary external-owner updates. The same table accepted an alias to its owned storage, so its own permitted actions mutate/delete a bound target. An implementation must either reject these ownership conflicts or state a narrower view contract. Bound aliases also mean visible copies cannot be counted as separate owned placements.

`tlacheck witnesses` retains these concrete reproductions, plus positive controls: successful move keeps the score, bound add/remove/move and whole-table clear refuse, refusal leaves the other owned member intact, drop preserves a disjoint external set, and successful clear removes owned rows/members while retaining the definition. The command owns only its temporary Unix-socket Redis process; it accepts a pinned source file, never a store address.

RowAdd also rewrites binding metadata without deleting the physical set; any lossless-reshape repair must cover that path as well as Bind.

ONE PLACE should be “at most one owned location per member within the chosen namespace,” with “exactly one” conditional on a live record. Otherwise remove/drop and an empty table violate it by definition. The latest section 7 settles the namespace as per table: a member record holds its cell in each table dimension. Cross-table membership remains allowed. The epoch semantics and ownership-alias contract still need agreement. The required record/set double link belongs in the next design model; it is absent from the pinned implementation and is not silently added here.

## Running and interpreting results

Use the official [TLA+ tools v1.7.4 release](https://github.com/tlaplus/tlaplus/releases/tag/v1.7.4), `tla2tools.jar` SHA-1 `bee4a54f3ee3d4afc347c3240ec2d9e93b075104`. It identifies itself as TLC 2.19. The local run used OpenJDK 27, 2 TLC workers, a 2 GiB heap and no remote services. Java's local RMI listener may require the normal local-execution permission in a sandbox. The runner does not download anything.

```sh
go run ./tools/tlacheck table --jar /path/to/tla2tools.jar --mode all --dir /tmp/table-model-results
go run ./tools/tlacheck table --jar /path/to/tla2tools.jar --mode strict --dir /tmp/table-model-strict
# Extract the exact pin in a nova-tools checkout, then replay locally:
git show f77458853af46fdbbafd6881a4b46006431f266f:pkg/nsprint/fn/lua/table.lua > /tmp/table-f7745885.lua
go run ./tools/tlacheck witnesses /tmp/table-f7745885.lua
```

`all` means current-contract checks plus **five expected failures and one allowed cross-table scope control**, not all desired invariants passing. The entire TLC runner has a 120-second budget, including all cases; timeout is a failure, not success or an inconclusive green. `strict` is expected to fail today: exit 1, with a `TABLE FAIL` line naming the first finding the table code still has. A newly missing or changed counterexample fails witness mode so a repair requires updating the model and its disposition. Do not install the positive-only suite as proof that ONE PLACE is solved. CI integration into nova-tools is still owed with the implementation repair and its cross-repository source pin.

Recorded runs of the larger three-call check report 11,230,481 generated states, 693,619 distinct states, and queue exhaustion in about 32 seconds; the smaller fixed-point run reports 49,752 generated states, 1,157 distinct states and queue exhaustion in under a second. Recorded runs of five expected counterexamples, the cross-table scope control and both positive configurations finish together in about 40 seconds. These are bounded-instance results and recorded timings, not guarantees. Retained raw logs are the evidence; counts should be refreshed when semantics or configurations change.

## Required next decisions and gates

1. Use the decided per-table placement namespace; settle the live-record, epoch and bound-alias ownership contracts.
2. Implement ONE PLACE and lossless bind (including metadata rebinding), then change the model to those actual operations. Preserve the current witnesses as implementation regressions with reversed expectations.
3. Add the epoch/placement relationship, records and guarded move doors as a single coherent protocol. A column graph alone cannot prove copy/lease/budget invariants.
4. Write abstraction mappings to the rebased CardMachine and later backend, explicitly marking any assumed repairs. Model external work as start/progress/completion/timeout steps, not one Redis transition.
5. Keep runtime corruption checks and functional atomic-refusal tests alongside model checking and future committed trace validation.

The model changes no live store, ACL, fleet process, existing CardMachine model, or coordinator spec.
