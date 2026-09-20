RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r21 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
BROKEN the installed nova tools cannot be executed in this sandbox: /home/ubuntu/.local/bin/nova-bus: Permission denied (exit 126); nova-version refuses identically, so the installed build can never be confirmed or run

TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 21 of 24
BUILD unknown — `nova-version` printed nothing; it was refused before exec: `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied` (exit 126)

The section spans exactly four lines (556-559) and contains NO fenced command blocks, so there is no section command to run in STEP 3. The document's claims can only be checked by running `nova-bus`, and the tool could not be run at all. What I did run:

1 | nova-version (STEP 1 build check) | exit 126 | BROKEN
2 | git rev-parse HEAD (STEP 1 build check) | exit 0 | CLEAN (printed 5298f6be12eaa0f7e6622334d2b6a1eb427649e3)
3 | nova-bus --help (probe) | exit 126 | BROKEN

No section command blocks were run: RAN 0. No command blocks were skipped for missing fixtures: SKIPPED 0.

Verbatim failure output (nova-version, identical for nova-bus):
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied
exit=126

Exact failure quoted for nova-bus:
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied

Left owed — the entire section could not be judged: every verifiable claim about `nova-bus` — the flags `--decide`, `--floor` (default 0.9), `--key-env` (default JEV_API_KEY), `--base-url`, `--allow-private`; the refusal of a clone lacking a `.public` marker; the `STOP:`/`HOLD:` structured-signal rule; the `INBOX NOTE` line carrying `kind= needs_reply= blocked= conf= wake= owner= ref=`; and the `INBOX DECIDED n= needs_reply= below_floor= wake=` summary — is unverifiable because the tool binary cannot execute in this sandbox (landlock denies the /home/ubuntu/.local/bin path; no nova binary exists anywhere else in the readable tree). A live run would additionally have needed a bus with notes and a TypeSafe Jev key/endpoint, which this sandbox does not provide, but the tool refusal alone blocks the reading. Head of the clone is exactly the pinned base (5298f6be12eaa0f7e6622334d2b6a1eb427649e3), so the tree is correct; only the installed tool is unreachable.

git status --short (run in repo/, output below — nothing):