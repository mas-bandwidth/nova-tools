# SetTable: Layer 1 of the sprint foundation, the table, as a TLA+ model

`SetTable.tla` is the state machine of Layer 1 (`tset/1`), written from its contract, `design/L1-CONTRACT.md` (the Layer 1 table contract, contract revision 2), a review candidate that is not yet confirmed for implementation. The model was written before any `tset` code, so it checks the contract and nothing else. `MCSetTable.tla` holds the small instance and the callers' programs. Every configuration named below is a file in `tla/` or in `tla/settable-bench/`.

## What is modelled

**The state Layer 1 owns.**

- `rec[t][m]`: the record of each member (stored ID) in each table. It holds whether the record exists, its epoch, its revision, its place (a cell, or none) and one application field.
- `cells[t][e][x]`: the sorted set of each cell at each epoch.
- `rows[t][e]`: the rows of each table at each epoch.
- `ep`: the active epoch.
- `done[<<e, op>>]`: the receipts, keyed by request epoch and op. Each one holds the digest the store matches (the intent) and the recorded result.

**The in-flight state of each caller.**

- `prog`: the caller's program, one or more parts. Each part is one atomic step with its own op.
- `pc` and `part`: where the caller is.
- `oe`: the verb's original epoch. It is fixed at the verb's first read and kept across retries and crashes, as the resume manifest keeps it.
- `snap`: what the caller read.
- `req`: the request it planned.
- `rep`: the reply it has, if any.
- `tries`: its retries so far.
- `late`: a request it gave up waiting for, which may still be executed.

**Two ghosts that exist only for the properties.**

- `hist`: every application of an identity so far.
- `last`: the store step just taken. Every other action resets it.

**Actions.**

| action | what it is |
|---|---|
| `Read(c)` | an atomic planning read of the named records, the active epoch and, for a clear, the rows of every table. The verb's first read fixes its original epoch |
| `Plan(c)` | builds the request from what was read. A move or remove takes its expected place from the read, and takes its expected revision from the read or names none (revisions are optional). A count guard can be added. For a clear, the plan is an advance plus one rows entry per table that restores every row read |
| `Apply(c)`, `Refuse(c)`, `Replay(c)` | the store executes the request in one atomic step, in the contract's phase order. First static validity: a move or remove with no source cell is `REQUEST`. Then the receipt: a matching intent replays, a different intent is `OPCONFLICT`. Then `EPOCHAHEAD`, `STALE`, `ADVANCE` and `OVERFLOW` (no successor epoch). Then topology: `ROWCONFLICT`, `OCCUPIED`, `DRIFT` of new-epoch keys. Then every member and cell guard against the state at apply time: `EXISTS`, `MISSING`, `MEMBEREPOCH`, `PLACE`, `NOROW`, `DRIFT`, `REVISION`, `CELLFULL`. Then every write, with the receipt at the request epoch |
| `LoseReply(c)` | the store ran the step and the reply never arrived (OUTCOMEUNKNOWN) |
| `Timeout(c)`, `LateRun(c)` | cancellation after dispatch: the caller stops waiting, and the request it sent can still be run later, with its reply going nowhere |
| `RetrySame(c)`, `RetryFresh(c)`, `Rebuild(c)` | the same op and intent again. RetrySame resends the same bytes. RetryFresh reads again and plans a new request without asking `done` first; the constant `DirectReplan` switches it off, which leaves only the contract's two paths: the same bytes again, or `done` first on resume. Rebuild reads again after a refusal |
| `Accept`, `Stale`, `Conflict`, `GiveUp`, `LostCause` | the caller's side of a reply: next part or finished; ended `stale`, `conflict` or `refused`; `lost` when its retries are used up |
| `Crash(c)`, `Resume(c)` | the caller's process dies. What it had in memory is gone. The manifest and anything left in flight survive. It resumes by asking `done` for every part identity and continuing at the first part that is not done |
| `Beat` | a second supported writer outside the programs, like the sprint's beat. It is one guarded step with no op, read and applied at once, between any caller's read and apply. The other caller's steps are also a second writer |
| `Quiet` | every caller has ended. This is rest, not deadlock, so TLC's deadlock check stays on |

