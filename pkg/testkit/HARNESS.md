# Test harnesses

Use `pkg/testkit` for the mechanics shared by tool tests: running an entry
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

Use `Git(t, n)` only in the tests of the adapter that runs real git; every
other test fakes git at that adapter's seam. It builds a bare remote and n
clones of it under `t.TempDir()`, runs the git on PATH, and gives every command
a fixed identity and `GIT_CONFIG_GLOBAL` at an empty file in that directory
through the command's environment, so the machine's git config never reaches
the test and nothing outside the temp directory is written.

Use `Commit(clone, files)` to give a clone history: it writes each named file
under the clone and commits them all as one commit.

Use `Push(clone)` when the test needs the remote to hold a clone's commit. It
is one `git push` of the clone's current branch from the clone to the remote.

Use `Head(repo)` to read the commit a clone's or the remote's HEAD names, for
asserting where a push or commit left it; it fails the test on a repository
with no commit.

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

Use `Script` for a fake with expectations over a transport interface
(docs/STANDARD.md section 8: "A fake is strict like the real tool: it refuses
what the real one refuses"). Declare the calls a test expects, in order, as
`Call{Name, Args, Result, Err}`; the package's fake holds the Script and each
interface method passes its own name and arguments to `Called`, then returns
the declared result and error. An unexpected call, a call out of order, or a
declared call the run never made fails the test naming it; `NewScript` arms the
leftover check at cleanup. Every call is declared: there is no matcher and no
wildcard. `script_test.go`'s `fakeStore` is the example:

```go
type fakeStore struct{ *testkit.Script }

func (f fakeStore) Get(key string) (string, error) {
	got, err := f.Called("Get", key)
	if err != nil {
		return "", err
	}
	return got.(string), nil
}
```

Script is a small ordered list rather than `testify/mock`, which cannot be
imported at this base: `mock` imports `github.com/stretchr/objx`, and that
module is absent from go.mod and go.sum, so `-mod=readonly` refuses it. The two
behaviors the transport design names are kept: every call is declared, and the
expectations are asserted at cleanup.

Use `Contract` in a tool's skeleton-contract test to hold the whole shape in one
call: `testkit.Contract(t, main, verbs)` runs the probes the skeleton contract's
"How a port is proved" names; the paragraph above `Contract` in contract.go
lists them.

