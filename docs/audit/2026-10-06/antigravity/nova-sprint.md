# nova-sprint: a cold audit (antigravity-bb)

Read as a stranger with the spec beside it, at the tip of
`sprint/mechanical-2026-10-02` = `1b559077e0cb9ee9e14efe2910743cc4cff45ab9`: the sprint
command package (`cmd/nova-sprint`), its engine and store (`internal/sprint`,
`internal/sprint/store`), the dashboard and wire packages (`internal/sprintdash`,
`pkg/sprintwire`), the sprint and card-contract specs under `docs/`, and the sprint
models under `tla/` (`ServerLanes`, the tick and the fence, `FriendPresence`). Nothing was
run against a live store or server; no product code was changed. Every command below ran on
a bench copy with `NOVA_TEST_NO_HOST=1`, on an in-memory world or a scratch file, and each
scratch test was removed before this report was committed. On a clean bench the tree's own
suites pass: `go test -count=1 -timeout 600s ./internal/docs ./internal/ci` ends
`ok ... internal/docs 2.428s` and `ok ... internal/ci 13.776s`, and
`go test -count=1 -timeout 900s ./cmd/nova-sprint/... ./internal/sprint/... ./internal/sprintdash/... ./pkg/sprintwire/...`
is `ok` on every package (172s, 11s, 18s and under a second), so nothing here is a red test.

1. **URGENT — a reader on a drained machine panics the tick with an integer divide by
   zero.** `internal/sprint/readers.go:658-663` (`readerRoom.share`, the divide at `:662`),
   reached from `internal/sprint/readers.go:726` (`levelReads`) and `:615`
   (`TickLevelReads`, a tick-start part). A member that drains has width 0
   (`internal/sprint/width.go:50-58`, `MemberWidth`: `DrainWidth` is `"0"`), and a reader
   named for that machine takes its width (`internal/sprint/readers.go:627-637`,
   `ReaderWidth`), so `r.free * roomParts / r.width` divides by zero as soon as the drained
   member's reader still holds an asked read and one other reader is up.
   Evidence — a scratch test on the in-memory rig (`widthWorld(t, 4, 4, 10)`, `TickAsk`,
   `FleetStep{Op: "up", Member: "m1", Drain: true}`, `TickLevelReads`) ran
   `go test -count=1 -timeout 120s -run TestAuditDrainedReaderLevelPanics -v ./internal/sprint/`
   and printed `reader-m1 width=0 reader-m2 width=4` then
   `panic: runtime error: integer divide by zero` with `readerRoom.share(...) readers.go:662`
   called from `levelReads(...) readers.go:726` and `TickLevelReads(...) readers.go:615`.
   The drain is a supported verb (`internal/sprint/steps_work.go:1914-1918`; `fleet up <m> --width 0`), and no test in `internal/sprint` covers a drained reader.
   Fix: at the top of `share`, return an unreachable share for `r.width <= 0`
   (`if r.width <= 0 { return -1 }`), or drop width-0 readers from the level's up list and
   take their asked reads back as an away reader's.

2. **URGENT — a friend's beat record is read-modify-written with no fence, so two beats
   for one friend can lose her proof.** `internal/sprint/store/friends.go:298-333`
   (`FriendBeatProof`: read `:299-302`, write `:333`); `internal/sprint/store/tick.go:1552`
   (Redis `SetKey` is a plain `SET`). The function reads `friend-beat:<f>`, steps the checks
   and the proof in memory (`sprint.ProveBeat`, `:315`), then writes the whole record with
   `kv.SetKey` — no `WATCH`/`MULTI`, no `Fenced`, no per-friend lock. Beats run beside every
   other beat on the beat lane (`cmd/nova-sprint/servelanes.go:28-31,123-138`,
   `docs/SPEC-SPRINT.md:6272-6276`, `tla/ServerLanes.tla:109-116` `BeatStart`), so a daemon
   retrying a beat whose answer was lost beside the next beat, or a manual `friend beat`
   beside the daemon, each read the same old record and the later write clobbers the
   earlier's `Asked` and `Pong`. A lost `Asked` makes the next `--pong` name a nonce never
   asked (`ProveBeat` refuses it, `internal/sprint/friend_proof.go:59-82`) and a lost `Pong`
   puts her down at once — the data loss the nonce proof exists to prevent.
   Evidence: the code path above; `FriendBeatProof` never calls `Fenced`, `Acquire`, `Relock`
   or a transaction, and `internal/sprint/store/lock.go` is the only fence helper.
   Fix: take the sprint fence (or a per-friend lock, or `WATCH`+`MULTI`) around the
   read-modify-write of the beat record.

