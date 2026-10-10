# nova-sprint — cold audit, claude, 2026-10-06

Base: `sprint/mechanical-2026-10-02` at `4f35234af1f406d5f5722d66eedf2849f98e4cee`.
Scope: `cmd/nova-sprint`, `internal/sprint` and `internal/sprint/store`,
`internal/sprintdash`, `pkg/sprintwire`, `docs/SPEC-SPRINT.md`,
`docs/SPEC-CARD-CONTRACT.md`, and the sprint models under `tla/` (ServerLanes,
the tick, the fence, FriendPresence). The code was read as a stranger with the
spec beside it; nothing was run against the live store or server, and no
product code was changed. The probes below ran in a scratch copy on the build
bench with `GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1` and were removed before
the commit. The scoped unit suites are green at this base, so every finding
below is a place the tests do not reach.

## 1. URGENT — a drained member's reader divides the tick by zero

`internal/sprint/readers.go:658-663` (`readerRoom.share`, the division at
`:662`), reached from `internal/sprint/readers.go:704-726` (`levelReads`, the
comparison at `:726`) and `internal/sprint/readers.go:613-616`
(`TickLevelReads`, a tick-start part). What is wrong: a member drained with
`fleet up <m> --width 0` keeps its reader up (a reader's state is its beat and
its hold alone, `internal/sprint/store/readers.go:154-191`), and
`ReaderWidth` returns that member's width (`readers.go:627-637`,
`internal/sprint/width.go:50-52`), so the level builds a room of width 0 and
divides the reader's free room by it, panicking the tick.

Evidence — a scratch probe on the in-memory world (two readers of width 4,
two primaries asked), removed after it printed:

```
widths before drain: m1=4 m2=4; asked on m1=1 m2=1
widths after drain: m1=0 m2=4; asked on m1=1 m2=1
--- FAIL: TestAuditProbeDrainedReaderLevelPanics
panic: runtime error: integer divide by zero
github.com/mas-bandwidth/nova-tools/internal/sprint.readerRoom.share(...)
        internal/sprint/readers.go:662
github.com/mas-bandwidth/nova-tools/internal/sprint.levelReads(...)
        internal/sprint/readers.go:726
github.com/mas-bandwidth/nova-tools/internal/sprint.TickLevelReads(...)
        internal/sprint/readers.go:615
```

Fix: before line 662, `if r.width <= 0 { return -1 }` (an unreachable share),
or drop width-0 readers from the level's `up` list and take their asked reads
back as an away reader's.

## 2. URGENT — `member.PathsProposed` drops every proposed path after an item with prose

`pkg/member/member.go:1751-1769` (the `break` at `:1767`). What is wrong:
after an item whose first word is followed by more words the loop breaks the
whole line, so every later comma-separated path is lost, while the contract
says the reader takes *each* item's path up to its first whitespace, dash or
semicolon and reads the rest as prose (`docs/SPEC-CARD-CONTRACT.md:393-398`)
and the command's own reader does exactly that
(`cmd/nova-sprint/recut_widen.go:115-141`, `proposedPath`). The rule's
`heldProposal` and `member.CarryProposed` use the truncating reader, so a
HOLD that proposes `a.go reason, b.go` carries only `a.go`.

Evidence — a scratch probe, removed after it printed, beside the command's
reader on the same lines:

```
member.PathsProposed("PATHS-PROPOSED: a.go reason, b.go, c.go") => [a.go] ok=true
member.PathsProposed("PATHS-PROPOSED: a.go, b.go reason, c.go") => [a.go b.go] ok=true
member.PathsProposed("PATHS-PROPOSED: internal/sprint/rules.go - because it needs it, internal/sprint/widen.go") => [internal/sprint/rules.go] ok=true
cmd proposals("PATHS-PROPOSED: a.go reason, b.go, c.go") => [a.go b.go c.go]
cmd proposals("PATHS-PROPOSED: a.go, b.go reason, c.go") => [a.go b.go c.go]
cmd proposals("PATHS-PROPOSED: internal/sprint/rules.go - because it needs it, internal/sprint/widen.go") => [internal/sprint/rules.go internal/sprint/widen.go]
```

Fix: give both callers one reader that reads each comma-separated item's path
up to its first prose and never stops the line; delete the `break`.

## 3. URGENT — a bare `--pong <time>` is proof a friend's session is alive

