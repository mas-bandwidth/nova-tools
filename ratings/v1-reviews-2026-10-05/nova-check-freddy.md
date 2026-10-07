# nova-check review, 2026-10-05

Rater: inception/mercury-2.5 (Freddy)
Build: 3baf154a3c8f
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons
The tool answers what it does clearly in its banner, and every refusal names its remedy. However, the help output for some verbs is dense and hard to scan. The JSON output option is good for automation. The dogfood ledger system is well-designed for tracking AI usage.

Confusion: The `convergence` verb has many flags with complex interactions; a cold reader would need to read SPEC-CHECK.md to understand which flags are required for a minimal run.

Doubted claim: The banner says "no defaults" but `--fail-max` has a default of 20, which is inconsistent.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `cmd/nova-check/main.go` usage banner | `--fail-max` defaults to 20 but banner says "no defaults" | either remove the default or update the banner to clarify this exception | S |
| 2 | `cmd/nova-check/main.go` usage banner | `convergence` verb lists 15+ flags without clear required/optional grouping | add explicit `(required)` markers to flags that must be provided | S |
| 3 | `internal/check/nocode.go` | Floor deny-list is embedded in binary; a user who wants to customize it must rebuild | allow loading from env var or config file, like other nova tools do | M |

## Good, keep
- The one-line refusal grammar is excellent: "refusing to guess" is a clear signal.
- The `--fail-max` cap with MORE line is perfect for bounded output.
- Dogfood ledger tracks AI usage systematically.
