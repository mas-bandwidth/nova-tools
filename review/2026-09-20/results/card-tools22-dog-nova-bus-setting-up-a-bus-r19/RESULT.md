RESULT tools22-dog-nova-bus-setting-up-a-bus-r19 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 19 of 24
BUILD v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6
1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus; cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus'; nova-bus check --bus ~/my-bus --full | exit 0 | CLEAN
RAN 1
SKIPPED 0
git status --short: (nothing)
