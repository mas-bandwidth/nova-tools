RESULT tools22-dog-nova-bus-the-rule-this-tool-does-r12 sha=5298f6be12ea
BROKEN installed nova-* tools cannot run in this sandbox: /home/gaffer/.local/bin/nova-version: Permission denied (exit 126)

TOOL nova-bus
VERB The rule this tool does not enforce
DOC docs/CLI.md:560-563
REPLICA 12 of 24
BUILD none — nova-version could not run; it printed nothing. Exact failure: `/usr/bin/bash: line 1: /home/gaffer/.local/bin/nova-version: Permission denied`, exit 126. The file exists (0755, 7,229,602 bytes) but the landlock sandbox (read-noexec=1) blocks both reading and executing /home/gaffer/.local/bin; `cp` of the binary is also Permission denied, and `nova-bus` fails identically (exit 126).

Command blocks in the section: 0. The section `### The rule this tool does not enforce` (docs/CLI.md:560-563) is prose only — it contains no fenced command blocks, so there is nothing for STEP 3 to run or STEP 4 to compare.

| n | command | exit | verdict |
|---|---|---|---|
| — | (no fenced command blocks in the section) | — | — |

RAN 0
SKIPPED 0

DRIFT: none — nothing was runnable to disagree.

Left owed:
- The installed build could not be confirmed against base sha 5298f6be12eaa0f7e6622334d2b6a1eb427649e3: `nova-version` is refused by the sandbox (Permission denied, exit 126), so a reading anchored to the correct binary cannot be produced. `git rev-parse HEAD` did print the correct sha 5298f6be12eaa0f7e6622334d2b6a1eb427649e3.
- The section itself contains no commands, so its prose reading is complete; it cannot be exercised against nova-bus because nova-bus is likewise unexecutable here (Permission denied, exit 126).

git status --short at the end prints nothing (empty output, exit 0).