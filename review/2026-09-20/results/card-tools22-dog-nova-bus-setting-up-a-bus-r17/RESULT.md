RESULT tools22-dog-nova-bus-setting-up-a-bus-r17 sha=5298f6be12ea
CLEAN
TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 17 of 24
BUILD nova-version devel linux/amd64 go1.26.6

| # | command | exit | verdict |
|---|---------|------|---------|
| 1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | 0 | CLEAN |
| 2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | 0 | CLEAN |
| 3 | nova-bus check --bus ~/my-bus --full | 0 | CLEAN |

RAN 3
SKIPPED 0

git status --short:
