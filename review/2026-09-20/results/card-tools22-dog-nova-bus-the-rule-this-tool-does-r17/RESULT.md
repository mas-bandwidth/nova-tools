RESULT tools22-dog-nova-bus-the-rule-this-tool-does-r17 sha=5298f6be12ea — read `nova-bus The rule this tool does not enforce` against docs/CLI.md:560-563 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB The rule this tool does not enforce
DOC docs/CLI.md:560-563
REPLICA 17 of 24
BUILD could not run (Permission denied on FUSE filesystem — /home/ubuntu/.local/bin/nova-version cannot be executed despite 0755)
RAN 0
SKIPPED 0
Left owed: nova-version could not execute to confirm build sha. The section is pure prose — zero fenced command blocks — so there is nothing to run, compare or drift against.
git status --short: (nothing from repo)