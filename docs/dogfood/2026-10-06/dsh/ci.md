# Dogfood: nova-ci — 2026-10-06, dsh

One friend, one tool, cold. I read only `nova-ci -h`, `nova-ci help`, every verb's
`-h`, and nova-ci's page under docs/ (`docs/CLI.md` §nova-ci, including `### bench run` and `### github receipt`, and the `## nova-ci` transcript in `docs/TESTS.md`),
then used every verb with its real flags: `slowtests` against a hand-built
`go test -json` stream and against a real `go test -json ./pkg/oneline/...`
run, with `--example`, `--budget`/`--package-budget`/`--test-budget`, `--allowlist`,
`--sleeps`, `--enforce`, `--max`, `--json`, `--load`/`--cpus`, `--allow-empty`, and
its refusals; `functional` against one package, many, `./...`, and a pattern that
matches nothing; `local --dry-run` against `origin/dev` and against the base this
branch names; `new-rule` and `new-verb` both `--dry-run` and really, in a scratch
checkout with a scratch `go.mod`, `Makefile` and `cmd/nova-ci/main.go`; `bench run`
really on a Linux bench (a tiny module for `go test`, `--with-git` for `git log`,
a failing command for the exit pass-through, `--fallback` for a host that does not
answer, and the path refusals); and `github receipt` with `--dry-run`, every
refused field, and a store that is not there. No server was started on any
machine; nothing was written outside the job directory and the bench paths the
verb names. The binary was built from this checkout at 051068768
(`nova-ci v1.0.1-0.20261007144949-051068768266 linux/amd64 go1.26.6`) and run on
a Linux bench, with a darwin build used only to drive `bench run` from the job
directory. In the transcripts below the bench host, the run id and the login
homes are placeholders, as the tree's generality rule requires. No code changed.

## Findings

1. `nova-ci slowtests < trunc3.jsonl`, where the stream completes the fixture's
   first package and then starts a second package with a test `run` that never
   ends and no package-level finish (nor does a stream with only a package `run`
   event fare better)

       CI-SLOW OK packages=1 slowest=github.com/mas-bandwidth/nova-tools/internal/p1:1.0s
       CI-LOAD load=28.66 cpus=64 per-cpu=0.45: measured, not a verdict
       exit=0

   Expected: `slowtests -h`'s own exit table says "1 ... a truncated package
   (started and never ended)". The second package started and never ended, was
   not counted in `packages=`, and the run passed. In the thinner fixture (a
   package `run` event and nothing else) the same exit 1 is reported as `CI-SLOW FAILED packages=0 slowest=none: looked at nothing`, which names an empty
   stream, not the truncation the help promises: the documented truncated-package
   verdict is never printed. A pipeline whose producer died mid-run passes. Grade:
   URGENT.

2. `printf '' | nova-ci slowtests` against the `## nova-ci` First run in `docs/TESTS.md`

       CI-SLOW FAILED packages=0 slowest=none: looked at nothing; run: nova-ci slowtests --allow-empty
       CI-LOAD load=37.80 cpus=32 per-cpu=1.18: measured, not a verdict
       exit=1

   Expected: the transcript says "with an empty stream it reads zero packages and
   prints `CI-SLOW OK packages=0 slowest=none`", exit 0, and that "only `--enforce`
   makes it exit 2". The binary fails an empty stream at exit 1 unless
   `--allow-empty`, and `--enforce` exits 1 (as `-h` says), not 2. The First run a
   stranger reads is wrong on both counts. Grade: URGENT.

3. `nova-ci bench run --host <bench> --root <bench-login-home> --dir ./scratch/bench-tree -- go version`

       go version go1.26.6 linux/amd64
       CI BENCH host=<bench> run=<bench-login-home>/run.<id> exit=0 removed=yes
       exit=0

   Expected: `-h` and `CLI.md` say `--root` and `--cache` are "plain paths,
   relative to the login's home or absolute, never `~`, `..`, the home or `/`".
   The bench login's home was accepted and a run directory was made directly in
   it. `--cache <bench-login-home>` was accepted too; `--root` equal to the
   caller's own home was accepted and reached the bench, refused only by the
   remote `mkdir`'s Permission denied; and `--root /` is the only home-ish path
   refused, as `--root "" is the home or the root itself`. The home check reads
   whichever home is convenient and does not hold the rule the help states.
   Grade: URGENT.

