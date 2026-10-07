# nova-sprint: a cold audit (antigravity)

Read as a stranger with the spec beside it, at the tip of
`sprint/mechanical-2026-10-02` = `dd275a1833786243257a6ed0c584f25f3bcf75bc`: the sprint
command package (`cmd/nova-sprint`), its engine and store (`internal/sprint`,
`internal/sprint/store`), the dashboard and wire packages (`internal/sprintdash`,
`internal/sprintwire`), the sprint and card-contract specs under `docs/`, and the sprint
models under `tla/` (`ServerLanes`, the tick and the fence, `FriendPresence`). Nothing was
run against a live store or server; no product code was changed. The tree's own unit suites
pass (`./internal/sprint/...`, `./internal/sprint/store/...`, `./internal/sprintdash/...`,
`./internal/sprintwire/...`), `go vet` is silent, and `staticcheck` reports only finding 8.
The two probes below ran on a bench with an in-memory world and a scratch clone, and both
scratch tests were removed before this report was committed.

1. **URGENT — a reader on a drained machine panics the tick with an integer divide by
   zero.** `internal/sprint/readers.go:662` (`readerRoom.share`), reached from
   `internal/sprint/readers.go:726` (`levelReads`) and `internal/sprint/readers.go:615`
   (`TickLevelReads`, a tick-start part); the width is
   `internal/sprint/readers.go:627-637` (`ReaderWidth`), which is `0` for a member whose
   fleet row drains (`MemberWidth`, `internal/sprint/width.go:50-58`, `DrainWidth`).
   What is wrong: `share` computes `r.free * roomParts / r.width`, and a drained member's
   reader room has `width == 0`, so the level's source scan divides by zero whenever a
   drained member's reader still holds an asked read and at least one other reader is up.
   Evidence: a scratch test on the in-memory world (readers `reader-m1` and `reader-m2` of
   width 4, four flash primaries, `TickAsk`, then
   `FleetStep{Op: "up", Member: "m1", Drain: true}`, then `TickLevelReads`) run on the
   bench with `go test -count=1 -timeout 300s -run TestAuditWidthZeroReaderPanic -v ./internal/sprint/`
   printed `reader-m1 load=2 reader-m2 load=2 m1 width=0 m2 width=4` and then
   `panic: runtime error: integer divide by zero` with `readerRoom.share(...) readers.go:662`
   called from `levelReads(...) readers.go:726` and `TickLevelReads(...) readers.go:615`.
   The drain is a supported verb (`cmd/nova-sprint/fleet_drain_test.go:15`), and no test in
   `internal/sprint` covers a drained *reader*.
   Fix: in `readerRoom.share`, return an unreachable share for `width <= 0` before the
   divide (`if r.width <= 0 { return -1 }`), or drop width-0 readers from the level's `up`
   list and take their asked reads back as an away reader's.

