# nova-memory review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9
Verdict: GOOD for an AI to use
Score: 9/10

## Reasons

This tool is excellent for AI use. The help banner (line 1-128 in main.go) answers what it does, how it works, and how to use it with a runnable example block. The refusal messages name what each flag wants with hints (rootHint, channelsHint, kHint at lines 136-142). Exit codes are documented (0=ran & passed, 1=ran & failed, 2=bad invocation). The first run section in docs/CLI.md (lines 343-373) provides copy-paste commands that actually execute.

First confusion: the `quickstart` verb is called a "demonstration" but is the recommended first run; this is clarified in the banner but could be clearer.

First doubted claim: "no default channel or default k" — the tool intentionally has no defaults, which is correct for preventing guesswork but requires the AI to make explicit choices every time.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory help` output (lines 43-128 in main.go) | The `verify` and `eval` verbs have their required flags split across two lines in the usage, which may cause copy-paste errors | Join the two-line flag descriptions into single lines using backslash continuation | S |

## Good, keep

- The refusal hints (rootHint, channelsHint, kHint) that explain what each required flag is and wants
- The `CAL` (calibration) output that shows unrelated probe scores, giving context to HIT scores
- The `class=` field on receipts distinguishing evidence types (notes vs logs)