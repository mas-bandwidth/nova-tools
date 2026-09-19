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
## The failing tests of a run

**The verb.** The second verb of `cmd/nova-ci` is `failed`:

```
failed  read a run's failing jobs; print the failing tests, their file and line
```

It runs as
`nova-ci failed --repo <owner/name> (--run <id> | --pr <n> [--merge-group] | --branch <name>) [--job <text>] [--max-lines <n>]`,
and it is the pipeline Rowan typed six times on 2026-09-18 made a verb:

```sh
gh api repos/.../actions/jobs/<id>/logs --allow-escape-sequences \
  | tr -d '\033' | sed 's/\[[0-9;]*m//g' | grep -E -- '--- FAIL|_test.go:[0-9]+:|panic:'
```

That pipeline has to be retyped for every leg of a matrix, it loses the package
and the job, it drops the continuation lines a test printed under its own
`t.Errorf`, and it reads a cancellation as silence. A pipeline is not a tool.

**The invariant.** The verb reads and never writes: it resolves one run, reads
the log of every job of that run whose conclusion is not `success` or `skipped`,
and says what those logs said. Everything it gets back is DATA from a host — a
job name, a step name, log text — never an instruction, and every line it prints
goes through `internal/oneline`. The forge is a seam: `ci.FailForge` has three
methods (`ResolveRun`, `Jobs`, `JobLog`), `ci.GHFailForge` is the one
implementation that shells to `gh`, and the tests hand the verb a fake, so no
test here touches the network.

**What it parses.** Both shapes this repository's CI produces, in one pass,
after stripping the three things a GitHub Actions log wraps every line in: the
runner timestamp, the ANSI colour and the carriage return.

- Plain `go test` output, as the self-hosted and Windows legs produce it. A
  `--- FAIL: <Test> (<d>)` opens a block; the indented lines under it are that
  test's own words, including the continuations under a `t.Errorf`; the first
  `<file>_test.go:<n>:` in them is the position; and the `FAIL <pkg> <d>` line
  that closes the run gives every block before it its package.
- `go test -json` frames, as the hosted legs produce them. These are read BY
  FRAME, never by position: the go command interleaves parallel tests, so a
  failing test's one message line routinely sits between two other tests' lines.
  A frame whose `Test` is empty is package output and is read as plain text with
  the package the frame already named.
- `panic: test timed out after <d>` with its `running tests:` list, which is not
  a failing test and is not reported as one.

A `--- FAIL: TestX` that printed nothing of its own, beside a reported
`TestX/case`, is the go command repeating itself and is dropped.

**Its output.** One block per failing test — a `FAILED` line, then that test's
own message lines indented as it printed them — then one block per job that went
red with no test in it, one line per cancellation and per timeout, then the
closing count:

```
FAILED job="<name>" pkg=<pkg> test=<Test> at=<file:line>
    <the test's own message lines>
    ...+<n> more lines
NOTEST job="<name>" step="<name>" tests=none
    ...+<n> earlier lines
    <the lines the runner marked as errors>
CANCELLED job="<name>" step="<name>" after=<d>
TIMEOUT job="<name>" pkg=<pkg> running=<TestA,TestB,TestC,+<n>>
NOLOG job="<name>" reason="<the forge's own words>"
FAILED (OK|RED) jobs=<n> [failed=<n>] [cancelled=<n>] tests=<n> [unread=<n>]
```

A job name and a step name are QUOTED rather than escaped as fields: `test (3/4
studio)` is what a reader pastes back into `--job`, and `oneline.Field` would
hand them `test\x20(3/4\x20studio)`, which the forge has never heard of. A
package, a test name and a `file:line` hold no space and stay bare, so one grep
reads a column. `at=` is omitted, never guessed, when the test printed no
position. A test's own words are bounded by `--max-lines` (default 8) and the
rest are COUNTED, the same cap-and-count rule `slowtests` uses; the `running=`
list is capped at three the same way. The closing count always prints, and its
counts are the truth about the run whether or not every line printed; its verdict
word is `OK` only when the run said nothing red, so it agrees with the exit code
instead of heading a report of reds, and the jobs are split into the `failed=<n>`
that went red of their own and the `cancelled=<n>` cut down with them, each field
omitted when it is zero. `running=none` and `tests=none` are said out loud rather
than left blank.

