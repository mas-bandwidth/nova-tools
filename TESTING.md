# TESTING.md: test what you touched, the way CI does

**Before you push: `nova-ci local`.** From the checkout you changed:

```sh
nova-ci local                  # the unit tier CI runs for this diff
nova-ci local --functional     # with the functional build tag, as the merge queue runs it
nova-ci local --base origin/main
```

It runs exactly what the unit tier of `.github/workflows/ci.yml` runs for your
change, so your answer is CI's answer before the push:

- **the packages** are `go run ./tools/ci select-packages`'s answer (the same
  selection, `internal/pkgselect`) against the merge base of `--base` (default `origin/dev`) and `HEAD`: the Go packages the
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

## The functional tier runs inside a container

Unit tests run as they are. The functional tier (the tests behind
`//go:build functional`, which start `redis-server`, `postgres`, built binaries
and child processes) runs inside ONE container per run, and never bare on a
shared machine: every dependency a test starts lives and dies with its
container. The fixtures are unchanged; they start their dependencies as child
processes, and those are inside the container.

```sh
make test-functional-container PKGS=./internal/ntable/...
```

Name the packages. The run needs rootless `podman` (on macOS, a running
`podman machine`) and the image's build context, `infra/functional-image`
(`FUNCTIONAL_CONTEXT=<dir>` names another). The target calls
`tools/functionalrun`, which, in order:

1. **reaps**: removes every container of this tool and this user whose
   deadline label, plus a grace, has passed, in any state: the run label with a
   run id of the tool's own shape, and the owner label equal to this uid. It
   selects by label, never by name, reports one of its own containers with an
   unreadable label and leaves it, and touches nothing else: no other
   container, no volume, no process;
2. **builds or reuses the image**, tagged by the hash of the build context, so an
   unchanged context is never built twice;
3. **uses this user's own cache volumes**, `nova-functional-gocache-uid<uid>` and
   `nova-functional-gomod-uid<uid>`, labelled with their owner; a volume of
   another owner is refused. The caches persist between runs and carry no run
   label, so they are never reaped and never counted as leftovers;
4. **fills the module cache** in one networked step (`go mod download`, skipped
   when the cache was filled for the same `go.mod` and `go.sum`);
5. **runs `make test-functional PKGS=...` in one container**: the tree mounted
   read-only at `/src`, `/tmp` and the home directory as tmpfs scratch, the build
   cache read-write, the module cache read-only, `--network none`, a private IPC
   namespace, no capabilities and no new privileges, limits on CPUs, memory
   (no swap) and pids, and the deadline
   (`FUNCTIONAL_DEADLINE`, default `10m`, the bound of this container alone)
   enforced from outside the container by the runtime's own `--timeout`, which
   holds even when the client is killed.
   Inside, `timeout` ends the run 10 s before the deadline and `go test
   -timeout` 20 s before it, so a hang prints its stack first;
6. **removes the container** at the end whatever happened: a pass, a failure,
   the deadline, or an interrupt (Ctrl-C reaches the tool, which removes the
   container, never the runtime's client alone; a second one kills the tool at
   once). It then counts the run's containers by label, within 30 s in all,
   and prints one line:

```
FUNCTIONAL RUN run=<id> ended=finished exit=0 wall=20.3s build=0.0s modcache=0.5s total=21.2s containers_left=0
```

The test output comes through unchanged on stdout and stderr. Through make,
the exit code is 0 green and 2 for any failure: make reports every failing
recipe as 2. The distinct codes exist when the tool is called directly
(`bin/functionalrun run ...`, which the target builds): the container's own
code (`make test-functional`'s: 0 green, 2 a red test or build), except where
the tool ended the run: 124 the deadline (the runtime's `--timeout`, or the
in-container timeout), 130 an interrupt, 125 the run could not start, its
runtime client was lost, or a container of the run was still present at the
end. The target execs the built tool, so a signal to make reaches the tool,
which removes the container.

**The build cache crosses runs.** A run can change what the next run of the
same user reads from the shared build cache: tests run with `/gocache`
writable, and Go does not verify the cache entries it reads. So a branch you
test can change what your next run, of any tree, builds from. For code you do
not trust (another person's branch before your own), use a throwaway build
cache for that run, an anonymous volume removed with the container:

```sh
make test-functional-container PKGS=./internal/ntable/... FUNCTIONAL_FLAGS=--fresh-gocache
```

The tool by hand, for its flags (`--cpus`, `--memory`, `--pids`, `--scratch`,
`--grace`, `--image`, the volume names):

```sh
go run ./tools/functionalrun help
go run ./tools/functionalrun reap --dry-run    # what the reaper would remove
```
