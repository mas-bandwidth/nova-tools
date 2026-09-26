# Testing: regenerating the class tests' allowlists

The class tests in `internal/ci` (indexed in [SPEC-CI.md](SPEC-CI.md), under
"The class tests") keep their exceptions in lists under `internal/ci/testdata/`.
Every list only shrinks, and every one is read and written by the one helper,
`internal/ci/allowlist` (`allowlist.Load` and `allowlist.Check`, nova-tools#4339).

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
list file there is loaded through the helper, and nothing in `internal/ci` reads
one with `os.ReadFile`, `os.Open` or `readFile`.
