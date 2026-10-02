# Test harnesses

Use `internal/testkit` for the mechanics shared by tool tests: running an entry
point, capturing its streams, and setting up small file fixtures. Keep the
assertions in testify and the tool-specific setup beside the tool's tests.

`testkit.Main` takes the common entry-point shape
`func(args []string, stdin io.Reader, stdout, stderr io.Writer) int`. Adapt a
tool with extra dependencies by closing over them in one package-specific
adapter; do not copy stream buffers into a local result type or add another
set of assertion methods. `Main.Do` returns a `Ran`, whose fields include the
exit code and both streams:

```go
r := command.Do(t, "inspect", "--json")
r.Exit(0)
assert.Empty(t, r.Stderr, "JSON goes to stdout: %s", r)
```

`Ran.Exit`, `Out`, `NotOut`, `Err`, `NotErr` and `Refused` are fail-fast
`require` checks. `Refused` checks a nonzero exit plus the `REFUSED` status
word and remedy marker on the line containing the named text. Use it only when
that grammar is the contract under test. For independent findings in one run,
use direct `assert` calls and pass the `Ran` as context. Keep exact-output
checks exact; a shorter chain is not a reason to weaken `Equal` to `Contains`.
Keep setup and prerequisites fatal with `require`; keep independent table-row
checks nonfatal with `assert`.

Use `WriteFile`, `ReadFile`, `ReadJSON`, `JSON` and `Tree` for common file
mechanics. `Tree` writes named fixture files under an `os.Root`; it rejects
paths outside that root. Use `SkipOn` when a test's property cannot be
observed on a named operating system. Domain fixtures such as a sandbox job,
fake volume manager or egress plan stay package-specific.

Use `testing/synctest` for time-dependent tests first. `Waits` is only for a
wait seam around code that must perform real I/O and cannot run in a synctest
bubble; it records requested durations and lets the test hold or release that
wait. Do not add a general-purpose fake clock to the kit.

Keep package-level seam swaps and their cleanup explicit next to the test or
domain fixture that needs them. A small adapter may bind a tool's injected
environment to `testkit.Main`; it does not own fixture state or assertion
policy. Avoid generic swap helpers and local copies of `Main`/`Ran` behavior.

The first two-file nova-sandbox pilot changes 16 lines and removes 7 across
`main_test.go` and `grammar_test.go` (+9 net). The environment adapter and its
imports account for the added lines, so this pilot demonstrates shared
mechanics and unchanged assertion severity; it does not claim a line-count
saving.
