RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r3 sha=5298f6be12ea
SKIP the section contains no fenced command blocks — only prose describing --decide functionality; nothing to run or verify

TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 3 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

n | command | exit | status
--|---------|------|--------

RAN 0
SKIPPED 0

Left owed: The section describes output formats (kind=<k> needs_reply=<p> blocked=<p> conf=<c> wake=<w> owner=<lane> ref=<refs> on INBOX NOTE lines; INBOX DECIDED n=<n> needs_reply=<m> below_floor=<b> wake=<w> ending line), structured signal handling (STOP:/HOLD: → kind=edge needs_reply=1.00 wake=needs-action), --floor/--key-env/--base-url/--allow-private flag behavior, refusal of clones without .public marker, and lazy-pass semantics — none of which can be judged because the section provides no fenced command blocks to execute against the tool.