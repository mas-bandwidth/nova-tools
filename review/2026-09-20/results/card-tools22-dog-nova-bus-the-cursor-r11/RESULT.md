RESULT tools22-dog-nova-bus-the-cursor-r11 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 11 of 24
BUILD (could not run `nova-version`: EACCES from sandbox on `/home/nova/.local/bin/`; see Left owed)
git rev-parse HEAD = 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 — matches base

<n> | <command> | exit <code> | result
(none — section has no fenced command blocks; purely prose)

RAN 0
SKIPPED 0

Left owed:
- `nova-version`: every binary in `/home/nova/.local/bin/` (`nova-version`, `nova-bus`, `nova-sandbox`) returns `EACCES (Permission denied)` on `execve`. Same user (nova), same ownership (nova:nova), correct ACLs (user::rwx). Kernel syscall denies before interpreter/dynamic-linker is reached. Other replicas running inside identical nova-sandbox may have different access patterns; this finding is environment-specific. Without `nova-version` output the build SHA cannot be confirmed beyond `git rev-parse HEAD`.
- Because the section contains zero fenced code blocks, there is nothing further left owed: nothing in the prose can produce DRIFT, only runnable divergence can.

git status --short:
?? .lease
?? .nova-sandbox-tmp/
?? harness-output.log
?? opencode.json
?? repo/
