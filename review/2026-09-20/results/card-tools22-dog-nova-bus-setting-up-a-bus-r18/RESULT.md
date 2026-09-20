RESULT tools22-dog-nova-bus-setting-up-a-bus-r18 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
SKIP installed nova-bus binary cannot execute (permission denied despite stat 0755), example-bus source not accessible at referenced relative path from scratch working directory
TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 18 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6 (built from repo at base SHA 5298f6be12eaa0f7e6622334d2b6a1eb427649e3)
| n | command | exit | status |
|---|---------|------|--------|
| 1 | JSON block: participants.json content | n/a | CLEAN — syntax valid |
| 2 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | skip | SKIP — relative path cmd/nova-bus/testdata/example-bus not available from scratch; section assumes repo context |
| 3 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | skip | SKIP — blocked by prior command failure; also depends on line 2 creating ~/my-bus |
| 4 | nova-bus check --bus ~/my-bus --full | skip | SKIP — installed /home/glenn/.local/bin/nova-bus permission denied (stat 0755 but no-read/no-exec) |

Verification performed (using binary built from same repo SHA, not installed):
- nova-bus check --bus <minimal-bus-with-only-participants.json> --full → EXIT 0, output:
    BUS SCOPE mode=full cursor=- changed=0
    BUS OK notes=0 lanes=3 receipts=0 participants=4 warn=0
- nova-bus check --bus <repo>/cmd/nova-bus/testdata/example-bus --full → EXIT 0, output:
    BUS SCOPE mode=full cursor=- changed=0
    BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0
- participants.json from CLI.md:396-428 parses as valid JSON
- Check subcommand accepts --bus <dir>, --full, --as <name>, --since <commit>, --legacy-before, --rebuild-index
- Documented flag names and usage (--bus, --full) match actual tool interface
- Exit code 0 for passing check agrees with doc claim "it passes check --full"

RAN 3 (JSON validity, check --full on minimal bus, check --full on example-bus from built binary)
SKIPPED 3 (cp command, git-init command, documented nova-bus check invocation)

Left owed: full end-to-end exercise of the documented invocation path (nova-bus check --bus ~/my-bus --full via PATH-resolved binary); system-installed nova-* binaries unreadable/unexecutable on this host

git status --short (repo):
(no output)
