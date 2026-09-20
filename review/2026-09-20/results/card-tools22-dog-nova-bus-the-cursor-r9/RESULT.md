RESULT tools22-dog-nova-bus-the-cursor-r9 sha=5298f6be12ea — read `nova-bus The cursor` against docs/CLI.md:522-527 and say CLEAN or DRIFT
BROKEN tool cannot run: nova-bus and nova-version are exit 126 "Permission denied" on every access
TOOL nova-bus
VERB The cursor
DOC docs/CLI.md:522-527
REPLICA 9 of 24
BUILD unverifiable — nova-version refused to run (exit 126 Permission denied); git rev-parse HEAD = 5298f6be12eaa0f7e6622334d2b6a1eb427649e3, the card's base

RAN 0
SKIPPED 0

Section docs/CLI.md:522-527 (`### The cursor`) contains NO fenced command blocks — it is two
prose paragraphs describing the CURSOR/OPEN/INDEX lane files and the `--full --advance`
remedy. There is therefore no command from the section to execute verbatim.

The section's tool, however, cannot be exercised at all on this machine. Every invocation is
denied by this environment's sandbox filter regardless of POSIX mode:

$ nova-version
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied
exit 126

$ nova-bus --help
exit 126
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied

The binaries are `-rwxr-xr-x` owned by ubuntu; the directory `/home/ubuntu/.local/bin` is
`0775` owned by ubuntu; the running user is `uid=1000 ubuntu`. Yet even opening the file for
read (`cat`, `exec 3< file`) fails with "Permission denied". `sudo` is unavailable ("the
'no new privileges' flag is set"). The job's own pre-approved `.nova-sandbox-tmp` contains a
filter library (`.bcdfe7fefcf5f72f-00000000.so`) and the `shim/` dir wraps only `bash`/`sh`,
not the nova tools. So the installed build identity (nova-version) cannot be obtained and
nova-bus cannot be run, making any "reading of a document against the installed tool"
unverifiable (per the card: such a reading would prove nothing anyway).

Because the section has no command blocks, there is nothing to judge line-by-line for drift;
because the documented tool is outright refused execution here, the section cannot be
claimed CLEAN.

Left owed
- Confirmation of the installed nova-bus/nova-version build sha (card Step 1): impossible here,
  the binaries are refused execution, not merely a sha mismatch.
- Any judgment of the section's prose claims (cursor refusal behavior, CURSOR/OPEN/INDEX
  layout, `--full --advance` remedy) against real tool output: the tool does not run.

git status --short below (must print nothing from the repository):
