RESULT tools22-dog-nova-bus-for-harnesses-that-do-no-r15 sha=5298f6be12ea — read `nova-bus For harnesses that do not wake you` against docs/CLI.md:528-541 and say CLEAN or DRIFT
SKIP nova-bus and nova-version are not installed on this machine

TOOL nova-bus
VERB For harnesses that do not wake you
DOC docs/CLI.md:528-541
REPLICA 15 of 24
BUILD not found — nova-version: command not found

1 | nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m --advance --remote origin --branch main | exit N/A | SKIP

RAN 0
SKIPPED 1
Left owed: The sole command block could not be run because nova-bus is not installed on this machine.