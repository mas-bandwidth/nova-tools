RESULT tools22-dog-nova-bus-the-cursor-r17 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
BROKEN nova-version cannot be executed: /home/glenn/.local/bin/nova-version: Permission denied (exit 126); binary exists with 0755 perms but filesystem refuses read/exec
TOOL nova-bus, VERB The cursor, DOC docs/CLI.md:522-527, REPLICA 17 of 24, BUILD unknown (cannot run)
0 | (no fenced command blocks in section) | exit N/A | BROKEN
RAN 0, SKIPPED 0
Left owed: could not verify tool build against base sha 5298f6be; tool never ran. Section is prose only (no fenced command blocks) describing cursor mechanics; cannot be exercised without working nova-bus binary. No commands were run or compared.