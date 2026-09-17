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
