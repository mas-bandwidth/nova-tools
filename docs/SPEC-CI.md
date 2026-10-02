# SPEC-CI — the class tests that read this repository's own CI path

This specification holds the class tests that guard the CI path by reading the
repository's own test files as text. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated. Related: the budget law
`TestNoTestAssertsAWallClockBoundUnderTenSeconds`.

## The CI class test against fixed waits on the CI path

**The help line.** The class test is entered in the CI check roster and in help
as the verb `waits`:

```
waits   read every _test.go on the CI path; refuse a fixed wall-clock wait or bound
```

It runs as `go test ./internal/ci -run TestNoFixedWaitsOnTheCIPath`, and it is
the bench's fixed-wait audit made an official verb: the check a PR runs.

**What it reads and what it writes.** It reads, as text, every `_test.go` under
`internal/` and `cmd/` that the two-minute CL path runs, and refuses three
shapes with the file and the line: a `time.Sleep` whose constant is over 100 ms;
a context or timer bound under ten seconds that a test uses as a pass/fail
condition (`context.WithTimeout`, `context.WithDeadline`, `time.After`,
`time.NewTimer`, or a labelled deadline); and an assertion on elapsed time
(`time.Since`, `elapsed`, `took`, `waited`). The allowed shape is a poll for the
event up to a generous bound — thirty seconds or more — read from the
environment with a default (`NOVA_TEST_WAIT`, default `30s`), and a fake clock
where the code under test needs one. It writes nothing. Its only input besides
the tree is `testdata/fixed-waits-allowlist.txt`: the existing offenders, each
with a reason and the date it was written, and that file may only shrink — a new
entry is a refusal, not a place to park a wait.

**Its one-line output.** On a clean tree it prints one line,
`CI-WAITS OK tests=<n> allowlisted=<n> refused=0`, where `tests=` is the
`_test.go` files read, `allowlisted=` the entries still on the allowlist, and
`refused=` the fixed waits found (always `0` on `OK`). On a refusal it prints
one line per offender, `CI-WAITS file=<path> line=<n> kind=<sleep|bound|elapsed>
remedy="<the one thing to do>"`, then closes with `CI-WAITS FAIL tests=<n>
allowlisted=<n> refused=<k>`; the count is the truth about the CI path whether
or not the lines printed.

**Its refusals (exit 2, one remedy line each).** A `time.Sleep` over 100 ms —
`remedy="poll for the event up to NOVA_TEST_WAIT, not a fixed sleep"`. A context
or timer bound under ten seconds used as a pass/fail condition —
`remedy="raise the bound to thirty seconds or more, or inject a fake clock"`. An
assertion on elapsed time — `remedy="assert the event, not the clock; use a fake
clock if the code needs one"`. A new line in the allowlist —
`remedy="fix the wait; the allowlist only shrinks"`. A refusal names the file and
the line, so the queue’s PR comment is the whole diagnosis.

**The mistake it prevents.** A wait cut to make a suite fit the two-minute
gate makes a test flaky under load, and a flaky test drops innocent PRs from the
queue.

**Red tests.**

1. A test carrying `time.Sleep(150 * time.Millisecond)` is refused with its file
   and line, and the remedy names the poll, with the network it waits on faked.
2. A test with `context.WithTimeout(ctx, 5*time.Second)` used as its pass/fail
   condition is refused as a bound under ten seconds, with the bench it guards
   faked.
3. A test asserting `time.Since(start) < 5*time.Second` is refused as an
   elapsed-time assertion, with the clock faked.
4. A polling test reading `NOVA_TEST_WAIT` (default `30s`) and waiting for the
   event through a fake network is allowed.
5. A test whose subprocess bench and clock are fakes passes with no wall clock
   in the file.
6. Adding an entry to `testdata/fixed-waits-allowlist.txt` is refused, and
   removing one is allowed.

## The CI class test against copied built binaries

**The help line.** The class test is entered in the CI check roster and in help
as the verb `testbins`:

```
testbins   read every _test.go on the CI path; refuse copying a built executable into a fixture
```

It runs as `go test ./internal/ci -run TestNoCopiedTestBinariesOnTheCIPath`, and
it is the fixture-copy audit made an official verb: the shared helper a
fixture places a built program with is the check a PR runs.

**What it reads and what it writes.** It reads, as text, every `_test.go` under
`internal/` and `cmd/` that the two-minute CI path runs, and refuses one shape
with the file and the line: an `os.WriteFile` whose mode literal carries an
execute bit and whose data argument is a variable the file fills from
`os.ReadFile` -- the variable the parser resolves the identifier to, never its
spelling, so a `raw` in one function does not taint another function's `raw`
(an `io.Copy` into an `os.Create`/`os.OpenFile` of the same mode is
the same shape). That shape copies a compiled executable into a fixture, and on
macOS every fresh copy of an executable is a never-seen binary the system policy
scanner assesses on its first exec; one is quick, but a package run places
dozens at once and they queue behind the scanner for longer than a test waits.
The allowed shape is `internal/testbin.Place`, which hard-links first and copies
only where a link is impossible; a shell script written `0o755` is not the
shape, because the interpreter is the executable and its bytes are never
assessed. It writes nothing. Its only input besides the tree is
`testdata/fixed-testbins-allowlist.txt`: the existing offenders, and that file
may only shrink — a new entry is a refusal, not a place to park a copy.

**Its one-line output.** On a clean tree it prints one line,
`CI-TESTBIN OK tests=<n> allowlisted=<n> refused=0`, where `tests=` is the
`_test.go` files read, `allowlisted=` the entries still on the allowlist, and
`refused=` the copied built binaries found (always `0` on `OK`). On a refusal it
prints one line per offender, `CI-TESTBIN file=<path> line=<n> kind=copy
remedy="place built binaries with testbin.Place: link, never copy"`, then closes
with `CI-TESTBIN FAIL tests=<n> allowlisted=<n> refused=<k>`; the count is the
truth about the CI path whether or not the lines printed.

**Its refusals (exit 2, one remedy line each).** A built executable copied into
a fixture — `remedy="place built binaries with testbin.Place: link, never
copy"`. A new line in the allowlist — `remedy="fix the copy; the allowlist only
shrinks"`. A refusal names the file and the line, so the queue’s PR comment is
the whole diagnosis.

**The mistake it prevents.** A package that copies one built fake runner into
dozens of fixtures queues the copies behind the macOS policy scanner past the
thirty seconds a test waits, and fails on darwin while hard-linking the one
fixture passes in less than half the time. Every fixture places a built program
through `internal/testbin.Place`, and the class test refuses a new copy.

**Red tests.**

1. A fixture `_test.go` that reads a built binary with `os.ReadFile` and writes
   the bytes `0o755` into a fixture is refused with its file and line, the
   fixture read from a tree given on the command line and not by walking the
   repository.
2. The bytes may be handed to the `os.WriteFile` through a package-level map,
   a `fakeBins` shape; the taint follows the bytes and the copy
   is refused.
3. A shell script written `0o755` is allowed: the interpreter is the executable.
   It stays allowed when another function in the same file copies a binary
   through a variable of the same name: only the copy is refused.
4. A test that places the built program through `testbin.Place` is allowed.
5. Adding an entry to `testdata/fixed-testbins-allowlist.txt` is refused, and
   removing one is allowed.

## The CI class test against unquoted paths in JSON and template literals

**The help line.** The class test is entered in the CI check roster and in help
as the verb `templates`:

```
templates   read every _test.go; refuse a filesystem path unquoted in a JSON or template literal
```

It runs as `go test ./internal/ci -run TestNoUnquotedPathsInTemplateLiterals`,
and it is the Windows-path audit made an official verb: the check a PR runs.

**What it reads and what it writes.** It reads, as text, every `_test.go` under
`internal/` and `cmd/`, and refuses two shapes with the file and the line: a
`filepath.Join(...)` concatenated raw into a JSON or `text/template` string
literal, and an OS path — a `C:\...` or `\\host\...` literal, an
`os.Getenv(...)` value, or any variable the checker can see holds a filesystem
path — placed unquoted inside such a literal. A backslash in a Windows path
begins an escape the literal's grammar does not have, so the file parses on
Linux and darwin and fails on Windows. The allowed shape is `strconv.Quote(path)`
— or `oneline.Quote`, its wrapper — whose escaped output is a string on every
platform. It writes nothing. Its only input besides the tree is
`testdata/template-paths-allowlist.txt`: the existing offenders, each with the
file, the line, a reason and the date, and that file may only shrink — a new
entry is a refusal, not a place to park a path.

**Its one-line output.** On a clean tree it prints one line,
`CI-TEMPLATES OK tests=<n> allowlisted=<n> refused=0`, where `tests=` is the
`_test.go` files read, `allowlisted=` the entries still on the allowlist, and
`refused=` the unquoted paths found (always `0` on `OK`). On a refusal it prints
one line per offender, `CI-TEMPLATES file=<path> line=<n> kind=<join|literal>
remedy="<the one thing to do>"`, then closes with `CI-TEMPLATES FAIL tests=<n>
allowlisted=<n> refused=<k>`; the count is the truth about the tree whether or
not the lines printed.

**Its refusals (exit 2, one remedy line each).** A `filepath.Join(...)` inside a
JSON or template literal — `remedy="wrap the path in strconv.Quote"`. An OS path
placed unquoted inside a JSON or template literal —
`remedy="wrap the path in strconv.Quote; a backslash there is an escape the
literal does not have"`. A new line in the allowlist —
`remedy="quote the path; the allowlist only shrinks"`. A refusal names the file
and the line, so the queue’s PR comment is the whole diagnosis.

**The mistake it prevents.** A `filepath.Join(dir, "key")` concatenated raw
into a JSON worker description puts `C:\...\key` into the literal unquoted, and
the parse fails on Windows where Linux and darwin never see it.

**Red tests.**

1. A fixture `_test.go` whose JSON literal concatenates `filepath.Join(dir,
   "key")` raw is refused with its file and line, the fixture read from a tree
   given on the command line and not by walking the repository.
2. A test that places `C:\Users\RUNN\keys\key` unquoted inside a JSON literal is
   refused, with the worker description faked rather than read from disk.
3. A test that places a path unquoted inside a `text/template` string is refused,
   with the template rendered into a fake writer and no subprocess started.
4. A test that wraps the path in `strconv.Quote` is allowed, with the bench it
   guards faked.
5. A test using `oneline.Quote` is allowed, with the network it reports on faked.
6. Adding an entry to `testdata/template-paths-allowlist.txt` is refused, and
   removing one is allowed.

## The per-package test time budget

**The verb.** The budget check is `cmd/nova-ci`'s first verb, `slowtests`:

```
slowtests  read newline-delimited go test -json TestEvents on stdin; refuse any
           package whose summed elapsed time is over --budget (default 60s)
```

It runs as `go test -json -count=1 <packages> | tee "$RUNNER_TEMP/test.json"; go
run ./cmd/nova-ci slowtests --budget 60 < "$RUNNER_TEMP/test.json"` in the
self-hosted `test` step of `.github/workflows/ci.yml`, so the slow package
surfaces to the coordinator the moment it happens.

**The invariant.** A package's total is the sum of its package-level
`Elapsed` — the `pass`, `fail` or `skip` event whose `Test` is empty — and a
package whose total is over `--budget` is a refusal. The engine is the
`internal/ci/slowtests` subpackage's `Parse` and `Sum`: the events come from the
caller, the budget comes from the caller, and nothing reads a file, the clock or
the network. The test-level `Elapsed` rows are kept, sorted worst first, only so
a finding can name where the time went; they never decide the verdict.

**Its one-line output.** On a clean stream it prints one line, `CI-SLOW OK
packages=<n> slowest=<pkg>:<seconds>`, where `packages=` is the packages seen
and `slowest=` the single slowest package overall (or `slowest=none` when the
stream is empty). On a refusal it prints one line per offending package, `CI-SLOW
package=<pkg> seconds=<seconds> budget=<b> slowest=<TestA:3.2s,TestB:2.9s>`, the
slowest tests in that package, comma-separated, worst first and capped at three,
and exits 1 under `--enforce` (the check ran and said no; 2 is input it could not read), 0 without it; the lines go to stdout, so one `CI-SLOW` grep reads the whole run.
The stream is one `go test -json` line per event, parsed by `encoding/json`; a
line that is not a TestEvent is a refusal naming its line number, never a silent
skip, so a truncated pipe cannot read as a clean run.

**Its refusals (exit 2, one remedy line each).** A malformed line —
`remedy="stdin is not newline-delimited go test -json"`. A `--budget` of zero or
less — `remedy="--budget must be a whole number of seconds greater than zero"`. A
missing or unreadable invocation is the tool’s own one-line refusal,
`nova-ci slowtests REFUSED: <every problem>; run: nova-ci slowtests -h`; a line
that is not a TestEvent ends `run: go test -json <packages> | nova-ci
slowtests --budget 60`, and a terminal on stdin is refused at once rather than waited on.

**The budget is in one place.** The verb judges a LIVE run against the budgets
it is handed; there is no second budget and no recorded table a change is judged
against. The unit tier's budgets and where each is enforced are *The unit
tier's budgets* and *A budget verdict is the same on any machine* below.

**The mistake it prevents.** A package can sit at 120 seconds in the suite with
nobody noticing when nothing sums the per-package elapsed time `go test -json`
already prints. A green that hides a doubling suite is the same mistake as a
flaky wait, one layer up. The same mistake one layer out is a number RECORDED
and enforced against nothing; a live run's CI-SLOW line is where such a number
is read.

