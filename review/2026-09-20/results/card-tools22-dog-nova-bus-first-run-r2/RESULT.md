RESULT tools22-dog-nova-bus-first-run-r2 sha=5298f6be12ea — read `nova-bus First run` against docs/CLI.md:361-392 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB First run
DOC docs/CLI.md:361-392
REPLICA 2 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6
1 | nova-bus names --bus ./bus | exit 0 | CLEAN
2 | nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md | exit 0 | CLEAN
3 | nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main | exit 0 | CLEAN
4 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main | exit 1 | CLEAN
5 | nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main | exit 0 | CLEAN
RAN 5
SKIPPED 0
Left owed: none
