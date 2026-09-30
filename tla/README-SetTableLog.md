# SetTableLog: Layer 2 of the sprint foundation, the log, as a TLA+ model

`SetTableLog.tla` is the state machine of Layer 2 (`tset/1`, the log), written from the Layer 2 log contract, `design/L2-CONTRACT.md`, at **contract revision 1** (a review candidate) with the coordinator's decisions on it (`rowan-3c6ca2c34063`), composed with **Layer 1 revision 4** (`design/L1-CONTRACT.md`, the published revision-4 pin as amended, sha256 `e055d64d0f6fc6e4d9bc587bf3a6789c1cacb28cbd6d4fda62a760459907973e`) by its section 1 and the Layer 2 alignments of its section 10. Of those nine alignments the model has four: the pair cursor (4), the fence returning before `L.plan` with no line (5), the guard-only rowset with no line (6), and the notes op rule for caller notes (9, without the preplan exemption, since there is no preplan here). It also has revision 3's final occupancy. The other revision-4 items are in the list under **Not modelled**, which is the same list as in the module header and the pull request. Where Layer 1 revision 4 changes what Layer 2 revision 1 says, the model follows Layer 1 revision 4; Layer 2 revision 2 is owed. `MCSetTableLog.tla` holds the small instance and the callers' programs; every configuration named below is a file in `tla/`.

## What is modelled

The log is written in the same commit as the table plan: one stored line per emitting entry and one per note, each with the stream id `<seq>-0` the plan allocated from one XINFO reading (`last-generated-id` must be exactly `<entries-added>-0`, within the live ceiling), and a history list per card (primary) holding the sequences of the lines that name it, deduplicated per line. A refusal writes nothing. Reads page with a cursor. Replay folds the log back into tables and compares them with the store.

**State.** The tables as far as the log needs them (the active epoch, rows per epoch, one record per stored ID with its place, score, one application field and revision; a cell is the record's place and score); the receipts, keyed by request epoch and op, with their status (`ok` or `fenced`); the log stream of each epoch with its XINFO metadata; the history lists; the callers (program, part, request epoch, reply, and whether the part's request is now its fence); one paged reader; one replay.

**Actions.** `Step` and `Advance` (the tables, the lines, the history appends and the receipt, in one action), `Fence` (a winning fence: its receipt only), `RefusedStep`, `PrepareRefused` (type, ACL and budget refusals, for a step or a fence), `ReceiptReplay` (with the saved status), `ErrorBetween` (fact F1: a runtime error after the table commands and before the log commands), `LostReply`, `Retry` (the same bytes), `FenceIt` (the caller fences an unknown outcome: the same original epoch and op, no entries, no notes), the reader (`ReadStart`, `ReadLines`, `ReadCards`), the replay (`RpStart`, `RpPage`, `RpCompare`), and raw writes by something that is not the supported writer (`XADD *` on the active epoch's log key or on the next epoch's, `XDEL`, `DEL`).

The store's checks follow the phase order of Layer 1 section 8: static validity (a fence carries no entries and no notes; a step with caller notes and no op is `REQUEST`), the receipt (`REPLAY`), the epoch (`STALE`), the fence's own path (it returns here, before any planner), the advance's successor (`OVERFLOW`), the rowset guards at the request epoch (`ROWSET`), topology (`ROWCONFLICT`, then `OCCUPIED` by final occupancy), member guards (`EXISTS`, `MISSING`, `MEMBEREPOCH`, `PLACE`, `ROWCONFLICT` for a create or move into a deleted row and for a changed stay in one, `NOROW`), then the log plan (`LIMIT` over the ids of one line, `DRIFT` for an advance whose new-epoch log key exists, `LOGID` for a foreign head, `OVERFLOW` at the sequence ceiling), then prepare.

**Cardlines.** An item is one `(line, member id)` projection: a member line gives the card one item per id whose about is that card, and a note that names the card gives one item. Each slot's position is the pair (list index, item index within that line); a page's limit counts items across slots; a position moves only for complete emitted items, so a page may end inside a line and the next page resumes at the exact next item. The instance's `BShared` line names `m1` and `m2`, both about `p1`: one history append for `p1`, two items. `MCSetTableLogCursor` keeps `PageLimit = 2`, and a page may return 1 or 2 items, so pages that end inside that line are in the run (a bench probe reaches one).

## Properties

