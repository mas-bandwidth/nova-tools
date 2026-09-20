RESULT tools22-dog-nova-bus-setting-up-a-bus-r1 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
DRIFT 1 findings
TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 1 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6
1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus ; cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' ; nova-bus check --bus ~/my-bus --full | exit 1 | DRIFT
DRIFT docs/CLI.md:429 doc says cp -R cmd/nova-bus/testdata/example-bus ~/my-bus succeeds | tool printed cp: cannot stat 'cmd/nova-bus/testdata/example-bus': No such file or directory | exit 1
RAN 1
SKIPPED 0
First 15 lines of DRIFT output:
cp: cannot stat 'cmd/nova-bus/testdata/example-bus': No such file or directory

Left owed
None — the only fenced code block was exercised from scratch and the result was recorded.

git status --short:
(none)
