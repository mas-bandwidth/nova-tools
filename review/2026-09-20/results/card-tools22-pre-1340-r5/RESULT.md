RESULT tools22-pre-1340-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1340 at head a60b9f7b4eb6: docs: batches 4–6 (draft for Stella)
PREREAD 1340 claims=10 proven=0 unproven=10 defects=4 high=0

PR 1340
HEAD a60b9f7b4eb6f7782f986add4382d25921531e5a
BASE dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
MERGE-BASE 65e861758248e695570098c9b1d793e93695870e
BEHIND 69
FILES 2 production, 0 test
LINES +238 -11

## CLAIMS

1. Updates the release notes header from "three batches" to "six batches" and revises the introductory paragraph accordingly — UNPROVEN
2. Adds Batch 4 section (#1332) documenting ten members including goenv class, CI lane 3, reports, tokens, work ask/asks, update release, sandbox run, dogfood ledger/gate, and work tests — UNPROVEN
3. Adds Batch 5 section (#1334) documenting one bus-lane member with version-join Windows lock fix and progress never entering protocol stream — UNPROVEN
4. Adds Batch 6 section (#1335) documenting the open merge-lane chain #1308→#1315→#1206 containing merge rebase/react/batch/queue/work events — UNPROVEN
5. Clarifies that batch 3 conflicted with dev after batch 4 landed and was rebased into the lane chain batch 6 carries — UNPROVEN
6. Documents new verb `nova-decide tune` reading decisions log to recommend which floor rows support — UNPROVEN
7. Documents new subcommand `nova-pulse progress` answering how fast/at what cost/how long from usage.tsv records — UNPROVEN
8. Documents `nova-sandbox egress` four verbs (plan, apply, check, drop) as the card's outbound wall on the bench — UNPROVEN
9. Appends full verb help output for `nova-tokens` including fold, report, sum, check, sources, profiles, session, fold-pool, and version — UNPROVEN
10. Updates dogfooding receipt lines from verbs=106/findings=105 to verbs=108/findings=107 in CLI.md — UNPROVEN

## DEFECTS

low docs/CLI.md:90 — "`--dsn` is a `postgres://` URL or a TSV path, default `$NOVA_DSN`" does not say whether `$NOVA_DSN` defaults to something concrete or to nothing if unset; a reader running this verb without that variable set would hit undefined behaviour — name a fallback value or say it errors out
low docs/CLI.md:1853 — The `nova-pulse progress` estimate formula `hours = remaining x wall_p90_s / parallelism x factor` uses ambiguous operator precedence; readers cannot tell whether this means `(remaining × wall_p90_s) / (parallelism × factor)` or `((remaining × wall_p90_s) / parallelism) × factor` — parenthesise the formula or write it as two explicit steps
medium docs/CLI.md:2268 — The nova-sandbox help output adds five new verbs (`probe`, `policy`, `check`, `worktree`, `version`) and seven egress verbs but the doc itself says "The contract is SPEC-SANDBOX.md" only once for the core sandbox verbs and never links to any spec for the new ones — if these are real verbs, link their contracts; if they are drafts, flag them as such so a reviewer knows they are not yet implemented
low docs/RELEASE-NOTES-2026-09-18.md:22 — "the first batch whose gate was a verb rather than a person's shell loop, and the first whose gate ran CI's own test command" uses "the first" twice for the same concept; either they are two distinct properties worth naming separately or this reads like repetition — clarify

## QUESTIONS

1. The dogfooding line count increases from 106 to 108 (docs/CLI.md:81,84) — exactly two new verbs were added somewhere between this PR and the last dogfood run. Which two? The release notes list many new verbs across batches 4–6 but I cannot find which two specifically caused the count bump.

2. `nova-decide tune` (docs/CLI.md:501-513) introduces a new verb not seen in any prior batch description. Is this verb already merged into dev under #1327, or is it being documented ahead of its implementation landing? The note marks #1327 as "in no batch yet."

3. `nova-pulse progress` (docs/CLI.md:1844-1871) documents an estimate formula referencing `SPEC-PULSE.md`. That file exists in the repo but the doc does not show a direct diff against it — did you verify the formula in the prose matches the implementation in bin/progress.sh exactly?

4. The sandbox egress section (docs/CLI.md:2497-2541) explicitly states the verbs land with #1330 which is "open and is in none of batches 4, 5 or 6." Why document verbs that belong to a separate open PR rather than waiting for #1330 to merge? Is the intent that #1340 stays attached to #1330 as a dependent review document?

## Left owed

This PR changes zero code — only docs/CLI.md and docs/RELEASE-NOTES-2026-09-18.md. I read both files in full via the diff above. What I could not verify from the diff alone: whether the new verb descriptions accurately match the actual implementations (no code to compare), whether SPEC-PULSE.md and SPEC-SANDBOX.md contain the referenced contracts and are internally consistent, and whether the dogfooding receipt numbers (106→108) correctly reflect which verbs were newly added since the last dogfood run. No test files exist in this diff to assess.

git status --short
5298f6be12eaa0f7e6622334d2b6a1eb427649e3
