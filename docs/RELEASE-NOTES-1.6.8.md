The stable tools point release after v1.6.7. It adds native Windows fleet members: a machine row's `gates_only` field, and the Windows process support nova-swarm needs to run a member there. The deal that reads `gates_only` ships in nova-sprint v1.6.6.

Install: download the assets for your platform from this release, then run `nova-update release install --from <release root> --version v1.6.8 --bin <dir>` with the nova-sprint v1.6.6 binaries staged beside them (see docs/SPEC-RELEASE.md). Run `nova-config migrate` once before nova-sprint v1.6.6 runs a fleet sync, because migration 0047 adds the column it reads.

## What changed in v1.6.8

- nova-config's machine row has a new field, `gates_only`: a comma list of GATE-NEEDS words (such as `windows`) that alone bring work to the sprint member on that machine. When it is set, the member is dealt only the cards whose GATE-NEEDS names one of them. It is never dealt a generic card or a read. Migration 0047 adds the column, and `config.Widths` carries it to `nova-sprint fleet sync`. Set it with `nova-config machine set <m> --gates_only windows`. (#6021)
- nova-swarm on Windows (#6021):
  - The disk floor reads `GetDiskFreeSpaceExW`. Before this, a member on Windows refused every card because it had no free-disk number.
  - A native child runs at `BELOW_NORMAL_PRIORITY_CLASS`.
  - `member` and `native` put themselves in a Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, so a stop or a crash ends every child.
  - The unix paths are unchanged.
- Windows unit tests cover all three. They passed natively on a Windows bench, and Linux CI type-checks them through `make vet-windows`. (#6021)
- The release publishes windows_amd64 assets for nova-swarm, nova-hook and nova-config.
