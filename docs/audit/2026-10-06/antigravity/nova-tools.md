# nova-tools — cold audit, antigravity, 2026-10-06

Base: `sprint/mechanical-2026-10-02` at `1b559077e0cb9ee9e14efe2910743cc4cff45ab9`.

Scope: nova-tools without the sprint — every command and internal package, the fleet
plays, the docs tree and their TLA+ models. The code was read as a stranger with the
specs beside it. Every command below was run in a scratch checkout on a remote bench
with a warm `GOCACHE`, `GOFLAGS=-mod=readonly` and `NOVA_TEST_NO_HOST=1`; no live store,
server or secret was touched, and no code was changed.

Six gates are red at this tip: the onboarding class test, the dead-code class test, the
functional and slow vet compiles, the Windows cross-vet, the certification race run in
`cmd/nova-config`, and the unit-tier one-typed-parser guard. The rest is a stale step
list, a stale tool count, a spec that promises two evidence places the daemon never
reads, and two model headers that cite code deleted days before the model landed.

---

1. **`internal/ci/onboarding_functional_test.go:71,79-93,118,122` — `TestEveryCommandMeetsTheOnboardingStandard` is red for `nova-up` and `nova-doctor`: the two tools have no `## <tool>` section in `docs/TESTS.md`, their README sentence is not their help's line 1, and `nova-doctor` also runs bare and offers one example line.**
   The test walks every directory under `cmd/` and holds it to the onboarding standard
   (`docs/STANDARD.md` point 5(c) and `docs/ONBOARDING.md` point 6). For `nova-up` it
   fails `:71` (no `## nova-up` section — `docs/TESTS.md` has `## nova-update` but no
   `## nova-up`) and `:122` (`README.md:47` says "set nova up on one machine: plan every
   step, then apply, from nothing to a first sprint" while `internal/up/tool.go:18`'s
   `What` is "sets up nova on one machine, from nothing to a first sprint"). For
   `nova-doctor` it fails `:71` (no `## nova-doctor` section), `:79-93` (a bare
   `nova-doctor` runs the checks: `internal/doctor/doctor.go:209` sets `Default: "run"`
   and `:261-263` rewrites no arguments to `run`, so it prints `DOCTOR ...` lines to
   stdout and exits by the worst check instead of refusing at exit 2 on stderr with
   `run: nova-doctor help`), `:118` (`internal/doctor/doctor.go:219,221` gives the verb
   one example line, `example: nova-doctor --local`, where the standard wants at least
   three), and `:122` (`internal/doctor/doctor.go:207` says "says what is missing for the
   nova tools to work, and the one line that fixes each" while `README.md:46` says "says
   what is missing for the nova tools to work here and, for each thing, the one line that
   fixes it").
   *Evidence:* `go test -tags functional -count=1 -timeout 900s ./internal/ci/` on the
   bench → `--- FAIL: TestEveryCommandMeetsTheOnboardingStandard/nova-up` and
   `.../nova-doctor`, `FAIL github.com/mas-bandwidth/nova-tools/internal/ci`; the
   nova-doctor failure text is `a bare `nova-doctor` wrote to stdout: "DOCTOR gosdk ok ..."` and `"" does not contain "run: nova-doctor help"`; `grep -n '^## nova-up\b\|^## nova-doctor\b' docs/TESTS.md` prints nothing (the file's sections run `## nova-bus` … `## nova-card`).
   `git blame` puts `Default: "run"` at `5a1b3c1cc` (2026-10-05).
   *Grade:* **URGENT** — the `onboarding` class rule is one of the ten never to break, and
   the functional job that runs it (`.github/workflows/ci.yml:927`) gates a stream's merge
   into `dev`; a v1.1.0 cut cannot carry it red.
   *Fix:* add `## nova-up` and `## nova-doctor` "First run" sections to `docs/TESTS.md`,
   make each tool's `What` and the README cell one sentence, give `nova-doctor` a
   three-line `example:` block, and drop its default verb so a bare command refuses in one
   line naming `nova-doctor help`.

2. **`internal/ci/dead_code_class_test.go:150-201` with `internal/ci/testdata/dead_code_allowlist.txt` — `TestDeadCode` is red: the shrink-only ledger has 14 packages wrong, and the class test refuses both the new dead code and the rows that shrank.**
   `findDeadCodeUnion` runs the go.mod-pinned `deadcode` (v0.50.0, go1.26.6) over
   `./cmd/...` for linux, darwin and windows and unions what is unreachable. The ledger
   (18 rows) is stale: `cmd/nova-check 2`, `cmd/nova-sprint 29`, `pkg/bus 2`,
   `internal/cardgen 1`, `pkg/friend 14`, `pkg/hostload 3`, `pkg/release 1`
   and `pkg/tlc 36` are not listed at all; `pkg/secrets` measures 3 over a ledger
   of 1, `internal/sprint` 51 over 2, `pkg/swarm` 40 over 39; `pkg/filelock` and
   `pkg/gitrun` measure 1 below their ledger of 2; and `internal/workgh` is a stale
   row (0 measured). The named functions include the half-landed adoption feature
   (`cmd/nova-sprint`: `adoptSteps.Ask`, `adoptSteps.BaseTip`, `AdoptPass.say`,
   `Adoption.push`) and the test/lane helpers in `pkg/friend` (`Batch`, `JobName`,
   `MemFS.Lstat`, `MemFS.MkdirAll`).
   *Evidence:* `go test -tags functional -count=1 -timeout 900s ./internal/ci/` on the
   bench → `--- FAIL: TestDeadCode (49.36s)`, `assert.Failf(t, "dead code rule", …)` at
   `dead_code_class_test.go:199`, and the 14 `… unreachable functions …` lines above;
   `go tool -n deadcode` resolves
   `golang.org/x/tools/cmd/deadcode v0.50.0`, the version `go.mod` pins.
   *Grade:* **URGENT** — the same functional job as finding 1 gates the merge into `dev`
   with the dead-code rule, and the ledger is shrink-only, so the tree cannot land green
   until the new functions are wired or deleted and the two shrunken rows lowered.
   *Fix:* wire or delete the newly unreachable functions (the adoption cluster first),
   then `NOVA_CI_UPDATE=1 go test -tags functional -run '^TestDeadCode$' ./internal/ci/`
   to lower `pkg/filelock` and `pkg/gitrun` and drop the `internal/workgh` row.

3. **`cmd/nova-swarm/mirror_test.go:17-19` against `cmd/nova-swarm/slow_helpers_test.go:104-113` — two test files declare the same `recordGit`, so `make vet-functional` and `make vet-slow` are red.**
   `mirror_test.go` (untagged) declares `type recordGit struct` with `run(ctx, dir, args...)`;
   `slow_helpers_test.go`, behind `//go:build slow || functional`, declares a second
   `type recordGit struct` with `run(ctx, gitrun.Options, args...)`. The untagged file is
   always compiled; the tagged file joins it under either tag, so the package has two
   declarations. `git blame` puts `mirror_test.go`'s copy at `a8a1c2af4` (2026-10-07
   12:36, "retire the coordinator stopgaps: loop run, mirror verb, disk-guard stop floor")
   and the tagged copy at `0ab8c0a29` (2026-10-06).
   *Evidence:* `go vet -tags functional ./...` on the bench →
   `vet: cmd/nova-swarm/slow_helpers_test.go:106:6: recordGit redeclared in this block`,
   exit 1; `go vet -tags slow ./...` prints the same line; the plain `go vet ./...` is
   green because the tag is what pulls the second file in.
   *Grade:* **URGENT** — `Makefile:162` and `Makefile:170` are the `vet-functional` and
   `vet-slow` steps of the per-change lint job, so every CL is red.
   *Fix:* rename the untagged unit fake (or the tagged helper) — a `recordGitUnit` /
   `recordMirrorGit` pair — so the two build sets cannot collide.

4. **`cmd/nova-config/looprun_test.go:265` — `syscall.Kill` is Unix-only and has no build tag, so the Windows cross-vet `make vet-windows` is red.**
   The test file begins at `package main` with no constraint and calls
   `syscall.Kill(pid, syscall.SIGKILL)` at `:265` (and `syscall.Kill(pid, 0)` at `:268`).
   The file imports `runtime` and skips its body when `runtime.GOOS != "linux"`, but a
   runtime `if` does not remove the symbol at compile time, and `syscall.Kill` is not
   defined for `GOOS=windows`.
   *Evidence:* `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...` on the bench →
   `# github.com/mas-bandwidth/nova-tools/cmd/nova-config` /
   `vet: cmd/nova-config/looprun_test.go:265:16: undefined: syscall.Kill`, exit 1.
   `Makefile:215` is the target and `.github/workflows/ci.yml:314-319` runs it in the lint
   job; the comment there calls it "the one Windows guard on the CL path".
   *Grade:* **URGENT** — the Windows cross-vet runs in the per-change lint job on every
   CL, so a GOOS=windows compile break is a red gate before any v1.1.0 cut.
   *Fix:* move the Unix-only test (or a `killTestProcess(pid)` helper) into
   `looprun_unix_test.go` behind `//go:build unix`, with a Windows no-op or an
   `os.Process.Kill` equivalent.

5. **`cmd/nova-config/looprun_test.go:255,268` — the test reads a `bytes.Buffer` while the child process is still writing it, a data race under `-race`.**
   `wrapper.Stderr = &werr` (`:254`) hands the buffer to `os/exec`, whose copier goroutine
   writes it, and `require.NoError(t, syscall.Kill(pid, 0), "… %s", werr.String())`
   (`:268`) evaluates `werr.String()` on the test goroutine before `wrapper.Wait()` has
   run. `git blame` puts the whole test on `31b78df6f` (2026-10-07 14:27).
   *Evidence:* `go test -race -count=1 -timeout 900s ./cmd/nova-config` on the bench →
   `WARNING: DATA RACE`, `Read at … by goroutine 45: bytes.(*Buffer).String() … cmd/nova-config.TestLoopRunWrapperDeathEndsTheCommandAndTheLock() looprun_test.go:268` against `Previous write … bytes.(*Buffer).grow() … os/exec …`; the
   package prints `FAIL github.com/mas-bandwidth/nova-tools/cmd/nova-config` and every
   other parallel test in it also reports `race detected during execution of test`.
   *Grade:* **URGENT** — the certification race shard runs `-race` over the live tree, and
   a race-detector hit reddens the whole `cmd/nova-config` leg at a v1.1.0 cut.
   *Fix:* do not attach the live buffer to `Stderr`; capture the child's output after
   `wrapper.Wait()` (or use a mutex-guarded writer), and build the failure message from
   static fields.

6. **`pkg/typedrec/oneparser_test.go:667-716` — `TestOneTypedParser/tree` is red: four new bare-token parsers in the sprint's brief code are outside the one-typed-parser home.**
   The tree walk flags a function that compares a line's token to a bare word, cuts its
   prefix or builds a token table outside `pkg/typedrec` and the allowlist. The four
   new hits are `cmd/nova-sprint/add.go:148` (`k == "PATHS"`, a bare comparison), and
   `internal/sprint/brief_defect.go:47` (a token-table entry), `:72`
   (`reason == BriefDefectPaths`, a comparison against a token constant) and `:95`
   (`strings.CutPrefix(line, "PATHS:")`, a prefix cut). All four lines are from
   `231f2f64c` ("sprint: admit briefs against the base tree", 2026-10-07 18:04), landed
   as `a9fe2a7fd`.
   *Evidence:* `go test -count=1 -timeout 600s ./pkg/typedrec` on the bench → `--- FAIL: TestOneTypedParser/tree`, `found 4 unexpected RESULT parser hit(s)` at
   `oneparser_test.go:712`, then the four `file:line fn form tok shape` lines; the package
   prints `FAIL github.com/mas-bandwidth/nova-tools/pkg/typedrec`.
   *Grade:* **URGENT** — the unit tier is red at the base, so the required gate for this
   card (and every card) cannot be green, and the one-typed-parser rule is the guard that
   keeps the RESULT/card-header grammar in one place.
   *Fix:* read the PATHS line and the fix line through the existing parser
   (`cardhdr.KeyValue`, as `add.go:148` already does) or add the four functions to
   `driftAllowlist` with the commit and reason, and re-run the package.

7. **`docs/SPEC-UP.md:57-84` — the spec's numbered step list stops at eight steps and never names the `ssh` step the code registers at order 55.**
   `internal/up/ssh.go:17` registers `Step{Name: "ssh", Order: 55, …}` between `secrets`
   (50, `docs/SPEC-UP.md:66`) and `redis` (60, `:69`). The spec's list runs
   `platform`(10), `dirs`(20), `binaries`(30), `sprint`(40), `secrets`(50), `redis`(60),
   `seat`(70), `smoke`(90) and has no `ssh` entry, so a reader following the normative
   list does not know the step exists or that it runs before `redis`. The card's own
   `docs/SETUP.md:193` section documents it, which makes the two docs disagree.
   *Evidence:* `grep -nE 'ssh|known_hosts' docs/SPEC-UP.md` prints nothing;
   `grep -rn 'Register(Step{Name:' internal/up/*.go` lists nine steps with order 55 = ssh;
   `docs/SPEC-UP.md:54-55` itself says a step added later "adds a file and edits no other",
   so the list is the only place that can say it.
   *Grade:* **NEXT** — a spec/code contradiction in the docs tree, not a wrong result.
   *Fix:* insert the `ssh` (55) step between `secrets` (50) and `redis` (60) in
   `docs/SPEC-UP.md`'s list, in the words of `docs/SETUP.md`'s section.

8. **`internal/docs/catalog.go:17` and the generated `AGENTS.md:161` — the map says "17 nova command-line tools" while `cmd/` holds 24 tool directories.**
   The catalog's `Page("cmd", "17 nova command-line tools", …)` is the hand-written
   purpose cell; `make map` copies it into every generated page, so the root map and the
   bottom table of `cmd/AGENTS.md` repeat the number. A reader comparing it with the map's
   own 24 tool rows cannot tell which tools the count drops. The same drift was recorded
   by a 2026-10-06 rating (`docs/ratings/snapshots/0c5803c2de40/work-read.md:30`,
   `version-read.md:33`), which saw 16 against 18 directories and 17 README rows; the
   count has since been raised to 17 but the tree has kept growing.
   *Evidence:* `ls -d cmd/*/ | wc -l` → 24; `grep -rn '17 nova command-line tools'` →
   only `internal/docs/catalog.go:17` and `AGENTS.md:161`.
   *Grade:* **NEXT** — a false count in the generated map, and the README table plus this
   number are how a reader decides what the repository ships.
   *Fix:* set the catalog cell to the tree's count (24) and run `make map`; better, have
   `make map` write the count from the directory listing so it cannot drift again.

9. **`docs/SPEC-FRIEND.md:580` against `cmd/nova-friend/main.go:1117` — the spec says a refusal is read from four evidence places (stdout, stderr, the harness log and the REPORT.md), but the daemon's only caller passes two, so a refusal written to a lane's `REPORT.md` is never detected.**
   `RefusalWatch.Observe` accepts a `LaneText` of four fields
   (`cmd/nova-friend/limit.go:44-55`) and `ReadRefusal` reads all four, and the test feeds
   `Stderr` and `Report` directly (`cmd/nova-friend/limit_test.go:61,65`). The production
   wiring, `cmd/nova-friend/main.go:1117`, is the only caller and passes
   `LaneText{Stdout: out, Log: friend.RunnerLog(dir)}`; `Report` (and the separate
   `Stderr`) are always empty there, and the daemon observes only when the lane exited
   non-zero. The commit that landed the feature (`3c829cb80`) and `docs/SPEC-FRIEND.md`
   both claim the four places are read.
   *Evidence:* `grep -rn 'LaneText{' cmd internal` → one production literal
   (`main.go:1117`) with `Stdout` and `Log`, and the test literals with `Stderr`/`Report`;
   `grep -n 'Report' cmd/nova-friend/limit.go` → only `Sources()` and `FirstErrorLine`.
   *Grade:* **NEXT** — the primary refusal path (a failed turn's stdout plus the head of
   stderr, `pkg/friend/adapter.go:181-186`) still works, so this is a promised source
   that is never read rather than a broken down.
   *Fix:* read the card's `REPORT.md` in the lane step and pass it as `LaneText.Report`,
   or narrow `docs/SPEC-FRIEND.md` and the commit's claim to the sources that are read.

10. **`tla/FileLock.tla:3-9` — the model's header says it was written from the merge package's `lock.go` and the wake package's `lockprobe_other.go`, neither of which is in the tree.**
    Line 4 lists "`pkg/bus`, merge, tokens, swarm, wake and update each carry a copy"
    and line 8 makes the merge package's lock rule ("the kernel releases the lock when its
    holder dies") the rule the design stands on. Neither the merge nor the wake package
    survives; `pkg/filelock/filelock_unix.go` is the surviving home and the models'
    `COVERAGE.tsv` row ("file-lock") still calls the module current. The same header is the
    only citation a reader has for the design's origin.
    *Evidence:* a listing of `internal/` holds no `merge` or `wake` entry; `grep -n 'merge\|wake' tla/FileLock.tla` → lines 4 and 8; the same two names are cited by no
    other model.
    *Grade:* **NEXT** — a dead path in a live model's header; the model itself is current.
    *Fix:* retarget the header to `pkg/filelock/filelock_unix.go` (the surviving
    copy) or mark the two deleted packages as historical in the same sentence.

11. **`internal/selftalk/selftalk_test.go:181` — the allocation budget does not hold under the race build and the leg takes nearly six minutes.**
    `TestScanAllocatesAFewTimesTheInputNotOneIntPerByte` asserts the scan allocates under
    8× a 4 MiB input; under `-race` the measured `TotalAlloc` is 9.8× and the test takes
    353 s. The package is on the certification race shard list
    (`internal/ci/cert_race_shards_class_test.go`), so the race job is red and near its
    cap. The dsh audit named the same test at this base; it is still red here.
    *Evidence:* `go test -race -count=1 -timeout 900s ./internal/selftalk` on the bench →
    `--- FAIL: TestScanAllocatesAFewTimesTheInputNotOneIntPerByte (353.71s)`,
    `selftalk_test.go:181`, `"41229536" is not less than "33554432" … 9.8x`, then
    `FAIL github.com/mas-bandwidth/nova-tools/internal/selftalk 353.713s`.
    *Grade:* **NEXT** — the non-race gate is green and the finding is already on the
    ledger; it reddens the race shard at a v1.1.0 cut.
    *Fix:* measure with `testing.AllocsPerRun`, or give the budget a `//go:build race`
    variant that reports the instrumentation rather than the code.

---

urgent=6 next=5
