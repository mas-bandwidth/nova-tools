RESULT tools22-dog-nova-bus-setting-up-a-bus-r10 sha=5298f6be12ea
BROKEN nova-version and nova-bus fail with exit 126 Permission denied; binary cannot be executed on this machine (execve returns EACCES, file unreadable)
TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 10 of 24
BUILD (could not run — binary unreadable)
1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus && cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' && nova-bus check --bus ~/my-bus --full | exit - | BROKEN
RAN 0
SKIPPED 1
Left owed All commands skipped because the nova binaries (nova-version, nova-bus) cannot be executed. execve returns EACCES Permission denied (exit 126) for both binaries. The dynamic linker also cannot read them. This is a system-level restriction preventing any nova tool from running.
git status --short: (nothing; clean)