4. `printf '{"Action":"run","Package":"…/internal/example","Test":"TestSleepy"}\n…SLEEPS: waits…\n' | nova-ci slowtests` (a skipped test carrying the `SLEEPS:` marker)

       CI-SLEEPS test=TestSleepy package=<module>/internal/example: skipped for a wall-clock wait and not on the\x20SLEEPS\x20ledger\x20(no\x20--sleeps\x20given); inject a clock or tag it //go:build functional
       CI-LOAD load=28.66 cpus=64 per-cpu=0.45: measured, not a verdict
       exit=1

   Expected: the sentence's spaces, "not on the SLEEPS ledger (no --sleeps
   given)". Six spaces in the rendered line are the literal four characters
   `\x20`; `sed -n l` shows the backslash. The verdict is right; its one
   explanatory line is not readable. Grade: NEXT.

5. `nova-ci functional --json ./internal/ci` (and `local --json`, `new-rule --json`,
   `new-verb --json`, `bench run --json`, `github receipt --json`, `version --json`)

       nova-ci functional REFUSED: unknown flag "--json" (functional takes no flags, only package directories such as ./cmd/nova-table or ./internal/...); run: nova-ci functional -h
       exit=2

   Expected: the suite's one-output-value law is that every verb takes `--json`;
   only `slowtests` here does. A consumer can read the timing verdict as JSON but
   not the functional selection, the package list `local` picks, a scaffold's file
   list, a bench run's `CI BENCH` line, or a receipt. Grade: NEXT.

6. `nova-ci github receipt --from-runner --redis 127.0.0.1:1 --repo mas-bandwidth/nova-tools --sha <40hex> --run-id 1 --workflow CI --conclusion success`

       redis: 2026/10/07 14:59:18 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
       redis: 2026/10/07 14:59:18 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
       redis: 2026/10/07 14:59:19 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
       nova-ci github receipt FAILED: XADD ev:github: dial tcp 127.0.0.1:1: connect: connection refused; receipt write could not be confirmed: fix the store or the bench seat and rerun ci-ok
       exit=1

   Expected: the command reference says a store that is down "is one line on
   stderr". The verb prints five: four raw go-redis pool lines (`pool.go:762`)
   then its own FAILED line. The exit and the FAILED line are right, but the
   `ci-ok` log it is written for is not the one line the docs promise. Grade: NEXT.

