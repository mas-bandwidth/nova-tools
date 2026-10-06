# Dogfood: nova-doctor — 2026-10-06, dsh

One friend, one tool, cold. I read only `nova-doctor -h`, `nova-doctor help`,
every verb's `-h`, and its page under docs/ (SPEC-DOCTOR.md), then used every
verb at least once with its real flags: a binary built from this checkout at
6ec8bb0, run on a Linux bench over ssh in the checkout, in scratch modules, in
scratch PATH labs (fake `nova-*` executables printing version lines), and
against the bench's real PATH. Refusals included. No server, no network; the
tool writes nothing (`effect: inspection: reads, writes nothing` held: no file
changed anywhere it ran). Two adoption reads are quoted where they were made:
the README's tool row and the headings of docs/CLI.md.

## Findings

1. `nova-doctor --local` (in the checkout; also outside one, also as
   `nova-doctor --json --local`)

        DOCTOR gosdk warn go 1.26.6, toolchain 1.26.6, GOCACHE ~/.cache/go-build; GOFLAGS does not carry -mod=readonly fix: export GOFLAGS=-mod=readonly before any go command on a bench (docs/SETUP.md, dep-go-sdk-b.w2)
        DOCTOR self fail nova-loop, nova-loop-migrate, nova-push-credential did not answer `version` with a version line fix: nova-update apply --file <manifest> nova-loop
        exit=2

   Expected (SPEC-DOCTOR.md, The command): "--local skips the checks only a
   fleet needs and says which, in one line: `DOCTOR local skipped=<name,name> (...)`", and `--json` printing `{"exit":N,"results":[...],"skipped":[...]}`.
   Observed: with `--local` every check still ran (both in a checkout and
   outside one), no `skipped` line is printed in any form, and the JSON
   carries no `skipped` key at all. The banner ("--local skip the checks only
   a fleet needs, and say which") and the README row make the same claim. A
   flag that is accepted, documented in three places, and never does anything
   observable. Grade: URGENT.

2. `nova-doctor run -h`

        usage: nova-doctor run [flags]
        checks: gosdk, self
        example: nova-doctor --local

   Expected: the page under docs/ to describe what the binary runs. SPEC-DOCTOR.md
   documents one check (`self`); the binary runs two, and `gosdk` (its GOFLAGS
   warn, its GOCACHE and go.mod fails) is absent from the page a stranger is
   told to read. Grade: NEXT.

3. `nova-doctor --check`

        RUN REFUSED: --check needs a value: it wants run only this check (repeatable); the names are in help run; run: nova-doctor run -h
        exit=2

   Expected: a placeholder that names what it wants. `run -h` renders the flag
   as `--check <help run>` — reading like a value to type — and the description
   ends "the names are in help run", which is not a command (the checks line
   above it, or `nova-doctor run -h`, is). The unknown-check refusal already
   answers better: "the checks are gosdk, self". Grade: NEXT.

4. `nova-doctor tmp/scratchfile.md` (an existing file; a directory behaves the same)

        DOCTOR gosdk warn go 1.26.6, toolchain 1.26.6, GOCACHE ~/.cache/go-build; GOFLAGS does not carry -mod=readonly fix: export GOFLAGS=-mod=readonly before any go command on a bench (docs/SETUP.md, dep-go-sdk-b.w2)
        DOCTOR self fail nova-loop, nova-loop-migrate, nova-push-credential did not answer `version` with a version line fix: nova-update apply --file <manifest> nova-loop
        exit=2

   Expected: a refusal — `version` answers `VERSION REFUSED: takes no positional arguments, got "stray"` — and the unknown-verb refusal
   advertises "a file is given by its path (./frobnicate)", implying a file
   operand means something to this tool. To `run` it means nothing: the
   output is identical to the bare run, the operand swallowed without a word.
   The tool does something plausible instead of refusing. Grade: NEXT.

5. `PATH=<scratch>/tie:/usr/bin:/bin nova-doctor --check self` (nova-a v1.0.0, nova-b v2.0.0)

        DOCTOR self fail the tools are not one release: nova-a=v1.0.0 differ from v2.0.0 (1 tools) fix: nova-update apply --file <manifest> nova-a --version v2.0.0
        exit=2

   Expected (SPEC-DOCTOR.md, self): the evidence to name "every tool that
   differs from the version most of the tools report" — with one tool on each
   side there is no most, the tie is broken silently toward v2.0.0, and the
   fix line tells the reader to move nova-a there with nothing saying why that
   direction. Also "(1 tools)". Grade: NEXT.

6. `nova-doctor --json --check nosuch`

        RUN REFUSED: no check named "nosuch"; the checks are gosdk, self; run: nova-doctor help
        exit=2

   Expected: a `--json` consumer parses stdout; on a usage refusal the output
   is the plain refusal, so the parse breaks and only the exit code speaks.
   SPEC-DOCTOR.md does not say which it should be. Grade: NEXT.

7. `nova-doctor --check nosuch --bogus`

        RUN REFUSED: unknown flag --bogus; the flags of run are --check, --json, --local, --strict; run: nova-doctor run -h
        exit=2

   Expected: one run to report every problem it can find (the onboarding
   standard). The unknown flag preempts the unknown check name; fixing the
   flag and rerunning reports it — two turns. Two unknown checks are both
   reported, so the check side of the grammar already can. Grade: NEXT.

8. `nova-doctor --check self` (the bench's real PATH)

        DOCTOR self fail nova-loop, nova-loop-migrate, nova-push-credential did not answer `version` with a version line fix: nova-update apply --file <manifest> nova-loop
        exit=2

   Expected: the result to name the next command as a paste, not a search.
   The evidence is true (each named tool answers `nova-loop: no command for version`, exit 2 — verified by hand), but the fix line carries the literal
   `<manifest>`, and where that path lives is named nowhere in the tool's
   output. Grade: NEXT.

9. `nova-doctor help` (the sentence after the usage lines)

        Every verb but run takes --json: run prints one `DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]` line per check; with --json it prints the same results as one object.

   Expected: read plainly it says `run` does not take `--json`; the usage
   line above it lists `--json` under `[run]`, `run -h` lists it, and
   `nova-doctor run --json` works and prints one object. The sentence
   contradicts the usage block it sits under. Grade: NEXT.

10. `nova-doctor version --json` (beside `nova-doctor --json`)

        {"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-doctor devel linux/amd64 go1.26.6"}
        exit=0

    Expected: one output structure across a tool's verbs (the one-value
    rule); `run --json` prints `{"exit":N,"results":[...]}`, `version --json`
    the suite shape, so a consumer switches parsers per verb. SPEC-DOCTOR.md
    documents only run's. Grade: NEXT.

11. `GOCACHE=../lab/okcache nova-doctor --check gosdk` (a writable relative path)

        DOCTOR gosdk fail GOCACHE off is not writable fix: export GOCACHE to a directory this user writes, e.g. export GOCACHE=$HOME/.cache/go-build (docs/SETUP.md, dep-go-sdk-b.w2)
        exit=2

    Expected: `go env GOCACHE` answers `off` for a relative value (verified;
    the doctor is faithful to go), but the reader who set GOCACHE to a path
    sees "off" and no trace of the value they set, the line reads as a
    contradiction (off, yet not writable), and the remedy's example is
    absolute. Naming the configured value beside go's answer would make it one
    turn. Grade: NEXT.

12. `grep -n "^## " docs/CLI.md` (adoption read, after the tool's own help)

        7:## nova-check
        246:## nova-self-talk
        290:## nova-fuse

    Expected: a `## nova-doctor` section — the command reference carries one for
    every other living tool (22 sections); nova-doctor's only page is
    SPEC-DOCTOR.md and the README row. Grade: NEXT.

## What worked (no finding, kept short)

The exit table is exact in every combination run: 0 all ok, 0 a warn without
`--strict`, 1 a warn with `--strict` and no fail, 2 a fail, 2 a usage
refusal. Checks run in name order and a fail does not stop the others. Every
unknown name is answered with the full list (`--check nosuch` names both
checks); `--check` is repeatable and a repeated name runs once; `--strict`
with a warn and a fail together exits 2, not 1. The GOFLAGS remedy is true:
exporting `GOFLAGS=-mod=readonly` cleared the gosdk warn on the next run.
The self check ignores non-nova executables beside its fakes, first-of-name
wins on a duplicated PATH entry, and its evidence on the real bench was
true. `gosdk` names the go version, the go.mod toolchain (a scratch module
without a toolchain line answers its `go` directive), refuses a
non-checkout with a fix that names the remedy, and an absolute unwritable or
missing GOCACHE is refused naming the exact path. Help is never a refusal:
the bare command, `-h`, `help`, and every verb's `-h` exit 0, and the bare
command is the documented first run.

The card's own mechanics: the named test `TestDocsTreeIsConsistent` does not
exist in ./internal/docs at this tip — `go test -run TestDocsTreeIsConsistent ./internal/docs` answers ok with no tests to run. Reported, not fixed.

## Gate

    go test -count=1 -timeout 600s ./internal/docs/... ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.418s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	17.830s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.018s [no tests to run]

Both packages pass on a Linux bench (the working tree synced there; the branch
and origin/dev carried as git bundles so the class tests that read git find
them). docs/dogfood is catalogued at this tip, so this report's new directory
needs no map edit. The named test does not exist (above); the run answers ok
with no tests to run.

READ 7/10 — the banner, the verb helps, the refusal grammar and the exit table
answer a cold reader fast and truly, but three places claim a --local behavior
the binary never shows, the page misses a whole check, and two help sentences
mislead (the --json sentence, --check's placeholder).
USE 8/10 — every verb ran for real on scratch PATHs, scratch modules and the
bench's real PATH, refusals honest and remedies mostly one turn (the GOFLAGS
remedy verifiably clears the warn), marred by the inert --local, the
swallowed positional and the non-pasteable <manifest>.

urgent=1 next=11
