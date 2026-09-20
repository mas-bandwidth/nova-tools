RESULT tools22-dog-nova-bus-for-harnesses-that-do-no-r9 sha=5298f6be12ea — read `nova-bus For harnesses that do not wake you` against docs/CLI.md:528-541 and say CLEAN or DRIFT
SKIP Section could not be exercised: the bus directory (~/bus) required by the wait command is not described as being created earlier in this section.
TOOL nova-bus
VERB For harnesses that do not wake you
DOC docs/CLI.md:528-541
REPLICA 9 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6
1 | nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m --advance --remote origin --branch main | exit 2 | SKIP
RAN 0
SKIPPED 1
Left owed: Could not run wait command; no instructions in section 528-541 to create a bus fixture.
