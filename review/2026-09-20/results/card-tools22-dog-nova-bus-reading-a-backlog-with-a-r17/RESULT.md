RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r17 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 17 of 24
BUILD devel linux/amd64 go1.26.6 (git rev-parse HEAD=5298f6be12eaa0f7e6622334d2b6a1eb427649e3; pre-installed /home/ubuntu/.local/bin/nova-version unreadable due to directory permission denied — built from repo at pinned base)

| n | command | exit | status |
|---|---------|------|--------|
| 1 | sed -n '556,559p' docs/CLI.md | 0 | CLEAN — printed lines 556-559; header on 556, prose on 558 describing `--decide`, empty on 557/559 |
| 2 | go run ./cmd/nova-bus --help | 0 | CLEAN — `--decide [--floor <f>] [--key-env <name>] [--base-url <url>] [--allow-private]]` present on `inbox` usage line |
| 3 | go run ./cmd/nova-version version | 0 | CLEAN — prints `devel linux/amd64 go1.26.6`; SHA matches pinned base via git rev-parse HEAD=5298f6be... |
| 4 | nova-bus inbox --bus <dir> --as <name> --receipt-max-words 40 --decide ... | N/A | SKIP requires: a bus directory (--bus), a user identity (--as), and JEV_API_KEY env var plus network access to TypeSafe Jev endpoint; none provided by the section |

RAN 3
SKIPPED 1

Git status: (nothing — repository unmodified)

Left owed
Verifying actual `INBOX NOTE` and `INBOX DECIDED` output fields against live invocation: the section describes output formats (`kind=<k> needs_reply=<p> blocked=<p> conf=<c> wake=<w> owner=<lane> ref=<refs>` on INBOX NOTE lines; `INBOX DECIDED n=<n> needs_reply=<m> below_floor=<b> wake=<w>` on summary) but provides zero fenced commands demonstrating these outputs, so a live test cannot be attempted here without inventing a bus fixture and JEV credentials. A subject starting with `STOP:` or `HOLD:` being auto-marked `kind=edge needs_reply=1.00 wake=needs-action` also cannot be verified without such a fixture. All described flags are confirmed present in tool usage. No drift detected.