| property | kind | what it says |
|---|---|---|
| `SeqGapless` | invariant | line `q` of an epoch has id `q-0`; entries-added is the count and last-generated-id the last seq; the key exists exactly when a line does |
| `ReplayEqualsStore` | invariant | the fold of each epoch's log equals that epoch's tables (records, cells, rows) |
| `HistoryExact` | invariant | each card's history list is exactly the seqs of the lines that name it, in order |
| `LineBound` | invariant | a line names at most `IdCap` ids |
| `SeqCeiling` | invariant | no live log passes the sequence ceiling |
| `RetryWritesOnce` | invariant | a caller request's lines are written by one store call (every note here is a caller's) |
| `ReplySeqsTrue` | invariant | a reply's first..last seq name exactly the lines that request wrote |
| `CursorNeitherSkipsNorRepeats` | invariant | a lines chain emits a prefix of the seqs below its high-water, all of them when done; a cardlines slot emits a prefix of the `(seq, member)` items it owes (computed from the log, not from the history list), all of them when exhausted, and a slot not exhausted points at exactly the next item it owes |
| `ReplayVerdictSound` | invariant | a replay says `differ` only when the log and the tables really disagree |
| `OneLinePerChange` | action | an accepted step writes one line per entry that changed something (read from the tables before and after), in input order and of its kind, a member line naming exactly the ids whose records changed, then one line per note; a guard and a guard-only rowset write none |
| `AboutExact` | action | every about a new line carries is the caller's: a member line's about for an id is the about the request aligned with that id, and the n-th note line's about set is the request's n-th note |
| `RefusedWritesNothing` | action | a refusal and a receipt replay change no key |
| `FenceWritesReceiptOnly` | action | a winning fence writes one new receipt at its request epoch, status `fenced`, both seqs 0, and nothing else: no line, no history, no table change |
| `FenceBypassesLog` | action | a fence is refused only `STALE` or by prepare's own preflight: the log plan never sees it |
| `TimeFrozen` | action | every line of one call carries that call's one TIME sample |
| `LogAppendOnly` | action | nothing is rewritten or removed within an epoch |
| `LogIdGuard` | action | the supported writer appends only after a head whose last-generated-id is exactly `<entries-added>-0` |

## The instance

2 cards (`p1`, `p2`), 3 stored IDs (`m1` and `m2` placed at epoch 0, `m3` free for a create after the advance), 3 rows, 2 callers, at most 4 step calls counting refusals, replays and fences, epochs 0..1, at most 1 lost reply. Epoch 0 starts as the fold of its three seed lines. The programs, in `MCSetTableLog.tla`:

| part | op | what it is |
|---|---|---|
| `AMoveSet` | a1 | a set move of `m1` and `m2` from r1 to r2: one line naming both cards |
| `AStay` | a2 | a stay setting field 2 on `m1` and `m2` (`m2` has it already: the line names `m1` only), and a note about both cards |
| `ARows` | a3 | rows add r2 (present) and r3, then move `m1` to r3 with score 2 |
| `ANotes` | a4 | two notes, no entry, with op |
| `ANoteNoOp` | none | a note with no op: `REQUEST` under the contract |
| `AThree` | a5 | after the advance: a field change on `m3` and a note |
| `AStayLate` | a6 | a stay setting field 1 on `m1` (already 1) and `m2` (2 to 1), abouts `p1`, `p2`: the changed-id mask drops the first id |
| `BClear` | b1 | advance 0 to 1, restore r1, create `m3` there, a note |
| `BClearRs` | b5 | the same clear behind a rowset guarding r1 and r2 at the request epoch (`ROWSET` once r3 was added); the rowset writes no line |
| `BRemove` | b2 | remove `m2` from r1 with a field delta |
| `BBack` | none | an op-less move of `m1` from r2 back to r1 |
| `BDel` | b3 | a guard on `m1` at r1 and a delete of r2 |
| `BShared` | b4 | a rescore of `m1` and `m2` in place, both about `p1` |
| `BDelStay` | b6 | after `AMoveSet`: a changed stay of both in r2 with a delete of r2: `ROWCONFLICT` |
| `BDelOut` | b7 | after `AMoveSet`: both moved out of r2 to r1 with the delete of r2: accepted by final occupancy, a move line and a rows line in one commit |

## Configurations and results

Every case is in group `settablelog` of `tla/CASES.tsv`, `required`, deadlock check on, and its record is in `tla/RUNS.tsv` (the bench's, made by `tlacheck run`: 2 workers for a passing case, 1 for a counterexample; the numbers below are that run's).

**Contract configurations, expected to pass:**

