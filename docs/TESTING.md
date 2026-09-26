# Testing: regenerating the class tests' allowlists

The class tests in `internal/ci` (indexed in [SPEC-CI.md](SPEC-CI.md), under
"The class tests") keep their exceptions in lists under `internal/ci/testdata/`.
Every list only shrinks, and every one is read and written by the one helper,
`internal/ci/allowlist` (`allowlist.Load` and `allowlist.Check`, nova-tools#4339).

## The two tiers

Glenn, 2026-09-26 11:20 AM ET (nova-tools#4328): "Only run tests that you
actually need to, according to the changes being made." "We should be minimal
and frugal with what we do in unit tests. And we should run functional tests,
not on every small PR being merged or worked on, but only as we merge whole work
streams, and functional tests themselves should be frugal and built wisely to
not waste resources."

**Unit tests mock.** A unit test starts no server, dials nothing and waits on no
clock: it fakes the store, the network and the process it talks to. It runs on
every pull request in `.github/workflows/ci.yml`'s `test` matrix, over the
packages the change touched, at most two cores a leg (`make test`, `GOTEST_P`),
and it is done in under 1 s per test and 2 s per package (`nova-ci slowtests`,
with `internal/ci/slow-tests_allowlist.txt` for the rows still being cut). The
unit tier's PATH holds a `redis-server` that refuses, so a test that needs a
real one fails there, naming this rule.

**Functional tests carry the tag and run per stream merge.** A test that needs
the real thing (a redis-server, a built binary, a child process) lives in a
`_test.go` that starts with `//go:build functional`. `make test-functional` runs
only those tests, and ci.yml's `functional` job runs it in the merge queue (a
whole work stream merging into dev), nightly and by hand, never on a pull
request. Keep them few and cheap: one server per package (`TestMain`) rather
than one per test, and the same two-minute cap as every job.

## `NOVA_CI_UPDATE=1`

A change that removes offenders -- a sleep fixed, a serial test made parallel, a
raw `os.RemoveAll` routed through `safepath` -- leaves rows that name nothing.
Regenerate every list with one variable, then run again:

```
NOVA_CI_UPDATE=1 go test -count=1 ./internal/ci/
go test -count=1 ./internal/ci/
```

Under `NOVA_CI_UPDATE=1` each allowlist-backed class test rewrites its list to
the set it measured and fails once with `updated, rerun`:

- a stale row (one that names no offender on the tree) is dropped;
- every comment and every kept row stays byte for byte;
- a `# ceiling: N` line (the serial list carries one) is lowered to the new row
  count, and never raised.

Every list here is **ceiling-only**: its count only falls. Under the update a
ceiling-only list refuses to grow and says so, one line per key it will not add
(`... is ceiling-only and refuses to grow under NOVA_CI_UPDATE=1: <key> is not
listed ...`), beside the class test's own remedy. The update never writes a
reason on anyone's behalf: a new offender is fixed, or listed by hand with the
reason a reviewer reads.

Only the value `1` updates. No script edits a list file; the rerun is the proof
the lists now match the tree.

## Which test reads which list

| List under `internal/ci/testdata/` | Class test |
|---|---|
| `bench-runners.allow` | `TestCIOneBenchRunner` |
| `cardtemplate_allowlist.txt` | `TestNoCardTemplateCarriesAnOSSpecificCommand` |
| `compared_examples.txt`, `unexecuted_examples.txt` | `TestIssue2218` |
| `fieldsindex_allowlist.txt` | `TestNoUncheckedFieldsIndex` |
| `fixed-testbins-allowlist.txt` | `TestNoCopiedTestBinariesOnTheCIPath` |
| `fixed-waits-allowlist.txt` | `TestNoFixedWaitsOnTheCIPath` |
| `forestwriter_allowlist.txt` | `TestForestWrittenOnlyByTheKernel` |
| `goenv-allowlist.txt` | `TestGoEnvClassRuleHoldsOverTheRepository` |
| `hostseam_allowlist.txt` | `TestNoTestReachesAHostThroughAnUnfakedSeam` |
| `lisptemppath_allowlist.txt` | `TestNoLispTestBuildsATempPathWithoutTheHelper` |
| `lisprandom_allowlist.txt` | `TestNoLispTestNamesAPathWithRandom` |
| `namedpaths_allowlist.txt` | `TestEveryNamedRepoPathExists` |
| `net-allowlist.txt` | `TestNoRealNetworkHostsOnTheCIPath` |
| `pathassert_allowlist.txt` | `TestNoTestComparesAPathAgainstASlashLiteral` |
| `prmerge_allowlist.txt` | `TestNoGhPrMergeSpellingInTheToolsGo` |
| `removeall_allowlist.txt` | `TestRemoveAllOnlyOnTempOrThroughSafepath` |
| `serial-tests_allowlist.txt` | `TestEveryTestOpensWithTParallel` |
| `sharedtemp_allowlist.txt` | `TestNoTestGlobsTheSharedTempDir` |
| `slowwaits_allowlist.txt` | `TestNoTestSleepsOverASecondOrWaitsOutADeadlineOverFive` |
| `template-paths-allowlist.txt` | `TestNoUnquotedPathsInTemplateLiterals` |
| `testoutpath_allowlist.txt` | `TestToolRunsInTestsWriteIntoATempDir` |
| `transcripts_allowlist.txt` | `TestEveryTranscriptIsExecutedLineForLine` |

`TestEveryAllowlistIsReadThroughTheOneHelper` holds the table's promise: every
list file there is loaded through the helper, and nothing anywhere in the tree
reads one with `os.ReadFile`, `os.Open` or `readFile`.