**The contract's guarantees, stated apart from the store's own check.** A witness that changes the store must be caught by a property that does not share its code. Each row gives the contract's guarantee and the property that checks it.

| property | kind | contract |
|---|---|---|
| `OnePlace` | invariant | one placement per member: at most one cell per table, only in its own epoch, exactly where its record says |
| `RowsHold` | invariant | no member in a cell of a row the table does not have (in an extant final row) |
| `RefusedWritesNothing` | action | a refusal changes no key |
| `AppliedIsWhole` | action | a step that changed the store applied every entry, and its receipt |
| `RevisionMoves` | action | a member's revision moves by exactly one in a step that changes it, never on a no-op |
| `GuardsAtApply` | action | no applied step had a guard that was false at apply time (guards per member and per cell, never a table revision) |
| `ReceiptTruth` | action | at most one application per identity. A receipt is its own step's result. A reply's result is the application's. A statically valid request whose identity was applied with the same intent, however it was planned, is answered with the recorded result and writes nothing |
| `ConflictOnChangedIntent` | action | a receipt with another intent refuses `OPCONFLICT` and writes nothing |
| `EpochSafe` | action | the epoch moves forward only by an applied advance. A step writes only at the epoch it names, and its receipt only at its request epoch. An applied identity is never answered `STALE`, so a lost reply at an advance is answered from the receipt |
| `Settles` | temporal | every caller comes to an end, under fairness of what callers and the store owe |

**Goals the contract cannot secure, or disclaims.** Each is expected to fail on the unbroken model, and each failure is written up under Findings.

| property | kind | goal |
|---|---|---|
| `RestoreIsCurrent` | action | a clear that restores every row restores the rows the closing epoch has when it closes |
| `RefusedIsFinal` | invariant | a verb that reported a refusal never has that part applied, then or later |
| `PartsWhole` | invariant | a verb that ended `STALE` has none of its parts applied |

## What is not modelled

- **Sizes, budgets and limits.** LIMIT, BUDGET, argv pieces, bytes and the 2,000-member ceiling are not modelled.
- **Time.** The 25 ms gate, TIME and `now_ms` are not modelled.
- **Redis itself.** Key types, ACL, argument splitting, the score parser and runtime errors are not modelled. The contract's rule that prepare finds every returnable error before the first write is assumed here, not proved: the store of the contract never half-applies. Witness W2 shows what happens when it does. Whether the code's prepare is complete is for the whole-store tests of section 1.6.
- **The log and history (Layer 2).** Lines, seqs, `about`, cardlines, LOGID and notes are not modelled.
- **Reads.** The only reads are the planning snapshot and the `done` query of the resume path. Range, count, rcount, pagination and cursors are not modelled.
- **Scores.** One application field stands for every effective change (placement, score or field).
- **The Go twin, `Steps` pipelining, and all static request checks but one.** TWICE, FIELDNAME, the other REQUEST shapes and duplicate JSON names are not modelled. The one static check that is modelled is a move or remove with no source cell.
- **Caller steps with no op.** Only the outside beat is op-less.
- **Rows in `before`, definitions, and the definition snapshot at advance.**
- **More than one advance and more than two tables.** The instance has one advance and two tables.

## The instance

It uses 2 tables (`t1`, `t2`), 2 rows (`r1`, `r2`) and 2 columns (`c1`, `c2`), 3 members (`m1`, `m2`, `m3`), 2 callers (`a`, `b`) and field values 0 and 1. Epochs are 0 and 1, which allows at most 1 advance.

