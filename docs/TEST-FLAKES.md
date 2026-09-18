# Test flakes: what was wrong, and the squeeze that proved it

A test that passes alone and fails under load is not a test to rerun. It is a test — or a
tool — that assumed something the machine was giving it for free: a wall clock that always
had enough seconds in it, a temp directory nobody else was in, or an order between two
processes that nothing in the code actually holds. This file is the record of the ones that
were run down, so that the next one is diagnosed and not rerun.

Every row carries the command that reproduces it. The shape is always the same: one core,
`GOMAXPROCS=1`, the package's own tests in parallel with each other, and a second `go test`
on the same core as load. That is a rough model of a Studio shard, where sixteen runners
share one machine and a test's share of the wall clock is a fraction of what it gets alone.

All squeezes below were run on hulk (`~/rowan-working/tmp/lane-flakes/repo`, Go 1.26.5,
`GOFLAGS=` cleared) and never on the Studio, which is the bench the shards run on.

## Fixed

| Test | Cause | Fix | Squeeze |
| --- | --- | --- | --- |
| `cmd/nova-review` `TestMutateVerbPrintsOneLineAndExitsZeroWhenTheTestIsRed`, `TestMutateVerbIgnoresTheCallersGOFLAGS`, `TestMutateVerbExitsOneAndNamesTheGreenTest`, `TestMutateGreenListingIsBounded` (one root cause) | `internal/review`'s `runUnits` read the verdict off the inner `go test`'s EXIT CODE when the run named no failing test: "exited non-zero, no `--- FAIL:` line, so every unit is red". A run the caller's `--timeout` killed exits non-zero and names nothing, so a loaded bench turned `red=1 green=1 PASS` into `red=2 green=0` — the same wrong counts three legs of integration-4 got from `GOFLAGS=-json`, arriving by the other road. | `runVerdict` (the decision, now a pure function of what a finished run leaves behind) asks `budgetEnded` FIRST: a run the context ended is a named SKIP and never a red unit. A skip never makes a verdict PASS, so the tool now says it could not answer instead of answering wrongly. `TestARunTheBudgetEndedIsASkipAndNeverARedUnit` pins it with no clock and no subprocess. | `GOMAXPROCS=1 taskset -c 12 go test ./cmd/nova-review/ -run TestMutate -count=20 -cpu 1 -parallel 8` with three `GOMAXPROCS=1 taskset -c 12 go test ./internal/pulse/ -count=3` on the same core — `ok … 337.680s`, 20/20 |

### The reproduction log for that one

The tool was built from this tree and pointed at the fixture its own `mutate` tests build
(a base, and a head that fixes `Sign` at zero and brings the test that is red without the
fix). The only thing that changes between the two runs is how many seconds the run is given
and whether the build cache is warm:

```
$ nova-review mutate --repo $LAB --base main --head HEAD
MUTATE 06b87135 red=1 green=1 PASS

$ GOCACHE=$(mktemp -d) nova-review mutate --repo $LAB --base main --head HEAD --timeout 1
MUTATE 06b87135 red=2 green=0 PASS          # the bench answered, not the range
```

and after the fix the second one says so out loud:

```
MUTATE SKIP file=sign/sign_test.go: the budget ended the run before it reported a result;
  nothing it printed is a verdict. Give --timeout more seconds, or run it on a quieter bench
MUTATE 06b87135 red=0 green=0 FAIL
```

The class this belongs to is the one `internal/goenv` already holds one half of: **the
verdict is a property of the range, never of the bench.** `goenv.Clean` keeps the caller's
environment out of it; `budgetEnded` keeps the caller's clock out of it.

## Open, with what the squeeze did and did not show

Two more were handed to this lane. Neither reproduced on hulk, and neither has a failure in
CI to read: a scan of the last **200 failed runs** of `mas-bandwidth/nova-tools`
(`gh run view <id> --log-failed`, every failing test name collected) has no `--- FAIL:` for
either of them. They are recorded here with what was run, so the next person starts from
the counts and not from zero.

| Test | Squeeze run | Result |
| --- | --- | --- |
| `cmd/nova-secrets` `TestPlaceNeverPrintsTheValue` | the package under `GOMAXPROCS=1` on one core, `-parallel 8`/`16`, alone and with every other test in the package, ~120 package runs across four shapes, including ten concurrent copies of the test binary pinned to a single core | green every time |
| `cmd/nova-merge` `TestBatchDropsTheConflictAndGoesRedOnTheFailingMember` | the same shapes over `./cmd/nova-merge/` | green every time |

What the scan DID find, on the dev CI run of 2026-09-18T20:12Z (run 35390201127,
`test-hosted (macos-latest, 2)`), is a sibling in the same fixture:

```
--- FAIL: TestBatchDropsAMemberWhoseOwnHeadIsNotGreen (0.69s)
    dogfood_test.go:371: a batch whose members were all dropped still runs its gate: exit 2
        BATCH REFUSED: git clone --quiet -- …/remote.git …/batch/integration-checks/repo:
        exit status 128: fatal: failed to copy file to '…/repo/.git/objects/1a/02b1…':
        No such file or directory
```

A LOCAL `git clone` walks the source's `objects/` with readdir and then hardlinks or copies
each entry one at a time; git reports the destination path but the errno can come from
either side. Something removed a loose object out of the fixture's bare repository between
the listing and the copy. That is an ordering between two processes with no sync point, and
it is the shape the lane was looking for — but the mechanism was not established here and
no guess was committed. The next step is a reproduction on a darwin bench (it has only ever
been seen on darwin shards) with `GIT_TRACE=1` on the gate's clone.

A third failure turned up in the merge package under the heaviest squeeze (ten copies of
the test binary on one core): `TestAPacketIsHandedOverCorrectlyWhileAFlushRuns` failed once
in 20 rounds at 42.5 s. It is not on this lane's list and is recorded here so it is not
found twice.
