# TESTING.md: test what you touched, the way CI does

**Before you push: `nova-ci local`.** From the checkout you changed:

```sh
nova-ci local                  # the unit tier CI runs for this diff
nova-ci local --functional     # with the functional build tag, as the merge queue runs it
nova-ci local --base origin/main
```

It runs exactly what the unit tier of `.github/workflows/ci.yml` runs for your
change (nova-tools#4336), so your answer is CI's answer before the push:

- **the packages** are `.github/scripts/select-packages.sh`'s answer against the
  merge base of `--base` (default `origin/dev`) and `HEAD`: the Go packages the
  committed diff touches, every package that imports one of them, and the class
  test packages every run carries;
- **the run** is the Makefile's `test` target, the one entry CI's legs call:
  its `go test` flags, its `-timeout`, and its `nova-ci slowtests` budgets;
  `--functional` passes `GOTEST_TAGS=functional`, so the redis-backed tests
  behind `//go:build functional` run too (CI runs them in its `functional` job
  as a stream merges; docs/TESTING.md, "The two tiers");
- **the machine** is shared, so everything runs under `nice -n 15` with
  `GOMAXPROCS=2` and `GOTEST_P=2` (`go test -p 2`, two cores, as a CI leg is
  held to), `-count=1`, and a private `RUNNER_TEMP`.

It prints one `PKG` line per package with its seconds, one `RED` line per
failing test with the tail of that test's own output, slowtests' `CI-SLOW`
verdict, and exits as CI would: 0 green, 1 a red test or a package that did not
build, 2 over the budgets or a step that could not run. Uncommitted Go files are
tested but not selected (the selection reads committed history, as CI's does);
it says so, and the fix is to commit them.

**Never run the whole tree** (`go test`, `go vet` or `go build` over `./...`) on
a shared bench: CPU is for real work, and CI runs the whole tree on every push to
dev. No doc and no card spells it (`internal/ci`: `TestNoWholeTreeGoTestInDocs`).
Outside a nova-tools checkout, test only the packages you touched:
`nice -n 15 go test -p 2 -count=1 <packages>`.
