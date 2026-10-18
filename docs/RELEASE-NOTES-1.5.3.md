The matching stable tools release for nova sprint v1.5.4: a hotfix to the sandbox wall's caps.

Install: download the assets for your platform from this release, then run `nova-sprint adopt <version> --source <nova-tools checkout at v1.5.3> --sprint-release <the nova-sprint v1.5.4 release downloaded> --inventory <file> --reason <text>` (see docs/SPEC-RELEASE.md).

## What changed in v1.5.3

- The `nova-sandbox` bare form sizes its caps from the machine: 64 processes per logical CPU and at least 512, never more than a quarter of the cgroup TasksMax above it; and an eighth of the machine's memory, never more than a quarter of the cgroup MemoryMax above it, at least 8 GiB and never more than half the machine (8 GiB where the memory cannot be read). Four trees at their caps fit under the member's login slice, so the slice's OOM killer does not take a sibling worker. The v1.5.1 sizing keyed both caps on 32 CPUs, so 16-thread members with 62 to 125 GiB got 512 processes and 8 GiB, and on 2026-10-17 the wall still ended about half the worker runs on hetzner1, hetzner2 and amd-3 (`runaway: 714 processes (cap 512)`, `8714186752 bytes of memory (cap 8589934592)`).
- Explicit `--max-procs` and `--max-mem` still win, and the `run` verb's caps are unchanged. No wire or record change; going back to v1.5.2 is a binary swap.
