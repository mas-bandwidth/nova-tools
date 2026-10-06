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

### dep-postgres-b.w2: Postgres for nova-config

nova-config keeps the fleet's rows — machines, friends, routes, loops — in a PostgreSQL
database, in schema `config`. `nova-config migrate` makes the schema and records each
migration in `config.schema_migrations`; `nova-config apply` copies the rows into Redis for
the runtime tools to read. A fleet needs it. One machine running `nova-up --local` does not,
because the sprint's twin store (`mem:<root>/stores/sprint.twin`) stands in for the fleet's
store.

A fleet provides it by exporting the address and user, then migrating:

```sh
export NOVA_PG_DSN=postgres://user@host:5432/nova
nova-config migrate
```

The password is never on the line: `NOVA_PG_PASSWORD_ENV` names the variable that holds it,
`NOVA_PG_PASSWORD` when that is unset. `nova-up` installs and runs no Postgres; a person or
the fleet does that once.

The `pg` check in `nova-doctor` runs `nova-config migrate --dry-run --json` and reads what the
store answered: it is `ok` when the store answers at the configured address as the configured
user and its schema is at the newest migration this binary carries. It fails, naming every
migration not applied, when the schema is behind, with the fix `nova-config migrate`; it
fails when the store does not answer, with the fix `nova-config status`; and with no
`NOVA_PG_DSN` set it is `ok` and names the local equivalent.
