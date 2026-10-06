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

### dep-secrets-b.w3: the secrets store and keys

The secrets dependency is [nova-secrets](SPEC-SECRETS.md): a git store of sops-sealed yaml
files, one age key per machine, and the store's `.sops.yaml` naming the recipients of each
file. The coordinator's seat needs it — `nova-up --local` seals the Redis passwords into it —
and every seat and loop that runs with a secret reads its own sealed `<seat>.yaml` from it by
name. `sops` and `age` run as programs, so they must be installed and on PATH; nothing here
is a library the tools link.

`nova-up --local` provides it. Its `binaries` step stops with the install command when
`sops`, `age` or `git` is not on PATH, and its `secrets` step makes the rest: the seat's key
under the root's `keys` directory through `nova-secrets keygen`, a bare git upstream beside
the store, the store working copy on `main` tracking it, and the `.sops.yaml` rule for the
seat's public key; `seat.env` then carries `NOVA_SECRETS_STORE`, `NOVA_SECRETS_SEAT` and
`NOVA_SECRETS_KEY`. A person on another machine does the same by hand: install `sops` (brew,
its release page, or `go install github.com/getsops/sops/v3/cmd/sops@latest`) and `age`, run
`nova-secrets keygen --as <seat> --key <path>`, clone the store to the path the seat names,
keep the key readable only by its owner (`chmod 600`; `age-keygen -y <key>` prints the
`# public key:` line the file must carry), and give the seat its file with `nova-secrets
seat add`.

The `secrets` check reads those three variables and PATH — names only, never a value, and
it prints none. It is ok when `sops` is on PATH, the key file is readable and carries the
public key comment, the store path is a git working copy, and `<seat>.yaml` is in it. Each
other answer is a fail with one fix line naming the verb or the step above: install `sops`,
set the missing variable, `chmod 600` the key, `nova-secrets keygen`, the `git clone` of the
store, or `nova-secrets seat add`. The check is fleet-scoped: `nova-doctor --local`
skips it with the other fleet checks and says which; run `nova-doctor` plain to
see it.
