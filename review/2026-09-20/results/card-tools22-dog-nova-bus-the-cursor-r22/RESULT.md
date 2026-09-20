RESULT tools22-dog-nova-bus-the-cursor-r22 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
KIND: transcript-test
DEADLINE: 1500
LEG: go
PATHS: docs/CLI.md
FILES: 0
TEST: none
MODE: read
TURNS: 25
SOURCE: docs/CLI.md:522
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
base-repo: /tmp/nova-tools-mirror.git
base-sha: 5298f6be12eaa0f7e6622334d2b6a1eb427649e3
ROUTE: jev=flash why=- eligible=rules
SKIP no fenced command blocks in the section; only prose documentation
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 22 of 24
BUILD /home/nova/.local/bin/nova-version (permission denied - not readable)
| n | command | exit | status |
|---|---------|------|--------|
| none | no fenced command blocks in section | | |
RAN 0
SKIPPED 0
Left owed: Section describes cursor mechanism but has no executable code blocks to test
