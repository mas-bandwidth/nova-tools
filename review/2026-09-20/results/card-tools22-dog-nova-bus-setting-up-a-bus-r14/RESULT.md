RESULT tools22-dog-nova-bus-setting-up-a-bus-r14 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
BROKEN the installed nova-version binary cannot be executed (Permission denied, blocked by landlock sandbox)
TOOL nova-bus, VERB Setting up a bus, DOC docs/CLI.md:393-433, REPLICA 14 of 24, BUILD <could not run>
RAN 0, SKIPPED 0
Left owed: the entire section could not be exercised because `nova-version` (and by extension `nova-bus`) cannot be executed in this sandbox environment. Landlock restricts all access (read+exec) to `/home/glenn/.local/bin/`.
git status --short: nothing from the repository (no changes)