- **Epoch 0.** `t1` has rows `r1` and `r2`, and `t2` has `r1` only. `m1` is in `t1` at `r1:c1` and in `t2` at `r1:c2`: one primary in two tables. `m2` is in `t2` at `r1:c1`, and `m3` is nowhere yet.
- **Bench-size bounds.** At most 2 retries per caller, 1 crash, 1 request left in flight, and 1 beat. The beat either moves `m1` back to `t1` `r1:c1` or changes `m2`'s field.

The programs (`MCSetTable.tla`):

| program | parts |
|---|---|
| `AMove` | a1: move `t1` `m1` to `r2:c1`, field 1 |
| `ATwo` | a1: the same move, and `t2` `m2` to `r1:c2`, in one step over two tables |
| `AChunk` | a1: the `t1` move; a2: the `t2` move (a chunked verb, two parts) |
| `ACap` | a1: move `t1` `m1` to `r2:c2` guarded by count(`r2:c2`) <= 0 |
| `AAddRow` | a1: rows add `r2` to `t2` |
| `BMove` | b1: move `t1` `m1` to `r1:c2`, field 0 (races a on `m1`) |
| `BClear` | b1: advance from the original epoch, restoring every row read |
| `BDelCreate` | b1: rows delete `r2` of `t1` and create `m3` at `r2:c1`, one step |
| `BCreate` | b1: create `t1` `m3` at `r2:c2` |
| `BReuse` | a1 (a's op, another intent): move `t1` `m1` to `r1:c2` |
| `BRemove` | b1: remove `t1` `m1`, field 1 |

## Reversed witnesses

`Broken` names the one rule a witness breaks. Every other rule stays the contract's.

| Broken | the rule broken |
|---|---|
| `W1` | guards checked at plan time only: the plan asks the store for a verdict, and apply writes on that verdict without checking again. The receipt and epoch checks still run at apply |
| `W2` | the commit can stop after the first table's writes, as a runtime error mid-commit would (the tracer's red probes). It writes no receipt and replies with an error of unknown outcome |
| `W3` | the receipt matches the request's bytes (epoch, op, intent, entries), not the intent |
| `W3b` | the receipt identity includes the request's bytes, so a replanned request finds no receipt |
| `W4` | an advance's receipt is written in the namespace of the epoch it moved to, while lookup stays at the request epoch |
| `W5` | a create's destination is checked against the pre-state rows plus additions, ignoring deletions in the same step |
| `W6` | a move or remove checks neither the record's place nor the source cell. Only a given revision is checked |
| `W6cell` | only the source-cell probe is dropped and the record's place is still checked. This is expected to hold (see the observation under Findings) |

## Running it

TLC runs only on a bench, never on a working machine. Each run goes in its own directory, with its own `java.io.tmpdir` and `-metadir`.

**The gated cases** live in `tla/` and are declared in `CASES.tsv` in four groups: `settable`, `settable-faults`, `settable-witnesses` and `settable-goals`. They are run and recorded by `tlacheck` like every other model. A pass case runs on two workers and a counterexample case on one, and each group has a 110 s budget. The records are in `RUNS.tsv`. After a model edit, `tlacheck groups --stale` names the groups to run again (see `README.md`):

```sh
for g in settable settable-faults settable-witnesses settable-goals; do
  go run ./tools/tlacheck run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-$g --group $g
done
```

**The bench-size runs** live in `tla/settable-bench/`. They use the full instance, and their state spaces are too large for the gate's two workers and 110 s. That puts them outside `CASES.tsv`, which covers `tla/MC*.cfg` only. Run them from `tla/`, each in its own directory:

```sh
cd tla
for c in MCSetTableFull MCSetTableFullLive; do
  mkdir -p /tmp/tlc-$c
  timeout 900 java -XX:+UseParallelGC -Djava.io.tmpdir=/tmp/tlc-$c -cp /path/to/tla2tools.jar \
    tlc2.TLC -workers 8 -metadir /tmp/tlc-$c/states -config settable-bench/$c.cfg MCSetTable.tla > /tmp/tlc-$c/$c.log 2>&1
done
```