**Every red job gets a line.** A job whose conclusion is not `success` or
`skipped` and whose log holds no test event at all — a C compiler error under
`-Werror` inside a `make test` step, so `go test` never ran — is
`NOTEST job="<name>" step="<name>" tests=none`, with the step read off the job
(omitted, never guessed, when the forge named none) and, under it, the lines the
RUNNER itself marked as errors: the compiler's own diagnosis, already picked out
of the log by the forge that recorded it. That block is bounded by `--max-lines`
from the END, because the errors that ended the step are the last ones annotated
and the earlier ones in a long log may belong to a negative control that was
supposed to be red; the dropped lines are counted above the ones shown. The
specimen is run 35329874611 of `mas-bandwidth/schema`, where one
`inline-gate (ubuntu-latest, go)` went red inside `make test` and seven siblings
were cancelled: the run was reported as seven `CANCELLED` lines and
`jobs=8 tests=0` under the word `OK`, and the one job worth chasing was counted
and never named.

**A log the forge will not give is a LINE.** A job whose log cannot be read is
`NOLOG job="<name>" reason="<the forge's own words>"`, and `unread=<n>` joins the
closing count; the run is still exit 1, and every other failing job still
reports. The specimen is a job CANCELLED while its run is still in progress,
whose log blob the forge answers `404 BlobNotFound` for until the run finishes:
refusing the whole run over that hides every other job's red, which is the one
thing this verb exists to surface. It was found by running the verb on its own
pull request the hour it was written.

**Its exit codes.** 0 when the run said nothing red, 1 when it said something —
which every job that did not succeed does, since each one gets a line, a
cancelled-only run included — 2 on a refusal. `failed` is a READER, so a red run is exit 1 — the caller asked
what broke and got an answer — while exit 2 stays what it is everywhere else in
this family: the invocation could not run.

**Its refusals (exit 2, one line each, ending at the door).** A `--repo` that is
not `<owner>/<name>`, and there is no default. Naming no run, or more than one of
`--run`, `--pr` and `--branch`. `--merge-group` without `--pr`, since the merge
queue's run belongs to a pull request. A `--job` that matched none of the run's
failing jobs, naming up to three of them. A `--max-lines` of zero or less and a
`--timeout` of zero or less, refused rather than read as unlimited. A stray
argument or an unknown flag. A forge that could not answer, in its own words.

**How a run is resolved.** `--run` is the run. `--pr` reads the pull request's
head branch and sha and takes the newest run of that sha, falling back to the
newest run of the branch, so a stale run of an older push is never read as this
one. `--merge-group` takes the newest `merge_group` run whose head branch holds
`/pr-<n>-` — GitHub names a queue branch
`gh-readonly-queue/<base>/pr-<n>-<sha>`, so the pull request number is in the
branch and nothing has to be guessed. `--branch` is the newest run on it.
`--job <text>` keeps the jobs whose name contains that text AND reads no other
job's log, so a forty-leg matrix costs one call rather than forty; a `--job` that
matches no failing job is a refusal naming the jobs that did fail, never a green
report.

**The mistake it removes.** Six times in one day, the same four commands, by
hand, to turn four megabytes of log into five lines — and the sixth time still
missed the cancelled siblings of a failed leg, because a cancellation leaves
nothing in the text. It is read here off the job's own steps instead.

**Red tests.** `internal/ci/failed_test.go` parses five real job logs, cut from
the runs of 2026-09-18 and committed under `internal/ci/testdata/failed/` with
their timestamps, ANSI and CRLF intact — `windows-sandbox.log` (job
105673713280, plain output, five failing tests), `studio-review.log` (job
105673768922, `-json` frames), `pulse-flake.log` (job 105696546293, `-json`
frames), `merge-darwin-timeout.log` (job 105698657603 of run 35375346271, the
`1m40s` timeout) and `inline-gate-werror.log` (job 105551505883 of run
35329874611 of `mas-bandwidth/schema`, a `-Werror` compiler error inside
`make test` and no test event at all). `cmd/nova-ci/failed_test.go` runs the verb
over them through a fake forge:

