# Testing

nova-tools is one Go module. Run builds and tests on a build machine, from the
repository root, using the Go version in [go.mod](../go.mod) or a compatible newer
version. Keep the job's working directory and Go cache separate from other jobs.

## Build a tool

From the repository root, give the output its installed name:

```sh
build_dir=$(mktemp -d)
go build -o "$build_dir/nova-bus" ./bus
"$build_dir/nova-bus" help
```

Use the same pattern for `config`, `sandbox`, `secrets`, `reserve` and `cairn`, naming
outputs `nova-config`, `nova-sandbox`, `nova-secrets`, `nova-reserve` and
`nova-cairn`.
Keep build outputs outside the checkout.

To build the current binaries together:

```sh
build_dir=$(mktemp -d)
for tool in bus config sandbox secrets reserve cairn; do
    go build -o "$build_dir/nova-$tool" "./$tool" || exit
done
"$build_dir/nova-bus" help
```

Call the resulting absolute path while trying a build. An unqualified command
may find an older installed binary on PATH. `go build ./...` checks compilation;
it does not leave these named executables behind.

## CI checks

[CI](../.github/workflows/ci.yml) checks formatting, builds the tree, vets normal
and tagged files, checks compilation for Windows, runs unit and functional
tests, runs the TLA+ models, and checks the Ansible plays. CI sets `NOVA_TEST_NO_HOST=1` for every job.
Its Go checks are:

```sh
gofmt -l .                                  # no filenames means clean
go build ./...
go vet ./...
go vet -tags functional,slow ./...
GOOS=windows go vet ./...
go test -p 4 -exec "env PATH=/nonexistent HOME=/nonexistent" ./...
go test -count=1 -p 4 -tags functional,slow ./...    # uncached: runs outside binaries
```

Set `NOVA_TEST_NO_HOST=1` in your environment when running these checks by hand.
The Windows vet command checks compilation; it does not run Windows tests.
The unit jobs run on Linux and macOS, and the functional jobs run on Linux and on macOS (mini-m5, `functional-macos`). The macOS job installs nothing: its PostgreSQL comes from `nova-fleet runner-packages` (fleet/README.md), and the job refuses, naming that play, when `initdb` is missing.
Top-level `ci-ok` requires every job to succeed. Fork pull requests do not run
on the repository's self-hosted machines.

`tests/boundary` checks layout, imports, dependencies and the line budgets in
`tests/BUDGET`, and refuses a unit test that calls real time, a socket, the network or another
program (`TestUnitTestsArePure`); it runs as part of `go test ./...`. `tests/contract` builds
each tool and checks help, unknown verbs and flags, missing inputs and allowed exits; it builds
and runs programs, so it is a functional test (`-tags functional`).
Neither needs a live store. These tests cover the named rules, not every
claim in a guide.

## Dependencies and skips

Unit tests use fake stores, clocks and process runners for the operations they
check. Functional tests use disposable directories, processes and services;
they do not use the fleet's live stores.

| Package | Functional prerequisites |
|---|---|
| `bus` | `redis-server` on PATH |
| `config` | PostgreSQL tools, including `initdb` and `postgres`; run as an ordinary user |
| `secrets` | `sops`, `age-keygen` and `git` on PATH |
| `sandbox` | Linux with Landlock for real enforcement checks |
| `reserve` | OS file locks and child processes on the tested platform |

The bus, config and secrets tests can skip when a dependency is missing. Use
`go test -v` when checking coverage and report the skips along with the result;
a passing command with skipped cases does not establish those cases. CI installs
Redis and PostgreSQL for its functional job; availability of sops and age still
depends on the runner.

For a focused check, replace `./...` with the package, such as `./bus`. Do not report a successful build as
evidence that a sandbox backend or a fleet installation works on a machine.

## Fault contract

`tests/contract` also holds the fault contract, a functional-tier test (build tag
`functional`, rows run in parallel, a few seconds in all). Each row in
`tests/contract/faults_<tool>_test.go` provokes one real fault (Redis stopped,
a missing store, a read-only directory, a corrupt file, a child killed by a signal,
a fake `ansible-playbook` on PATH, a binary missing from PATH, a bad DSN, an unset
variable) and holds the tool to the same rules: a non-zero exit, exactly one stderr
line with a reason and a `run: <command>` remedy, and empty stdout unless the row
documents otherwise. The remedy is extracted and run where it has no `<placeholder>`,
to prove it pasteable. A row that fails today carries `Known: "<gap #>"` (the number
in the audit's ranked gaps); it is skipped with the printed reason and fails the build
the day it passes, so a fix PR deletes the `Known` and the row becomes the gate.
`lint_test.go` in the same package refuses a `_ =` on an error without an `ignored:`
comment, a non-zero exit with nothing written to stderr before it, and a `no_log:`
task in `fleet/` with no receipt task; its known counts are lowered the same way.
Run it with `go test -tags functional -run 'Faults|Lint' -v ./tests/contract`; it
needs `redis-server`, PostgreSQL's `initdb` and `psql`, `sops`, `age-keygen` and `git`,
and a row whose dependency is missing is counted and skipped, or fails when
`NOVA_CONTRACT_STRICT=1` is set (the CI functional job sets it). A row that changes a
mode skips when run as root.

## Models

Bus delivery, reservations and cairn records have TLA+ models beside their
code. The examples below assume the jar is at `/opt/tla/tla2tools.jar`; use
your own absolute path. Run every model, or name the model directories:

```sh
tests/tla/check.sh /opt/tla/tla2tools.jar
tests/tla/check.sh /opt/tla/tla2tools.jar bus/tla reserve/tla cairn/tla
```

The runner requires Java, Bash, GNU `timeout` and `xargs`, and the jar matching
[tests/tla/tla2tools.sha256](../tests/tla/tla2tools.sha256). Use an absolute jar path because
each case runs in a temporary copy of its model directory. CI takes that path
from the repository variable `TLC_JAR`.

`CASES.tsv` names each configuration, module, expected result, property and
deadlock mode. A normal case must pass. A broken case changes the model on purpose so that a
property fails, and it must fail as its row says. Without a case table, a directory
must contain one model; configurations containing `Broken` must produce a
violation and the others must pass. A timeout fails the run. The default limit
is 600 seconds per case, settable with `TLC_TIMEOUT`.

## Each tool

Each tool's `SPEC.md`, beside its README, says what its tests and models
cover: [bus](../bus/SPEC.md), [cairn](../cairn/SPEC.md),
[config](../config/SPEC.md), [reserve](../reserve/SPEC.md) and
[sandbox](../sandbox/SPEC.md).

## Fleet checks

CI installs ansible-core in a fresh virtual environment and installs the
collections in [fleet/requirements.yml](../fleet/requirements.yml). It then
runs these commands from the repository root:

```sh
for play in fleet/*.yml; do
    [ "$play" = fleet/requirements.yml ] && continue
    ansible-playbook -i fleet/ci/inventory --syntax-check "$play"
done
ansible-playbook -i fleet/ci/inventory fleet/services.yml --check --diff </dev/null
```

The [CI inventory](../fleet/ci/inventory) supplies a local stub host. These
checks cover parsing and a check-mode plan for the programs machines keep
running or run on a schedule. An installation or service adoption needs its own evidence on the selected hosts.

Before applying a play yourself, inspect its inventory and variables. The
[fleet guide](../fleet/README.md) describes check mode and the effects of each play.
Record the release tag and hosts when reporting an installation.