TLC prints every variable in a counterexample. The ones to read are:

- `last`: the store step just taken, with who ran it, its outcome, its code and the request.
- `pc`, `req` and `late`: the callers.
- `cells`, `rec`, `rows` and `done`: the store.

## Results

All runs used TLC 2.19 (`tla2tools.jar` sha256 `936a2620...`) on Java 21.0.12.1, on a 32-core Linux bench, on 2026-09-30 (UTC).

**The bench-size runs.** These use the full instance and the task's command: 8 workers, a 900 s cap.

| config | programs | result | generated | distinct | depth | seconds |
|---|---|---|---|---|---|---|
| `settable-bench/MCSetTableFull` | every pair: 5 for a, 6 for b; 2 retries, 1 crash, 1 in flight, 1 beat | pass, every contract property | 97,617,232 | 21,752,468 | 34 | 544.1 |
| `settable-bench/MCSetTableFullLive` | `AMove`, `AChunk` x `BMove`, `BClear`, `BReuse`; 2 retries, 1 crash, 1 in flight, no beat; under fairness | pass, `Settles` | 5,453,977 | 1,298,791 | 33 | 137.2 |

`MCSetTableFull` checks every contract property: `TypeOK`, `OnePlace` and `RowsHold` as invariants, and `RefusedWritesNothing`, `AppliedIsWhole`, `RevisionMoves`, `GuardsAtApply`, `ReceiptTruth`, `ConflictOnChangedIntent` and `EpochSafe` as action properties. It finished inside 10 minutes, so it is not split into groups. Splitting it by property would not shrink it anyway: TLC explores the whole reachable graph whatever it checks.

`MCSetTableFullLive` runs without the beat. With the beat (1 beat, the rest the same), it did not finish inside the 900 s cap, and a timeout is not a pass:

- With the task's command, it timed out with 6,849,808 distinct states explored and 689,653 still on the queue.
- With `-lncheck final`, the exploration completed (39,118,238 generated, 8,643,471 distinct), but the final liveness check did not finish before the cap.

The beat is an outside event and is not under fairness. Callers' retries and crashes are bounded whether it runs or not, so dropping it leaves the termination argument the same.

**The gated cases.** These are recorded in `RUNS.tsv` by `tlacheck`. Pass cases run on two workers, with two JVM processors and a 2 GB heap; counterexample cases run on one worker.

