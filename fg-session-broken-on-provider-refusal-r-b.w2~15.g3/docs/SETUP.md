# Setting up nova

This guide takes a machine from nothing to a first sprint using only these docs and the tools.
Each section is one way to set up; the first is one machine with no config.

## One machine: nova-up --local

`nova-up --local` sets up everything a first sprint needs on the machine it runs on, under one
root (`~/nova`, or `--root <dir>`). The design is [SPEC-UP.md](SPEC-UP.md).

1. Install the programs it runs. On darwin:

   ```sh
   brew install git redis sops age
   go install ./cmd/...   # in a checkout of this repository: nova-up and the nova tools
   ```

   On linux, `sudo apt-get install -y git redis-server age`, sops from its release page or
   `go install github.com/getsops/sops/v3/cmd/sops@latest`, and the nova tools as above.

2. See the plan. A dry run prints one line per step and writes nothing:

   ```sh
   nova-up --local --dry-run
   ```

   Each line is `UP <step> <ok|create|change|missing> <detail>`. A `missing` line names the
   command that provides what is missing; run it, then run the dry run again.

3. Apply it:

   ```sh
   nova-up --local
   ```

   It makes the root, the sprint's twin store, a secrets store for the coordinator's seat, a
   loopback Redis run by the service manager with its passwords sealed in that store, the
   seat file, and a smoke: one card taken from added to landed on a twin of its own. The last
   line names the seat file.

4. Run it again. A second run prints `ok` on every line and `UP UNCHANGED`; it changes nothing.

5. Start the first sprint as the coordinator:

   ```sh
   set -a; . ~/nova/seat.env; set +a
   nova-sprint where
   ```

   The coordinator's day is [SPRINT-COORDINATOR.md](SPRINT-COORDINATOR.md); its seat is
   [SPRINT-COORDINATOR-SEAT.md](SPRINT-COORDINATOR-SEAT.md).

What `--local` never touches, and how each step decides it has nothing to do, is in
[SPEC-UP.md](SPEC-UP.md).

## Checking the setup

### setup-nova-doctor-r-r5.w1: nova-doctor

When anything is wrong, run `nova-doctor` first. It runs every check, prints one
`DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]` line each, and gives the one fix
line for each thing that is not ok. It changes nothing.

```
nova-doctor            every check
nova-doctor --local    skip the checks only a fleet needs, and say which
nova-doctor --check self --json
```

Exit 0 is all ok, 1 a warn under `--strict`, 2 a fail. The first check, `self`, finds the
nova tools on PATH and fails when they are not one release, naming the odd one. The
contract is [SPEC-DOCTOR.md](SPEC-DOCTOR.md).

### dep-go-sdk-b.w2: the Go toolchain

The Go SDK is a dependency of a bench, and never of the coordinator's machine. A bench
builds and tests this repository (`go build ./...`, `go test`), which needs the Go version
`go.mod`'s `toolchain` line names, a writable `GOCACHE`, and `GOFLAGS=-mod=readonly`. The
coordinator's machine builds and tests nothing, so no `go` runs there: the bench rule.

`nova-up --local` sets up the coordinator's machine and installs no Go on it. Its
`binaries` step checks the programs the tools run and names the install command for a
missing one; the Go toolchain is not among them, because the coordinator installs the
released tools instead of building them. On a bench, a person installs the toolchain
`go.mod` names (the archive from <https://go.dev/dl/> or the distribution package at that
version) and exports `GOFLAGS=-mod=readonly` with a private `GOCACHE`, as every card does.

The `gosdk` check reads the role from the machine's seat. On the coordinator's machine,
`go` on PATH is a warn naming the bench rule, with the released install as its fix; with
no `go` there it is ok. On a bench it fails when `go` is absent, when its version is not
`go.mod`'s toolchain version, or when `GOCACHE` is not writable, and warns when `GOFLAGS`
does not carry `-mod=readonly`. Each fix line names the step above.
