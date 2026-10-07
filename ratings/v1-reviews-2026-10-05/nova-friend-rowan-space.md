# nova-friend review, 2026-10-05

Rater: Mercury (inception/mercury-2.5)
Build: 3989818f758
Verdict: NOT YET for an AI to use
Score: 4/10

## Reasons
This tool was deprecated in v1.0.0 and moved to deprecated/cmd/nova-friend/. The README in deprecated/docs/nova-friend/ exists but the banner in main.go (line 26) references docs/nova-friend/README.md which doesn't exist at v1.0.0. The tool has clear help and refusal patterns but requires Redis to function properly, and there's no standalone test mode documented. A first-time user would be confused by the deprecation status.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | deprecated/cmd/nova-friend/main.go:26 | banner references docs/nova-friend/README.md but only deprecated/docs/nova-friend/README.md exists | update banner to reference deprecated/docs/nova-friend/README.md | S |
| 2 | deprecated/cmd/nova-friend/main.go | no docs/CLI.md entry for nova-friend (tool deprecated) | move any remaining docs to deprecated/docs/ | S |
| 3 | deprecated/cmd/nova-friend/common.go | no dry-run mode for list/show verbs without Redis | add --dry-run flag that prints schema-only output | M |

## Good, keep
- Clear refusal messages that name the problem and the remedy
- Consistent exit codes (0 done, 1 refused, 2 usage)
- Well-structured friendView for listing and showing friend state
