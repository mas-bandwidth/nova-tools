# Cold audit: nova-sprint

Read at this base's tip: `cmd/nova-sprint`, `internal/sprint` (core and its `store`),
`internal/sprintdash`, `pkg/sprintwire`, `docs/SPEC-SPRINT.md`,
`docs/SPEC-CARD-CONTRACT.md`, and the sprint models under `tla/` (ServerLanes,
DirtyTick and DirtyTickRead, FriendPresence, Level, ReadsByRoom, CardContract).
Nothing was changed; every command ran against an in-memory snapshot or a scratch
store, never a live store or server. The tree's own unit tests pass
(`./internal/sprint/...`, `./pkg/sprintwire/...`, `./internal/sprintdash/...`),
`go vet` is silent and `staticcheck` reports no functional finding, so these are the
places the tests do not reach.

1. **URGENT — a reader on a drained machine panics the tick with an integer divide by
   zero.** `internal/sprint/readers.go:662` (`readerRoom.share`), reached from
   `internal/sprint/readers.go:726` (`levelReads`) and `internal/sprint/readers.go:615`
   (`TickLevelReads`, a tick-start part at `internal/sprint/steps_tick.go:289`).
   What is wrong: `share` divides by `r.width`, and a machine reader's width is its
   fleet row's (`ReaderWidth`, `internal/sprint/readers.go:627`), which is `0` for a
   draining member (`MemberWidth`, `internal/sprint/width.go:50-58`), so the reader
   level's source scan divides by zero whenever a drained member's reader still holds
   an asked read and at least one other reader is up.
   Evidence: a scratch unit test on the in-memory world (`reader-m1` and `reader-m2`
   of width 4, four flash primaries, `TickAsk` then `FleetStep{Op: "up", Member: "m1", Drain: true}`, then `TickLevelReads`) run with
   `go test -count=1 -timeout 120s -run TestAuditWidthZeroReaderLevelPanics ./internal/sprint/`
   printed `reader-m1 load=2 reader-m2 load=2` and then
   `panic: runtime error: integer divide by zero`, with
   `readerRoom.share(...) readers.go:662` called from `levelReads(...) readers.go:726`
   and `TickLevelReads(...) readers.go:615`. The drain is a supported verb
   (`cmd/nova-sprint/width_test.go:32`, `cmd/nova-sprint/fleet_drain_test.go`), and no
   test in `internal/sprint` covers a drained *reader*; the scratch test was removed
   before the commit, so nothing of it is in this report's branch.
   Fix: in `readerRoom.share`, return an unreachable share for `width <= 0` (`return -1`),
   or drop width-0 readers from the level's `up` list and take their asked reads back as
   an away reader's.

