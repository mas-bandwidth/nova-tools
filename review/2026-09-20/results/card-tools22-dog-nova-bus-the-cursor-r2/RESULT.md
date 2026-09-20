RESULT tools22-dog-nova-bus-the-cursor-r2 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
BLOCKED installed build cannot be verified — nova-version exits Permission denied for all access methods (exec, execv, cp, strings, objdump) despite stat showing 0755 / glenn:glenn; kernel-level sandbox prevents reading/executing /home/glenn/.local/bin/nova-{version,bus}
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 2 of 15
BUILD <unavailable — Permission denied>
n | command | exit e | status
1 | (none — section contains only prose, no fenced code blocks) | — | SKIP
SKIPPED 1 — no fenced command blocks exist in lines 522-527; the section is two paragraphs of descriptive text about how `inbox` and `check` maintain cursors (`CURSOR`, `OPEN`, `INDEX`)
RAN 0
Left owed: Could not confirm installed build SHA matches base 5298f6be12eaa0f7e6622334d2b6a1eb427649e3. A sandbox rule blocks all read/exec/syscalls on /home/glenn/.local/bin/nova-{version,bus}, so even though git HEAD confirms the repo is at the pinned base, the actual binary under test was never verified.
git status --short: (empty — no changes)
