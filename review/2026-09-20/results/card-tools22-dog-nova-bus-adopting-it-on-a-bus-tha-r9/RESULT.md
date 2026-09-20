RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r9 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
SKIP section cannot be exercised: commands use unresolvable placeholders without fixture setup
TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 9 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

| n | command | exit | status |
|---|---------|------|--------|
| 1 | `nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)"` | 0 | SKIP missing --bus dir |
| 2 | `nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main` | 0 | SKIP missing --bus dir, --as value, git repo with origin remote |

DRIFT lines: none (section fully skipped)
RAN 0
SKIPPED 2

Left owed: nothing — every command block in the section needs a bus directory (`<dir>`), and the second also needs a user identity (`<you>`) and a git repository configured with an `origin` remote on `main`. The section never instructs how to create any of these fixtures, so there is nothing left to judge once the skips are declared.

git status --short: (empty)
