RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r22 sha=5298f6be12ea
CLEAN
TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 22 of 24
BUILD v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

No fenced code blocks in lines 556–559; section is prose only (heading + one descriptive paragraph). RAN=0, SKIPPED=0.
Documented flags `--decide`, `--floor`, `--key-env`, `--base-url`, `--allow-private` confirmed present via `nova-bus --help`.

RAN 0
SKIPPED 0
Left owed — nothing from lines 556–559 could be fully verified because the section contains no executable examples to exercise `--decide` output (INBOX NOTE with kind/wake/needs_reply/blocked/conf fields, INBOX DECIDED summary line). The description's claim that `--decide` refuses a clone with no `.public` marker, its structured-signal rule for STOP:/HOLD: subjects, and the exact output format of decided notes all require an inbox with notes and a JEV API key to test — neither was created nor is possible here.
git status --short: (clean)