3. **URGENT — the legacy bare-time `--pong` is renewed by every server restart, so a
   pre-nonce daemon proves a session alive for an hour after each one.** The verb turns any
   parseable `--pong <RFC3339>` into `words.Legacy` while
   `a.now().Sub(a.serveStarted) < sprint.LegacyPongGrace`
   (`cmd/nova-sprint/friends.go:419-423`), and the store writes that timestamp as the proof
   with `proved=true` (`internal/sprint/store/friends.go:323-326`), so `FriendEvidence`
   reads her up on it with no check asked and no nonce answered
   (`internal/sprint/presence.go:227-236`). `a.serveStarted` is set fresh in every `listen`
   (`cmd/nova-sprint/serve.go:385`), so a supervised restart (exit 3 on a replaced binary,
   exit 4 on a wedged tick) hands such a daemon another full hour; the model has no such
   path at all (`tla/FriendPresence.tla:203-226,312`: `UpIn` reads up only from an answered
   check), and the contract's own words call a time "a beat with no proof"
   (`internal/sprint/friend_proof.go:51-52`; `docs/SPEC-SPRINT.md:184` is the grace the code
   follows). The START names this defect: for an hour after every server start a caller who
   can name a friend keeps her up with a timestamp while her session takes no turn.
   Evidence: the three sites above; the help itself admits the exception
   (`cmd/nova-sprint/friends.go:128`, "which counts for 1h0m0s after the server starts").
   Fix: key the grace to a persisted adoption (or delete `LegacyPongGrace` and the legacy
   branch, with the spec lines in `docs/SPEC-SPRINT.md:184` and `docs/SPEC-FRIEND.md:1172`),
   so a pong that names no nonce the daemon's run asked is `NoProof`.

4. **URGENT — a batch's remaining verbs run after its caller has gone.** In `serveCtx` the
   line is taken once, at the batch's first on-line verb (`cmd/nova-sprint/serve.go:247-256`,
   `a.serial.LockCtx(ctx)`), and after that the loop runs every later verb with no `ctx.Err()`
   check (`:257-261`); a caller that dropped mid-batch still has its writes committed. The
   spec says the opposite: "a caller that has gone ... has the verbs of its batch not yet run
   answered exit 2, not run, and nothing is changed by them"
   (`docs/SPEC-SPRINT.md:6283-6285`), and `goneResult` exists for exactly that answer
   (`cmd/nova-sprint/servelanes.go:152-160`). The only test of a gone caller
   (`cmd/nova-sprint/servelanes_test.go`) exercises the wait for the line, not a
   cancellation after the line is taken.
   Evidence: the loop above takes the line only once (`if !held`) and never reads `ctx`
   again; the gone path is entered only when `LockCtx` itself returns an error (`:248-254`),
   never for a context that expires while the batch holds the line.
   Fix: at the top of each iteration, when `ctx.Err() != nil`, answer the remaining verbs
   with `goneResult` and stop.

