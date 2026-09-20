RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r7 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus, VERB Reading a backlog with a typed decision, DOC docs/CLI.md:556-559, REPLICA 7 of 24, BUILD <nova-version could not run>

The section at docs/CLI.md:556-559 contains no fenced command blocks. It is pure prose describing the `--decide` flag. There is nothing to run and nothing to disagree with.

RAN 0, SKIPPED 0

Left owed: `nova-version` could not run (Permission denied, exit 126). No fenced code blocks exist in the section to test.

git status --short: (clean, no output)