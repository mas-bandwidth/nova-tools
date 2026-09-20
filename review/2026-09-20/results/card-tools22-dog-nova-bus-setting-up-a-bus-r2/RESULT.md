RESULT tools22-dog-nova-bus-setting-up-a-bus-r2 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
BROKEN the installed nova-bus/nova-version binaries cannot be read or executed under this sandbox's landlock wall: /usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied (exit 126)

TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 2 of 24
BUILD none — nova-version printed only `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied` (exit 126); the binary is denied read AND exec, so the installed build cannot be confirmed against the card's base sha 5298f6be

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | n/a | SKIP |
| 2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | n/a | SKIP |
| 3 | nova-bus check --bus ~/my-bus --full | 126 | BROKEN |

BROKEN docs/CLI.md:431 the documented command `nova-bus check --bus ~/my-bus --full` could not run at all: the tool binary is unexecutable here.
Exact failure, verbatim (run from <JOBDIR>/scratch):
```
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied
exit=126
```

RAN 1
SKIPPED 2

First 15 lines of output for each DRIFT: none — there are no DRIFTs; the section could not be exercised because the tool could not run (verdict BROKEN).

Left owed:
- The two shell setup commands (cp, git init, git add, git commit) could not be judged: their target `~/my-bus` is `/home/ubuntu/my-bus`, outside the job directory, which the card forbids writing to; the sandbox also denies all access to `/home/ubuntu` (listing and reading `/home/ubuntu` and `/home/ubuntu/.bashrc` each returned Permission denied), so the bus could not be made as the document says.
- `nova-bus check --full` could not be judged CLEAN or DRIFT because no nova-bus invocation can run in this sandbox. Step 1 of the card (confirm the installed build) is likewise unfulfillable: nova-version printed nothing but a Permission denied.
- Evidence that this is environmental, not a tool bug: `stat` shows /home/ubuntu/.local/bin/nova-version is 0755 owned by ubuntu, yet `cp`, `head`, `file` and direct exec are all refused; sudo is disabled ("no new privileges"); there is no other copy of the tool under the job directory (the repo holds only cmd/nova-bus source, and building it would violate "you add no file").

git status --short at the end:
```
<empty — nothing printed>
```