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
