RESULT tools22-dog-nova-bus-setting-up-a-bus-r23 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 23 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

| # | command | exit | verdict |
|---|---------|------|---------|
| 1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | 0 | CLEAN |
| 2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | 0 | CLEAN |
| 3 | nova-bus check --bus ~/my-bus --full | 0 | CLEAN |

RAN 3
SKIPPED 0

CMD3 output (first 15 lines):
BUS SCOPE mode=full cursor=- changed=0
BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0

Left owed: The section does not document the stdout/stderr output format of `nova-bus check --full`, only that "it passes check --full" — meaning exit 0 is the contract. The installed `nova-version` and `nova-bus` binaries could not execute directly (`Permission denied`) due to sandbox restrictions; I built `nova-bus` from source at the pinned commit and used it for verification. Git user identity (`user.email`/`user.name`) was configured globally before running the commands because the environment lacked this setting, but it is a universal git prerequisite, not something specific to the bus setup.
