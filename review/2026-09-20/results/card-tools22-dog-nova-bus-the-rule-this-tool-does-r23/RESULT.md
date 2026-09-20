RESULT tools22-dog-nova-bus-the-rule-this-tool-does-r23 sha=5298f6be12ea — read `nova-bus The rule this tool does not enforce` against docs/CLI.md:560-563 and say CLEAN or DRIFT
SKIP the nova-bus binary is blocked by the sandbox (EACCES on execve), so the tool cannot be executed on this machine
- TOOL nova-bus, VERB The rule this tool does not enforce, DOC docs/CLI.md:560-563, REPLICA 23 of 24, BUILD unreachable (nova-version: Permission denied)
- 0 command blocks in this section
RAN 0, SKIPPED 0 (section is purely prose, no fenced commands)
Left owed: nova-version could not run (Permission denied / exit 126); the tool binary at /home/nova/.local/bin/nova-bus is unreadable and unexecutable due to sandbox restrictions. Without build verification, the reading cannot be confirmed against the correct tool.
git status --short: (nothing, clean)