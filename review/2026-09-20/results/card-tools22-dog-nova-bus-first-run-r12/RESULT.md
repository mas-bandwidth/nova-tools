RESULT tools22-dog-nova-bus-first-run-r12 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
DRIFT 2 findings

TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 12 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

| n | command | exit | status |
|---|---------|------|--------|
| 1 | nova-bus names --bus ./bus | 0 | CLEAN |
| 2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | 0 | DRIFT |
| 3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | N/A | SKIP |
| 4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | 1 | CLEAN |
| 5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | N/A | SKIP |

DRIFT docs/CLI.md:373 doc says `draft` prints the header a first note needs — `From:`, `To:` and `Subject:` — and nothing else | tool printed extra line to stderr: `DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>` | exit 0

DRIFT docs/CLI.md:375-376 doc's example sequence `draft > draft.md` immediately followed by `send --file draft.md` cannot be run as written because `draft.md` contains the uneditable placeholder `<the note goes here>` and must be edited between these two commands; this step is omitted from the First run code blocks | no applicable exit

RAN 3
SKIPPED 2

Left owed:
- CMD3 (`nova-bus send`): the section shows `> draft.md` followed immediately by `send --file draft.md`, but the template contains `<the note goes here>` which must be replaced with actual body text before sending; this edit step is described only in the "Reading it" paragraph below the code blocks, not in the First run section itself. Also, `--remote origin --branch main` requires a git remote named `origin` capable of push; the section (and the copied-out README) only instructs `git init`, never `git remote add origin ...`. Neither the body edit nor the remote setup can be judged here.
- CMD5 (`nova-bus inbox --full --advance`): requires `--remote origin --branch main` for `--advance` to update the bus cursor (tool exits 2 with "it needs --remote and --branch; refusing to guess" without them). Setting up a working remote is not described in the First run section, so this command cannot be run as written.

git status --short:
(must print nothing from the repository)
