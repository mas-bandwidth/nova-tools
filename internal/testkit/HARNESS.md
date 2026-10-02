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

**The limits of a compaction** (the owner, 2026-10-02, after the pilot). Every pinned
BEHAVIOUR stays pinned; `Equal` is never weakened to `Contains`. `Test` names may
go: cases that differ only in data are rows of one table, a shared long setup may
be one scenario's steps, and each former `Test` keeps a subtest name, so a failure
still names the case. A helper may own the failure sentence; a custom sentence
stays only where it carries a reason the check and the row name do not, and a
reason comment stays once, at the table or the row. Still forbidden: production
changes, raised ceilings, added allowlist rows, changed fakes.

**The method.** (1) Inventory before editing: one line per `Test`, file, name and
the behaviour it pins, plus a check count (testify calls, `ExitErr` as two,
`t.Error*`). (2) Compact. Severity in a row: `require` for setup and for a check
whose failure makes the rest of the row meaningless, `assert` for independent
facts. A `Test` that swaps a seam stays serial and keeps the name of one merged
test already on `serial-tests_allowlist.txt`; the others' rows go stale and are
removed with `NOVA_CI_UPDATE=1 go test -run '^TestEveryTestOpensWithTParallel$'
./internal/ci/`, which lowers the ceiling. Names other ledgers cite
(`compared_examples.txt`) stay. A deleted test file, or one git reads as deleted
across a merge, is declared in `deleted-tests.txt` in that change. (3) Map every
inventory line to the row or step that now pins it, then break ten production
behaviours across the converted files, at least two in merged tables, and watch
each go red; a probe that stays green is checked against the base before calling
it lost. A rewrite by script is read line by line: in the pilot a rename turned
`require.Equal(t, out, again)` into a value compared with itself.

**Measuring.** `wc -l cmd/<pkg>/*_test.go` before and after; `git diff --shortstat
<base> -- '*.go'`; `go test -count=1 ./cmd/<pkg>/` three times each side; `go
test -v` counting top-level tests and subtests.

**What it did.** cmd/nova-sandbox: harness pilot 7,582 -> 7,453 (-1.7%) under the
old limits; compaction 7,453 -> 5,748 (-24.2% from 7,582), 212 tests -> 114 with
272 subtests, wall time unchanged, ten probes red. What remains: 1,040 comment
lines (reasons, issues, measurements), 398 blank, 4,310 code, of which the fakes
(disk, forge, Win32, nft) are a fixed cost. Next package: split it by file among
agents only after the shared helpers are settled (in `harness_test.go` and here),
because helpers added late collide; write the inventory with the check count
per `Test` so the mapping is mechanical; and read every allowlist that keys on a
test name before merging any.