**The unit tier's budgets.** `make test` runs
`nova-ci slowtests --package-budget 2 --test-budget 1 --allowlist
internal/ci/slow-tests_allowlist.txt --sleeps
internal/ci/sleeps-skips_allowlist.txt`: a package over 2 s is the line above,
and a top-level test over 1 s is `CI-SLOW test=<name> package=<pkg>
seconds=<s> budget=<b>`, unless an allowlist row
(`pkg<TAB>test<TAB>seconds<TAB><measured>s@<where>`, `-` in the test column for
the package's own row) names a higher budget for exactly that package or test.
Every row names the time it was measured at and where: `run<id>` (a CI run) or
a bench (`slowtests.Benches`), never free
text (`2s@guess` is refused), with a budget between that time and three times
it (`TestSlowAllowlistRowsNameTheirMeasurement`,
`TestSlowAllowlistRatchetRefusesAnUnmeasuredRow`,
`TestSlowTestsMeasuredWhereIsARunOrABench`). A row is a measurement, never a
budget divided by a factor: an enforcing nightly run's raw times are what a row
is measured from.

**A budget verdict is the same on any machine.** Tests that run near 0.9 s
idle read 1.0-1.2 s on a host at half load, so a per-test budget enforced on a
loaded shard is red with no test failing, and a load gate that waives the
budgets makes the verdict depend on the load instead. So:

- **Enforced on every leg, load-independent, static:** no unit test waits on
  the wall clock (the `unitwaits` class test below), and a test skipped with
  the SLEEPS marker that the ledger does not name is `CI-SLEEPS test=<name>
  package=<pkg>` and exit 2, the push leg included.
- **Measured on every leg:** every CI-SLOW line and one `CI-LOAD load=<n>
  cpus=<n> per-cpu=<n>: measured, not a verdict` line (the host's load
  average, the larger of its 1- and 5-minute figures; `load=unknown` with the
  reason when it cannot be read) are printed, and the exit is 0 on them.
- **Enforced only on the nightly whole-tree run on the space legs:** ci.yml's
  `test` job runs on `schedule` too, test-packages deals that tree onto the
  space shards only, and `ci unit-test` on the nightly leg runs `make test
  GOTEST_COUNT_FLAG=-count=1 SLOWTESTS_ENFORCE=1`, which passes `--enforce`: a
  CI-SLOW line fails the run there (slowtests exits 1) and nowhere else. A red schedule run blocks
  nothing (ci-ok does not run on schedule); it is evidence, and its raw times
  are what a row is measured from. test-hosted's ubuntu leg was the other
  candidate (a fresh VM, idle by construction) and was not used: it runs `make
  test-short`, whose `-short` skips are not the unit tier the budgets describe.

(`TestUnitBudgetsJudgeTheTestNotTheLoad`, `TestSlowTestsVerdictIsTheSameAtAnyLoad`,
`TestSlowtestsVerdictIsTheSameAtAnyLoadEndToEnd`.) A push or manual run over the
whole tree keeps `--budget 60`, printed.

**Red tests.** `internal/ci/slowtests/slowtests_test.go` feeds canned TestEvent
lines through the parser and the summer, and `cmd/nova-ci/main_test.go` runs the
verb end to end:

1. A package at `3.2s` under a `60s` budget is `CI-SLOW OK packages=1
   slowest=example.com/pkg:3.2s`, exit 0.
2. A package summing `75.3s` with tests at `3.2s` and `2.9s` is one line naming
   the package, its total, the budget and `slowest=TestA:3.2s,TestB:2.9s`, exit
   2 under `--enforce` and 0 without it.
3. An empty stream is `CI-SLOW OK packages=0 slowest=none`, exit 0.
4. A line that is not JSON is a refusal naming its line number.
5. The slowest list is sorted and capped at three.
6. More than one package over budget prints one line each, worst first, an order
   that does not depend on map iteration.
## The CI class test against a real network host on the CI path

**The help line.** The class test is entered in the CI check roster and in help
as the verb `net`:

```
net     read every _test.go on the CI path; refuse a real network host or host:port
```

It runs as `go test ./internal/ci -run TestNoRealNetworkHostsOnTheCIPath`, and
it is the hard rule — *unit tests test LOGIC, not the network* — made an
official verb: every endpoint is mocked locally, and only the soak,
fuzz and nightly suites may reach the real network.

**What it reads and what it writes.** It reads, as text, every `_test.go` under
`internal/` and `cmd/` that the two-minute CL path runs, parses each as Go, and
refuses a string literal that names a real network host: an `http://` or
`https://` URL whose host is not a local endpoint, and a bare `host:port` whose
host is not one. A local endpoint is `localhost`, `127.0.0.1`, `::1`, or one of
the RFC 2606 / RFC 6761 reserved test domains `example.*`, `*.invalid` and
`*.test`; those can never reach a real service. A file whose header carries a
`//go:build nightly` or `//go:build soak` constraint is skipped whole, because
those are the suites where the real network is allowed. It writes nothing. Its
only input besides the tree is `testdata/net-allowlist.txt`: the existing
offenders, each with a reason and the date it was written, and that file may
only shrink — a new entry is a refusal, not a place to park a host. Like the
fixed-waits list it is matched by **file and kind, never by line**, so a merge
that shifts lines in a listed file does not turn dev red.

**Its one-line output.** On a clean tree it prints one line,
`CI-NET OK tests=<n> allowlisted=<n> refused=0`, where `tests=` is the
`_test.go` files read, `allowlisted=` the entries still on the allowlist, and
`refused=` the real hosts found (always `0` on `OK`). On a refusal it prints
one line per offender, `CI-NET file=<path> line=<n> host=<h>
remedy="<the one thing to do>"`, then closes with `CI-NET FAIL tests=<n>
allowlisted=<n> refused=<k>`; the count is the truth about the CI path whether
or not the lines printed.

**Its refusals (exit 2, one remedy line each).** An `http(s)` URL or bare
`host:port` whose host is not local, reserved or exempt —
`remedy="mock the endpoint with httptest or a local fake"`. A new line in the
allowlist — `remedy="fix the host; the allowlist only shrinks"`. A refusal names
the file, the line and the host, so the queue's PR comment is the whole
diagnosis.

**The mistake it removes.** A unit test that dials a real host passes on a
developer's laptop and fails, slowly, on the walled CI path — or worse, passes
because it reached a service the lane may not use, which is how a flake and a
secret leak look the same in the log.

**Red tests.**

1. A test carrying `https://api.acme.com` is refused with its file, line and
   host, and the remedy names httptest or a local fake.
2. A test whose only hosts are `localhost`, the loopback IPs and the reserved
   `example.*` / `*.invalid` / `*.test` names is allowed.
3. A file carrying `//go:build nightly` with a real host is exempt.
4. A file carrying `//go:build soak` with a real host is exempt.
5. A bare `host:port` with a real host is refused; one on a local or reserved
   host is allowed.
6. Adding an entry to `testdata/net-allowlist.txt` is refused, and removing one
   is allowed.
7. A row allows one offender of its kind in its file wherever the offender now
   stands; a second offender of the same kind in the file is refused.

## The CI class test against a child `go` that inherits the environment

**The help line.** The class test is entered in the CI check roster and in help
as the verb `goenv`:

```
goenv   read every .go under cmd/ and internal/; refuse a child `go` that inherits the caller's environment
```

It runs as `go test ./internal/ci -run TestGoEnvClassRuleHoldsOverTheRepository`.

**What it reads and what it writes.** It reads, as text, every `.go` file under
`internal/` and `cmd/` — tests included, because a test helper that builds a
binary is a tool spawning `go` exactly like a verb is — parses each as Go, and
refuses an `exec.Command` or `exec.CommandContext` whose `argv[0]` is the
literal `"go"` unless the function around it assigns that command's `Env` from
`goenv.Clean(...)`, directly or through an `append` onto it. The heuristic is
conservative on purpose: it asks to SEE the sanitized environment beside the
call, so a helper that hides it one frame away is refused rather than trusted.
`internal/goenv` itself and every `testdata/` directory are skipped. It writes
nothing. Its only input besides the tree is `testdata/goenv-allowlist.txt`: the
existing offenders, each with a reason and the date it was written, and that
file may only shrink — it is empty, because every site was fixed when the rule
landed. Like the fixed-waits and net lists it is matched by **file and kind,
never by line**.

**What `goenv.Clean` drops.** `GOFLAGS`, every `GOTEST*` variable, any other
`GO`-prefixed variable whose value carries a `-json` or `--json` flag, and every
variable whose NAME carries `KEY`, `TOKEN` or `SECRET` — a forge token
(`GH_TOKEN`, `GITHUB_TOKEN`), a provider key, a secret — because a tool that
runs checks whose code came from a pull request runs them in a child built from
`Clean`. The credential drop is by NAME and never by
value. `GOTMPDIR` is deliberately kept: it names a location, not an output shape, and a
tool that wants its own scratch appends `GOTMPDIR=` after `Clean`, where the
last value wins.

**Its one-line output.** On a clean tree it prints one line,
`CI-GOENV OK files=<n> allowlisted=<n> refused=0`. On a refusal it prints one
line per offender, `CI-GOENV file=<path> line=<n> func=<name>
remedy="<the one thing to do>"`, then closes with `CI-GOENV FAIL files=<n>
allowlisted=<n> refused=<k>`.

**Its refusals (exit 2, one remedy line each).** A child `go` command with no
sanitized environment — `remedy="set cmd.Env = goenv.Clean(os.Environ())"`. A
row in the allowlist that names no offender —
`remedy="delete the stale row; the allowlist only shrinks"`.

**The mistake it prevents.** A tool that reads the output of a `go` command it
started is reading a shape the caller can change. CI's `make test` exports
`GOFLAGS=-json`; an inner `go test` that inherits it answers in JSON with no
`--- PASS:` line in it, and a parser counting `--- PASS:` lines reads a green
unit as red, failing CI legs on a tool that is working.

**Red tests.**

1. A `runUnits` that starts a child `go test` with no sanitized environment is
   refused with its file, its line, its function and the remedy.
2. The fixed shape is allowed, both spellings: `goenv.Clean(os.Environ())` and
   an `append` onto it for the tool's own variables.
3. `cmd.Env = append(os.Environ(), ...)` is still refused — the environment is
   set, but it is the caller's, so `GOFLAGS` travels — and a non-`go` command in
   the same file is not this rule's business.
4. A row that names no offender is refused, so the list only shrinks.
5. A row holds one offender of its kind in its file wherever it now stands.
6. The rule holds over this repository with an empty allowlist.

## The class tests

The sections above are the class tests written out in full. This section is
the **index**: one entry for every class test this repository runs, so a friend
meeting a red for the first time can read the rule, the mistake it prevents and
the one thing to do, without reading the test. The long sections stay; an entry
here that has one points at it.

**What makes a test a class test.** It reads this repository's own text — the
`.go` files, `.github/workflows/*.yml`, the `Makefile`, `docs/` — and refuses a
SHAPE wherever it stands, rather than exercising one function. It is the fix for
a whole class made mechanical, which is the only kind of fix that survives the
next card: a rule lands with its sweep of the tree, or it does not land
(pit-stop ledger item 20, which lives in the `rowan-new`
repository at `reports/pitstop-tests-2026-09-17.md` — a sibling checkout, not
this one, so the citation is deliberately prose and not a link).

**The marker.** A class test is a `Test` function in `internal/ci` that is
either declared in a `*_class_test.go` file or named with one of the quantifier
prefixes `TestNo…`, `TestEvery…` or `TestOnly…` — the name says the rule holds over
the whole tree, which is what a class test is. **Every such function must be
named by an entry in this section, and every `Test…` name this section prints
must exist under `internal/ci`.** Both halves are held by
`internal/docs/spec_ci_index_test.go`, so the index cannot rot: a new class test
with no entry is red, and an entry naming a test that was renamed or deleted is
red. The neighbouring tests in this package that pin the workflow's shape rather
than a class — the CL-tier budget, the integration-branch list,
`TestFleetProbeRunsTheNetworkProbeInsideNovaSandbox` — carry no marker and
are not indexed here; they are read from `internal/ci` directly.

**The shared conventions.** Every class test that carries exceptions keeps them
in shrink-only ledgers under `internal/ci/testdata/`, checked in BOTH
directions: an offender that is not listed is red, and a listed entry that names
no offender is also red, so fixing a site means deleting its row in the same
change and a new row parks nothing. The newer lists are matched by **file and
kind, never by line**, so a merge that shifts lines in a listed file does not
turn dev red. Every list is read and written by the one helper,
`internal/ci/allowlist` (`allowlist.Load`, `allowlist.Check`): under
`NOVA_CI_UPDATE=1` a class test rewrites its list to the set it measured --
stale rows dropped, comments and kept rows byte for byte -- and fails once with
`updated, rerun`, so a removal regenerates every list with one variable and no
script edits a list file. Every list here is ceiling-only: under the update it
refuses to grow and says so, one line per key ([TESTING.md](TESTING.md)). And
every entry below names its **narrowings** — the false
negatives the heuristic accepts on purpose — because a class test with false
positives is one people learn to edit around, and a narrowing nobody wrote down
is read as coverage.

**The generated index.** `make map` derives the complete by-name list in
`docs/STANDARD.md` from the named entries in this section and embeds the same
text in the root map. The list between its generation markers is generated,
never edited by hand. The committed-map check compares both documents with the
rendered result and names `make map` as the remedy for a stale list.

**Counted ledgers by package.** The `discarded`, `scripthide`, `okonfailure`,
`remedy`, `testify`, `generality` and `generality-text` ledgers keep their files at
`internal/ci/testdata/<ledger>/<package>.txt`. A package is the repository-relative
parent directory of the row's source file; scripts and other non-Go files use
their source directory in the same way. The testify ledger already keys each
row by package and kind, and uses that package directly. Nested directories stay nested, so
packages with the same basename remain distinct. Root-level source files use
`@root.txt`; each real path component beginning with `@` gains one leading `@`,
so that spelling cannot collide with a source directory. Row keys retain the full source
path, kind or token, count, and any reason. Every shard has its own row ceiling;
loading the ledger checks every shard, including a package with no remaining
findings. A missing shard holds no permitted debt.

Fix a site first, then run `NOVA_CI_UPDATE=1 make test PKGS=./internal/ci`.
The counted helper reads every shard and refuses an unlisted key, increased
count, malformed row or foreign-package row before writing any shard. It lowers
only counts that shrink, drops stale rows, and lowers that shard's ceiling.
Retained rows keep their trailing reasons and comment lines when a count falls;
only the count field changes, including in rows separated by tabs or Unicode spaces.
Only changed shard files are replaced; unchanged packages keep their bytes and
modification times. A changed ledger reports `updated, rerun`; rerun without the
variable to check the result. An unchanged counted ledger is not rewritten.
Each replacement is atomic for its file; the update is not a transaction across
multiple files. Whole-file generality fixture exemptions remain a separate,
exact-path ledger with their reasons and stale-row check. Its own exact path
is a recorded-data entry because its keys preserve source directory names;
this entry covers the identifier ledger, not the files its rows name. Each
referenced fixture still needs a reason and a finding, and each counted shard
is parsed and checked by its class rule.

### `waits` — no fixed wall-clock wait on the CI path

**The rule.** No `_test.go` on the CL path carries a `time.Sleep` over 100 ms, a
context or timer bound under ten seconds used as a pass/fail condition, or an
assertion on elapsed time; it polls for the event up to `NOVA_TEST_WAIT`
(default `30s`) or injects a fake clock.
**The mistake it prevents.** A wait cut to fit the two-minute gate makes a test
flaky under load, and a flaky test drops innocent PRs from the queue.
**The test.** `TestNoFixedWaitsOnTheCIPath` (`internal/ci/ci_waits_test.go`),
with its six red tests — `TestWaitsRefusesFixedSleep`, `TestWaitsRefusesShortBound`,
`TestWaitsRefusesElapsedAssertion`, `TestWaitsAllowsThePoll`,
`TestWaitsAllowsFakeBenchAndClock`, `TestWaitsAllowlistGrowsRefused` — and
`TestWaitsAllowlistSurvivesShiftedLines` for the file-and-kind match.
**Its allowlist.** `internal/ci/testdata/fixed-waits-allowlist.txt`, the waits
that predate the checker, one `file:line kind date reason` per row; shrink-only.
**Its remedy lines.** `remedy="poll for the event up to NOVA_TEST_WAIT, not a
fixed sleep"`, `remedy="raise the bound to thirty seconds or more, or inject a
fake clock"`, `remedy="assert the event, not the clock; use a fake clock if the
code needs one"`, and for a new row `remedy="fix the wait; the allowlist only
shrinks"`.
**Its narrowings.** It reads text, so a duration built from a variable or a
constant in another file is invisible; a sleep under 100 ms is allowed outright;
and a bound handed to fake-driven code as an INPUT is out of scope — only the
shapes above are read. Full section: *The CI class test against fixed waits on
the CI path*.

### `testbins` — no built executable copied into a fixture

**The rule.** A `_test.go` on the CI path places a built program into a fixture
with `internal/testbin.Place`, which hard-links first and copies only where a
link is impossible (another filesystem, Windows); an `os.WriteFile` with an
execute bit of bytes read by `os.ReadFile`, or an `io.Copy` into an executable
`os.Create`/`os.OpenFile`, is refused. `testbin.PlaceCopy` is the one exception,
for a test whose subject is that the file is NOT the same binary.
**The mistake it prevents.** One built fake runner copied into dozens of
fixtures queues behind the macOS policy scanner past the thirty seconds a test
waits; hard-linking the one fixture passes in less than half the time.
**The test.** `TestNoCopiedTestBinariesOnTheCIPath`
(`internal/ci/ci_testbins_test.go`), with `TestTestbinsRefusesACopiedBinary`,
`TestTestbinsRefusesAMapHeldCopy`, `TestTestbinsAllowsAShellScript`,
`TestTestbinsAllowsThePlacedHelper`, `TestTestbinsAllowlistGrowsRefused`,
`TestTestbinsOutputMatchesTheSpec`, `TestTestbinsVerbLineMatchesTheSpec` and
`TestTestbinsAllowlistSurvivesShiftedLines` for the file-and-kind match.
**Its allowlist.** `internal/ci/testdata/fixed-testbins-allowlist.txt` — empty,
because every site places by link through `testbin.Place`; shrink-only.
**Its remedy lines.** `remedy="place built binaries with testbin.Place: link,
never copy"`, and `remedy="fix the copy; the allowlist only shrinks"` for a new
row.
**Its narrowings.** It follows the bytes within ONE file, so a copy through a
helper in another file, or through `exec.Command("cp", …)`, is not seen; the
mode must be an integer literal, so a mode held in a variable is not guessed at;
and a shell script written `0o755` is allowed outright, because the interpreter
is the executable. Full section: *The CI class test against copied built
binaries*.

### `templates` — no unquoted filesystem path in a JSON or template literal

**The rule.** A path placed inside a JSON or `text/template` string literal is
wrapped in `strconv.Quote` (or `oneline.Quote`); a raw `filepath.Join(...)` or a
`C:\…` literal there is refused.
**The mistake it prevents.** A `filepath.Join(dir, "key")` concatenated raw
into a JSON worker description puts `C:\…\key` in a literal whose grammar has no
such escape, so the parse fails on Windows where Linux and darwin never look.
**The test.** `TestNoUnquotedPathsInTemplateLiterals`
(`internal/ci/ci_templates_test.go`), with `TestTemplatesRefusesRawFilepathJoin`,
`TestTemplatesRefusesUnquotedOSPathLiteral`, `TestTemplatesRefusesUnquotedPathInTemplate`,
`TestTemplatesAllowsStrconvQuote`, `TestTemplatesAllowsOnelineQuote` and
`TestTemplatesAllowlistGrowsRefused`.
**Its allowlist.** `internal/ci/testdata/template-paths-allowlist.txt`, four
`ToSlash(Join(...))` rows in `internal/swarm/inputlimit_test.go` that predate the
checker; shrink-only.
**Its remedy lines.** `remedy="wrap the path in strconv.Quote"`, and
`remedy="quote the path; the allowlist only shrinks"` for a new row.
**Its narrowings.** Path-valued is what the checker can SEE — a literal, an
`os.Getenv`, or a variable it watched being assigned a path in the same function;
a path arriving through a struct field or a helper is not read. Full section:
*The CI class test against unquoted paths in JSON and template literals*.

### `net` — no real network host on the CI path

**The rule.** Unit tests test LOGIC. No
`_test.go` on the CL path may name a real host in a URL or a bare `host:port`;
endpoints are `httptest` or a local fake, and fixtures name `example.com`,
`*.invalid` or `*.test`. Only `//go:build nightly` and `//go:build soak` files
may reach the network.
**The mistake it prevents.** A unit test that dials a real host passes or
fails on the network, not on its logic. A fixture carrying an
`https://github.com/…` literal that never dials is read as a dial too, and the
honest fix is to generalize the code to any origin host and retarget the
fixture — splitting the literal to hide it from the class is an evasion.
**The test.** `TestNoRealNetworkHostsOnTheCIPath` (`internal/ci/ci_net_test.go`),
with `TestNetRefusesRealURLHost`, `TestNetAllowsLocalAndReservedHosts`,
`TestNetAllowsNightlyBuildTag`, `TestNetAllowsSoakBuildTag`,
`TestNetRefusesBareHostPort`, `TestNetAllowsLocalHostPort`,
`TestNetAllowlistGrowsRefused` and `TestNetAllowlistSurvivesShiftedLines`.
**Its allowlist.** `internal/ci/testdata/net-allowlist.txt`, matched by file and
kind; shrink-only.
**Its remedy lines.** `remedy="mock the endpoint with httptest or a local fake"`,
and `remedy="fix the host; the allowlist only shrinks"`.
**Its narrowings.** It reads a whole URL or `host:port` LITERAL, so a host
assembled at run time from parts is invisible, and a literal that never dials is
still refused — the false positive is deliberate, because telling the two apart
needs the call graph and the cost of guessing wrong is a secret leak that looks
like a flake. Full section: *The CI class test against a real network host on the
CI path*.

### `goenv` — a child `go` never inherits the caller's environment

**The rule.** Every `exec.Command("go", …)` in `cmd/` and `internal/`, and
every `subproc.Command`, `subproc.CommandFor`, `subproc.Context` and
`subproc.Long` given the literal `"go"`, sets `cmd.Env` from `goenv.Clean(...)`;
a tool that reads the output of a `go` it started must not let the caller choose
that output's shape.
**The mistake it prevents.** CI's `make test` exports `GOFLAGS=-json`; an
inner `go test` that inherits it answers in JSON with no `--- PASS:` line, and a
parser counting those lines reports a green unit as red.
**The test.** `TestGoEnvClassRuleHoldsOverTheRepository`
(`internal/ci/ci_goenv_test.go`), with `TestGoEnvRefusesThePreFixMutate`,
`TestGoEnvAllowsCleanEnvironment`, `TestGoEnvRefusesTheCallersOwnEnviron`,
`TestGoEnvRefusesAStaleAllowlistRow`, `TestGoEnvAllowlistHoldsOneOffender` and
`TestGoEnvVerbLineMatchesTheSpec`.
**Its allowlist.** `internal/ci/testdata/goenv-allowlist.txt` — empty, because
every site was fixed when the rule landed; shrink-only, so it stays that way.
**Its remedy lines.** `remedy="set cmd.Env = goenv.Clean(os.Environ())"`, and
`remedy="delete the stale row; the allowlist only shrinks"`.
**Its narrowings.** It asks to SEE the sanitized environment beside the call, so
a helper that sets `Env` one frame away is refused rather than trusted (a false
positive it accepts), and a `go` spawned through a variable command name or a
shell is not seen at all. Full section: *The CI class test against a child `go`
that inherits the environment*.

### `subproc` — every child process has a bound or a cancellable context

**The rule.** Production code under `cmd/`, `internal/` and `tools/` starts a
child through `internal/subproc` or `internal/gitrun` and never through a bare
`exec.Command`. A one-shot child (git, gh, ssh, sops, tailscale, go tooling, ps)
runs under `subproc.Command` or `gitrun`: the caller's own context deadline when
it is sooner, else the named default of its kind (git 60 s, and 300 s for a git
that goes to the network or moves a whole tree; gh 120 s; ssh 300 s; go tooling
300 s; other tools 60 s), and `WaitDelay` of 5 s. A long-lived child (a harness
run, a member's native child, a server) runs under `subproc.Long`: a cancellable
context, no deadline. An `exec.CommandContext` outside those two packages stands
only in a function that assigns a `WaitDelay`. `subproc.Context` and
`subproc.Long` are never handed `context.Background()`, `context.TODO()` or
`nil`, directly or through a local: the caller's context, or one derived with
`context.WithCancel`, goes in.
**The mistake it prevents.** A hung git, ssh or sops blocked its caller for as
long as the child chose to live; and a killed child whose own child kept the
pipe open still blocked `Output`, because the kill ends the process and not the
pipe.
**The test.** `TestEveryChildProcessGoesThroughTheSubprocDoor`
(`internal/ci/subprocess_bound_class_test.go`), with
`TestSubprocessClassTestRefusesItsProbes`, which pins each shape it refuses (a
`Background`/`TODO`/`nil` context, a `WaitDelay` that is only a comment or a
string or sits in another function, an aliased or dot-imported `os/exec`).
**Its allowlist.** `subprocBackgroundAllowed` in the test, a `file:Func` and a
reason each; empty, because every long-lived child derives its context.
**Its remedy lines.** The message names the file, the line and the door to use.
**Its narrowings.** It reads call sites by import path, so a child started
through `os.StartProcess` or an `exec.Cmd` literal is not seen, and it asks that
the function assign `WaitDelay`, not that it be the very command.

### `gitoperand` — a card-derived git operand follows `--`

**The rule.** A git call under `internal/swarm` and `cmd/nova-swarm` puts every
operand that is not a literal behind `--`, or behind `--end-of-options` for a rev
that `--` would turn into a path. Git 2.43, the oldest the benches carry, honours
`--end-of-options` in `rev-parse --verify`, `cat-file`, `show`, `ls-tree`, `rev-list` and
`switch`; it does not in `git checkout` (the rev is read as a path) or `git grep` (the
tree-ish is read as a revision), so staging switches to the card's sha with
`git switch -C`, and the one `git grep` takes a tree-ish only in the 40 hex digits
`rev-parse` printed. A card is untrusted input: its `base-repo:`,
`base-sha:`, `BASE:` ref and `PR-HEAD:` reach git, and a value that starts with `-`
is read as an option when nothing separates it. Staging also refuses such a value
by name before any git runs.
**The mistake it prevents.** `git remote set-url origin <base-repo>` read a
`base-repo: --bogus` as a flag, and the regression test for the set-url step relied
on exactly that.
**The test.** `TestCardDerivedGitOperandsFollowTheSeparator`
(`internal/ci/gitoperand_class_test.go`), with
`TestGitOperandClassTestRefusesItsProbes`, which pins each shape it refuses and its
neighbour that is fine, and reads the real `stage.go` with its `--` removed, and
`TestGitOperandClassTestSeesTheCatFileSeparatorInStage`, which strips the separator from
the `cat-file -e` call there, where `-e` takes no value, and must go red. A git
call is a swarm git helper (`stageGit`, `baseGit`, `gitOut`, `gitOutput`), a `gitrun`
runner found by import path, `exec.Command` or `exec.CommandContext` of the literal
`"git"`, or an argv built apart from its call (a `[]string` literal that starts with a
git subcommand, and `append` onto it). An argument is fine when it is a literal, the
value of an option that takes one (`-C`, `-B`, `--reference`, and `-e` after `grep`
only: `cat-file -e` takes no value), after the
separator, or a concatenation that begins with a literal that does not start with
`-`.
**Its allowlist.** `gitOperandAllowed` in the test, a `file:Func` and a reason each:
`testDefinedAt` (the `git grep` above, which refuses any tree that is not a full hex
sha before it runs).
**Its remedy lines.** The message names the file and the line and the separator to
add.
**Its narrowings.** It reads the syntax: a final `args...` spread is read where the
argv is built, not at the call, and an argv assembled through a path it cannot see (a
function that returns a slice built elsewhere) is not followed. It does not know which
values a card supplies, so every non-literal operand in these two packages is held to
the rule.

### `slowtests` — no package over the per-package time budget

**The rule.** A package whose summed `go test -json` package elapsed time is over
`--budget` (default 60 s) is a refusal, printed where the coordinator sees it.
**The mistake it prevents.** A package can sit at 120 s in the suite with
nobody noticing when nothing sums the elapsed time `go test -json` already
prints. A green that hides a doubling suite is a flaky wait one layer up, and
slow CI brings everything to a crawl.
**The test.** `TestSlowTestsUnderBudgetIsOK`,
`TestSlowTestsOverBudgetNamesThePackageAndSlowestTests`,
`TestSlowTestsEmptyInputIsOKWithZeroPackages`,
`TestSlowTestsMalformedLineIsRefused`,
`TestSlowTestsSlowestListIsSortedAndCapped` and
`TestSlowTestsOverPackagesAreOrderedWorstFirst`
(`internal/ci/slowtests/slowtests_test.go`), with the verb run end to end in
`cmd/nova-ci/main_test.go`.
**Its allowlist.** `internal/ci/slow-tests_allowlist.txt`, one row per package
or test allowed over the unit tier's 2 s / 1 s budgets, each naming its
measured time and where, its budget at most three times that
(`TestSlowAllowlistRowsNameTheirMeasurement`).
**Its remedy lines.** `remedy="stdin is not newline-delimited go test -json"` and
`remedy="--budget must be a whole number of seconds greater than zero"`.
**Its narrowings.** It judges on the PACKAGE total only; the test-level rows are
kept, sorted worst first and capped at three, purely so a finding can say where
the time went. It sees one run on one machine, so a package that is fast on one
bench and slow on another is two measurements, and an allowlist row names where
its time was measured. Full section: *The per-package test time budget*.

### `removeall` — no `os.RemoveAll` of a computed path

**The rule.** A computed `os.RemoveAll` is one mistake away from deleting the
whole disk. Outside `internal/safepath`, `os.RemoveAll` may only take a variable
that came back from `os.MkdirTemp` in the same function; every other removal goes
through `safepath.RemoveUnder(root, path)`, which refuses an empty path, the root
itself, a path outside the root and a symlink.
**The mistake it prevents.** A computed slot path handed to `os.RemoveAll`
removes whatever the computation names, including a root or an empty path;
`safepath.RemoveUnder` refuses those shapes before anything is removed.
**The test.** `TestRemoveAllOnlyOnTempOrThroughSafepath`
(`internal/ci/removeall_class_test.go`).
**Its allowlist.** `internal/ci/testdata/removeall_allowlist.txt`, one
`file:function` per row — three, each a `MkdirTemp` dir removed in its own
function (`internal/secrets/seal.go` ×2, `internal/secrets/sops.go`);
shrink-only in both directions.
**Its remedy lines.** `os.RemoveAll of a computed path; route it through
safepath.RemoveUnder(root, path) so an arbitrary directory is refused`; for an
unlisted temp removal, `a new raw removal needs the safepath.RemoveUnder route or
an allowlist entry with a reason`; for a stale row, `delete the stale entry (the
list only shrinks)`.
**Its narrowings.** Non-test `.go` files only — a test that removes a computed
path is not read. It follows `MkdirTemp` within one function, so a temp directory
passed in as an argument reads as computed (refused, and the remedy is the
safepath route anyway). `os.Remove`, `RemoveAll` behind an interface, and a shell
`rm` in a script are other rules' business.

### `fieldsindex` — no unchecked index into a split result

**The rule.** An index or slice expression on the result of `strings.Fields`,
`strings.Split` or `bytes.Fields` must be preceded by a `len(x)` comparison in
the same function. The length of that slice is decided by the DATA, not by the
code: a line with fewer separators than the code expects yields a shorter slice,
and the next subscript panics.
**The mistake it prevents.** A commit-only cursor line splits into two fields
and a walker reaches `fields[2:]`. The instance is one missing `len(fields) < 3`
refusal; the class is every other place a splitter's answer is trusted to be as
long as the code assumed. A panic is the worst shape for this — it takes the
whole verb down, in a loop nobody is watching, on the one input nobody had.
**The test.** `TestNoUncheckedFieldsIndex`
(`internal/ci/fieldsindex_class_test.go`), with the rule proved over source in
`internal/ci/fieldsindex_rule_test.go`:
`TestFieldsIndexRefusesThePreFixCursor` (the short cursor line),
`TestFieldsIndexAcceptsTheFixedCursor`,
`TestFieldsIndexAcceptsTheShapesThatCannotBeShort`,
`TestFieldsIndexRefusesTheShapesThatCanBeShort` (the narrowings are narrow),
`TestFieldsIndexKeyIsFileAndFunction` and
`TestFieldsIndexAllowlistIsShrinkOnly`.
**Its allowlist.** `internal/ci/testdata/fieldsindex_allowlist.txt`, one
`file:function # reason` per row — empty. Shrink-only in both directions, and
every row must carry a reason.
**Its remedy lines.** `<index|slice> expression on <x>, the result of <splitter>,
with no len(<x>) comparison in <func>; add the length check and a refusal line,
or an allowlist row in testdata/fieldsindex_allowlist.txt with the reason`; for a
stale row, `delete the stale row (the list only shrinks)`.
**Its narrowings.** Four shapes that cannot be short are read as measured:
`len(x)` in the header of a `for` (the reverse walk `for i := len(x) - 1; i >= 0;
i--`), `len(x)` inside the subscript itself (`x[len(x)-1]`), `switch len(x)`,
and `range x`. A fifth is in range by construction: `strings.Split` with a
non-empty **string literal** separator always returns at least one element, so
`x[0]` and `x[1:]` on it are not read — a computed separator, which may be empty,
gets no such pass, and neither does `strings.Fields`, which returns nothing for a
blank string. The rule asks only that the length was LOOKED at in the same
function: deciding which branch a comparison guards needs the control-flow graph,
and a function that measures the slice and still indexes it wrongly is a
different mistake from one that never measured it at all. A split result passed
to another function, stored in a struct field, or indexed through a second
variable is not followed. Non-test `.go` files under `cmd/` and `internal/` only.

### `hostseam` — no test reaches a host through an unfaked seam

**The rule.** Every function under `cmd/` and `internal/` that reaches another
machine calls `testguard.RefuseHosts(<program>, <args>…)` before it starts the
child. Under `NOVA_TEST_NO_HOST`, which `make test` exports for every tier, that
call panics with the command line, so a test that constructed production code
and injected no fake refuses THERE instead of on a bench. A function is a host
seam when it starts a child (`exec.Command`/`exec.CommandContext`) and names an
ssh-family program — `ssh`, `scp`, `sftp`, `rsync` — in a string literal, or
when its own name or its receiver's carries one of those words.
**The mistake it prevents.** A unit test that holds production code and
injects no fake runs the REAL workloads on a bench: nobody wrote a hostname in
the test, but production code builds its own default, and that default is
`exec.Command("ssh", …)`. The `net` class reads test files for a real host and
cannot see it: the host is never in the test's text, it is in a default two
packages away. That is the difference between the two rules — `net` reads what a
test SAYS, this reads what production DOES.
**The test.** `TestNoTestReachesAHostThroughAnUnfakedSeam`
(`internal/ci/hostseam_class_test.go`), with `TestNoHostSeamIsFoundByASubstring`,
the table that pins the name heuristic against three false positives
(`IsSHA`, `HarnessSHA256`, `hasShebang`). The guard itself is
`internal/testguard`, held by `TestUnsetGuardLetsTheSeamRun`,
`TestArmedGuardNamesTheCommandAndTheRemedy`, `TestAFakeOnPATHIsNotAHost` and
`TestAllowHostsIsScopedAndNests`.
**Its allowlist.** `internal/ci/testdata/hostseam_allowlist.txt`, one
`file:function  # reason` per row — three, each a function that starts no
child of its own (`ExecSSH.sshArgs`, whose callers each guard the child they
start; an error type named for ssh's exit code; and a Redis writer of a bench's
ssh cell). A row with no
reason is refused, and the list is checked in both directions, so it only
shrinks.
**Its remedy lines.** ``add `testguard.RefuseHosts(<program>, <args>...)` before
the child runs, or list it in testdata/hostseam_allowlist.txt with a reason``;
for a guard below the exec, `a guard that runs once the host has been reached
guards nothing`; for a stale row, `delete the stale entry (the list only
shrinks)`; and from the guard itself, `inject the fake the seam takes, or
install a fake on PATH and declare it with testguard.AllowHosts()`.
**Its narrowings.** A seam that reaches a host without spawning an ssh-family
program — a Go SSH library, a raw socket — is not seen; nothing in this tree
does that. The guard treats a program that resolves INSIDE a temp
directory as a fake, which is what this repository's fake `ssh` scripts are, so
a test that installs its fake somewhere else must say `defer
testguard.AllowHosts()()`, and a test that builds the real seam while another
fake sits on PATH is not caught. `AllowHosts` is process-wide for its scope, so
a test that opens one must not run in parallel with one relying on the guard.
And the class test reads names and literals, not types: a program held in a
variable, in another package's constant, or behind an interface is invisible to
it — which is why the rule is written where the CHILD is started.

### `pathassert` — no test compares a path against a slash literal

**The rule.** A test that compares a path against a string literal containing `/`
asserts the SEPARATOR, not the behaviour; compare `filepath.ToSlash(got)`, or
build the want side with `filepath.Join`.
**The mistake it prevents.** A path asserted with slashes is red on Windows and
green everywhere else. It is one of the three shapes behind Windows-only reds —
with the execute-bit assertion and the unsuffixed fake `.exe`.
**The test.** `TestNoTestComparesAPathAgainstASlashLiteral`
(`internal/ci/pathassert_class_test.go`), with
`TestPathAssertHeuristicReadsWhatItClaims`, the table that proves the heuristic
flags what the comment says it flags and nothing else.
**Its allowlist.** `internal/ci/testdata/pathassert_allowlist.txt` — empty: no
path-against-literal assertion is permitted; shrink-only.
**Its remedy line.** `a path is compared against the literal <lit>; on Windows
that path comes back with backslashes. Compare filepath.ToSlash(got) against the
literal, or build the want side with filepath.Join.`
**Its narrowings.** Written out in the test and worth repeating: path-valued is
narrow on purpose (a call to one of the named path producers, or an identifier
assigned from one earlier in the same function), and the comparison must sit in
an `if` whose body reports with `t.Errorf`/`t.Fatalf`. A path in a struct field,
a path returned through a helper, a comparison written without an `if`, and a
table-driven case whose want column carries a slash are all real instances this
test is silent about. The Windows leg on the PR is what catches the rest — the
class test buys the cheap half.

### `testoutpath` — a path a tool writes is named inside `t.TempDir()`

**The rule.** A test that runs a tool may not give a relative string literal to
an output flag (`-o`, `--out`, `--output`, `--graph`): the run writes it into the
package directory and leaves it in the tree.
**The mistake it prevents.** A test that runs a tool with `--out deps.json`
leaves `deps.json` in the package directory, and the tree carries it. The same
class one directory along is a wrong `TMPDIR=$PWD/scratch` line, replicated,
leaving junk files under `scratch/` on dev.
**The test.** `TestToolRunsInTestsWriteIntoATempDir`
(`internal/ci/testoutpath_class_test.go`), with
`TestRelativeOutputPathScannerReadsTheFixtures` over the before/after fixtures in
`internal/ci/testdata/testoutpath/`.
**Its allowlist.** `internal/ci/testdata/testoutpath_allowlist.txt`, one
`file:function` per row — EMPTY, which is the point: an offender is fixed
rather than listed; shrink-only in both directions.
**Its remedy line.** `name it inside t.TempDir(): filepath.Join(t.TempDir(), ...)`.
**Its narrowings.** Two, named out loud. The defect that prompted it is NOT
caught by it: a relative output path that is not in the `_test.go` — one that
comes out of a usage banner in `main.go` or out of `docs/TESTS.md`, run as a
transcript verbatim — is not seen. And a function that chdirs is skipped whole, because
`os.Chdir`/`t.Chdir` into a temp directory makes a relative path safe again and
telling the safe chdir from the unsafe one means knowing where it went. Only
output flags are read; an input flag may name a `testdata` fixture relatively,
which is correct.

### `sharedtemp` — a directory a test LISTS is its own, never `os.TempDir()`

**The rule.** No `_test.go` under `cmd/` or `internal/` lists a directory built
from `os.TempDir()` — no `filepath.Glob`, `os.ReadDir` or `ioutil.ReadDir` over
an expression naming it, directly or through a local assigned from it. It is the
read side of `testoutpath`: that one holds the paths a tool WRITES inside
`t.TempDir()`, this one holds the directories a test READS.
**The mistake it prevents.** A test that proves a tool removes its throwaway
worktree by globbing `os.TempDir()/<tool>-*` before the run and again after it,
refusing any entry that was not in the snapshot, is exact on a laptop; on a
self-hosted runner `os.TempDir()` is shared with every other job on the box, and
a sibling shard starting its own run between the two listings puts a directory
in the glob this test never made — reds with no defect.
**The test.** `TestNoTestGlobsTheSharedTempDir`
(`internal/ci/sharedtemp_class_test.go`), with
`TestSharedTempReadScannerReadsTheFixtures` over the before/after fixtures in
`internal/ci/testdata/sharedtemp/`.
**Its allowlist.** `internal/ci/testdata/sharedtemp_allowlist.txt`, one
`file:function` per row with its reason — EMPTY, which is the point: an
offender is fixed rather than listed; shrink-only in both directions.
**Its remedy line.** `give the tool a temp root option defaulting to
os.TempDir(), pass t.TempDir() from the test, and read THAT directory: the
assertion stays "nothing left behind", over a directory only this test writes`.
**Its narrowings.** Two, named out loud. The taint is per FUNCTION and per LOCAL:
a variable assigned `os.TempDir()` anywhere in the same body taints every listing
of it, but a package-level variable, a struct field or a value handed in by a
caller is invisible — following those means becoming a type checker, and the
shape that hurt is written inline. And only LISTINGS are read: `os.Stat`,
`os.Open` and `os.RemoveAll` over one named path in the shared directory are
questions about that path, which no sibling job can answer wrongly.

### `busprogress` — progress never enters a protocol stream

**The rule.** The rule has two halves: a program that takes over 0.1 s says
what it is doing on stderr, AND a progress line never enters a stream a consumer
parses. Every package that starts `nova-bus` and READS what it said asks
`internal/bus` — `bus.IsProgress(` or the one classifier that does, `Classify(` —
whether a line is progress.
**The mistake it prevents.** `nova-bus`'s since-walk narrates `INBOX WALK
commits=1/1 notes=0 elapsed=3ms` on stderr exactly as the first half asks; a
consumer that merges stdout and stderr on purpose, so an `INBOX REFUSED` is
never lost, and whose classifier's default case PRINTS, relays the progress line
as a bus line, counts it as a change in the world, and ends a poll before the
mail arrives.
**The test.** `TestEveryNovaBusConsumerDropsProgressLines`
(`internal/ci/busprogress_class_test.go`); the producer half it indexes is
`TestProgressNeverEntersTheProtocolStream` in `cmd/nova-bus`, and the consumer half is this test itself.
**Its allowlist.** None. The registry is `internal/bus/protocol.go` and a
consumer either reaches it or discards both streams; a start that reads nothing
back is not a consumer and is not held to this.
**Its remedy line.** `<pkg> reads nova-bus's output and nothing in its package
reaches internal/bus.IsProgress; a progress line on stderr will be parsed as
protocol -- drop progress through the registry, or read stdout alone`.
**Its narrowings.** It is per-PACKAGE and textual: a package that names
`bus.IsProgress(` anywhere satisfies it, even if the one reader that matters does
not call it, and a consumer that starts `nova-bus` through an indirection the
walk cannot see is invisible. The registry file's existence is asserted, not its
contents.

### `outputs` — no multi-line value written to a step output

**The rule.** `$GITHUB_OUTPUT` and `$GITHUB_ENV` are `key=value` FILES, one pair
per line. A variable assigned from a one-item-per-line producer (`go list` over a
`...` pattern, `git diff --name-only`, `git ls-files`, `find`, `ls`, `cat`,
`printf '%s\n'`) with no single-line guard (`tr`, `paste`, `xargs`, `jq -c`,
`head -1`, `tail -1`, `wc -l`) may not be written to either.
**The mistake it prevents.** A package selection that does
`pkgs=$(go list ./...)` on a go.mod change and writes it straight to
`$GITHUB_OUTPUT` is correct for every run that changes one package, and fails
ALL legs at once the first time a change touches go.mod. That is the shape worth
a rule: a line that is right until the day its input is plural, and then fails
everything at once rather than one leg.
**The test.** `TestNoMultiLineValueIsWrittenToAStepOutput`
(`internal/ci/ci_outputs_test.go`), with
`TestOutputHeuristicFlagsTheMergeGateRegression`, which runs the heuristic over
the unguarded step and over the guarded one — a class rule whose tree is already
clean proves nothing by passing, so it is made to fail on purpose.
**Its allowlist.** None; the exceptions are shapes, not files, and they are in
the reader.
**Its remedy line.** The finding names the workflow, the line and the variable,
and the fix is the guard: `| tr '\n' ' '`, or the `key<<EOF` heredoc when the
value is genuinely multi-line.
**Its narrowings.** Three false negatives accepted to keep false positives at
zero: `go list` counts only with a `...` pattern (the gate's own
`p=$(go list "./${d#./}")` asks about one package); a repository script
(`x=$(bash .github/scripts/foo.sh)`) is not a producer, because its output shape
is the script's business; and the `key<<EOF` heredoc and a redirect attached to a
`{ … }` block are not followed into. The one place it over-reports is a variable
made plural, consumed, then reused — and the remedy there is the same guard.

### `wall clock` — no test asserts a bound under ten seconds

**The rule.** No `_test.go` line may carry a literal duration under ten seconds
where the test leans on the wall clock: a context deadline, a `time.After` or
`NewTimer` watchdog, or an elapsed assertion. Thirty seconds or more is the
generous bound; a fake that must stay short carries a `// wall-ok: <reason>`
comment.
**The mistake it prevents.** A test that fails under load and passes alone — a
version probe timing out at five seconds, a stall assertion on a five-second
deadline. A batch `deadline:`/`idle:` literal names no context and asserts no
elapsed time, yet it drives a REAL subprocess kill, so within a file that builds
a `BatchInput` any `time.Second` literal under ten is a bet on how loaded the
machine is.
**The test.** `TestNoTestAssertsAWallClockBoundUnderTenSeconds`
(`internal/ci/ci_budget_test.go`) — the law `waits` stands beside.
**Its allowlist.** A per-line `// wall-ok: <reason>` comment, and the check reads
the CODE before any comment on the line, so prose about the rule cannot trip it.
**Its remedy line.** `batch-driving test carries a wall-clock literal under ten
seconds (use thirty seconds or more, or an injected clock with // wall-ok:
<reason>)`.
**Its narrowings.** Duration INPUTS to fake-driven code — a runner deadline, a
flag table, a parsed `Retry-After` — are not assertions about the machine and are
out of scope. The batch-deadline half is scoped to files that name `runBatch` or
`BatchInput{`, because only there does a short literal reach a real process; a
sub-ten-second duration built from a constant or a variable is not read.

### `make` — the Makefile is the one entry for build, test and lint

**The rule.** A friend's `make test` and CI's test are the same command. Every build, test, vet, format or acceptance command in `ci.yml` is a
`make` invocation, and the Makefile declares `build`, `test`, `test-full`,
`lint`, `check`, `clean` and `help` as phony targets, with `check` the union of
the gates CI runs and `clean` removing only its two explicit directories.
**The mistake it prevents.** A command CI runs that a friend's `make` does not
is a test nobody can run locally. And a test of the Makefile that shells out to
`make -n check` and compares the dry-run text passes on GNU make 4.3 and fails on
4.4.1 and 3.81, because the dry-run is not a contract and `$(GO)`/`$(PKGS)`
expand to whatever the inherited environment says: a test whose verdict depends
on the host's make version says nothing about the repository.
**The test.** `TestCIBuildTestLintCommandsGoThroughMake` and
`TestMakefileIsTheOneEntry` (`internal/ci/makefile_test.go`), which PARSE the
Makefile's own rules — targets, prerequisites, recipes, variables, includes —
expanding the Makefile's own values and never running make, so the answer is the
same byte for byte on 3.81, on 4.4.1 and on the Windows legs where there is no
make at all.
**Its allowlist.** `commandAllowlist` in the test file: the setup and shard-loop
lines that run no test and check no formatting (`go list`, `go test -list`,
`go version`, `go env`, `command -v go`, `GOMAXPROCS`, and the fleet probe's own
diagnostic build).
**Its remedy line.** `ci.yml:<n>: build/test/lint command is not a make
invocation: <cmd>`, and `ci.yml names no make invocation; the Makefile is not the
one entry for the CL tier`.
**Its narrowings.** Both files are read as text — go.mod carries no YAML library
and these tests must answer identically on every platform in the matrix — so a
command assembled in a variable or hidden in a composite action is not seen, and
the allowlist is matched by prefix.

### `cache` — no cache step on a self-hosted runner

**The rule.** Every `actions/cache` step in `ci.yml` (including its `restore` and
`save` halves) carries
`if: runner.environment == 'github-hosted'`, and every `setup-go` step there says
`cache: false`; a persistent runner already has its cache on disk.
**The mistake it prevents.** A cache step written for a fresh hosted machine,
run on a persistent runner, tars the whole multi-GB Go build cache on every job,
outlives the job's timeout and is orphaned still compressing — dozens of `tar`
and `zstd` processes loading the machine until every test on it crawls.
**The test.** `TestNoCacheStepRunsOnASelfHostedRunner` (`internal/ci/ci_cache_test.go`).
**Its allowlist.** None.
**Its remedy line.** `an actions/cache step can run on a self-hosted runner; add
if: runner.environment == 'github-hosted'`, printed with the first lines of the
offending step.
**Its narrowings.** `ci.yml` only, split on step boundaries as text: a cache
action inside a composite action or another workflow is not read.

### `pinned-actions` — every action is pinned by SHA

**The rule.** Every `uses:` in `ci.yml` and `certification.yml` is
`owner/action@<40-hex-sha>`; a tag is a moving target with write access to the
runner.
**The mistake it prevents.** A supply-chain rule, and the cheapest class test in
the package. It belongs beside `SECURITY.md`'s standing position:
our own public repositories, defensive work, no unpinned third-party code in the
lane.
**The test.** `TestEveryActionIsPinnedBySHA` (`internal/ci/ci_budget_test.go`).
**Its allowlist.** None.
**Its remedy line.** `<file>:<line>: uses: is not owner/action@40-hex-sha: <line>`.
**Its narrowings.** The two workflow files only, matched by line: a `uses:` in a
composite action or in a workflow added later without being named here is not
read.

### `ci-ok` — every triggering event reaches a verdict

**The rule.** `ci-ok` is the only required check, and its verdict steps are gated
by event name, so every event the workflow triggers on — `pull_request`,
`merge_group`, `push`, `workflow_dispatch` — is named by a step. (`schedule` runs
the nightly tier and owes no verdict.)
**The mistake it prevents.** An unnamed event lets `ci-ok` run zero steps and
report SUCCESS over red needs — a green that means nothing, the same mistake as
green-once-treated-as-green-now one layer down.
**The test.** `TestEveryTriggeringEventReachesACIOKVerdict`
(`internal/ci/ci_budget_test.go`).
**Its allowlist.** None.
**Its remedy line.** `ci-ok has no verdict step guarded for <event>: the workflow
triggers on it, so a run on that event would report success with no step run`.
**Its narrowings.** The event list is written in the test, so an event added to
`on:` and nowhere else is not noticed until somebody adds it here; the check is
that the guard EXISTS, not that the step behind it asserts the right needs.

### `tiers` — unit tests on every change, functional tests as streams merge

**The rule.** Only the tests a change needs run, according to the changes being
made. The unit tier
(ci.yml's `test` matrix) takes at most two cores a leg — the share step's
min(share, 2) and the Makefile's `GOTEST_P ?= 2` on `go test -p` and
`-parallel` — and its PATH holds a `redis-server` that prints `unit tier:
redis-server is functional-only (build tag functional)` and exits 86, so
`testutil.Start` fails closed under `NOVA_CI=1`. The functional tier (the
`functional` job, `make test-functional`) runs only the `//go:build functional`
tests of the selected packages, on `merge_group`, `schedule` and
`workflow_dispatch`, never on `pull_request`, four space shards under the
two-minute cap; `ci-ok` requires it when it ran. The unit budgets are 2 s a
package and 1 s a test, with an allowlist whose every row names its
measurement, printed on every leg and enforced only on the nightly space legs;
what is enforced on every leg is static (`unitwaits`).
**The mistake it prevents.** CI is the bottleneck of the working process and the
real blocker for merging: when every PR's shards each take the whole of a box,
every test file starts its own redis-server, and tests run over a second, the
queue waits on CI.
**The test.** `TestUnitTierRefusesRedisServer` (writes the shim the way `ci unit-tier-shim`
does and runs it: exit 86 and its line), `TestStartFailsClosedOnTheUnitTierShim` (functional-tagged, in
`internal/ci/redis_ci_test.go`: `testutil.Start` against that shim fails
closed), `TestUnitLegTakesAtMostTwoCores` (the share
function, `pkgselect.RunnerShare`, with one runner on any box), `TestFunctionalTierRunsOnlyAsStreamsMerge` and
`TestSlowAllowlistRowsNameTheirMeasurement`,
`TestSlowAllowlistRatchetRefusesAnUnmeasuredRow`,
`TestUnitBudgetsJudgeTheTestNotTheLoad` (a 1.4 s test is exit 0 with its
CI-SLOW and CI-LOAD lines at load 2, at load 20 and unread, and exit 2 at all
three under enforce; an unledgered SLEEPS skip is red at all three on both
legs), `TestNightlySpaceLegIsTheOnlyEnforcingLeg` (ci.yml's test job runs on
schedule, the fan-out deals it onto the Linux legs only and
`pkgselect.UnitMakeArgs` passes SLOWTESTS_ENFORCE=1 only for that leg; nothing
spells SLOWTESTS_ENFORCE=0; the Makefile reads it only to
pass --enforce and carries slowtests' exit through) and
`TestMeasuredBenchesAreCIRunners` (every bench a row may name is one ci.yml
names) (`internal/ci/unit_tier_class_test.go`); through make itself,
`TestMakeTestExitIsCISleepsOnEveryLegAndCISlowOnlyNightly`
(functional-tagged, `cmd/nova-ci/make_functional_test.go`).
**Its allowlist.** `internal/ci/slow-tests_allowlist.txt`, every row naming its
measurement (`<seconds>s@run<id>` or `@<bench>`), and
`internal/ci/sleeps-skips_allowlist.txt`, the SLEEPS ledger (`unitwaits`).
**Its remedy line.** Tag a test that needs a real server, binary or process
`//go:build functional`; make a slow unit test fast, or delete its allowlist row
once it is.
**Its narrowings.** The class tests read ci.yml and the Makefile; they do not
prove GitHub prepends GITHUB_PATH in step order, which the leg's own `test` step
checks (`command -v redis-server` must be the shim). A test that starts a
server by an absolute path is not caught by the shim.

### `hosted-shards` — the hosted legs meet the cap by shards, heavy packages apart

**The rule.** ci.yml's `test-hosted` keeps `timeout-minutes: 2` and meets it by
shard count: ubuntu-latest runs shards 1..6 and macos-latest 1..8, every leg
carrying its OS's `shards`. The `deal this shard's packages` step places the
heavy package (`cmd/nova-bus`) first, then every other package round-robin in
`go list` order; vet and test both read the deal's `HOSTED_PKGS`. The Go cache
is restored at the path Go uses on each OS (`~/Library/Caches/go-build` on
macOS, `~/.cache/go-build` on Linux, plus `~/go/pkg/mod`) by
`actions/cache/restore` before `build`, and written by `actions/cache/save`
after `build` and before the test step, only when the key was not an exact hit.
**Why the per-OS path.** GOCACHE is `os.UserCacheDir()/go-build`, which is
`~/Library/Caches/go-build` on macos-latest (the job's own `go env`), so the one
Linux path cached no macOS build at all.
**Why restore and save are split.** The combined `actions/cache` saves in a post
step with `post-if: success()`, so a shard the cap cancels never saves and a leg
that only fits warm would never be warmed.
**Why the save comes before the tests.** The tests are the step the cap cancels;
a shard whose save runs before its tests warms the shards that start after it,
even when its own test step is cancelled.
**The mistake it prevents.** At too few shards per OS, a shard holding two heavy
packages together is cancelled by the cap and turns `ci-ok` red, and a
count-only deal keeps them together however many shards there are. A cache step
that caches the Linux path on every OS logs "cache hit" on macOS while caching
no macOS build at all, and the macOS shards spend the cap building.
**The test.** `TestHostedShardsUnderTheCap` (every ci.yml job at two minutes;
both hosted OSes; shards 1..n with n at least 6 and 8), `TestHostedDealPartitionsTheTree`
(the deal step over a stand-in list lands every package in exactly one shard)
and `TestHostedDealSplitsTheHeavyPackages` (the deal step over the real
`go list ./...`: no two heavy packages share a shard)
(`internal/ci/hosted_shards_class_test.go`), and `TestHostedCacheIsWhereGoKeepsIt`
(restore and save carry the per-OS path, no combined `actions/cache`, restore
before `build` before save before the tests; `internal/ci/hosted_cache_class_test.go`).
**Its allowlist.** `hostedHeavy` in that file, which the step spells verbatim.
**Its remedy line.** Add a shard to the OS's matrix and `include`, or name a
package in `hostedHeavy` and in the step's `heavy=`; never raise the timeout.
**Its narrowings.** The shard counts come from cancelled legs, which are lower
bounds; the class tests do not time a hosted leg, dev's push run does.
The heavy list is named from one reader measurement, not from a hosted
per-package timing.

### `darwin-gate` — the darwin unit shards run only where the target branch is dev or main

**The rule.** ci.yml's `test-packages` deals the unit matrix with `ci test-matrix`,
and the darwin shards are dealt only when `pkgselect.DarwinOn(event, target)` says
so: always on `schedule` and `workflow_dispatch`; otherwise only when `--target-branch`
(a pull_request's `github.base_ref`, the merge queue's `github.event.merge_group.base_ref`,
a push's `github.ref_name`; a `refs/heads/` prefix is cut) is one of
`pkgselect.DarwinBranches` (`main`, `dev`, the integration list of the concurrency
group in short form). With the gate off every selected package rides the Linux
shards, the darwin-only packages (`cmd/nova-sandbox`, `internal/sandbox`) are dropped
before the nothing-to-test check and have no leg, the darwin-sensitivity analysis
does not run, and a change that selected only darwin-only packages gets the one leg
that prints "nothing to test for this change". Linux shards are unchanged, `ci-ok`
reads the `test` job whose matrix has no darwin entry on a working branch, and
certification.yml (which runs on pushes to dev, nightly and on dispatch, and certifies
on darwin) is untouched. A change that touches only darwin-only packages gets no test
run until it reaches dev. No ruleset applies to a working branch, so no darwin check
is required there.
**The mistake it prevents.** The Go is the same Go on both OSes and the darwin legs
are the slowest and the scarcest: cancelled at the two-minute cap whenever their
host is loaded, they turned a working-branch pull request red for a reason the
change did not cause and held the queue behind them.
**The test.** `TestDarwinOn` and `TestFanoutWithTheDarwinLegsOffIsLinuxOnly`
(`internal/pkgselect`) hold the gate over the events and the targets, prefixed and
bare; the `test-matrix` tests in `tools/ci/sel_test.go` hold the verb with the gate off.
`TestDarwinShardsRunOnlyForIntegrationBranches` (`internal/ci/darwin_gate_class_test.go`)
reads ci.yml: `pkgselect.DarwinBranches` equals the integration list, the list step
hands the verb each event's own target branch, and no other job carries a macOS label
on a `runs-on` list (any position, quoted or block form, any case) or runs macos-latest
on a pull_request or merge_group. `TestMacOSRunnerMatchCatchesEveryPosition` holds that
label match itself.
**Its allowlist.** None.
**Its remedy line.** Add the branch to the integration list and to
`pkgselect.DarwinBranches` together.
**Its narrowings.** It reads the workflow as text and does not run the step; the
branch expressions are GitHub's.

### `cert-race-shards` — the whole-tree race run meets the cap by shards, its cache saved before the tests

**The rule.** certification.yml's `test` job keeps `timeout-minutes: 2` and meets
it by shard count: ubuntu-latest and macos-latest each run shards 1..8, every leg
carrying its OS's `shards`. Its `deal this shard's packages` step is test-hosted's
deal over the live packages (`go run ./tools/ci deal`), with the measured heavy list
(`internal/ci`, `cmd/nova-tokens`, `cmd/nova-sandbox`,
`cmd/nova-self-talk`, `internal/update`, `cmd/nova-secrets`, `internal/bus`) dealt
first, one per shard. Every shard
restores the `<os>-gorace-` cache (the race build cache, the module cache and the
Go toolchain's tool-cache directory, so setup-go finds the toolchain rather than
installing it), builds every external package the live tree's tests import under
`-race`, SAVES the cache, and only then runs `go test -race -count=1` over its
packages. The legs need `race-cache`, one leg per OS, which looks the exact entry
up without downloading it and, only on a miss, builds and saves it, so the test
legs of a cold run start warm.
**The mistake it prevents.** Unsharded, the job builds, vets and race-tests the
whole tree on one runner: it reaches the test step well into the two-minute cap,
the race tests alone take minutes, so the cap cancels it on every push, and a
post-step cache save never runs, so every run starts cold.
**The test.** `TestCertificationRaceShardsPartitionTheLiveTree`
(`internal/ci/cert_race_shards_class_test.go`): both OSes at shards 1..n with n at
least 8; the deal step, run over the real `go list ./...`, lands every live
package in exactly one shard and no deprecated one in any; no two heavy packages
share a shard; restore, race dependency build, save and test in that order; the
test step carries `-race`, `-count=1` and `$HOSTED_PKGS`.
**Its allowlist.** `certRaceHeavy` in that file, which the step spells verbatim.
**Its remedy line.** Add a shard to the matrix and both `include` entries, or name
a package in `certRaceHeavy` and in the step's `heavy=`; never raise the timeout.
**Its narrowings.** The heavy list and the shard count come from the per-package
times one hosted certification run reports (`ok <pkg> <seconds>`, the larger of the
two OSes); the class test does not time a hosted leg, a certification run does. The build and vet are
ci.yml's (lint on every event, test-hosted on push); a package's race compile in
its shard is the macOS and Linux compile of the race build.

### `release-legs` — the release and its dry run build one leg per platform and sum the whole set on one machine

**The rule.** release.yml's `build` and certification.yml's `release-build` are a
matrix of one leg per line of `tools/ghrelease/release-targets`, the same list in
all three places. Each leg's only compile is `go run ./tools/ghrelease build
<stamp> <goos> <goarch> dist` (every `cmd/*/` tool, `-trimpath`, `CGO_ENABLED=0`,
the ldflags `ghrelease ldflags` composes, named `<tool>_<stamp>_<goos>_<goarch>[.exe]`),
and each uploads the artifact `release-<goos>-<goarch>`. release.yml's `release`
and certification.yml's `release-dry-run` need those legs, download every
`release-*` artifact into one directory, run `ghrelease stamp` over the
linux/amd64 binaries, and then run `ghrelease sums`, which
refuses a directory that is not exactly the shipped set and writes and verifies
`SHA256SUMS` over the whole of it. In release.yml that runs in the step that
attaches the set to the release. release.yml restores the certification build
cache and never saves one.
**The mistake it prevents.** One runner cross-building every platform in one job
does not fit the two-minute cap: the cap cancels it inside that build, and
release.yml's build is the same loop. Two copies of
a target list drift, and a dry run that builds differently from the release proves
nothing about the release.
**The test.** `TestReleaseMatricesAreTheTargetsFile`,
`TestReleaseSumsAreOneMachineOverTheWholeSet`,
`TestEveryJobThatRunsGhreleaseSetsUpGoFirst` (a job that runs `go run ./tools/ghrelease`
carries the pinned `actions/setup-go` with `go-version-file: go.mod` before its first
call) and `TestNoWorkflowStepRunsAReleaseScript` (no release or certification step runs a
release shell script; the verbs are `tools/ghrelease`)
(`internal/ci/release_matrix_class_test.go`): both matrices equal the targets
file; no `go build` in a build leg, one `ghrelease build` call over the matrix
target, one `release-<goos>-<goarch>` upload; no cache save in release.yml; the
final job needs the legs and downloads every `release-*` artifact, asserts the
stamp, then runs `ghrelease sums`, in release.yml in the attaching step
(`ghrelease attach`).
**Its allowlist.** None. The targets file is the list, embedded in `tools/ghrelease`.
**Its remedy line.** A platform is added or dropped in `tools/ghrelease/release-targets`
and in both matrices in one change; a compile change goes into `tools/ghrelease/build.go`.
**Its narrowings.** The tests read the workflow text; whether a leg fits the cap is
measured by a certification run. The negative controls of the release verbs that
need no binary (stamp refusals, tag alphabet, certified gate, upload boundary) are
unit tests of `tools/ghrelease`; the ones that need this tree's real binaries (an
unstamped build, a tool that loses its stamp) are `ghrelease controls`, run in
certification.yml's `release-checks`, which builds them on the linux-amd64 leg's
cache; they are fixtures and are never summed or shipped.

### `onboarding` — every command meets the onboarding standard

**The rule.** `docs/ONBOARDING.md`, asserted for EVERY directory under `cmd/` by
walking it: `<tool> help` prints usage ending in an `example:` block, a bare
command refuses in ONE line naming that door, and `docs/TESTS.md` carries a
`### First run` transcript inside that tool's `## <tool>` section.
**The mistake it prevents.** The walk exists for the binary nobody has written
yet — a new command joins the standard on the day it appears, not the day
somebody remembers a table. The skip map `notYetOnTheStandard` is EMPTY and that
is the point: a skip that outlives the branch it names excuses a tool from the
standard for good. The same lesson from the other side: a tool whose `CLI.md`
section is prose-shaped contributes ZERO rows to `nova-check dogfood`.
**The test.** `TestEveryCommandMeetsTheOnboardingStandard`
(`internal/ci/onboarding_test.go`).
**Its allowlist.** `notYetOnTheStandard` in the test file: empty, and an entry
must name a genuinely open branch, so a skip is a dated pointer rather than a
permanent exemption and it stops firing the moment that branch's section lands.
**Its remedy line.** The finding names the tool and the missing half — the
`example:` block, the one-line refusal, or the `### First run` section in
`docs/TESTS.md`.
**Its narrowings.** Only the two repo-wide points are checked here; the rest are
per-binary and live in each command's own `firstrun_test.go`, where the
transcript is executed line for line through one comparator (`transcripts`).

### `one section` — docs/TESTS.md names each tool exactly once

**The rule.** No two `## ` headings in `docs/TESTS.md` carry the same name. A
tool with more than one thing to say says it in `###` subsections of its one
section.
**The mistake it prevents.** A tool written twice in `docs/TESTS.md`:
`onboarding.Section` cuts to the FIRST match and cannot fail, so the tool's
`firstrun_test.go` executes the first section and the second is read by no test
at all. The second copy drifts into sentences the binary does not print, and a
dogfood run reproduces them as DEFECT while every test in the repository is
green: the drift is not a test that is too weak, it is a document half of which
no test can see.
**The test.** `TestNoToolIsWrittenTwiceInTheTranscripts`
(`internal/ci/onboarding_test.go`), over the parse in
`onboarding.RepeatedSections`.
**Its allowlist.** None. A repeated heading has no good case: the second copy's
readership is nobody.
**Its remedy line.** The finding names every repeated heading and says only the
first is read — by `onboarding.Section`, by every `firstrun_test.go`, and by a
person looking for the one place to change.
**Its narrowings.** Only `docs/TESTS.md` and only `## ` headings; a repeated
`###` inside one tool's section is that section's business, and a tool's own
test is what holds a second subsection to what the tool prints.

### `transcripts` — every documented transcript is EXECUTED, line for line

**The rule.** For every `## <tool>` section of `docs/TESTS.md`, a test in
`cmd/<tool>` runs every `$` line of the section's `### First run` block, in
order, in one sitting, and compares each command's whole output with the block
written under it through `onboarding.CompareTranscript` — same number of lines,
same lines, same order, every value compared as written. The only values matched
by shape are the run-owned ones named from the one shared table,
`onboarding.Volatile` (`at`, `took`, `created`, `tmpdir`, `sha`); a name the
table does not hold is refused, so no call site can turn a red green by widening
one pattern. A `firstrun_test.go` may compare no other way.
**The mistake it prevents.** A check that a tool's section EXISTS asserts
nothing about whether anything runs it: a section executed by no test is a
promise no build checks, and a test that collects what was printed into a
`printed map[string]bool` and asks whether each documented line is somewhere in
it passes an abridged or a reordered block.
**The test.** `TestEveryTranscriptIsExecutedLineForLine`
(`internal/ci/transcripts_class_test.go`). It walks `cmd/` for the directories
`docs/TESTS.md` has a section for, and reads each package's test sources for the
one comparator called with that tool's own name, and for the comparisons it
replaces: `onboarding.Execute`, `onboarding.Compare`, `onboarding.Shape` and a
`printed` set.
**Its allowlist.** `internal/ci/testdata/transcripts_allowlist.txt`, one `<tool>`
per line with the issue that owes it, checked in both directions -- a stale entry and an ORPHAN naming no section are both red -- so it only
shrinks: an unlisted unexecuted section is red, and a listed section a test now
executes with the one comparator is a stale entry and red too.
**Its remedy line.** ``docs/TESTS.md has a `## <tool>` section and no test in
cmd/<tool> compares it with onboarding.CompareTranscript(; the section is a
promise no build checks. Convert it — run every `$` line of the `### First run`
block in order and hand the steps and the results to the one comparator — or
list <tool> in testdata/transcripts_allowlist.txt with the issue that owes it``.
**Its narrowings.** Only `docs/TESTS.md`, only `## <tool>` sections that have a
directory under `cmd/`, and only that package's own top-level test files. **It
is a spelling proxy and this is its boundary.** A section counts as executed
when one test file in the package carries three spellings together: the
comparator's call, the tool's own name as a literal, and `TESTS.md`. The third
is load-bearing — without it a package comparing a hand-written fixture that
held its own name would count as executing its section. What the proxy still
cannot see is a file that opens the document and compares something it built
from it. A fourth way of comparing, written from scratch, is likewise not seen
until it is named here. Whether a transcript is TRUE is not this test's
business — a document that disagrees with its tool is a finding, never an edit
that makes a test pass.

### `version` — every tool prints the one version line

**The rule.** Every `cmd/nova-*` binary answers `version` with exactly one line
in the grammar `<tool> <identity> <goos>/<goarch> <go version>` followed by any
number of `key=value` extras — the grammar `internal/buildinfo` both writes and
reads, and `docs/SPEC.md` states once.
**The mistake it prevents.** A version line of a different shape (`SANDBOX
VERSION tool=... version=...`) is refused by every reader, and
`nova-version snapshot --bin ~/.local/bin --out ./tools.tsv` refuses the whole install over one
such tool, exit 2. One grammar with `key=value` extras means a tool can say one
more true thing about itself — `snapshot` reads the four tokens through
`internal/buildinfo.Parse` and accepts the extras — while two shapes would mean
every reader carries its own tolerant parser.
**The test.** `TestEveryToolPrintsTheOneVersionLine` and
`TestTheVersionGrammarIsSpelledOutOnceInTheSpec`
(`internal/ci/version_class_test.go`). The first builds every `cmd/nova-*` in
one `go build ./cmd/...` and runs the REAL binary through the REAL reader; the
second holds `docs/SPEC.md` to the same sentence.
**Its allowlist.** None. The test walks `cmd/` rather than holding a list, so a
tool added tomorrow is held to the grammar on the day it appears.
**Its remedy line.** `` `<tool> version` printed a line internal/buildinfo.Parse
refuses; the grammar is `<tool> <identity> <goos>/<goarch> <go version>` and
then any number of key=value extras``.
**Its narrowings.** Only the `version` verb is read; a `--version` flag or a
version inside a banner is not. Extras are not checked beyond their `key=value`
shape.
### `namedpaths` — every path this repository names, it has

**The rule.** A token in this repository's non-test Go source — in a string
literal OR in a comment — or in `docs/*.md`, that begins with one of this
repository's own top-level directories (`cmd`, `internal`, `docs`, `tools`,
`scripts`, `testdata`, `fleet`, `infra`, `.github`) and carries a slash must
name a file or a directory that is in the tree, or be named in the allowlist
with its reason.
**The mistake it prevents.** A comment that places the bench standard's witness under
`scripts/` when it is `tools/benchstandard` sends a friend following it to
nothing, because a path inside a comment or a string is just text to Go and
nothing else in CI has an opinion about it. Prompts give positive instructions
with EXACT paths; the text a friend reads IS the mechanism, so a dead path is
not a typo, it is a friend's search.
**The test.** `TestEveryNamedRepoPathExists`
(`internal/ci/namedpaths_class_test.go`), with
`TestTheNamedPathHeuristicReadsWhatItClaims`, which holds the reader to
sixteen hand-written lines so a rule that quietly stopped matching anything
cannot pass as a green run, and `TestTheNamedPathExistenceCheckReadsTheTree`,
which holds the other half against this package's own directory.
**Its allowlist.** `internal/ci/testdata/namedpaths_allowlist.txt`, one
`<name> <reason>` per line, in three groups: the files a specification has
PLANNED and nobody has written yet, the invented names a document uses to show the SHAPE of a path
(`.github/scripts/foo.sh`, `docs/history`), and the paths that live in another
tree — another repository, another branch, or a retired file. Checked in BOTH
directions, and the second direction has two spellings: a listed name nothing
writes any more is a stale row, and a listed name that is IN the tree now is the
good red — the planned file was written, so the row goes on the same day.
**Its remedy lines.** `<file>:<line>: <name> names no file or directory in this
tree; a friend following it finds nothing -- correct the path, or add it to
internal/ci/testdata/namedpaths_allowlist.txt with the reason it is not a real
path`, and for the two stale spellings `delete the stale entry (the list only
shrinks)` and `it is in the tree now; delete the entry`.
**Its narrowings.** Test files and `testdata/` fixtures are not read — a
fixture's whole job is to be an invented tree. A token that does not BEGIN a
word is out, which is what keeps import paths, URLs and paths on a bench out. A
glob, a template or a printf verb on either side takes the token out, so
`docs/SPEC-*.md` and `internal/%s/doc.go` are patterns, not names. A
package-qualified Go symbol (`internal/merge.Enqueuer.Enqueue`) and a bare
exported name under a package directory (`internal/lockfile/TestLockRule1`) are
read as symbols, not files, by Go's own upper-case signal. And a `testdata/…`
name is looked for under EVERY package, because a fixture path is always written
relative to the package that owns it.

### `prmerge` — nothing reaches the dev merge queue but a batch

**The rule.** Nothing reaches the dev merge queue but a batch. `gh pr merge` in any spelling, and any `--auto` flag to it, is refused in
every non-test Go file under `cmd/` and `internal/` and in every file under
`.github/`. Admission to a merge queue is `internal/merge.Enqueuer.Enqueue`, the
`enqueuePullRequest` mutation, and nothing else.
**The mistake it prevents.** A pull request carrying GitHub's auto-merge,
switched on by a `gh pr merge` call made while it was red, is enqueued by the
forge itself as its checks go green, and lands on dev with nobody having
enqueued it; code that calls `gh pr merge` spreads the same standing instruction
across every open pull request it touches.
**The test.** `TestNoGhPrMergeSpellingInTheToolsGo` and
`TestNoGhPrMergeSpellingUnderDotGithub` (`internal/ci/prmerge_class_test.go`).
The Go walk reads string literals in source order per function, so a command
built in a slice is seen as well as one passed inline, and a refusal message
that mentions the spelling is one literal, not an argument list.
**Its allowlist.** `internal/ci/testdata/prmerge_allowlist.txt`, `<path>:<func>`
per line, checked in both directions so it only shrinks: the mutation guard
that names `--auto` to refuse it, the audit's `--disable-auto`, which takes an
auto-merge OFF, and the secrets store's own pull request.
**Its remedy line.** `<path>:<line>: gh pr merge (or --auto) is refused; enqueue
through internal/merge.Enqueuer.Enqueue, or take the auto-merge off with the
audit's --disable-auto`.
**Its narrowings.** Test files are not read; a comment may still say auto-merge
— the rule is about what runs. A spelling assembled at run time from separate
words is not seen.

### `nightly-tags` — every tagged suite is run by a scheduled job and vetted by a CI vet step

**The rule.** Every opt-in build tag a `_test.go` carries is named by a
SCHEDULED workflow, either literally on a `go test`/`go vet` line
(`go test -tags perf` over the tree) or as a `tag:` entry of a job's matrix the
step then expands (`go test -tags ${{ matrix.tag }}` over the tree). Platform and toolchain
constraints are not opt-ins and are out of scope: `//go:build darwin` says where
a test runs, not whether it runs, and a negation (`!windows`) is on by default
everywhere else. And every opt-in tag is also vetted by a CI vet step — `make
vet-functional`, `make vet-slow`, `make vet-shippedsmoke`, `make vet-novadisk`
in ci.yml's lint job, `go vet -tags perf` in certification.yml — so a file
behind a tag is type-checked on every change and a tag-only build break is red
at the PR rather than the night after.
**The mistake it prevents.** A build tag is how this tree takes a test off the
per-change path, and a plain `go test` over the tree compiles the file away
silently. So a tag no scheduled job passes to `go test -tags` is not a slower
tier, it is a deleted test that still looks like a test in the tree. The one
real end-to-end run of the sandbox, a real APFS volume made, used and destroyed
(`//go:build darwin && novadisk`), is held this way: `nightly-slow.yml` runs tag
`novadisk` on `macos-latest` on its schedule. Worse than uncovered would be the
net checker's exemption: `internal/ci` EXEMPTS a file carrying `//go:build
nightly` or `//go:build soak` from the no-real-network rule (`ci_net.go`,
`ci_net_test.go` cases 3 and 4), so with no workflow running either tag a
real-network test could be written, waved through by the checker, and never
execute once.
**The test.** `TestEveryTestBuildTagIsRunBySomeScheduledJob`,
`TestTheNetworkExemptTagsHaveAHomeInTheSchedule`,
`TestSomeScheduledJobRunsTheRaceDetector` and
`TestEveryTestBuildTagIsVettedByCIVetSteps`
(`internal/ci/nightlytags_class_test.go`). The first is the class and names no
tag: it walks every `_test.go` for the tags that HIDE a file, reads every tag
the scheduled workflows name, and refuses the difference with the files that
would have gone unrun, so a tag invented tomorrow is covered the day its first
test file lands. The second holds the net checker's two exempt tags to a leg
whether or not a file carries one. The third holds `race` — implicit,
because it comes from the `-race` flag rather than from `-tags` — to a scheduled
job that actually passes `-race`. The fourth is the vetting half: it walks the
Makefile's `vet*` targets and every workflow `go vet` line for the tags each
passes, and refuses a tag no vet step passes, so a tag that hides a test file is
type-checked on a pull request.
**Its allowlist.** None. The walk reads the tree rather than a list, so a tag
added tomorrow is held on the day its first test file lands.
**Its remedy line.** ``build tag "<tag>" hides <n> test file(s) and NO scheduled
job runs it: <files> — remedy: add a `tag: <tag>` leg to nightly-slow.yml's
matrix (or `go test -tags <tag>` to another scheduled workflow), or drop the tag
from those files``. And for the vetting half: ``build tag "<tag>" hides <n>
test file(s) and no CI vet step passes `-tags <tag>`: <files> — remedy: add a
`vet-<tag>` target to the Makefile and a `make vet-<tag>` step to ci.yml's lint
job, or drop the tag from those files``.
**Its narrowings.** Only SCHEDULED workflows count for the run half, and only
what actually reaches `go test`: whole-line YAML comments are dropped first, so
prose ABOUT a tag never stands in for a job that runs it. A tag assembled at run
time, or passed through a variable the step does not expand inline, is not seen.
For the vetting half, only a `go vet` line's own `-tags` counts: a `go test
-tags` run compiles but does not vet, and a matrix `${{ matrix.tag }}` names no
vet step.
### `functional` — no untagged test file starts a redis-server

**The rule.** Every `_test.go` that calls a helper which execs redis-server
(`testutil.Start`, `testutil.Program`, `wstest.Start`) carries `//go:build
functional`, joined with `&&` to any constraint it already has. A file that
mixes redis-backed and pure tests is split: the pure tests stay in the
untagged file, the redis-backed ones live in `<name>_functional_test.go`.
**The mistake it prevents.** Unit tests are under 2 s (ideally 1) and never so
aggressive that they fill a whole machine's cores; functional tests run as whole
work streams merge, never on every small PR. A redis-server started per test on
every pull request's shards breaks both.
**The test.** `TestRedisBackedTestsCarryTheFunctionalTag`
(`internal/ci/functionaltag_class_test.go`) walks every `_test.go`, finds the
direct calls through each file's own import of the two helper packages, and
fails on each file whose build constraint is still true without `functional`.
`TestNeedsFunctionalReadsTheConstraint` and
`TestStartsRedisSeesTheHelpersThroughTheirImport` pin the two readers it uses.
**Its allowlist.** None.
**Its remedy line.** ``<file> starts a redis-server but builds without `-tags
functional`: put `//go:build functional` (joined with && to any constraint it
has) on its first line, or move its redis-backed tests to
<name>_functional_test.go and keep the pure ones here``.
**Its narrowings.** Only the direct call is read. A file that reaches redis
through a package-local helper is held by the compiler instead: the helper's
file is tagged, so an untagged caller does not build, and the lint job's
`go vet ./...` is red on it. The functional tier runs where a whole stream
lands (`make check`, ci.yml's `functional` job on merge_group and schedule) and nightly
(nightly-slow.yml's `functional` leg); the lint job's `make vet-functional`
compiles it on every change.

### `functional-image` — the functional-tier image is pinned and carries every program the tier runs

**The rule.** `infra/functional-image/Containerfile` names its base image by
digest, installs the Go version `go.mod` pins, checks every download against a
sha256, takes its packages from a dated archive snapshot with
`--no-install-recommends`, and runs as a non-root user with `GOTOOLCHAIN=local`,
`GOPROXY=off`, `NOVA_CI=1` and `NOVA_FUNCTIONAL_RUN=container`. Every program a
Go file under `cmd`, `internal` or `tools` runs by name (a string literal, or a
package-level constant holding one, given to `exec.Command`,
`exec.CommandContext` or `exec.LookPath`) is a row of
`infra/functional-image/binaries.txt`, and the image carries it or the row says
why the tier does without it.
**The mistake it prevents.** A functional run inside a container that lacks a
program the tests exec either fails on it or, worse, skips the test and stays
green; an unpinned base, archive or toolchain makes two runs of one commit
different runs.
**The test.** `TestFunctionalImageBaseIsPinnedByDigest`,
`TestFunctionalImageGoIsTheModulesPin`, `TestFunctionalImageInputsArePinned`,
`TestFunctionalImageRunsAsTheTierExpects`,
`TestFunctionalImageCarriesEveryBinaryTheTierExecs`,
`TestFunctionalImageDownloadCheckSeesEveryWayAroundIt`,
`TestFunctionalImageSourceCheckSeesADeletedInstall` and
`TestFunctionalImageRuntimeAndReadmeAgree`
(`internal/ci/functional_image_class_test.go`), and
`TestFunctionalImageReadmeKeepsTheBuildCachePerTrustDomain`,
`TestFunctionalImageBuildLeavesNothingBehind`,
`TestFunctionalImageRootUserSpellings`,
`TestFunctionalImageReadmeNamesEveryWritablePlace`,
`TestFunctionalImageReadmeRunCommandCarriesEveryFlag`,
`TestContainerRuntimeRefusesRootBeforeItsFirstChange`,
`TestContainerRuntimeSubidsNeverReuseARange`,
`TestContainerRuntimeTasksCannotHideAWeakening` (short-form module names, `ignore_errors`, `block`, and the pinned task list and `when`s of `subid.yml`),
`TestContainerRuntimeSubidExpressionsArePinned` (the allocation's Jinja, pinned
as the text that was evaluated with ansible's template engine on constructed
subuid files, since the unit tier has no ansible),
`TestContainerRuntimeDropInHasItsDirectory` and
`TestContainerRuntimeProbeValuesAreNumbers`
(`internal/ci/functional_image_runtime_class_test.go`). The
`RuntimeAndReadmeAgree` test holds the runtime role's probe
(`fleet/roles/container-runtime`) to the image's base and to the flags of the
README's run command. The download check requires every `curl` to use `-f` and
`-o` and never pipe, forbids `wget` and an `ADD` of a URL, and requires every
`sha256sum` to be `sha256sum -c -` fed a pinned ARG value; a `source` row is
read from instructions, comments excluded. The role tests hold that the role
refuses root before its first change, allocates a subordinate id range after
every existing one, makes the delegation drop-in's directory (for the runner's
manager only), computes the probe's cpu.max as a number and probes
`no-new-privileges` and the dropped capabilities; the README's build cache is
per trust domain, and the run command (not the table below it) carries every
flag. The download check also refuses a checksum step that continues after a
failure (`|| true`, `;`), a fetch by `git clone`, `go install`, `pip` and the
like, a `COPY --from` an outside image, an apt source with no `Signed-By` or
with `Trusted: yes`, TLS peer checks off outside the bootstrap, and `curl`
reached through a variable; `USER` is refused in any spelling of uid 0.
**Its allowlist.** `infra/functional-image/binaries.txt` itself: one row per
program, checked in both directions, so a program no file runs any more is a
row to delete.
**Its remedy line.** ``<name> is run by name and is not a row of
infra/functional-image/binaries.txt: add a row and install it in the
Containerfile, or say why the tier can do without it``.
**Its narrowings.** It reads names, not scripts: a program run inside a `sh -c`
string or through a variable is not seen, and the `[unscanned]` rows of the list
carry the ones known to be needed. The rest is caught by a run of the tier in
the image, where `NOVA_CI=1` makes a missing program a failure. It does not
build the image.

### `redis-version` — every place that names a Redis version names the same one

**The rule.** `ARG REDIS_VERSION` in `infra/functional-image/Containerfile` is
the repository's one Redis version, the one the functional image builds, the
image `make test-functional-container` runs the functional tier in. Every other
place that names a Redis version equals it: the CI installer's source build
(`tools/ci/installredis.go`), the image's README,
`docs/nova-table/README.md`, and every phrase of the living tree that writes a
three-part version right after the word Redis (`Redis <v>`, `Redis (<v>)`,
`redis-server <v>`, `--redis-version <v>`, `redis-<v>.tar.gz`,
`REDIS_VERSION=<v>`, `REDIS_VERSION="<v>"`, the same assignment written with
spaces (`REDIS_VERSION = "<v>"`, `const RedisVersion = "<v>"`,
`redisVersion := "<v>"`, `REDIS_VERSION ?= <v>`), `redis_version:<v>`,
`redis:<v>`, the apt pin `redis-server=6:<v>-1`, the output of
`redis-server --version` (`v=<v>`), and a version or a name in backticks).
**The mistake it prevents.** Two Redis versions in one tree: the functional
tier is green on one while a document, an installer or a captured error text
names the other, and a behaviour that differs between them is found by a user
and never by a test.
**The test.** `TestRedisIsOneVersionEverywhere` and
`TestRedisVersionRuleSeesEachShape`
(`internal/ci/redis_version_class_test.go`). The first reads the reference,
requires each named place to name a version at least once, sweeps the files of
the living tree of these kinds (Go, Markdown, YAML, TOML, INI, JSON, shell,
PowerShell, Python, Lua, Jinja, card, template, TLA+, env and text files, and
the files named `Containerfile`, `Dockerfile` or `Makefile`), and reports every
version that differs with every place that names it. The second is the control:
each shape is found, each kind of file is read and release history, captured
data and other people's code (`vendor`, `node_modules`, a Python virtualenv) are
not, the versions of other programs (`go-redis v9.22.0`, `nova-redis 1.0.0`),
minimums (`Redis 7 or later`) and addresses (`redis=127.0.0.1:6379`) are not,
and a split of three versions is reported once per differing version with all
its places.
**Its allowlist.** None. `redisVersionHistory` lists the paths the sweep does
not read, each with its reason (release history, and the fixtures of a package
deprecated in place that record servers of other versions), and the test is red
when a row names a path the tree no longer holds.
**Its remedy line.** ``Redis <v> is named at <file>:<line>, but the one version
is <ref> (ARG REDIS_VERSION in infra/functional-image/Containerfile): make every
place name <ref>, and take REDIS_SHA256 from the project's published hash for
that release; if the place states a minimum and not the version run, write the
minimum with one or two parts (`Redis 7 or later`), which this rule does not
read``.
**Its narrowings.** One- and two-part mentions (`Redis 7`, `Redis 6.2`) name a
feature generation and are not read: a minimum is written with one or two parts
(`Redis 7 or later`), the version the repository runs in full. A file of a kind
not listed (`.cfg`, `.sql`, `.tsv`) is not read. CI's functional job does not
run in the image: it runs `make test-functional` on the runner's own
`redis-server`, and the installer keeps a `redis-server` already on the runner's
PATH, so a runner can be on another version than the one the image builds. The
rule cannot read what `apt` or Homebrew installs on a runner or the
`redis-server` a runner already holds, and cannot check a sha256 against a
version offline: the image build's `sha256sum -c` checks it against the tarball.

### `cardtemplates` — no card template carries a command only one platform has

**The rule.** A card template is the text a worker is handed verbatim; nothing
rewrites it between `cut` and the shell. The estate is mixed — some machines
run linux and others darwin — so a shipped
template may spell only commands BOTH answer. The portable spellings are
`command -v <name>` for presence, `go version`, `dotnet --version` and
`java -version 2>&1` for the three toolchains that each spell it differently,
and a `uname`-chosen pair (`sysctl -n hw.ncpu` on darwin, `nproc` elsewhere) for
a fact only one platform reports. A card reports its work, not its machine:
timing comes from the harness's own line, never from GNU `time(1)`.
**The mistake it prevents.** A template that spells `/usr/bin/time -f`, `nproc`,
`go --version` or `java --version` is fine on a linux bench and dies on a Mac
inside the worker, minutes in, with a shell error that names nothing about
portability — not at cut time, where it would have cost nothing.
**The test.** `TestNoCardTemplateCarriesAnOSSpecificCommand`
(`internal/ci/ci_cardtemplates_test.go`), over the checker in
`internal/ci/ci_cardtemplates.go`, which reads every `*.md` and `*.card` under
`CardTemplateDirs` as text, and over the card and pulse templates selected from
`swarm.TemplateNames()` (a name that is `models.tsv`, or that is neither
`swarm.IsCardTemplate` nor `swarm.IsPulseTemplate`, is skipped), each read through
`swarm.Template`. Neither directory in `CardTemplateDirs` exists in the tree, so
the templates the rule reads are those selected ones; a directory that is not there is skipped, and a run that reads NO template
at all is red, because that is how the list goes stale.
**Its allowlist.** `internal/ci/testdata/cardtemplate_allowlist.txt`, one
`file spell date reason` per row — empty, matched by file and spelling and never by line; shrink-only in both
directions, so a row whose spelling has left is as red as a spelling with no
row.
**Its remedy lines.** One per rule, carried on the finding and printed with it:
e.g. ``cores=$(if [ "$(uname -s)" = Darwin ]; then sysctl -n hw.ncpu; else
nproc; fi)`` for `nproc`, ``go version — the go command has no --version and
exits 2 with a usage wall`` for `go_version`, ``there is no /usr/bin/time on a
stock Mac; report the harness's own timing line instead of measuring it in the
card`` for `gnu_time`.
**Its narrowings.** The rule list GROWS (the allowlist is the one that shrinks):
it knows the spellings that have failed on a bench, not every difference between GNU and
BSD userland. It is line-oriented and literal — a command assembled from
variables, or spelt across two lines, is not seen — and a rule whose line also
names its portable other half (`nproc` beside `hw.ncpu`, `readlink -f` with its
own `||` fallback) is not refused, because that IS the portable spelling. The
`readlink -f` fallback is checked per invocation: the `||` must follow that
`readlink -f` before any `;`, `)`, pipe or `&&`, unquoted and at that
command's own level (a `||` inside quotes, inside a nested `$(...)`, or after an
unquoted `#` comment marker does not count), and `2>/dev/null` alone is
refused, since it hides the failure but leaves the result empty. A `.tsv`
beside the templates is a table and is not read. Prose that merely discusses a
spelling is refused like any other line: a template is not the place to write
about commands it does not run.

### `selection` — `internal/ci` is always in the selected packages

**The rule.** `./internal/ci` is added to the package set on every selection —
by `internal/pkgselect`'s `Select` (`go run ./tools/ci select-packages`) — never
only as a fallback when the diff selected nothing.
**The mistake it prevents.** `internal/ci` scans the tree instead of importing
what it guards, so nothing in a diff ever "touches" it: an edit to any package
selects no shard that would run the class tests unless the selection adds it.
Every rule in this document is worth exactly as much as this line. The other
edge: when the diff has ALREADY selected `internal/ci`, the package is in the
set once, never twice in every shard of every leg.
**The test.** `TestSelectPackagesAlwaysAddsInternalCI`
(`internal/ci/ci_selection_test.go`).
**Its allowlist.** None.
**Its remedy line.** `pkgselect.Select does not add ./internal/ci to want
unconditionally; internal/ci scans the tree instead of importing what it guards,
so an edit elsewhere selects no shard to run its class tests`.
**Its narrowings.** The selection is run over a fixture and pinned by a regular
expression over `internal/pkgselect/select.go`; another path into the package set
would need its own row here, and the test cannot know it exists.

### `toolchainroots` — the bench standard and the wall name one list per OS, with one kind each

**The rule.** `internal/swarm/toolchain.go` is the ONE list of the bench
toolchain roots the sandbox wall grants a card, **per GOOS**, and each root
carries its KIND: `~/sdk` read **and execute**, `~/go/pkg/mod` read **without**
execute, `~/go/bin` granted under neither, and on darwin the installed trees
(`/opt/homebrew/Cellar/go`, `/opt/homebrew/Cellar/sbcl`,
`/opt/homebrew/opt/openjdk`, `/Library/Java/JavaVirtualMachines`,
`/usr/local/share/dotnet`) read **and execute**, never a launcher directory.
The linux side of the agreement is the linux provisioning standard:
`tools/benchstandard` carries the linux names between its
`NOVA_TOOLCHAIN_ROOTS` markers (in `checks.go`) and drifts on a missing one. A Mac bench has no
witness, because a Mac's toolchains are installed rather than
provisioned into a home, so the darwin side is the wall's list alone.
The granted roots are the one list in `internal/swarm/toolchain.go`
(`toolchainRoots`, per GOOS, each with its kind).
**The mistake it prevents.** Two contracts that name the same paths in two
places disagree: a provisioning standard that puts Go under `~/sdk` beside a
wall that names no toolchain root and pins `GOTOOLCHAIN=local` denies every Go
card EXECUTION of the bench's own `go`, which falls back to `/usr/bin/go` and
dies on `go: go.mod requires go >= 1.26`. The kind half: a `--read` root
CARRIES EXECUTE on both wall bodies, so a root granted without its kind hands a
card execute over the module cache and `~/go/bin`. The per-OS half: a Mac's
toolchains are INSTALLED and on `PATH`, and still die inside a bare wall —
`go: cannot find GOROOT directory: 'go' binary is trimmed`, `dotnet: Failed to
resolve full path of the current executable []`, `java: Unable to locate a Java
Runtime` — because each resolves its runtime from the directory of the launcher
that ran it and that launcher is a symlink OUT of any granted tree. One list for
every OS leaves the Mac benches dead.
**The test.** `TestBenchStandardAndTheWallNameTheSameToolchainRoots`
(`internal/ci/toolchainroots_class_test.go`), per OS and checked in BOTH
directions — a root the wall grants that the standard does not name is a wall
granting a path that will not be there, and a root the standard names that the
wall does not grant is the original bug returning — plus the kinds by name and
the refusal of any `.../bin`.
**Its allowlist.** None. The list is read from the one source at run time, over
every OS it speaks for (`swarm.ToolchainRootOSes`), so a root — or an OS — added
tomorrow is held to the standard and to a kind on the day it appears.
**Its remedy line.** `the <os> provisioning standard and the wall name different
toolchain roots … They are ONE list. Edit both sides together`, and for a kind,
`the wall grants the module cache ~/go/pkg/mod EXECUTE: it is the
read-without-execute kind`.
**Its narrowings.** It reads the declaration, not a running wall: that the two
kinds are ENFORCED is proved by the wall's own tests on both bodies
(`TestLandlockReadNoExecReadsAndRefusesToExecute`,
`TestReadNoExecReadsAndRefusesToExecuteOnDarwin`), that the argv carries each
root under its own flag is not proved here, and that the version under a
Cellar prefix is read off the launcher rather than guessed by
`TestToolchainVersionDirReadsTheVersionOffTheLauncher`.

### `walltoolchain` — a toolchain on PATH is a toolchain the WALL can execute

**The rule.** `tools/benchstandard`'s executable-root check resolves each of `go` and
`sbcl` off PATH, links followed, and drifts unless the real path lies under a
read root the sandbox wall grants: the WHOLE linux system table
(`linuxReadRoots` in `internal/sandbox/wrap_linux.go`, copied between the
`NOVA_WALL_READ_ROOTS` markers of `tools/benchstandard/checks.go` — `/usr /bin /sbin /lib /lib64 /etc
/run/systemd/resolve /opt /dev /proc`, every one landlock's read subset, which
carries execute), the directory `/etc/resolv.conf` resolves to on this machine
(the wall's `linuxRoots`; `NOVA_RESOLV_CONF` is the witness's test seam
for that file), and `$HOME/sdk` from `internal/swarm/toolchain.go`. The line names the PATH entry, the path it really
resolves to, the granted home, and the remedy — `$HOME/sdk/<tool>-<ver>/` — so
the finding carries its own fix. That check is about EXECUTABILITY INSIDE THE WALL
and is a separate line from `sbcl not on PATH`, which is about presence:
a bench can fail either, both, or neither.
**The mistake it prevents.** `command -v sbcl` answers about the bench user's
own shell. A card runs behind the wall, and an interpreter at
`$HOME/.local/bin/sbcl` satisfies `command -v` while being `Permission denied`
to the card — so the bench passes the standard and every card on it dies, and
the work crowds onto the one bench whose toolchain happens to sit under a
granted root. Moved under `$HOME/sdk/<tool>-<ver>/`, the same toolchain runs
inside the wall.
**The test.** The witness's own tests, in `tools/benchstandard/wall_test.go`, run
the witness against a FAKE bench: a real directory tree under a HOME of its own
and a host whose processes answer from a script. The negative half puts the tool at
`$HOME/.local/bin` — a real misplacement — and demands exactly
one DRIFT line carrying the remedy. The positive half puts it at
`$HOME/sdk/<tool>-<ver>/bin` and demands NO line, which is the half that catches
a check written as "always drift".
`TestBenchStandardAndTheWallNameTheSameReadRoots`
(`internal/ci/benchstandard_wall_toolchain_test.go`) holds the witness's marker
block equal, in order, to `linuxReadRoots` read from the wall's source, and the
check to reading it — a hand-picked subset without `/etc`,
`/run/systemd/resolve`, `/dev` or `/proc` rejects a conforming bench.
The same file holds the dynamic root:
a WSL2-shaped symlinked resolver config makes a tool under its directory
accepted, and the same layout with no resolver pointing there drifts.
**Its allowlist.** None. Both tools are held to the same rule by one loop; a
tool that needs an exception is a tool the wall cannot run.
**Its remedy line.** `<tool> on PATH is <p> -> <resolved>, under NO read root
the sandbox wall grants (the system roots of internal/sandbox/wrap_linux.go,
the resolver directory, and $HOME/sdk from internal/swarm/toolchain.go): a card cannot EXECUTE it inside the wall. Install
it under $HOME/sdk/<tool>-<ver>/ and point the PATH entry there`.
**Its narrowings.** It reads PATH, so a card that calls a toolchain by absolute
path never consulted it; it checks READABILITY OF THE PATH, not that the wall
would in fact grant execute on that root — `toolchainroots` holds the kinds, and
only `sdk` is the execute kind, so `go/pkg/mod` is deliberately not a root this
check accepts; it covers `go` and `sbcl` only, so a third toolchain added to a
bench is invisible until it joins the loop; and the root list here is the LINUX
one, because `tools/benchstandard` is the linux bench's witness.

### `ciworkspace` — the workspace cleanup never fails a job before checkout

**The rule.** Every copy of the `remove stale build dirs from the shared runner`
step in `.github/workflows/ci.yml` refuses an EMPTY `GITHUB_WORKSPACE`
(`[ -n … ] || exit 1`), CONTINUES when the workspace directory does not exist
(`[ -d … ] || exit 0`), and CONTINUES when the workspace holds no `.git`
(`[ -d "${GITHUB_WORKSPACE}/.git" ] || exit 0`). It may not exit non-zero
because `.git` is absent: the step runs before `actions/checkout`, so an absent
`.git` is the normal first-run state and not a fault. The last guard is the
BELT, and it points the other way from the first two: the step ends in
`find "${GITHUB_WORKSPACE}" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +`, so
the shape has to keep that sweep off any directory that is not a checkout of
this repository. `GITHUB_WORKSPACE` is whatever the runner was configured with;
a runner pointed at a home directory, a mount, or a hand-made path by a
misconfiguration would have its contents deleted by a sweep with no belt,
trading a red job for a lost directory. A workspace with no `.git`
has nothing of ours in it to clean, and `actions/checkout` empties a
non-repository workspace itself before it clones, so continuing loses nothing.
**The mistake it prevents.** A precheck of
`[ -n "${GITHUB_WORKSPACE}" ] && [ -d "${GITHUB_WORKSPACE}/.git" ] || exit 1`
fails every job on a runner with a fresh workspace before a line of the
repository has been read, `ci-ok` failing downstream of them, every one
reporting `failed_step: 2:remove stale build dirs from the shared runner`.
**The test.** `TestWorkspaceCleanupDoesNotFailBeforeCheckout`
(`internal/ci/ciworkspace_class_test.go`). It reads `ci.yml` as text, finds
EVERY copy of the named step, and reports the occurrence index and its line
number, so a repair made in five of six copies is found rather than passing on
the first.
**Its allowlist.** None. Every copy of the step is held to the same shape; a
copy that needs an exception is a copy that should not exist.
**Its remedy lines.** Each is prefixed `occurrence <i> of <n> (ci.yml line <l>): `:
``the cleanup step still refuses a workspace with no .git; it runs before
checkout, so a runner whose workspace does not exist yet goes red before a line
of the repository is read``; ``the cleanup step no longer refuses an empty
GITHUB_WORKSPACE (`[ -n "${GITHUB_WORKSPACE}" ] || exit 1`); a missing workspace
is still worth refusing``; ``the cleanup step has no `[ -d "${GITHUB_WORKSPACE}" ]
|| exit 0` guard; a missing workspace directory is the normal first-run state
and must continue``; and, for the belt, ``the cleanup step runs `find
"${GITHUB_WORKSPACE}" … -exec rm -rf` with no `[ -d "${GITHUB_WORKSPACE}/.git" ]
|| exit 0` belt in front of it; a workspace that exists but is not a checkout of
this repository must be left alone rather than emptied``.
**Its narrowings.** It matches the step by its `- name:` text, so a copy renamed
or a cleanup inlined into another step would not be counted; and it reads the
workflow as text, so a value built elsewhere and interpolated in is invisible to
it.

### `parallel` — every test opens with `t.Parallel()`

**The rule.** Every top-level `func TestX(t *testing.T)` in a `_test.go` under
`cmd/` or `internal/`, whatever its build tags, has `t.Parallel()` as its FIRST
statement, or sits on the serial allowlist with its reason.
**The mistake it prevents.** Go tests always run in parallel. A package whose
hundreds of tests run one after another takes minutes against a two-minute
ceiling and a one-minute target. A test stays serial only for a reason: it
calls `t.Setenv`/`t.Chdir`, `os.Setenv` or `os.Chdir`, or assigns a
package-level variable from test code, directly or through a helper, or reads a
process-wide counter (`tokens.Opens`) every parallel test adds
to, or registers into a package map (`go test -race` finds those).
**The test.** `TestEveryTestOpensWithTParallel`
(`internal/ci/parallel_class_test.go`).
**Its allowlist.** `internal/ci/testdata/serial-tests_allowlist.txt`, one
`path:TestName serial: <reason>` per line (`t.Setenv`, `t.Chdir`, `os.Chdir`,
`swaps package var <pkg>.<name>`, or a sentence); shrink-only both ways, a row
without a `serial:` reason is refused, and the list's own `# ceiling: N` line
caps the count so the list can only come down (`NOVA_CI_UPDATE=1` lowers it
with the rows it drops, and never raises it).
**Its remedy lines.** `does not open with t.Parallel(); make it the first
statement, or give the test a per-test seam (cmd.Env, an injected clock,
t.TempDir) instead of t.Setenv, os.Chdir or a package-level swap`; for a stale
row, `delete the stale entry and lower its ceiling line (the list only
shrinks; NOVA_CI_UPDATE=1 does both)`.
**Its narrowings.** It reads the syntax only: a `t.Parallel()` later in the body
does not count, a test that opens with `t.Parallel()` and then races a shared
resource is not seen (that is `go test -race`'s job), and subtests are not
required to call it.

### `testify` — every Go test uses testify

**The rule.** Every Go test under `cmd/` and `internal/` uses
`github.com/stretchr/testify` (docs/STANDARD.md, section 8): `require` for setup and
preconditions, `assert` inside table rows, a testify suite or a testkit helper struct
for a shared rig, `testify/mock` for a fake with expectations, `ErrorIs`, `ErrorAs`,
`ErrorContains`, `Eventually`, `JSONEq`, `ElementsMatch`, `FileExists` and `Panics`
over their hand-written equivalents, named cases under `t.Run`, `t.Parallel()` in
every test, and the environment and working directory injected through the code's
config.
Opening every test with `t.Parallel()` is held by `parallel`
(`TestEveryTestOpensWithTParallel` and `serial-tests_allowlist.txt`); this check does
not count it a second time.
**The mistake it prevents.** A test that stops at its first bad row hides the rest; a
hand-written `if got != want { t.Errorf }` prints less than the assertion it imitates;
`t.Setenv` and a Chdir forbid `t.Parallel()`, so the package's tests queue.
**The test.** `TestTestsUseTestify` (`internal/ci/testify_class_test.go`); the
detector is pinned by `TestTestifyLedgerMeasuresEachShape` and the ledger's
judgement and package-file updates by `TestTestifyLedgerOnlyFalls`
(`internal/ci/testify_shapes_test.go`). `TestTestifyLedgerReadsPackageShards`
checks that package keys and reasons survive the shard loader.
It counts, per package and per kind: `assert`, an `if` whose body calls `t.Fatal`,
`t.Fatalf`, `t.Error`, `t.Errorf`, `t.Fail` or `t.FailNow` directly (the shapes
`if err != nil`, `if got != want`, `if !strings.Contains(...)`, a `reflect.DeepEqual`
guard and every other bool); `env`, a `t.Setenv`, `t.Chdir` or `os.Chdir` call.
**Its allowlist.** the `testify` package ledger, one
`<package>:<kind> <sites> <reason>` row per package and kind (`assert`, `env`) still short. The count
only falls: a package measuring more sites than its row, a package with a site and
no row, and a row above what the package measures are each a red run, and
`NOVA_CI_UPDATE=1` lowers the counts and drops the rows at zero, never raises a
count and never adds a row.
**Its remedy lines.** One per kind, and per site the testify call for its shape:
`require.NoError` and `require.Error` for an err guard, `assert.Equal` and
`assert.NotEqual` for a comparison, `assert.Nil` and `assert.NotNil`, `assert.Len`
and `assert.Empty` for a length, `assert.Contains` for a substring guard,
`assert.Equal`, `assert.ElementsMatch` or `assert.JSONEq` for a `reflect.DeepEqual`
guard, `assert.True` and `assert.False` for any other bool.
**Its narrowings.** It reads the syntax only: an assertion that fails the test by
another route (a `panic`, a helper that calls `t.Fatal` without an `if`) is not seen,
a receiver is taken as a testing value when it is named `t` or `tb` or is a
parameter typed `*testing.T`, `*testing.B` or `testing.TB`, a test that uses testify
for some checks and a bare `if` for others is counted for the bare ones, and
`os.Setenv` is not counted (a `TestMain` may set the process environment once).
Shapes the matcher does not count yet: a failing call in an `else { ... }` block (an
`else if` is an `if` and is counted), a failing call inside a loop or a nested block
under the `if`, a failing call in a `switch` or `select` case body, and a helper in a
non-test file that takes a `testing.TB` and fails it. Each is a bare assertion the
ledger does not see, so the ledger's count is a floor, not the whole of the work.

### `slowwaits` — no per-commit test sleeps over a second or waits out a deadline

**The rule.** No `_test.go` under `cmd/` or `internal/` outside a
`//go:build slow` file writes `time.Sleep` of more than one second, or hands
the code under test a deadline over five seconds and under thirty -- a field,
assignment or `--flag` value named deadline, timeout, wall, grace or idle.
**The mistake it prevents.** Tests get slower as work accretes: a verdict test
sitting a minute in provider retry waits it asserts nothing about, a route test
polling the wrong key until its 30 s ceiling, a deadline test waiting out a
stalled reader for 25 s. The five-to-thirty band is the shape of a deadline proved by
reaching it; below five is the short injected deadline, and thirty or more is
the generous ceiling `waits` and the ten-second law ask for, which costs
nothing and is not read here.
**The test.** `TestNoTestSleepsOverASecondOrWaitsOutADeadlineOverFive`
(`internal/ci/slowwaits_class_test.go`), and `TestSlowWaitsRuleSeesEveryShape`
beside it, which pins the reader against six shapes it must catch and eight it
must pass.
**Its allowlist.** `internal/ci/testdata/slowwaits_allowlist.txt`, one
`path:Func <reason>` per line, keyed by function and not by line. Shrink-only.
**Its remedy lines.** `inject a short one through the seam (200 ms proves a
deadline as well as 10 s does), wait on the event instead of the clock, or move
the test behind //go:build slow (nightly-slow.yml runs it)`; for a stale row,
`delete the stale entry (the list only shrinks)`.
**Its narrowings.** It reads literal durations only (`constDuration`, the
`waits` rule's evaluator): a duration from a variable, a `FAKE-SLEEP` or shell
`sleep` inside a fake's script, a provider retry wait inside the code under
test and a poll that never sees its event are all invisible to it. The
per-package budget (`slowtests`) and the measured table are the net under those.

### `unitwaits` — no unit test waits on the wall clock

**The rule.** A budget verdict must be the same on any machine, so what fails a
leg is static. Every `_test.go`
the unit tier compiles (under `cmd/`, `internal/` and `tools/`, outside
testdata, built with no custom tag: a `//go:build functional`, slow, soak,
nightly, novadisk, perf or race file is not a unit test) is read, and a call of
`time.Sleep`, `time.After` (so `<-time.After` in a select), `time.Tick`,
`time.NewTimer`, `time.NewTicker` or `time.AfterFunc`, any of those named as a
value (the real clock handed to a seam), or a `context.WithTimeout` /
`WithDeadline` whose context the same function waits on (`<-ctx.Done()`), is
refused unless internal/ci/sleeps-skips_allowlist.txt names the package
directory and the top-level function it is written in. A wait through an
injected clock seam is not a wall-clock wait and is not found: the seams the
tree has are internal/bus's lockClock, internal/swarm's
batchClock and pullClock, internal/log.Clock and
the injected `Sleep func(time.Duration)` and `now func() time.Time` fields of
internal/swarm.
**The mistake it prevents.** A load gate makes the wall-time verdict depend on
the machine: the same head red at one load and green at another. And a change
that adds a SLEEPS skip and its ledger row in one diff would pass without the
ratchet against the merge parent.
**The test.** `TestNoUnitTestWaitsOnTheWallClock`,
`TestSleepsLedgerIsTheTreesSleepsSkips` (the ledger names exactly the tree's
SLEEPS skips and waiting functions: a missing row or a stale one is red),
`TestSleepsLedgerOnlyShrinksAgainstTheMergeParent` (a row HEAD has that the
ledger at the merge base lacks is red: HEAD's first parent in CI, dev's tip on
a pull request's merge ref and in the queue, the comparison `classtests` makes;
the merge base with origin/dev on a developer's branch; a base with no ledger is
the seed), with the controls
`TestWallClockWaitDetectorFindsTheWaitsAndNotTheSeam`
(`internal/ci/unitwaits_class_test.go`) and `TestSleepsLedgerGrowthIsReadOutOfGit`
(functional-tagged, `internal/ci/unitwaits_git_functional_test.go`: seven
commits in a repository it builds).
**The ratchet row (the waits the tree owes).** The ledger's rows are the
wall-clock waits the tree still carries, grandfathered or SLEEPS-skipped.
`TestNoUnitTestWaitsOnTheWallClock -v` prints the count; it only falls.
**Its allowlist.** `internal/ci/sleeps-skips_allowlist.txt`,
`pkg<TAB>Func<TAB>where`, read through `slowtests.ParseSleeps`, the reader
`make test`'s CI-SLEEPS check uses. It only shrinks.
**Its remedy line.** `inject a clock (an injected Sleep
func) or tag the file //go:build functional (the ledger only shrinks)`.
**Its narrowings.** It reads the test files only: a wall-clock wait inside the
code under test (a production retry that sleeps) is invisible to it, and the
printed CI-SLOW line and the nightly enforcing run are the net under that. A
context deadline handed to the code under test and waited on there is not seen.

### `allowlist` — every list is read through the one helper

**The rule.** Every list file under `internal/ci/testdata/` (`*allowlist*.txt`,
`*.allow`, `*_examples.txt`) is loaded by a call to `loadAllowlist` or
`allowlist.Load`. Counted package shards are discovered recursively below their
ledger directories and consumed through `allowlist.LoadPackages`. No Go file
elsewhere in the tree reads a list or shard directly with `os.ReadFile`,
`os.Open` or `readFile`.
**The mistake it prevents.** A class test with its own list format and no update
path turns every removal into a hand-written script rewriting its list, a round
trip per list per change. The helper gives every list one reader and one `NOVA_CI_UPDATE=1`
writer; a list read any other way has no update path.
**The test.** `TestEveryAllowlistIsReadThroughTheOneHelper`
(`internal/ci/allowlist_update_test.go`); the helper's own contract is
`internal/ci/allowlist/allowlist_test.go`.
**Its allowlist.** None.
**Its remedy lines.** `is not read through allowlist.Load (loadAllowlist in a
test)`; `reads a list file directly; read it with loadAllowlist or
allowlist.Load`.
**Its narrowings.** The walk is syntactic: a call's path is resolved through
string literals (`filepath.Join` parts included), package constants, a variable
assigned in the same function and one level of parameter; a path computed any
other way is not seen. A package none of whose files spells a list file's name
is not parsed, since the resolver could not reach a list from it.

### `seatwrap` — no script wraps `nova-secrets exec` around a seat tool for its Redis password

**The rule.** No shell file in the tree (a `.sh`, `.bash` or `.zsh` file, or an
extensionless file with a sh/bash/zsh shebang) and no Markdown page under
`docs/` runs `nova-secrets exec` with an `--only` list made only of Redis
passwords (`NOVA_REDIS_*`, `REDISCLI_AUTH`) around `redis-cli` or one of the
seat tools the test's `seatTools` names. A seat tool takes `--seat <name>` (or
`NOVA_SEAT`) and reads the Redis user and password from the seat's file itself,
through `internal/seatcred` on the library the exec verb runs on.
**The mistake it prevents.** A session whose every Redis call goes through bash
wrappers in a scratchpad, one around redis-cli and one around a tool that could
not read its own seat's password, has to recreate them each time, and a wrapper
line left in a script or a doc teaches the next session to rebuild it.
**The test.** `TestNoSecretsExecWrapsASeatTool`, with its control
`TestSeatWrapRuleSeesEachShape` (`internal/ci/seatwrap_class_test.go`), which
pins the wrapper shapes, a multi-line launch and two siblings as red, and a
model-key wrapper, a tool outside the set and the `--seat` spellings as green.
**Its allowlist.** None.
**Its remedy line.** Pass the tool `--seat <name>` (or set `NOVA_SEAT`); the
test's message names the hand-read spelling.
**Its narrowings.** A command is one line after backslash continuations are
joined, so a wrapper split across a heredoc or a shell function is not seen;
an `--only` that names any key other than a Redis password (a model key, a
GitHub token) or is `all` or a variable is not flagged, because `--seat`
delivers only the Redis login and that wrapper is still the way to deliver the
rest; a wrapper around any other nova tool (`nova-tokens`, `nova-redis serve`)
is not flagged until that tool takes `--seat`; and only
`docs/` Markdown is read, so a README elsewhere is not.

### `seatredis` — no verb of a live tool that selects a seat refuses an empty `--redis`

**The rule.** Every `--redis` flag of a live package that selects a seat (a
package whose Go calls seatcred's `FromArgs`, directly or on
`seatcred.Process()`; `cmd/nova-table` among them) defaults to a seat-aware
expression: the tool's seat-first default (`redisDefault(...)`, or a name the
function declared from it), `redisOr(...)` around a hand-parsed value, or
`seatcred.Addr()`. Each of these is the verb's own environment default first,
then the selected seat row's address from seats.tsv. A `--redis` read by hand
(a `["redis"]` index or `flagValues(..., "redis")`) sits in a function that
calls `redisOr`.
**The mistake it prevents.** A tool given `--seat coordinator` and a seats.tsv
row naming its Redis, whose verbs declare `--redis` with no default, refuses the
empty address before `store.Open` can fall back to the seat — so a session
still types `--redis` on every line, doing by hand what a wrapper script
does.
**The test.** `TestNoVerbRefusesAnEmptyRedisUnderASeat`, with its control
`TestSeatRedisRuleSeesEachShape` (`internal/ci/seatredis_class_test.go`). It
reads the shared AST and fails when the set of seat-selecting packages lacks
`cmd/nova-table` or no `--redis` flag is found there, so it cannot pass by
matching nothing. The control pins `""`, `os.Getenv(...)`, a `StringVar` with
`""`, a bare `flags["redis"]`, `redisDefaultFrom()` and a name declared from
anything else as red, and the seat-aware spellings and a `--store` flag as
green.
**Its allowlist.** None.
**Its remedy line.** `default the flag to the tool's seat-first default
(nova-table: redisDefault(os.Getenv)), wrap a hand-parsed one in redisOr, or use
seatcred.Addr()`.
**Its narrowings.** Only `String`, `StringVar` and the two hand-parse shapes
are read, and only a flag named `redis`. A live tool that selects no seat is
not held (`nova-tokens`): it has no seat to fall back to. A
hand-parsed read counts as covered when its function calls `redisOr` anywhere,
not necessarily on that value.

### Tests this spec demands

This list sits inside **The class tests** on purpose, as its last entry: half (b) of `TestSpecCIIndexesEveryClassTest` (`internal/docs/spec_ci_index_test.go`) reads every `Test…` name this section prints, so a test named below that is renamed or deleted turns that test red instead of leaving a line that describes a test that no longer runs.

Every class test reads this repository's own text — `.go` files, `.github/workflows/*.yml`, the `Makefile`, `docs/` — through the shared `repoTree(t)` (one `filepath.WalkDir` and one `go/parser` pass per test process); each rule carries `t.Parallel()`, filters the tree itself, and writes only to its own `t.TempDir()`, with allowlists under `internal/ci/testdata/` checked in both directions so they only shrink. Nothing reaches the network (forges and endpoints are fakes; real logs are fixtures), and each rule is proven able to fail by a planted offender before it is trusted.

1. `TestNoFixedWaitsOnTheCIPath` — no `_test.go` on the CL path carries a fixed `time.Sleep` over 100 ms, a context/timer bound under ten seconds, or an elapsed-time assertion; the allowlist only shrinks.
2. `TestNoUnquotedPathsInTemplateLiterals` — a filesystem path in a JSON or `text/template` literal is wrapped in `strconv.Quote` (or `oneline.Quote`); a raw `filepath.Join` or `C:\…` literal there is refused.
3. `TestSlowTestsUnderBudgetIsOK` / `TestSlowTestsOverBudgetNamesThePackageAndSlowestTests` — a package whose summed `go test -json` elapsed time exceeds `--budget` (default 60 s) is a refusal that names the package and its slowest tests, worst first, capped at three.
5. `TestNoRealNetworkHostsOnTheCIPath` — no `_test.go` on the CL path names a real host in a URL or bare `host:port`; endpoints are `httptest` or a local fake, and only `//go:build nightly`/`soak` files may reach the network.
6. `TestGoEnvClassRuleHoldsOverTheRepository` — every `exec.Command("go", …)` in `cmd/` and `internal/` sets `cmd.Env` from `goenv.Clean(...)`, so a child `go` never inherits the caller's `GOFLAGS`/credentials.
7. `TestRemoveAllOnlyOnTempOrThroughSafepath` — outside `internal/safepath`, `os.RemoveAll` may only take a variable returned by `os.MkdirTemp` in the same function; every other removal goes through `safepath.RemoveUnder`.
8. `TestNoTestReachesAHostThroughAnUnfakedSeam` — every host seam calls `testguard.RefuseHosts` before starting the child, so a test holding production code refuses under `NOVA_TEST_NO_HOST` rather than reaching a bench.
9. `TestNoTestComparesAPathAgainstASlashLiteral` — a path is never compared against a `/`-containing literal; compare `filepath.ToSlash(got)` or build the want side with `filepath.Join`.
10. `TestToolRunsInTestsWriteIntoATempDir` — a test that runs a tool names every output path inside `t.TempDir()`, never a relative literal that lands in the tree.
11. `TestNoTestGlobsTheSharedTempDir` — no `_test.go` lists (`Glob`/`ReadDir`) a directory built from `os.TempDir()`; it reads only its own `t.TempDir()`.
12. `TestEveryNovaBusConsumerDropsProgressLines` — every package that starts `nova-bus` and reads its output routes lines through `bus.IsProgress(`/`Classify(` so progress never enters a parsed protocol stream.
13. `TestNoMultiLineValueIsWrittenToAStepOutput` — a variable assigned from a one-item-per-line producer without a single-line guard may not be written to `$GITHUB_OUTPUT`/`$GITHUB_ENV`.
14. `TestNoTestAssertsAWallClockBoundUnderTenSeconds` — no `_test.go` carries a literal duration under ten seconds where the test leans on the wall clock; thirty seconds is the generous bound, or `// wall-ok:`.
15. `TestCIBuildTestLintCommandsGoThroughMake` — every build/test/vet/format command in `ci.yml` is a `make` invocation.
16. `TestMakefileIsTheOneEntry` — the Makefile declares `build`, `test`, `test-full`, `lint`, `check`, `clean`, `help` as phony targets, with `check` the union of CI's gates.
19. `TestNoCacheStepRunsOnASelfHostedRunner` — every `actions/cache` step in `ci.yml` is `github-hosted`-only and every `setup-go` says `cache: false`.
20. `TestEveryActionIsPinnedBySHA` — every `uses:` in `ci.yml` and `certification.yml` is `owner/action@<40-hex-sha>`.
21. `TestEveryTriggeringEventReachesACIOKVerdict` — `ci-ok` has a verdict step gated for every triggering event (`pull_request`, `merge_group`, `push`, `workflow_dispatch`).
22. `TestEveryCommandMeetsTheOnboardingStandard` — every `cmd/` tool's help ends in an `example:` block, a bare command refuses in one line, and `docs/TESTS.md` carries its `### First run` transcript.
24. `TestNoToolIsWrittenTwiceInTheTranscripts` — no two `## ` headings in `docs/TESTS.md` carry the same tool name.
25. `TestEveryToolPrintsTheOneVersionLine` — every `cmd/nova-*` binary answers `version` with one line in the `internal/buildinfo` grammar.
26. `TestTheVersionGrammarIsSpelledOutOnceInTheSpec` — `docs/SPEC.md` states that grammar once.
28. `TestNoGhPrMergeSpellingInTheToolsGo` / `TestNoGhPrMergeSpellingUnderDotGithub` — no `gh pr merge` (or `--auto`) spelling reaches the dev queue but a batch; enqueue is `internal/merge.Enqueuer.Enqueue`.
29. `TestEveryTestBuildTagIsRunBySomeScheduledJob` — every opt-in build tag a `_test.go` carries is named by a scheduled workflow's `go test -tags`.
30. `TestTheNetworkExemptTagsHaveAHomeInTheSchedule` — the net checker's `nightly`/`soak` exempt tags have a scheduled leg.
31. `TestSomeScheduledJobRunsTheRaceDetector` — some scheduled job actually passes `-race`.
32. `TestSelectPackagesAlwaysAddsInternalCI` — `./internal/ci` is added to the package set on every selection, not only as a fallback.
33. `TestBenchStandardAndTheWallNameTheSameToolchainRoots` — the bench standard and the wall name one toolchain-root list per OS, each root with its kind, checked in both directions.
34. `TestWorkspaceCleanupDoesNotFailBeforeCheckout` — the workspace-cleanup step refuses an empty `GITHUB_WORKSPACE`, continues over an absent directory and over a workspace with no `.git` (the belt), so it never fails a job before checkout.
35. `TestSharedRepoTreeListsAndParsesTheRepository` — the shared tree is this repository, every `.go` file carries a usable syntax tree, and the loader runs exactly once.
36. `TestSharedRepoTreeSkipsTheGitDirectory` — `.git` is never walked into.
38. `TestSpecCIIndexesEveryClassTest` — every class test is named by the index and every indexed `Test…` name exists.
39. `TestNoGhInAnyBrief` / `TestBriefRuleCatchesEachSpelling` — no brief this repository ships tells a child to call GitHub (GitHub is a git remote only). **The mistake it prevents:** one PR can cost ~60 REST calls, and a token's hourly budget spent freezes every merge for an hour; a brief that says `gh api`, `gh pr`, GraphQL or a bare remote clone teaches the next child to spend the budget again. **The sweep:** `internal/swarm/templates.go` and every card fixture under `cmd/nova-swarm/testdata/cards/*.md` (an empty glob is a red run, so a source that moves must move in the list too); a line matching `gh ` as a command (line start or after a non-word, non-path character, so "through " does not match), `graphql` in any case, or a `git clone` of any remote (`https://`, `ssh://`, `git@`) without `--reference` on the same line is refused. **No allowlist:** the remedy is the verb, not an exception. **The remedy line:** `<file>:<line>: gh  in a brief: <line>` (or `GraphQL in a brief`, or `a remote clone without the bench mirror as --reference`), with the fix named once: the verbs that read a brief or a post, or a clone with `--reference ~/nova-bench/mirror/<repo>.git`. **The control:** `TestBriefRuleCatchesEachSpelling` feeds the scanner one brief per spelling and wants exactly one finding at that line, and a brief carrying the verbs, a mirror-referenced clone and the words "through" and "high" wants none.
40. `TestCopiesRunNiced` — every path that execs a copy's harness, or a coordinator child's local test run, steps its OWN process down to nice 15 (`internal/yield`, `Nice = 15`: `setpriority(PRIO_PROCESS, 0, n)` on darwin, where a nice belongs to the process, and on Linux, where a nice belongs to a THREAD and a child forked from an un-niced thread inherits 0, `setpriority(PRIO_PROCESS, tid, n)` over every thread in `/proc/self/task`, repeated until a pass sets none — the one-thread form leaves most children of a wrapper at nice 0) BEFORE the exec: `cmdLocal` (`cmd/nova-ci/local.go`, before its first `localCapture(`; its `nice -n` is pinned to `yield.Nice`) and `cmdNative` (`cmd/nova-swarm/main.go`, `yieldNative(nativeToCI, ...)` before `nativeRun(`: every card a sprint member or reader launches, so the wall, the harness and the card's child inherit it), no production caller sets a `Yield` of its own, and no production file writes `nativeToCI` (read on the parsed tree: any assignment naming it, or its address taken; the test binary's TestMain alone makes it a no-op, because that binary is a CI leg running `cmdNative` in-process) (CI over work is a permanent setting: work creates more CI, so without it the fleet is unstable). **The mistake it prevents:** copies at nice 0 share the cores evenly with the CI legs on the same machines, so with the slots raised the load per core climbs past 4 and a CI shard nears the two-minute cap: more work means slower CI means more work waiting. **The sweep:** the named exec path, read as text: the yield call's index in the function body against the exec call's. **No allowlist:** a new worker kind gets its nice by calling `yield.ToCI` before its exec and joining the list. **The remedy line:** `<file> <func>: no <yield> call: a copy or a local test run must yield to CI before it execs`, or `<yield> stands after <exec>: a yield after the exec yields nothing`, or `nice_linux.go: the one-thread form setpriority(PRIO_PROCESS, 0, n) nices the calling thread only`. **The control:** `internal/yield/yield_test.go` reads the process's own priority back after `ToCI`; `internal/yield/child_test.go` starts sixteen children from fresh goroutines after `ToCI` and wants each to read its own nice as 15 (the one-thread form fails it).

### `cap` — every job two minutes, permanently, on every platform

**The rule.** Every job in every workflow under `.github/workflows/` declares
`timeout-minutes: 2`, literally: no expression, no per-leg ceiling in a matrix,
no tier that is exempt (nightly, certification and release included), on every
platform. Work that needs longer is split into parallel functional test programs,
each its own job under the cap. Raising the cap is never the fix.

**The mistake it prevents.** A larger cap is a "hang detector with room": under
one, a nine-minute leg runs to completion and is treated as normal, and every fix
to it takes ten minutes per iteration. Tests accrete while work goes on, and
without a hard cap every check-in ends up waiting half an hour; we fix it or it
does not land, and nothing else stops the test creep.

**The test.** `TestEveryCIJobIsCappedAtTwoMinutes` (`internal/ci/ci_budget_test.go`)
reads every workflow file, `.yml` and `.yaml`, and refuses a job over the cap, a
job with no literal job-level cap (a step-level timeout does not count), or a
`timeout-minutes` expression. `TestShardGoTestTimeoutIsUnderTheJobCap`
keeps `go test -timeout` (`pkgselect.ShardGoTestTimeout` and the Makefile's
`GOTEST_TIMEOUT`, `MERGE_TIMEOUT`, `DARWIN_TIMEOUT`, `SHORT_TIMEOUT`) under the
cap, and `TestEveryMakeTimeoutIsUnderTheJobCap` reads every `-timeout` in every
Makefile recipe, expanded, and refuses one at or over the cap (test-short, the
hosted legs' target, carried a literal `12m` no check read), so a run ends with a
Go stack before the job cap kills it without one.

**The reach.** These tests police the tree they run in. A scheduled run executes
the default branch's copy of the workflow: its test-hosted
carries
`timeout-minutes: 15` and a windows-latest leg, and its four windows legs ran
178-195 s to success uncancelled. The cap reaches a schedule when these files
reach the default branch.

**The remedy line.** Split the job (shards by measured package size, or one job
per functional program), move a process-in-the-loop test behind the `slow` tag
into the nightly functional matrix, or put the leg on a machine that compiles the
set in seconds. Never a larger number.

### `wholetree` — no doc and no card tells anyone to test the whole tree

**The rule.** No Markdown file in the tree outside `testdata/` (the docs,
`AGENTS.md`, `TESTING.md`, the READMEs), no card template (`CardTemplateDirs`),
and no brief source (the `briefSources` the no-gh rule reads) spells `go test`, with any flags, over `./...`
or over one of the three trees that are most of it (`./cmd/...`,
`./internal/...`, `./tools/...`).
The door is `nova-ci local`: the packages
`go run ./tools/ci select-packages` picks against the merge base of the base
and `HEAD`, run through the Makefile's `test` target under `nice -n 15` at
`-p 2`, with the unit budgets, exiting as CI would ([TESTING.md](../TESTING.md)).
**The mistake it prevents.** CPU is for real work: children test the packages
they touched and nothing else. A child left to invent a way to test what it
changed (a sharding script under a timeout, a loop hunting t.Parallel
violations, hand timing scripts) runs the whole tree on the benches the real
work shares, and a doc that spells the whole-tree run teaches the next child to
do it again.
**The test.** `TestNoWholeTreeGoTestInDocs`, with its control
`TestWholeTreeRuleSeesEachSpelling` (`internal/ci/wholetree_class_test.go`),
which pins the bare, flagged, piped and table-cell spellings and the
`./cmd/...`, `./internal/...` and `./tools/...` trees as red, and `nova-ci
local`, a named package, one tool's own subtree, `go vet ./...` and prose as
green.
**Its allowlist.** None; the `cmd/`, `internal/` and `tools/` guard cells of
`AGENTS.md` are `nova-ci local`, from `internal/docs/catalog.go`.
**Its remedy line.** `run nova-ci local (the unit tier CI runs for this diff)
or name the packages you touched: nice -n 15 go test -p 2 -count=1 ./cmd/<tool>`.
**Its narrowings.** One line at a time: a command split across a backslash
continuation is not seen. A package list spelled out by hand, however long, is
not read, nor is one tool's own subtree (`./cmd/nova-ci/...`), and neither are
the Makefile's `test-full` and `test-slow` targets, which CI's whole-tree runs
call.
### `ci-receipt` — ci-ok reports every run to Redis from the runner

**The rule.** The `ci-ok` job of `.github/workflows/ci.yml`, whose own `if`
is exactly `always() && github.event_name != 'schedule'`, has exactly one
step that runs `nova-ci github receipt --from-runner`, under `if: always() &&
(github.event_name != 'pull_request' ||
github.event.pull_request.head.repo.full_name == github.repository)`,
with `set -euo pipefail` and no `|| true` or `continue-on-error`, and its
command is exactly this tree's writer, `go run ./cmd/nova-ci github receipt
--from-runner`, under the bench seat (`"$HOME/.local/bin/nova-secrets" exec …
--only NOVA_REDIS_BENCH_PASSWORD --require NOVA_REDIS_BENCH_PASSWORD`,
`NOVA_SPRINT_REDIS_USER=bench`, `--redis "$NOVA_CARD_REDIS"`), passing every
field of the row from the run's own context — `--repo`, `--sha` (the PR head,
else `github.sha`), `--run-id`, `--pr`, `--workflow`, `--conclusion`
(`job.status`) — and nothing the row does not carry (`--job`, `--event`,
`--head-branch`, `--base-branch`). It never calls `curl`, `gh api` or
`api.github.com`. Its run block carries exactly one installed tool path, the
`"$HOME/.local/bin/nova-secrets" exec` wrapper, and none of: any other installed
tool path (`~/.local/bin`, `.local/bin/nova-ci`), a parked tool's name, the
words `installed`, `RECEIPT WRITER`, `probe` or `version`, `flag provided but
not defined`, `command -v nova` or `which nova` — so it neither runs nor probes
an installed build.
**The mistake it prevents.** With the signed webhook receiver behind a tailscale
funnel kept off by design, and nothing allowed to poll GitHub for a check state
(the no-polling rule), `ev:github` stays empty unless the run reports
itself. The runners are ours and run as the bench seat, so the run does: one
`ev:github` row (internal/cireceipt). A receipt that silently did not happen
must never read as one that did, which is why the step must fail the job. The
writer is this tree's, versioned with the commit under test, so no runner's
installed build matters. The cost is stated, not hidden: a tree that does not
compile writes no receipt and exits 1; this is accepted, because the red ci-ok
is itself the signal, a store watch is for the green-or-red completion of runs
that reached the step, and a tree that does not compile fails the other jobs
first. The receipt STEP carries the
head-repo guard every self-hosted job carries (`github.event_name !=
'pull_request' || github.event.pull_request.head.repo.full_name ==
github.repository`), because it runs this tree's code holding the bench
seat's Redis password and a fork's pull request must not reach it. The ci-ok
JOB does not carry it, on purpose: on a fork's pull request every self-hosted
need is skipped and ci-ok reads those skips as red, which is what keeps the
fork PR out of the merge queue; a guard on the job would skip ci-ok, and
GitHub counts a skipped required check as passing, so the fork PR could be
enqueued with no PR-stage CI. So a fork PR's ci-ok runs, is red, and writes no
receipt. The receipt is the one row; no `ci:<repo>:<sha>:gh` fold or
`pr:<repo>:<n>` claim is written.
**The test.** `TestCIOKReportsEveryRunToRedisFromTheRunner`
(`internal/ci/ciok_receipt_class_test.go`), reading the job as YAML,
comparing the job's `if` exactly (no head-repo guard), the receipt step's
`if` exactly (`always()` and the head-repo guard) and the step's whole run
block line by line.
**Its allowlist.** None: one step, one command, no exceptions.
**Its remedy line.** Each red names what the step lacks or names, e.g. `the
receipt step calls GitHub; the run's own context has every field`; the fix is
the step, never the test.
**Its narrowings.** It reads the step's text and does not run it, so a bench
with no `card.env` is found by the run itself (the step's own refusal names
the bench play), not here.

### `silent` — no silent failure on the copy model's live path

**The rule.** Every verb returns an error that you see, for breadcrumbs as you
work; failing silently is not allowed, because without it no system built here
can be made reliable. In the live packages
(`internal/nsprint/{reconcile, taskcard,
table, card, launch, fn, capacity, pipeerr}`, `internal/ntable`,
`cmd/nova-table`), no non-test `.go` file holds
`_ = err` (any error-named identifier assigned to the blank identifier) or a
`|| true` inside a Go string literal (an embedded script step whose exit is
thrown away). A failure is returned, printed as one typed line (`REFUSED <verb>:
<why>` on stderr with exit 1, or the verb's own receipt vocabulary) or, in a
loop, counted and printed once per pass (the reconciler's `DUTY <name> ...
err=<text>` line is the model).
**The mistake it prevents.** A render that reports a Redis outage as `NOTASK`;
a refused end that leaves only `code=2` on a line written to `/dev/null`; a
reconciler duty that throws its pass's `REFUSED` lines away and returns clean
counts; a consumer whose `slots` field will not parse, skipped every pass with
no line; a lapsed copy the expire sweep cannot end, left in `working` with
nothing said; a go-redis pipeline whose first absent field (`redis.Nil`) hides a
later `NOPERM` and reads the rest as zero. Each is a card that sits still while
the table says nothing.
**The test.** `TestNoSilentFailureOnTheLivePath`
(`internal/ci/silent_class_test.go`), with the rule proved over source in
`TestSilentRuleReadsTheTwoShapes` (the two shapes refused; a discarded value
that is not an error and a `|| true` in a comment are not).
**Its allowlist.** `internal/ci/testdata/silent_allowlist.txt`, read through
the one helper (`allowlist`), one `file:function <why it is judged not silent>`
per row (a package-level literal is `file:<package>`); EMPTY under
`# ceiling: 0`, shrink-only in both directions, and a row with no reason is
red.
**Its remedy lines.** `` `_ = err` drops the failure where it happened; return
it, print one typed line (REFUSED <verb>: <why>) or count it into the pass's
DUTY line``; `` `|| true` inside a Go string literal hides an embedded script
step's failure; drop it and read the step's exit``; for a stale row, `delete the
stale entry (the list only shrinks; NOVA_CI_UPDATE=1 drops it)`.
**Its narrowings.** Non-test `.go` files of the live packages only. It reads the
two shapes by their syntax: `_, _ = f()` (a discarded multi-value), `_ =
f.Close()`, an `err` assigned and never read, and an `if err != nil { return
nil }` are not read (`go vet`, errcheck and the reviewer's eye are theirs), and
neither is a Lua function that returns `nil` where a `REFUSED <why>` belongs.
The go-redis pipeline shape has its own remedy rather than a rule:
`internal/nsprint/pipeerr.Exec` walks every command of a pipeline whose fields
may be absent and returns the first error that is not `redis.Nil`.

### `classtests` — no merge deletes a test file or a list undeclared

**The rule.** What a change takes away from its first parent's tree is read
out of git and compared with what the same change declares. Every `_test.go`
and every list under `internal/ci/testdata` that HEAD's first parent had and
HEAD lacks (renames excluded) is a red run unless a `<path> <why>` row for it
was ADDED to `internal/ci/testdata/deleted-tests.txt` in the same change; a
row that names no deletion of the change is red too. On a pull request the
checkout is the merge ref and the first parent is dev's tip, so the set is
exactly what merging the change deletes from dev; in the merge queue the
same; on dev, a squash's own effect. On a promotion — dev to main or
sprint/foundation to dev (the `pull_request` event with `GITHUB_BASE_REF` main
and `GITHUB_HEAD_REF` dev, or `GITHUB_BASE_REF` dev and `GITHUB_HEAD_REF`
sprint/foundation, whose payload's head repository is this repository, read by
`promotionSkip`) the comparison does not run and the run logs a NOTE saying why:
the first parent is the base branch's tip, so the set would be every deletion
the head branch accumulated since the last promotion, each declared in the
change that made it on that branch, where this rule ran; a fork's branch named
dev or sprint/foundation is refused by the head repository. On
sprint/foundation to dev the skip drops the stale-base check too (a head that
lacks files dev has reads as a deletion only in the comparison the skip
drops), so it has one precondition, read from git (`promotionBaseCheck`): dev
is expected to be an ancestor of the head's history at promotion time, dev
merged into sprint/foundation first; `git merge-base --is-ancestor` of the
merge ref's first parent (dev's tip) and second parent (the head) must hold.
When it does not, or the head's ancestry is cut by a shallow graft so it
cannot be read, or the checkout is not a merge ref, the skip is not taken: the
full comparison runs and the NOTE says why (fail closed). The promotion pull
request's runs fetch the head's full history (`git fetch --no-tags
--filter=blob:none --unshallow origin
+sprint/foundation:refs/remotes/origin/sprint/foundation`) to answer it. Once
the promotion has landed, a landing run (`landingRun`) at a two-parent merge
keeps the first-parent comparison and excuses what the promoted branch itself
did (`excuseDevDeletions`). A main run (`mainRun`: `push`, `workflow_dispatch`
or `schedule` on `refs/heads/main`, main being the default branch where
schedules run; or no GitHub environment with main checked out, so a local
audit agrees with CI) excuses dev's history. A dev run (`devRun`: a merge-queue
group for dev, ref `refs/heads/gh-readonly-queue/dev/...`; `push` or
`workflow_dispatch` on `refs/heads/dev`; or no GitHub environment with dev
checked out) excuses sprint/foundation's, the same rule one branch down: the
group carries no head branch name, so the landed promotion is read from git as
a merge commit whose second parent is an ancestor of
`refs/remotes/origin/sprint/foundation`. Below, "the side branch" is dev on a
main run and sprint/foundation on a dev run. A deletion is excused only when
the side branch's history since the last promotion (`git log --no-renames
<merge-base>..<second-parent> --diff-filter=D --name-only`, the second
parent's ancestry above the merge base with the first parent) deleted the path
AND the second parent's tree lacks it, so a file the side branch deleted once
and restored is still its own, and a file it deleted before the last promotion
that the base branch holds again is the base branch's; a row added in the
change is excused when that history deleted its path. The second parent must
be the side branch's: `git merge-base --is-ancestor <second-parent>
refs/remotes/origin/<side-branch>` must hold, which the side branch's
non-fast-forward rule keeps true for every promotion, and the ref is read for
that confirmation only, never as the history, so the verdict for one sha never
changes as the side branch advances. On main a second parent outside dev's
history, or one `origin/dev` cannot vouch for here (stale, or shallow),
excuses nothing and is a finding of its own naming the fetch. On dev a merge
commit whose second parent is not sprint/foundation's is not a promotion: it
excuses nothing and is compared as everywhere, with a NOTE and no finding of
its own; a missing or shallow `origin/sprint/foundation` is the finding naming
the fetch, as on main. What is not excused is a finding as everywhere: a file
the base branch alone had and the merge lost, since the side branch never
deleted it. And the merge's own change is checked against the second parent:
every guarded path the side branch's tip has and HEAD lacks is a finding
unless a row added beyond it declares it, which first-parent comparison alone
never sees. The NOTE names how many deletions and rows the history excused. A
one-parent commit on main or dev is compared with its parent as everywhere:
the dev queue's merge method is squash, so a promotion that lands through the
queue as a squash is the ordinary comparison and is red for every deletion the
promoted branch made; only a promotion that lands as a merge commit is read as
one (the squash of the same tree is not excused). The ancestry must be
complete: the landing steps of ci.yml (`test`, `test-hosted`) and
certification.yml (`test`) run `git fetch --no-tags --filter=blob:none
--unshallow origin +dev:refs/remotes/origin/dev` after checkout on the default
branch, and `git fetch --no-tags --filter=blob:none --unshallow origin
+sprint/foundation:refs/remotes/origin/sprint/foundation` on a dev run at a
merge commit and on the promotion pull request (a workspace that is already
complete takes the same fetch without `--unshallow`); when the second parent's
ancestry is cut by a shallow graft, or the second parent is missing, nothing is
excused, the readable comparisons run (the first parent's, and the second
parent's tree whenever that commit is present, as it is at `fetch-depth: 2`),
and the unreadable history is a finding of its own naming that fetch, so a
landing run never passes on a history it could not read, and the verdict for
one sha never depends on what the side branch did later.
**The mistake it prevents.** A branch rebased with a stale tree that lacks
files dev gained an hour before — a class test, its allowlist and its controls —
undoes the fixes they held when it merges. Every check on the merge is green,
because a rule that is not there cannot fail; only a reader finds the loss. A
list of class tests kept in the tree does not help, because a squash overwrites
the tree, list and all: deleting the file and its row together stays green.
Hence git, not the tree, as the base of the comparison, and a declaration that
only counts when the same change adds it — a stale tree's old rows declare
nothing.
**The test.** `TestNoMergeDeletesATestFileUndeclared`
(`internal/ci/classtests_class_test.go`): HEAD's first parent from the raw
commit object (a shallow checkout grafts parents away in traversal and keeps
them in the object), `git diff -M --diff-filter=D --name-only <parent> HEAD`
for the deletions, `git diff <parent> HEAD -- deleted-tests.txt` for the
rows this change adds. `TestMergeRuleReadsTheDeletionOutOfGit` proves it over
a repository it builds: the stale-base squash shape red for the test file and
the list and silent for a source file, a rename not a deletion, a same-change
row green, a row naming no deletion red, an old row declaring nothing.
`TestGuardedByMergeRuleReadsThePath` and
`TestDeclaredRowsAddedReadsOnlyTheAddedRows` pin the two readers;
`TestPromotionSkipReadsTheEvent` pins the promotion shape against its
reversed witnesses (the same event into dev from a feature branch, a feature
branch into main, sprint/foundation into main, another sprint branch into dev,
dev into sprint/foundation, a fork's branch named dev or sprint/foundation, an
absent payload, a push, a merge-queue group, no environment), so a loosened
promotion form (any base, a `sprint/` prefix) is red;
`TestPromotionBaseCheckReadsTheAncestry` pins the skip's ancestry precondition
over a merge ref built as GitHub builds it (dev's tip an ancestor of the head
passes; a head cut from before dev moved is refused naming dev's tip; dev to
main has no precondition; a one-parent checkout is refused; a depth-2 checkout
is refused naming the fetch, and passes after it);
`TestDevRunReadsTheEventRefAndBranch` pins the dev-run shape against its own
(main's events, another branch's queue, a dev-prefixed branch's queue,
sprint/foundation's push, a pull request's merge ref, a local run off dev);
`TestMainRunReadsTheEventRefAndBranch` pins the main-run shape
against its own (the same events on dev, a pull request's merge ref, a
merge-queue group, a local run off main or detached);
`TestExcuseDevDeletionsReadsHistoryAndDevTree` pins the filter; and
`TestMainRunSeesWhatMainAloneHad`, `TestMainRunExcusesOnlyWhatDevDeleted`,
`TestMainRunHoldsItsTreeControls`, `TestMainRunExcusesOnlyDevsHistory`,
`TestMainRunFailsClosedOnAShallowAncestry` and
`TestMainRunNamesTheFetchWhenDevHasMovedOn` (a depth-2 checkout whose
`origin/dev` has moved past the second parent is red naming the fetch, and
green after it) are the witnesses over a
repository they build once per package (a base with the ledger; main
adds a file of its own; dev deletes one file with a row, deletes another and
restores it, trims the rows, adds a file): the merge whose tree is dev's is
red for main's own file only, under push and under no environment on main;
the true promotion is green on main and red on a feature branch; a merge
missing the restored file is red for it (control 1); a merge missing dev's
new file is red for it (control 2); a squash on main is red as everywhere; a
depth-2 clone is red for the shallow history itself, naming the fetch, beside
the readable findings (control 3); a feature branch off the base that deletes
a test, merged into main, is red for the test and for the second parent
outside dev's history (control 4). `TestDevLandingExcusesWhatFoundationDeleted`,
`TestDevLandingHoldsItsTreeControls`, `TestDevLandingExcusesOnlyFoundationsHistory`
and `TestDevLandingFailsClosedOnAnUnreadableHistory` are the same witnesses one
branch down, over a repository of sprint/foundation's shape (foundation deletes
one file with a row, deletes another and restores it, trims the rows, adds a
file; dev adds a file of its own): the landed promotion, a merge commit whose
second parent is foundation's tip, is green under a merge-queue group, a push
to dev and no environment on dev, and red as a squash of the same tree, for
the file foundation deleted; red off dev (a pull request's merge ref, a feature
branch, main's queue); a merge missing a file foundation deleted and restored
(control 1), a file dev never had (control 2) or a file foundation never had
(dev's own) is red for it; a feature branch merged into dev is compared as
everywhere; and a checkout with no `origin/sprint/foundation`, or a depth-2
one, is red naming the fetch, green after it (control 3).
`TestMainRunNeverPassesOnAShallowAncestry`
is the counterexample that shape must refuse: a merge whose tree is main's
tip, losing dev's new test, has an empty first-parent diff, and a depth-2
clone of it is red twice, for the shallow history and for the test against
the second parent's tree. Every
workflow checks out with `fetch-depth: 2` so the first parent is in every
checkout;
a checkout without it is a red run naming the fetch depth, never a pass.
**Its allowlist.** `internal/ci/testdata/deleted-tests.txt`, a log: a row
is a declaration, not an exception, and it counts only in the change that
adds it, so old rows may be trimmed and trimming weakens nothing. The file
merges by union (`merge=union` in `.gitattributes`) and its rows are an
unordered set, a repeated row counting once, so two changes that each delete a
test file do not conflict on it.
**Its remedy lines.** `<sha> (<subject>) deletes <file>, which its
parent <sha> had, and no row of internal/ci/testdata/deleted-tests.txt added
in the same change declares it: restore the file (git checkout <parent> --
<file>), or, if the deletion is meant, add the row <file> <why> to
internal/ci/testdata/deleted-tests.txt in this change`; `<sha> adds the row
<path> ..., but the change deletes no such file`.
**Its narrowings.** What it catches: a file the merge removes from dev's
tree with nothing in the change saying so — the stale-base shape, whether the
list of rules went with it or not. What it does not catch: a file emptied of
its tests but left on disk (`internal/docs`' index test holds a `Test…` name
SPEC-CI names; a control with no SPEC entry, nothing does); a deletion
declared with a row, however wrong the why (a reader's eye); a squash that
reverts live code, docs or a Lua function without deleting a guarded file
(the `silent` rule holds its own live path, nothing holds the rest); a
rename git's default similarity (50%) does not see, which reads as a
deletion and wants a row; a list under a subdirectory of `testdata`, or a
fixture that is not `_test.go`; and one merge only — HEAD against its first
parent, never the merges before it, so a deletion that landed before this
rule is not found by it. A local run reads the last commit on the branch,
which is the developer's own; a root commit is a red run, not a pass. A
squash-merged promotion has one parent and is compared with main's tip as
everywhere, red for all dev deleted since the last promotion, so the
promotion lands as a merge commit. A push to dev by a bypass actor is never
gated by a required check, so what it deletes enters dev's history without
this rule having run on it, and a main run excuses it all the same.
`origin/dev` is trusted to be dev's; the workflow's own fetch
(`+dev:refs/remotes/origin/dev`) makes it so.

### `tlc` — bounded model evidence

`make tlc` runs one declared `TLC_GROUP` on a Linux bench, using explicit
`TLC_JAR` and `TLC_OUT` paths; it is `tlacheck run` (tools/tlacheck, over
internal/tlc). It downloads nothing, uses at most two TLC workers and two JVM
processors, and caps the whole group at 110 seconds. Each case has an owned
temporary state directory, and TLC runs in a private copy of the models under
`TLC_OUT`, so the error-trace files it writes never land in `tla/`. Expected counterexamples
must name the selected invariant, action or temporal property and return its
expected TLC exit; a timeout, parse failure or unrelated violation is a failure.
Final liveness checking remains enabled.

`tla/CASES.tsv` declares every MC configuration, instance module, expected result,
property, deadlock policy, execution group, gate and debt. Layer one's required
gate covers MemberTable, EpochMemberTable, TableEdit, TableOrder, TableSession,
TableFirstContact, RedisFn and FirstConn, including their negative witnesses.
The epoch fixed-point instance has its own group so it does not spend the main
epoch instance's budget. Other measured cases remain recorded. CardMachine has
explicit failed measurement debt; it is not counted as a passing proof or
silently replaced by a smaller configuration.

`tla/RUNS.tsv` retains each measured module/configuration, generated and distinct
states, elapsed time, result, exit, declared expectation, budget and run mode.
It also records the platform of the bench (`host`: the label `<goos>-<goarch>` the tool
computes, from the closed list `Platforms` in `internal/tlc/records.go`, never a machine's
name), its logical CPU count (`cpus`), the java version, the TLC workers of the case, UTC
start, installed jar hash, an input fingerprint and the count of files under it. The fingerprint covers what
that case's TLC run reads and nothing else: its configuration; the module `CASES.tsv`
names for it and, transitively, every module that one `EXTENDS` or `INSTANCE`s (a name
with no file under `tla/` must be one of the ten modules the TLC jar bundles, the `standardModules` list in
`internal/tlc/inputs.go`, which read nothing from the tree; any other name refuses the case);
the case's own row of `CASES.tsv` under the file's header; and the runner's result files
(`outcome.go`, `plan.go`, `run.go` and `suite.go` of `internal/tlc`: the command line, flags, workers
and timeouts of a run, the reading of TLC's output and the reading of a plan row into the case a run is judged by). The package's other non-test files
are bookkeeping and in no fingerprint, and `TestEveryRunnerFileIsClassified` holds each file
to exactly one of the two lists (`ResultFiles`, `BookkeepingFiles`). It is the SHA-256 over those inputs in path
order, each as its path, a NUL, the hex SHA-256 of its bytes and a newline: no timestamp,
no host and no absolute path. The jar is not an input; the record names it in its own
column, beside the java version and the worker count. `tlacheck inputs --case <config>` prints each input and its hash, the fingerprint
and the count. `tlacheck merge` joins the records of the group runs into the committed
file, refuses a record that is not the fingerprint its case has at that checkout (another
runner, or a model edited since the run) or whose module, expected outcome or property cell is not the plan's for the case, and with `--keep` carries the current records of
the cases no run measured again; `tlacheck groups --stale` names the groups to run again.
Unknown state counts on a failed timeout stay unknown, never zero-state success.
Editing a model, a configuration or a case's row requires refreshing the records of the
cases that read it, and only those; editing a result file of the runner requires refreshing every record, and editing a
bookkeeping file or the standard-module list requires none.

`TestTLCRecordsCoverCurrentModels` checks declaration coverage and current evidence
without executing Java or making a network call. Required models cannot become
bench debt to evade their gate. Only the two named deferred models may retain
failed or missing measurements, and the class lists that debt explicitly; any
existing debt record must still have a current fingerprint and honest provenance.
It holds `internal/tlc`'s own fingerprint of every case, computed from the bytes the
runner was built with, to the one the class computes from the checkout's files, so a
record the runner writes is stale only because an input changed, and it holds the real
tree to the rule that a case reads its own models and the ones they extend and no others.
`TestTLCStaleRecordNamesTheCaseAndItsInputFiles` holds a stale record to being refused by
naming its case and the files that case reads. `TestTLCRecordsAndCasesMustMatchOneToOne`
holds a configuration with no declared case or no record, a record with no configuration,
and a case whose module or extended module cannot be found to being refused by name.
`TestTLCPerCaseFingerprintStalesOnlyTheCasesThatReadWhatChanged` proves, on a fixture of
two models that share a module and a third that shares nothing, that an edit to one model
stales that case only, an edit to the shared module stales the cases that extend it and no
other, an edit to one row of the case plan stales that case only, an edit to the runner
stales every case, and edits to files no case reads stale none.
`TestTLCEveryFileACaseReadsStalesIt` changes each file a case reads, one at a time, and
each change stales the case. `TestTLCRecordHostIsAPlatformLabel` refuses a `host` cell that is not a listed platform label
and a CPU count that is not a count. `TestTLCRecordFileHoldsOneJar` holds the record file to one `jar_sha256`, and `tlacheck merge` refuses a
set of records measured with more than one jar, naming each jar with its record count and the
groups to run again. `TestTLCRecordFreshnessAndCoverageWitnesses` proves failed
records, wrong exits, invalid gate waivers and manual required records refuse, while a
declared failed bench measurement is retained as debt, never PASS.

The scheduled/on-demand `.github/workflows/tlc.yml` derives its required matrix
from the case plan and runs only on self-hosted Linux runners, with two-minute
job timeouts and a 110-second group limit. The repository's `TLC_JAR` variable
names the preinstalled jar; Java and Go must already be on PATH. Logs and
TSV records are uploaded even on failure. This workflow does not accept PR events.
The ordinary change gate checks the committed records; the nightly repeats the
actual model runs. A failing or absent nightly is not evidence of a checked model.

`make tlc-full` is a separate manual bench experiment with an explicitly supplied
`TLC_BUDGET` of at most 3600 seconds. It refuses CI environments. Its full-instance
records carry `mode=manual` and their actual budget; they cannot satisfy a required
bounded gate. No workflow job gets a longer timeout. A smaller model, if introduced,
needs a separate configuration and a stated coverage difference; it cannot replace
the original failed measurement.

### `generality` — no fleet, host, tailnet, friend or person names in living code, contracts, defaults or refusals

**The rule.** No living Go file under `cmd/`, `internal/` or `tools/` (outside `testdata/`, `vendor/`, and `_test.go` files) carries a reference to our fleet machines, hostnames, tailnet nodes, friend or person names, or GitHub accounts (everything must be general; concepts like machine, bench, coordinator, friend, seat, store, route, pool, card, stream, repo, issue, entry are what code knows; fleet specifics belong in configuration or receipts, not in code, contracts, defaults or refusals).
**The mistake it prevents.** Code written with hardcoded machine names, friend identities or private accounts cannot be reused or operated as a general platform, leaks private infrastructure details into public source, and prevents running the tool suite against different fleets or configurations.
**The test.** `TestGeneralityGuardrail` (`internal/ci/generality_class_test.go`), with `TestGeneralityTokenExtraction` for token extraction heuristics and boundary controls, `TestGeneralitySpaceHasNoSyntaxException` for a machine name counted in every syntax position, `TestGeneralityOccurrenceWitness` for proving that adding an occurrence of an allowed token to an already-allowed file fails the check, and `TestGeneralityAllowlistUpdate` for proving allowlist update refuses growth and cleanly writes on shrinking.
**Its allowlist.** the `generality` package ledger, existing occurrences across the living tree, formatted as `path/to/file.go:token count`; sorted, shrink-only with ceiling.
**Its remedy lines.** `remedy="remove the host, tailnet or person name; make the reference general or read it from configuration; docs/SPEC-CI.md#generality"`, and for an unlisted or grown count: `remedy="shrink the allowlist count; the list only shrinks"`.
**Its narrowings.** It scans living `.go` files under `cmd/`, `internal/` and `tools/` only, skipping `testdata/`, `vendor/`, and `_test.go` files. It excludes Go package `import` statements (including `github.com/mas-bandwidth/...` imports) and marked documentation examples in comments (lines with `e.g.` or `example:`). Boundary controls ensure substring words like `miniredis`, `revision`, `deterministic`, `minimum`, `studios`, `whitespace`, and compound words like `TrimSpace` are not matched. A machine name counts wherever it appears in Go syntax: identifiers, struct tags, comments and string literals.

### `tool-standard` — every tool built on internal/tool is held to the standard its definition alone can break

**The rule.** A package that builds a `tool.Tool` has a test that calls its `Problems()` and fails on each: every verb's effect is inspection, local write or delivery (optionally with a clause), and the how text is at most five lines of at most 100 characters. The rest of the banner standard (the what line, usage, exit codes, the example block last, `-h` per verb) holds by construction in `internal/tool`.
**The mistake it prevents.** The first two tools on the skeleton shipped how texts of 11 and 12 lines, and `help <verb>` printed `effect: unstated`, while every test was green: nothing checked what only the definition says.
**The test.** `TestEveryToolDefinitionIsHeldToTheStandard` (`internal/ci/toolstandard_class_test.go`); `Problems()` itself is pinned by `internal/tool` `TestProblems`.
**Its allowlist.** None.
**Its remedy line.** `remedy="add a test that fails on each of its Problems() (as cmd/nova-cairn TestCairnToolMeetsTheStandard)"`.
**Its narrowings.** It reads non-test `.go` files under `cmd/` and `internal/` for a `tool.Tool{` literal, outside `testdata/` and `internal/tool` itself, and asks that a `_test.go` file in the same directory call `.Problems()`.

### `generality-text` — the same rule over every living text file that is not Go

**The rule.** The `generality` rule binds the whole tree, not only `.go` files: no living text file carries a machine, host, friend or person name, a tailnet address, or a home path that names a user. A fleet's store address, its coordinator seat, its user names and its home paths belong in its own configuration and receipts, never in a shipped `fleet/*.tsv`, `*.yml`, `templates/*.j2`, workflow, script, Lua function or document; a doc example uses a generic name or a placeholder.
**The mistake it prevents.** A scan that read only `.go` files let one fleet's tailnet address, coordinator seat, user names and home paths ride in through files the scan never opened.
**The test.** `TestGeneralityText` (`internal/ci/generality_text_class_test.go`), with `TestGeneralityTextFindings` for what a line's findings are, `TestGeneralityTextScope` for which files are read, `TestGeneralityTextContactDoc` for the contact addresses that pass in `docs/SECURITY.md` only, and `TestGeneralityTextWitness` for the reversed witnesses (a new finding in a `.yml`, `.tsv`, `.j2`, `.md`, `.lua`, `.sh`, Makefile or workflow fails; a second occurrence in a listed file fails; a fixture row needs a reason and a finding).
**What is found.** The name inventory of `generality_class_test.go` (no name is added by this test), and three patterns: `tailnet-address` (an IPv4 address in `100.64.0.0/10`, an IPv6 address under the tailnet prefix, a `.ts.net` hostname; the two ranges written as CIDRs are the concept and pass), `home-path` (`/Users/<name>`, `/home/<name>`, `C:\Users\<name>` whose name is not a generic one: a documented placeholder, a container user this repository defines, a hosted runner's), and any reference to the organisation's account other than the project's own public links, which are its identity and not fleet names (a documented pattern, not list rows): the repository's own path (the module path and issue references), the seed repository it grows from, the secrets store design's repository, (each anchored on the left: the start of a line, a character that cannot continue a path, or a URL prefix, so a longer path that merely ends in one does not pass), and the project's two published contact addresses, those exact addresses and no other local part, in `docs/SECURITY.md` only.
**Its lists.** `internal/ci/testdata/generality_text_fixtures_allowlist.txt`, `path reason`, whole files that are recorded data (captured output, a verbatim excerpt of a real record, a recorded reply of a public repository), a reason on every row; and the `generality-text` package ledger, `path:token count`, the debt that existed when the scan was widened. Both only shrink: an unlisted finding, a rising count, a falling count and a stale row each fail, and `NOVA_CI_UPDATE=1` removes rows and never adds one.
**Its narrowings.** The scan reads the files the shared walk finds with a suffix of `.lua .tsv .yml .yaml .j2 .md .sh .json .txt .ini .tmpl .tla .lisp .sexp .cfg .card .sql .py .ps1 .jsonl .log .notes`, and the files named `Makefile` and `Containerfile`; `.go` files are the other test's, and `.git` is never read. There is no marked-example exemption: a doc example is written with a generic name. A `[:space:]` character class is syntax and not a finding.

### `remedy` — every refusal in the tools names its next step

**The rule.** The owner's rule for every tool: "they should never fail silently, and they should always provide helpful breadcrumbs how to fix anything going wrong." Every refusal site in a non-test `.go` file under `cmd/` prints a remedy in the house form: `run: <command>` (a malformed invocation's is `<tool> <verb> -h`), `remedy=` on an event line, `wants <x>`, `see <where>`, `rerun`, or an imperative naming the flag, the file or the value (`pass --overwrite`, `give 1 to 64`). The forms are one list, `oneline.HasRemedy`, which the rule and the printers share; a printer ends a line that names none with `oneline.WithRemedy(what, next)`.
**The mistake it prevents.** A refusal that says what is wrong and nothing about what to do: `DRAFT REFUSED: write <path>: permission denied`, `nova-sandbox version` exiting 0 over a flag it never read, `SECRETS EXEC FAIL invocation: missing '--' delimiter` with no pointer to the form.
**The test.** `TestEveryRefusalCarriesARemedy` (`internal/ci/remedy_class_test.go`), with the three sites proved over source in `TestRemedyRuleReadsTheThreeSites`. It finds a refusal three ways: a call to the package's refusal printer (a function with an `io.Writer` parameter whose name holds `refus`; it is clear when the printer prints a remedy itself, directly or through another printer, or when its arguments do); a `fmt` print whose text holds `REFUSED`, `refused`, `refusing` or `cannot`; and an exit-2 path (`return 2`, `os.Exit(2)`), read with the prints just before it in its block. The runtime half is `testverbhelp.RefusalProblems`, run by every tool's verb-help test: each verb handed `--no-such-flag-breadcrumb` exits non-zero, says something on stderr that names the flag, the usage or a remedy, closes no stdout line with OK, and writes nothing.
**Its allowlist.** the `remedy` package ledger, `file:function:kind <sites> <why>`, kind `refuse-call`, `refuse-print` or `exit-2`; `<sites>` is how many unremedied sites the row covers. Shrink-only by site and by row: a new site under a listed key makes the measured count exceed the row's and the run is red; a fixed site leaves the count too high and the run is red until it is lowered (`NOVA_CI_UPDATE=1` lowers a count and drops a row with no site left, and never raises a count or adds a row); the row count has a `# ceiling:`; a row with no reason is red.
**Its remedy line.** `` <file:function:kind> prints no remedy (<text>); end the line with `; run: <command>` (or `remedy:`, `wants <x>`, `see <tool> help <verb>`) so the reader knows the next step``; for a row whose count is exceeded, `<ledger> lists <key> at <n> sites, but <m> are there now (<the sites>)`; for a row whose count is too high, `lower the row's count to <m>`; for a stale row, `delete the stale row (the list only shrinks; NOVA_CI_UPDATE=1 drops it)`. A remedy is what `oneline.HasRemedy` reads: `run:`, `remedy:`, `fix:`, `wants`, a help pointer (`nova-<tool> help <verb>`), an imperative that names a flag, or `see`/`retry`/`try`/`want` followed by a flag or a command in backticks; the bare words `see`, `want`, `retry` and `help` in a sentence are not one.
**Its narrowings.** The text read is the call's string literals and the package's string constants: a remedy carried inside an error's own text (redisconn's `next:` step) is not seen, and a line built into a variable before it is printed is read as no line. An exit 2 inside `if <check>(..., stderr) { return 2 }`, after `fs.Parse`, or on a flag an earlier printed refusal set (`bad = true`, `if bad { return 2 }`) is judged where the line was printed. A print that says `usage` or runs to three lines is help, not a refusal. Fixtures under `testdata/` are not read.

### `no-ok-on-failure` — a failed run's last word is never OK

**The rule.** An OK line is never followed by a non-zero exit, and a FAIL or REFUSED line never by exit 0 (docs/CLI-STYLE.md (e): the closing status of a failed run is FAIL). A line printed OK on every outcome and then an exit code carried in a variable is the same mistake.
**The mistake it prevents.** `QUICKSTART OK done=2 worst-exit=1` over two failed checks: a reader who stops at the word reads a pass.
**The test.** `TestNoOKOnFailure` (`internal/ci/okonfailure_class_test.go`), with `TestOKOnFailureRuleReadsBothWays` over source. In every block of a non-test `.go` file under `cmd/` it pairs each `return <n>` or `os.Exit(<n>)` with the `fmt` prints just before it: the word that counts is the last status line the run printed (the last line holding an OK event word, or a FAIL or REFUSED word): an OK event line (upper-case tokens, then `OK`) then a non-zero literal is `ok-nonzero`, a FAIL or REFUSED line then 0 is `fail-zero`, and an OK line then `return code` (a variable named for an exit) is `ok-carried`. An OK printed after a REFUSED, then `return 2`, is `ok-nonzero`. The runtime half is `testverbhelp.RefusalProblems` (see `remedy`).
**Its allowlist.** the `okonfailure` package ledger, `file:function:kind <sites> <why>`, shrink-only by site and by row as in `remedy`.
**Its remedy line.** `prints "<line>", then exits <n>; a failed run's last word is FAIL or REFUSED, never OK`, and for `ok-carried`, `print OK only when <code> is 0, FAIL otherwise`.
**Its narrowings.** Only prints that stand directly before the exit in the same block are read, and only literal exit codes (or, for `ok-carried`, a variable named `code`, `worst`, `exit`, `rc`, `status` or `result`); an OK printed by a helper, or a code decided far from its line, is not seen.

### `discarded` — no error thrown away without its reason

**The rule.** In every non-test `.go` file under `cmd/`, `internal/` and `tools/`, three shapes carry `// ignored: <reason>` on their line or the line above: `_ = <call>` and `_, _ = <call>` (every left side blank) and `_ = err`; `if err != nil { ... return }` with a bare return and no read of `err`; and `err = nil`. The reason is real (a best-effort cleanup whose failure path returns the error that matters, a close after a read that already succeeded, a kill of a process that may already be gone); a failure that hides is fixed instead: returned, or printed as one line with its remedy.
**The mistake it prevents.** A staged checkout whose origin still named the mirror; a secrets invariant that skipped a file it could not read and passed over it; a decision-log row and a usage row lost with no line.
**The test.** `TestNoErrorIsDiscarded` (`internal/ci/discarded_class_test.go`), with `TestDiscardedRuleReadsTheThreeShapes` over source and `TestSiteLedgerShrinksBySiteNotByRow` for the counted ledger the four never-silent rules (`remedy`, `no-ok-on-failure`, `discarded`, `script-hide`) share, and `TestSiteLedgerNamesReasonlessShard` for a missing reason that names its exact shard and stops the update.
**Its allowlist.** the `discarded` package ledger, `file:function:shape <sites> <why>`, shape `blank`, `bare-return` or `err-nil`; each row a failure that goes silent today and wants more than a comment (a writer the function does not have, a caller that drops what it returns, a design call). Shrink-only by site and by row as in `remedy`: a discard with no `// ignored:` under a listed function raises the function's count and the run is red, so a site leaves the ledger by gaining its reason or its fix, and then its count is lowered. A row with no reason is red.
**Its remedy line.** `the call's error is dropped; return it, print it as one line with its remedy, or say why it is safe with `// ignored: <reason>` on this line or the one above` (and the same for the other two shapes).
**Its narrowings.** The rule reads syntax, not types: `_ = <call>` is read whatever the call returns, except a builtin, a conversion and a flag definition (`fs.String(name, value, usage)`, which returns a pointer); a call whose error is dropped as a bare statement (`f.Close()`, `defer f.Close()`) is not read. A comment in `internal/tlc/suite.go` restales every TLC record, so that site waits in the ledger for the next TLC run.

### `script-hide` — no script or play hides a failure without its reason

**The rule.** Every line of a script or a play under `fleet/`, `scripts/`, `infra/` and `tools/` (`.sh`, `.yml`, `.yaml`, `.j2`, `.ps1`; `*_test.sh` and `testdata/` aside) that forces an exit to success (`|| true`), sends an error stream nowhere (`2>/dev/null`, `&>/dev/null`, `>/dev/null 2>&1`, `2>$null`), or makes a task unable to fail (`failed_when: false`, `ignore_errors: true`) carries `# ignored: <reason>` on the line or the line above. `command -v x >/dev/null 2>&1` asks a question whose answer is the exit and is not read.
**The mistake it prevents.** A bench check that runs `nova-secrets check >/dev/null 2>&1` and prints its own line, dropping the check's reason and remedy; a sweep whose `rm -rf` errors go nowhere while the disk stays full; a formatter whose parse errors read as formatted.
**The test.** `TestNoScriptHidesAFailure` (`internal/ci/discarded_class_test.go`), with `TestScriptHideRuleReadsTheShapes` for the line reader.
**Its allowlist.** the `scripthide` package ledger, `file:shape <sites> <why>`, shape `or-true`, `stderr-null`, `failed-when-false` or `ignore-errors`; the reason names the lines that hide a failure the operator should see. `fleet/` is being reworked, so its rows wait for that. Shrink-only by site and by row as in `remedy`: a new `|| true` or `2>/dev/null` in a listed file raises that file's count for the shape and the run is red, and a line leaves the ledger by gaining its `# ignored: <reason>` or its fix, after which the row's count is lowered.
**Its remedy line.** `"<line>" throws a failure away; let it show, or say why it is safe with `# ignored: <reason>` on this line or the one above`.
**Its narrowings.** Keyed by file and shape and counted by line: each unreasoned line of a listed shape in a listed file is a site. A Go string literal holding a script (`tools/functionalrun`) is the `silent` rule's, on the live path only.

### `deadcode` — no unreachable functions from production roots

**The rule.** Production reachability is analyzed by `deadcode` from the `cmd/` mains as roots (`./cmd/...`), without `-test` (code reached only by tests is the next contraction's target). The analysis runs across three operating systems (GOOS `linux`, `darwin`, `windows`) and holds the union of dead functions to a per-package shrink-only ledger.
**The mistake it prevents.** Unused, unreachable functions and methods accumulating across the codebase; the maintainer's contraction-phase directive ("dead code to zero with a class test holding it").
**The test.** `TestDeadCode` (`internal/ci/dead_code_class_test.go`), with its allowlist mechanics witness `TestDeadCodeWitness`. Runs in the functional tier behind `//go:build functional`.
**Its allowlist.** `internal/ci/testdata/dead_code_allowlist.txt`, the shrink-only per-package ledger (`<package> <count>`); `NOVA_CI_UPDATE=1 go test -tags functional -run '^TestDeadCode$' ./internal/ci/` lowers counts and drops zero-count rows (the rule is functional-tier only, so `NOVA_CI_UPDATE=1 make test PKGS=./internal/ci` never reaches it, and the functional container mounts the source read-only). The list refuses to grow or raise any count.
**Its remedy line.** `remedy="delete the unreachable function(s) or wire them into cmd/...; the dead code ledger only shrinks and refuses to raise counts or add rows"`.
**Its narrowings.** Analyzes static reachability from main executables in `cmd/...` without `-test` flags using `golang.org/x/tools/cmd/deadcode` across `linux`, `darwin`, and `windows`.

### `fleet-plays` — the fleet plays read only the inventory and work through the Go tools

**The rule.** The plays under `fleet/` that converge a fleet (`tools.yml`, `redis.yml`, `loops.yml`) and their templates read their values from the inventory `nova-config inventory` prints and from `fleet/group_vars/all.yml`, and do their work through the Go tools: no task uses ansible's `shell`, `script` or `raw`; every play runs on a group the inventory prints, or `localhost`; every command task says how its change is read (`changed_when`); every template a task names exists and every template is rendered by a task; every `nova_*` a play or template reads is defined by group_vars, by the inventory (a host variable or `all.vars`), by a `set_fact`, or named by an `assert` as the operator's `-e`; every `loop_*` a template reads is a variable of its play or task, every `l.<field>` a field of the inventory's loop record, every `ansible_*` a fact the plays gather; and `fleet/retired-tools.txt` names no tool `cmd/` ships.
**The mistake it prevents.** A template reading a variable its play renamed, which ansible finds only on a machine; a play that hides its work in a shell line; a retirement list that removes a tool the same run installs.
**The test.** `TestFleetPlaysReadOnlyTheInventory` (`internal/ci/fleetplays_class_test.go`), which also renders both fixtures' inventories through `config.LoadFixture` and `config.BuildInventory` and asserts the groups and the typed loop records, with `TestFleetPlaysRuleReadsTheShapes`, which plants each offence in a copy of the real source. The functional half, `TestFleetPlaysPassSyntaxAndCheckOnTheFixture` (`internal/ci/fleetplays_functional_test.go`), runs the three plays with `--syntax-check` and `--check --diff` against `fleet/testdata/check-fixture.yml` and asserts the rendered units; it skips where `ansible-playbook` is not installed.
**Its allowlist.** None.
**Its remedy line.** each finding names the play or template, the task and what it reads or runs.
**Its narrowings.** Variables are found by their prefixes (`nova_`, `loop_`, `ansible_`, `l.`) in the plays' uncommented text and the templates' Jinja blocks; a variable of another spelling is not read.

### `sprint-tables-locked` — the four sprint tables change only with their lock file

**The rule.** `internal/sprint/TABLES.lock` pins the work, readers, merge and fleet tables as `schema.go` defines them (one line per column: `table.column projection fold hidden`, with `label=` where the column has a header label, in table order) and the order the sprint view shows the tables in. A PR that changes any table's shape turns the test red until the lock file changes in the same PR, where a read sees it.
**The mistake it prevents.** A `provider` column added to the fleet table that nobody asked for; the installed build fails every tick on the real store. The maintainer, 2026-10-01: "i never want new things unless i ask for them" and "i dislike this drift from the design of nova sprint tables that is *complete and locked*."
**The test.** `TestSprintTablesAreLocked` (`internal/ci/sprint_tables_lock_class_test.go`): renders the lock's text from `sprint.Names{}.Definitions()` and `sprint.ViewOrder` and compares it, line by line, to the lock file's lines (comments and blanks aside).
**Its allowlist.** None.
**Its remedy line.** `schema.go no longer matches internal/sprint/TABLES.lock; a PR that changes a table's shape changes the lock file in the same PR, where a read sees it`, then each differing line, the lock's and the schema's.
**Its narrowings.** Column width (always 0 here) is not in the lock; a change to what a table holds that is not in its definition (a card's fields, a hidden column's contents) is not seen.

### `onewriter` — a worker is a client and does not open the store

**The rule.** One process writes a sprint's state: the run loop, beside the store, which is also the sprint's server (`nova-sprint run --listen`). A worker sends its verbs to it and reads its replies. The packages a worker's machine runs (`cmd/nova-swarm`, `internal/member`, `internal/sprintwire`) import, directly or through any package of this module, none of the packages that open the store (`internal/sprint/store`, `internal/redisconn`, `internal/ntable`, `internal/nsprint/store`, any `github.com/redis/` module).
**The mistake it prevents.** A distributed system where a client and a server would do. nova-sprint's workers each read the tables across the network, plan and write back behind one fence; from 108 ms away a write loses it for about 50 s and gives up, and a finished card takes a median 391 s to be reported. A lock, a reservation and a queue with four recovery rules each add states before the simple shape is seen. The maintainer, 2026-10-01: "never write a complicated distributed system when a simple client/server will work just fine." / "simple client/server always wins."
**The test.** `TestAWorkerDoesNotOpenTheStore` (`internal/ci/onewriter_class_test.go`), with `TestOneWriterFindsAChainToTheStore`: it walks the imports of the non-test files from each worker package through the module's own packages and is red on the first chain that reaches the store, printing the chain.
**Its allowlist.** None.
**Its remedy line.** `remedy="a worker is a client: ask the sprint's server (internal/sprintwire) and never open the store from a worker's machine (docs/SPEC-CI.md, onewriter)"`.
**Its narrowings.** It reads imports, so a worker that reaches the store by running a binary that opens it would not be seen (the member has no such path: `--server` is required); test files are not read.

### `flag-usage` — every flag a tool registers says what it wants

**The rule.** A flag is registered with a description that says what it wants (its unit, its role, an example value): `<tool> <verb> -h` is all an AI reads before it calls the verb. On `internal/tool`, `Problems()` names every flag without one, so `tool-standard` holds it there by construction.
**The mistake it prevents.** `-h` listing `--repo <string>` and nothing else: 31 flags of one tool, 8 of another, read cold by raters who could not tell what the flag wanted (tool ledger X7).
**The test.** `TestEveryFlagSaysWhatItWants` (`internal/ci/flagusage_class_test.go`), with `TestFlagUsageRuleReadsEveryShape`: every non-test `.go` file under `cmd/` and `internal/` is read for a call shaped like a registration of package flag (`String`, `StringVar`, `Var`, `Func` and the rest, by arity) whose usage is an empty string literal.
**Its allowlist.** the `flagusage` package ledger, `file:function <sites> <why>`, counted and shrink-only like `remedy`.
**Its remedy line.** `a flag registered with no description; give it a usage string that says what it wants ...`.
**Its narrowings.** A usage built at run time (a variable, a concatenation) is not read; `testdata/` is not read.

### `tool-answers` — every tool answers a mistake with the way forward

**The rule.** Run as an AI would run it wrongly, every tool answers with the next step: a bare command prints the `REFUSED` word; an unknown verb is refused at exit 2 in one line naming the tool's verbs; an unknown flag is refused naming the verb's flags (never the flag package's `flag provided but not defined`); a verb group's `-h` lists its verbs on stdout at exit 0; every verb's `-h` states its effect, and a verb that writes takes `--dry-run`. On `internal/tool` each holds by construction (`tool.FlagRefusal` is the unknown-flag answer for a tool not on it).
**The mistake it prevents.** Fourteen tools answered a misspelled flag with Go's stock line and the tool-wide help, eleven answered an unknown verb without the verbs, fourteen bare commands printed no status word, two groups refused `-h`, and seven tools wrote with no dry run (tool ledger X2, X3, X4, X11, X12).
**The test.** The functional walk `TestEveryCommandMeetsTheOnboardingStandard` measures it on the binaries it builds (`internal/ci/toolanswers_functional_test.go`); the judges are proved in the unit tier by `TestToolAnswersJudges` (`internal/ci/toolanswers_class_test.go`). The ledger is checked only when every tool ran.
**Its allowlist.** the `toolanswers` package ledger, one shard per tool, `cmd/<tool>:<kind> <count> <why>`, kind `bare`, `unknown-verb`, `unknown-flag`, `group-help` or `dry-run`; the count is the verbs or groups short of the rule. Counted and shrink-only.
**Its remedy line.** Each site names its kind's remedy after `to clear it:`; moving the tool onto `internal/tool` clears every kind but `dry-run`, which clears verb by verb with `Verb.DryRun` and `Call.DryRun`.
**Its narrowings.** The unknown flag is tried on one verb per tool (the first whose `-h` lists a flag): a tool parses every verb through one seam. A verb's effect is read from its `-h`, so a tool not on `internal/tool` meets `dry-run` only where a verb lists `--dry-run`.

## How the class tests read the tree: one walk, one parse, in parallel

Every rule above is a sweep of this repository's own source. A rule that pays
for its own sweep — `filepath.WalkDir` over `cmd/` and `internal/`,
`os.ReadFile` on each of the `.go` files, and `go/parser` over each of them
again — multiplies the same walk and parse by the number of rules, in a package
the CL tier holds to a two-minute budget.

**One walk, one parse, per test process.** `internal/ci/tree_test.go` holds the
shared tree: a `sync.Once` walks the repository root exactly once, reads the
bytes of every `.go` file and of everything under `.github/`, and parses the
`.go` files into ONE `token.FileSet`. `repoTree(t)` hands every rule the same
index; `.git` is never descended into. The contract is pinned by
`TestSharedRepoTreeListsAndParsesTheRepository` and
`TestSharedRepoTreeSkipsTheGitDirectory` — the tree is this repository and not
empty, every `.go` file it lists carries a usable syntax tree, and the loader
runs exactly once however many callers ask for it. A cache that reloads is a
walk with extra bookkeeping.

**The rules are READ-ONLY over the tree.** No test in this package writes a file
the walk can see, so nothing invalidates the cache and a second walk buys
nothing. That is also why the rules are safe to run concurrently: each
class test carries `t.Parallel()` and reads the shared tree and its own
`testdata/` allowlist, and writes only to its own `t.TempDir()` -- or, under
`NOVA_CI_UPDATE=1`, to its own list, which no walk reads. A test that
chdirs, sets an environment variable, or writes shared state does NOT get
`t.Parallel()`, and the shared tree is not a licence to add one.

**Each rule filters the tree itself.** It does not ask for a pre-filtered list,
because the filters differ in ways that decide whether a rule holds: the
wall-clock budget rule reads `testdata` (it holds its own fixtures to the law),
the shared-temp and output-path rules skip it (they would otherwise find the
offenders they plant), and the build-tag rule skips `vendor` and `node_modules`
as well. Skipping the wrong one is the difference between a rule that holds and
a rule that passes by checking nothing.

**On parse mode.** The tree is parsed with mode `0`, because every rule that
reads it walks declarations and expressions and none of them reads a comment.
The production checkers that DO want comments — `CheckNet`, which reads
`// net-ok:` reasons — take a caller-supplied root, are not tests, and keep
their own walk. A rule here that ever needs comments caches a SECOND variant
keyed by `parser.Mode` rather than widening this one, so that changing the mode
can never quietly change what an existing rule sees.
