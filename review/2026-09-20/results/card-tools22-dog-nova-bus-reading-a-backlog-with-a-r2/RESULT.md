RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r2 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 2 of 15
BUILD <could not execute /usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied — installed build sha unverifiable; repo HEAD confirmed 5298f6be12eaa0f7e6622334d2b6a1eb427649e3>

| n | command | exit | status |
|---|---------|------|--------|
| 1 | (no fenced command blocks found in lines 556-559) | — | CLEAN |

RAN 0
SKIPPED 0

Left owed: Nothing. This section contains only prose documentation describing `--decide`, `--key-env`, `--base-url`, `--floor`, `--allow-private`, structured signal handling (`STOP:`, `HOLD:`), `.public` marker refusal, and the lazy-pass optimization. There are zero fenced code blocks and zero example invocations to exercise or compare against the installed binary.