`cmd/nova-sprint/friends.go:420-422`, `internal/sprint/store/friends.go:323-325`,
`internal/sprint/friend_proof.go:26-31`. What is wrong: for `LegacyPongGrace`
(one hour) after each server start, `friend beat <friend> --pong <RFC3339 time>`
is recorded as the beat's proof (`store/friends.go:325`), so `FriendEvidence`
reads her up on a timestamp her session never earned
(`internal/sprint/presence.go:229-236`), the "bare timestamp as proof a session
was alive" this card names. `a.serveStarted` is set fresh on every `listen`
(`cmd/nova-sprint/serve.go:385`), so a supervised restart (exit 3 on a replaced
binary, exit 4 on a wedged tick) hands a pre-nonce daemon another full hour.
The model's up comes only from an answer to a nonce the daemon asked
(`tla/FriendPresence.tla:111-118` `UpIn`, `:203-226` `Ping`/`Answer`) and it
has no variable for a time; `docs/SPEC-SPRINT.md:184-185` and
`docs/SPEC-FRIEND.md:1172,1262` do sanction the grace, and the help admits it
(`cmd/nova-sprint/friends.go:128`), so this is the written exception itself,
not a hidden one. One beat that asks and answers as her proves her either way
(`friend_proof.go:44-48`), so the grace protects nothing the nonces cannot.

Fix: delete `LegacyPongGrace` and the legacy branch; a pong that names no nonce
the daemon's run asked within `CheckAnswerWithin` is no proof, and the spec
sections that document the grace go with it.

## 4. URGENT — a friend's beat record is read-modify-written with no fence, so one of two beats is lost

`internal/sprint/store/friends.go:289-333` (read `getKeys` at `:300`, write
`kv.SetKey` at `:333`) and `cmd/nova-sprint/servelanes.go:123-131`
(`serveLanes.friendBeat`). What is wrong: `FriendBeatProof` reads the beat
record, steps its checks and its proof in memory, and writes the record whole;
on Redis that write is a plain `SET` with no `WATCH`/`MULTI` and no
compare-and-set (`internal/sprint/store/tick.go:1552-1554`), and a friend's
beat runs on the beat lane with no line, "beside the line and beside every
other beat" (`cmd/nova-sprint/serve.go:238-241`,
`docs/SPEC-SPRINT.md:6296-6298`). Two overlapping beats for one friend — a
daemon retrying a beat whose reply was lost beside the next beat, or a manual
`friend beat` beside the daemon — each read the same old record and the later
write clobbers the earlier's `Asked` checks and `Pong`: a lost `Asked` makes
the next `--pong` name a nonce that was never asked (`ProveBeat`,
`internal/sprint/friend_proof.go:70-82`) and a lost `Pong` puts her down, the
two losses the nonce proof exists to prevent.

Fix: take the sprint fence (or a per-friend lock, or `WATCH`+`MULTI`) around
the read-modify-write of the beat record.

## 5. NEXT — `progress` and `queue` are retried without the operation id the spec promises

`pkg/sprintwire/worker.go:47`: the id is appended only for `take`,
`finish` and `read`. `docs/SPEC-SPRINT.md:6488-6490` promises it for every
write whose answer was lost, and the `Worker` comment says "A write carries one
operation id through them" (`worker.go:33-35`). The server runs `progress` and
`queue` as line-taking writes (`cmd/nova-sprint/serve.go:218-241`), so a retry
after a lost answer re-runs them as new operations (both are idempotent in
effect today, so no result is lost; the promise is what does not hold).

Fix: add `progress` and `queue` to the id list at `worker.go:47`, or narrow
the spec line and the comment to the three verbs that take an id.

## 6. NEXT — `Worker.Run` panics on an empty answer and on a nil error

`pkg/sprintwire/worker.go:62` (`r := res[0]`) and `:68`
(`err.Error()`). The loop indexes the answer before checking it is there, and
the exhausted path dereferences `err` without checking it; a `Send` that
returns `(nil, nil)` panics on `res[0]`, and a budget already expired before
the first try leaves the loop unentered, `err` nil, and panics on `err.Error()`.
`Send` is a public field (`worker.go:22-24`) and tests pass their own.

Evidence — a scratch probe, removed after it printed:

```
Worker.Run with an empty answer panicked: runtime error: index out of range [0] with length 0
Worker.Run with an expired budget panicked: runtime error: invalid memory address or nil pointer dereference
```

Fix: `if len(res) == 0 { continue }` (or return exit 2 naming the empty answer)
and `if err == nil { err = errors.New("the server did not answer") }` before
line 68.

## 7. NEXT — the server's line-of-control comments cite a property that does not exist

`cmd/nova-sprint/main.go:239-245` (`controlLine`) and
`cmd/nova-sprint/seriallock.go:8-16` (`serialLock`) both cite
`tla/ServerLanes.tla, AbandonedNeverRuns`; the model's property is
`GoneNeverRuns` (`tla/ServerLanes.tla:204`, and the header at `:26`), and
`grep -rn AbandonedNeverRuns` finds only those two comments. `serialLock` also
calls itself "the server's one line of control (`app.serial`)", while
`app.serial` is `controlLine` (`main.go:148`) and `serialLock` is the read
lane's own lock (`servelanes.go:74-77`).

