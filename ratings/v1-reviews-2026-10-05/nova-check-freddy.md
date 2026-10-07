# nova-check review, 2026-10-05

Rater: inception/mercury-2.5 (Freddy)
Build: 3baf154bb6ef
Verdict: GOOD WITH FIXES for an AI to use
Score: 8/10

## Reasons
The tool has a clear banner that answers what it does and how it works. Every refusal names the missing flag and what it wants. The `--fail-max` cap prevents output flooding. However, the help text is dense for some verbs (especially `convergence` with 15+ flags) and the `--fail-max` default conflicts with the "no defaults" claim in the banner.

Confusion: The `convergence` verb has many flags without required/optional markers; a cold reader needs docs/SPEC-CHECK.md to know which are mandatory.

Doubted claim: The banner says "There are no defaults" but `--fail-max` defaults to 20, creating inconsistency.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `cmd/nova-check/main.go` banner text | Says "no defaults" but `--fail-max` has default 20 | Either remove default or add "except --fail-max" clarification | S |
| 2 | `cmd/nova-check/main.go` usage help | `convergence` verb lists 15+ flags without required markers | Add `(required)` markers to mandatory flags | S |
| 3 | `internal/check/nocode.go` | Deny-list is hardcoded; no way to customize without rebuild | Support env var or config file like other nova tools | M |

## Good, keep
- One-line refusal grammar is clear and consistent
- `--fail-max` with MORE line bounds output effectively
- All paths come from flags; nothing is guessed
