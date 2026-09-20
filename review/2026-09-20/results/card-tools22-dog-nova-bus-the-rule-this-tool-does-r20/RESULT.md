RESULT tools22-dog-nova-bus-the-rule-this-tool-does-r20 sha=5298f6be12ea — read `nova-bus The rule this tool does not enforce` against docs/CLI.md:560-563 and say CLEAN or DRIFT
SKIP section has no executable code blocks; it is purely textual documentation explaining nova-bus philosophy
TOOL nova-bus
VERB The rule this tool does not enforce
DOC docs/CLI.md:560-563
REPLICA 20 of 24
BUILD nova-version permission denied (executable at /home/nova/.local/bin/nova-version)

1 | (none) | exit - | SKIP

RAN 0
SKIPPED 0

Left owed: Section contains no commands to run. It is a philosophical note stating that nova-bus does not enforce rules about data vs. grants - this is by design, not an implementation gap.

git status --short:
?? RESULT.md
?? repo/