7. `nova-ci slowtests --allowlist allow-ok.txt < slow.jsonl`, with the row
   `internal/example<TAB>-<TAB>60<TAB>50s@bench1` (the help's own row shape)

       nova-ci slowtests REFUSED: --allowlist allow-ok.txt: line 1: measured "50s@bench1": where "bench1" is neither run<id> (a CI run) nor a runner label .github/workflows/ci.yml names (); run: nova-ci slowtests -h
       exit=2

   Expected: the help says `where` is "run<id> or a bench"; the refusal says the
   only other door is "a runner label .github/workflows/ci.yml names", then names
   none — the parentheses are empty. A reader told the label must be one the file
   names has no way to learn any label without reading the file the tool just
   read. A `run<id>` row is accepted and raises the budget, so only that half of
   the sentence is unusable. Grade: NEXT.

8. `nova-ci bench bogus`

       CI REFUSED: unknown verb "bench bogus" in bench; did you mean bench run? the verbs are bench run; run: nova-ci bench -h
       exit=2

   Expected: the refusal grammar opens with the tool and verb, `nova-ci bench REFUSED: …` / `nova-ci REFUSED: …`, as every other refusal in the tool does
   (`nova-ci slowtests REFUSED:`, `nova-ci REFUSED: unknown verb "bogus" …`).
   `CI REFUSED:` drops the tool name, so a log scan for `nova-ci` misses the one
   refusal a mistyped group produces. Grade: NEXT.

9. `nova-ci bench run --host <bench> --root / --dir ./scratch/bench-tree -- go version`

       BENCH-RUN REFUSED: --root "" is the home or the root itself; run: nova-ci bench run -h
       exit=2

   Expected: the refusal name the value it refused, `/`. It prints an empty
   string, so a reader with two roots on the line cannot tell which one the rule
   struck, and the message's two alternatives ("the home or the root itself") do
   not say which one a bare `/` is. Grade: NEXT.

10. `nova-ci new-verb --dry-run <missing-tool> widget` in a checkout (the tool
    directory's name is elided to `<missing-tool>`)

        nova-ci new-verb REFUSED: <missing-tool> has no func main — new-verb adds a verb to an existing tool, and a verb in a package with no entry point stops the tree building; create <missing-tool>/main.go with func main and its dispatch switch first; run: nova-ci new-verb -h
        exit=2

    Expected: the tool directory does not exist. The refusal states it exists and
    lacks `func main`, and its remedy is to create a file in a directory that is
    not there (`create <missing-tool>/main.go with func main`). Naming the missing
    directory would take one turn; this phrasing sends the reader to look for a
    directory that was never created. Grade: NEXT.

11. `nova-ci slowtests --sleeps sleeps-fullpath.txt < sleeps.jsonl`, with the row
    carrying the package exactly as `go test -json` prints it,
    `github.com/mas-bandwidth/nova-tools/internal/example<TAB>TestSleepy<TAB>bench1`

        nova-ci slowtests REFUSED: --sleeps sleeps-fullpath.txt: line 1: package "github.com/mas-bandwidth/nova-tools/internal/example" must be the full module-relative path; run: nova-ci slowtests -h
        exit=2

    Expected: the help's row shape is `internal/pkg<TAB>test<TAB>where` — a path
    relative to the module root with no module prefix — and the package the reader
    has in hand is the full import path the event stream carries. The refusal asks
    for "the full module-relative path", which reads as the string just refused
    and contradicts itself; the module-relative form is the accepted one, but the
    message gives no example of it. `--allowlist` refuses the same way. Grade:
    NEXT.

## What worked (no finding, kept short)

The banner answers what it does, how it works and how to use it; `help`, `--help`
and every verb's `-h`/`--help` exit 0 and print before anything is read; `-h`
after flags still prints help and runs nothing; bare, unknown-verb and unknown-flag
answers name every verb at once. The exit table is exact everywhere run: 0 pass,
1 a check that said no, 2 usage or cannot run. `slowtests` is faithful to
`go test -json`: package `Elapsed`, the `slowest=` list, `--budget` whole seconds,
`--package-budget`, `--test-budget 0`, `--enforce` (exit 1), `--max` with its
`MORE shown= total=` line and `--max 0`, `--allow-empty`, `--load`/`--cpus` either
way, the 3x allowlist ceiling and its refusals (`budget 200s is not between its measurement 50s and 3 times it`), the `run<id>` row raising a budget, an
unreadable list file, `--sleeps` accepting a good row, `NaN`/`Inf`/float budgets,
a non-JSON line naming the line and the wanted shape, a terminal on stdin naming
both ways in, and the `--json` refusal object (`status":"refused"`, `why`,
`remedy`) on stdout. `functional` printed one package and its `-run` pattern,
normalised `./cmd/nova-ci/...`, printed the combined `./...` selection, and its
zero case as `CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-1-dirs`. The
scaffolds are real: `--dry-run` lists exactly `wrote`'s files and refuses a file
that is already there, the real write refuses to overwrite, both print the
dispatch snippet `new-verb` will not edit, and hyphen names camel-case to a valid
Go identifier (`bad-name` → `TestNoBadNameViolations`, `bad-verb` →
`cmdBadVerb`). `local --dry-run` printed the base, merge base, 145 package paths
and the exact `nice -n 15 make test "PKGS=…" GOTEST_P=2 GOTEST_COUNT_FLAG=-count=1 GOTEST_TAGS= (GOMAXPROCS=2)` line, and refused outside a checkout. `bench run`
really ran `go test ./...` and `git log` on the bench, streamed the command's own
stdout, passed the command's exit status through (a failing `go build` gave exit
1), removed the run directory and nothing else, passed over a host that did not
resolve with one `CI BENCH PASSED … next=<bench>`, and refused a missing `--host`,
`--dir`, `--`, a missing directory, and a `~` in a path. `github receipt --dry-run`
checked every field, printed `ev=-` and the NOTE, and named every refused field at
once (`--repo`, `--sha`, `--run-id`, `--workflow`, `--conclusion`, `--pr`, `--at`)
plus the missing store.

The card's named test `./internal/docs TestDocsTreeIsConsistent` does not exist at
this tip; the run answers ok with no tests to run. Reported, not fixed.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.989s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	8.605s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.010s [no tests to run]

Both packages pass on a Linux bench (the working tree synced there with
`GOCACHE` in the job's bench tree, `GOFLAGS=-mod=readonly`,
`NOVA_TEST_NO_HOST=1`; `origin/dev` is fetched to its tip so the selection and the
ledger-ratchet tests have their merge base). `docs/dogfood` is catalogued at this
tip, so this report's new file needs no map edit. The named test does not exist
(above); the run answers ok with no tests to run.

READ 6/10 — the banner, the verb helps and the exit table answer a cold reader
fast and truly, but the First run transcript in `docs/TESTS.md` is wrong about
the empty stream and `--enforce`'s exit, the exit table promises a
truncated-package verdict the binary never prints, the allowlist `where` refusal
names no accepted label, and the accepted path shape is described in words that
contradict themselves.
USE 7/10 — every verb ran for real on fixtures, a scratch checkout, a real
`go test -json` stream and a Linux bench, with honest one-turn refusals and clean
cleanup, marred by a check that passes a stream truncated mid-package, a
`bench run` that makes its run directory in the bench home the help forbids, and
a store-down receipt that is five lines rather than one.

urgent=3 next=8
