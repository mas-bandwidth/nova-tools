RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r1 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
SKIP the section cannot be exercised here: the installed nova-* tools are unreadable/unexecutable under this sandbox (`/home/nova/.local/bin` is not in the sandbox's granted read set; both `nova-version` and `nova-bus` fail with `Permission denied`), and docs/CLI.md:556-559 contains no fenced command block to run

TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 1 of 24
BUILD <not available: `nova-version` printed no version, only the sandbox failure `/usr/bin/bash: line 1: /home/nova/.local/bin/nova-version: Permission denied`>

Command blocks in the section: 0 (the section is one prose paragraph; `grep -c '```'` over lines 556-559 returned 0). No table rows.

RAN 0
SKIPPED 0

Left owed: the whole section. `nova-bus --decide` could not be run, so none of the document's claims could be verified against the tool: the `kind`/`needs_reply`/`blocked`/`conf`/`wake`/`owner`/`ref` fields on `INBOX NOTE`, the `INBOX DECIDED n=<n> needs_reply=<m> below_floor=<b> wake=<w>` closing line, the `--decide`/`--floor`/`--key-env`/`--base-url`/`--allow-private` flags, the `.public` marker refusal, the `STOP:`/`HOLD:` rule (`kind=edge needs_reply=1.00 wake=needs-action`), and the laziness claim (empty inbox makes zero provider calls). `nova-version` was required by STEP 1 to confirm the installed build and printed nothing usable.

Verification notes (verbatim):
- `git rev-parse HEAD` → `5298f6be12eaa0f7e6622334d2b6a1eb427649e3` (matches base).
- `nova-version` → `/usr/bin/bash: line 1: /home/nova/.local/bin/nova-version: Permission denied`
- `nova-bus --help` → `/usr/bin/bash: line 1: /home/nova/.local/bin/nova-bus: Permission denied`
- `ls -la /home/nova/.local/bin/nova-version` → `-rwxr-xr-x 1 nova nova 7229602 Sep 20 20:25 /home/nova/.local/bin/nova-version` (file exists but the sandbox denies read/exec; Landlock grants read only on the job dir, harness-v1.18.20, `/home/nova/sdk` and go mod cache, per native-argv.log).
- `git status --short` → nothing printed.