# Testing: regenerating the class tests' allowlists

The class tests in `internal/ci` (indexed in [SPEC-CI.md](SPEC-CI.md), under
"The class tests") keep their exceptions in lists under `internal/ci/testdata/`.
Every list only shrinks, and every one is read and written by the one helper,
`internal/ci/allowlist` (`allowlist.Load` and `allowlist.Check`).

How to run each tier and use the store helpers of `internal/testredis` in a test:
[FOUNDATION-TESTING.md](FOUNDATION-TESTING.md).

## The two tiers

Only run the tests the change needs. Unit tests are minimal and frugal.
Functional tests do not run on every small pull request; they run as whole work
streams merge, and they are frugal and built wisely so they do not waste
resources.

**Unit tests mock.** A unit test starts no server, dials nothing and waits on no
clock: it fakes the store, the network and the process it talks to. It runs on
every pull request in `.github/workflows/ci.yml`'s `test` matrix, over the
packages the change touched, at most two cores a leg (`make test`, `GOTEST_P`),
and it is done in under 1 s per test and 2 s per package (`nova-ci slowtests`,
with `internal/ci/slow-tests_allowlist.txt` for the rows still being cut, each
naming its measurement; the times are printed on every leg and enforced only on
the nightly bench legs, while a wall-clock wait in a unit test or a
`t.Skip("SLEEPS: ...")` missing from `internal/ci/sleeps-skips_allowlist.txt` is
red on every leg). The
unit tier's PATH holds a `redis-server` that refuses, so a test that needs a
real one fails there, naming this rule.

**Functional tests carry the tag and run per stream merge.** A test that needs
the real thing (a redis-server, a built binary, a child process) lives in a
`_test.go` that starts with `//go:build functional`. `make test-functional` runs
only those tests, and ci.yml's `functional` job runs it in the merge queue (a
whole work stream merging into dev), nightly and by hand, never on a pull
request. Keep them few and cheap: one server per package (`TestMain`) rather
than one per test, and the same two-minute cap as every job. On a working machine
they run inside one container per run, `make test-functional-container
PKGS=<packages>` or `nova-ci functional --in-container <packages>`, on podman or
docker ([TESTING.md](../TESTING.md)), never bare.

### Vetting the build-tagged test files

A test file behind an opt-in build tag is compiled by no plain `go vet ./...`,
so the vet targets compile each tag on every change: the lint job runs `make
vet-functional`, `make vet-slow`, `make vet-shippedsmoke` and `make vet-novadisk`
(and certification vets `perf` with `go vet -tags perf`).
`internal/ci`'s `TestEveryTestBuildTagIsVettedByCIVetSteps` reads the vet
targets and refuses a tag no vet step passes, so a tag that hides a test file is
type-checked on a pull request rather than only when its nightly job runs.

### Table epoch actions and receipt replay

`TestTableEpochActionsAndReceiptReplay` in `internal/ntable` runs eight fixed
seeds against an owned source/replay Redis pair, reset between seeds. Each seed
has three generations, 24 random actions per generation, and stale-writer probes
for every modeled write at each advance. Every action checks placement, score
order, immutable member epochs, unchanged history, and refusal without writes.
Accepted receipt arguments and member deltas must match the independent state
model; the replay store executes those receipts and must reach the same state.
Both stores compare complete key snapshots before and after accepted writes;
added, changed or deleted keys outside the model transition's allowed set fail.
Catalog membership and the complete template and immutable identity hashes must
also equal the model after every action, including their initial registration.
Failure output includes the seed and complete action trace. A `-run` selection
ending in `/seed_1$` reproduces just that seed in the functional test.

Action names correspond to `EpochMemberTable.tla`; this is bounded execution
coverage, not exhaustive model checking or a concurrent-writer test. External
bindings, batches, definition edits and row sorting have separate tests.

### delivery-conformance-r.w1 — the nightly delivery check

The unit tier proves every harness adapter against fakes
(`TestEveryAdapterPassesDeliveryConformance`, internal/friend); the live
session is proved once a night by `nova-friend check` on each friend's
machine, one nova-config loop record per friend (kind loop), never a hand
loop or a script:

```sh
nova-config loop add friend-check-<friend> --machine <the friend's machine> --argv '["/usr/bin/env","NOVA_BUS_REDIS=<the bus store, host:port>","nova-friend","check","--as","<friend>","--harness","<harness>","--dir","<the friend's working directory>"]' --every 86400 --as <coordinator>
```

Its line (`CHECK OK harness= took=`, or `CHECK FAIL harness= stage= why=` at
exit 1) is the loop's record; the check is docs/SPEC-FRIEND.md, the subsection
of the same name.

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
| `compared_examples.txt`, `unexecuted_examples.txt` | `TestPlatformsMatchCILegsAndUnexecutedExamplesOnlyShrink` |
| `fieldsindex_allowlist.txt` | `TestNoUncheckedFieldsIndex` |
| `fixed-testbins-allowlist.txt` | `TestNoCopiedTestBinariesOnTheCIPath` |
| `fixed-waits-allowlist.txt` | `TestNoFixedWaitsOnTheCIPath` |
| `forestwriter_allowlist.txt` | `TestForestWrittenOnlyByTheKernel` |
| `goenv-allowlist.txt` | `TestGoEnvClassRuleHoldsOverTheRepository` |
| `hostseam_allowlist.txt` | `TestNoTestReachesAHostThroughAnUnfakedSeam` |
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
