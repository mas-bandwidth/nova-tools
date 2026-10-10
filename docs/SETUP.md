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

   The coordinator's day is [SPRINT-COORDINATOR.md](https://github.com/mas-bandwidth/nova-sprint/blob/main/docs/SPRINT-COORDINATOR.md); its seat is
   [SPRINT-COORDINATOR-SEAT.md](https://github.com/mas-bandwidth/nova-sprint/blob/main/docs/SPRINT-COORDINATOR-SEAT.md).

What `--local` never touches, and how each step decides it has nothing to do, is in
[SPEC-UP.md](SPEC-UP.md).

## Checking the setup

### setup-nova-doctor-r-r5.w1: nova-doctor

When anything is wrong, run `nova-doctor run` first. It runs every check, prints one
`DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]` line each, and gives the one fix
line for each thing that is not ok. It changes nothing.

```
nova-doctor run                     every check
nova-doctor run --local             skip the checks only a fleet needs, and say which
nova-doctor run --check self --json
```

Exit 0 is all ok, 1 a warn under `--strict`, 2 a fail. The first check, `self`, finds the
nova tools on PATH and fails when they are not one release, naming the odd one. The
contract is [SPEC-DOCTOR.md](SPEC-DOCTOR.md).

### dep-providers-bb.w3: the model providers and their routes

Every nova-config route names a provider and a model, and the sprint's friends and native
runs spend through them: the deal draws a route per card, the harness launches with that
provider's key, and the balance poll rests a provider whose funds reach zero
([SPEC-SPRINT.md](https://github.com/mas-bandwidth/nova-sprint/blob/main/docs/SPEC-SPRINT.md), `internal/sprint/provider_funds.go`). A route whose
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

### dep-harnesses-b.w7: the friend harnesses

Each friend row names a harness (`claude`, `opencode`, `codex`, or `grok`) and its mode; a
Claude one-shot row also names `config_dir`. This is a hidden dependency because a friend
cannot take a card when its harness binary or account directory is absent.

Who needs it: every machine that runs cards with friends. `nova-up --local` does not install
third-party harnesses or create their account directories. A person installs the harness on
PATH and provisions each row with the nova-friend verb:

```sh
nova-friend install --as <friend> --harness <h> --dir <friend-dir> --config-dir <claude-config-dir>
```

The `harness` doctor check asks each row's harness for `--version` without running it against
a model, and checks the version is supported and the Claude one-shot `config_dir` exists. A
failure names the friend and the fix line runs `nova-friend install` with the needed paths.

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

### dep-secrets-bb.w4: the secrets store and keys

The secrets dependency is [nova-secrets](SPEC-SECRETS.md): a git store of sops-sealed yaml
files, one age key per machine, and the store's `.sops.yaml` naming the recipients of each
file. Every seat and every loop that runs with a credential opens its own `<seat>.yaml` from
the store by name — `nova-up --local` seals the coordinator's Redis passwords into it, and a
fleet's loop records name the provider and store keys their commands read — so a missing key,
a key readable by anybody, or a `sops` that is not installed is a hidden dependency: the
command starts and fails later with a provider's or a store's error.

`nova-up --local` provides it on the one machine it sets up. Its `binaries` step stops with
the install command when `sops`, `age` or `git` is not on PATH, and its `secrets` step makes
the rest: the seat's age key through `nova-secrets keygen`, a bare git upstream beside the
store, the store working copy on `main` tracking it, and the `.sops.yaml` rule for the seat's
public key; `seat.env` then carries `NOVA_SECRETS_STORE`, `NOVA_SECRETS_SEAT` and
`NOVA_SECRETS_KEY`. A person on another machine does the same by hand: install `sops` (brew,
its release page, or `go install github.com/getsops/sops/v3/cmd/sops@latest`) and `age`, run
`nova-secrets keygen --as <seat> --key <path>`, clone the store to the path the seat names,
keep the key readable only by its owner (`chmod 600` in a `chmod 700` directory), and give the
seat its file with `nova-secrets seat add`.

The `secrets` check (`internal/doctor/check_secrets.go`) reads the three variables and PATH,
and prints names and modes only, never a value. It is ok when `sops` is on PATH, the key file
is readable at mode `0600` in a directory at mode `0700` and carries the `# public key:` line
`age-keygen` writes, the store is a git working copy with its `.sops.yaml`, and every secret
name the seat's enabled loop records require (`nova-config loop list --json`, the `keys` of
each row whose `seat` is this machine's) appears in the store's own listing
(`nova-secrets names`, which takes no key). Any other answer is a fail with one fix line
naming the verb or the step above: install `sops`, set the missing variable, `chmod 600` or
`chmod 700` the key, `nova-secrets keygen`, the `git clone` of the store, or `nova-secrets seal --store <store> --as <seat> --name <NAME>` for a name the loops require and the store
does not hold. The check is fleet-scoped: `nova-doctor --local` skips it with the other fleet
checks and says which; run `nova-doctor run` without `--local` to see it.

### dep-ssh-b.w8: ssh between the coordinator and the benches

The coordinator and the fleet's members reach the benches by ssh: the land, the sandbox
worktrees and the bench rule all start `ssh <bench>` on a machine that must answer without a
prompt. A host key that is not known, no key the bench accepts, or a bench that does not
answer is a hidden dependency: the card or the land stops with ssh's own error, and a person
who did not set the fleet up cannot tell what is missing.

Who needs it: any machine that reaches a bench (a fleet member, the coordinator's runs). A
single-machine `nova-up --local` setup needs no ssh to another machine, so its `ssh` step
makes this machine's side alone: it makes `~/.ssh` at mode `0700` and an empty `known_hosts`
at mode `0600` when they are absent, and it copies no key. A person fills the rest by hand:

```sh
ssh-keyscan <bench> >> ~/.ssh/known_hosts   # one line per bench: the host key is known
ssh-copy-id <bench>                        # this machine's public key reaches the bench
```

With both, `ssh -o BatchMode=yes -o ConnectTimeout=5 <bench> true` answers with no prompt.

The `ssh` doctor check (`internal/doctor/check_ssh.go`) reads the inventory
(`nova-config machine list`, with the seat loaded: `set -a; . ~/nova/seat.env; set +a`) and
runs that probe once per machine, each bounded by the check's own deadline. It names every
bench whose probe fails and the reason (unknown host key, no key or permission denied,
timeout), with the one fix line above. It is fleet-only, so `nova-doctor --local` skips it
and says which; run `nova-doctor run` without `--local` to include it.

### sprint-dashboard-verb-r-b.w7: the sprint dashboard

The sprint dashboard is `nova-sprint dashboard`, run as a loop record on the
coordinator's machine, never a hand-written launchd or systemd unit. Add the record, apply
it, and the fleet's loops play writes its unit:

```
nova-config loop add sprint-dashboard --machine <m> --argv '["env","NOVA_SPRINT_SERVER=127.0.0.1:6390","nova-sprint","dashboard","--logo","<home>/sprint-logo.svg"]' --keepalive true --as <name>
```

It serves the page on `127.0.0.1:7390` and reads the sprint once a second whether or not
a page is open; `--logo` is optional. The contract is
[SPEC-SPRINT-DASHBOARD.md](SPEC-SPRINT-DASHBOARD.md); the loop record and its play are in
[FLEET.md](FLEET.md). `nova-doctor --check dashboard` says `ok` when the loop record's unit
is installed and its loopback port answers, `warn` when no loop record runs the dashboard
on this machine or a hand unit serves it, and `fail` when the unit is there and the port
does not answer. It is a fleet check: `--local` skips it.

### sprint-dashboard-verb-r-b.w8: the dashboard's loopback check and logo type

`nova-doctor --check dashboard` fails, not passes, when the loop record runs
`nova-sprint dashboard` but its `--listen` names no loopback address: the fix line names
the loop record and a loopback address to add. The logo routes type the image by its magic
bytes before its file name, so a WebP image named `logo.png` is served as `image/webp`.

### sprint-dashboard-verb-r-b.w9: the dashboard fixes carried onto the current base

The doctor check requires the loop record's dashboard to answer on loopback, and the
logo routes detect raster content from its bytes before the file name. Tests:
`TestDashboardCheck` (internal/doctor) and
`TestDashboardServesWhatServerPyServedFromOnePoller` (internal/sprintdash).
