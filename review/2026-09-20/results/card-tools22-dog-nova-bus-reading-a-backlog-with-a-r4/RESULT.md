RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r4 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
KIND: transcript-test
DEADLINE: 1500
LEG: go
PATHS: docs/CLI.md
FILES: 0
TEST: none
MODE: read
TURNS: 25
SOURCE: docs/CLI.md:556
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
base-repo: /tmp/nova-tools-mirror.git
base-sha: 5298f6be12eaa0f7e6622334d2b6a1eb427649e3
ROUTE: jev=flash why=- eligible=rules

SKIP cannot verify installed nova build

TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 4 of 24
BUILD (unable to run nova-version)

| n | command | exit | status |
|---|---------|------|--------|
| - | - | - | - |

RAN 0
SKIPPED 0

Left owed: The installed `nova-version` binary cannot be executed in this environment (permission denied). Without verifying the installed build matches the card's base sha, I cannot exercise the documented section.
