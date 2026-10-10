# nova-tools — cold audit, dsh (DeepSeek Harness), 2026-10-06

Base: `sprint/mechanical-2026-10-02` at `ad2f20c6bc71f6769ad464fe96586c57c0c5fb11`.
Scope: nova-tools without the sprint — every command and internal package, the fleet
plays, the docs tree and their TLA+ models. The code was read as a stranger with the
specs beside it, and every command below was run on a remote bench with `GOCACHE` warm,
`GOFLAGS=-mod=readonly` and `NOVA_TEST_NO_HOST=1`. No code was changed.

Two gates are red on this base and are the URGENTs: the unit suite (`internal/check`)
and the certification race suite (`pkg/bus`, `cmd/nova-friend`). The rest is model
and comment drift found beside the code.

---

1. **`internal/check/attest_test.go:292` (`dispatchRE`) and `:294` (`TestRecordLayerCheckCountMatchesSPEC`) —
   the SPEC/verb agreement test still parses the dispatch switch that `cmd/nova-check/main.go` no longer has, so it fails for every check.**
   The regexp `^\s*case "([a-z-]+)":\s*\n\s*return cmd` reads `../../cmd/nova-check/main.go` (`:320`), but the sprint's `sk-check: skeleton` (commit `bd28d20d8`, 2026-10-06, not in `origin/dev`) replaced that `case` switch with a `tool.Tool{Verbs: []tool.Verb{...}}` table (`cmd/nova-check/main.go:75-90`, `cmd/nova-check/verbs.go`), so `verbNames` is empty and every SPEC subsection is reported as undispatched.
   *Evidence:* `go test -count=1 -timeout 600s ./internal/check/` on the bench → `--- FAIL: TestRecordLayerCheckCountMatchesSPEC`, ten lines of `SPEC.md has a nova-check subsection "…" that ../../cmd/nova-check/main.go does not dispatch` (links, kernel, hygiene, convergence, spelling, attest, nocode, floors, corpus, dogfood), and `FAIL github.com/mas-bandwidth/nova-tools/internal/check`. `git show origin/dev:cmd/nova-check/main.go | grep 'case "'` shows the old switch at dev; `git merge-base --is-ancestor bd28d20d8 origin/dev` is false.
   *Grade:* **URGENT** — the whole unit suite is red on the base, so v1.1.0 cannot be cut green, and the guard that holds SPEC.md's check list equal to the binary's verbs now holds nothing (it would also stay green through a real drift).
   *Fix:* read the verb names from the new table — parse the constructors in `novaCheck`'s `Verbs:` block (`quickstartVerb()` … `spellingVerb()`, `main.go:75-90`), or move the agreement check into `cmd/nova-check` where the table lives.

2. **`pkg/bus/bus_test.go:18-27` (`rig`) with `pkg/bus/token_test.go:195-210` (`TestRacingRetriesMakeOneMessage`) —
   the rig's fake `Rand` mutates one shared byte from eight goroutines, a data race under `-race`.**
   `rig` creates `n := byte(0)` (`bus_test.go:21`) and its closure does `n++` / `b[i] = n` (`:24`) with no lock; `TestRacingRetriesMakeOneMessage` starts eight goroutines that each call `b.Send` (`token_test.go:201-203`), and `Bus.Send` calls `ulid`, which calls `b.Rand` (`bus.go:328`, `bus.go:708`).
   *Evidence:* `go test -race -count=1 -timeout 300s ./pkg/bus/` on the bench → two `WARNING: DATA RACE` reports, both at the rig's fake `Rand` closure (`bus_test.go:24`), reached through `Bus.ulid` (`bus.go:708`) from `TestRacingRetriesMakeOneMessage` (`token_test.go:203`); `FAIL github.com/mas-bandwidth/nova-tools/pkg/bus`.
   *Grade:* **URGENT** — the certification workflow runs `go test -race` over the live tree (`internal/ci/cert_race_shards_class_test.go`), so this reddens the v1.1.0 race gate.
   *Fix:* guard the rig's counter with a mutex (or `atomic`) so the fake `Rand` is safe for concurrent `Send`, keeping the ids reproducible.