| config | instance | result | generated | distinct | seconds | trace |
|---|---|---|---|---|---|---|
| `MCSetTable` | every program pair; 1 retry, no crash, 1 left in flight, no beat | pass, every contract property | 802,270 | 249,257 | 24.1 | |
| `MCSetTableFaults` | `AMove` x `BMove`; 1 retry, 1 crash, 1 in flight, 1 beat | pass, every contract property | 2,058,299 | 507,056 | 55.1 | |
| `MCSetTableLive` | the liveness menu; 1 retry, 1 in flight, under fairness | pass, `Settles` | 298,886 | 92,096 | 27.5 | |
| `MCSetTableW6Cell` | `AMove` x `BMove`; 2 retries, nothing else | pass, `OnePlace`, `GuardsAtApply` | 27,437 | 9,128 | 2.0 | |
| `MCSetTableW1` | `AMove` x `BMove` | `GuardsAtApply` violated | 1,639 | 866 | 1.5 | 7 states |
| `MCSetTableW2` | `ATwo` x `BMove` | `AppliedIsWhole` violated | 39 | 35 | 1.1 | 4 states |
| `MCSetTableW3` | `AMove` x `BMove` | `ReceiptTruth` violated | 8,275 | 3,785 | 2.2 | 9 states |
| `MCSetTableW3b` | `AMove` x `BMove` | `ReceiptTruth` violated | 8,275 | 3,785 | 2.4 | 9 states |
| `MCSetTableW4` | `AMove` x `BClear` | `EpochSafe` violated | 94 | 76 | 1.0 | 4 states |
| `MCSetTableW4Lost` | `AMove` x `BClear` | `LostAdvanceAnswered` violated | 2,132 | 957 | 1.6 | 7 states |
| `MCSetTableW5` | `AMove` x `BDelCreate` | `RowsHold` violated | 94 | 76 | 1.1 | 4 states |
| `MCSetTableW6` | `AMove` x `BMove` | `OnePlace` violated | 1,611 | 846 | 1.5 | 7 states |
| `MCSetTableRestore` | `AAddRow` x `BClear` | `RestoreIsCurrent` violated (finding 1) | 1,105 | 523 | 1.4 | 7 states |
| `MCSetTableRefused` | `AMove` x `BMove`, beat | `RefusedIsFinal` violated (finding 2) | 84,984 | 30,883 | 5.2 | 12 states |
| `MCSetTableResume` | `AMove` x `BRemove`, no direct replan | `RefusedIsFinal` violated (finding 2) | 42,414 | 16,042 | 4.4 | 11 states |
| `MCSetTableReplan` | `AMove` x `BRemove` | `RefusedIsFinal` violated (finding 3) | 29,951 | 11,292 | 3.0 | 10 states |
| `MCSetTableParts` | `AChunk` x `BClear` | `PartsWhole` violated (by design) | 31,665 | 11,820 | 4.0 | 12 states |

Every witness and goal was also run with the task's command (8 workers, 900 s cap). Each failed with the same property, with the same trace length. The one exception is `MCSetTableW6Cell`, which passed with 27,437 generated and 9,128 distinct states. With 8 workers, the number of states found before a violation varies from run to run. The trace lengths came out the same.

**The witnesses, read by hand.**

- **W1** (7 states). a and b both read `m1` at `r1:c1` and plan a move, each with a verdict of ok at plan time. a applies (`m1` to `r2:c1`). b applies on its plan-time verdict: `ZREM r1:c1` is a no-op and `ZADD r1:c2` succeeds. `m1` is now in two cells, and b's place guard was false when it applied.
- **W2** (4 states). a's step moves `m1` in `t1` and `m2` in `t2`. The commit writes `t1` and stops, so `m1` moves while `m2` does not, no receipt is written, and a hears an error. This is the tracer's P3b and P4 shape.
- **W3** (9 states). a's move is applied and the reply is lost. a reads again: `m1` is at `r2:c1` with rev 2. a replans with the same intent, which yields different bytes. The store refuses `OPCONFLICT` instead of returning the recorded result.
- **W3b** (9 states). The same up to the replan. The replan finds no receipt and is applied a second time, as a no-op with `changed 0`. The caller gets a result that is not the recorded one.
- **W4** (4 states). The clear advances 0 to 1 and writes its receipt under epoch 1. **W4Lost** (7 states): the reply is lost, the same bytes are resent at request epoch 0, and the store finds no receipt at epoch 0 and answers `STALE` to an advance that was applied.
- **W5** (4 states). One step deletes row `r2` of `t1` and creates `m3` at `r2:c1`. It applies, and `m3` sits in a row the table does not have.
- **W6** (7 states). a and b both read `m1` at `r1:c1`. a's move to `r2:c1` is applied. b's move to `r1:c2` was planned from the earlier read with no revision. Unchecked at the source, it removes `m1` from `r1:c1`, where it no longer is, and adds it to `r1:c2`. `m1` is now in both `r2:c1` and `r1:c2`.

## Findings

The unbroken model holds every guarantee of the contract on the full instance. Everything below comes from the goal configurations, and each trace was checked by hand against the contract's text. Findings 1 to 3 are holes in the contract. Finding 4 is its stated scope. The last is an observation.