| configuration | what it adds | states generated / distinct |
|---|---|---|
| `MCSetTableLog` | the main menu, fences | 52,089 / 21,440 |
| `MCSetTableLogCeiling` | sequence ceiling 4 | 28,910 / 11,770 |
| `MCSetTableLogLimit` | id cap 1 | 41,274 / 16,974 |
| `MCSetTableLogCursor` | the paged reader, lines and cardlines | 143,159 / 38,287 |
| `MCSetTableLogRaw` | raw `XADD *` on the active or the next epoch's key, raw `XDEL`, a reader, fences | 467,920 / 113,102 |
| `MCSetTableLogReplayClosed` | replay only of an epoch already advanced past | 7,748 / 2,567 |
| `MCSetTableLogReplayQuiet` | replay of any epoch, the live one included, with no store action while it runs | 17,039 / 6,154 |
| `MCSetTableLogNotesOp` | the op-less note (refused `REQUEST`) and a notes step with op, lost replies and resends | 1,345 / 567 |

**Reversed witnesses, each expected to fail the property named** (the table in the module header says the rule each one breaks): `WCounter` (SeqGapless), `WNoopIds` (OneLinePerChange), `WRefusedLines` (RefusedWritesNothing), `WNoScore` (ReplayEqualsStore), `WSplit` (ReplayEqualsStore), `WFirstOnly` (HistoryExact), `WNoDedupe` (HistoryExact), `WCursorLimit` (CursorNeitherSkipsNorRepeats), `WPairSkip` (CursorNeitherSkipsNorRepeats), `WNoDone` (RetryWritesOnce), `WAboutMask` (AboutExact), `WRowsetLine` (OneLinePerChange), `WFencePlan` (FenceBypassesLog), `WPerLine` (TimeFrozen), `WNoCap` (LineBound), `WNoCeiling` (SeqCeiling), `WTrim` (LogAppendOnly), `WShape` (LogIdGuard), and `MCSetTableLogNoOpNote` (RetryWritesOnce), which keeps Layer 2 revision 1's rule that a step with caller notes and no op is accepted (`Broken = "opnotes"`). Every trace was read; each one breaks exactly the rule it names. The four new ones, as recorded:

- `WPairSkip`: `BShared` writes line 4 naming `m1` and `m2`, both about `p1`; a cardlines page of 2 items emits `(2, m1)` and `(4, m1)` and ends inside line 4; the witness moves the list index on, so the slot is exhausted without `(4, m2)` (4 states).
- `WAboutMask`: `AStayLate` changes only `m2`; the line names `m2` with about `p1`, the about of the first, unchanged id (2 states). No other property sees it: the history list and the fold agree with the line.
- `WRowsetLine`: `BClearRs` writes a rowset line first in the new epoch, before the advance line (2 states).
- `WFencePlan`: a step is refused by prepare and its reply lost, the caller fences, a raw `XADD *` makes the head foreign, and the fence is refused `LOGID` (6 states); under the contract that fence is recorded `fenced`.

**Goal configurations, expected to fail on the contract model; each counterexample was checked against the contract by hand:**

