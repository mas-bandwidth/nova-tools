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

### dep-redis-stores-b.w2: The Redis stores and their ACL users

The nova tools keep their shared state in Redis: the sprint store, the bus store and
whatever else the applied inventory names. Each store has one ACL user per role — the
coordinator, the member, the friend and the read-only table reader — rendered from the
function library and the key families by `internal/redisacl` and set with `nova-redis acl
apply`.

On one machine, `nova-up --local` provides it: the `redis` step serves one loopback Redis
through the service manager, draws a password per user, seals each in the secrets store
and applies the users with `nova-redis acl apply` under `nova-secrets exec`, so no password
is written to a file or printed. In a fleet the `redis.yml` play renders the users on the
machine running the play and checks and applies them on the store deployer
([FLEET.md](FLEET.md), "redis.yml"); a person does the same by hand with `nova-redis acl
apply`.

The doctor's `redis` check reads each store the environment names (`NOVA_REDIS_ADDR` for
the machine's Redis, `NOVA_SPRINT_REDIS` for the sprint store, `NOVA_BUS_REDIS` for the
bus store; a `mem:` address is the sprint's in-process twin and holds no ACL), dials it,
holds `redis-server` to the version floor the ACL users need, and compares the store's
live ACL with the users this build renders. A user that is missing or different is a
`fail` naming the user and the store, with the `nova-redis acl apply` line that fixes it;
a password is never printed. A machine with no Redis store named is a `warn` with the
setup step.
