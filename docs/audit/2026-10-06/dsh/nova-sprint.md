# nova-sprint: a cold audit (dsh)

Base: `sprint/mechanical-2026-10-02` at `0adf87bfe`. The findings were first read at
`ad2f20c6b` and are carried here; every cited file is unchanged between the two bases
except `internal/sprint/rules_read.go` (the widen rule, whose line numbers below moved)
and the two specs, and findings 3 and 7 were revised after a cold read of the first
revision: 3 is now scoped to malformed stored data, and 7 is recorded as a policy
question rather than a defect. Scope: `cmd/nova-sprint`,
`internal/sprint` and `internal/sprint/store`, `internal/sprintdash`,
`pkg/sprintwire`, `docs/SPEC-SPRINT.md`, `docs/SPEC-CARD-CONTRACT.md`, and
the sprint models under `tla/` (ServerLanes, DirtyTick and the fence,
FriendPresence). Read with the spec beside the code. No live store or server
was touched; every command below ran on a scratch clone on the build bench,
with `NOVA_TEST_NO_HOST=1`. The unit suites of every package above ran clean on
the first revision's bench (`go test -count=1 -timeout 600s ./cmd/nova-sprint/... ./internal/sprint/... ./internal/sprintdash/... ./pkg/sprintwire/...`), so nothing here is a
red test.

## 1. URGENT — `member.PathsProposed` drops every proposal after the first item that carries prose

`pkg/member/member.go:1758-1768` (the loop that ends with `break` at
1766). `docs/SPEC-CARD-CONTRACT.md:393-398` says the reader takes *each*
comma-separated item's path up to its first whitespace, dash or semicolon and
reads the rest as prose. The code takes the first whitespace-separated word of
an item and then **stops the whole line** as soon as that item has a second
word, so a reason on one item silently discards every path after it. The
sibling reader that `brief --widen` uses, `cmd/nova-sprint/recut_widen.go:115-143`
(`proposals`/`proposedPath`), does not stop: the two readers of one line
disagree, and the rule (`heldProposal`, `internal/sprint/paths_proposed.go:85-103`)
and `member.CarryProposed` (`member.go:1777-1787`) use the truncating one.

Evidence — a temporary probe test run on the bench scratch clone (removed
after it printed; nothing committed):

```
member.PathsProposed("PATHS-PROPOSED: a.go reason, b.go, c.go")              => [a.go]
member.PathsProposed("PATHS-PROPOSED: a.go, b.go reason, c.go")              => [a.go b.go]
member.PathsProposed("PATHS-PROPOSED: internal/sprint/rules.go - because it needs it, internal/sprint/widen.go") => [internal/sprint/rules.go]

the `proposals` reader in cmd/nova-sprint for the same three lines:
  => [a.go b.go c.go] / [a.go b.go c.go] / [internal/sprint/rules.go internal/sprint/widen.go]
```

Fix: in `PathsProposed`, read each comma-separated item's path independently
(the `proposedPath` rule) and never `break` the line on prose; give both
callers one reader.

## 2. URGENT — a friend's beat record is read-modify-written with no fence, so two beats for one friend can lose her proof

`internal/sprint/store/friends.go:272-317`. `FriendBeatProof` reads the beat
record (`getKeys`, `internal/sprint/store/presence.go:250-265`), steps the
checks and the proof in memory, then writes it whole with `kv.SetKey`
(`friends.go:316`). On Redis that is a plain `SET` (`internal/sprint/store/tick.go:1552-1554`),
with no `WATCH`/`MULTI`, no fence and no compare-and-set. The spec and the
model run beats concurrently *"beside every other beat"*
(`docs/SPEC-SPRINT.md:6133-6136`, `tla/ServerLanes.tla:109-112`, `BeatStart`),
so two overlapping beats for the same friend — a daemon retrying a beat whose
reply was lost beside the next one, or a manual `friend beat` beside the
daemon — each read the same old record and the later write clobbers the
earlier's `Asked` and `Pong`. A lost `Asked` makes the next `--pong` name a
nonce that was never asked, so `ProveBeat` (`internal/sprint/friend_proof.go:59-82`)
refuses it and her session's evidence is lost; a lost `Pong` puts her down at
once. Both are the data loss the nonce proof exists to prevent.

Evidence: the code path above (`friends.go:282-286` read, `friends.go:316`
write, `tick.go:1552` plain `SET`); `FriendBeatProof` never calls `Fenced`,
`Acquire` or a Redis transaction.

Fix: take the sprint fence (or a per-friend lock, or `WATCH`+`MULTI`) around
the read-modify-write of the beat record.

## 3. NEXT (hardening, malformed data only) — `widenBrief` rewrites every `PATHS:` line from the one union read off the first

`cmd/nova-sprint/recut_widen.go:195-233`. `union` is filled only from the
**first** `PATHS:` line (the `at < 0` gate at 200, set at 201), and then every
`PATHS:` line is rewritten to that one `set` (line 221). Its own doc at
191-194 says "every `PATHS:` line the union of its globs and globs", and the
rule's `WidenBrief` (`internal/sprint/paths_proposed.go:265-274`, via
`PathsWidened` at `internal/sprint/rules_read.go:130-150`) appends the new
globs to each line's own globs: the two wideners of a multi-line header
disagree.

