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

### dep-providers-bb.w3: the model providers and their routes

Every nova-config route names a provider and a model, and the sprint's friends and native
runs spend through them: the deal draws a route per card, the harness launches with that
provider's key, and the balance poll rests a provider whose funds reach zero
([SPEC-SPRINT.md](SPEC-SPRINT.md), `internal/sprint/provider_funds.go`). A route whose
provider has no key, or whose provider is out of funds, is a hidden dependency: the tool
runs until a take is refused, and the refusal is a mystery to a person who did not set the
route up.

Who needs it: any machine that runs cards on metered routes (a fleet member, a
coordinator's own runs). `nova-up --local` writes a first sprint's store and seat but makes
no nova-config and seals no provider key, so it does not provide this dependency; a person
adds the routes and seals each provider's key by hand:

```sh
nova-config route add pro-a --tier pro --provider deepseek --model deepseek-v4 --tokens 400000 --deadline 1800
nova-config apply
nova-secrets seal --store <store> --as <seat> --key <path> --sops <path> --name DEEPSEEK_API_KEY
```

The doctor check `providers` (`internal/doctor/check_providers.go`) reads every route
(`nova-config route list --json`) and the seat's secrets names, and for each enabled route
holds three facts: the provider's key is in the secrets store by name, the provider answers
a no-cost call (its models list), and the funds it reports, where it reports them, are over
zero. A disabled route is listed with its note and never checked; a route that would fail
is named in the evidence, with the one fix line (seal the key, or `nova-sprint funded <provider> --reason '<the payment>'`). The check is one only a fleet needs, so
`nova-doctor --local` skips it and a fleet run includes it.

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

### dep-tailnet-b.w4: the tailnet

A fleet's machines reach each other and the stores only over its tailnet (tailscale): the
tools refuse an address that is neither loopback nor a tailnet address, so a machine with no
tailnet cannot be named or reached by the others. A single-machine `nova-up --local` setup
does not need one: it uses a twin store and a loopback Redis on the one machine.

`nova-up` does not join a tailnet, because joining needs an account and a login prompt a
person answers; a person installs tailscale and runs `tailscale up` once on each fleet
machine, so it is logged in and named on the tailnet. On the coordinator's machine the
inventory is then read with the seat loaded (`set -a; . ~/nova/seat.env; set +a`).

The `tailnet` check is fleet only, so `nova-doctor --local` skips it and lists it among the
skipped checks. Without `--local` it fails when tailscale is not installed or did not answer,
when its backend state is not `Running`, when this machine has no name on the tailnet, or when
a machine of `nova-config machine list` is not named on the tailnet; the evidence names each
missing machine and the fix line runs `tailscale up` on it. The check passes when tailscale is
up, this machine is named, and every inventory machine answers on the tailnet.

### sprint-local-only-mode-r-bb.w1: local-only mode

A single-machine setup with no tailnet uses `NOVA_SPRINT_LOCAL=1` (or a sprint row setting with
the same name) to run in local-only mode. In local-only mode every address must be loopback;
tailnet and other addresses are refused with a reason naming the mode. No tailnet is needed.

`nova-up --local` sets local-only mode by default. `nova-doctor --local` skips the tailnet check
and names this mode as the reason.

The `tailnet` check is skipped under local-only mode, just as `nova-doctor --local` skips it.
