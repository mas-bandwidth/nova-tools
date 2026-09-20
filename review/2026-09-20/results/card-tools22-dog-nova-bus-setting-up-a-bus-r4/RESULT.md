RESULT tools22-dog-nova-bus-setting-up-a-bus-r4 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
BROKEN `nova-bus` and `nova-version` binaries at `/home/glenn/.local/bin/` return EACCES on execve despite 0755 permissions; tool cannot run at all

TOOL nova-bus, VERB Setting up a bus, DOC docs/CLI.md:393-433, REPLICA 4 of 24, BUILD unknown (nova-version cannot execute)

RAN 0, SKIPPED 0
Left owed: The entire section could not be exercised because `nova-bus` binary cannot execute. The three-command block (`cp`, `git init/add/commit`, `nova-bus check`) at the end of the section was never reached.

git status --short: (no output, clean)