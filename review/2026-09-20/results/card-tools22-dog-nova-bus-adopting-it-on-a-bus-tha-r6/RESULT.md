RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r6 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555
SKIP section requires a bus directory but the doc does not tell you how to create one
TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 6 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

| # | command | exit | verdict |
|---|---------|------|---------|
| 1 | `nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)"` | SKIP — no bus directory, section never tells you how to create one | SKIP |
| 2 | `nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 \\\n  --full --legacy-now \\\n  --advance --remote origin --branch main` | SKIP — no bus directory, section never tells you how to create one | SKIP |

RAN 0
SKIPPED 2

Left owed: Neither command can be judged against document output because the section assumes a pre-existing bus (`<dir>`) but provides zero instructions to create one. The doc text describes what the commands do conceptually ("draw the line at the moment you switch", explains `--legacy-before`, `--legacy-now`, cursor behavior, INBOX lines) but produces no example output for comparison. Without a bus and without example output printed by the doc's own commands, there is nothing to run against and nothing to drift-check.

git status --short (must be empty):
(empty)
