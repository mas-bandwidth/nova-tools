# SetTable: Layer 1 of the sprint foundation, the table, as a TLA+ model

`SetTable.tla` is the state machine of Layer 1 (`tset/1`), written from its contract, `design/L1-CONTRACT.md` (the Layer 1 table contract), at **contract revision 4**: the published Layer 1 revision-4 pin, sha256 `c4ba033f147fd925bb4628938d4156bf2286815f3422bacff6cd1c825baddbf1`. The pin is not a G0 pass. The model checks the contract and nothing else. `MCSetTable.tla` holds the small instance, the callers' programs and the reachability probes. Every configuration named below is a file in `tla/` or in `tla/settable-bench/`.

The first version of this model was written from contract revision 2. It found three holes (H1 to H3 under Findings), and revision 4 is the contract's answer to them. This version models that answer: rowset guards before an advance, a receipt status and the explicit fence, the caller's settlement rule after an unknown outcome, new op identities for a fenced part, and done before a replan. It also carries the two revision-3 items the model owed: final occupancy for a deleted row, and stays made by the plan.

## What is modelled

**The state Layer 1 owns.**

- `rec[t][m]`: the record of each member (stored ID) in each table: whether it exists, its epoch, its revision, its place (a cell, or none) and one application field.
- `cells[t][e][x]`: the sorted set of each cell at each epoch.
- `rows[t][e]`: the rows of each table at each epoch (names; ranks are not modelled).
- `ep`: the active epoch.
- `done[<<e, op>>]`: the receipts, keyed by request epoch and op identity. Each one holds the digest the store matches (the intent), the recorded result and the **status**: `ok`, or `fenced` (settled as not applied).

**The in-flight state of each caller.**

- `prog`: the caller's program, one or more parts. Each part is one atomic step with its own op identity.
- `pc` and `part`: where the caller is.
- `oe`: the verb's original epoch, fixed at its first read and kept across retries and crashes, as the resume manifest keeps it.
- `snap`, `req`, `rep`: what the caller read, the request it built (with the original `fence` bit), and the reply it has.
- `tries`: its retries so far (resends, fences, done queries and rebuilds). The bound is per caller.
- `late`: a request it gave up waiting for, which may still be executed.
- `unres`: the current part's identity had a dispatched request whose outcome is unknown, and nothing has settled it. While it is set, a refusal is not final.
- `reop[c][i]`: how many new op identities part i has taken after a fenced settlement. The op identity is `<<op, k>>`, a bounded family.
- `via`: how the next plan is made, for the ghost `how` of a request (below).

**Ghosts that exist only for the properties.**

- `hist`: every application of an identity so far.
- `last`: the store step just taken. Every other action resets it.
- `how` in a request: `plan` for a plan on the contract's path (a part's first plan, a rebuild after a final refusal, or a plan under the same identity made after done said absent), `direct` for a replan of an unresolved identity made without asking done (only when `DirectReplan` is TRUE), and `fence`. It is not part of the request's bytes, and resent bytes keep it.

**Actions.**

| action | what it is |
|---|---|
| `Read(c)` | an atomic planning read of the named records, the active epoch and, for a clear, the rows of every catalog table. The verb's first read fixes its original epoch |
| `Plan(c)` | builds the request from what was read. A move or remove takes its expected place from the read, and takes its expected revision from the read or names none. A move with no destination is a stay in the member's own cell. A count guard can be added. A clear is one `rowset` guard per catalog table (`rows:[]` for a table read empty), then the advance, then one rows entry per table that restores every row read |
| `Fence(c)` | the explicit fence: the same original epoch, op and intent, `fence:true`, no entries. It is sent only for an unresolved identity: on an unknown outcome, on a refusal that does not settle it, or after done said absent |
| `Answer(c)` | the store executes the request in one atomic step, in the contract's phase order. Static validity first: a move or remove with no source cell, a rowset out of place (after or without an advance, or twice for one table), or a fence with entries, is `REQUEST`. Then the receipt: a matching intent replays **with the saved status**, a different intent is `OPCONFLICT`. Then `EPOCHAHEAD` and `STALE`. A fence with no receipt at the active epoch takes its own path: it writes only its receipt, status `fenced`, and answers `fenced`. Otherwise `ADVANCE` and `OVERFLOW` (no successor epoch); then the rowset guards at the **request epoch** (`ROWSET`); then topology (`ROWCONFLICT`, `OCCUPIED` by final occupancy, `DRIFT` of new-epoch keys); then every member and cell guard against the state at apply time; then every write, with the receipt at the request epoch |
| `FenceFault(c)` | a fence refused with a code the model does not compute (`FAULT`): ENGINE and CONFIG, which section 8 puts before the receipt lookup, and NOPERM or WRONGTYPE from `S.fence_prepare`. It writes nothing and settles nothing (bounded by `MaxFaults`) |
| `LoseReply(c)` | the store ran the step and the reply never arrived (OUTCOMEUNKNOWN) |
| `Timeout(c)`, `LateRun(c)` | cancellation after dispatch: the caller stops waiting, and the request it sent can still run later, with its reply going nowhere |
| `RetrySame(c)` | the same bytes again, while the identity is unresolved |
| `AskDone(c)`, `Resume(c)` | the resume path: done for every part identity of the manifest, then the first part not done with status `ok`. `absent`: a plan under the **same** identity. `fenced`: settled as not applied. `conflict`: ended. `AskDone` takes this path without a crash |
| `Rebuild(c)`, `RetryFresh(c)` | a re-read and a new plan under the same identity: after a final refusal, or, when `DirectReplan` is TRUE, while the identity is unresolved without asking done. `DirectReplan` is FALSE, the contract's path, in every configuration but the bench's `MCSetTableDirect` |
| `Accept`, `Stale`, `Conflict`, `GiveUp`, `ReportRefused`, `NewOp`, `LostCause` | the caller's side of a reply. `ok` or an `ok` replay: next part or finished. `STALE`, `OPCONFLICT`: ended. Any other refusal is reported final (`GiveUp`) only when nothing unresolved is behind it. `fenced`: report the part refused, or replan it under a new op. `lost` when the retries are used up with the identity unresolved |
| `Crash(c)` | the caller's process dies. What it had in memory is gone; the manifest and anything left in flight survive. After a crash the unfinished part is presumed dispatched once the verb has fixed its epoch |
| `Beat` | a second supported writer outside the programs, like the sprint's beat: one guarded step with no op, read and applied at once. The other caller's steps are also a second writer |
| `Quiet` | every caller has ended. This is rest, not deadlock, so TLC's deadlock check stays on |

