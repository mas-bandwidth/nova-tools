RESULT tools22-dog-nova-bus-setting-up-a-bus-r22 sha=5298f6be12ea
CLEAN
TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 22 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

| # | command | exit | verdict |
|---|---------|------|---------|
| 1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | 0 | CLEAN |
| 2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | 0 | CLEAN |
| 3 | nova-bus check --bus ~/my-bus --full | 0 | CLEAN |

RAN 3
SKIPPED 0
Left owed none

git status --short
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json
?? repo/
