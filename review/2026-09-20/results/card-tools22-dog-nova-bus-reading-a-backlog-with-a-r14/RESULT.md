RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r14 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
CLEAN

TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 14 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

The section (docs/CLI.md:556-559) is prose with NO fenced command blocks, so STEP 3
("take each fenced command block in that section") yields zero command blocks to run.
Every claim the prose makes about `--decide` was checked against the tool, built from
the pinned base source (`git rev-parse HEAD` = 5298f6be12eaa0f7e6622334d2b6a1eb427649e3);
the built `nova-version` reports the 12-hex revision 5298f6be12ea, matching the card's base.
The installed binaries at /home/ubuntu/.local/bin were unreadable inside the sandbox
(landlock grants do not cover that path; exec/cat returned "Permission denied"), so the
same-build tool was built from the pinned tree into the job dir and exercised there.

Verification runs (exit 0 each) and how each prose claim matched:

1. `nova-bus inbox --bus <dir> --as someone --receipt-max-words 40 --decide` on a clone
   with no `.public` marker -> `INBOX REFUSED: --decide sends note text to a provider
   that may train on it, and "<bus>" has no .public marker; pass --allow-private to mean
   it anyway, or leave that clone private`. Matches "refuses a clone with no `.public`
   marker, by name" and "`--allow-private` is the one explicit way to mean it anyway".

2. Empty inbox with `--decide`, no key set -> `INBOX DECIDED n=0 needs_reply=0
   below_floor=0 wake=0` at exit 0 with zero provider calls. Matches "The pass is lazy,
   so an empty inbox makes zero provider calls" and the `INBOX DECIDED n=<n>
   needs_reply=<m> below_floor=<b> wake=<w>` grammar.

3. A note with subject `STOP: ...`, fake JEV_API_KEY set, no network -> `INBOX NOTE
   id=... STOP: do not send this kind=edge needs_reply=1.00 blocked=0.00 conf=1.00
   wake=needs-action owner=someone ref=-` followed by `INBOX DECIDED n=1 needs_reply=1
   below_floor=0 wake=1`, exit 0, and no provider call. Matches "A subject starting
   `STOP:` or `HOLD:` ... is never sent: it is always marked `kind=edge needs_reply=1.00
   wake=needs-action` by rule".

4. A note with subject `HOLD: hold the batch, see #1617` and body `see acme/repo#12 and
   #5 #6 #7 #8 #9` -> `... kind=edge needs_reply=1.00 blocked=0.00 conf=1.00
   wake=needs-action owner=someone ref=#1617,acme/repo#12,#5,#6+3`. Matches the INBOX
   NOTE suffix field order/spelling `kind=<k> needs_reply=<p> blocked=<p> conf=<c>
   wake=<w> owner=<lane> ref=<refs>`, `owner` from the `To:` header, and `ref` as every
   `#<digits>` and `<owner>/<repo>#<digits>` in subject and body, at most four then
   `+<n>` (the four shown are the first four in order, +3 for the rest).

5. `--decide --key-env JEV_API_KEY --base-url http://127.0.0.1:1 --floor 0.9` on a
   non-structured note with key unset -> `INBOX REFUSED: decide: JEV_API_KEY is not set;
   refusing to guess` at exit 2. Matches `--key-env` default `JEV_API_KEY`; the key is
   never a file or an argument and is never printed.

6. Flag surface: `nova-bus help` lists `[--decide [--floor <f>] [--key-env <name>]
   [--base-url <url>] [--allow-private]]` on the inbox verb. Source confirms
   `--floor` default 0.9, `--key-env` default JEV_API_KEY, kind choices (start, done,
   question, edge, refusal, receipt), noul `needs_reply`/`blocked`, wake choices (ack,
   info, needs-action), body truncation to 600 chars with `sk-` keys redacted, and the
   INBOX DECIDED counts (needs_reply >= 0.5, below_floor = conf < floor, wake =
   needs-action/unknown). All agree with the prose.

Table of command blocks in the section: none (the section contains no fenced command
block, so there are no rows).

RAN 0
SKIPPED 0

Left owed: a live provider round-trip (a real typed decision with kind/needs_reply/
blocked/conf from TypeSafe Jev) was not exercised: it needs a JEV_API_KEY and a network
endpoint, which the section never supplies and which I did not invent. The output
grammar and the refusal/lazy/structured-signal paths were verified without a key, and
the provider-facing shape (600-char body, sk- redaction) is confirmed in the pinned
source. The installed binaries themselves were unreachable in the sandbox (Permission
denied on /home/ubuntu/.local/bin), so the same-build binary built from the pinned tree
was used; this does not change the verdict since the build reports the base revision.

git status --short: (empty)