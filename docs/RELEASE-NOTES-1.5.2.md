The matching stable tools release for nova sprint v1.5.3: a hotfix to one bound.

Install: download the assets for your platform from this release, then run `nova-sprint adopt v1.5.3 --source <nova-tools checkout at v1.5.2> --sprint-release <the nova-sprint v1.5.3 release downloaded> --inventory <file> --reason <text>` (see docs/SPEC-RELEASE.md).

## What changed in v1.5.2

- A nova table holds 1024 properties at an epoch, not 64: `ntable.LimitTableProps`, the function library's `T.limits.table_props` and docs/SPEC-NOVA-TABLE.md move together. The sprint store's work table holds one `base_landed.<sha>` property per repo and base; on 2026-10-17 it reached 65 and every tick drain was refused with `properties per table: bound 64, observed 65`. The properties one manifest sets stay at 64.
- No wire or record change; going back to v1.5.1 is a binary swap and a library load, as long as no table holds more than 64 properties.