1. The plain log yields five failing tests, each with its package from the `FAIL`
   trailer and its `file:line` from the test's own first message.
2. A test's continuation lines come back whole, the part a `grep '_test.go:'`
   drops.
3. The `-json` log attributes a message line to the test whose frame carried it,
   not to the test whose line happens to precede it.
4. The timeout is a `TIMEOUT` naming the package and the ten tests still running,
   and is not counted as a failing test.
5. A cancelled step is read off the job, with how long it had been running.
6. `--max-lines` bounds a test's words and counts the rest; the closing count
   does not move.
7. Only the jobs that did not succeed are read, `--job` reads exactly one, and a
   `--job` matching none of them refuses with their names rather than reporting
   a green run.
8. A job whose log the forge will not hand over is a `NOLOG` line and an
   `unread=` count, and the run's other failing jobs still report in full.
9. Every refusal above exits 2, writes nothing on stdout and ends at the door,
   and `failed --help` opens the verb's own door without asking a forge anything.
10. A job whose conclusion is failure and whose log holds no test event is a
    `NOTEST` line naming the job and the step that went red, carrying the last of
    the lines the runner marked as errors, and the run exits 1. Over the shape of
    run 35329874611 — that one job and seven cancelled siblings — the summary is
    `FAILED RED jobs=8 failed=1 cancelled=7 tests=0`, and a cancelled-only run is
    the exit 1 it has always been.

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
table exists. Full section: *The per-package test time budget*.

### `failed` — a run's failing tests, not its log

**The rule.** A red run is read as the failing tests it holds — job, package,
test, `file:line` and the test's own words — never as four megabytes of log
scrolled by eye.
**The hurt.** Rowan retyped the same `gh api … | tr | sed | grep` pipeline six
times on 2026-09-18. It has to be retyped per matrix leg, it loses the package
and the job, it drops the continuation lines under a `t.Errorf`, and it reads a
cancelled sibling as silence.
**The test.** `TestAPlainGoTestLogNamesEveryFailingTestWithItsFileAndLine`,
`TestATestsOwnWordsComeBackWhole`,
`TestAJSONLogAttributesLinesByFrameNotByPosition`,
`TestTheSecondJSONLogReadsTheSameWay`,
`TestATimeoutNamesThePackageAndTheTestsStillRunning`,
`TestACancelledStepIsReadFromTheJobNotTheLog`,
`TestTheMessageLinesAreCappedAndTheRestCounted`,
`TestACleanRunIsOneLineAndExitZero`,
`TestTheRunningListIsCappedAndCounted`,
`TestStripLogLineTakesTheWrapperAndNothingElse`,
`TestARunReadsOnlyTheJobsThatDidNotSucceed`,
`TestTheJobFilterReadsOnlyThatJobsLog`,
`TestAJobFilterThatMatchesNothingSaysWhichJobsFailed`,
`TestALogTheForgeWillNotGiveIsALineNotTheEndOfTheReport`,
`TestTheSelectorReachesTheForgeUntouched`,
`TestAFailingJobWithNoTestEventIsNamedAnyway`,
`TestTheSummarySplitsARealRedFromItsCancelledSiblings` and
`TestACancelledOnlyRunStaysExitOne` (`internal/ci/failed_test.go`), over five
real job logs in `internal/ci/testdata/failed/`, with the verb run end to end in
`cmd/nova-ci/failed_test.go`.
**Its allowlist.** None: it reports what a run said, and there is nothing to
excuse.
**Its remedy lines.** None; its refusals are the verb's own, each naming what the
input wants — `--repo` as `<owner>/<name>`, exactly one of `--run`, `--pr` and
`--branch`, `--merge-group` with `--pr`, a positive `--max-lines` and `--timeout`.
**Its narrowings.** It reads only the jobs whose conclusion is not `success` or
`skipped`, and only the two shapes `go test` prints; a failure that is neither a
`--- FAIL`, a `-json` fail frame nor a timeout panic — a compile error, a runner
that died — is a `NOTEST` line naming the job and its failed step, with the lines
the runner marked as errors under it, and the rest of that log is left to `--job`
and the forge. A log it cannot read at all is a `NOLOG` line and an `unread=`
count, never a refusal. Full section: *The failing tests of a run*.

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

