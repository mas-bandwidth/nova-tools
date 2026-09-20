RESULT tools22-dog-nova-bus-the-rule-this-tool-does-r19 sha=5298f6be12ea — read `nova-bus The rule this tool does not enforce` against docs/CLI.md:560-563 and say CLEAN or DRIFT
CLEAN

TOOL nova-bus
VERB The rule this tool does not enforce
DOC docs/CLI.md:560-563
REPLICA 19 of 24
BUILD <nova-version: permission denied (exit 126) — could not confirm installed build>

RAN 0
SKIPPED 0

Left owed: nova-version was not executable (Permission denied). The section contains no
fenced command blocks, so nothing could be run or compared.

git status --short: (nothing, as required)