# nova-bus READ rating, nova-tools 1.2.0

Rater: a friend
Build: c76fcb249cc1

## Reasons

Read cold: nova-bus help, every verb's -h, docs/SPEC-BUS.md, then cmd/nova-bus/main.go from main into supporting packages. The help is excellent - banner answers three core questions (what, how, how to use), exit table stated clearly, every verb has effect line. The spec is well-structured with clear semantics (at-least-once delivery, tokens for safe retries, inbox push proof requirement). Code structure is clean with 1000+ lines in main.go.

The tool is strongest at its help surface and refusal grammar - every refusal names the problem, the value wanted, and a concrete remedy. The JSON output works uniformly across all verbs. The dry-run flag works for most verbs when redis is provided.

What holds back a 10: dry-run still requires --redis before it can refuse gracefully, which is confusing for users trying to test flags. The inbox push proof requirement (deaf names) is well-explained but adds complexity.

A 10 would need: dry-run to work without --redis (read the flag before refusing), and clearer separation between "redis not configured" vs "missing friends/machines in config".

## Findings

## Good, keep
- The banner answers three questions: what it does, how it works (loop, consumer groups), and first run requirements
- Every refusal names the problem, wanted value, and remedy in one line
- Uniform JSON output across all verbs
- The spec clearly documents the at-least-once delivery model and token retry mechanism
- Help text is comprehensive with effect lines per verb

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| --json on all verbs | YES | help says "Every verb takes --json" and tested send --json works |
| dry-run works | PARTIAL | requires --redis to be provided first, or errors "verb never read it" |