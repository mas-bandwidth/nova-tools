RESULT tools22-dog-nova-bus-the-cursor-r19 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
BLOCKED installed build could not be determined: nova-version exit 126 Permission denied
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 19 of 24
BUILD (cannot determine — nova-version fails to execute)
RAN 0
SKIPPED 0
Left owed: cannot confirm the installed tool matches the base revision because nova-version exits 126 with "Permission denied". The section itself (lines 522-527) contains only prose — no fenced command blocks — so there are no commands to run and no output to compare. The section is CLEAN on its own terms, but the prerequisite build verification fails.

git status --short: (nothing from the repository)