### 1. HOLE: an advance cannot guard the epoch it closes, so a row added at the old epoch while a clear is in flight is silently left out of the new epoch

Trace, `MCSetTableRestore`, 7 states, unbroken model:

1. Initial state: epoch 0, and `t2` has the rows `{r1}`.
2. a reads (`AAddRow`).
3. a plans `[ep0 a1: rows(t2 +r2)]`.
4. b reads (`BClear`): `t1` rows `{r1,r2}`, `t2` rows `{r1}`.
5. a's step is applied: `t2` at epoch 0 is now `{r1,r2}`. a has its receipt and its reply ok.
6. b plans from its read: `[ep0 b1: advance(from 0); rows(t1 +r1,r2); rows(t2 +r1)]`.
7. b's step is applied: epoch 1, where `t2` has `{r1}` only. The row a was told it added is not in the active epoch.

Checked against the contract:

- **Every later entry works at the new epoch.** The section 3 table says advance is "At most one, first entry … Following data entries use next epoch".
- **So no guard in the step can see the old epoch.**
  - Section 3: "Pre_rows for post-advance entries is empty at the new epoch" and "All existing-member sources and count/rcount cells must exist in pre_rows". So a count or rcount guard in an advance step refuses `NOROW`, whatever it names.
  - A guard on an old-epoch record refuses `MEMBEREPOCH`, because the record must belong to `write_epoch`.
  - No entry kind guards a table's row set, and `S.zguard` "does not add a Layer 1 entry kind".
- **The contract leaves completeness to the caller.** It says "The caller supplies the complete restoration list" and "No scan over old rows or implicit restoration is hidden in advance". It never says how a caller can make that list complete at the moment of commit.
- **Section 5 covers the receipt, not the effect.** When a same-epoch call wins the race with clear, "its receipt and old-epoch history remain". Nothing carries its effect forward.

A row deleted at the old epoch inside the same window is restored anyway, by the same mechanism, though that was not run. The same holds for any member state that a clear carries into the new epoch by create entries built from its read: that case is argued from the text and was not modelled. Two ways to close it:

- Allow guard entries before the advance entry, evaluated at the request epoch. One example is a rows guard ("these are exactly `t`'s rows"): ZCARD plus ZMSCORE of the named rows, bounded by the 1,024-row limit, with no scan. Member guards would work the same way.
- Or state, above Layer 1, that clear excludes every other writer from its read to its commit, and model that layer.

### 2. HOLE: OUTCOMEUNKNOWN has no settle rule, so a later refusal is not final while an earlier copy of the request may still run