Fix: cite `GoneNeverRuns` in both, and describe `serialLock` as the read lane's
lock (or delete it and use the shared `sprint.ControlLine`).

## 8. NEXT — `widenBrief` rewrites every `PATHS:` line from a union read off the first (malformed-data hardening)

`cmd/nova-sprint/recut_widen.go:195-233`: `union` is filled only from the
`at < 0` first `PATHS:` line (`:200`), and then every `PATHS:` line is set to
that one value (`:214`, `:221`). The rule's `WidenBrief`/`PathsWidened`
(`internal/sprint/paths_proposed.go:265-274`,
`internal/sprint/rules_read.go:130-150`) instead widens each line from its own
globs, so the two wideners of a multi-line header disagree. A repeated typed
`PATHS:` key is refused at admission
(`pkg/swarm/lintheader.go:352-354`), so no admitted card reaches it; the
overclaim is on a stored brief edited by hand.

Evidence — a scratch probe, removed after it printed:

```
widenBrief("STATUS: x\nPATHS: a.go\nPATHS: b.go\n", [z.go]) =>
    STATUS: x
    CARRY: x attempt 1 head=abc
    PATHS: a.go,z.go
    PATHS: a.go,z.go
```

Fix: call the one shared `PathsWidened` (or refuse a second `PATHS:` line here
as the linter does), so the doc and the code agree.

## 9. NEXT — the twin's id counter discards its parse error, so an id is handed out twice

`cmd/nova-sprint/twin.go:142-151`: `n, _ = strconv.Atoi(v)` (`:148`) turns a
corrupt `twin:ids` value into 0, the next id is `t1` again, and the store takes
an operation id it has seen as the same operation and answers the old recorded
result, changing nothing. `newID`'s own comment (`:136-141`) says a counter
that was not kept would hand the same id out again, and only a `SetKey`
failure reaches `t.idErr`; a hand-edited or older twin file reaches the parse
path on the next verb.

Fix: record a parse failure in `t.idErr` and refuse the verb, as a `SetKey`
failure does.

## 10. NEXT — the friend finish's refusals name no remedy, and two are capitalized error strings

`cmd/nova-sprint/friendcards.go:263,270,272`. The no-`REPO:` refusal and the
two `Head ...` refusals carry no `run:` remedy, against the standard's "every
refusal carries a breadcrumb" (`docs/STANDARD.md:54`), and the two sentences
begin with a capital through `fmt.Errorf` (staticcheck ST1005). The caller
prints them verbatim (`cmd/nova-sprint/collect.go:195`).

Fix: append a remedy that runs (for example `run: nova-sprint card <id>`), and
lower-case the two `Head` sentences or print them as lines.

## 11. NEXT — a batch's later verbs run after its caller has gone

`cmd/nova-sprint/serve.go:247-260`. The batch takes the line once under the
caller's context (`:247-252`), then runs every later verb with no `ctx.Err()`
check (`:257-260`), so a caller that dropped after the line was taken still has
its remaining writes committed, against `docs/SPEC-SPRINT.md:6302-6305` ("a
caller that has gone ... has the verbs of its batch not yet run answered exit
2, not run, and nothing is changed by them"). The gone-caller test exercises
the wait for the line, not a cancellation after it was taken.

Fix: at the top of each iteration, when `ctx.Err() != nil`, answer the
remaining verbs with `goneResult` and stop.

## 12. NEXT — `isHold` reads the word HOLD anywhere in a failed report

`internal/sprint/widen.go:139-144`. After the verdict line,
`isHold` scans every field for a word that trims to `HOLD`, so a `FAIL` report
whose prose says e.g. "the earlier HOLD was wrong" and which also names a
`PATHS` word (`widen.go:190`) is treated as a HOLD and the widen rule edits
the brief in place; the contract reads a report's verdict from its `Verdict:`
line (`docs/SPEC-CARD-CONTRACT.md:187-210`), and `CollectVerdict` is that one
reader.

Fix: return `CollectVerdict(report) == "HOLD"` alone (an explicit `HOLD:` line
where the one-line friend form is needed).

Checked without a finding, for the reader's sense of coverage: the tick
deadline's stretch and the wedged-tick exit (`cmd/nova-sprint/run.go`), the
fence acquire/relock/release (`internal/sprint/store/lock.go`,
`internal/sprint/store/engine.go:474-480`), the friend presence windows
(`internal/sprint/presence.go:220-259`), the dashboard's release view and
event stream (`internal/sprintdash`), and the route arithmetic guards
(`internal/sprint/routetable.go:192-194`).

urgent=4 next=8
