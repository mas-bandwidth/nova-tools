# nova-tokens review, 2026-10-05

Rater: inception/mercury-2.5
Build: 3baf154b
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons
Verdict GOOD WITH FIXES because the tool is well-designed and well-documented, but it has some barriers for cold AI use. First confusion: `--scratch` is required with `--opencode` but its purpose isn't immediately clear from the help; docs/SPEC-TOKENS.md line 46 explains it's for the OpenCode database copy, but this should be in the help banner. First doubted claim: "Every path is a flag" - the spec says this, but the bus lane reading (docs/TESTS.md line 434) shows `./bus` as a relative path that has an implicit default structure.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens help` | `--scratch` flag purpose is unclear without reading SPEC-TOKENS.md | Add one-line description in usage banner explaining it's for OpenCode database copy | S |
| 2 | `nova-tokens fold --claude` | recursive walk is documented but no warning about performance cost | Add NOTE in help about recursive walk cost on large directories | S |
| 3 | `nova-tokens check --through` | help doesn't show the full exit behavior when stale | Add exit code note to help | S |

## Good, keep
- One file per day with atomic rename (rule 8 in SPEC-TOKENS.md)
- Five token types kept separate, dashes for unreported types
- The refusal grammar for unknown verbs and missing flags