Trace, `MCSetTableRefused`, 12 states (the 8-worker run found the same trace with the callers' roles swapped):

1. Initial state.
2. a reads.
3. a plans `[ep0 a1: move t1 m1 r1:c1 -> r2:c1]` with no expected revision.
4. a times out; the request is still in flight.
5. a resends the same bytes, as the contract advises.
6. b reads.
7. b plans.
8. b's move is applied: `m1` is now at `r1:c2`.
9. a's resend is refused `PLACE`, "nothing was changed".
10. a gives up and reports refused.
11. The beat moves `m1` back to `r1:c1`.
12. a's first copy runs: the place guard holds and no revision was named, so it is applied and receipt `(0, a1)` is written.

Trace, `MCSetTableResume`, 11 states. This one uses only the contract's own paths: no direct replan, and done first on resume.

1. Initial state.
2. b reads.
3. b plans `remove(t1 m1 from r1:c1)`.
4. b times out; the request is in flight.
5. b crashes.
6. Resume: `done` says absent.
7. The first copy runs and is applied: `m1` is removed and receipt `(0, b1)` is written.
8. b reads: `m1` has no place.
9. b's replan has no source cell.
10. The replan is refused `REQUEST`. Static validity comes before the receipt, so the receipt check never runs.
11. b gives up and reports refused.

Checked against the contract:

- **The contract says the outcome is unknown and hands the caller the bytes.** Section 8: "cancellation after dispatch or connection loss → OUTCOMEUNKNOWN, with exact request bytes retained for recovery". Section 1.4: "Save request bytes across retries", and a refusal says "nothing was changed".
- **"Nothing was changed" is true of the call, not of the op.** Redis runs a command whose bytes have reached it even after the client has given up, and a retry on a new connection can run before it.
- **The done-to-commit race is closed only for a part that is submitted and passes static validation.** Section 5 says "Every submitted part still checks its receipt atomically, closing the race between done and commit". In the Resume trace the replanned part never reaches the receipt check.
- **Revisions narrow the hole but do not close it.** A revision only grows, so a revision guard that failed once fails for ever. Count, rcount, head and row guards, and the place guard of a plan with no revision, can become true again.

What settles an identity after OUTCOMEUNKNOWN, by the contract's own rules:

- ok or replay for it;
- `STALE`: the epoch never returns, so an earlier copy is `STALE` too;
- `OPCONFLICT`: receipts are permanent.

A guard refusal or a static `REQUEST` does not settle it. Two ways to close it:

- State that rule in sections 1.5 and 5.
- Name a fence. An op-carrying step with the same intent and no member effects ("an empty guarded/no-op request may record a receipt") writes the receipt, and the earlier copy then replays and writes nothing. This fence is inferred from the text and was not run in the model.

### 3. HOLE (narrower): "including a fresh replan" is false for a remove, and for a move whose member has lost its place

Trace, `MCSetTableReplan`, 10 states:

1. Initial state.
2. b reads.
3. b plans `remove(t1 m1 from r1:c1)`.
4. b's remove is applied.
5. b loses the reply.
6. b retries from a fresh read.
7. b reads: `m1` has no place.
8. b plans `remove(t1 m1 from -)`.
9. The store refuses `REQUEST`.
10. b gives up.

Checked against the contract:

- **The guarantee claims fresh replans.** The section 2 row says "Same op and stable intent apply at most once, including a fresh replan". At most once holds.
- **But a fresh replan of an applied remove is never answered from the receipt.**
  - `from` is an observation: it is where the member was when read, and the intent excludes what was read (section 5). The applied remove erased that place.
  - `from` is required and cannot be null (sections 1.2 and 3), so the replan is statically invalid.
  - Static validation runs before the receipt (section 8's phase order; section 3: "Static request validity is still checked on replay").
- **The done-first rule of section 4 is what saves the caller**: "fresh CLI invocations … consult batched done before replanning". But it saves it only when no earlier copy is in flight (finding 2).

Two ways to close it: make the section 2 row say "after done", and add a remove to `TestReplannedIntentReplayAcrossAdvance`. Or let a move or remove be replayed from its receipt before static checks of observation-derived fields.

### 4. By the contract's stated scope: a chunked verb is not atomic across an advance

Trace, `MCSetTableParts`, 12 states. a's part a1 is applied at epoch 0 and b's clear advances the epoch. a's part a2, at request epoch 0, is refused `STALE`. The verb ends stale with a1 applied. Section 5 says so: "This contract guarantees each identified atomic part, not a whole multi-part transaction". This is not a hole. It is recorded because the verb layer owns the repair, for example a stop before clear, or a verb-level redo at the new epoch.

### Observation: the source-cell probe is not what keeps one place

`MCSetTableW6Cell` drops only the source-cell probe (the ZSCORE of `from`) and keeps the record's place check. `OnePlace` and `GuardsAtApply` hold over the whole state space (9,128 distinct states, no error).

Under the exclusive supported writer, the record check alone keeps one place. The probe only finds drift from raw writes, which section 2 puts outside the guarantee, and it costs one ZSCORE per member. Section 2's "a point guard reads the member record and its indicated source-cell score" stays right as a drift check. The model does not show it to be needed.
