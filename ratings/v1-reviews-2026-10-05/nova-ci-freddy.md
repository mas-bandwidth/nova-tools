# nova-ci review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9 (v1.0.0)
Verdict: GOOD for an AI to use
Score: 8/10

## Reasons

Verdict: GOOD for an AI to use because help is consistent, refusals name their remedies, and the JSON/JSONL input shape is stable. Score: 8/10; a 10 would need the github receipt command to print its own usage when run without flags rather than exiting 2 before any validation.

First confusion: github receipt: --repo wants owner/name, got "nova-tools" — the tool refuses the value before any redis connect, which is good, but its help shows --repo owner/name without quoting the slash requirement; an AI would guess single-word repo names. First doubted claim: "exit 0 written, 1 the store refused it, 2 usage" — yet running `nova-ci github receipt --from-runner` with no flags exits 2 before any validation, which matches "usage" but the message doesn't list the missing flags explicitly.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci github receipt --from-runner` | exits 2 before any flag validation; the refusal is "exit 2 usage" but no flag list is shown | print a one-line usage showing required flags when no flags are present, before any redis dial | S |
| 2 | `nova-ci github receipt --repo` | accepts `nova-tools` and refuses with `--repo wants owner/name, got "nova-tools"` | accept single-word names and treat them as `owner/name` where the owner is inferred from the bench's env | S |
| 3 | `nova-ci local` | requires the user to know about `--base origin/dev` and `--functional` | add `--base` to help banner's one-liner | S |

## Good, keep

- The CI-SLOW output format is stable and parsable by downstream jobs.
- Exit codes distinguish measured vs enforced (0 vs 2 under `--enforce`).
- The class tests (waits, testbins, net, goenv) are documented in SPEC-CI.md with clear remedies.
