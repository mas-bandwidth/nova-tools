RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r17 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
SKIP the section presumes a pre-existing hand-written bus ("a bus written by hand for months", "a bus that already exists") and never constructs one; both fenced blocks use <dir>/<you> placeholders and need a git remote named origin, so neither block could be exercised on this machine (run verbatim, bash fails on `<dir` before the tool starts)

TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 17 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1 (built from pinned repo at 5298f6be12eaa0f7e6622334d2b6a1eb427649e3; vcs revision stamp 5298f6be12ea confirms base build)

STEP 1 note: `git rev-parse HEAD` in repo printed 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 (matches base). `nova-version` was NOT pre-installed in this sandbox (`command not found`); I built `nova-version` and `nova-bus` from the pinned repo into <JOBDIR>/scratch (no file in the repo changed). The built `nova-version version` prints `nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1`; the trailing revision stamp is the 12-hex of the card's base, so the binary read against is the base build.

| n | command (one line) | exit | verdict |
|---|--------------------|------|---------|
| 1 | nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | 1 (shell; tool never ran) | SKIP |
| 2 | nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main | 1 (shell; tool never ran) | SKIP |

No DRIFT lines: none of the flags the document names is missing from the built tool. Read-only probe `nova-bus help` shows `check --bus <dir> (--full | --as <name> | --since <commit>) [--legacy-before <date-or-instant>] [--rebuild-index]` and `inbox --bus <dir> --as <name> --receipt-max-words <n> ... [--full] [--legacy-before <date-or-instant>|--legacy-now|--carry-history] [--advance --remote <name> --branch <name> ...]`; `--legacy-before`, `--legacy-now`, `--rebuild-index` all exist. No exit status or output field could be compared because the tool never ran.

First lines of output for the two attempts (verbatim):

Attempt 1 (block 1), run verbatim from <JOBDIR>/scratch:
```
/bin/bash: dir: No such file or directory
```
exit=1 (bash interprets `--bus <dir>`'s `<dir` as an input redirect from a file named `dir`; the file does not exist, so `nova-bus` never starts).

Attempt 2 (block 2), run verbatim from <JOBDIR>/scratch:
```
/bin/bash: dir: No such file or directory
```
exit=1 (same shell-level placeholder failure; `nova-bus` never starts).

RAN 0
SKIPPED 2
Left owed: nothing runnable actually executed the tool, so no line-by-line comparison of the documented `INBOX LEGACY` / `INBOX SWITCH` output fields or of the "refusal hands you the exact line to run" behavior was possible — those need a real pre-existing bus, reader identity and origin remote, which the section never tells the reader how to make and which this machine does not have. The section's prose (lines 549-555) was read in full but its claims could not be exercised.