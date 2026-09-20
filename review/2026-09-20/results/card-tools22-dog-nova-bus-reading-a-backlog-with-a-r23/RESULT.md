RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r23 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
SKIP nova-bus binary cannot execute on this machine — Permission Denied by kernel/filesystem despite 0755 ownership; all invocations (/bin/bash -c, env, direct path) fail with "Permission denied" and even read access to the file is denied
TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 23 of 24
BUILD <unknown — /home/nova/.local/bin/ is Permission Denied at filesystem level; all nova-* tools unexecutable>
1 | --decide flag behaviour (described in prose, no fenced command block) | n/a | SKIP
RAN 0
SKIPPED 1
Left owed the entire section contents — every claim about `--decide`, `kind` choices, `wake` choices, `INBOX NOTE`/`INBOX DECIDED` output format, floor behavior, structured signals, `.public` marker enforcement, and lazy empty-inbox optimization cannot be verified without running `nova-bus`.
