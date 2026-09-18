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

**The mistake it removes.** `nova-secrets` sat at 120 seconds in the suite and
nothing noticed, because nothing summed the per-package elapsed time `go test
-json` was already printing. A green that hides a doubling suite is the same
mistake as a flaky wait, one layer up.

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
