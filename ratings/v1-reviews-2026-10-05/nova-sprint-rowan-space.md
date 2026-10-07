# nova-sprint review, 2026-10-05

Rater: Inception/Mercury-2.5
Build: 52046bd91236
Verdict: NOT YET for an AI to use
Score: 2/10

## Reasons
Nova-sprint is in deprecated/cmd/nova-sprint/ - it is not an active tool. There is no section for nova-sprint in docs/CLI.md, no SPEC-SPRINT.md file, and no command-line interface available in cmd/. The tool was a sprint table reader that worked with Redis, but it has been superseded (the table functionality is now in nova-table/internal/ntable per deprecated/docs/nova-sprint/scaffold.md). An AI cannot use this tool because: (1) it cannot be built or installed from cmd/, (2) help output is unavailable, (3) there is no documented first-run workflow. The first confusion: expected nova-sprint in cmd/ but found it only in deprecated/. The first doubted claim: the BRIEF says to review "nova-sprint" as if it were a shipped tool, but it is archived.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-sprint/ does not exist (only deprecated/cmd/nova-sprint/) | Tool is deprecated; cannot be built or installed as a command | Mark the deprecated path in README as legacy; provide migration notes to nova-table | S |
| 2 | docs/CLI.md has no nova-sprint section | No documentation for help or usage; AI cannot discover commands | Add nova-sprint section to docs/CLI.md if tool is to be revived, or remove reference from BRIEF | S |
| 3 | docs/SPEC-SPRINT.md does not exist | No spec to understand behavior, flags, or exit codes | Create spec or document in deprecated/docs/nova-sprint/README.md | M |

## Good, keep
The deprecated code shows clear refusal grammar with named remedies (e.g., "needs --redis <addr>") and consistent exit codes (0 ran, 2 could not run). The table read uses consistent Redis snapshots via FCALL_RO.
