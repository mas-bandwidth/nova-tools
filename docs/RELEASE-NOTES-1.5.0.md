The matching stable tools release for nova sprint v1.5.0.

Install: download the assets for your platform from this release, then run `nova-sprint adopt v1.5.0 --source <nova-tools checkout> --sprint-release <the nova-sprint v1.5.0 release downloaded> --inventory <file> --reason <text>` (see docs/SPEC-RELEASE.md).

## What changed in v1.5.0

The tools read their records in both the legacy form and the new schema-table form, and count each read by form. Writers still write the legacy form, so a machine on v1.4.8 still reads everything a v1.5.0 machine writes.

- Dual readers, counted per form: the bus envelope (`nova-bus recv`, and the new `nova-bus show`), the friend pong, the hook's ack, beat and lease/proof stamps, the seat hook, and the nova-config rows and their Redis view.
- Table definitions with generated wire code and golden tests for the bus envelope, pong, ack/beat/hook stamp, config rows, friend files, decide, ledgers, models, release, seat and tokens, and swarm records.
- The binary bus envelope writer exists but is off by default, and nothing turns it on.
- Behaviour change: `nova-bus send` refuses a message that breaks a schema bound, with exit 2 and the reason on stderr, before writing anything. New `--card` and `--head` flags.
