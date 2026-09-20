RESULT tools22-dog-nova-bus-first-run-r14 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 14 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

Nova-version was not preinstalled; I built nova-bus and nova-version from the pinned base
(HEAD=5298f6be12eaa0f7e6622334d2b6a1eb427649e3) into scratch/bin, and the built binary's
version stamp is 5298f6be12ea — the card's base. The fixture was made the way the section and
the example-bus README direct: copy cmd/nova-bus/testdata/example-bus to ./bus, git init -b main,
commit, and (as the section says "the bus the tests run these lines against", firstrun_test.go)
a bare origin beside it. The draft's placeholder was replaced with a body before send, as the
section's prose says the writer does.

1 | nova-bus names --bus ./bus | exit 0 | CLEAN
2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | exit 0 | CLEAN
3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | exit 0 | CLEAN
4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | exit 1 | CLEAN
5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | exit 0 | CLEAN

RAN 5
SKIPPED 0
Left owed: nothing. The INBOX REFUSED line (command 4) printed verbatim, cursor included, and
exited 1 (the check said NO), which is a refusal, not a 2 could-not-run. body_bytes in the SEND
line was 47 on my run against 46 in the doc — the size of the body the writer wrote, not a
field-name/order/spelling difference, so not drift. DRAFT NOTE on draft printed to stderr; the
doc's block for it shows only the redirect, consistent with TESTS.md's `! DRAFT NOTE` line.

git status --short prints nothing.