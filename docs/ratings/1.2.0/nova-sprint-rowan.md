# nova-sprint READ and USE rating, nova-tools 1.2.0

Rater: mercury-2.5 under opencode
Build: 17ec8d256a04943b921987ebd9c19458917b19e9
READ: 8/10
USE: 7.5/10

nova-sprint: a sprint of work cards, dealt to a fleet of workers and read before they land. The tool has many verbs for card management, sprint control, and member coordination.

## Reasons

READ. The CLI.md has a complete first-run example using --redis mem:<file> for learning without Redis. The verbs are documented in the CLI reference. The spec is SPEC-SPRINT.md. What holds the score: there are 40+ verbs and the help output is very long with no grouping.

USE. The in-memory twin mode (--redis mem:<file>) works for learning. The tick system is clear. What holds the score: the number of verbs is overwhelming, and there is no way to see all available flags without running each verb with -h.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-sprint/verbs.go | the verb list has 40+ verbs without any grouping in help | add verb groups like sprint, cards, member, fleet | M |
| 2 | cmd/nova-sprint/main.go | no --short help option to get a concise summary | add --short flag to show only major verbs | S |
| 3 | docs/CLI.md#nova-sprint | no reference table of exit codes | add exit codes section to CLI docs | S |
| 4 | cmd/nova-sprint/main.go:help | no verb autocomplete in shell | add shell completion script | M |

## Good, keep

- Clear first-run example using in-memory twin mode.
- Full sprint lifecycle is documented (init, add, take, finish, read, merge).
- --redis mem:<file> makes learning without Redis possible.

## Compared with earlier ratings

This tool was not rated in 1.1.0. The first rating shows a complex but functional sprint coordinator.
