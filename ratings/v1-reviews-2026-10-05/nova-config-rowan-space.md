# nova-config review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9
Verdict: GOOD WITH FIXES for an AI to use
Score: 8/10

## Reasons
The tool is well-structured for AI use: help is accessible (`nova-config help`, `<verb> -h`), exit codes are clear (0=done, 1=refused, 2=usage), and refusals name their remedies. The grammar is generated from descriptors (docs/SPEC-CONFIG.md, internal/config/kind.go), so all kinds share identical verbs. However, one issue prevents a full score: the `add`, `set`, and `remove` verbs have no preview mode, so an AI cannot verify what will be written before making changes.

The first confusion came at `nova-config friend add` where the error message didn't show what fields would be stored. The first doubt is that there's no way to dry-run a write to verify correctness against a scratch store.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-config/main.go:runKindAdd/runKindSet/runKindRemove | `add`, `set`, and `remove` verbs write immediately with no preview; an AI cannot safely verify changes against a scratch store before making them | add `--dry-run` flag that prints the planned CONFIG REMOVE/SET/ADD output without writing to the store | S |

## Good, keep
- The generated CLI grammar: all kinds share identical add/set/remove/list/show/history verbs, making the tool predictable for AI use (internal/config/kind.go:45-90).
- The refusal pattern: exit 1 for store refusals with a "run: <next>" remedy, exit 2 for usage errors (cmd/nova-config/main.go:235-249).
- The history mechanism: every write is recorded in config.history with before/after/actor/at in the same transaction (docs/SPEC-CONFIG.md:196-201).