2. **URGENT — a bare RFC3339 time is accepted as proof a friend's session is alive.**
   `cmd/nova-sprint/friends.go:420-423`; `internal/sprint/friend_proof.go:26-31`
   (`LegacyPongGrace`); `internal/sprint/store/friends.go:306-309`.
   What is wrong: for the first hour after the server starts, the verb turns any parseable
   `--pong <RFC3339>` into `words.Legacy`, and the store writes that timestamp as
   `rec.Pong` with `proved=true`, so a friend reads up on a timestamp alone, with no check
   asked by her daemon's run and no nonce ever answered; the contract says a bare time is
   "a beat with no proof" (`internal/sprint/friend_proof.go:20-24`), and the model makes a
   friend up only from an answered check (`tla/FriendPresence.tla:111-118` `UpIn`,
   `:218-225` `Answer`, `:312` `UpHasFreshAnswer`).
   Evidence: the code above; the help itself admits the exception at
   `cmd/nova-sprint/friends.go:128` ("but for the old `--pong <time>`, which counts for
   1h0m0s after the server starts"), and `a.serveStarted` is reset on every `listen`
   (`cmd/nova-sprint/serve.go:385`), so a restart hands a pre-nonce daemon another hour.
   Fix: delete `LegacyPongGrace` and the legacy branch; a pong that names no nonce the
   daemon's run asked within `CheckAnswerWithin` is `NoProof`.

3. **URGENT — `member.PathsProposed` drops every proposed path after the first item that
   carries prose.** `internal/member/member.go:1745-1770`, the `break` at `:1766`;
   `docs/SPEC-CARD-CONTRACT.md:392-398`; the sibling reader
   `cmd/nova-sprint/recut_widen.go:111-143`.
   What is wrong: the loop appends an item's first word and then stops the whole line on
   `len(words) > 1`, so `PATHS-PROPOSED: \`b.go\`, c/d.go because the test needs both, and
   e.go` reads as `[b.go, c/d.go]`: `e.go` never reaches `member.CarryProposed`
   (`member.go:1771-1786`) or the rule's `heldProposal`
   (`internal/sprint/paths_proposed.go:85`), while the contract says the reader takes each
   comma-separated item's path up to its first whitespace, dash or semicolon and reads the
   rest as prose; the tool's own reader (`recut_widen.go:134-143`) keeps `e.go`, so the
   same line answers differently depending on which path parsed it.
   Evidence: the code and the spec, and the test that pins the drop:
   `cmd/nova-sprint/brief_widen_test.go:115-117` expects `[b.go, c/d.go]` for the line
   above.
   Fix: one reader for the line that reads each comma-separated item's path up to its first
   whitespace, dash or semicolon and never breaks the line on prose.

4. **URGENT — a friend's beat record is read-modify-written with no fence, so two beats
   for one friend can lose her proof.** `internal/sprint/store/friends.go:272-317` (read
   `:282-286`, write `:316`); `internal/sprint/store/tick.go:1550-1553` (plain `SET`).
   What is wrong: `FriendBeatProof` reads the beat record, steps the checks and the proof
   in memory and writes it whole with `kv.SetKey`, which on Redis is a plain `SET` with no
   `WATCH`/`MULTI` and no fence; two overlapping beats for the same friend (a daemon
   retrying a lost answer beside the next beat, or a manual `friend beat` beside the
   daemon) each read the same old record and the later write clobbers the earlier's `Asked`
   and `Pong`; a lost `Asked` makes the next `--pong` name a nonce that was never asked
   (`ProveBeat` refuses it, `internal/sprint/friend_proof.go:59-82`) and a lost `Pong` puts
   her down at once, which is the data loss the nonce proof exists to prevent.
   Evidence: the code path above; `FriendBeatProof` never calls `Fenced`, `Acquire` or a
   transaction, and the spec runs beats beside every other beat
   (`docs/SPEC-SPRINT.md:6232-6236`; `tla/ServerLanes.tla:109-116`, `BeatStart`).
   Fix: take the sprint fence (or a per-friend lock, or `WATCH`+`MULTI`) around the
   read-modify-write of the beat record.

5. **NEXT — a batch's remaining verbs run after its caller has gone.**
   `cmd/nova-sprint/serve.go:204-274`, the line taken once at `:248`.
   What is wrong: the caller's context is read once, in `a.serial.LockCtx(ctx)` at the
   batch's first on-line verb; once the line is held the loop runs every later verb with no
   `ctx.Err()` check, so a caller that dropped mid-batch still has its writes committed.
   Evidence: the loop above and the spec line it contradicts,
   `docs/SPEC-SPRINT.md:6240-6243` ("a caller that has gone ... has the verbs of its batch
   not yet run answered exit 2, not run, and nothing is changed by them"); the only test of
   a gone caller (`cmd/nova-sprint/servelanes_test.go:137-161`) exercises the wait for the
   line, not a cancellation after the line is taken.
   Fix: at the top of each iteration, when `ctx.Err() != nil`, answer the remaining verbs
   with `goneResult` and stop.

6. **NEXT — a worker's `progress` and `queue` are retried without the operation id the
   spec says every write carries.** `internal/sprintwire/worker.go:47-49`.
   What is wrong: the id is appended only for `take`, `finish` and `read`, while
   `docs/SPEC-SPRINT.md:6427-6430` ("A worker whose answer was lost sends the verb again
   with the same operation id (`--op`)") and the `Worker` comment
   (`internal/sprintwire/worker.go:33-35`) promise it for every write, and the server runs
   `progress` and `queue` as line-taking writes (`cmd/nova-sprint/serve.go:100`).
   Evidence: read `worker.go:47` beside the two promises;
   `internal/sprintwire/worker_test.go:37-40` pins the gap for `queue` ("a read is sent as
   given, with no operation id") while `worker_test.go:55-69` pins the id for the other
   three. Both verbs are idempotent in effect, so no result is lost; the promise is what
   does not hold.
   Fix: add `progress` (and `queue`) to the id list at `worker.go:47`, or narrow the spec
   line and the comment to the three commits that take an id.

7. **NEXT — `Worker.Run` can panic on a `Send` that returns no result, and dereferences a
   nil error when the retry loop never runs.** `internal/sprintwire/worker.go:62`
   (`r := res[0]`) and `:68` (`err.Error()`).
   What is wrong: the loop indexes the answer before checking it is there, and on the
   exhausted path it calls `err.Error()` without checking `err` is non-nil; a `Send` that
   returns `(nil, nil)` (or an empty slice with no error) panics on `res[0]`, and a budget
   already elapsed before the first send leaves the loop unentered and `err` nil.
   Evidence: the two lines; `Client.Do` enforces the length
   (`internal/sprintwire/wire.go:95-104`), so the production path is safe today, but
   `Send` is a public field with no stated length contract and tests pass their own
   functions (`internal/sprintwire/worker_test.go:23-30`).
   Fix: check `len(res) > 0` before `res[0]` and return exit 2 naming a malformed answer,
   and set `if err == nil { err = ctx.Err() }` before `:68`.

8. **NEXT — the friend finish's refusals name no remedy, and two are capitalized error
   strings.** `cmd/nova-sprint/friendcards.go:239`, `:246` and `:248`.
   What is wrong: `friendFinish` returns "the card names no REPO: line, so origin's tip of
   %s cannot be read" and the two "Head ..." refusals with no `run: ...` remedy the
   onboarding standard asks for, and the two `Head` lines begin a capitalized error string.
   Evidence: `staticcheck` on the bench printed exactly
   `cmd/nova-sprint/friendcards.go:246:14: error strings should not be capitalized (ST1005)`
   and `cmd/nova-sprint/friendcards.go:248:14: error strings should not be capitalized (ST1005)`;
   the caller `cmd/nova-sprint/collect.go` prints the error verbatim as
   `COLLECT <card> REFUSED <error>` and adds nothing.
   Fix: lowercase both and append a remedy (for example, the card verb that reads the
   branch and REPO line the report must name).

9. **NEXT — the widen path's parsers and glob helpers are duplicated in two packages, and
   the two wideners already disagree.** `cmd/nova-sprint/recut_widen.go:111-143`
   (`proposals`/`proposedPath`) and `:195-239` (`widenBrief`/`globNames`) against
   `internal/sprint/paths_proposed.go:192-195` (`globNames`), `:265-274` (`WidenBrief`) and
   `internal/sprint/rules_read.go:130-150` (`PathsWidened`).
   What is wrong: each package carries its own proposal reader, glob helper and
   brief rewriter; the readers disagree today (finding 3), and `widenBrief` fills `union`
   only from the first `PATHS:` line (`at < 0` at `recut_widen.go:200`) and then rewrites
   every `PATHS:` line to that one set (`:220-221`), while `PathsWidened` appends the new
   globs to each line's own globs. The repository's own standard (AGENTS.md, "Duplicated
   function becomes one lower-level module") names a copy a bug.
   Fix: move the path reader and the brief rewrite to one place under `internal/sprint` and
   have the command call it.

10. **NEXT — once a removal is kept for a dirty clone, gc still walks the whole tree.**
    `internal/sprint/gc.go:564-589`, the `keep != ""` test at `:582`.
    What is wrong: when `Dirty` reports a clone with work that is nowhere else, the walk
    sets `keep` but returns `nil` for every remaining entry, so `filepath.WalkDir` keeps
    reading every file and directory of a tree it will not remove, only to discard the
    count; on a job or bench tree this is the whole clone read for nothing, on every
    machine, hourly.
    Evidence: the callback returns `nil` (never `fs.SkipAll`) and the only use of `keep` is
    the early return at `:590-594`; the byte count is discarded on that path.
    Fix: return `fs.SkipAll` from the callback once `keep` is set (or check `p.r.Dirty`
    before the walk and skip the sizing walk when the removal is kept).

urgent=4 next=6