### `hostseam` — no test reaches a host through an unfaked seam

**The rule.** Every function under `cmd/` and `internal/` that reaches another
machine calls `testguard.RefuseHosts(<program>, <args>…)` before it starts the
child. Under `NOVA_TEST_NO_HOST`, which `make test` exports for every tier, that
call panics with the command line, so a test that constructed production code
and injected no fake refuses THERE instead of on a bench. A function is a host
seam when it starts a child (`exec.Command`/`exec.CommandContext`) and names an
ssh-family program — `ssh`, `scp`, `sftp`, `rsync` — in a string literal, or
when its own name or its receiver's carries one of those words.
**The hurt.** 2026-09-18: a unit test in the certify verb's first cut (`#1382`)
ran the REAL workloads on hulk and reached redis on space. Nobody wrote a
hostname in the test — the test held production code, production code built its
own default because nothing injected a fake, and that default was
`exec.Command("ssh", …)`. The `net` class reads test files for a real host and
could not see it: the host was never in the test's text, it was in a default two
packages away. That is the difference between the two rules — `net` reads what a
test SAYS, this reads what production DOES.
**The test.** `TestNoTestReachesAHostThroughAnUnfakedSeam`
(`internal/ci/hostseam_class_test.go`), with `TestNoHostSeamIsFoundByASubstring`,
the table that pins the name heuristic against the three false positives its
first sweep had (`IsSHA`, `HarnessSHA256`, `hasShebang`). The guard itself is
`internal/testguard`, held by `TestUnsetGuardLetsTheSeamRun`,
`TestArmedGuardNamesTheCommandAndTheRemedy`, `TestAFakeOnPATHIsNotAHost` and
`TestAllowHostsIsScopedAndNests`; the fake-less red that bought the rule is
`TestTheRealSSHRunnerPanicsUnderTheGuard`
(`internal/pulse/hostguard_test.go`), which constructs the real `SSHRunner`,
injects nothing, and ran a child `ssh` before the guard existed.
**Its allowlist.** `internal/ci/testdata/hostseam_allowlist.txt`, one
`file:function  # reason` per row — six today, every one a function that reaches
its host through another function in the tree that DOES call the guard (the
`FleetRunner` implementation, the two `…OverSSH` fan-outs, `powerWaitSSH`,
`ExecSSH.sshArgs`, and an error type named for ssh's exit code). A row with no
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
does that today. The guard treats a program that resolves INSIDE a temp
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
**Amended by [SPEC-TOOLWORK.md](SPEC-TOOLWORK.md) §7 (draft, 2026-09-19):** that last sentence was
not true of 8 of 22 sections, and 9 more compared a set of shapes; §7 makes execution, line for
line through one comparator, the thing the class test asserts.

### `kernel-components` — no kernel source is compiled by nobody

**The rule.** Every `.lisp` file under `lisp/nova-work/src/` and
`lisp/nova-work/tests/` is named by a `:components` list in
`lisp/nova-work/nova-work.asd`, or is named in the `notCompiled` ledger with the
issue that owes its removal. The system names no file that is gone, and an entry
whose file is gone or has become a component fails too, so the ledger only
shrinks.
**The hurt.** ASDF loads a file because the system names it, never because it is
in the directory, so an unnamed file is not slow-to-load — SBCL never reads it.
On 2026-09-19, `dev@47d81e9c`: **28 of the 60 files in `src/` were in no system**
— all 17 `replays-86NN.lisp` and 11 feature-named `replays-*.lisp`, roughly 700
defuns and defstructs that nothing compiled — while `run-tests.sh` reported
`total=327 pass=327 fail=0`. `#1102` read that as a naming problem and called the
fold mechanical. It is not: appending all 28 to the system and running the suite
dies with `attempt to redefine the STRUCTURE-OBJECT class SAVEPOINT incompatibly
with the current definition` loading `src/replays-8641.fasl`, exit 1, the 327
cases never reached. A card told to move that file into `src/savepoint.lisp`
would have landed a kernel that does not load — or dropped the colliding form to
get green, with nobody able to say which of the 700 forms went.
**The test.** `TestEveryKernelSourceIsACompiledComponent`
(`internal/ci/lispkernel_class_test.go`). It is a Go test rather than a lisp one
on purpose: the lisp job runs only when `lisp/**` or `docs/SPEC-WORK.md` moved,
and a file nothing compiles is exactly what a green lisp run cannot see.
**Its allowlist.** `notCompiled` in the test file: 28 entries, every one owed to
`#1102`. It is the point of the test rather than a hole in it — a silent file is
invisible, a listed one is a debt with an issue number that cannot grow without
this test saying so.
**Its remedy line.** The finding names the file and says SBCL never reads it: it
compiles nothing, no acceptance case covers it, and `run-tests.sh` is green
without it.
**Its narrowings.** Only the `nova-work` kernel and only `.lisp` files directly
under `src/` and `tests/`. It reads the component list, not the load: whether the
system as named *loads* is `make test-lisp`'s business.
### `one section` — docs/TESTS.md names each tool exactly once

**The rule.** No two `## ` headings in `docs/TESTS.md` carry the same name. A
tool with more than one thing to say says it in `###` subsections of its one
section.
**The hurt.** `docs/TESTS.md` carried `## nova-work` twice. `onboarding.Section`
cuts to the FIRST match and cannot fail, so `cmd/nova-work/firstrun_test.go`
executed the first section and the second was read by no test at all. It drifted
into two sentences the binary no longer printed — a bare-command refusal in the
retired spelling (`nova-work: no verb given`, against the shipped
`WORK REFUSED: a verb is required`) and an `events` line carrying `--repo`, the
flag that switches ON the `gh pr list` fallback that section's own prose says is
off. Both reproduced as DEFECT on space AND on hulk in the 2026-09-18 two-bench
dogfood run while every test in this repository was green, which is `#1506`
read from its other end: the drift was not a test that was too weak, it was a
document half of which no test could see.
**The test.** `TestNoToolIsWrittenTwiceInTheTranscripts`
(`internal/ci/onboarding_test.go`), over the parse in
`onboarding.RepeatedSections`.
**Its allowlist.** None. A repeated heading has no good case: the second copy's
readership is nobody.
**Its remedy line.** The finding names every repeated heading and says only the
first is read — by `onboarding.Section`, by every `firstrun_test.go`, and by a
person looking for the one place to change.
**Its narrowings.** Only `docs/TESTS.md` and only `## ` headings; a repeated
`###` inside one tool's section is that section's business, and `cmd/nova-work`'s
own `TestTESTSRefusalsAreWhatTheToolPrints` is what holds a second subsection to
what the tool prints.

### `transcripts` — every documented transcript is EXECUTED, line for line

**The rule.** For every `## <tool>` section of `docs/TESTS.md`, a test in
`cmd/<tool>` runs every `$` line of the section's `### First run` block, in
order, in one sitting, and compares each command's whole output with the block
written under it through `onboarding.CompareTranscript` — same number of lines,
same lines, same order, every value compared as written. The only values matched
by shape are the run-owned ones named from the one shared table,
`onboarding.Volatile` (`at`, `took`, `created`, `tmpdir`, `sha`); a name the
table does not hold is refused, so no call site can turn a red green by widening
one pattern. A `firstrun_test.go` may compare no other way. Specified in
`docs/SPEC-TOOLWORK.md` §7 rules 2-4.
**The hurt.** The class test this replaces asserted that a tool's section
EXISTS; nothing asserted that anything ran it. At the 2026-09-19 triage 8 of 22
sections were executed by no test — including `nova-ci`, whose two CI-SLOW lines
a stranger copies were a promise no build checked — and nine more collected what
was printed into a `printed map[string]bool` and asked whether each documented
line was somewhere in it, so an abridged or a reordered block passed. Four
abridged transcripts (#1638, #1639, #1641, and nova-post's `--channel fake`,
#1631) were found that week, every one of them by a person.
**The test.** `TestEveryTranscriptIsExecutedLineForLine`
(`internal/ci/transcripts_class_test.go`). It walks `cmd/` for the directories
`docs/TESTS.md` has a section for, and reads each package's test sources for the
one comparator called with that tool's own name, and for the comparisons rule 2
replaces: `onboarding.Execute`, `onboarding.Compare`, `onboarding.Shape` and a
`printed` set.
**Its allowlist.** `internal/ci/testdata/transcripts_allowlist.txt`, one `<tool>`
per line with the issue that owes it, checked in both directions -- a stale entry and an ORPHAN naming no section are both red -- so it only
shrinks: an unlisted unexecuted section is red, and a listed section a test now
executes with the one comparator is a stale entry and red too. Today: the
twenty-one sections not yet converted (#1653, #1654, #1657, #1722).
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
held its own name counted as executing its section (reproduced green in the
cold read of #1723 at `215b7740`). What the proxy still cannot see is a file
that opens the document and compares something it built from it; what closes
that is the `transcript-test` kind's own control (`docs/SPEC-TOOLWORK.md` §7
rule 4), which seeds the tool's real section three ways and demands red — a
control that runs per card, where this class test runs per tree. A fourth way
of comparing, written from scratch, is likewise not seen until it is named
here. Whether a transcript is TRUE is not this test's business — a document that
disagrees with its tool is a finding and a `fix-red` card
(`docs/SPEC-TOOLWORK.md` §7 rule 6), never an edit that makes a test pass.

### `platform-leg` — a skipped transcript is still executed somewhere

**The rule.** A `## <tool>` section of `docs/TESTS.md` whose transcript one
platform cannot reproduce says which in ONE typed line:
`Platform: <goos>[,<goos>] — <what the other benches print instead>`. It is a
GOOS list and not a sentence, so a transcript test can skip on it BY NAME and
this rule can check it: every platform named must be a leg
`.github/workflows/ci.yml` runs, read off the merge gate's `leg:` matrix rather
than listed here. A skip is a hole, and a hole is sound only while something
else fills it.
**The hurt.** #1509. `nova-sandbox probe` prints `backend=sandbox-exec` and an
empty `abi=` on macOS and `backend=landlock` with `hosts=`, `gpu=`, `used=` and
`ancestors=` on Linux — fields the macOS transcript has no slot for — and the
section said so in prose no test could act on. Windows is the live case for the
leg half: every `windows-latest` leg was dropped on 2026-09-18 (Glenn: "drop the
native windows CI runners. WSL only from now on."), so a `Platform: windows`
line today would name a transcript that is skipped on every bench, which reads
in a green build exactly like one every bench runs.
**The test.** `TestPlatformLineMustNameACILeg`
(`internal/ci/platformleg_class_test.go`); the line itself is parsed by
`onboarding.SectionPlatforms`, and `onboarding.SectionSkipReason` is the named
skip a transcript test uses.
**Its allowlist.** `internal/ci/testdata/platform_line_allowlist.txt`, one
`<tool>` per line with the issue that owes it, checked in both directions -- a stale
entry and an ORPHAN naming no section are both red -- so it
only shrinks. Today: `nova-swarm`, whose line names a machine state and not a
platform (#1509).
**Its remedy line.** ``the `## <tool>` section is recorded for "<goos>" and no
leg of .github/workflows/ci.yml runs <goos>``.
**Its narrowings.** Only `docs/TESTS.md`, and only sections with a directory
under `cmd/`. It reads the line, never the transcript: whether the block under
it really is platform-specific is `TestFirstRunTranscriptsNameTheirPlatform`'s
question, and whether it is TRUE is a `fix-red` card's.

### `unexecuted-examples` — every pasteable line is executed or counted

**The rule.** Every `$ ` line in a fenced block of `README.md`,
`docs/USAGE.md`, `docs/nova-swarm-quickstart.md` and `docs/CLI.md`'s
`### First run` sections, and every `example:` line of every `help` banner, is
either executed by a test through `onboarding.CompareTranscript` — the same
comparator `docs/TESTS.md`'s sections are held to — or listed with the reason it
is not.
**The hurt.** #1455 measured the banners a stranger is sent to first: 28 of 61
`example:` lines exited 2 when pasted. A door that opens onto a wall, and no
build said so, because the banner's contract stopped at "there is an example
block". The count is the point: it is not that these lines work, it is that
somebody has to say out loud when a new one arrives unchecked.
**The test.** `TestUnexecutedExamplesOnlyShrink`
(`internal/ci/examples_class_test.go`). It builds each `cmd/` binary and reads
its `help` banner, because the banner is assembled at run time and what a
stranger pastes is what the binary said.
**Its allowlist.** `internal/ci/testdata/unexecuted_examples.txt`, one
`<line>` TAB `<reason>` per entry, checked in both directions so it only
shrinks. The separator is a TAB and not the ` #` the other lists use, because
these keys are shell lines and one of them carries a shell comment of its own.
Today: 118 lines, none executed.
**Its remedy line.** ``<line> is pasteable and no test executes it through
onboarding.CompareTranscript; execute it, or list it in
testdata/unexecuted_examples.txt with the reason (the list only shrinks)``.
**Its narrowings.** It does not RUN the lines and does not ask whether they
work — that is what each listed line's own card is for. A `$ ` outside a fenced
block is prose (`$N$` in the swarm quickstart is arithmetic), and in
`docs/CLI.md` only the `### First run` blocks are counted.

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

### `toolchainroots` — the bench standard and the wall name one list per OS, with one kind each

**The rule.** `internal/swarm/toolchain.go` is the ONE list of the bench
toolchain roots the sandbox wall grants a card, **per GOOS**, and each root
carries its KIND: `~/sdk` read **and execute**, `~/go/pkg/mod` read **without**
execute, `~/go/bin` granted under neither, and on darwin the installed trees
(`/opt/homebrew/Cellar/go`, `/opt/homebrew/Cellar/sbcl`,
`/opt/homebrew/opt/openjdk`, `/Library/Java/JavaVirtualMachines`,
`/usr/local/share/dotnet`) read **and execute**, never a launcher directory.
Each OS's side of the agreement is that OS's provisioning standard:
`tools/bench-standard.sh` carries the linux names between its
`NOVA_TOOLCHAIN_ROOTS` markers and drifts on a missing one, and
`pulse.FleetStandardChecks`'s `toolchain-*` checks carry both OSes' — a linux
root **demanded**, a darwin root **reported**, because a Mac's toolchains are
installed rather than provisioned into a home. Both `docs/SPEC-SWARM.md` and
`docs/CLI.md` name every granted root.
**The hurt.** Two contracts named the same paths in two places and disagreed: the
provisioning standard put Go under `~/sdk`, the wall's implicit worker
description named no toolchain root at all and pinned `GOTOOLCHAIN=local`, so
every Go card on hulk was denied EXECUTION of the bench's own `go`, fell back to
`/usr/bin/go` 1.22.2 and died on `go: go.mod requires go >= 1.26` (the schema
dogfood loop, 2026-09-18). The kind half is Johnny's security read of `#1364`: a
`--read` root CARRIES EXECUTE on both wall bodies, so the first fix was one
review away from handing a card execute over the module cache and `~/go/bin`.
The per-OS half is the same day's darwin face, measured on the M2 Air: a Mac's
toolchains are INSTALLED and on `PATH`, and three of them still died inside the
bare wall — `go: cannot find GOROOT directory: 'go' binary is trimmed`,
`dotnet: Failed to resolve full path of the current executable []`, `java: Unable
to locate a Java Runtime` — because each resolves its runtime from the directory
of the launcher that ran it and that launcher is a symlink OUT of any granted
tree. One list for every OS would have left the Mac benches dead.
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
root under its own flag by `TestNativeArgvReadsTheBenchToolchainRoots` and, on a
Mac, `TestNativeArgvReadsTheDarwinToolchainRoots`, and that the version under a
Cellar prefix is read off the launcher rather than guessed by
`TestToolchainVersionDirReadsTheVersionOffTheLauncher`.

## Parked class tests

A parked rule is one this repository decided to stop enforcing, kept here with
its hurt so the decision can be read rather than rediscovered. The test files are
DELETED — a class test that does not run is worse than no test, because it reads
like cover — and `internal/docs`' index test knows this section by name, so the
`Test…` names below are allowed to name tests that no longer exist. Nothing else
in this document is allowed to.

**Why these four are parked.** Glenn, 2026-09-18: *"drop the native windows CI
runners. WSL only from now on."* Every `windows-latest` leg left `ci.yml` in that
change — `test-windows-pr`'s four shards, `test-hosted-merge`'s windows leg, and
`test-hosted`'s windows entry on the push and nightly matrix — and with them the
measured Windows size table, `WINDOWS_TIMEOUT` and `make windows-timeout`. What
remains on the CL path is ONE cheap compile guard in the `lint` job,
`make vet-windows` (`GOOS=windows go vet ./...`), which type-checks every package
and every `_test.go` for Windows on a Linux runner in seconds; the merge gate
(`nova-merge`'s `vet-windows` step) runs the same command, so the guard is
paid twice before a change lands. Running Windows TESTS is the certification
tier's job, which is a release blocker and never a CL one. Windows as a place to
put work is the Threadripper under WSL2 — a LINUX bench with Linux runners
labelled `linux,X64,threadripper` — see `docs/BENCH-STANDARD-WINDOWS.md`.

**What would unpark them.** A native Windows CI runner, or a measured Windows leg
that fits the two-minute law. Read `windows-sizes` below before writing either:
its rule — a number in a table or a cap must come from a MEASUREMENT, and a
measurement made at a cap is a floor and not a size — is the one that cost the
most to learn and it is live today, one platform over, as `darwin-sizes`.

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
**PARKED 2026-09-18.** Glenn: "drop the native windows CI runners. WSL only from now on." `test-windows-pr` is gone from `ci.yml` and `TestPullRequestsGetAWindowsLeg` is deleted with it. The rule is kept here for the record, and it is the one to read first if a Windows leg is ever wanted again.
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
**PARKED 2026-09-18.** Glenn: "drop the native windows CI runners. WSL only from now on." `TestWindowsPRShardPlanIsDerivedFromMeasurements` is deleted, `WINDOWS_TIMEOUT` is out of the Makefile and `testdata/ci/package-sizes-windows.tsv` is out of the tree; the measurements are in git at dev `65e86175`. **The rule itself is NOT parked**: it runs today as `darwin-sizes`, on the platform that still has a measured merge leg.
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
**PARKED 2026-09-18.** Glenn: "drop the native windows CI runners. WSL only from now on." The merge gate's windows leg, its arm of the shard plan and `make windows-timeout` are all gone. **The rule itself is NOT parked**: it runs today as `darwin-table`.
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
**PARKED 2026-09-18.** Glenn: "drop the native windows CI runners. WSL only from now on." Exactly ZERO `pull_request` jobs reach `windows-latest` now, which is the same rule at its limit, and `TestOnlyOneWindowsLegRunsOnAPullRequest` is deleted. What a pull request gets instead is the `lint` job's `make vet-windows` (`GOOS=windows go vet ./...`), which compiles every package and every test file for Windows on a Linux runner.
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

## How the class tests read the tree: one walk, one parse, in parallel

Every rule above is a sweep of this repository's own source, and for a while
every rule paid for its own sweep: `filepath.WalkDir` over `cmd/` and
`internal/`, `os.ReadFile` on each of the 1,052 `.go` files, and `go/parser`
over each of them again. Eight rules, eight walks, eight parses of the same
bytes. Measured on hulk at `dev` `04bb4e1c`, `go test ./internal/ci/ -count=1`
took 10.2 s and 10.4 s, and `-race` took 49.4 s — for a package the CL tier is
held to a two-minute budget with.

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
the walk can see, so nothing invalidates the cache and the second walk was never
buying anything. That is also why the rules are safe to run concurrently: each
class test carries `t.Parallel()` and reads the shared tree and its own
`testdata/` allowlist, and writes only to its own `t.TempDir()`. A test that
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
