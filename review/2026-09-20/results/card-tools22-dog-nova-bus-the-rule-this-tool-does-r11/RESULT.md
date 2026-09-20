RESULT tools22-dog-nova-bus-the-rule-this-tool-does-r11 sha=5298f6be12ea — read `nova-bus The rule this tool does not enforce` against docs/CLI.md:560-563 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB The rule this tool does not enforce
DOC docs/CLI.md:560-563
REPLICA 11 of 24
BUILD could not be confirmed: nova-version failed to execute in the sandbox (Permission denied, exit 126); /home/ubuntu/.local/bin is unreadable under landlock. git rev-parse HEAD prints 5298f6be12eaa0f7e6622334d2b6a1eb427649e3, matching the card's base.

The section is four lines of prose (`### The rule this tool does not enforce`, blank, and two prose lines). It contains no fenced command blocks, so there were no commands to run, compare, or judge.

| n | command | exit | verdict |
|---|---------|------|---------|
| (none) | no fenced command blocks in docs/CLI.md:560-563 | — | CLEAN |

RAN 0
SKIPPED 0
Left owed: nothing — the section makes no runnable claim and asks no tool to do anything. The only environmental note is that the installed `nova-version` binary could not be executed (landlock denies read of /home/ubuntu/.local/bin, "Permission denied", exit 126), so the installed-build check could not be performed; because the section contains no commands, no binary was needed to judge it.

git status --short (empty):