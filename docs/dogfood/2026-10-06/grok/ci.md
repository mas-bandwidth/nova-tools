# Dogfood: nova-ci — 2026-10-06, grok

Read cold as a stranger: only `nova-ci -h`, `nova-ci help`, every verb's `-h`,
and the page under `docs/` (`docs/CLI.md`'s `## nova-ci` section and
`docs/TESTS.md`'s `## nova-ci` transcript). Built from the checkout at
e8f70f600ebf as `nova-ci v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64
go1.26.6`, and a `GOOS=darwin` build of the same tree for `bench run`. Every
verb ran at least once with its real flags, the refusals too: `new-rule` and
`new-verb` under `--dry-run` (nothing written), `github receipt` under
`--dry-run` (nothing dialled), `local` under `--dry-run`, `bench run` for real
against the Linux bench, and `slowtests` over the built-in `--example` stream,
over a real `go test -json` pipe, and over scratch event streams.

## Findings

1. `go test -json ./pkg/foo | nova-ci slowtests --budget 60 --sleeps ledger.txt`

   with `ledger.txt` holding `pkg/foo<TAB>TestSleepyFoo<TAB>run1`, in a scratch
   module (`module example.com/sleepmod`) that is not nova-tools:

       nova-ci slowtests REFUSED: --sleeps /tmp/sleeps_foo.txt: line 1: package "pkg/foo" must be the full module-relative path; run: nova-ci slowtests -h
       exit=2

   The same row is refused by `--allowlist`, and so are
   `example.com/sleepmod`, `.` and `sleepmod`. `pkg/foo` IS the package's full
   module-relative path, and `slowtests` is documented as working "in any Go
   module"; the reader is told the path is wrong and given no shape that is
   right. The check reads only `cmd/`, `internal/` and `tools/` prefixes, none
   of which the help names (its row example is `internal/pkg<TAB>...`). A
   stranger whose module has a `pkg/` tree can never allow a SLEEPS skip or a
   budget. Grade: URGENT.

2. `nova-ci new-rule --root /tmp/sleepmod --dry-run probe`

   (and `nova-ci new-verb --root /tmp/sleepmod --dry-run notool probe`), where
   `/tmp/sleepmod` is a scratch Go module with a `go.mod` and no relation to
   nova-tools:

       would write internal/ci/<rule>_class_test.go
       would write internal/ci/testdata/<rule>/fixture.txt
       would write make/rule_<rule>.mk
       exit=0

   (the rule name `probe` stands in for `<rule>`; none of the three files
   exists in this tree). Expected the refusal the same flag gives a directory
   with no `go.mod`
   ("is not a nova-tools checkout (no go.mod)"): the verb help says it "needs a
   nova-tools checkout", and a foreign module's `go.mod` is not one. A real run
   would write this repository's rule and verb skeletons into somebody else's
   module. Grade: URGENT.

3. `nova-ci new-rule --root . --dry-run deadcode`

   `deadcode` is a documented class rule (its test is
   `internal/ci/dead_code_class_test.go`):

       would write internal/ci/<rule>_class_test.go
       would write internal/ci/testdata/<rule>/fixture.txt
       would write make/rule_<rule>.mk
       exit=0

   (the accepted name was `deadcode`; the three planned files do not exist, and
   were not written because of `--dry-run`). Expected a refusal, the way
   `new-rule --dry-run classtests` refuses a file that exists and
   `new-verb --dry-run nova-ci slowtests` refuses an existing verb ("is already
   a verb nova-ci help lists"). A rule name that is already a rule is not
   checked, so a duplicate can be scaffolded under a variant spelling.
   Grade: NEXT.

4. `go test -json ./... | nova-ci slowtests --budget 60` in the scratch module,
   whose root test calls `t.Skip("SLEEPS: probe")`

       CI-SLEEPS test=TestSleepy package=example.com/sleepmod: skipped for a wall-clock wait and not on the\x20SLEEPS\x20ledger\x20(no\x20--sleeps\x20given); inject a clock or tag it //go:build functional
       CI-LOAD load=33.92 cpus=64 per-cpu=0.53: measured, not a verdict
       exit=1

   Expected the ledger named in plain words, as the remedy on the same line is:
   the default ledger string is passed through the field escaper, so every
   unledgered SLEEPS skip prints `not on the\x20SLEEPS\x20ledger`. Grade: NEXT.

5. `nova-ci bench -h` (and `nova-ci bench run -h`)

       usage: nova-ci bench <run> [flags]
         nova-ci bench run --host <h> [--fallback <h>] --dir <tree> [--root <dir>] [--cache <dir>] [--with-git] -- <go command>
       `nova-ci bench <verb> -h` lists a verb's flags.
       exit codes: nova-ci: test-time budgets over go test -json output, and this repository's own CI steps
       exit=0

   Expected the group usage and, when it has one, its own exit table, the way
   `local -h` and `github receipt -h` open theirs with `exit codes: 0 done, ...`.
   The `exit codes:` label is glued to the top-level banner's first line, and
   the same glued line opens `bench run -h` before that verb's own real exit
   table lower down. Grade: NEXT.

6. `nova-ci slowtests < /dev/null`

       CI-SLOW FAILED packages=0 slowest=none: looked at nothing; run: nova-ci slowtests --allow-empty
       CI-LOAD load=35.58 cpus=64 per-cpu=0.56: measured, not a verdict
       exit=1

   Expected the verb's own status word to lead the line
   (`nova-ci slowtests REFUSED: ...`), as the refusals and the `CI-SLOW OK`
   line do; `CI-SLOW FAILED` reads as a finding prefix, not the run's verdict.
   The remedy is present. Grade: NEXT.

7. `nova-ci slowtests < /dev/null`, against `docs/TESTS.md` §nova-ci

   Printed as in finding 6. Expected the behaviour that page promises: it says
   "with an empty stream it reads zero packages and prints `CI-SLOW OK
   packages=0 slowest=none`", but the tool prints `CI-SLOW FAILED packages=0
   slowest=none: looked at nothing` at exit 1 unless `--allow-empty` is passed,
   which the prose does not name. Grade: NEXT.

8. `nova-ci github receipt --from-runner --repo mas-bandwidth/nova-tools --sha 5844884e267c0000000000000000000000000000 --run-id 1 --workflow CI --conclusion success --dry-run --json`

       nova-ci github receipt REFUSED: unknown flag --json; the flags of github receipt are --at, --conclusion, --dry-run, --from-runner, --pr, --redis, --repo, --run-id, --sha, --workflow; run: nova-ci github receipt -h
       exit=2

   `functional`, `version`, `new-rule`, `new-verb`, `local` and `bench run` also
   refuse `--json`. Expected the standard's one shape across the set
   (AGENTS.md section 2: "Every verb accepts `--json`"); only `slowtests` does,
   and only its help names the flag. Grade: NEXT.

## What the tool got right

- `slowtests --example` and `--json` render the same value; `--max 0` prints
  every finding, `--max 1` prints one plus the `MORE shown= total=` line naming
  the flag that widens it, and `--enforce` is the only thing that reddens a
  CI-SLOW line.
- `github receipt` with nothing set names every missing field at once, each with
  what it wants; the bad `--repo`, `--sha`, `--run-id` and `--conclusion` cases
  are one line each, and `--dry-run` dials nothing.
- `functional` never exits in silence: `packages=0` comes with a reason, a flag
  and an unmatched pattern are refused, and `-h` is free.
- `bench run` copies, runs, streams and removes its run directory; the fallback
  names why the first host was passed over, and the `REFUSED` cases name the
  missing input.
- `local --dry-run` prints the merge base, the packages and the exact `make`
  line with its env, and refuses a base with no merge base with the fetch to
  run.

READ 8/10 — the banner answers what the tool does, how it works and how to start,
every verb states its effect and exit table, and the refusals name every missing
input at once with a next command; the glued `exit codes:` banner on `bench -h`,
the sleeps/allowlist path shape the help never defines, and the empty-stream
prose that contradicts the exit table keep it from a 9.

USE 7/10 — `--example`, the real stdin pipe, the budget and `--max` paths,
`functional`, `local --dry-run`, `github receipt --dry-run` and `bench run` all
did what their help says on a first read; the allowlist ledger is unusable in
any module without a `cmd/`, `internal/` or `tools/` tree, and `--root` accepts
a foreign `go.mod` as a nova-tools checkout, so two real paths stumble.

urgent=2 next=6
