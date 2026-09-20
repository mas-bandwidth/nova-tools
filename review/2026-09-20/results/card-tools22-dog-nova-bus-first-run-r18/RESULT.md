RESULT tools22-dog-nova-bus-first-run-r18 sha=5298f6be12ea — read docs/CLI.md:361-392 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 18 of 24
BUILD nova-version devel darwin/arm64 go1.27.1
| # | command | exit | status |
|---|---------|------|--------|
| 1 | `nova-bus names --bus ./bus` | 0 | CLEAN |
| 2 | `nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md` | 0 | CLEAN |
| 3 | `nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main` | 1 | SKIP needs remote 'origin' not configured by section |
| 4 | `nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main` | 1 | CLEAN (refused on bad CURSOR, output matches doc verbatim) |
| 5 | `nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main` | 1 | SKIP depends on send having pushed a new commit to origin/main first |
RAN 3
SKIPPED 2
Left owed: send (block 3) and second inbox (block 5) depend on a git remote named 'origin' with a reachable repository. The 'First run' section copies out example-bus and runs `git init`, but never configures or documents how to add a remote. Without one, `send` cannot push (and `--advance` on inbox cannot fetch branch-level status). A minimal fix: the section should instruct adding a local bare clone or test fixture as `origin`, e.g. `git remote add origin ../bare-bus.git`. Without that, blocks 3–5 exercise only their pre-fetch logic (cursor validation, full scan) but cannot complete their network-dependent stages.
