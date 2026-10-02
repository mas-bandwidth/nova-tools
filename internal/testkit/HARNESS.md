# Test harnesses

Use `internal/testkit` for the mechanics shared by tool tests: running an entry
point, capturing its streams, and small file fixtures. Keep assertions in
testify and the tool-specific setup beside the tool's tests.

**Order of preference**, for every piece: the standard library, then testify
(already in go.mod), then what `internal/testkit` already has, and new code
only for a named, unmet need. A piece goes into `internal/testkit` only when
two or more packages would use it; one package's harness lives in that
package's `harness_test.go`; a helper with fewer than three callers is inlined.

**The shape.** `testkit.Main` is the entry point
`func(args []string, stdin io.Reader, stdout, stderr io.Writer) int`; a tool
that also takes an environment, a clock or an app is bound to it by one small
adapter (`withEnv(run, env)` in cmd/nova-sandbox). `Main.Do` returns a `Ran`
(`r.Code`, `r.Stdout`, `r.Stderr`), so every run in every file is one value, not
three locals and a buffer. `Ran.Exit`, `Out`, `NotOut`, `Err`, `NotErr` and
`Refused` are `require` checks that fail with the run as their message;
`ExitErr(code, want, msg, args...)` is Exit and Err with the caller's own
sentence. A domain bench (a fake disk, a fake forge, an egress plan) stays in
its verb's test file as a struct or function that owns its setup through
`t.TempDir` and `t.Cleanup`, with methods named for what the test means
(`b.once`, `j.on`, `planned`). Cases that differ only in data are table rows.

Before (two excerpts of cmd/nova-sandbox/run_test.go):

```go
old := runGoEnv
t.Cleanup(func() { runGoEnv = old })
runGoEnv = func() (goDirs, error) { return goDirs{Root: root, ModCache: mod}, nil }
code, errOut := runOnce(t, b, runFlagsFor(t)...)
require.Equal(t, 125, code, "a duplicate name is not refused with reason=volume_exists: exit %d\n%s", code, errOut)
require.Contains(t, errOut, "reason=volume_exists", "a duplicate name is not refused with reason=volume_exists: exit %d\n%s", code, errOut)
```

After:

```go
swap(t, &runGoEnv, func() (goDirs, error) { return goDirs{Root: root, ModCache: mod}, nil })
r := b.once(t, runFlagsFor(t)...)
r.ExitErr(125, "reason=volume_exists", "a duplicate name is not refused with reason=volume_exists: exit %d\n%s", r.Code, r.Stderr)
```

A package with three or more seam swaps keeps a generic `swap(t, &seam, v)` in
its `harness_test.go`: the swap stays at the call site, only the restore moves.
Such a test still cannot open with `t.Parallel()` and stays on the serial list.
Files are `WriteFile`, `ReadFile`, `ReadJSON`, `JSON` and `Tree` (which refuses a
path outside its root); `SkipOn` names the platform a property cannot be seen on.
Time is `testing/synctest` first; `Waits` only for code that must do real I/O.

**Hard limits** of a harness pass. The same `Test` names survive, in the same
files (subtests may be added under them). No check is removed or weakened:
`Equal` stays `Equal`, a fatal check stays `require`, a `t.Error`-class check
stays `assert`. Every failure message keeps its sentence and its arguments. No
production code changes. No ledger ceiling is raised and no allowlist row is
added under `internal/ci/testdata`. A rewrite by script is read line by line:
in the pilot a rename turned `require.Equal(t, out, again)` into a comparison of
a value with itself, which compiles, passes, and checks nothing.

**Proving no check was lost.** Count the checks before and after (testify calls,
`ExitErr` as two, `t.Error*`), account for every difference, then break five
production behaviours the converted checks pin and watch each test go red.

**Measuring.** Lines: `wc -l cmd/<pkg>/*_test.go` at the base and after, and
`git diff --shortstat origin/sprint/foundation -- '*.go'` for the whole branch,
harness included. Time: `go test -count=1 ./cmd/<pkg>/` three times, before and
after.

**What to expect.** The cmd/nova-sandbox pilot (2026-10-02) went from 7,582 to
7,453 test lines (-1.7%), checks intact, wall time unchanged. The testify pass
before it took 8,751 to 7,582. Under the limits above, the lines that remain are
reasons (17% are comments), failure sentences, and the fakes that stand in for
the disk, the forge and Win32; a harness pass makes runs uniform and removes
copy-paste, but a 20% cut would need sentences dropped or tests merged.
