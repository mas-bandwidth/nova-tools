# nova-config review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9
Verdict: GOOD WITH FIXES for an AI to use
Score: 8/10

## Reasons
The tool is well-structured for AI use: help is accessible (`nova-config help`, `<verb> -h`), exit codes are clear (0=done, 1=refused, 2=usage), and refusals name their remedies. The grammar is generated from descriptors (docs/SPEC-CONFIG.md, internal/config/kind.go), so all kinds share identical verbs. However, two issues prevent a full score: (1) the `apply` verb doesn't accept `--redis` for dry-run testing against a scratch store, forcing use of `--check` which doesn't show Redis writes; (2) the `--as` friend flag is required on every write but isn't consistently documented in the help examples (internal/config/kind.go:53-62, main.go:65-71).

The first confusion came at docs/CLI.md:64 where `nova-config help` is described as "the whole banner" but the actual usage shows more example commands. The first doubt is the apply/redis asymmetry at internal/config/apply.go where apply reads Postgres and writes Redis but doesn't expose `--redis` for the kind verbs, making scratch testing harder.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/config/apply.go:apply | `apply` accepts `--check` to print the plan but offers no `--dry-run` mode that simulates Redis writes against a test store; an AI cannot safely verify what apply will do without a real Redis connection | add `--dry-run` flag that parses Postgres, derives rows, plans changes, but writes to stdout instead of Redis; or document that `--check` + manual verification is the intended flow | S |
| 2 | cmd/nova-config/main.go:65-71 | help output and examples omit that `--as` is required on every write (`add`, `set`, `remove`); an AI trying `nova-config machine add studio --user glenn` without `--as` gets exit 2 with no example showing the flag | add `--as <friend>` to usage examples and flag help text; document env NOVA_FRIEND as fallback | S |
| 3 | docs/SPEC-CONFIG.md:217-220 | the `apply` plan mentions `Derive` for friend roles but doesn't explain that `coordinator` is added to a friend's Redis roles only at apply time, not stored; an AI managing friends may not understand why `friend:<f>:roles` in Redis differs from Postgres | add a section "Derived roles" explaining that sprint.coordinator → friend.roles is computed at apply, not persisted | S |

## Good, keep
- The generated CLI grammar: all kinds share identical add/set/remove/list/show/history verbs, making the tool predictable for AI use (internal/config/kind.go:45-90).
- The refusal pattern: exit 1 for store refusals with a "run: <next>" remedy, exit 2 for usage errors (cmd/nova-config/main.go:235-249).
- The history mechanism: every write is recorded in config.history with before/after/actor/at in the same transaction (docs/SPEC-CONFIG.md:196-201).