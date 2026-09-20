RESULT tools22-dog-nova-bus-the-rule-this-tool-does-r13 sha=5298f6be12ea — read `nova-bus The rule this tool does not enforce` against docs/CLI.md:560-563 and say CLEAN or DRIFT
SKIP section has no command blocks to exercise
TOOL nova-bus
VERB The rule this tool does not enforce
DOC docs/CLI.md:560-563
REPLICA 13 of 24
BUILD <not accessible: /home/glenn/.local/bin/nova-* is blocked by sandbox; git rev-parse HEAD=5298f6be12eaa0f7e6622334d2b6a1eb427649e3 confirms repo is at correct base>
SKIPPED section contains no fenced code blocks — only a header (line 560) and two prose paragraphs (lines 562-563) describing a design principle that `nova-bus` deliberately does not implement. Nothing runs.
RAN 0
SKIPPED 0
Left owed none — the section has no commands, flags, or output examples to verify; it asserts absence of behavior rather than documenting invocations.
