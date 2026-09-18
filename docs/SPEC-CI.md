# SPEC-CI — the class tests that read this repository's own CI path

This specification holds the class tests that guard the CI path by reading the
repository's own test files as text. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated, and beside [SPEC-TEST.md](SPEC-TEST.md),
which owns the verbs that run the local and CI suites. Related: nova-tools #1142,
and the existing budget law `TestNoTestAssertsAWallClockBoundUnderTenSeconds`.

## The CI class test against fixed waits on the CI path

**The help line.** The class test is entered in the CI check roster and in help
as the verb `waits`:

```
waits   read every _test.go on the CI path; refuse a fixed wall-clock wait or bound
```

It runs as `go test ./internal/ci -run TestNoFixedWaitsOnTheCIPath`, and it is
the bench's fixed-wait audit made an official verb (#1142): the script every
card used to sketch by hand is now the check a PR runs.

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

**The mistake it removes.** A card cut waits to make a suite fit the two-minute
gate, which made `TestTheLaunchIsATransaction` flaky under load and dropped three
innocent PRs from the queue in one hour — the same class hit twice in one night.

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

## The CI class test against unquoted paths in JSON and template literals

**The help line.** The class test is entered in the CI check roster and in help
as the verb `templates`:

```
templates   read every _test.go; refuse a filesystem path unquoted in a JSON or template literal
```

