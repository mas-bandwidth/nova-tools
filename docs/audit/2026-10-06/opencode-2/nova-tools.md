# nova-tools cold audit, 2026-10-06 (opencode-2)

Tool: nova-tools. Base: `sprint/mechanical-2026-10-02` at
`ad2f20c6bc71f6769ad464fe96586c57c0c5fb11` (2026-10-07 15:26 -0400). A cold
read of the commands and internal packages named by the card, without the
sprint: nova-friend, nova-bus, nova-config, nova-redis, nova-secrets,
nova-swarm, nova-cairn, nova-local, nova-tokens, nova-fuse, nova-ci and the
rest, the fleet plays, the docs tree and their TLA+ models. Nothing was fixed;
this report is the whole of the card.

Method: read the code with the spec beside it, then run the gates on a Linux
bench (never on the working machine): the whole-tree unit tier, the race
detector over the live packages, the functional onboarding walk
(`TestEveryCommandMeetsTheOnboardingStandard`), `gofmt -l`, `go vet` and
`staticcheck`. Every fact below is a line of code, a test's own output, or a
byte a command printed; no store, server or secret was touched. Two spec/help
contradictions that the 1.2.0 cold ratings already name are listed last and
marked, so the coordinator can dedupe rather than re-report them.

## Findings

## 1. The bus test rig's random source is raced, so `go test -race ./pkg/bus/` is red for the whole package — URGENT

`pkg/bus/bus_test.go:20-28` (the `Rand` closure written at `:24`);
`pkg/bus/token_test.go:193-209` (`TestRacingRetriesMakeOneMessage`).

**What is wrong:** `rig` hands every `Bus` a `Rand` closure that reads and
writes a captured `byte` counter (`n++`) with no lock, and
`TestRacingRetriesMakeOneMessage` calls `b.Send` from eight goroutines through
that one `Bus`, so the counter is a data race.

**Evidence:** `go test -race -count=1 ./pkg/bus/` fails:

    WARNING: DATA RACE
    Read at 0x00c00058e00f by goroutine 133:
      bus.rig.func1()  pkg/bus/bus_test.go:24
      pkg/bus.(*Bus).ulid()  pkg/bus/bus.go:708
      pkg/bus.(*Bus).Send()  pkg/bus/bus.go:328
      pkg/bus.TestRacingRetriesMakeOneMessage.func1()  pkg/bus/token_test.go:203
    Previous write at 0x00c00058e00f by goroutine 130:
      bus.rig.func1()  pkg/bus/bus_test.go:24
    --- FAIL: TestRacingRetriesMakeOneMessage
    FAIL  github.com/mas-bandwidth/nova-tools/pkg/bus

The certification workflow's race step (`.github/workflows/certification.yml`,
`Makefile:380 test-race`) runs the whole tree under the race detector, so this
is a release gate, not a private test detail.

**Fix:** make the rig's counter concurrency-safe (a mutex or `sync/atomic`), or
give the racing goroutines their own `Bus` over the one `Fake`.

## 2. The nova-friend test rig's clock is raced, so `go test -race ./cmd/nova-friend/` is red — URGENT

`cmd/nova-friend/main_test.go:95` (the `world` rig's `now` closure);
`cmd/nova-friend/main_test.go:160` (the `stopAfter` callback that calls it).

**What is wrong:** the rig's `now` closure writes `r.now` (and the state
`r.answerChecks` touches) and is called both from the daemon goroutine
(`pkg/friend/daemon.go` -> `d.Now()`) and from the test's `stopAfter`
callback with no synchronization, so the two race.

