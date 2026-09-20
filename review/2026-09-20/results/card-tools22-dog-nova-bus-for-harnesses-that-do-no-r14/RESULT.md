RESULT tools22-dog-nova-bus-for-harnesses-that-do-no-r14 sha=5298f6be12ea
SKIP no fixture bus or git remote configured; section never creates them
TOOL nova-bus
VERB For harnesses that do not wake you
DOC docs/CLI.md:528-541
REPLICA 14 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

| n | command | exit | status |
|---|---------|------|--------|
| 1 | nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m --advance --remote origin --branch main | 2 | SKIP |

RAN 0
SKIPPED 1

SKIP reason (first 15 lines of output):
nova-bus wait: a wait fetches the bus and reads what changed since your cursor, which need git; /Users/glenn/rowan-working/tmp/b63c4cb8-c494-a5fe-d90e-d1ee2c1acd18-card-tools22-dog-nova-bus-for-harnesses-that-do-no-r14/data/bus is not a git work tree: git -C /Users/glenn/rowan-working/tmp/b63c4cb8-c494-a5fe-d90e-d1ee2c1acd18-card-tools22-dog-nova-bus-for-harnesses-that-do-no-r14/data/bus rev-parse --show-toplevel: exit status 128: fatal: cannot change to '/Users/glenn/rowan-working/tmp/b63c4cb8-c494-a5fe-d90e-d1ee2c1acd18-card-tools22-dog-nova-bus-for-harnesses-that-do-no-r14/data/bus': No such file or directory

Left owed — the wait loop itself cannot be exercised without a real bus git work tree populated with notes, a participants roster containing "Ada", and a git remote named `origin` pushing to a reachable repo. The section provides zero setup for any of these.

git status --short (repo):
?? nova-bus
?? nova-version