Reachability — this is a hardening gap, not a defect on an admitted card. A
repeated typed `PATHS:` key is refused at admission
(`pkg/swarm/lintheader.go:352-354`: "is declared twice ... a card with two
of this line has no one value for it"), and the gate reads only the first line
(`pkg/decide/gate.go:205-219`), so no card admission accepts carries a
second `PATHS:` line and no accepted card loses a glob here. What remains is that
`widenBrief`'s plural loop and doc overclaim: they promise a shape the rest of
the contract refuses, so a stored brief edited by hand (or a future relaxation
of the duplicate-key rule) would silently drop the later lines' globs on the
next `brief --widen`.

Evidence — the first revision's bench probe:
`widenBrief("...PATHS: a.go\nPATHS: b.go\n...", ["z.go"], ...)` printed

```
PATHS: a.go,z.go
PATHS: a.go,z.go
```

— `b.go` is gone. The linter refusal above is what keeps this off the admitted
path; the probe is malformed stored data, not an accepted card.

Fix: keep one `PATHS:` line's semantics and refuse a second here too (so the
doc and the code agree), or widen each line from its own globs through the one
shared `PathsWidened`.

## 4. NEXT — `serialLock` is documented as `app.serial` and cites a property that does not exist

`cmd/nova-sprint/seriallock.go:8-16`: the type is called "the server's one line
of control (`app.serial`)" and cites `tla/ServerLanes.tla, AbandonedNeverRuns`.
Since the tick's turn landed, `app.serial` is `controlLine` over
`sprint.ControlLine` (`cmd/nova-sprint/main.go:236-251`), and `serialLock` is
only the read lane's own line (`cmd/nova-sprint/servelanes.go:74-77`,
`serveLanes.line`). The model property is `GoneNeverRuns`
(`tla/ServerLanes.tla:204`); there is no `AbandonedNeverRuns`. This is a second
lock type with the first one's comment and a false reference, which a cold
reader will take for the server's line.

Fix: rewrite the comment as the read lane's lock (or delete `serialLock` and
use the shared `sprint.ControlLine`), and cite `GoneNeverRuns`.

## 5. NEXT — `FriendPresence.tla` models one of the three evidence sources the code accepts

`tla/FriendPresence.tla:111-118` (`UpIn`) derives up from `answered`/`answerAge`
alone, and the rules are `UpHasFreshAnswer`/`UpHasRunningHarness`
(`FriendPresence.tla:312-318`). `FriendStatus`/`FriendEvidence`
(`internal/sprint/presence.go:220-259`) also accepts the coordinator's pong
observation (`f.Health.Seen`, `f.Health.State == Up`,
`f.Health.Generation == f.Generation`, within `FriendPongWindow` = 10m) and a
finished card (`f.Finished`, within `FriendFinishWindow` = 30m). The model has
no variable for either, so TLC proves properties of a strictly smaller rule
than the one that decides whether a friend is up and her cards are dealt
(`steps_work.go` take admission; `FriendPresence.tla:343-349`).

Fix: add the pong and finish evidence to the model's `UpIn` (with their
windows), or remove those paths from the code.

## 6. NEXT — `Worker.Run` can panic on a nil error or an empty answer

`pkg/sprintwire/worker.go:57-68`. If the loop body never runs — `Budget`
is negative, so `context.WithTimeout` is already done at entry — `err` stays
`nil` and `err.Error()` at line 68 dereferences nil. If a `Send` returns an
empty result slice with a nil error, line 62 (`res[0]`) panics. The comment on
`Send` (`worker.go:18-20`) only promises that a *bad answer* is an error, so
both are reachable from a caller's seam.

Fix: before line 68, `if err == nil { err = errors.New("the server answered no result") }`,
and check `len(res) == 0` after `Send`.

## 7. Policy question (not a graded defect) — should the legacy `--pong <time>` grace survive a server restart?

The first revision presented this as a defect; it is reframed here as an open
policy question. `cmd/nova-sprint/friends.go:419-423` accepts a bare time
as proof while `a.now().Sub(a.serveStarted) < sprint.LegacyPongGrace` (one hour,
`internal/sprint/friend_proof.go:26-31`), and `a.serveStarted` is set fresh on each
`listen` (`cmd/nova-sprint/serve.go:385`), so a supervised restart (exit 3 on a
replaced binary, exit 4 on a wedged tick) hands a pre-nonce daemon another full
hour. No requirement in the spec makes that grace outlive the process: the
contract measures it from the server's start (`friend_proof.go:26-31`, "After it,
a time is a beat with no proof"), so resetting it on restart is what the written
contract says, not a violation of it. Whether the grace should instead be keyed
off a persisted adoption time (or the daemon's run) is a policy choice for the
owner; until such a requirement is written, this stands as an open question and is
not counted below.

## 8. NEXT — the widen path's parsers and glob helpers are duplicated in two packages

`cmd/nova-sprint/recut_widen.go` and `internal/sprint/paths_proposed.go` each
carry a `globNames` (`recut_widen.go:236-239` vs `paths_proposed.go:192-195`),
a brief-rewriting `widenBrief`/`WidenBrief` (`recut_widen.go:195-233` vs
`paths_proposed.go:265-274`), and a proposal reader (`proposals`/`proposedPath`
vs `member.PathsProposed`). They already disagree today (issues 1 and 3). The
repository's own standard (`AGENTS.md`, "Duplicated function becomes one
lower-level module") names this a bug, and every fix to one copy has to be made
again in the other.

Fix: move the path reader and the brief rewrite to one place under
`internal/sprint` and have the command call it.

## 9. NEXT — two capitalized error strings in `friendcards.go`

`cmd/nova-sprint/friendcards.go:246` and `:248`. `staticcheck` on the bench
printed exactly:

```
cmd/nova-sprint/friendcards.go:246:14: error strings should not be capitalized (ST1005)
cmd/nova-sprint/friendcards.go:248:14: error strings should not be capitalized (ST1005)
```

Both are user-facing sentences that begin "Head ...", returned through
`fmt.Errorf` and the standard printer. A sentence that is output should be
printed as a line, not wrapped as an error string.

Fix: print these two sentences through the refusal/line printer, or start them
lower-case.

urgent=2 next=6
