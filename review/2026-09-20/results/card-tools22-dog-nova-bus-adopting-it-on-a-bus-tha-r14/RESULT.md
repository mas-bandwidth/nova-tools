RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r14 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 14 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea+dirty darwin/arm64 go1.27.1

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | 0 | CLEAN |
| 2 | nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main | 1 | CLEAN |

RAN 2
SKIPPED 0

LEFT OWED — nothing. Both fenced command blocks were runnable with minimal substitutions (<dir> → scratch/test-bus, <you> → Emma). All flags present (--full, --legacy-before, --legacy-now, --receipt-max-words, --advance, --remote, --branch) confirmed via `nova-bus help`. Output field names match doc descriptions (`INBOX LEGACY`, `INBOX OPEN`, `INBOX OK`). Exit 1 on command 2 is "verb ran and said NO" (local git changes in test bus), not a drift.

git status --short at end of repo:
?? nova-bus
?? nova-version