**The contract's guarantees, stated apart from the store's own check.** A witness that changes the store must be caught by a property that does not share its code.

| property | kind | contract |
|---|---|---|
| `OnePlace` | invariant | one placement per member: at most one cell per table, only in its own epoch, exactly where its record says |
| `RowsHold` | invariant | no member in a cell of a row the table does not have (in an extant final row) |
| `RefusedWritesNothing` | action | a refusal changes no key |
| `AppliedIsWhole` | action | a step that changed the store applied every entry, with its `ok` receipt; a fence that changed the store wrote its `fenced` receipt and nothing else |
| `RevisionMoves` | action | a member's revision moves by exactly one in a step that changes it, never on a no-op |
| `GuardsAtApply` | action | no applied step had a guard that was false at apply time, the rowset guards and final occupancy included |
| `ReceiptTruth` | action | at most one application per identity; an `ok` receipt is its own step's; a `fenced` identity was never applied; a reply's result is the application's. A statically valid request whose identity was applied with the same intent is answered with the recorded `ok` result and writes nothing. **Narrowed at revision 4 (section 2, H3):** the claim covers resent bytes, a plan made after done, and a fence; a `direct` replan is not claimed, and a `FAULT` is outside it |
| `ConflictOnChangedIntent` | action | a receipt with another intent refuses `OPCONFLICT` and writes nothing |
| `EpochSafe` | action | the epoch moves forward only by an applied advance; a step writes only at the epoch it names, and its receipt (or a fence's) only at its request epoch; an applied identity is never answered `STALE` |
| `RestoreIsCurrent` | action | **new contract property (H1):** a complete topology-preserving clear restores the rows the closing epoch has when it closes, for every catalog table, an empty one included |
| `RefusedIsFinal` | invariant | **new contract property (H2):** a verb that reported a part refused never has that part applied, then or later, under any of its op identities |
| `FinishedIsApplied` | invariant | **new (H2):** a verb that ended finished has every part applied, a crash after a fence included |
| `PartOnce` | invariant | **new (H2):** no part's effects are applied under two op identities |
| `Settles` | temporal | every caller comes to an end, under fairness of what callers and the store owe |

**A goal the contract disclaims.** `PartsWhole` (invariant): a verb that ended `STALE` has none of its parts applied. Section 5 says "This contract guarantees each identified atomic part, not a whole multi-part transaction", so it is expected to fail (finding 4).

## What is not modelled

- **Sizes, budgets and limits.** LIMIT, BUDGET, argv pieces, bytes, the 2,000-member ceiling, the 1,024 row pairs of an advance, the 32 KiB receipt cap.
- **Time.** The 25 ms gate, TIME and `now_ms`.
- **Redis itself.** Key types, ACL, argument splitting, the score parser and runtime errors. The contract's rule that prepare finds every returnable error before the first write is assumed, not proved: the store of the contract never half-applies, and witness W2 shows what breaks when it does. The refusals of a fence that the model does not compute are one outside event, `FAULT`.
- **Row ranks.** A rowset compares row names only. The contract's rowset also compares exact ranks, so a delete-then-re-add that keeps the name and changes the rank is not modelled, and neither is the rank a restored row gets in the new epoch.
- **The log and history (Layer 2).** Lines, seqs, `about`, cardlines, LOGID and notes are in `SetTableLog.tla`, not here. That a fence and a rowset emit no line is Layer 2's to check.
- **Preplans and the sealed original request.** The enclosing preplan, appended entries and notes, and the seal of the rowset prefix and fence bit are not modelled.
- **Reads.** Only the planning snapshot and the `done` query. Range, count, rcount, pagination, cursors and EPOCHGONE are not modelled.
- **Scores.** One application field stands for every effective change.
- **The Go twin, `Steps` pipelining, and all static request checks but three.** The three: a move or remove with no source cell, the rowset shape, and the fence shape. No program sends a malformed rowset or fence, so those two static checks are written but never refuse in these runs.
- **Carried member state across a clear.** A clear here restores rows only. Member freshness across a clear is the upper design's writer exclusion (errata 1), which the contract relies on and this model does not.
- **Op-less caller steps.** Only the outside beat is op-less.
- **More than two advances, two callers, three tables.** Epochs are 0..1 in every configuration but the two-advance ones (0..2).

## The instance

Two tables (`t1`, `t2`); three (`t3` added, with no rows at epoch 0) for the empty-table cases. 2 rows (`r1`, `r2`), 2 columns (`c1`, `c2`), 3 members (`m1`, `m2`, `m3`), 2 callers (`a`, `b`), field values 0 and 1.

- **Epoch 0.** `t1` has rows `r1` and `r2`, `t2` has `r1` only, `t3` (when present) has none. `m1` is in `t1` at `r1:c1` and in `t2` at `r1:c2`: one primary in two tables. `m2` is in `t2` at `r1:c1`, and `m3` is nowhere yet.
- **The beat** either moves `m1` back to `t1` `r1:c1` with field 0, or sets `m2`'s field to 1 where it is.

The programs (`MCSetTable.tla`):

| program | parts |
|---|---|
| `AMove` | a1: move `t1` `m1` to `r2:c1`, field 1 |
| `ATwo` | a1: the same move, and `t2` `m2` to `r1:c2`, in one step over two tables |
| `AChunk` | a1: the `t1` move; a2: the `t2` move (a chunked verb, two parts) |
| `ACap` | a1: move `t1` `m1` to `r2:c2` guarded by count(`r2:c2`) <= 0 |
| `AAddRow` | a1: rows add `r2` to `t2` |
| `AAddRowT3` | a1: rows add `r1` to `t3`, the table that was empty (three-table instance) |
| `AClear` | a1: a complete clear (for two advances) |
| `BMove` | b1: move `t1` `m1` to `r1:c2`, field 0 (races a on `m1`) |
| `BClear` | b1: a complete clear: a rowset for every catalog table, advance from the original epoch, restore every row read |
| `BDelCreate` | b1: rows delete `r2` of `t1` and create `m3` at `r2:c1`, one step |
| `BCreate` | b1: create `t1` `m3` at `r2:c2` |
| `BReuse` | a1 (a's op, another intent): move `t1` `m1` to `r1:c2` |
| `BRemove` | b1: remove `t1` `m1`, field 1 |
| `BStay` | b1: `t2` `m2` stays in its cell and takes field 1 (a no-op stay once it has it) |
| `BDelMove` | b1: rows delete `r2` of `t1` and move `m1` to `r1:c2`, one step (final occupancy) |

## Reversed witnesses

`Broken` names the one rule a witness breaks. Every other rule stays the contract's.

| Broken | the rule broken | fails |
|---|---|---|
| `W1` | guards checked at plan time only: apply writes on the plan-time verdict | `GuardsAtApply` |
| `W2` | the commit can stop after the first table's writes (a runtime error mid-commit): no receipt, an error reply | `AppliedIsWhole` |
| `W3` | the receipt matches the request's bytes, not the intent | `ReceiptTruth` |
| `W3b` | the receipt identity includes the request's bytes | `ReceiptTruth` |
| `W4` | an advance's receipt is written at the epoch it moved to, while lookup stays at the request epoch | `EpochSafe`; `LostAdvanceAnswered` (W4Lost) |
| `W5` | a create's destination is checked against pre-state rows plus additions, ignoring deletions in the same step | `RowsHold` |
| `W6` | a move or remove checks neither the record's place nor the source cell | `OnePlace` |
| `W6cell` | only the source-cell probe is dropped; the record's place is still checked. Expected to hold (observation under Findings) | holds |
| `W7` | the rowset guards are not evaluated | `RestoreIsCurrent` |
| `W7b` | a clear leaves out the rowset of a table it read as empty | `RestoreIsCurrent` |
| `W8` | a winning fence stores status `ok` (its fresh reply still says `fenced`) | `FinishedIsApplied`, after Crash and Resume |
| `W9` | a refusal is reported final even while the identity is unresolved (giving up without a fence) | `RefusedIsFinal` |
| `W10` | after done said absent for an unresolved identity, the caller takes a new op while the first copy may be in flight | `PartOnce` |

## Running it

TLC runs only on a bench, never on a working machine. Each run goes in its own directory, with its own `java.io.tmpdir` and `-metadir`.

**The gated cases** live in `tla/` and are declared in `CASES.tsv` in seven groups, each within the gate's 110 s on two workers: `settable` (`MCSetTable`, `MCSetTableRestore`, `MCSetTableEpochs`, `MCSetTableOccupy`, `MCSetTableW6Cell`), `settable-b` (`MCSetTableMainB`), `settable-live`, `settable-faults`, `settable-holes` (`MCSetTableRefused`, `MCSetTableResume`, `MCSetTableReplan`), `settable-witnesses` and `settable-goals`. `tlacheck` runs and records them like every other model (the records are in `RUNS.tsv`; `tlacheck groups --stale` names the groups an edit staled; see `README.md`):

```sh
for g in settable settable-b settable-live settable-faults settable-holes settable-witnesses settable-goals; do
  go run ./tools/tlacheck run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-$g --group $g
done
```

**The bench-size runs and the reachability probes** live in `tla/settable-bench/`, which the gate does not scan. Run them from `tla/`, each in its own directory:

```sh
cd tla
for c in MCSetTableFullA MCSetTableFullB1 MCSetTableFullB2 MCSetTableFullRefused MCSetTableFullResume MCSetTableFullRestore MCSetTableFullEpochs MCSetTableDirect MCSetTableFullLive; do
  mkdir -p /tmp/tlc-$c
  timeout 900 java -XX:+UseParallelGC -Djava.io.tmpdir=/tmp/tlc-$c -cp /path/to/tla2tools.jar \
    tlc2.TLC -workers 8 -metadir /tmp/tlc-$c/states -config settable-bench/$c.cfg MCSetTable.tla > /tmp/tlc-$c/$c.log 2>&1
done
```

The probes (`settable-bench/MCSetTableReach*.cfg`) run the same way. Each copies a gated case's bounds with one probe invariant that says a path never happens, and each is expected to be violated.

TLC prints every variable in a counterexample. The ones to read are `last` (the store step just taken: who ran it, its outcome, code, status and request), `pc`, `req`, `unres` and `late` (the callers), and `cells`, `rec`, `rows` and `done` (the store).

## Results

All runs used TLC 2.19 (`tla2tools.jar` sha256 `936a2620...`) on Java 21.0.12.1, on a 32-core Linux bench, on 2026-09-30 (UTC), with `SetTable.tla` at this commit (the note under the bench table says which runs predate two menu definitions in `MCSetTable.tla`).

**The gated cases**, recorded in `RUNS.tsv` by `tlacheck`: pass cases on two workers (two JVM processors, 2 GB heap), counterexample cases on one. Unless the row says otherwise, a case has `MaxEpoch` 1, `DirectReplan` FALSE, 1 request in flight, no new op, no fault, and checks the contract properties above except `Settles`.

| config | instance | result | generated | distinct | seconds | trace |
|---|---|---|---|---|---|---|
| `MCSetTable` | a's 5 programs x `BMove`, `BClear`, `BDelCreate`, `BCreate`; 1 retry each | pass | 731,363 | 234,155 | 35.6 | |
| `MCSetTableMainB` | a's 5 programs x `BReuse`, `BRemove`, `BStay`, `BDelMove`; 1 retry each | pass | 1,190,898 | 368,905 | 62.1 | |
| `MCSetTableLive` | `AMove`, `AChunk` x `BMove`, `BClear`, `BReuse`; 1 retry each; under fairness | pass, `Settles` | 467,471 | 145,604 | 53.3 | |
| `MCSetTableFaults` | `AMove` x `BMove`; 1 retry each, 1 crash, 1 new op, 1 fault | pass | 992,539 | 276,978 | 53.1 | |
| `MCSetTableRestore` | three tables; `AAddRow`, `AAddRowT3` x `BClear`; 1 retry each (H1) | pass | 34,370 | 11,932 | 5.3 | |
| `MCSetTableEpochs` | `AClear` x `BClear`, epochs 0..2; 1 retry each, 1 crash | pass | 70,359 | 21,024 | 5.4 | |
| `MCSetTableOccupy` | `AMove` x `BDelMove`, `BStay`; 1 retry each, 1 beat | pass | 608,792 | 169,753 | 35.8 | |
| `MCSetTableRefused` | `AMove` x `BMove`; a 2 retries, b none, 1 beat (H2 trace A) | pass | 198,509 | 57,643 | 13.0 | |
| `MCSetTableResume` | `AMove` x `BRemove`; b 1 retry, a none, 1 crash, 1 beat (H2 trace B) | pass | 416,823 | 113,452 | 24.7 | |
| `MCSetTableReplan` | `AMove` x `BRemove`; b 2 retries, a none, 1 beat (H3) | pass | 138,777 | 40,177 | 10.2 | |
| `MCSetTableW6Cell` | `AMove` x `BMove`; 2 retries each, nothing in flight | pass, `OnePlace`, `GuardsAtApply` | 22,857 | 7,420 | 2.9 | |
| `MCSetTableW1` | `AMove` x `BMove` | `GuardsAtApply` violated | 1,803 | 976 | 1.8 | 7 states |
| `MCSetTableW2` | `ATwo` x `BMove` | `AppliedIsWhole` violated | 39 | 35 | 1.4 | 4 states |
| `MCSetTableW3` | `AMove` x `BMove` | `ReceiptTruth` violated | 1,355 | 778 | 1.8 | 7 states |
| `MCSetTableW3b` | `AMove` x `BMove` | `ReceiptTruth` violated | 1,355 | 778 | 1.9 | 7 states |
| `MCSetTableW4` | `AMove` x `BClear` | `EpochSafe` violated | 94 | 76 | 1.2 | 4 states |
| `MCSetTableW4Lost` | `AMove` x `BClear` | `LostAdvanceAnswered` violated | 2,451 | 1,152 | 1.9 | 7 states |
| `MCSetTableW5` | `AMove` x `BDelCreate` | `RowsHold` violated | 94 | 76 | 1.2 | 4 states |
| `MCSetTableW6` | `AMove` x `BMove` | `OnePlace` violated | 1,775 | 956 | 1.8 | 7 states |
| `MCSetTableW7` | `AAddRow` x `BClear` | `RestoreIsCurrent` violated | 1,238 | 612 | 1.6 | 7 states |
| `MCSetTableW7b` | three tables; `AAddRowT3` x `BClear` | `RestoreIsCurrent` violated | 1,238 | 612 | 1.5 | 7 states |
| `MCSetTableW8` | `AMove` x `BMove` | `FinishedIsApplied` violated | 4,392 | 2,155 | 2.1 | 8 states |
| `MCSetTableW9` | `AMove` x `BMove`, 1 beat | `RefusedIsFinal` violated | 124,818 | 45,563 | 12.0 | 12 states |
| `MCSetTableW10` | `AMove` x `BMove`, 1 new op | `PartOnce` violated | 4,361 | 2,274 | 2.2 | 10 states |
| `MCSetTableParts` | `AChunk` x `BClear` | `PartsWhole` violated (by the contract's scope) | 55,108 | 21,127 | 6.2 | 12 states |

The witnesses W1 to W9 and `MCSetTableParts` have 2 retries each, 1 crash, 1 in flight and 1 beat, as at revision 2; W10 has 2 retries each and 1 new op.

**The bench-size runs**, with the task's command: 8 workers, a 900 s cap, every contract property (`Settles` for the live run). The full instance is split by b's programs (`MCSetTableFullA` takes four of them, `MCSetTableFullB1` and `MCSetTableFullB2` two each) so that each part fits the cap; together they cover every program pair.

| config | instance | result | generated | distinct | depth | seconds |
|---|---|---|---|---|---|---|
| `settable-bench/MCSetTableFullA` | a's 5 programs x `BMove`, `BClear`, `BDelCreate`, `BCreate`; 1 retry each, 1 crash, 1 in flight, 1 beat, 1 new op, 1 fault | pass, every contract property | 61,278,169 | 15,755,813 | 36 | 807.9 |
| `settable-bench/MCSetTableFullB1` | a's 5 programs x `BReuse`, `BRemove`; the same bounds | pass, every contract property | 39,233,360 | 9,767,430 | 36 | 469.3 |
| `settable-bench/MCSetTableFullB2` | a's 5 programs x `BStay`, `BDelMove`; the same bounds | pass, every contract property | 54,525,753 | 13,689,511 | 36 | 625.8 |
| `settable-bench/MCSetTableFullRefused` | `AMove` x `BMove`; 2 retries each, 1 crash, 1 in flight, 1 beat, 1 new op, 1 fault (H2 trace A, rich) | pass, every contract property | 57,910,278 | 12,683,953 | 40 | 643.6 |
| `settable-bench/MCSetTableFullResume` | `AMove` x `BRemove`; the same bounds (H2 trace B and H3, rich) | pass, every contract property | 37,152,875 | 8,058,942 | 40 | 405.7 |
| `settable-bench/MCSetTableFullRestore` | three tables; `AAddRow`, `AAddRowT3` x `BClear`; the same bounds (H1, rich) | pass, every contract property | 12,927,722 | 3,150,966 | 39 | 180.5 |
| `settable-bench/MCSetTableFullEpochs` | `AClear`, `AMove` x `BClear`, epochs 0..2; the same bounds | pass, every contract property | 15,362,730 | 3,790,851 | 39 | 202.1 |
| `settable-bench/MCSetTableDirect` | `AMove` x `BRemove`; 2 retries each, 1 crash, 1 in flight, 1 beat; `DirectReplan` TRUE | pass, every contract property | 29,475,185 | 5,792,483 | 34 | 200.4 |
| `settable-bench/MCSetTableFullLive` | `AMove`, `AChunk` x `BMove`, `BClear`, `BReuse`; 2 retries each, 1 crash, 1 in flight, no beat; under fairness (revision 2's bounds) | pass, `Settles` | 9,025,017 | 2,142,155 | 33 | 325.1 |

Three runs hit the 900 s cap, and a timeout is not a pass:

- The full instance's second half as one run (a's 5 programs x `BReuse`, `BRemove`, `BStay`, `BDelMove`, the bounds of `MCSetTableFullA`): 901.9 s, 16,275,884 distinct explored and 1,743,107 on the queue, no violation found up to then. It is split into `MCSetTableFullB1` and `MCSetTableFullB2`, which both pass.
- `MCSetTableFullLive` with 1 new op and 1 fault added: 901.9 s, 4,093,466 distinct explored and 826,735 on the queue; the periodic liveness checks up to then found no violation.
- `MCSetTableDirect` with 1 new op and 1 fault added: 902.7 s, 19,306,174 distinct explored and 1,836,017 on the queue, no violation found up to then.

`SetTable.tla` is byte-identical (sha256 `a8200e02...`) for every run in this file. `MCSetTableFullA`, `FullRefused`, `FullResume`, `FullRestore`, `FullEpochs` and the three timed-out runs used `MCSetTable.tla` before the two menu definitions `MenuMainB1` and `MenuMainB2` were added to it; nothing they read changed. `MCSetTableFullB1`, `FullB2`, `FullLive`, `MCSetTableDirect`, the probes and the gated records used the committed file (sha256 `12c45e5f...`).

**The reachability probes** (each copies the named gated case; each is violated, so the path is reached):

| probe | copies | the path it shows is reached | trace |
|---|---|---|---|
| `MCSetTableReachFenceInFlight` | `MCSetTableRefused` | a fence settles `fenced` while the first copy is still in flight | 6 states |
| `MCSetTableReachLateFenced` | `MCSetTableRefused` | a late first copy replays `fenced` and writes nothing | 7 states |
| `MCSetTableReachFault` | `MCSetTableFaults` | a fence refused `FAULT` | 6 states |
| `MCSetTableReachNewOp` | `MCSetTableFaults` | a verb finishes with a part applied under a new op | 11 states |
| `MCSetTableReachResumeFenced` | `MCSetTableResume` | resume reads a `fenced` receipt after a crash | 8 states |
| `MCSetTableReachRowset` | `MCSetTableRestore` | a clear refused `ROWSET` | 7 states |
| `MCSetTableReachOccupied` | `MCSetTableOccupy` | a delete refused `OCCUPIED` by final occupancy (m1 left behind) | 7 states |
| `MCSetTableReachDeleteMoveOut` | `MCSetTableOccupy` | a delete applied with its last member moved out in the same step | 7 states |
| `MCSetTableReachNoopStay` | `MCSetTableOccupy` | a caller's no-op stay applied (revision unchanged) | 5 states |
| `MCSetTableReachFieldStay` | `MCSetTableOccupy` | a caller's field-changing stay applied (revision + 1) | 4 states |
| `MCSetTableReachReplayTwo` | `MCSetTableEpochs` | a resend at epoch 0 replayed from its receipt after two advances | 10 states |
| `MCSetTableReachFenceTwo` | `MCSetTableEpochs` | a fence at epoch 0 replayed `ok` after two advances | 10 states |

**The witnesses, read by hand.**

- **W1** (7 states). a and b both read `m1` at `r1:c1` and plan a move with a plan-time verdict of ok. a applies (`m1` to `r2:c1`). b applies on its plan-time verdict: `m1` is now in `r2:c1` and `r1:c2`, and b's place guard was false when it applied.
- **W2** (4 states). a's step moves `m1` in `t1` and `m2` in `t2`. The commit writes `t1` and stops: `m1` moves, `m2` does not, no receipt, and a hears an error.
- **W3** (7 states). a's move is applied and the reply is lost. a fences: the same epoch, op and intent, but other bytes (no entries). Matched by bytes, the receipt answers a's own applied identity `OPCONFLICT`. The fence is why the receipt must match the intent: it is by definition other bytes for the same identity.
- **W3b** (7 states). The same up to the fence. With the bytes in the identity, the fence finds no receipt, takes the fence path and overwrites the applied `ok` receipt with `fenced`.
- **W4** (4 states). The clear advances 0 to 1 and writes its receipt under epoch 1. **W4Lost** (7 states): the reply is lost, the same bytes are resent at request epoch 0, and the store answers `STALE` to an advance that was applied.
- **W5** (4 states). One step deletes row `r2` of `t1` and creates `m3` at `r2:c1`. It applies, and `m3` sits in a row the table does not have.
- **W6** (7 states). a and b both read `m1` at `r1:c1`. a's move to `r2:c1` is applied. b's move to `r1:c2`, planned from the earlier read with no revision and unchecked at the source, adds `m1` to `r1:c2`. `m1` is in two cells.
- **W7** (7 states). a plans "rows add `r2` to `t2`"; b reads for its clear (`t2` = `{r1}`); a's step is applied at epoch 0; b plans its clear from the read, with `rowset(t2, {r1})`; unchecked, the clear applies and epoch 1's `t2` lacks the row a was told it added. This is the revision-2 hole 1 trace, now a witness.
- **W7b** (7 states). Three tables. b reads for its clear: `t3` has no rows, so W7b's plan leaves out `t3`'s rowset. a adds `r1` to `t3` at epoch 0. The clear applies, and epoch 1's `t3` is empty. With `rowset(t3, rows:[])` the ZCARD of 1 against 0 names would refuse `ROWSET`.
- **W8** (8 states). a's move times out (the first copy is in flight). a fences; the fence wins and, broken, stores status `ok` (its fresh reply still says `fenced`). a crashes. Resume asks done, sees a match with status `ok`, and ends finished. `a1` was never applied, and the late copy can only replay. Section 5: "A crash after fencing is safe only because `done` and replay retain the `fenced` status".
- **W9** (12 states). a's move (no revision) times out; a resends the same bytes; b moves `m1` to `r1:c2`; a's resend is refused `PLACE`; a gives up without a fence and reports refused; the beat moves `m1` back to `r1:c1`; a's first copy runs and is applied under `a1`. This is the revision-2 hole 2 trace A, now a witness.
- **W10** (10 states). a's move times out. a asks done: absent. Broken, a takes the new op `a1.1`. The first copy `a1.0` runs and is applied (`m1` to `r2:c1`). a reads and plans under `a1.1`: `m1` is already there with field 1, so the plan is a no-op stay, and it is applied with its own `ok` receipt. The part is applied under two op identities. In this shortest trace the second application changes nothing; with a writer moving `m1` back in between, it would move `m1` twice.

## Findings

The three holes that the revision-2 model found are closed by revision 4. Each closure was checked twice: on a gated case that reaches the revision-2 trace with smaller bounds (`MCSetTableRestore`, `MCSetTableRefused`, `MCSetTableResume`, `MCSetTableReplan`, formerly failing goals, now passing contract cases), and on a bench-size case with revision 2's bounds for that trace or richer (`MCSetTableFullRestore`, `MCSetTableFullRefused`, `MCSetTableFullResume`, and `MCSetTableDirect` for the direct replan of hole 3). The unbroken model holds every contract property on every configuration listed. One counterexample on the unbroken model came up while this version was written. It was the model's error, and it is recorded below with the fix. No new hole in the contract was found.

### H1, closed: an advance guards the epoch it closes with rowset entries

Revision 2's trace (`MCSetTableRestore`, 7 states): a clear read the rows, a row was added at the old epoch, and the clear, planned from its read, left the row out of the new epoch. At revision 4 the clear carries one rowset guard per catalog table, before the advance, evaluated at the request epoch (section 3: "A prefix of rowset entries before advance is instead evaluated wholly at the request epoch"), and a complete clear guards every configured table, "including `rows:[]` for an initially empty table". In the model the same interleaving now refuses the clear `ROWSET` (probe `MCSetTableReachRowset`), and `RestoreIsCurrent` holds as a contract property on `MCSetTableRestore` (three tables, the empty `t3` included) and on `MCSetTableFullRestore`. W7 (the guard not evaluated) and W7b (the empty table's guard left out) each bring the trace back.

What the model does not check: ranks (a rowset here compares names), and carried member state (section 3 says a rowset "proves only row topology and ranks"; member freshness across a clear rests on errata 1's writer exclusion).

### H2, closed: an unknown outcome is settled only by ok, STALE, OPCONFLICT or fenced

Revision 2's traces: `MCSetTableRefused` (a resend refused `PLACE`, the caller reports refused, the first copy applies later) and `MCSetTableResume` (after a crash, done says absent, the first copy applies, the replan is refused `REQUEST`, the caller reports refused). At revision 4, section 5: "`done:absent` alone is **not** definitive after a dispatched unknown outcome", "A guard refusal, REQUEST or any refusal other than STALE or OPCONFLICT of a resend **or fence** does not settle it", and the resume caller "fences that identity before treating a refusal as final or allocating a new op". In the model:

- `GiveUp` is allowed only when nothing unresolved is behind the refusal. The same interleavings now end with a fence, which either replays the first copy's `ok` (the caller accepts) or records `fenced` (the late copy then replays `fenced` and writes nothing: probes `MCSetTableReachFenceInFlight`, `MCSetTableReachLateFenced`). `RefusedIsFinal` holds as a contract property on both configurations and on their bench-size versions.
- The status survives a crash: `FinishedIsApplied` holds with a crash after the fence (probe `MCSetTableReachResumeFenced`); W8 shows what storing `ok` would do.
- A fenced part is replanned under a new op, never an unresolved one: `PartOnce` holds; W10 shows the new op after `done:absent`.
- A fence refused with a code other than STALE or OPCONFLICT settles nothing: the `FAULT` event leaves the identity unresolved, and the caller keeps recovering (probe `MCSetTableReachFault`).
- A fence after two advances replays the saved result (probe `MCSetTableReachFenceTwo`); a fence for an identity never applied, after the epoch moved, is `STALE`, and the first copy is `STALE` too.

### H3, closed: a fresh replan is answered from the receipt only after done

Revision 2's trace (`MCSetTableReplan`, 10 states): an applied remove, a lost reply, a replan from a fresh read with no source cell, `REQUEST`, and the caller reported refused. Section 2 now says "a fresh replan is answered from the receipt only **after done**". In the model the contract's path (`DirectReplan` FALSE) asks done first; when done says `ok` the part is done, and when it says absent while a copy is in flight, a statically invalid replan is refused `REQUEST` without settling, and the fence settles it. `ReceiptTruth` is narrowed to resent bytes, a plan made after done, and a fence. `MCSetTableReplan` passes. `MCSetTableDirect` (bench) runs the direct path (`DirectReplan` TRUE) on the same programs: it passes every contract property (5,792,483 distinct states). So on the direct path too, a statically invalid replan of an unresolved identity cannot end the verb refused: the settlement rule and the fence close hole 3, and done-first is what makes the answer come from the receipt rather than from a fence.

### The one counterexample on the unbroken model: the model's error, fixed

The first runs of `MCSetTableFaults`, `MCSetTableRefused`, `MCSetTableResume` and `MCSetTableReplan` violated `ReceiptTruth` in 7 states: a's move is applied, the reply is lost, a fences, and the fence is refused by the outside fault. `ReceiptTruth` demanded a replay. Checked against the contract: section 8's phase order puts "definitions/engine" before "original receipt intent replay/conflict", so ENGINE or CONFIG can refuse a request whose identity has a receipt, and the contract's replay claim covers only a request that passes the phases before the receipt. The model's premise ("statically valid") stood for exactly those phases and did not cover codes the model does not compute. The model was wrong: the fault now has its own code, `FAULT`, outside the premises of `ReceiptTruth` and `ConflictOnChangedIntent`. The contract is consistent here, and the settlement rule already says such a refused fence settles nothing.

### The revision-3 items

- **Final occupancy.** `OCCUPIED` counts a deleted row's pre-state members minus the members the same step moves out or removes; a changed stay into a deleted row is left to its entry's `ROWCONFLICT`. `BDelMove` reaches both outcomes: the delete applies when `m1` was the last member of `r2` and moves out (probe `MCSetTableReachDeleteMoveOut`), and refuses `OCCUPIED` when the plan is stale and `m1` is left behind (probe `MCSetTableReachOccupied`). `RowsHold` holds throughout.
- **Stays made by the plan.** A move with no destination is a stay in the member's own cell. `BStay` reaches a field-changing stay (revision + 1) and a no-op stay (revision unchanged) (probes `MCSetTableReachFieldStay`, `MCSetTableReachNoopStay`), and `RevisionMoves` holds over both.

### Finding 4, by the contract's stated scope: a chunked verb is not atomic across an advance

`MCSetTableParts`, 12 states: a's part a1 is applied at epoch 0 and b's clear advances the epoch; a's part a2, at request epoch 0, is refused `STALE`; the verb ends stale with a1 applied. Section 5: "This contract guarantees each identified atomic part, not a whole multi-part transaction". Not a hole; the verb layer owns the repair.

### Observation, carried: the source-cell probe is not what keeps one place

`MCSetTableW6Cell` drops only the source-cell probe and keeps the record's place check. `OnePlace` and `GuardsAtApply` hold (7,420 distinct states). Under the exclusive supported writer the record check alone keeps one place; the probe finds drift from raw writes, which section 2 puts outside the guarantee.

### Stated gaps

- **Two advances** are checked only on `MCSetTableEpochs` (`AClear` x `BClear`) and `MCSetTableFullEpochs` (`AClear`, `AMove` x `BClear`, 2 retries each, 1 crash, 1 beat, 1 new op, 1 fault), not on the full instance, whose menu has one clear program.
- **Ranks**, **preplans and the seal**, **Layer 2**, and **carried member state** are not modelled (above).
- `Settles` is checked without the beat, and without new ops or faults. A liveness run with the beat was not attempted at revision 4 (at revision 2 it did not finish inside 900 s); with a new op and a fault added, the bench-size liveness run did not finish inside the cap (above). Retries, crashes, new ops and faults are all bounded, so the termination argument does not depend on them.
