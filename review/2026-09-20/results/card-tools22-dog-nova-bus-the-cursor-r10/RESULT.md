RESULT tools22-dog-nova-bus-the-cursor-r10 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 10 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

| # | command | exit | status |
|---|---------|------|--------|
— (no fenced code blocks in this section — lines 522–527 are purely conceptual prose)

RAN 0
SKIPPED 0

Left owed nothing — no commands to run, no output to compare. The prose references `--full` and `--advance` flags; both exist on `nova-bus inbox`. Verbs `inbox`, `check`, `send`, `receipt` all exist. No contradictions found.

git status --short: (clean)