5. **URGENT — `member.PathsProposed` drops every proposed path after the first item that
   carries prose, so the sprint's `paths` rule widens from a truncated list.**
   `pkg/member/member.go:1751-1771`, the `break` at `:1767`; the sprint rule reads the
   line through it at `internal/sprint/paths_proposed.go:94` (`heldProposal`). The loop
   appends an item's first word and then stops the whole line on `len(words) > 1`, so
   `PATHS-PROPOSED: a.go reason, b.go, c.go` reads as `[a.go]` and `b.go` never reaches the
   twin's brief (`WidenBrief`, `paths_proposed.go:142`) or `member.CarryProposed`
   (`pkg/member/member.go:1777-1787`), while the contract says the reader takes each
   comma-separated item's path up to its first whitespace, dash or semicolon and reads the
   rest as prose (`docs/SPEC-CARD-CONTRACT.md:394-398`). The command's own sibling reader
   (`cmd/nova-sprint/recut_widen.go:115-143`) does not stop, so the same line answers
   differently depending on which path parsed it.
   Evidence — a scratch test on the bench ran
   `go test -count=1 -timeout 120s -run TestAuditPathsProposedTruncates -v ./pkg/member/`
   and printed `"PATHS-PROPOSED: a.go reason, b.go, c.go" => [a.go] ok=true` and
   `"PATHS-PROPOSED: a.go, b.go reason, c.go" => [a.go b.go] ok=true`.
   Fix: read each comma-separated item's path independently (the `proposedPath` rule in
   `recut_widen.go`) and never break the whole line on prose; give both callers one reader.

6. **NEXT — `sprintwire.Worker.Run` panics on an answer with no result or a nil error.**
   `pkg/sprintwire/worker.go:57-68`: the loop `for try := 0; try < Tries && ctx.Err() == nil` can be left unentered (a non-positive `Budget` makes `context.WithTimeout` already
   done), so `err` stays `nil` and `:68` calls `err.Error()`; and `:62` (`r := res[0]`)
   indexes the answer before checking it is there, so a `Send` returning `(nil, nil)` or an
   empty slice panics. `Send` is a public field with no stated length contract and tests pass
   their own functions (`pkg/sprintwire/worker_test.go`); production is safe only
   because `Client.Do` enforces the length (`pkg/sprintwire/wire.go:95-104`).
   Evidence: the two lines and the loop guard; no `Budget` setter in the tree passes a
   negative value (`cmd/nova-swarm/member.go:207` leaves it zero, which is `Timeout`).
   Fix: check `len(res) > 0` before `res[0]` and set `if err == nil { err = ctx.Err() }`
   before `:68`, returning exit 2 with a named error.

7. **NEXT — the friend beat's `paced` and `window` fields are dead.** `internal/sprint/presence.go:107-111`
   declares `Paced *int` and `Window string` on `sprint.FriendReport`, but no producer sets
   them and no reader reads them: the `friend beat` verb has no `--paced`/`--window` flag
   (`cmd/nova-sprint/friends.go:360-374`), `FriendBeatProof`'s two report guards never test
   them (`internal/sprint/store/friends.go:305-310`), and the only `.Paced` assignment in
   the tree is a different struct (`pkg/friend/lanes.go:703`, `pkg/friend.Status`).
   So a beat can never carry a paced width, and the fields exist only on paper (perhaps to
   read records a former build wrote).
   Evidence: `grep -rn "\.Paced"` returns only `pkg/friend/lanes.go:703`, and
   `grep -rn '"--paced"\|--paced' cmd/nova-sprint pkg/friend internal/sprint` returns
   nothing.
   Fix: remove the two fields (and the comment) or add the `--paced`/`--window` flags and
   carry them into `FriendReport`, so the declaration and the live path agree.

8. **NEXT — the server spec's friend-beat flag list is stale.** `docs/SPEC-SPRINT.md:6357`
   says the server runs `friend beat <friend>` "with its report's flags (`--running`,
   `--working`, `--queue`, `--width`, `--load`), each once with its value, and nothing
   more", but `friendBeatFlags` (`cmd/nova-sprint/serve.go:415-430`) also accepts `--active`,
   `--check`, `--pong`, `--run`, `--until` and `--reason`, and `friendBeatReport`
   (`:462-477`) requires them, each once with its value. The list the spec holds a reader to
   is not the set the server takes, and a reader who believes it cannot see how the daemon's
   proof words reach the beat lane.
   Evidence: the spec line against the map and its validator; the flags are also named in the
   section 1 help (`cmd/nova-sprint/friends.go:370-374`), so the code is the complete side.
   Fix: name every flag the server accepts in the server section (or state that the friend's
   report's flags are section 1's and this line is only an example).

