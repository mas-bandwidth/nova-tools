RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r18 sha=5298f6be12eaa0f7e6622334d2b6a1eb427649e3 — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 18 of 24
BUILD blocked - could not execute nova-version (Permission denied, Landlock sandbox read restriction)
RAN 0
SKIPPED 0
Left owed: nova-version cannot be executed in this sandboxed environment, so the installed build cannot be confirmed against the base. The git HEAD matches 5298f6be12eaa0f7e6622334d2b6a1eb427649e3. The section had no fenced command blocks to run.