It runs as `go test ./internal/ci -run TestNoUnquotedPathsInTemplateLiterals`,
and it is the Windows-path audit made an official verb (#1142): the grep every
card ran by eye is now the check a PR runs.

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

**The mistake it removes.** Two Windows-only reds (#904, #920) came from a
`filepath.Join(dir, "key")` concatenated raw into a JSON worker description, so
`C:\...\key` reached the literal unquoted and the parse failed where Linux and
darwin never saw it.

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
then exits 2; the lines go to stdout, so one `CI-SLOW` grep reads the whole run.
The stream is one `go test -json` line per event, parsed by `encoding/json`; a
line that is not a TestEvent is a refusal naming its line number, never a silent
skip, so a truncated pipe cannot read as a clean run.

**Its refusals (exit 2, one remedy line each).** A malformed line —
`remedy="stdin is not newline-delimited go test -json"`. A `--budget` of zero or
less — `remedy="--budget must be a whole number of seconds greater than zero"`. A
missing or unreadable invocation is the tool’s own one-line refusal ending `run:
nova-ci help`.

**The budget is per platform.** The verb judges a LIVE run against the
`--budget` it is handed; `docs/TEST-DURATIONS.md` is the recorded half, and
since the record grew a `## Bench:` section per machine the ceiling a package is
judged against there is ITS OWN PLATFORM'S. A section may state a
`budget-factor:` in its heading -- what the same suite costs on that platform
relative to the budget bench -- and its rows are judged at `60 s x factor` when
the record's own check in `tools/testdur` is running on that
`runtime.GOOS/GOARCH`. darwin/arm64's factor is 2.2, the whole-suite ratio
measured on the Air (#1411). The `[budget]` bench's rows are judged at a plain
sixty everywhere, and a platform with no section of its own falls back to that,
so a platform is never silently unbudgeted. A `budget-factor:` that is not a
positive number is a refusal and never a silent fallback, because a ceiling
quietly set to the wrong number is worse than no ceiling: no ceiling at least
reads as no ceiling. `tools/testdur` heads each table it prints with the
platform it measured, so a regenerated table cannot be pasted under another
bench's heading, and a bench's package table is the one directly under its
heading -- a table under a `###` inside the section is prose, not a second
measurement of the same packages.

**The mistake it removes.** `nova-secrets` sat at 120 seconds in the suite and
nothing noticed, because nothing summed the per-package elapsed time `go test
-json` was already printing. A green that hides a doubling suite is the same
mistake as a flaky wait, one layer up. The same mistake one layer out is a
number RECORDED and enforced against nothing: `cmd/nova-wake` was recorded at
62.9 s on the Air -- over a minute -- and until the per-platform ceiling above,
nothing read it.

**Red tests.** `internal/ci/slowtests/slowtests_test.go` feeds canned TestEvent
lines through the parser and the summer, and `cmd/nova-ci/main_test.go` runs the
verb end to end:

1. A package at `3.2s` under a `60s` budget is `CI-SLOW OK packages=1
   slowest=example.com/pkg:3.2s`, exit 0.
2. A package summing `75.3s` with tests at `3.2s` and `2.9s` is one line naming
   the package, its total, the budget and `slowest=TestA:3.2s,TestB:2.9s`, exit
   2.
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
it is Glenn's hard rule of 2026-09-17 — *unit tests test LOGIC, not the network*
— made an official verb: every endpoint is mocked locally, and only the soak,
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

**What `goenv.Clean` drops.** `GOFLAGS`, every `GOTEST*` variable, and any other
`GO`-prefixed variable whose value carries a `-json` or `--json` flag.
`GOTMPDIR` is deliberately kept: it names a location, not an output shape, and a
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

**The mistake it removes.** A tool that reads the output of a `go` command it
started is reading a shape the caller can change. CI's `make test` exports
`GOFLAGS=-json`; on 2026-09-18 the inner `go test` of `nova-review mutate`
inherited it, answered in JSON with no `--- PASS:` line in it, and the parser
counted the unit that stayed green as red: `MUTATE <sha> red=1 green=1 PASS`
was reported as `red=2 green=0`, and three legs of integration-4 (#1332) failed
on a tool that was working.

**Red tests.**

1. The pre-fix `runUnits` of `nova-review mutate` is refused with its file, its
   line, its function and the remedy.
2. The fixed shape is allowed, both spellings: `goenv.Clean(os.Environ())` and
   an `append` onto it for the tool's own variables.
3. `cmd.Env = append(os.Environ(), ...)` is still refused — the environment is
   set, but it is the caller's, so `GOFLAGS` travels — and a non-`go` command in
   the same file is not this rule's business.
4. A row that names no offender is refused, so the list only shrinks.
5. A row holds one offender of its kind in its file wherever it now stands.
6. The rule holds over this repository with an empty allowlist.

## The class tests

The five sections above are the class tests written out in full. This section is
the **index**: one entry for every class test this repository runs, so a friend
meeting a red for the first time can read the rule, the hurt that bought it and
the one thing to do, without reading the test. The long sections stay; an entry
here that has one points at it.

**What makes a test a class test.** It reads this repository's own text — the
`.go` files, `.github/workflows/*.yml`, the `Makefile`, `docs/` — and refuses a
SHAPE wherever it stands, rather than exercising one function. It is the fix for
a whole class made mechanical, which is the only kind of fix that survives the
next card: a rule lands with its sweep of the tree, or it does not land
([pit-stop ledger item 20](../reports/pitstop-tests-2026-09-17.md)).

**The marker.** A class test is a `Test` function in `internal/ci` that is
either declared in a `*_class_test.go` file or named with one of the quantifier
prefixes `TestNo…`, `TestEvery…` or `TestOnly…` — the name says the rule holds over
the whole tree, which is what a class test is. **Every such function must be
named by an entry in this section, and every `Test…` name this section prints
must exist under `internal/ci`.** Both halves are held by
`internal/docs/spec_ci_index_test.go`, so the index cannot rot: a new class test
with no entry is red, and an entry naming a test that was renamed or deleted is
red. The neighbouring tests in this package that pin the workflow's shape rather
than a class — the CL-tier budget, the integration-branch list, the hosted merge
legs, `TestFleetProbeRunsTheNetworkProbeInsideNovaSandbox` — carry no marker and
are not indexed here; they are read from `internal/ci` directly.

**The shared conventions.** Every class test that carries exceptions keeps them
in one shrink-only file under `internal/ci/testdata/`, checked in BOTH
directions: an offender that is not listed is red, and a listed entry that names
no offender is also red, so fixing a site means deleting its row in the same
change and a new row parks nothing. The newer lists are matched by **file and
kind, never by line**, so a merge that shifts lines in a listed file does not
turn dev red. And every entry below names its **narrowings** — the false
negatives the heuristic accepts on purpose — because a class test with false
positives is one people learn to edit around, and a narrowing nobody wrote down
is read as coverage.

### `waits` — no fixed wall-clock wait on the CI path

**The rule.** No `_test.go` on the CL path carries a `time.Sleep` over 100 ms, a
context or timer bound under ten seconds used as a pass/fail condition, or an
assertion on elapsed time; it polls for the event up to `NOVA_TEST_WAIT`
(default `30s`) or injects a fake clock.
**The hurt.** A card cut waits to fit the two-minute gate, `TestTheLaunchIsATransaction`
went flaky under load and dropped three innocent PRs from the queue in one hour;
the same class then refused `#1099` out of a merge group (ledger item 9).
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

### `templates` — no unquoted filesystem path in a JSON or template literal

**The rule.** A path placed inside a JSON or `text/template` string literal is
wrapped in `strconv.Quote` (or `oneline.Quote`); a raw `filepath.Join(...)` or a
`C:\…` literal there is refused.
**The hurt.** Two Windows-only reds, `#904` and `#920`: a
`filepath.Join(dir, "key")` concatenated raw into a JSON worker description put
`C:\…\key` in a literal whose grammar has no such escape, so the parse failed
where Linux and darwin never looked.
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

**The rule.** Glenn's hard rule of 2026-09-17: unit tests test LOGIC. No
`_test.go` on the CL path may name a real host in a URL or a bare `host:port`;
endpoints are `httptest` or a local fake, and fixtures name `example.com`,
`*.invalid` or `*.test`. Only `//go:build nightly` and `//go:build soak` files
may reach the network.
**The hurt.** Ledger item 13 — the 07:08Z merge group of 2026-09-18 was poisoned
three entries deep by `#1267` and `#1074`, whose test FIXTURES carried
`https://github.com/…` literals that never dial. The class read them as dials,
and the honest fix was to generalize `parseRepoSlug` to any origin host and
retarget the fixtures, not to split the literal (`#1330` later corrected exactly
such a split as an evasion).
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

**The rule.** Every `exec.Command("go", …)` in `cmd/` and `internal/` sets
`cmd.Env` from `goenv.Clean(...)`; a tool that reads the output of a `go` it
started must not let the caller choose that output's shape.
**The hurt.** 2026-09-18, ledger items 36–37: CI's `make test` exports
`GOFLAGS=-json`, `nova-review mutate`'s inner `go test` inherited it, answered in
JSON with no `--- PASS:` line, and the parser reported the green unit as red —
`red=1 green=1 PASS` became `red=2 green=0` and took down three legs of
integration-4 (`#1332`). `#1333` landed the rule at 21 sites, and it immediately
caught `#1324`'s release build.
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

### `slowtests` — no package over the per-package time budget

**The rule.** A package whose summed `go test -json` package elapsed time is over
`--budget` (default 60 s) is a refusal, printed where the coordinator sees it.
**The hurt.** `nova-secrets` sat at 120 s in the suite and nothing noticed,
because nothing summed the elapsed time `go test -json` was already printing. A
green that hides a doubling suite is a flaky wait one layer up, and slow CI
brings everything to a crawl.
**The test.** `TestSlowTestsUnderBudgetIsOK`,
`TestSlowTestsOverBudgetNamesThePackageAndSlowestTests`,
`TestSlowTestsEmptyInputIsOKWithZeroPackages`,
`TestSlowTestsMalformedLineIsRefused`,
`TestSlowTestsSlowestListIsSortedAndCapped` and
`TestSlowTestsOverPackagesAreOrderedWorstFirst`
(`internal/ci/slowtests/slowtests_test.go`), with the verb run end to end in
`cmd/nova-ci/main_test.go`.
**Its allowlist.** None: the budget is a number on the command line, and a
package over it is named every time.
**Its remedy lines.** `remedy="stdin is not newline-delimited go test -json"` and
`remedy="--budget must be a whole number of seconds greater than zero"`.
**Its narrowings.** It judges on the PACKAGE total only; the test-level rows are
kept, sorted worst first and capped at three, purely so a finding can say where
the time went. It sees one run on one machine, so a package that is fast on hulk
and slow on windows-latest is two measurements, which is why the Windows sizes
table exists — and why the RECORD holds one `## Bench:` section per machine,
each with its own ceiling, checked by `tools/testdur`'s own tests against
`docs/TEST-DURATIONS.md`. Full section: *The per-package test time budget*.

### `removeall` — no `os.RemoveAll` of a computed path

**The rule.** Glenn, 2026-09-17: "It is just one mistake away from deleting the
whole disk." Outside `internal/safepath`, `os.RemoveAll` may only take a variable
that came back from `os.MkdirTemp` in the same function; every other removal goes
through `safepath.RemoveUnder(root, path)`, which refuses an empty path, the root
itself, a path outside the root and a symlink.
**The hurt.** Ledger item 13: `#1059` removed computed slot paths with
`os.RemoveAll` and was dequeued out of the 2026-09-18 07:08Z merge group; card
9375 routed it through `safepath.RemoveUnder`. The bench-hygiene hostile-name
pass (group 1 of the ledger) is the same rule proved by hand, 40/40.
**The test.** `TestRemoveAllOnlyOnTempOrThroughSafepath`
(`internal/ci/removeall_class_test.go`).
**Its allowlist.** `internal/ci/testdata/removeall_allowlist.txt`, one
`file:function` per row — four today, each a `MkdirTemp` dir removed in its own
function (`internal/pulse/run.go`, `internal/secrets/seal.go` ×2,
`internal/secrets/sops.go`); shrink-only in both directions.
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

### `pathassert` — no test compares a path against a slash literal

**The rule.** A test that compares a path against a string literal containing `/`
asserts the SEPARATOR, not the behaviour; compare `filepath.ToSlash(got)`, or
build the want side with `filepath.Join`.
**The hurt.** Ledger item 26: `#1303` asserted a path with slashes and was
dropped from the 2026-09-18 10:12Z group. It is one of the three shapes that made
up every Windows-only red the merge group has seen — with the execute-bit
assertion (`#1262`, ledger 22 and 38) and the unsuffixed fake `.exe` (ledger 15).
**The test.** `TestNoTestComparesAPathAgainstASlashLiteral`
(`internal/ci/pathassert_class_test.go`), with
`TestPathAssertHeuristicReadsWhatItClaims`, the table that proves the heuristic
flags what the comment says it flags and nothing else.
**Its allowlist.** `internal/ci/testdata/pathassert_allowlist.txt` — empty: no
path-against-literal assertion is permitted today; shrink-only.
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
**The hurt.** Ledger item 32: `go test ./cmd/nova-work/` left
`cmd/nova-work/deps.json` in the tree, found while reviewing `#1323`; `#1329`
landed the rule and found one more offender. It is ledger item 24's class met one
directory along — 79 junk files under `scratch/` on dev from one wrong
`TMPDIR=$PWD/scratch` line replicated across ~100 cards (`#1311`).
**The test.** `TestToolRunsInTestsWriteIntoATempDir`
(`internal/ci/testoutpath_class_test.go`), with
`TestRelativeOutputPathScannerReadsTheFixtures` over the before/after fixtures in
`internal/ci/testdata/testoutpath/`.
**Its allowlist.** `internal/ci/testdata/testoutpath_allowlist.txt`, one
`file:function` per row — EMPTY, which is the point: the one offender the rule
found was fixed rather than listed; shrink-only in both directions.
**Its remedy line.** `name it inside t.TempDir(): filepath.Join(t.TempDir(), ...)`.
**Its narrowings.** Two, named out loud. The defect that prompted it is NOT
caught by it: `./deps.json` was never in the `_test.go` — it came out of the
usage banner in `main.go` and out of `docs/TESTS.md`, and the test ran the
transcript verbatim. And a function that chdirs is skipped whole, because
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
**The hurt.** `internal/review.TestMutateRemovesItsWorktreeOnBothPaths` proved
that `review.Mutate` removes its throwaway worktree by globbing
`os.TempDir()/nova-review-mutate-*` before the run and again after it, refusing
any entry that was not in the snapshot. On a laptop that is exact; on a
self-hosted Studio runner `os.TempDir()` is shared with every other job on the
box, and a sibling shard starting its own mutate between the two listings put a
directory in the glob this test never made. It went red in `#1341` twice, in
`#1345` and in `#1360` in the night of 2026-09-18 — four reds, no defect.
**The test.** `TestNoTestGlobsTheSharedTempDir`
(`internal/ci/sharedtemp_class_test.go`), with
`TestSharedTempReadScannerReadsTheFixtures` over the before/after fixtures in
`internal/ci/testdata/sharedtemp/`.
**Its allowlist.** `internal/ci/testdata/sharedtemp_allowlist.txt`, one
`file:function` per row with its reason — EMPTY, which is the point: the one
offender the rule found when it landed was fixed rather than listed; shrink-only
in both directions.
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

**The rule.** Glenn's rule has two halves: a program that takes over 0.1 s says
what it is doing on stderr, AND a progress line never enters a stream a consumer
parses. Every package that starts `nova-bus` and READS what it said asks
`internal/bus` — `bus.IsProgress(` or the one classifier that does, `Classify(` —
whether a line is progress.
**The hurt.** 2026-09-18, ledger items 30 and 32: `nova-bus`'s since-walk
narrated `INBOX WALK commits=1/1 notes=0 elapsed=3ms` on stderr exactly as the
first half asks; `nova-wake` merges stdout and stderr on purpose so an
`INBOX REFUSED` is never lost, its classifier's default case PRINTS, and the
progress line was relayed as `WAKE BUS LINE INBOX WALK …`, counted as a change in
the world, and ended a poll before the mail arrived. `#1309` was red against dev
for it; `#1320` landed one registry, four rule tests and this class.
**The test.** `TestEveryNovaBusConsumerDropsProgressLines`
(`internal/ci/busprogress_class_test.go`); the two halves it indexes are
`TestProgressNeverEntersTheProtocolStream` in `cmd/nova-bus` and
`TestProgressIsNeverRelayedAsABusLine` in `internal/wake`.
**Its allowlist.** None. The registry is `internal/bus/protocol.go` and a
consumer either reaches it or discards both streams; a start that reads nothing
back (`internal/pulse` sends a note and reads nothing) is not a consumer and is
not held to this.
**Its remedy line.** `<pkg> reads nova-bus's output and nothing in its package
reaches internal/bus.IsProgress; a progress line on stderr will be parsed as
protocol, which is the 2026-09-18 defect -- drop progress through the registry,
or read stdout alone`.
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
**The hurt.** Ledger item 31, 2026-09-18: integration-3's merge group failed on
ALL EIGHTEEN legs at once. The merge gate's selection did `pkgs=$(go list ./...)`
on a go.mod change and wrote it straight to `$GITHUB_OUTPUT`; the step had been
correct for every group since it was written, because until `#1272` (go-redis) no
group had changed go.mod. `#1318` fixed the instance with `| tr '\n' ' '`; this is
the class. That is the shape worth a rule: a line that is right until the day its
input is plural, and then fails everything at once rather than one leg.
**The test.** `TestNoMultiLineValueIsWrittenToAStepOutput`
(`internal/ci/ci_outputs_test.go`), with
`TestOutputHeuristicFlagsTheMergeGateRegression`, which runs the heuristic over
the dead step and over `#1318`'s guarded one — a class rule whose tree is already
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
**The hurt.** Two tests failed under load and passed alone — a version probe
timing out at five seconds and a stall assertion on a five-second deadline. The
class widened on `#916`: a batch `deadline:`/`idle:` literal names no context and
asserts no elapsed time, yet it drives a REAL subprocess kill, so within a file
that builds a `BatchInput` any `time.Second` literal under ten is a bet on how
loaded the machine is. Ledger items 16, 26 and 32 are the same bet losing on
darwin under load.
**The test.** `TestNoTestAssertsAWallClockBoundUnderTenSeconds`
(`internal/ci/ci_budget_test.go`) — the law `waits` was later built beside.
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

**The rule.** CARD-9019: a friend's `make test` and CI's test are the same
command. Every build, test, vet, format or acceptance command in `ci.yml` is a
`make` invocation, and the Makefile declares `build`, `test`, `test-full`,
`lint`, `check`, `clean` and `help` as phony targets, with `check` the union of
the gates CI runs and `clean` removing only its two explicit directories.
**The hurt.** Ledger item 21: `#1224` was dropped from batch 1 because a Makefile
test differed on the space runner — the first revision of this test shelled out
to `make -n check` and compared the dry-run text. It passed on hulk (GNU make
4.3) and failed on the space runner (4.4.1) and the Studio (3.81), because the
dry-run is not a contract and `$(GO)`/`$(PKGS)` expand to whatever the inherited
environment says. A test whose verdict depends on the host's make version says
nothing about the repository.
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

### `windows-pr` — a pull request gets a Windows leg, and it stays cheap

**The rule.** `test-windows-pr` runs on `windows-latest` over the packages
`.github/scripts/select-packages.sh` selects — the same script the self-hosted
shards call, so there is one answer to "what does this change test" — sharded,
skipped when the PR moves no Go file, fork-guarded, and aggregated by `ci-ok`.
**The hurt.** Windows ran only in the merge group and on push, so a Windows-only
failure was found after a PR was enqueued, where a red shard drops the whole
group and restarts every PR behind it: one PR's small mistake became every PR's
delay (ledger items 22, 26). `#1317` landed the leg; it paid for itself three
times in its first real run (ledger 38) — `#1324`'s install-skip, and behind it a
product bug (`adopt` named a bare `nova-update` on a Windows target), and the
execute-bit assertion class.
**The test.** `TestPullRequestsGetAWindowsLeg` (`internal/ci/ci_windows_pr_test.go`).
**Its allowlist.** None; the shape is the contract and the test is the shape.
**Its remedy line.** Each finding names the missing part of the shape — the
runner, the shard count, the sizes file, the selection script, the skip
condition, the fork guard or the `ci-ok` need.
**Its narrowings.** It reads `ci.yml` as text, so it pins the words a reviewer
would look for rather than the semantics a YAML parser would give; a leg that
satisfies every string and still does the wrong thing passes here and is caught
by the run.

### `windows-sizes` — the shard plan comes from measurements, never a convention

**The rule.** The three numbers that decide this leg's cost — the measured sizes
in `testdata/ci/package-sizes-windows.tsv`, the shard budget, and
`WINDOWS_TIMEOUT` in the Makefile — stay in one place and in step, and the
ceiling can never drop back below a size actually observed.
**The hurt.** Ledger items 39–40. The leg shipped with the hosted convention of
100 s per package; its first real run (`#1332`) found four packages ON that
ceiling under `-short`. A ceiling a tree's packages sit on is not naming a hang,
it IS the hang. Then the PR leg's six-minute cap killed finished shards 2–4
seconds into cleanup, and the merge leg's cap cut a shard mid-listing. The lesson
the whole day repeated: **a number in a table or a cap must come from a
measurement, and a measurement made at a cap is a floor, not a size.**
**The test.** `TestWindowsPRShardPlanIsDerivedFromMeasurements`
(`internal/ci/ci_windows_pr_test.go`).
**Its allowlist.** The sizes file itself, whose censored rows are LABELLED as
floors rather than written as sizes; the forcing packages `#1332` measured at the
ceiling are named in the test, so they cannot quietly fall out of the table.
**Its remedy line.** `the Makefile does not say where WINDOWS_TIMEOUT's number
comes from; a ceiling is a claim about the machine and belongs in the repository
with its measurement`.
**Its narrowings.** It checks that the numbers are measured and consistent, not
that they are still TRUE: a package that doubles on windows-latest keeps its old
row until somebody measures again, and only the run says so.

### `windows-table` — the merge gate's Windows leg deals from the Windows table

**The rule.** The merge group's windows leg reads the FULL column of the Windows
sizes table, takes its ceiling from `make -s windows-timeout`, and deals an
unmeasured or censored package across EVERY slot; linux and darwin keep the Linux
table, which is their measurement.
**The hurt.** Ledger item 38: the merge group's windows leg dealt from the LINUX
table — `cmd/nova-bus` at 6.4 s bought three shards, and shard 0 was killed at
the 100 s ceiling with three tests still running. Linux could not have said
otherwise: `cmd/nova-bus` is 10.0 s on hulk and at least 100 s there,
`cmd/nova-swarm` 51.0 s on hulk and 37 s there.
**The test.** `TestMergeGateWindowsLegDealsFromTheWindowsTable`
(`internal/ci/ci_windows_pr_test.go`).
**Its allowlist.** None.
**Its remedy lines.** `the merge gate's shard plan never reads <sizes file>; its
windows leg would deal from the Linux column again, which is how integration-4's
group was dropped`; `an unknown Windows size must be dealt across every slot,
never guessed downward`; and `the merge gate's windows leg does not take its
ceiling from make -s windows-timeout; the Windows number would be written twice
and drift`.
**Its narrowings.** It asserts the shell branch's text inside one named step; if
the step is renamed the test fails loudly rather than silently passing, which is
the trade it takes.

### `one-windows-leg` — exactly one Windows leg on a pull request

**The rule.** Exactly one `pull_request` job reaches `windows-latest`, and it is
`test-windows-pr`; the packages and path filters of the retired hosted leg are
still present in it.
**The hurt.** Ledger items 39–40: integration-4 ran both legs on the same commit.
The three sharded shards passed at 13:37–13:43Z and the older unsharded leg was
cancelled by its own six-minute timeout at 13:43:39 and failed the run. A second
leg that can only fail is not redundancy, it is a second thing to fix.
**The test.** `TestOnlyOneWindowsLegRunsOnAPullRequest`
(`internal/ci/ci_windows_pr_test.go`).
**Its allowlist.** None. The retirement is only correct if nothing it covered was
lost, so the two packages it ran and the three paths it watched are asserted
present in `test-windows-pr` — the paths matter on their own, because a change to
a fixture under `cmd/nova-bus` moves no `.go` file and `select-packages.sh`
cannot see it.
**Its remedy line.** `the pull_request jobs that reach windows-latest are <jobs>,
want exactly [test-windows-pr]; two Windows legs on one PR is a second thing to
fix and integration-4 is what it costs`.
**Its narrowings.** It counts jobs that name `windows-latest` AND the
`pull_request` event in their text; a Windows runner reached through a reusable
workflow or a matrix value built elsewhere would not be counted.

### `darwin-sizes` — the darwin cap is measured on a quiet host, with a stated margin

**The rule.** The numbers that decide the darwin merge leg's cost — the measured
sizes in `testdata/ci/package-sizes-darwin.tsv`, the 40 s shard budget, and
`DARWIN_TIMEOUT` in the Makefile — stay in one place and in step, and the ceiling
is never less than the stated margin over the largest share the plan can hand one
`go test`. The sizes are read on a QUIET host — no runner busy, no merge group in
flight — and the margin over them is TWO, written down as a number rather than
folded into the table. A measurement is a floor; a cap is a floor times a margin;
neither is ever a guess.
**The hurt.** 2026-09-18 16:41Z, merge-group run 35369433950 (batch 7, `#1360`):
`test-hosted-merge (darwin, 0)` and `(darwin, 1)` were CANCELLED at the
five-minute per-leg cap on superman, and a cancelled shard drops the whole group
and restarts every PR behind it. The log does not say what the headline says:
NO package came near the 100 s per-package ceiling. Shard 0 finished 27 packages
summing 211.8 s of `go test` between 16:36:43 and 16:41:24 and was killed partway
through the rest, its largest single invocation `cmd/nova-wake` at 50.7 s. What
ran out was the SHARD'S SUM, and the sum is decided by how many ways each package
is dealt. Dealing came off the LINUX table, and FOUR of the five largest darwin
packages sit under its 40 s budget, so each was dealt three ways instead of six:
`cmd/nova-wake` 24.6 s on hulk against 120.3 s measured on superman,
`cmd/nova-merge` 7.7 against 68.8, `internal/swarm` 17.4 against 64.4 and
`cmd/nova-bus` 10.0 against 54.2. The second half of the hurt
is the machine's STATE: superman was in its post-power-on condition, Spotlight
settling and XprotectService scanning fresh test binaries with sixteen runners
live, so the numbers of that moment were the state's and not the host's. That is
why the rule names the conditions and not only the number — and why the margin is
measured rather than assumed, on the same host both ways: `cmd/nova-merge` is
68.8 s whole on a quiet superman against about 147 s on the loaded superman of
that run, which is 2.1x.
**The test.** `TestDarwinMergeShardPlanIsDerivedFromMeasurements`
(`internal/ci/darwin_shards_class_test.go`).
**Its allowlist.** The sizes file itself, whose censored rows are LABELLED as
floors rather than written as sizes; the forcing packages are named in the test,
so they cannot quietly fall out of the table.
**Its remedy lines.** `the Makefile declares no DARWIN_TIMEOUT; the darwin
per-package ceiling has nowhere to live but a workflow line nobody can run`;
`DARWIN_TIMEOUT = <d>, under 2x the largest per-invocation share the plan can
hand one go test`; and `<file> does not say "quiet" anywhere in its header; a
size is only a size if the header says what the machine was doing when it was
read`.
**Its narrowings.** Like its Windows sibling it checks that the numbers are
measured and consistent, not that they are still TRUE: a package that doubles on
an x64 Mac keeps its old row until somebody measures again, and only the run says
so. It reads the table's header for the words `quiet` and `margin` rather than
verifying the conditions, which no test can check after the fact.

### `darwin-table` — the merge gate's darwin leg deals from the darwin table

**The rule.** The merge group's darwin leg reads the FULL column of
`testdata/ci/package-sizes-darwin.tsv`, takes its ceiling from
`make -s darwin-timeout`, and deals an unmeasured or censored package across
EVERY slot the group opened; only linux still keeps the Linux table, which is its
own measurement.
**The hurt.** The same run, 35369433950, and the same shape as `windows-table`
one platform later: a leg dealing from a table measured on another machine. Linux
could not have said otherwise — `cmd/nova-merge` is 7.7 s on hulk and 68.8 s on a
quiet x64 Mac, `cmd/nova-bus` 10.0 s there and 54.2 s here — so four of the five
packages that dominate this leg sat under the 40 s budget in the only table it
read, and were dealt three ways instead of six. Three platforms are three
measurements, and the last leg reading somebody else's numbers was the one that
dropped the group.
**The test.** `TestMergeGateDarwinLegDealsFromTheDarwinTable`
(`internal/ci/darwin_shards_class_test.go`).
**Its allowlist.** None.
**Its remedy lines.** `the merge gate's shard plan never reads <sizes file>; its
darwin leg would deal from the Linux column again, which is how run 35369433950's
group was dropped`; `an unknown darwin size must be dealt across every slot and
never guessed downward`; and `the merge gate's darwin leg does not take its
ceiling from make -s darwin-timeout; the darwin number would be written twice and
drift`.
**Its narrowings.** It asserts the shell branch's text inside one named step, so
a renamed step fails loudly rather than passing silently — the same trade its
Windows sibling takes. The windows and darwin branches in that step are
deliberately NOT factored into one parameterised helper: both tests read the step
as TEXT and assert each leg's own table and column literally, and a shared `awk`
taking the column as a variable would satisfy neither. Twenty lines of duplicated
shell is the price of a rule a reviewer can see.

### `cache` — no cache step on a self-hosted runner

**The rule.** Every `actions/cache` step in `ci.yml` carries
`if: runner.environment == 'github-hosted'`, and every `setup-go` step there says
`cache: false`; a persistent runner already has its cache on disk.
**The hurt.** 2026-09-17: the merge gate's darwin leg moved to the Studio's
persistent runners and kept a cache step written for a fresh hosted machine. Its
save phase tarred the whole multi-GB Go build cache on every job, outlived the
job's timeout and was orphaned still compressing — 82 `tar` and 79 `zstd`
processes put the Studio at load 147 and made every test on the machine crawl.
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
**The hurt.** A supply-chain rule adopted before it was paid for, and the cheapest
class test in the package. It belongs beside `SECURITY.md`'s standing position:
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
**The hurt.** Found on `#766` before the queue was turned on: an unnamed event
would let `ci-ok` run zero steps and report SUCCESS over red needs — a green that
means nothing, which is the same mistake as green-once-treated-as-green-now
(ledger item 20) one layer down.
**The test.** `TestEveryTriggeringEventReachesACIOKVerdict`
(`internal/ci/ci_budget_test.go`).
**Its allowlist.** None.
**Its remedy line.** `ci-ok has no verdict step guarded for <event>: the workflow
triggers on it, so a run on that event would report success with no step run`.
**Its narrowings.** The event list is written in the test, so an event added to
`on:` and nowhere else is not noticed until somebody adds it here; the check is
that the guard EXISTS, not that the step behind it asserts the right needs.

### `onboarding` — every command meets the onboarding standard

**The rule.** `docs/ONBOARDING.md`, asserted for EVERY directory under `cmd/` by
walking it: `<tool> help` prints usage ending in an `example:` block, a bare
command refuses in ONE line naming that door, and `docs/TESTS.md` carries a
`### First run` transcript inside that tool's `## <tool>` section.
**The hurt.** The walk exists for the binary nobody has written yet — a sixth
command joins the standard on the day it appears, not the day somebody remembers
a table. The skip map `notYetOnTheStandard` is EMPTY and that is the point:
`nova-bus` sat in it after the branch it named had merged, so the one tool the
README sends a stranger to first was the one tool excused from the standard.
Ledger item 44 is the same lesson from the other side — `nova-sandbox` and
`nova-work` contribute ZERO rows to `nova-check dogfood` because their `CLI.md`
sections are prose-shaped.
**The test.** `TestEveryCommandMeetsTheOnboardingStandard`
(`internal/ci/onboarding_test.go`).
**Its allowlist.** `notYetOnTheStandard` in the test file: empty, and an entry
must name a genuinely open branch, so a skip is a dated pointer rather than a
permanent exemption and it stops firing the moment that branch's section lands.
**Its remedy line.** The finding names the tool and the missing half — the
`example:` block, the one-line refusal, or the `### First run` section in
`docs/TESTS.md`.
**Its narrowings.** Only the two repo-wide points are checked here; the rest are
per-binary and live in each command's own `firstrun_test.go`, where the example
lines are EXECUTED, the refusal sentences asserted and the transcript compared
against real output.

### `version` — every tool prints the one version line

**The rule.** Every `cmd/nova-*` binary answers `version` with exactly one line
in the grammar `<tool> <identity> <goos>/<goarch> <go version>` followed by any
number of `key=value` extras — the grammar `internal/buildinfo` both writes and
reads, and `docs/SPEC.md` states once.
**The hurt.** `#1297` and `#1264`: `nova-version snapshot --bin ~/.local/bin`
refused an entire install, exit 2, because nova-merge printed five tokens where
the reader wanted four, and nova-sandbox printed a line of a different shape
(`SANDBOX VERSION tool=... version=...`). Two shapes meant every reader of a
version line carried its own tolerant parser, and a tool that said one more true
thing about itself broke them one at a time.
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
### `benchname` — a bench name is resolved through the machines registry

**The rule.** Glenn's lock of 2026-09-18: runner hosts are CI-only — no card,
probe or load on a machine that serves the merge group's shards. A function in
`internal/pulse` or `cmd/nova-pulse` that takes a `bench string` must resolve it
through `internal/fleet`'s registry (`RequireBench`, `Lookup`, or the two pulse
wrappers `fleetOneBench` and `refuseNonBenches`) before the name reaches a
machine, or be named in the allowlist with its reason.
**The hurt.** The lock was broken by a SHAPE, not a mistake: a bench name was a
bare string, so `--bench batman` was a hostname to ssh to and nothing in the
tools knew batman is six CI runners and not a card bench (2026-09-18: a
reproduction loaded onto batman put the darwin shards under, ledger item 13).
**The test.** `TestEveryBenchNameIsResolvedThroughTheRegistry` and
`TestTheBenchNameHeuristicReadsWhatItClaims`
(`internal/ci/benchname_class_test.go`); the second holds the heuristic itself
to hand-written sources so the first cannot pass by reading nothing.
**Its allowlist.** `internal/ci/testdata/benchname_allowlist.txt`, one
`<path>:<func>` per line with its reason, checked in BOTH directions — a listed
function that has left or now resolves its name is a red run — so the list only
shrinks. Today: `reissue` (formats card text, touches no machine), the two raw
seams `sshCapacity.Capacity` and `flashLauncher.Launch` (wrapped by
`pulse.Fill`), and the Mac power verbs, which exist FOR the runner hosts.
**Its remedy line.** `<path>:<func> takes a bench name and does not resolve it
through the machines registry; call fleet.RequireBench (or fleetOneBench /
refuseNonBenches) first, or add it to internal/ci/testdata/benchname_allowlist.txt
with the reason`.
**Its narrowings.** Only the two packages where a bench name reaches a machine
are read; a bench name that arrives as a different type, or through a package
outside them, is the compiler's rule, not this one.

### `prmerge` — nothing reaches the dev merge queue but a batch

**The rule.** Glenn, 2026-09-18: "Nothing reaches the dev merge queue but a
batch." `gh pr merge` in any spelling, and any `--auto` flag to it, is refused in
every non-test Go file under `cmd/` and `internal/` and in every file under
`.github/`. Admission to a merge queue is `internal/merge.Enqueuer.Enqueue`, the
`enqueuePullRequest` mutation, and nothing else.
**The hurt.** Four pull requests landed on dev that morning that nobody
enqueued: each carried GitHub's auto-merge, switched on hours earlier by a
`gh pr merge` call made while the pull request was red, and the forge enqueued
them itself as their checks went green. Twenty-seven open pull requests were
carrying the same instruction when the sweep found them; the enqueuer in
`internal/pulse/ledger.go` was doing it in code.
**The test.** `TestNoGhPrMergeSpellingInTheToolsGo` and
`TestNoGhPrMergeSpellingUnderDotGithub` (`internal/ci/prmerge_class_test.go`).
The Go walk reads string literals in source order per function, so a command
built in a slice is seen as well as one passed inline, and a refusal message
that mentions the spelling is one literal, not an argument list.
**Its allowlist.** `internal/ci/testdata/prmerge_allowlist.txt`, `<path>:<func>`
per line, checked in both directions so it only shrinks. Today: the audit's
`--disable-auto`, which takes an auto-merge OFF.
**Its remedy line.** `<path>:<line>: gh pr merge (or --auto) is refused; enqueue
through internal/merge.Enqueuer.Enqueue, or take the auto-merge off with the
audit's --disable-auto`.
**Its narrowings.** Test files are not read; a comment may still say auto-merge
— the rule is about what runs. A spelling assembled at run time from separate
words is not seen.

### `nightly-tags` — every tagged suite is run by a scheduled job

**The rule.** Every opt-in build tag a `_test.go` carries is named by a
SCHEDULED workflow, either literally on a `go test`/`go vet` line
(`go test -tags perf ./...`) or as a `tag:` entry of a job's matrix the step
then expands (`go test -tags ${{ matrix.tag }} ./...`). Platform and toolchain
constraints are not opt-ins and are out of scope: `//go:build darwin` says where
a test runs, not whether it runs, and a negation (`!windows`) is on by default
everywhere else.
**The hurt.** A build tag is how this tree takes a test off the per-change path,
and `go test ./...` without the tag compiles the file away silently. So a tag no
scheduled job passes to `go test -tags` is not a slower tier, it is a deleted
test that still looks like a test in the tree. One tag was in exactly that
state: `//go:build darwin && novadisk`
(`cmd/nova-sandbox/run_e2e_darwin_test.go`, the one real end-to-end run of the
sandbox — a real APFS volume made, used and destroyed) had never been compiled
by any workflow, and its own comment said "run it by hand, on a Mac". Nobody
did. Two more were worse than uncovered: `internal/ci`'s net checker EXEMPTS a
file carrying `//go:build nightly` or `//go:build soak` from the
no-real-network rule (`ci_net.go`, `ci_net_test.go` cases 3 and 4), and no
workflow ran either tag — a real-network test could be written, waved through by
the checker, and never execute once.
**The test.** `TestEveryTestBuildTagIsRunBySomeScheduledJob`,
`TestTheNetworkExemptTagsHaveAHomeInTheSchedule` and
`TestSomeScheduledJobRunsTheRaceDetector`
(`internal/ci/nightlytags_class_test.go`). The first is the class and names no
tag: it walks every `_test.go` for the tags that HIDE a file, reads every tag
the scheduled workflows name, and refuses the difference with the files that
would have gone unrun, so a tag invented tomorrow is covered the day its first
test file lands. The second holds the net checker's two exempt tags to a leg
whether or not a file carries one today. The third holds `race` — implicit,
because it comes from the `-race` flag rather than from `-tags` — to a scheduled
job that actually passes `-race`.
**Its allowlist.** None. The walk reads the tree rather than a list, so a tag
added tomorrow is held on the day its first test file lands.
**Its remedy line.** ``build tag "<tag>" hides <n> test file(s) and NO scheduled
job runs it: <files> — remedy: add a `tag: <tag>` leg to nightly-slow.yml's
matrix (or `go test -tags <tag>` to another scheduled workflow), or drop the tag
from those files``.
**Its narrowings.** Only SCHEDULED workflows count, and only what actually
reaches `go test`: whole-line YAML comments are dropped first, so prose ABOUT a
tag never stands in for a job that runs it. A tag assembled at run time, or
passed through a variable the step does not expand inline, is not seen.

### `selection` — `internal/ci` is always in the selected packages

**The rule.** `./internal/ci` is added to the package set on every selection —
by `.github/scripts/select-packages.sh` and by the merge gate's own inline
selection in `ci.yml` — never only as a fallback when the diff selected nothing.
**The hurt.** `internal/ci` scans the tree instead of importing what it guards,
so nothing in a diff ever "touches" it: PR `#1073` edited `cmd/nova-swarm` and
selected no shard that would run the class tests. Every rule in this document is
worth exactly as much as this line. The 2026-09-18 correction is the other edge:
when the diff had ALREADY selected `internal/ci`, a bare append ran it twice in
every shard of every leg (ten runs across the windows legs of run 35354900090
alone), so the append now sits in a `case` arm — a guard that prevents a
DUPLICATE keeps the rule.
**The test.** `TestSelectPackagesAlwaysAddsInternalCI` and
`TestMergeGateAlwaysAppendsInternalCI` (`internal/ci/ci_selection_test.go`).
**Its allowlist.** None.
**Its remedy line.** `select-packages.sh does not add ./internal/ci to want
unconditionally; internal/ci scans the tree instead of importing what it guards,
so a cmd/nova-swarm edit (PR #1073) selects no shard to run its class tests`.
**Its narrowings.** Two independent selections are pinned by two regular
expressions over two files; a third path into the package set would need a third
row here, and the test cannot know it exists.
