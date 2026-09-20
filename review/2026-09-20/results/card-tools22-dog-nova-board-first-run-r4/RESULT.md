RESULT tools22-dog-nova-board-first-run-r4 sha=5298f6be12ea — read `nova-board First run` against docs/CLI.md:2586-2643 and say CLEAN or DRIFT
SKIP the installed nova-board/nova-version binaries cannot be read or executed in this sandbox: landlock grants reads only to the run root, harness, sdk and go/pkg-mod, not /home/ubuntu/.local/bin, so no documented command could be run

TOOL nova-board
VERB First run
DOC docs/CLI.md:2586-2643
REPLICA 4 of 24
BUILD <none — `nova-version` could not run; /home/ubuntu/.local/bin/nova-version exists (7229602 bytes, mode 755, owner ubuntu) but every open/exec of it returns "Permission denied" (exit 126)>

git rev-parse HEAD at <JOBDIR>/repo printed `5298f6be12eaa0f7e6622334d2b6a1eb427649e3` (matches card base). So STEP 1's first half passed.

`nova-version` printed nothing: `bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied`, exit 126. `nova-board` (documented verb) likewise: `bash: line 1: /home/ubuntu/.local/bin/nova-board: Permission denied`, exit 126. Both binaries are stat-able (correct owner/mode) but their contents and execution are refused by the sandbox. The section's only fenced command block (`nova-board quickstart --dir ./board --stale 10m`) therefore could not be run.

Reason for the block (from <RUNROOT>/native-argv.log and <RUNROOT>/native.log): the sandbox argv is
`nova-sandbox --read <RUNROOT> --write <JOBDIR> --write <DATA> --write <TMPDIR> --write /home/ubuntu/rowan-working/tmp/cache --cwd <JOBDIR> --read /home/ubuntu/nova-bench/harness-v1.18.20 --read /home/ubuntu/sdk --read-noexec /home/ubuntu/go/pkg/mod -- opencode run ...`
and native.log reports `SANDBOX OK backend=landlock abi=8 used=6 read=3 read-noexec=1 write=5`. `/home/ubuntu/.local/bin` is in no read list, so the installed tools are unreachable inside this sandbox. No copy of `nova-board` exists under any readable path (searched run root, cache, sdk, harness), and the go-build cache holds only older builds of nova-version/nova-ci/nova-bus/nova-work, not the installed build — so I could not read the doc against the real installed binary.

Table of command blocks in the section:

1 | nova-board quickstart --dir ./board --stale 10m | exit 126 | SKIP
   (refusal: "bash: line 1: /home/ubuntu/.local/bin/nova-board: Permission denied" — installed binary unreadable/unexecutable in this sandbox)

RAN 0
SKIPPED 1
First 15 lines of output for the SKIPped command:
```
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-board: Permission denied
```
(exit 126; identical refusal for `nova-version`)

Left owed: the entire section (all 9 expected output lines of the quickstart block, plus the QUICKSTART LINE/NOTE text and the prose about list/check/take/close refusal) is unjudged — nothing could be compared because no command could run. The installed build sha is unconfirmed for the same reason.

`git status --short` at <JOBDIR>/repo printed nothing (exit 0).