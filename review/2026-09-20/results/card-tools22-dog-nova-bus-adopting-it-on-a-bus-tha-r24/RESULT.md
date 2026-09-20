RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r24 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
SKIP nova-bus tool is not installed on this machine; nova-version: command not found

TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 24 of 24
BUILD not installed (nova-version: command not found)

Command blocks:
1 | nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | SKIP (nova-bus not installed)
2 | nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main | SKIP (nova-bus not installed)

RAN 0
SKIPPED 2

Left owed: all commands require nova-bus which is not available on this machine. The section's two fenced command blocks and all instructions referencing `nova-bus check`, `nova-bus inbox`, `--rebuild-index`, `--carry-history` etc. could not be exercised.

git status --short: (empty — no changes to repo)