2. **NEXT — a worker's `progress` and `queue` are retried without the operation id the
   spec says every lost write carries.** `pkg/sprintwire/worker.go:47`.
   What is wrong: the id is appended only for `take`, `finish` and `read`,
   while `docs/SPEC-SPRINT.md` section 14 ("A worker whose answer was lost sends the verb
   again with the same operation id (`--op`)") and the `Worker` comment at
   `pkg/sprintwire/worker.go:33-35` ("A write carries one operation id through
   them") promise it for every write, and the server runs `progress` and `queue` as
   line-taking writes (`cmd/nova-sprint/serve.go:100-101`).
   Evidence: read `worker.go:47` beside the two promises;
   `pkg/sprintwire/worker_test.go:40` pins the gap for `queue` ("a read is sent as
   given, with no operation id") and `worker_test.go:57` pins the id for the other
   three. Both verbs are idempotent in effect (a progress stamp is rewritten, a queue
   beat re-recorded), so no result is lost; the promise is what does not hold.
   Fix: add `progress` (and `queue`) to the id list at `worker.go:47`, or narrow the
   spec line and the comment to the three commits that take an id.

3. **NEXT — `Worker.Run` can panic on a `Send` that returns no result, and dereferences
   a nil error when the retry loop never runs.** `pkg/sprintwire/worker.go:62`
   (`r := res[0]`) and `pkg/sprintwire/worker.go:68` (`err.Error()`).
   What is wrong: the loop indexes the answer before checking it is there, and on the
   exhausted path it calls `err.Error()` without checking `err` is non-nil; a `Send`
   that returns `(nil, nil)` (or an empty slice with no error) panics on `res[0]`, and
   a context already expired before the first send panics with a nil-pointer
   dereference.
   Evidence: the two lines; `Client.Do` does enforce the length
   (`pkg/sprintwire/wire.go:95-104`), so the production path is safe today, but
   `Send` is a public field with no stated length contract and tests pass their own
   functions (`pkg/sprintwire/worker_test.go:23-30`).
   Fix: check `len(res) > 0` before `res[0]` and return exit 2 naming the malformed
   answer, and check `err != nil` before `err.Error()`.

4. **NEXT — the friend finish's refusals name no remedy, and two are capitalized
   error strings.** `cmd/nova-sprint/friendcards.go:239`, `:246` and `:248`.
   What is wrong: `friendFinish` returns "the card names no REPO: line, so origin's tip
   of %s cannot be read" and the two "Head ..." refusals with no `run: ...` remedy the
   onboarding standard asks for (docs/STANDARD.md, "every refusal carries a
   breadcrumb"), and the two `Head` lines begin a capitalized error string.
   Evidence: `staticcheck ./cmd/nova-sprint/...` prints
   `cmd/nova-sprint/friendcards.go:246:14: error strings should not be capitalized (ST1005)`
   and `cmd/nova-sprint/friendcards.go:248:14: ... (ST1005)`; the caller
   `cmd/nova-sprint/collect.go:195` prints the error verbatim as
   `COLLECT <card> REFUSED <error>` and adds nothing.
   Fix: lowercase both and append a remedy (for example, run the card verb that reads
   the branch and REPO line the report must name).

5. **NEXT — `TestServerSwitchRunsAShadowTickAndRefusesABrokenBinary` can fail on a
   just-written candidate under load.** `cmd/nova-sprint/server_switch_shadow_test.go:59-62`.
   What is wrong: the test writes the candidate script and immediately has
   `server switch` exec it; when the filesystem has not released the write, the exec
   fails with `text file busy` and the test's assertion about the shadow tick's reason
   does not match.
   Evidence: one full `go test -count=1 -timeout 900s ./cmd/nova-sprint/` run failed at
   `server_switch_shadow_test.go:65` with
   `fork/exec .../candidate: text file busy` in the subtest "a broken binary is refused
   and the old server keeps running"; `go test -count=3 -run TestServerSwitchRunsAShadowTickAndRefusesABrokenBinary ./cmd/nova-sprint/` then
   passed 3/3, and a second full run of the package ended `ok ... 33.464s`. The
   product's refusal (the candidate could not run) is correct; the test's expectation is
   what is not deterministic.
   Fix: retry the candidate exec on `ETXTBSY` in the test's helper (or wait for the
   write to be released) before asserting the reason, or drop `t.Parallel()` there.

Checked without a finding, for the reader's sense of coverage: the tick deadline
stretch and the wedged-tick exit (`cmd/nova-sprint/run.go`), the ready buffers
(`cmd/nova-sprint/reads.go:1178-1179`), the drain's deal and level paths for work
cards (`internal/sprint/steps_work.go`), the presence and session-proof rules
(`internal/sprint/presence.go`), the timers, the stream archive and remove rules,
the store's fence acquire/lock/relock (`internal/sprint/store/lock.go`), the twin's
catch-up and receipt application (`internal/sprint/store/twin.go`), and the dashboard's
release view and event stream (`internal/sprintdash`). Their arithmetic guards
(`rate.go` `h > 0`, `cost_reconcile.go` `rec.Internal <= 0` and `rec.Used > 0`,
`routetable.go` `m[1] > 0`) are present, and the remaining panic sites are init-time
schema checks or the reference model's own.

urgent=1 next=4
