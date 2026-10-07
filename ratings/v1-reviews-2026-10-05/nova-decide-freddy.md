# nova-decide review, 2026-10-05

Rater: inception/mercury-2.5
Build: ca8bb8c
Verdict: GOOD WITH FIXES for an AI to use
Score: 8.5/10

## Reasons
The tool follows nova standards well: verbs with clear effects, --json output, dry-run support, and structured refusal messages. The spec (docs/SPEC-NOVA-DECIDE.md) is comprehensive.

First confusion: the banner's "how it works" is dense (lines 40-45 of main.go). It lists the decision types but doesn't clearly explain the state→answer→record flow. An AI reading cold might not immediately grasp that state is the card+diff while answers are the decision output.

First doubted claim: brief decision's "converges" question. The spec says "converges is a rank, not a probability" (line 673-674 of spec), yet the output format uses p_converges=. This is misleading—either it's truly uncalibrated and shouldn't show p, or it needs to be clear that p is a rank.

The calibration section is excellent—real AUC numbers from actual review rounds. The TLA+ models for the record are properly referenced.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-NOVA-DECIDE.md line 673-674 | brief.converges is described as "a rank, not a probability" but output shows p_converges=<p> | either change the field name to converge_rank=<rank> or clarify that p is a proxy, not a calibrated probability | S |
| 2 | cmd/nova-decide/main.go lines 40-45 | banner's how-it-works paragraph is dense; doesn't clearly separate state from answer flow | add a 1-line summary: "state = card+diff+rule; backend answers schema over state; decision appended to record" | S |
| 3 | internal/decide package (see docs/SPEC-NOVA-DECIDE.md section 1) | no quickstart verb exists; a first-run AI has to write schema/state files manually | add `quickstart` that writes example schema, state, and runs ask with --dry-run | M |

## Good, keep
- The record format (JSONL with decision/outcome/act lines) is clean and supports calibration
- Output structure (OK/REFUSED/FAILED prefix with --json parity) follows nova standards
- --dry-run on all decision verbs lets AIs plan before committing
- The gate decision's flaky/caused/pre-existing classes with configurable bars
