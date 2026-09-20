RESULT tools22-dog-nova-bus-the-cursor-r4 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
BROKEN installed tool `nova-version` could not execute (Permission denied); cannot verify build matches base sha
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 4 of 24
BUILD (unable to run nova-version)
RAN 0
SKIPPED 0
Left owed: The entire section could not be exercised because the installed `nova-version` binary at /home/gaffer/.local/bin/nova-version (stat: 755, owned by glenn:gaffer) refuses execution with "Permission denied" despite correct permissions. AppArmor is loaded but the process is unconfined. No fenced command blocks exist in docs/CLI.md:522-527 (the section is purely descriptive prose about cursor mechanics), so even with a working binary there would be no commands to run. Cannot judge CLEAN or DRIFT against an unverifiable build.