**Evidence:** `go test -race -count=1 ./cmd/nova-friend/` fails with ten
`WARNING: DATA RACE` reports; the first:

    Read at ... by goroutine 50:
      cmd/nova-friend.(*rig).world.func4()  cmd/nova-friend/main_test.go:95
      cmd/nova-friend.TestRunWithNoSessionAnsweringBeatsDown.stopAfter.func6()  main_test.go:160
      pkg/friend.(*SessionCheck).BeatOr.1()  pkg/friend/presence.go:404
      pkg/friend.(*Limits).BeatOrDown.9()  pkg/friend/limit.go:383
    Previous write at ... by goroutine 212:
      cmd/nova-friend.(*rig).world.func4()  cmd/nova-friend/main_test.go:95
    FAIL  github.com/mas-bandwidth/nova-tools/cmd/nova-friend

**Fix:** guard the rig's `now`/`answerChecks` state with a mutex (or an atomic
clock) so the daemon goroutine and the test callback share it safely.

## 3. The unit tier is red at the base: `internal/check` still greps the old `main.go` dispatch and fails for every check — URGENT

`internal/check/attest_test.go:293` (`dispatchRE`), `:295`
(`TestRecordLayerCheckCountMatchesSPEC`), `:341` (the `main.go` it reads).

**What is wrong:** the test finds the check verbs by matching
`case "<verb>":\n return cmd...` in `cmd/nova-check/main.go`, but the binary
now builds a `tool.Tool` with a `Verbs: []tool.Verb{...}` table
(`cmd/nova-check/main.go:53-90`, the verbs in `cmd/nova-check/verbs.go`), so
the regex matches nothing and the test fails once for every `###` subsection
of `docs/SPEC.md`'s nova-check section. The check now tests nothing at all.

**Evidence:** `go test -count=1 ./internal/check/`:

    --- FAIL: TestRecordLayerCheckCountMatchesSPEC
      attest_test.go:332: SPEC.md has a nova-check subsection "attest" that
      ../../cmd/nova-check/main.go does not dispatch
      ... (same for corpus, hygiene, links, kernel, floors, dogfood,
      convergence, spelling, nocode)
    FAIL  github.com/mas-bandwidth/nova-tools/internal/check

**Fix:** read the verb names from the `Verbs:` table (or from
`cmd/nova-check/verbs.go`) instead of the removed `case` dispatch.

## 4. A bare `nova-doctor` runs every check instead of refusing, so it prints five lines on stdout at exit 2 and never names the door — URGENT

`internal/doctor/doctor.go:260-263` (`Main` rewrites no arguments to `run`);
`internal/doctor/doctor.go:204-256` (`Tool` with `Default: "run"`).

**What is wrong:** ONBOARDING point 1 and the `toolanswers` rule require a
bare `<tool>` to print one `<TOOL> REFUSED: ...; run: <tool> help` line on
stderr at exit 2. `Main` replaces the empty argument list with `run` before
the skeleton sees it, so the tool runs its checks, writes each result to
stdout, and exits 2 with nothing on stderr. The skeleton's own `Default`
handling already refuses a bare invocation (`pkg/tool/tool.go:166-176`),
so the pre-default is the bug.

**Evidence:** on the bench,

    $ ./nova-doctor >/tmp/o 2>/tmp/e; echo exit=$?; wc -l /tmp/o /tmp/e
    exit=2
    5 /tmp/o
    0 /tmp/e

and the functional class test:

    a bare `nova-doctor` wrote to stdout: "DOCTOR gosdk ...\nDOCTOR providers fail ..."
    a bare `nova-doctor` names no door; it must contain "run: nova-doctor help"

**Fix:** delete `if len(args) == 0 { args = []string{"run"} }` and let the
skeleton refuse a bare invocation (keep `Default: "run"` only for the
flag-or-file first word the skeleton already handles).

## 5. `docs/TESTS.md` and `docs/CLI.md` have no `## nova-doctor` or `## nova-up` section — NEXT

`docs/TESTS.md` (headings at `:79` nova-bus … `:1207` nova-work; neither
`## nova-doctor` nor `## nova-up` exists); `docs/CLI.md` (same two missing).

**What is wrong:** Onboarding point 3 requires a `### First run` transcript
under the tool's `## <tool>` section in `docs/CLI.md`, and point 5(c) requires
the `## <tool>` section in `docs/TESTS.md` that a test executes. Both
documents hold a section for every command but these two, so the standard's
own test fails for them.