3. **`cmd/nova-friend/main_test.go:95` (the `now` seam) with `:157-166` (`stopAfter`) —
   the fake clock writes `r.now` and `r.answered` unlocked while the daemon's own goroutines call it, a data race under `-race`.**
   The seam is `now: func() time.Time { r.answerChecks(); r.now = r.now.Add(time.Second); return r.now }` (`:95`), and `answerChecks` fills `r.answered` and sends on the store (`:132-152`); the sprint's harness watch and limit watch call `Now` from goroutines the daemon starts (`pkg/friend/alive.go:441-450`, `pkg/friend/limit.go:283-289`).
   *Evidence:* `go test -race -count=1 -timeout 600s ./cmd/nova-friend/` on the bench → ten `WARNING: DATA RACE` reports whose first frame is `cmd/nova-friend.(*rig).world.func4() main_test.go:95`, read from `TestRunKeepsTheHarnessWatchAdvisory.stopAfter.func3` (`main_test.go:160`, test at `:1066`) and written from `pkg/friend.(*Limits).see` (`limit.go:289`) through `(*OpenCode).Deliver` (`adapter.go:317`); `FAIL github.com/mas-bandwidth/nova-tools/cmd/nova-friend`.
   *Grade:* **URGENT** — same certification race gate; the new `TestRunKeepsTheHarnessWatchAdvisory` (not present at `origin/dev`) is the trigger.
   *Fix:* make the rig's clock and `answered` map goroutine-safe (a mutex around `now` and `answerChecks`, or an atomic clock), so the daemon's goroutines can call the seam.

4. **`tla/BusCursor.tla:3-4` and `tla/README.md:33` —
   the model cites pkg/bus files that were deleted three days before the model landed.**
   The header says it models `pkg/bus (cursor.go:1-25, :401-417 WriteCursor, :611-612 ReadOpen; lock.go:1-30; conflict.go:41-55; protocol.go; git.go)`; commit `84d517dfb` (2026-10-04), whose subject names that bus's removal, deleted the commit-cursor bus and all those files, and `docs/SPEC-BUS.md:8` records that nova-bus took its name when that bus was removed.
   *Evidence:* `ls pkg/bus` holds no `cursor.go`, `lock.go`, `conflict.go`, `protocol.go` or `git.go`; `git show --stat 84d517dfb` is the removal; `tla/RUNS.tsv:440-443` still records the `MCBusCursor*` TLC runs as PASS on 2026-10-07; `tla/README.md:33` describes the model in the present tense.
   *Grade:* **NEXT** — a live, TLC-registered model of removed code is dead weight and a false claim about what `pkg/bus` contains.
   *Fix:* retire it (delete `tla/BusCursor.tla`, the `tla/MCBusCursor*.cfg`/`.tla` files and the README/RUNS/COVERAGE rows) or rewrite it against the Redis-stream bus that replaced the removed one.

5. **`tla/Bus2.tla:1-2` —
   the delivery model's header names a spec and package that do not exist.**
   Line 2 reads `nova-bus2's delivery machine (…; …bus2.go: Send, Recv, AckEntry and Ack; …)` and gives the spec as `SPEC-BUS2.md` and the package as `bus2`; neither is in the tree, because the bus was renamed to `pkg/bus` and specced by `docs/SPEC-BUS.md` on 2026-10-04 (`docs/SPEC-BUS.md:7-8`), and the same model's README row already points at `docs/SPEC-BUS.md, pkg/bus` (`tla/README.md:32`).
   *Evidence:* `grep -rn 'SPEC-BUS2'` returns only `tla/Bus2.tla:2`; the spec file `SPEC-BUS2.md` and the package directory `bus2` do not exist anywhere in the tree, while `pkg/bus` is the live package.
   *Grade:* **NEXT** — the index and the model disagree about what the model describes.
   *Fix:* retarget the header to `docs/SPEC-BUS.md` and `pkg/bus` (or retire the model with its `MCBus2*` wrappers).

