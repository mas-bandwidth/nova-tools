RESULT tools22-dog-nova-bus-setting-up-a-bus-r9 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
DRIFT 1 findings
TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 9 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | exit 0 | CLEAN
2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | exit 128 | DRIFT
3 | nova-bus check --bus ~/my-bus --full | exit 0 | CLEAN

DRIFT docs/CLI.md:430 doc says `cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus'` | tool printed `fatal: unable to auto-detect email address (got 'nova@hetzner.(none)')` | exit 128

RAN 3
SKIPPED 0
Command 2 output:
Author identity unknown

*** Please tell me who you are.

Run

  git config --global user.email "you@example.com"
  git config --global user.name "Your Name"

to set your account's default identity.
Omit --global to set the identity only in this repository.

fatal: unable to auto-detect email address (got 'nova@hetzner.(none)')

Left owed
None
