RESULT tools22-dog-nova-bus-setting-up-a-bus-r12 sha=5298f6be12ea
CLEAN
TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 12 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6
n | command | exit | status
1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | exit 0 | CLEAN
2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | exit 0 | CLEAN
3 | nova-bus check --bus ~/my-bus --full | exit 0 | CLEAN
RAN 3
SKIPPED 0
Left owed nothing