**Evidence:** `go test -tags functional -run TestEveryCommandMeetsTheOnboardingStandard ./internal/ci/`:

    the document has no `## nova-up` section
    the document has no `## nova-doctor` section

**Fix:** add the two `## nova-doctor` and `## nova-up` sections (with their
`### First run` transcripts) to `docs/CLI.md` and `docs/TESTS.md`.

## 6. The README's catalogue sentences for nova-doctor and nova-up are not the banners' line 1 — NEXT

`README.md:46` (nova-doctor) and `README.md:47` (nova-up) against
`internal/doctor/doctor.go:207` and the nova-up banner.

**What is wrong:** Onboarding point 6 requires line 1 of `help` and the
README's "What it does" cell to be one sentence; the two have drifted apart.

**Evidence:** the same functional run:

    README.md's "What it does" for nova-doctor is "says what is missing for the
    nova tools to work here and, for each thing, the one line that fixes it",
    and line 1 of `nova-doctor help` says "says what is missing for the nova
    tools to work, and the one line that fixes each"
    README.md's "What it does" for nova-up is "set nova up on one machine: plan
    every step, then apply, from nothing to a first sprint", and line 1 of
    `nova-up help` says "sets up nova on one machine, from nothing to a first sprint"

**Fix:** make the README cells and the banner line 1 one sentence (pick one
spelling of each).

## 7. `nova-doctor help`'s `example:` block is one command, not the three a first run needs — NEXT

`internal/doctor/doctor.go:219` (`Example: "--local"`), block rendered at
`pkg/tool/tool.go:524-529`.

**What is wrong:** Onboarding point 6 requires three to six command lines a
stranger runs in order; the block holds one.

**Evidence:** the functional run: `` `nova-doctor help`'s example: block runs
the tool 1 time(s): ["nova-doctor --local"] ``.

**Fix:** add two more real first-run lines (e.g. `--check <name>`, `run --strict`) to the banner's example.

## 8. The tool-answers ledger has no rows for three in-scope sites and a stale count for a fourth — NEXT

`internal/ci/testdata/toolanswers` (loaded by
`internal/ci/toolanswers_class_test.go:25`).

**What is wrong:** the ledger's ceiling is exceeded or a site is missing, so
the class rule is red: `cmd/nova-config:dry-run` (its `login`/`logout` state
`local write` and take no `--dry-run`), `cmd/nova-friend:dry-run` (`beat`
states a `delivery` and takes no `--dry-run`), `cmd/nova-doctor:bare` (the
bare invocation above), and `cmd/nova-sprint:dry-run` is listed at 56 sites
while 69 exist. (The nova-sprint row is the sprint's; the other three are
nova-tools'.)

**Evidence:** the functional run prints, for each: "`dry-run` (no row in
testdata/toolanswers)" and "testdata/toolanswers lists cmd/nova-sprint:dry-run
at 56 sites, but 69 are there now".

**Fix:** add the three rows and re-measure the fourth with
`NOVA_CI_UPDATE=1` (or split the nova-sprint count out), and make the named
verbs honour `--dry-run`.

## 9. `PreserveUnreadable` reads the unreadable box with an unbounded `os.ReadFile` — NEXT

`internal/fuse/fuse.go:456`; the bound it should share is
`internal/fuse/fuse.go:222-264` (`maxBoxBytes`, `io.LimitReader`).

**What is wrong:** `ReadBox` deliberately caps the box at 1 MiB "so a
directory writer cannot exhaust memory", but the lockdown path's
`PreserveUnreadable` (called from `cmd/nova-fuse/main.go:550` before a
lockdown clobbers an unreadable box) reads the same path with `os.ReadFile`,
which allocates the whole file. A multi-gigabyte regular file at the box path
is read whole into memory on the one path that must always work. It also
skips `ReadBox`'s `Lstat`+`SameFile` re-check.

**Evidence:** `internal/fuse/fuse.go:456` `data, err := os.ReadFile(path)`,
against `internal/fuse/fuse.go:258-264`:
`io.ReadAll(io.LimitReader(f, maxBoxBytes))` then `if len(data) == maxBoxBytes { refuse }`.

**Fix:** read the evidence through the same `io.LimitReader(f, maxBoxBytes)`
cap (or refuse a source over the cap and say the bytes were not preserved).

## 10. `cmd/nova-card/generate_test.go` is not gofmt-clean — NEXT

`cmd/nova-card/generate_test.go:240` (a doubled blank line).

**What is wrong:** the tree is not formatted to the standard's own shape.

**Evidence:** `gofmt -l cmd internal tools` prints
`cmd/nova-card/generate_test.go`; `gofmt -d` removes one blank line after the
`assert.NoDirExists` test (around line 239).

**Fix:** run `gofmt -w cmd/nova-card/generate_test.go`.

## 11. `docs/SPEC-CHECK.md` names a `nova-dev` binary that does not exist — NEXT (already named in docs/ratings/1.2.0)

`docs/SPEC-CHECK.md:1` ("# nova-dev convergence — specification") and `:13`
(says `help` prints `nova-dev convergence --repo ...` byte for byte).

**What is wrong:** there is no `nova-dev` command; the verb is
`nova-check convergence` (`cmd/nova-check/convergence.go:42-47`), and no
banner prints the spec's line, so the spec contradicts the binary.

**Fix:** retitle the file `nova-check convergence` and quote the line
`nova-check convergence -h` really prints.

## 12. `docs/SPEC.md` still spells the listings' ceiling `--fail-max` — NEXT (already named in docs/ratings/1.2.0)

`docs/SPEC.md:353` ("Every listing here takes `--fail-max <n>`").

**What is wrong:** the nova-check listings' help and `docs/CLI.md` call the
flag `--max`; `--fail-max` is the old spelling, kept only as an alias for one
release that answers `NOTE --fail-max is --max`
(`cmd/nova-check/verbs.go:16-20,23-28`), so the SPEC's sentence names a spelling
the shipped text has retired.

**Fix:** say `--max` in `:353`, and give `--fail-max` as the alias the 1.2.0
ratings already ask for.

## 13. `cmd/nova-sandbox`'s worktree tests flake under a full-suite parallel run — NEXT

`cmd/nova-sandbox/worktree_test.go:184`, `:281`
(`TestWorktreePruneRemovesMergedAndClosedAndKeepsOpen`,
`TestWorktreeCoverRunGitReportsSuccessAndFailure`).

**What is wrong:** in one full unit-tier run the first failed with
`creating the tree for --pr 2: exit 2 ... already exists` and the second with
`git init --quiet: waitid: no child processes`; both are `ok` in isolation and
under `go test -count=5 ./cmd/nova-sandbox/`. Either the worktree verb can
reuse a GUID directory a failed run left behind, or the test shares state it
should re-create per case. A flake on the cert path is a defect.

**Evidence:** the whole-suite log's `--- FAIL: TestWorktreePruneRemovesMerged andClosedAndKeepsOpen` / `TestWorktreeCoverRunGitReportsSuccessAndFailure`
with those two messages; `go test -count=5 ./cmd/nova-sandbox/` then prints
`ok ... 9.836s`.

**Fix:** make the worktree verb re-create a stale GUID directory (or refuse it
without leaving it), and give each case its own scratch state.

## What was not done

- No fix was made: this card is the audit alone.
- No live store, server or secret was read; the probes used scratch files and
  the bench's test temp directories only.
- `nova-sprint` and the sprint's own packages were read only where the class
  tests name them; the card keeps the sprint audit separate.
- `go vet ./...` was clean; `staticcheck ./...`'s findings all match the
  committed `internal/ci/testdata/staticcheck` ledger, so none is reported.

urgent=4 next=9