- `MCSetTableLogReplay` (ReplayVerdictSound, 6 states): a paged replay of the live epoch, with verbs running, folds the log through the tail of its first page; a step then writes its line and its table change together, and the compare calls a correct log and tables `differ`. Decided (`rowan-3c6ca2c34063`, H1): replay is defined for a closed epoch, or for the live epoch only while the machine is STOPPED and no verb runs; it replays into the Mem twin or an owned scratch store, never the live store; the check's log pass compares per piece at the piece's observed last. The text is owed to Layer 2 revision 2. `MCSetTableLogReplayClosed` and `MCSetTableLogReplayQuiet` pass.
- `MCSetTableLogError` (ReplayEqualsStore, 2 states): a runtime error after the table commands and before the log commands keeps the table change with no line and no receipt (the recorded trace is an errored advance: the epoch moves to 1 and `m3` is created there, and the new epoch's log is empty). Decided scope (`rowan-3c6ca2c34063`, flagged, kept as stated scope): plan proves it cannot come from a refusal, only from a bug or an OOM; the order stays tables, then log, then done; the check's compare detects the tables ahead of the log and raises a judgment, and nothing repairs it silently. Under Layer 1 revision 4 the caller's resend of the same bytes is refused `PLACE`, which settles nothing; the caller then fences, and since no receipt was written the fence wins: it records `fenced` and settles the identity as NOT APPLIED while the step's table change stands. For an errored advance the resend and the fence are refused `STALE` instead, which Layer 1 section 5 also counts as settling the identity, while the advance stands and the new epoch's log does not start with its advance line. Both are outside Layer 1's no-rollback promise, and the check's compare is what reports them. Bench probes reach both (the model's caller reaches the fence after the `PLACE` refusal through a lost reply, since it does not model the settlement rule; Layer 1's model does).
- `MCSetTableLogDelKey` (ReplayEqualsStore, 2 states): a raw `DEL` of the log key; replay finds the tables ahead of the log, as section 2 says it will. Stated scope, not a hole.

**Bench-only probes, not committed** (each an invariant that must be violated): the passing configurations reach `REQUEST` for the op-less note and a replay of the notes step with op; `ROWSET`; the rowset clear applied; `ROWCONFLICT` for the changed stay and `BDelOut` applied; `OCCUPIED`; `PLACE`; `STALE`; `MEMBEREPOCH`; a prepare refusal of a step and of a fence; a fence that wins, including at a foreign log head; a fence that replays an `ok` receipt; a replayed `fenced` receipt (5 calls, 2 lost replies); `AStayLate` applied; a member step in epoch 1; a replay of an advance's receipt; `DRIFT` for an advance after a raw `XADD *` on the next epoch's key; `LOGID` on a foreign head; `LIMIT`; seq `OVERFLOW`; a cardlines page ending inside a line, a chain done with `p1`'s `(4, m2)` item, a note item, multi-page lines and cardlines chains; a lines and a cardlines chain refused at a hole; a quiet replay of the live epoch after a step, verdict `equal`; the error then a fence that wins, and an errored advance whose fence is refused `STALE`. Larger instances also pass: the main configuration with 6 calls and 2 lost replies (274,697 states, 100,942 distinct) and the cursor configuration with 5 calls and 2 lost replies (666,962 states, 163,191 distinct).

## Not modelled

- Byte sizes and budgets other than the id cap and the page limit (a page may end early, which stands for the byte budget): the 1 MiB line, the 512 KiB cardlines item, the 8 MiB reply and fetch budgets, the XINFO reservation, fetching each distinct seq once per cardlines call, and the memory gate.
- The line encoding (n, d, key order, shared/set/unset: one field, set only), include_meta and field projections, and lines items as {seq, n, d} with d verbatim.
- Ranks of rows (rows are a set; a rowset compares row names only).
- Several tables (one table, one column), and so the catalog.
- Intent digests and OPCONFLICT (a retry is the same bytes; a fence carries the same op).
- TWICE and the other static request checks, except notes without op (REQUEST, modelled) and a fence with entries or notes (written, never sent): a rowset out of place, an advance without op, field names.
- EPOCHAHEAD, ADVANCE (advance.from), EPOCHGONE and teardown, and REVISION, count and rcount guards (Layer 1's model has the guards).
- Redis types and ACL (their refusals, and a fence refused by S.fence_prepare, are the nondeterministic PrepareRefused).
- The enclosing preplan: entries and notes appended in Lua before S.plan, log_plan.note_seqs aligned with ctx.notes (ReplySeqsTrue does not check note seqs), and the exemption of preplan notes in an op-less step from the notes-op rule.
- Atomic lines as a bounded prefix with next and through (only the page chain is modelled), the last query, L.read_line_at, the done query, and the read request's wire (kind strings, answers aligned with queries).
- The caller's side of Layer 1 section 5 (Layer 1's model has it): the rule that after an unknown outcome only ok, an ok replay, STALE, OPCONFLICT or fenced settles an identity, done before a replan, and a new op after a fenced settlement. Here a caller takes any reply as final and fences only from an unknown outcome.
- Of the replay decision (H1): the STOPPED machine itself (stood for by no store action while a replay runs), the twin or scratch target, and the per-piece compare at the observed last.
- Decided for Layer 2 revision 2, pending, not modelled: a note's about set counting under the 2,000-per-line cap (LineBound counts member ids only); skipping XINFO when the step writes no line (LogCode refuses LOGID on a zero-line step at a foreign head); DRIFT when the log key is absent but a touched history key exists; one RPUSH per history key per step.
- The first epoch's tables are assumed to be exactly the fold of its log (decided: the fixture initializer writes rows through a step).
- OneLinePerChange assumes at most one rows entry per step, which every menu keeps; normalizing repeated row names into their first entry is modelled in the builder but not checked independently.
- The ABA case of an op-less table step (another caller restores its precondition before the resend) is outside the menus; it is Layer 1's op-less rule, not the log's.
- A second advance (epochs are 0..1). DRIFT on an advance whose new-epoch log key exists is modelled: only something other than the supported writer can write that key first (epochs are monotone), and the raw kind "next" does, in MCSetTableLogRaw.
- Liveness: no fairness, and page chains or callers ending are not checked; "at least one position advances on a nonempty page" is covered only as a page returning 1..PageLimit items.

## Running it

On a Linux bench with java, never on a working machine: `tlacheck run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-out --group settablelog` runs the thirty cases under the house budget, and `tla/README.md` says how the records are refreshed and merged. One case by hand: `timeout 900 java -cp /path/to/tla2tools.jar tlc2.TLC -workers 8 -config MCSetTableLogCursor.cfg MCSetTableLog.tla`, from a private copy of `tla/`.
