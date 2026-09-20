RESULT tools22-dog-nova-bus-the-cursor-r8 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 8 of 24
BUILD Permission denied — `/home/ubuntu/.local/bin/nova-version` could not be run
RAN 0
SKIPPED 0
Left owed: The `nova-version` binary at `/home/ubuntu/.local/bin/nova-version` exists with permissions 0755 but the kernel refuses to read/execute it (Permission denied). The section at 522-527 contains no fenced command blocks, so there was nothing to exercise. The prose was read and recorded faithfully.