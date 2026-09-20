RESULT tools22-dog-nova-bus-first-run-r15 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
SKIP the nova-bus (and nova-version) binaries are not installed on this machine, so the section's commands cannot be exercised here

TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 15 of 24
BUILD not available — `nova-version` command not found (`/bin/bash: nova-version: command not found`)

Base check: `git rev-parse HEAD` printed `5298f6be12eaa0f7e6622334d2b6a1eb427649e3`, which matches the card's pinned base. Not BLOCKED on head. But the card's STEP 1 gate runs `nova-version`, and the binary is not installed; neither is `nova-bus`. A search across the machine (`which nova-bus`, `which nova-version`, `find / -name nova-bus`, `/Users/glenn/go/bin`, `/Users/glenn/bin`, module cache) found no installed `nova-*` tool. The document's `### First run` section commands all invoke `nova-bus` and therefore cannot be run as written.

None of the section's four command blocks could be exercised. They each require the `nova-bus` binary, which is not present. I did not fabricate a fixture or build a substitute binary: the card requires a reading against the *installed* tool of the same build, and none is installed.

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-bus names --bus ./bus | - | SKIP (tool not installed) |
| 2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | - | SKIP (tool not installed) |
| 3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | - | SKIP (tool not installed) |
| 4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | - | SKIP (tool not installed) |
| 5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | - | SKIP (tool not installed) |

(Blocks 1-5 correspond to the five fenced command lines in the section; blocks 2-5 share the same missing-tool reason. There is also the prose-only "Copy cmd/nova-bus/testdata/example-bus…" instruction, which likewise could not be applied because the section as a whole could not be run.)

No DRIFT lines are reported: nothing ran, so there is nothing to compare line-by-line. Verdicting DRIFT would require actual tool output to contradict the document; the honest finding is that the section cannot be tried here.

RAN 0
SKIPPED 5
(no DRIFT output to quote — no command ran)

Left owed: the entire `nova-bus First run` section (docs/CLI.md:361-392). It could not be judged because the `nova-bus` and `nova-version` binaries are not installed on this machine, so the documented commands cannot be executed. The card's STEP 1 confirmation of the installed build via `nova-version` also fails for the same reason. Head matches the pinned base (`5298f6be12eaa0f7e6622334d2b6a1eb427649e3`), so the tree is correct; only the installed tool is missing.

git status --short at job root: (empty — repo is clean, nothing staged or modified)
