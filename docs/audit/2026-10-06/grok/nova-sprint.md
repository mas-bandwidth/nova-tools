# nova-sprint: cold audit, 2026-10-06

Read as a stranger against the design at the base `sprint/mechanical-2026-10-02`,
tip `ad2f20c6bc71f6769ad464fe96586c57c0c5fb11`: the sprint command package
(`cmd/nova-sprint`), its engine and store (`internal/sprint`, `internal/sprint/store`),
the dashboard and wire packages (`internal/sprintdash`, `pkg/sprintwire`), the
sprint and card-contract specs under `docs/`, and the sprint models under `tla/`
(`ServerLanes`, the tick/fence, `FriendPresence`). Nothing was run against the live
store or server; no code was changed. `go vet` and the unit tier of the scoped packages
were run on a bench, clean.

## Issues

### 1. URGENT. A bare RFC3339 time is accepted as proof a friend's session is alive
`cmd/nova-sprint/friends.go:420-423`; `internal/sprint/store/friends.go:306-309`.
The verb turns any parseable `--pong <RFC3339>` into `words.Legacy` for
`sprint.LegacyPongGrace` (an hour) after the server start, and the store writes that
timestamp as `rec.Pong` with `proved=true`, so `FriendEvidence`
(`internal/sprint/presence.go:233-236`) reads her up on `Beat.Proof` alone, with no
check asked by her daemon's run and no nonce ever answered.
Evidence: the code above (the legacy branch is the only proof that does not go through
`sprint.ProveBeat`), and the model it contradicts: `tla/FriendPresence.tla:203-226`
makes a session answer (`Answer`) possible only when a nonce is `pending`, and
`tla/FriendPresence.tla:312` (`UpHasFreshAnswer`) reads up only from `answered`; the
help at `cmd/nova-sprint/friends.go:128` admits the exception in words ("the old
`--pong <time>`, which counts for 1h0m0s after the server starts"). This is the defect
the card names: for the first hour after every server start, a caller who can name a
friend keeps her up with a timestamp while her session takes no turn.
Fix: delete `LegacyPongGrace` and the legacy branch; a pong that names no nonce the
daemon's run asked within `CheckAnswerWithin` is `NoProof`.

### 2. URGENT. `member.PathsProposed` drops every proposed path after the first item with prose
`pkg/member/member.go:1758-1769`.
The loop appends an item's first word and then breaks on `len(words) > 1`, so
`PATHS-PROPOSED: a.go because it is used, b.go` reads as `[a.go]`: `b.go` never reaches
`member.CarryProposed` (`member.go:1777-1787`) or the `paths` rule's `heldProposal`
(`internal/sprint/paths_proposed.go:94`), and the twin or the in-place widen is built
from the truncated list.
Evidence: `cmd/nova-sprint/brief_widen_test.go:115-117` pins the drop
(`PATHS-PROPOSED: \`b.go\`, c/d.go because the test needs both, and e.go` expects
`[b.go, c/d.go]`), while the contract says the opposite at
`docs/SPEC-CARD-CONTRACT.md:396-398` ("the reader takes each comma-separated item's
path up to its first whitespace, dash or semicolon, reads the rest as prose") and
`docs/SPEC-SPRINT.md:2171-2173`; the tool's own reader
(`cmd/nova-sprint/recut_widen.go:115-143`) keeps `b.go`, so the same line answers
differently depending on which path parsed it.
Fix: one reader for the line, reading each comma-separated item's path up to its first
whitespace, dash or semicolon and stopping only at a semicolon.

### 3. NEXT. Two implementations of "widen the brief in place" disagree
`internal/sprint/widen.go:289-333` (`widenInPlace` = `SharedWidened(PathsWidened(...))`:
each `PATHS:` line widened in place, `SHARED:` lines widened) against
`cmd/nova-sprint/recut_widen.go:195-233` (`widenBrief`: every `PATHS:` line set to the
union of all old globs, `SHARED:` lines never touched, `CARRY:` inserted).
Evidence: the two functions and the spec that describes them differently —
`docs/SPEC-SPRINT.md:2168-2176` (the verb: union, no `SHARED:`) against
`docs/SPEC-SPRINT.md:4987` (the rule: "its `PATHS:` and `SHARED:` lines widened by
exactly those files ... as `brief --widen` edits it"); a card widened by hand and a
card widened by the rule therefore carry different briefs, and only one widens a
`SHARED:` glob.
Fix: `brief --widen` calls the same `sprint.PathsWidened`/`sprint.SharedWidened` the
rule uses, and the spec is corrected to one account.

### 4. NEXT. A batch's remaining verbs run after its caller has gone
`cmd/nova-sprint/serve.go:247-274`.
The caller's context is read once, in `a.serial.LockCtx(ctx)` at the batch's first
on-line verb; once the line is held the loop runs every later verb with no `ctx.Err()`
check, and a caller that dropped mid-batch still has its writes committed.
Evidence: the loop and the spec line it contradicts, `docs/SPEC-SPRINT.md:6142-6144`
("a caller that has gone ... has the verbs of its batch not yet run answered exit 2,
not run, and nothing is changed by them"); the only test of the gone caller
(`cmd/nova-sprint/servelanes_test.go:137-161`) exercises the wait for the line, not a
cancellation after the line is taken.
Fix: at the top of each iteration, when `ctx.Err() != nil`, answer the remaining verbs
with `goneResult` and stop.

### 5. NEXT. `sprintwire.Worker.Run` panics when the context is already done
`pkg/sprintwire/worker.go:54-68`.
`ctx, cancel := context.WithTimeout(...)` with a non-positive budget leaves the `for`
loop unentered, `err` stays nil, and line 68 calls `err.Error()` on it.
Evidence: the loop guard `for try := 0; try < Tries && ctx.Err() == nil` and the final
`return 2, w.failed(nil, []byte(err.Error()))`; `Budget` is a caller-settable duration
whose documented zero is `Timeout`, not a refusal of negatives.
Fix: `if err == nil { err = ctx.Err() }` before line 68 (or a named
"the context ended before the first try" error).

### 6. NEXT. The twin id counter's parse error is discarded, so an id is handed out twice
`cmd/nova-sprint/twin.go:146-149`.
`n, _ = strconv.Atoi(v)` turns a corrupt `twin:ids` value into `0`, the next id is `t1`
again, and the store, which takes an operation id it has seen as the same operation,
returns the old recorded result and changes nothing.
Evidence: the code, and `newID`'s own comment (`twin.go:126-141`) that a counter not
kept "would hand the same id out again", while only `SetKey` failures reach `t.idErr`;
a twin file whose counter key is not a number (an older format, a hand edit) reaches
this on the next verb.
Fix: report a counter that is not a whole number through `t.idErr` and refuse the verb
rather than defaulting to 0.

### 7. NEXT. A corrupt friend beat record is silently dropped
`internal/sprint/store/friends.go:285`.
`_ = json.Unmarshal([]byte(vals[0]), &prev)` loses the record's `Asked` checks and
`Pong`, so a friend whose session answered reads down on the next beat and nothing
prints why.
Evidence: the line and its comment ("an unreadable record holds no check and no
proof"); the store writes no note and returns no error, against the standard's "never
fails silently, and every refusal carries a breadcrumb"
(`docs/STANDARD.md`, "A tool is for an AI").
Fix: return the unmarshal error (or write a happened note naming the friend and the
record), so a corrupt beat is surfaced, not treated as an empty one.

### 8. NEXT. `isHold` reads the word HOLD anywhere in a failed report
`internal/sprint/widen.go:139-144`.
`isHold` accepts any field that trims to `HOLD`, so a report whose verdict is `FAIL`
and whose prose says e.g. "the earlier HOLD was wrong" (with a `PATHS` word, which
`widenAnswers` also requires, `widen.go:190`) is treated as a HOLD and the widen rule
edits the brief in place; `sprint.CollectVerdict` is the contract's one verdict reader.
Evidence: `isHold`'s `strings.Fields` scan and `widenAnswers`'s use of it at
`widen.go:196`; the contract reads a report's verdict from its `Verdict:` line
(`docs/SPEC-CARD-CONTRACT.md:187-210`).
Fix: read the verdict with `CollectVerdict` and treat only a HOLD verdict (or an
explicit `HOLD:` reason) as a HOLD.

### 9. NEXT. `--have` accepts a card id twice where the spec says each is given once
`cmd/nova-sprint/reads.go:493-502`.
`strings.Split(have, ",")` is folded into `w.have[id] = true` with no refusal of a
repeat, so the same card id twice is silently one card, while the spec says "its
`--have` card ids, each given once" (`docs/SPEC-SPRINT.md:6220`).
Evidence: the loop above and the spec line; every other malformed `--have` (a bad id,
too many) is refused in the same function, so the silence is inconsistent with its own
grammar.
Fix: refuse a repeated id in `--have`, naming it, as the other `--have` faults are.

urgent=2 next=7
