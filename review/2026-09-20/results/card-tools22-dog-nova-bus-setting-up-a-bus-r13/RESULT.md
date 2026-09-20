RESULT tools22-dog-nova-bus-setting-up-a-bus-r13 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
SKIP installed nova-* binaries are blocked by the sandbox (execve returns 126 Permission denied); none of the three commands can be exercised

TOOL nova-bus, VERB Setting up a bus, DOC docs/CLI.md:393-433, REPLICA 13 of 24, BUILD <unavailable>

| # | command | exit | verdict |
|---|---------|------|---------|
| 1 | `cp -R cmd/nova-bus/testdata/example-bus ~/my-bus` | — | SKIP — writes outside job directory |
| 2 | `cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus'` | — | SKIP — depends on command 1, writes outside job directory |
| 3 | `nova-bus check --bus ~/my-bus --full` | — | SKIP — nova-bus binary blocked (Permission denied, exit 126) |

RAN 0, SKIPPED 3

Left owed: the entire section is unexercisable because the installed nova-* bins in /home/glenn/.local/bin/ are inaccessible under this sandbox. nova-version also blocked, so build identity cannot be confirmed.