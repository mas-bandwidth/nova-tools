The matching stable tools release for nova sprint v1.5.2.

Install: download the assets for your platform from this release, then run `nova-sprint adopt v1.5.2 --source <nova-tools checkout at v1.5.1> --sprint-release <the nova-sprint v1.5.2 release downloaded> --inventory <file> --reason <text>` (see docs/SPEC-RELEASE.md).

## What changed in v1.5.1

Worker sandboxes are sized to the machine, so ordinary parallel builds are no longer killed as runaways (about 90 worker children ended exit 137 on 2026-10-17 under the old fixed caps).

- `nova-sandbox` bare form takes `--max-procs` and `--max-mem`. With neither it sizes the wall to the machine: 1024 processes on 32 or more CPUs, else 512; 16 GiB on 32 or more CPUs with 64 GiB or more, else 8 GiB; never above half the machine's memory. The old fixed caps were 256 processes and 8 GiB.
- `nova-swarm` hands every card's child `GOFLAGS=-p=4`, `MAKEFLAGS=-j8` and `CMAKE_BUILD_PARALLEL_LEVEL=8` (GOMAXPROCS untouched).
- Card tiers: only a card whose NEW: names a new `.tla` module needs the heavy tier.