9. **NEXT — `isHold` still reads the word HOLD anywhere in a failed report.**
   `internal/sprint/widen.go:139-144`. It first asks `CollectVerdict` (`:140`), but the
   fallback `slices.ContainsFunc(strings.Fields(report), ... == "HOLD")` treats any field
   that trims to HOLD as the verdict, so a report whose `Verdict:` line is FAIL and whose
   prose says, for example, "the earlier HOLD was wrong" is a HOLD to the widen rule once it
   also names `PATHS` (`widenAnswers`, `internal/sprint/widen.go:190`). A failed attempt's
   prose then edits the card's brief in place instead of being left for a mind.
   Evidence: the fallback and its use at `:190`; the contract reads a report's verdict from
   its `Verdict:` line (`docs/SPEC-CARD-CONTRACT.md:256-259`), and `CollectVerdict`
   (`internal/sprint/collect.go:123-141`) is the contract's one verdict reader.
   Fix: read the verdict with `CollectVerdict` alone (or accept only an explicit `HOLD:`
   reason line), and drop the word scan.

10. **NEXT — the twin's id counter drops its parse error and can hand an id out twice.**
    `cmd/nova-sprint/twin.go:146-149`: `n, _ = strconv.Atoi(v)` turns a corrupt `twin:ids`
    value into `0`, the next id is `t1` again, and the store, which takes an operation id it
    has seen as the same operation, returns the old recorded result and changes nothing. The
    function's own comment (`:126-131`) says a counter not kept "would hand the same id out
    again", but only `SetKey` failures reach `t.idErr`.
    Evidence: the ignored `Atoi` error at `:148`; a twin file whose counter key is not a
    whole number (an older format, a hand edit) reaches this on the next verb.
    Fix: report a counter that is not a whole number through `t.idErr` and refuse the verb
    rather than defaulting to zero.

11. **NEXT — `serialLock`'s comment names the server's line and cites a property that does
    not exist.** `cmd/nova-sprint/seriallock.go:8-16` calls `serialLock` "the server's one
    line of control (app.serial)" and cites `tla/ServerLanes.tla, AbandonedNeverRuns`. Since
    the tick's turn landed, `app.serial` is `controlLine` over `sprint.ControlLine`
    (`cmd/nova-sprint/main.go:236-297`), and `serialLock` is only the read lane's own line
    (`cmd/nova-sprint/servelanes.go:75,143`). The model property is `GoneNeverRuns`
    (`tla/ServerLanes.tla:204`); there is no `AbandonedNeverRuns` in the model.
    Evidence: the comment, the two types, and `grep -n "AbandonedNeverRuns" tla/ServerLanes.tla`
    returning nothing while `GoneNeverRuns` is at `:204`.
    Fix: rewrite the comment as the read lane's lock (or delete `serialLock` and use the
    shared `sprint.ControlLine`), and cite `GoneNeverRuns`.

12. **NEXT — a removal kept for a dirty clone still walks the whole tree.** `internal/sprint/gc.go:572-594`:
    when `Dirty` reports a clone with work that is nowhere else, the walk sets `keep` at
    `:586-587` and returns `nil` (`:588`), so `filepath.WalkDir` keeps reading every file and
    directory of a tree it will not remove, only to discard `size` at the early return
    (`:590-594`). On a job or bench tree that is the whole clone read for nothing, on every
    machine, on every pass.
    Evidence: the callback returns `nil` (never `fs.SkipAll`) and the only use of `size` is
    the removal path below `:594`.
    Fix: return `fs.SkipAll` from the callback once `keep` is set (or check `p.r.Dirty`
    before the sizing walk and skip it when the removal is kept).

urgent=5 next=7
