RESULT tools22-dog-nova-bus-setting-up-a-bus-r5 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
BROKEN nova-bus and nova-version are blocked by the sandbox (Landlock denies read/exec to /home/glenn/.local/bin); documented commands cannot be exercised

TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 5 of 24
BUILD (could not run — nova-version: Permission denied)
| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | `cp -R cmd/nova-bus/testdata/example-bus ~/my-bus` | — | BROKEN |
| 2 | `cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus'` | — | BROKEN |
| 3 | `nova-bus check --bus ~/my-bus --full` | — | BROKEN |

RAN 0
SKIPPED 0
Left owed All three command blocks in the section could not be run because nova-bus and nova-version are inaccessible. The sandbox (Landlock abi=8, clamped to 6) was configured with read/write access to the job directory and a few caches, but does not include /home/glenn/.local/bin — the only location where nova-bus and nova-version are installed. The file exists (7229602 bytes, 755) but the kernel denies all read I/O and execve. Even `nova-version` — required by STEP 1 to confirm the build matches the base — failed with exit 126 (Permission denied).

git status --short: (nothing printed, repo is clean)