6. **`pkg/secrets/model_invariants_test.go:3` —
   the test says the TLA+ module is "beside this package", but it is only on an unmerged branch.**
   The comment reads "The TLA+ module `tla/SecretsSeat.tla` beside this package states the design as state…", and the test replays `MCSecretsSeatBrokenReceiptByValue.cfg`, `MCSecretsSeatBrokenSeatAddReplaces.cfg` and other witnesses (`:230`, `:301-305`, `:368`, `:414`, `:505`), but no `tla/SecretsSeat.tla` and no `tla/MCSecretsSeat*.cfg` exist at this base. `pkg/secrets/gate.go:227-228,305` and `docs/SPEC-SECRETS.md:426` name the branch `sprint/md-secrets-h.w1.g1.e15`; the test comment does not.
   *Evidence:* `find . -name 'SecretsSeat*'` at the base → nothing; `git ls-tree -r --name-only origin/sprint/md-secrets-h.w1.g1.e15 | grep SecretsSeat` lists `tla/SecretsSeat.tla` and the eight `MCSecretsSeat*` files; `git merge-base --is-ancestor origin/sprint/md-secrets-h.w1.g1.e15 HEAD` is false (merge base `08ff859c4`).
   *Grade:* **NEXT** — the test's premise is false on the branch it ships on; the model and its witnesses must be landed or the comment must say where they are.
   *Fix:* name the branch in the comment (as `gate.go` and `SPEC-SECRETS.md` do), or land `tla/SecretsSeat.tla` and its MC configs with the package.

7. **`pkg/friend/stage.go:676` —
   the comment cites `tla/JobWorktrees.tla`, but the model is at `pkg/friend/tla/JobWorktrees.tla`.**
   Line 676 reads `witness "async" of tla/JobWorktrees.tla)`, while the same file's line 39 correctly says `pkg/friend/tla/JobWorktrees.tla`.
   *Evidence:* `ls tla/JobWorktrees.tla` → no such file; `ls pkg/friend/tla/JobWorktrees.tla` → present.
   *Grade:* **NEXT** — a dead path in a citation a reader cannot follow.
   *Fix:* change the line to `pkg/friend/tla/JobWorktrees.tla`.

8. **`internal/selftalk/selftalk_test.go:186-211` (`TestScanAllocatesAFewTimesTheInputNotOneIntPerByte`) —
   the allocation budget does not hold under the race build and the test gets pathologically slow there.**
   The test asserts `delta < len(text)*8` over a 4 MiB input; under `-race` the measured `TotalAlloc` is 13.6x the input, and a focused race run of the same test hit a 300 s timeout inside `regexp.(*Regexp).FindString` from `ScanInstallation` (`installation.go:135`).
   *Evidence:* the whole race suite on the bench → `internal/selftalk: "57040760" is not less than "33554432" … scan allocated 57040760 bytes for 4194304 of input (13.6x), want under 8x`; `go test -race -count=1 -timeout 300s -run TestScanAllocatesAFewTimesTheInputNotOneIntPerByte ./internal/selftalk/` → `FAIL … 300.069s` with the goroutine parked in `FindString`/`classify` (`installation.go:135`).
   *Grade:* **NEXT** — the certification race shard runs this package under `-race`, where the bound and the leg's cap do not hold; the non-race run is green.
   *Fix:* skip or relax the budget under the race build (a `//go:build race` helper that reports itself), or measure with `testing.AllocsPerRun`, which reports the code's allocations rather than the race instrumentation's.

---

urgent=3 next=5
