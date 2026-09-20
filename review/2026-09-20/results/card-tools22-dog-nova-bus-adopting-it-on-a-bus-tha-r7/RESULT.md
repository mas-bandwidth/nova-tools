RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r7 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
SKIP the section's two command blocks are templates in `<dir>` and `<you>` for a bus that already exists, and the section never tells the reader how to create the bus, the reader identity, or the `origin`/`main` remote it presumes; running them verbatim fails in the shell before nova-bus is ever invoked (`<dir>` parses as redirection), so there is no fixture to exercise.

- TOOL nova-bus
- VERB Adopting it on a bus that already exists: the switch day
- DOC docs/CLI.md:542-555
- REPLICA 7 of 24
- BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

Setup notes:
- `git rev-parse HEAD` = `5298f6be12eaa0f7e6622334d2b6a1eb427649e3`, the card's base. OK.
- `nova-version` was not on PATH (`command not found`, exit 127) and there was no preinstalled binary anywhere. To have a same-build tool to read against, I built `nova-bus` and `nova-version` from the pinned checkout (HEAD = the base) into `<JOBDIR>/scratch/bin`; the built version line carries `5298f6be12ea`, the card's base sha. So the binary read is the base build, built from the base tree.

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | `nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)"` | 1 | SKIP |
| 2 | `nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main` | 1 | SKIP |

SKIP reasons per command:
- Block 1: `nova-bus check --bus <dir> ...` — `<dir>` is a placeholder for a bus that already exists ("A bus written by hand for months"); the section never tells the reader how to make the bus, so it cannot be run as written. Run verbatim from scratch it dies in the shell (`/bin/bash: dir: No such file or directory`, exit 1) because `<dir>` is input redirection; nova-bus is never invoked.
- Block 2: `nova-bus inbox --bus <dir> --as <you> ...` — same `<dir>` placeholder plus `<you>` (a reader identity the section never defines) plus `--remote origin --branch main` presuming a git remote the section never sets up. Verbatim run fails identically in the shell (exit 1) before nova-bus runs.

No DRIFT lines: the flag surface named by the section (`--full`, `--legacy-before`, `--legacy-now`, `--carry-history`, `--receipt-max-words`, `--advance`, `--remote`, `--branch`, `--rebuild-index`) all exist in `nova-bus help` with the same spelling; no verb or output field is missing, and no required flag is undocumented. The prose claims about `INBOX LEGACY`, `INBOX SWITCH`, and the `--advance` refusal could not be checked because no bus fixture exists.

RAN 0
SKIPPED 2

First 15 lines of output for each SKIP (the shell failure before the tool ran):
- Block 1: `/bin/bash: dir: No such file or directory` (exit 1)
- Block 2: `/bin/bash: dir: No such file or directory` (exit 1)

Left owed: everything in this section depends on a bus that already exists, a reader identity, and a real git remote — none of which docs/CLI.md:542-555 tells the reader how to make. Nothing in the section could be exercised against a real bus on this machine, and I did not fabricate one (the card forbids inventing a fixture the section never explains). The prose-only claims (INBOX LEGACY / INBOX SWITCH lines, the `--advance` refusal message, `check --full --rebuild-index`) are therefore unjudged.

git status --short in <JOBDIR>/repo prints nothing (clean).