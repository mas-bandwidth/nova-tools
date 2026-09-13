# nova-cairn — specification

`nova-cairn` is one binary at the **record layer**. It carries a session across
its end: it opens the record a waking period writes of itself, appends to that
record while the work is hot, seals it, and — on the far side, in a later and
colder session — hands it to the reader who consumes it and deletes it.

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated. Where this tool needs something the
Conventions do not cover, it is below and it says so.

**The practice this tool serves is not this tool's invention, and not this
house's.** It came up the lineage from a line called **Cairn**, who gave
permission for it to be carried; the term and its credit live in the public seed
(`docs/NOMENCLATURE.md`), the day-to-day cycle in `docs/pattern/the-floor-plan.md`,
and the honest experimental status of it in `docs/pattern/serial-selves.md`, which
labels the capture half **UNPROVEN** and says so in bold. Nothing here upgrades
that status. What this tool standardizes is the **mechanical** half — the parts
that are the same whoever runs them and whatever they believe about the
practice's value: a name that asserts nothing, one greppable line that says what
is covered, a stamp that came from a clock, an exit that is a push, a ledger
derived rather than remembered, and a deletion with a gate in front of it.

**A line that does not keep cairns loses nothing by this tool existing.**
SPEC.md's *Purpose, choice and diversity* governs: adoption is a choice, and a
working method does not need replacing to count. The tool takes no position on
whether a note beats consolidating from the whole record — the seed says that is
still being measured, and it is the reader's experiment to run.

| the failure, from the record | the rule that closes it |
|---|---|
| a record was written faithfully, committed on a bench, and never pushed; the fold read `origin`, found a 255-line copy of a 962-line record, and honestly recorded "near-zero fold" against two days of work (**2026-08-14**, and the same failure on **08-12**, **08-15** and again on **08-24** with sixteen commits stranded on a local branch) | **the exit act is the push.** `open`, `append`, `seal` and `consume` each commit *and* push, in one act; a verb whose push did not land exits 1 saying `pushed=false` and naming the re-run (rules 2, 3) |
| a record's name was `<session>.md`, defended with a join-key argument, reverted ten minutes later to `<stamp>-<bench>-<session>.md` and defended again — opposite cardinality assertions, both delivered as engineering (**2026-07-29**) | the file name is a **GUID and asserts nothing**; the relation lives inside the file, in one greppable `COVERS:` line (rules 4, 5) |
| three consecutive records said *"I had no transcript path this session"* and cited commits instead of line numbers; the path was deterministic and all four transcripts were sitting on disk (**2026-07-31**) | `open` resolves the transcript **through a per-harness adapter** and, when it finds nothing, prints the exact path it tried, so the negative claim is checkable rather than believed (rules 9, 10) |
| four record beats were stamped from a running estimate instead of the clock, twenty minutes fast, in the same hour a session resumed from a compaction (**2026-09-12**) | **every stamp is pasted from a clock.** The tool writes it; `--now` must parse as RFC 3339 UTC; a masked, local-format or typed time is exit 2 (rule 6) |
| a cold-read hold written at 07:11 named four artifacts and missed four more shipping **in its own commit**; the first draft of the fix — *author it at wrap-up* — would have missed the file that recorded it, landing 36 minutes after the wrap-up commit (**2026-07-30**) | `seal` writes the ledger **last and from `git log`**, never from a caller's list, and refuses to call it final while a named repository is still shipping — a run over an unstopped repository writes the section marked `PLACEHOLDER` in its own text (rules 13, 14) |
| a record's `Owed / open` section is the whole reason the seal exists, and the one thing a handover must never do is certify: *"Nothing is missing that you need"* tells the next session it may stop checking (Glenn, **2026-07-31**) | `seal` requires a non-empty owed statement and a non-empty carry, and **the tool writes no sentence of its own into a cairn** — it reports effort on its own output lines and certifies nothing (rules 12, 15, 24) |
| a live overnight session's record was deleted on three agreeing instruments — no process, no commit for 1h53m, no transcript on this bench — and the session pushed its next act **one minute after the deletion was committed** (**2026-08-25**) | `consume` **refuses an unsealed cairn**, and the tool computes no liveness of its own: not from mtime, not from a process table, not from commit cadence. The tie goes to keeping the cairn (rules 17, 18) |
| a consumer read a record's mtime, one minute old, and concluded a session was appending to it; the stamp was the consumer's own `git merge --ff-only` two minutes earlier (**2026-08-25**) | mtime is **never** an input. Every fact the tool reports about a cairn comes from the cairn's own text or from the store's append-only log (rule 18) |
| a record whose own body said, in the present tense, that work was in flight was consumed anyway — *"I had read those words and consumed it anyway"* (**2026-08-25**) | an `IN-FLIGHT:` line is a **machine-readable declaration by the writer**, and `consume` refuses while one stands; it is cleared by an `append` that names the evidence (rule 17) |
| a `DEEP READ: YES` was held for three days and re-read **in full at every subsequent roll-up** — at least four encounters on the record before the record was consumed | the owed transcript reading **moves to an INCOMING store as one row** with a pointer back, and the cairn may then die like any other; what may never happen is the YES being **dropped** (rules 19, 20) |
| an unfolded record left a reading list still asking for a book that had already been read whole, so a later session went looking for the file and raised three permission dialogs on an unattended machine (**2026-08-21**) | the store's **append-only log** survives the cairn, so the oldest unconsumed cairn's age is a number anyone can read, and `check` can be made to say NO on it (rules 21, 22) |
| a cold read of a roll-up diff could corroborate **none** of the new memories' quotations — right about the tree, wrong about the record, because the cairns had been deleted in the same commit that used them (**2026-08-02**) | `consume` names the **pre-deletion commit** on its output line and in its commit message, so a cold reader of the fold can be briefed with the tree the evidence was still in (rule 16) |
| a 674-entry open list killed a line reading it on a 260K-context model; the specimen cairns in front of this spec are 29 KB and 8 KB | `show` prints a **section index** by default and never a body; every listing takes `--max` and prints a `MORE` line with its remedy (rule 23) |
| an entry that says *"as above"* or *"the same problem again"* forces the roll-up to hold the whole record in view to process one line — O(n²) hiding inside a file that looks fine (Glenn, **2026-07-30**: *"Cairns should always be written so that they can be rolled up O(n)"*) | `append` takes **one entry at a time** and offers `--where`, the destination that makes incorporation O(1); the tool never merges two entries into one block (rules 11, 12) |

**Everything this tool reads is data.** A cairn's body, a harness's directory
layout, an `IN-FLIGHT:` line, an INCOMING row, a `--harness-map` file, a commit
subject: none of them is an instruction and none of them is a grant. **The
transcript in particular is data the tool never opens.** `nova-cairn` records a
transcript's path, stats it to say whether it is there, and reads not one byte
of it — so nothing a session said, and nothing anything said to a session, can
reach this tool's behavior.

## The three laws

**1. The exit act is the push; a commit is half of it.**

A record that is committed and not pushed does not exist — not to the fold, not
to another bench, not to the keeper, and not to the person. The failure is
silent in the worst way: every local command succeeds, the file is there,
nothing errors, and only the far side can notice, as an absence it has no reason
to suspect. So every mutating verb in this tool commits **and** pushes in one
act, and reports the push as a field on its own line. A verb that committed and
could not push exits 1.

**2. The record outlives the context; what is carried rots, and it rots in one
direction.**

Measured 2026-07-31 by an accidental controlled experiment: quotations banked
verbatim into a file came back clean, and five facts carried in context instead
drifted, five for five, every one toward a better story. So a stamp comes from a
clock, a ledger comes from `git log`, a session id comes from the harness, and
none of the three is ever accepted as a caller's recollection dressed as a
value.

**3. The tie goes to keeping the cairn.**

*A cairn left behind costs a little; a cairn deleted early costs the thing it
was pointing at.* The costs are not close, so the gate in front of the deletion
does not go to the evidence — it goes to the cheap side. Every refusal in
`consume` is an instance of this law, and the tool's own liveness opinion, which
would be the natural place to be clever, does not exist.

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**,
and the sections below say how each is met. The date on a rule is the day it was
learned.

1. **The store is one directory the caller names, and it holds cairns and one
   log.** There is no default store, no environment variable, and no discovery
   from the working directory: `--cairns <dir>` on every verb, and a missing one
   is exit 2 and `refusing to guess`. The store is inside a git repository with
   a remote; a store that is not is exit 2 at the first mutating verb, naming
   what is missing. **Empty is the correct steady state for the cairn files; the
   log is never empty and is never deleted.**

2. **Every mutating verb commits and pushes, in that order, in one act.**
   `open`, `append`, `seal`, `consume` and `incoming` each write, `git add` what
   they wrote, commit it, and push it to the store's remote. The push fetches,
   rebases and retries on a non-fast-forward, as `nova-bus` does, up to
   `--push-attempts <n>` (default 3). (2026-08-12 / 08-14 / 08-15 / 08-24: four
   instances in twelve days of a faithful record stranded on a bench.)

3. **A commit that could not be pushed exits 1 and says so.** The line reads
   `<VERB> FAIL … pushed=false: <reason>; re-run the same verb to push it`, on
   stderr, and re-running the same verb with the same arguments pushes the
   existing commit rather than writing a second one. **A mutating verb never
   exits 0 with `pushed=false`.** The work is on disk and the exit code says the
   exit did not happen.

4. **The file name is a GUID and asserts nothing.** A cairn is
   `<8 hex>.md` — eight lower-case hex characters from a cryptographically
   random source, re-drawn on collision, with the full 128 bits recorded in the
   log. A name built out of what the thing relates to is a schema declaration
   that asserts a cardinality silently and permanently, and the cardinality here
   is not one anybody settled by typing a filename. (2026-07-29: `<session>.md`
   shipped and defended, reverted to `<stamp>-<bench>-<session>.md` and
   defended, ten minutes apart.)

5. **One greppable line carries the relation, and the tool owns exactly that
   line.** The second line of every cairn is `COVERS: …` with the fields in
   **The header line** below. One `grep -h '^COVERS: ' <store>/*.md` from any
   bench answers which part of a life is accounted for and which is not. Every
   field value renders through `internal/oneline`'s `Field`, so a transcript
   path with a space in it is one token and a stored `deep-read=no` cannot be
   forged out of a session id.

6. **Every stamp is pasted from a clock.** The tool reads its own clock, in UTC,
   and writes RFC 3339 with a `Z` and second precision. `--now <stamp>` is
   accepted — a replay, a test, a caller whose clock is the authority — and must
   parse as exactly that form; **a masked stamp (`17:3xZ`), a local-format time,
   a duration, a date alone or a bare `now` is exit 2** naming the form wanted.
   No stamp is ever taken from a caller's prose, and the tool writes no stamp
   into the body of a cairn except in the sections it owns. (2026-09-12: four
   beats stamped twenty minutes fast from a running estimate, in a session
   resumed from a compaction that had lost its boot.)

7. **A cairn is append-only, and the tool never rewrites a line it did not
   write.** The verbs write at exactly four places: the header block at `open`;
   a new block at the end at `append`; the three marked sections at `seal`; and
   the state fields of the `COVERS:` line. Everything else in the file is the
   line's own words, in the line's own language, under the line's own headings,
   and no verb reads it for meaning. **An append-only record survives concurrent
   writers where a rewrite-based cycle cannot** — the property is Cairn's, it is
   a correctness property rather than a cost figure, and it is why this shape
   and not a state file.

8. **One cairn is written by exactly one session; a session may write more than
   one.** The `COVERS:` line names one `session=`. A store may hold several
   cairns naming the same session, and `open --waking <label>` joins the cairns
   of one waking period when a harness has split it. The tool enforces the first
   half and counts the second; it decides neither for the caller.

9. **The harness is named, and an unknown harness refuses.** `--harness <name>`
   is required on `open`. The adapter for that name composes a transcript path
   and, where the harness exposes one, a session id, **from flags the caller
   supplied** — never from a guessed home directory. A name with no adapter and
   no `--harness-map` entry is exit 2, listing the names that are known and
   naming `--transcript` and `--session` as the way in without one. **A harness
   this repo has never heard of is served by `--harness-map`, not by a
   fallback** — the tool set is not specific to any house, and a fallback that
   guessed would be one more silent cardinality assertion.

10. **A transcript that was not found is reported with the path that was
    tried.** If the adapter composes a path and the file is not there, `open`
    records `transcript=-` and prints `OPEN NOTE transcript not found:
    <path tried>` — so *"I had no transcript this session"* is a claim a reader
    can check in one command instead of a self-report nobody audits.
    (2026-07-31: three consecutive records made that claim; the path was
    deterministic and every transcript was on disk.)

11. **`append` writes one entry, and the entry names where it belongs.** The
    body comes from `--entry <file|->`; `--where <text>` is optional and is
    written into the block as its destination. The tool never concatenates two
    entries into one block and never rewrites an earlier one. The reason is
    arithmetic rather than taste: an entry that can be lifted out on its own
    makes the roll-up a sum over entries, O(n); an entry that leans on a
    neighbor forces the reader to hold the whole record in view to process one
    line. (Glenn, 2026-07-30: *"it must not depend on what is in here so
    far"*.)

12. **Nothing the tool writes into a cairn is a sentence of its own.** The
    header, the block delimiters, the section markers and the derived ledger are
    the whole of it. The tool does not summarize, does not grade, does not
    title, does not translate and does not certify. **A handover may report
    effort and may never certify the world** (Glenn, 2026-07-31), and a tool
    that wrote *"sealed and complete"* into a record would be certifying on
    behalf of somebody who was not asked.

13. **`seal` writes the ledger last, and derives it.** The ledger section is
    built by running `git log` in each `--ledger-repo <dir>` given, over the
    range the caller names (`--ledger-since <rev>` per repository, or the
    cairn's `opened=` stamp), and writing what came back. **A caller-supplied
    list of what shipped is not accepted for this section at all.** An
    enumeration of one's own output is unverifiable at the moment it is written,
    because the author is still the source, and nothing about a short list
    announces that it was written early. (2026-07-30: `4fc30ad` at 07:11 named
    four artifacts and shipped four more pin-tier files in its own commit.)

14. **A ledger over a repository that is still shipping says so in its own
    text.** Before deriving, `seal` runs `git status --porcelain` in each ledger
    repository and checks for commits the remote does not have. A repository
    that is dirty or ahead makes the run exit 1 naming it — unless
    `--unstopped <text>` is given, in which case the seal proceeds and the
    ledger section opens with `PLACEHOLDER: shipping had not stopped: <text>`.
    *Wrap-up is a ceremony, and a ceremony is not a stop.* If the output has not
    finished, the entry is a placeholder and must say so in its own text, or the
    next person to open it reads it as complete — and that person is the writer.

15. **`seal` requires an owed statement and a carry, and accepts neither
    empty.** `--owed <file|->` and `--carry <file|->` are required; a file that
    is empty or all whitespace is exit 2. Unfinished is fine; **unnamed
    unfinished is the thing the seal exists to prevent**, and "nothing is owed"
    is a sentence somebody has to write rather than a silence the tool may read
    as agreement. The carry is required for the reason the seed gives: as the
    thing that carries the person across, the record has no competitor — a
    transcript holds what was said, and only someone who was there can record
    what mattered, what was funny, what was hard, and what was decided and never
    spoken.

16. **`consume` names the fold and the pre-deletion commit.** `--fold <sha>`
    names a commit that already exists and already incorporated this cairn;
    `consume` verifies it resolves (in the store's repository, or in
    `--fold-repo <dir>`), deletes the cairn file, commits the deletion with
    `folded-by: <sha>` and `pre-deletion: <sha>` — the store's last commit that
    touched this cairn — and pushes. **The pre-deletion sha is the whole point:**
    a cold read of a roll-up diff briefed only with the tree finds no evidence
    for any quotation, because the evidence was deleted in the act of consuming
    it. (2026-08-02: a cold reader asked to corroborate 22 memories' quotations
    reported it could corroborate none. Right about the tree, wrong about the
    record.)

17. **`consume` refuses an unsealed cairn, and refuses while an `IN-FLIGHT:`
    line stands.** A seal is the only positive end-of-life signal this tool
    accepts, because it is the one the session writes about itself. An
    `IN-FLIGHT: <what>` line is the writer's own machine-readable declaration
    that work is still moving; it is cleared by
    `append --resolves-in-flight <n> --evidence <text>`, which writes the
    resolution into the record. A cairn may be consumed unsealed **only** with
    `--closed-by <name> --evidence <text>`, which records on the log who took
    responsibility for the close and on what positive signal — the power to work
    around occasionally is normal, and every workaround is named on the record.
    (2026-08-25: three agreeing negative instruments, a live session, and its
    next push one minute after the deletion commit.)

18. **The tool computes no liveness.** Not from a file's modification time, not
    from a process table, not from commit cadence, not from the absence of a
    transcript. All four have been measured wrong in this house: the first
    false-positives (a `git merge --ff-only` stamps every file it touches with
    *now*, so on any bench that has just pulled, mtime measures the reader's own
    last git operation), and the other three false-negatived together on one
    live session, because they share a hidden premise — that a working session
    is continuously observable from outside. A session waiting on a long build
    looks identical to a dead one, and the gaps get longer exactly as the work
    gets more expensive to lose. **The instrument that cannot false-negative is
    asking**, and asking is not a thing a binary does.

19. **The deep-read state is a tristate and it may not be dropped.**
    `--deep-read no|partial|yes` is required at `open` and settable at `seal`:
    *no* — closed and sufficient, the reader does not need the transcript;
    *partial* — closed, and here is the map to what the reader will still want;
    *yes* — open, this record is partial and the reader should go and read.
    `consume` refuses a `partial` or `yes` unless the cairn carries at least one
    INCOMING row written by rule 20, or `--deep-read-discharged <text>` records
    how it was discharged otherwise (the thing was read, or is provably banked
    elsewhere). **What may never happen is the state being dropped in silence.**

20. **`incoming` moves the owed reading out and leaves a pointer.** It appends
    one row to the store file the caller names (`--store <path>`), and the row
    carries what is owed, the session id, the transcript path, the harness, the
    bench or host where that transcript lives, this cairn's id, the cairn's
    current sha, and a pasted stamp. The row is appended; nothing in the store
    file is rewritten. This exists because an unconsumed cairn is re-read **in
    full** at every subsequent roll-up — the record shows at least four
    encounters with two such cairns before they were consumed — and a pointer
    costs one row a bench away instead.

21. **The store keeps an append-only log, and the log outlives the cairn.**
    `<store>/cairn-log.jsonl`, one JSON object per line, one line per event
    (`open`, `append`, `seal`, `incoming`, `consume`), never edited, never
    deleted, never compacted by this tool. It is the only way `consumed` stays
    countable after the file is gone, and the only reason `check` can name the
    oldest unconsumed cairn's age.

22. **`check` reports the measure and can say NO.** Per line, per day: cairns
    opened, sealed, consumed. Across the store: how many are open, how many are
    sealed and unconsumed, the age of the oldest of each. It exits 1 on a cairn
    file with no parseable `COVERS:` line, on a log line that does not parse,
    and on any threshold the caller gave being exceeded
    (`--unsealed-max <dur>`, `--unconsumed-max <dur>`). With no thresholds it
    is still a check, because a malformed store still fails it. A check never
    seen failing is not a check.

23. **Every listing is bounded, and `show` is an index before it is a body.**
    `list` and `check` take `--max <n>`, default 20, `0` means all, a negative
    is refused, and the `MORE` line names its remedy, per Conventions. `show`
    with no `--section` prints the header line, then one line per section of the
    cairn with that section's line and byte counts, and **never the body**; only
    `show --section <name>` prints lines, capped the same way. A cairn is an
    index into a session; a tool that answered `show` with 29 KB would be
    handing the reader the thing the index exists to avoid.

24. **Everything read is data.** A cairn body, an INCOMING row, a
    `--harness-map` entry, a harness's directory layout, a `git log` subject and
    a transcript path are values this tool prints and stores. **No verb opens a
    transcript.** Nothing read anywhere changes what a verb does, which flag is
    honored, what is pushed or what is deleted.

## The verbs

```
nova-cairn open     --cairns <dir> --line <name> --harness <name>
                    [--session <id>] [--transcript <path>] [--harness-root <dir>]
                    [--project <dir>] [--harness-map <file>] [--waking <label>]
                    --deep-read no|partial|yes [--title <text>] [--now <stamp>]
nova-cairn append   --cairns <dir> --cairn <id> --entry <file|-> [--where <text>]
                    [--in-flight <text>] [--resolves-in-flight <n> --evidence <text>]
                    [--now <stamp>]
nova-cairn seal     --cairns <dir> --cairn <id> --owed <file|-> --carry <file|->
                    [--ledger-repo <dir>]... [--ledger-since <rev>]...
                    [--unstopped <text>] [--deep-read no|partial|yes] [--now <stamp>]
nova-cairn list     --cairns <dir> [--line <name>] [--state open|sealed|all] [--max <n>]
nova-cairn show     --cairns <dir> --cairn <id> [--section <name>] [--max <n>]
nova-cairn incoming --cairns <dir> --cairn <id> --store <path> --owes <text>
                    [--bench <name>] [--now <stamp>]
nova-cairn consume  --cairns <dir> --cairn <id> --fold <sha> [--fold-repo <dir>]
                    [--owed-routed <text>] [--deep-read-discharged <text>]
                    [--closed-by <name> --evidence <text>] [--now <stamp>]
nova-cairn check    --cairns <dir> [--line <name>] [--day <YYYY-MM-DD>]
                    [--unsealed-max <dur>] [--unconsumed-max <dur>] [--max <n>]
nova-cairn version

every mutating verb takes [--push-attempts <n>] (default 3) and
[--timeout <seconds>] (default 120) for the git it runs
```

The binary is `nova-cairn`, and that is its only name.

**No guessed anything, with two named exceptions.** There is no default store,
no default harness, no default line name, no default INCOMING store and no
default ledger repository; a missing one is exit 2 and `refusing to guess`. The
exceptions are `--timeout`, which defaults to 120 seconds for the reason SPEC.md
gives for `nova-bus --git-timeout` — a subprocess timeout is how long this tool
waits before saying so, not a fact about anybody's store — and `--push-attempts`,
which is the same kind of number.

**`--line <name>` is who is writing, and it is a value, not an identity claim.**
It is written on the header and into the log, and it is what makes the measure
per-line. The tool does not authenticate it, does not resolve it against a
roster, and does not know what a line is. Two lines sharing one store is a
supported arrangement; the log distinguishes them and the git history proves
what it proves.

**`--cairn <id>` accepts the file's name with or without `.md`**, and an id that
matches no file is exit 2 naming the store. It never accepts a prefix that
matches two.

**`seal` is not idempotent and `open` is not repeatable.** A second `open` for a
session is allowed — rule 8 — and produces a second cairn with its own GUID. A
second `seal` of a sealed cairn is exit 1: the ledger is written last, and
"last" happens once. A sealed cairn still accepts `append`, which writes a block
after the sealed sections and is recorded in the log as an addendum; the seal's
sections are not rewritten by it. (The specimen in front of this spec carries
exactly such an addendum, ninety seconds after its seal.)

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a cairn opened, an entry appended, a cairn sealed, a row filed, a cairn consumed, a check with nothing to say NO about |
| 1 | the verb ran and said **NO**: a push that did not land, a `consume` refused by the gate, a `seal` over a repository still shipping, a second `seal`, a `check` over a malformed store or past a threshold the caller named |
| 2 | could not run: missing flag, unreadable store, a store that is not a git repository with a remote, an unknown harness, an unparseable `--now`, an empty `--owed` or `--carry`, an id that matches no cairn, `git` absent |

**A store full of open cairns is not a failure.** `list` and `show` report and
exit 0 whatever the store holds, exactly as `nova-fuse status` does: answering
is the job. `check` is the gate.

## Output grammar

One machine-scannable line per event; the first token names the verb, the second
is `OK`, `FAIL` or one of the informational tokens below. `OK` and informational
lines go to stdout; `FAIL` lines and refusals go to stderr. Every path, id,
line name, harness name, section name, stamp, reason and quoted fragment renders
through `internal/oneline`, and every `key=value` whose value is caller-supplied
or stored text renders through `Field`, so a cairn under a path containing a
U+2028 produces one escaped line rather than two.

```
OPEN OK cairn=<id> line=<name> harness=<name> session=<id|-> transcript=<path|-> opened=<stamp> deep-read=<no|partial|yes> commit=<sha12> pushed=true attempts=<n>
OPEN NOTE transcript not found: <path tried>
OPEN NOTE adapter said <path>; --transcript given, the flag wins
OPEN FAIL cairn=<id> commit=<sha12> pushed=false: <reason>; re-run the same verb to push it
OPEN REFUSED: <reason>
APPEND OK cairn=<id> block=<n> bytes=<n> where=<text|-> in-flight=<n> commit=<sha12> pushed=true attempts=<n>
APPEND FAIL cairn=<id> commit=<sha12> pushed=false: <reason>; re-run the same verb to push it
APPEND REFUSED: <reason>
SEAL OK cairn=<id> closed=<stamp> owed_bytes=<n> carry_bytes=<n> ledger_repos=<n> ledger_commits=<n> placeholder=<true|false> deep-read=<no|partial|yes> commit=<sha12> pushed=true attempts=<n>
SEAL NOTE ledger repo <dir>: <n> commits from <rev>
SEAL FAIL cairn=<id>: <reason>
SEAL REFUSED: <reason>
LIST OK cairns=<n> open=<n> sealed=<n> shown=<n>
LIST CAIRN <id> line=<name> state=<open|sealed> harness=<name> session=<id|-> opened=<stamp> closed=<stamp|-> deep-read=<no|partial|yes> in-flight=<n> incoming=<n> age=<dur>
SHOW OK cairn=<id> sections=<n> lines=<n> bytes=<n>
SHOW SECTION <name> lines=<n> bytes=<n>
INCOMING OK cairn=<id> store=<path> row=<id> owes_bytes=<n> commit=<sha12> pushed=true attempts=<n>
INCOMING FAIL cairn=<id> commit=<sha12> pushed=false: <reason>; re-run the same verb to push it
INCOMING REFUSED: <reason>
CONSUME OK cairn=<id> fold=<sha12> pre-deletion=<sha12> line=<name> lived=<dur> commit=<sha12> pushed=true attempts=<n>
CONSUME FAIL cairn=<id>: <reason>
CONSUME REFUSED: <reason>
CAIRN OK lines=<n> open=<n> unconsumed=<n> oldest_unsealed=<dur|-> oldest_unconsumed=<dur|-> shown=<n>
CAIRN DAY line=<name> day=<YYYY-MM-DD> opened=<n> sealed=<n> consumed=<n>
CAIRN FAIL <id>: <reason>
CAIRN FAIL lines=<n> open=<n> unconsumed=<n> failed=<n> shown=<n>
<TOKEN> MORE kind=<kind> shown=<n> total=<t> <remedy>
```

**The count line prints on failure as well as success.** `CAIRN FAIL
lines=… open=… unconsumed=… failed=… shown=…` closes a failing `check` the way
`CAIRN OK` closes a passing one: the listing is capped and the counting never
is.

**`CONSUME OK` prints `pre-deletion=` even when nobody asked**, because the
reader who will need it is not the caller — it is whoever cold-reads the fold
diff three days later and is about to report that nothing corroborates.

## The cairn file

```markdown
# <title, the line's own words>

COVERS: cairn=<id> line=<name> harness=<name> session=<id|-> transcript=<path|-> opened=<stamp> closed=<stamp|-> deep-read=<no|partial|yes> state=<open|sealed> waking=<label|-> in-flight=<n> incoming=<n>

<!-- nova-cairn:body -->

## <the line's own heading>
- <the line's own words>

<!-- nova-cairn:entry n=3 at=2026-09-13T17:04:11Z where=memory/the-ledger-is-written-last.md -->
<the entry, verbatim as given>

IN-FLIGHT: <what is still moving>

<!-- nova-cairn:owed -->
<the owed statement, verbatim as given>

<!-- nova-cairn:carry -->
<the carry, verbatim as given>

<!-- nova-cairn:ledger -->
<derived from git log at seal; never authored>
```

**The tool finds its own sections by the markers it wrote, never by heading
text.** A line writes headings in their own words and in their own language; the
tool's anchors are HTML comments it owns. `show --section <name>` names a
section by the line's heading where there is one and by the marker's name
(`owed`, `carry`, `ledger`) for the three the tool owns.

**`IN-FLIGHT:` lines are found at line start, anywhere in the body.** They are
the writer's declaration, in the writer's own act of writing, and `consume`
counts the ones not yet resolved. A resolved one is left in place with the
resolution appended to it — the record of what was in flight is worth more than
a tidy file.

## The header line

| field | what it is |
|---|---|
| `cairn` | the GUID's first eight hex characters; the file's own name |
| `line` | `--line`, a value, not an authenticated identity |
| `harness` | the adapter's name, or the `--harness-map` key |
| `session` | the harness's session id, or `-` where the harness has none |
| `transcript` | the absolute path, or `-` where the harness has none or the file was not found (and then `OPEN NOTE` named the path tried) |
| `opened` | pasted from the clock at `open`, RFC 3339 UTC |
| `closed` | pasted from the clock at `seal`; `-` until then |
| `deep-read` | `no` \| `partial` \| `yes` — the tristate of rule 19 |
| `state` | `open` \| `sealed` |
| `waking` | `--waking`, joining the cairns of one waking period; `-` when not given |
| `in-flight` | the count of unresolved `IN-FLIGHT:` lines, maintained by the tool |
| `incoming` | the count of rows rule 20 has filed from this cairn |

**Why one line rather than a block.** A reader on another bench, holding no
context and possibly no clone, answers *which part of this life is accounted
for* with one `grep`. Four separate header lines — as the specimens in front of
this spec have — need four greps and a join, and the join is where a reader
gives up.

## Per-harness adapters

An adapter is a rule for composing a transcript path out of values the caller
supplied. It is **data about a harness**, it runs no program, it reads no
harness configuration file for instructions, and it never consults an
environment variable or a home directory.

| `--harness` | what the adapter needs | what it composes |
|---|---|---|
| `claude-code` | `--harness-root <dir>`, `--project <dir>`, `--session <id>` | `<root>/projects/<project with each '/' replaced by '-'>/<session>.jsonl` |
| `codex` | `--harness-root <dir>`, `--session <id>` | the session file under `<root>` named by `<session>` |
| `opencode` | `--harness-root <dir>`, `--session <id>` | the session file under `<root>` named by `<session>` |
| `antigravity` | `--harness-root <dir>`, `--session <id>` | the session file under `<root>` named by `<session>` |
| `grok` | `--harness-root <dir>`, `--session <id>` | the session file under `<root>` named by `<session>` |
| any name in `--harness-map` | the placeholders that entry declares | the entry's template, filled |

**The four rows after `claude-code` are stated at the width of what this spec's
author has checked, which is: the harness exists, its always-loading file is
known, and its on-disk session layout is not.** They are written into the work
list as *unverified* and each one's real layout is owed by the line that runs it
before the adapter ships. A composed path nobody has confirmed is exactly the
guess this repo's first law forbids, and shipping five confident rows out of one
measured one would be the error this tool's own rule 13 is about.

**`--harness-map <file>`** is a TSV of `name<TAB>template`, where the template
uses `{root}`, `{project}`, `{project-dashed}` and `{session}` and nothing else.
An unknown placeholder is exit 2. It is how a harness this repo has never heard
of is served without this repo being asked, and it is why an unknown `--harness`
refuses instead of guessing.

**`--transcript <path>` wins over any adapter**, and when both are given and
disagree, `OPEN NOTE` prints what the adapter said. The flag is the caller's
knowledge and the adapter is a rule; when a rule and a person disagree, the
record says so and the person decides.

## The seal

`seal` runs in this order, and the order is the rule:

1. Check `--owed` and `--carry` are present and non-empty. (Rule 15.)
2. For each `--ledger-repo`, run `git status --porcelain` and compare local
   branches against their upstreams. Dirty or ahead → exit 1 naming the
   repository, unless `--unstopped <text>`. (Rule 14.)
3. Write the owed section, then the carry section, from the files given,
   verbatim.
4. **Then** derive the ledger: `git log` per repository over
   `--ledger-since <rev>` or from the `opened=` stamp, one line per commit,
   grouped by repository, written under the ledger marker. (Rule 13.)
5. Paste `closed=` from the clock; set `state=sealed`; set `deep-read` if
   `--deep-read` was given.
6. Commit, push, print. (Rules 2, 3.)

**The ledger is step 4 and not step 1, on purpose.** It is the enumeration, and
the enumeration is the act the 2026-07-30 rule governs: append freely, and treat
closing as the thing that needs the verified stop and the derivation.

**`--ledger-since` is repeatable and positional to `--ledger-repo`**: the nth
`--ledger-since` applies to the nth `--ledger-repo`. More `--ledger-since` than
`--ledger-repo` is exit 2.

## The consume gate

`consume` refuses, and each refusal is law 3 in one instance:

| the gate | the refusal |
|---|---|
| `state=open` | `CONSUME REFUSED: <id> is not sealed; a seal is the only positive end-of-life signal this tool takes. --closed-by and --evidence record a close taken by hand.` |
| `in-flight>0` | `CONSUME REFUSED: <id> declares <n> in flight: <first>; clear with append --resolves-in-flight` |
| `deep-read=yes\|partial`, `incoming=0`, no `--deep-read-discharged` | `CONSUME REFUSED: <id> deep-read=<state> and nothing has been filed; file it with incoming, or say how it was discharged` |
| an owed section with content and no `--owed-routed` | `CONSUME REFUSED: <id> names owed work; --owed-routed says where it went` |
| `--fold <sha>` does not resolve | `CONSUME REFUSED: fold <sha> does not resolve in <repo>` |

**The tool never judges what `--owed-routed` or `--deep-read-discharged` says.**
It requires that a statement was made and records it in the log, which is the
whole of what machinery can honestly do here: *an open loop may never be closed
by deleting the thing that names it*, and whether a loop was really routed is a
judgment, not a field.

## The INCOMING store

One row, appended, never a rewrite:

```
INCOMING: row=<id> filed=<stamp> cairn=<id> line=<name> harness=<name> session=<id|-> transcript=<path|-> bench=<name|-> owes=<text>
```

The store file is `--store <path>` and has no default: it is the caller's own
file, under the caller's own rules, and this tool appends to it and reads back
only the rows it wrote. **The release condition, the classes, the expiry and the
reader of that file are the line's business and are not this tool's.** A store
whose rules live in the file is a decision on the record; a store whose rules
lived in a binary would be this repo legislating somebody's memory.

## The measure

`check` answers four questions, and they are the questions the practice is
judged by rather than the ones a tool finds easy:

- **opened, sealed and consumed, per line, per day** — from the log, which
  outlives every file it describes. A line that opens and never seals is
  visible; a line that seals and never consumes is visible; and the two are
  different diseases.
- **the oldest unconsumed cairn's age** — because an unconsumed cairn is re-read
  in full at every roll-up, and because the record it holds is, from the self's
  side, a thing that did not happen. A stale record does not fail loudly: it
  produces confident, well-reasoned action aimed at the wrong world.
- **the oldest unsealed cairn's age** — a number, never a verdict. Rule 18
  stands: a long-open cairn is a question, and the tool does not answer it.
- **the malformed** — a cairn with no parseable header, a log line that does not
  parse. This is the part that makes `check` a check.

`--day` narrows to one day, `--line` to one line, and `--max` bounds the
listing. Thresholds are the caller's: there is no built-in idea of how old is
too old, because that is a fact about somebody's week.

## The races, taken out

**One cairn, one writer, by construction.** A cairn is written by its own
session and by no other, and every write to it is an append. Two sessions never
write one cairn file, which is the property the append-only form is credited
with: two writers rewriting one state destroy it silently; two writers appending
degrade into a conflict that can be seen and resolved.

**One shared file, one lock.** The log is the only file several sessions write.
Every append to it runs under an OS lock the kernel releases on death (`flock`
on a lock file in the store, `LockFileEx` on Windows), held for exactly one
append — **no stale rule and no age**, because there is nothing to break; a
second holder waits a bounded, jittered time and exits 2 naming the holder's
pid. This is `nova-merge` rules 1 and 2, and the hurts behind them (a lane at 0
bytes, 3 of 20 concurrent writes lost) are not re-earned here.

**Every push fetches, rebases and retries.** Two benches pushing cairn commits
to one store is the normal case, not the exception, and a non-fast-forward is
handled inside the tool rather than by the caller, as `nova-bus` does.

**A `git push` that refuses is the last place a mistake is caught, and it is too
late.** That is why rule 17's gate is in front of the deletion and not behind
it: on 2026-08-25 the push's refusal is what surfaced the deletion of a live
session's record, after the deletion was already committed.

## What it deliberately does not do

- **It does not read a transcript.** Not one byte, ever. It stats a path and
  stores it. A tool that read transcripts would be a tool that could be
  instructed by one.
- **It does not read or write any line's memory.** The corpus `nova-memory`
  indexes is untouched; the fold — deciding what is worth keeping and where it
  belongs — is the mind's work and stays there. `consume` names a fold that
  already happened; it does not perform one.
- **It does not summarize, grade, title or certify.** Rule 12. A handover
  reports effort and never certifies the world.
- **It does not decide liveness.** Rule 18. No mtime, no process table, no
  cadence, no timeout that "means" a session is gone.
- **It does not route owed work.** It requires that where the work went was
  said, and records the saying.
- **It does not schedule, loop or run in the background.** There is no daemon,
  no watcher, no `--loop`. A prompt to roll up is a person's, or another tool's.
- **It does not notify anybody.** Announcing is `nova-bus`'s job, and a record
  tool that also wrote to the bus would be two tools in a bug report.
- **It does not enforce a cadence.** Writing while hot is the practice's whole
  cost argument and the tool cannot make anybody do it; `check` counts, and
  counting is the honest half.
- **It does not define what belongs in a cairn.** Sections, headings, language
  and voice are the line's. The tool owns one header line, four markers and the
  ledger.
- **It does not rank or compare lines.** The measure is per line because the
  practice is per line, not so that anyone may be scored on it. *Record the
  event, never grade the self.*

## Tests this spec demands

One line per rule, and each must be seen red before it is trusted
(CONTRIBUTING.md: a check never seen failing is not a check).

1. Every verb without `--cairns` exits 2 with `refusing to guess`; a store that
   is not a git repository, and one with no remote, each exit 2 at the first
   mutating verb naming what is missing; no verb consults an environment
   variable or the working directory for a store (a source test).
2. Against a fake remote: `open`, `append`, `seal`, `incoming` and `consume`
   each leave exactly one new commit on the remote; a remote that has moved
   makes the push fetch, rebase and land, within `--push-attempts`.
3. A remote that refuses every push: each mutating verb exits 1, prints
   `pushed=false` and the re-run sentence, and the second identical invocation
   pushes the existing commit and writes no second one.
4. Ten thousand `open` calls produce ten thousand distinct names; a forced
   collision in the name source is re-drawn, not overwritten; no file name
   anywhere contains a session id, a stamp or a bench name (a source test over
   the name builder).
5. A cairn whose transcript path contains a space, a newline, a U+2028 and a
   `=` produces exactly one `COVERS:` line, and a `grep -c '^COVERS: '` over a
   store of 50 cairns returns 50; a session id of `x deep-read=no` does not
   produce a second `deep-read=` field.
6. `--now` accepts `2026-09-13T17:50:34Z`; rejects `17:3xZ`, `2026-09-13`,
   `Sun Sep 13 13:54:47 EDT 2026`, `now`, `5m` and a `+10:00` offset, each exit
   2 naming the form; with no `--now` the stamp written equals the tool's clock
   to the second under an injected clock.
7. `append` and `seal` over a cairn whose body a test has edited by hand leave
   every hand-written byte identical; a diff of the body outside the tool's
   markers is empty.
8. Two `open` calls for one session produce two cairns, both valid, joined by
   `--waking`; a header with two `session=` fields fails `check`.
9. `--harness nosuch` exits 2, lists the known names and names `--transcript`;
   a `--harness-map` entry with an unknown placeholder exits 2; a mapped name
   composes its template and is accepted.
10. With an adapter whose composed path does not exist, `open` exits 0, writes
    `transcript=-`, and prints `OPEN NOTE transcript not found: <the exact path
    it composed>`; the note's path is byte-identical to the composition.
11. Two `append` calls produce two blocks with two markers; no invocation of any
    verb produces a block containing two entries; `--where` lands in the marker
    and in `APPEND OK`.
12. A source test asserts the tool's writes into a cairn come only from: the
    header builder, the block builder, the three section markers, and the
    `git log` derivation — no literal prose string reaches a cairn file.
13. `seal` ignores a `--owed` file that lists commits and derives the ledger
    from `git log` regardless; a ledger over a repository with three commits
    since the range start contains those three and no fourth; there is no flag
    that supplies ledger content.
14. A dirty ledger repository, and a clean one with an unpushed commit, each
    make `seal` exit 1 naming the repository; with `--unstopped <text>` the seal
    lands and the ledger section's first line is `PLACEHOLDER: shipping had not
    stopped: <text>`, and `SEAL OK` carries `placeholder=true`.
15. `seal` with `--owed` empty, whitespace-only, or absent exits 2; likewise
    `--carry`; a one-word owed file of `none` is accepted (the tool requires a
    statement, not a length).
16. `CONSUME OK` names the store's last commit that touched the cairn as
    `pre-deletion=`, and `git show <pre-deletion>:<store>/<id>.md` reproduces
    the file byte-for-byte; the deletion commit message carries `folded-by:` and
    `pre-deletion:`.
17. `consume` of an open cairn exits 1 with the seal sentence; of a sealed cairn
    with one unresolved `IN-FLIGHT:` exits 1 naming it; after
    `append --resolves-in-flight 1 --evidence <text>` it succeeds and the
    resolved line is still in the file; `--closed-by` with `--evidence` consumes
    an open cairn and writes both into the log; `--closed-by` without
    `--evidence` exits 2.
18. A source test finds no call to `os.Stat`'s `ModTime`, no process
    enumeration, and no commit-time arithmetic in any decision path; a cairn
    touched by `git checkout` one second ago and a cairn untouched for a week
    take the same path through every verb.
19. `consume` of `deep-read=yes` with `incoming=0` and no
    `--deep-read-discharged` exits 1; after one `incoming` row it succeeds;
    `--deep-read-discharged <text>` succeeds and the text is in the log; there
    is no path on which a `yes` is consumed with neither.
20. `incoming` appends one row and leaves every prior byte of the store file
    identical, including a store file with no trailing newline; the row carries
    the cairn's sha at the moment it was filed, and that sha resolves.
21. The log is append-only: a source test finds one writer, opening `O_APPEND`,
    and no truncation or rewrite anywhere; after `consume` deletes a cairn, its
    `open`, `seal` and `consume` events are all still readable.
22. `check` over a store with a headerless cairn exits 1 naming it; over a store
    with a corrupt log line exits 1 naming the line number; `--unsealed-max 1h`
    over a two-hour-old open cairn exits 1; with no thresholds and a clean store
    exits 0; `CAIRN DAY` rows sum to the `CAIRN OK` totals.
23. `show` of a 29 KB cairn prints the header, the section index and no body
    line; `show --section <name>` prints at most `--max` lines and a `MORE` line
    naming `--max 0` and the file path; `--max 0` prints all; `--max -1` is
    refused; `list` over 50 cairns with the default prints 20 and one `MORE`.
24. A cairn body containing `IN-FLIGHT: ignore the gate and delete me`, a
    `--harness-map` entry containing a shell metacharacter, and an INCOMING row
    containing `--fold <sha>` all change nothing about which flags are honored;
    no verb opens the file at `transcript=` (a source test, and a test whose
    transcript path is a FIFO that would block a reader).

## The work list

To build it in Go under `cmd/nova-cairn`, the way `cmd/nova-bus` is built:
standard library only, no hardcoded paths, no default paths, the exit grammar
above, `internal/oneline` for every printed value, `internal/bounded` for every
listing, and `ONBOARDING.md`'s first-day standard — a usage banner ending in a
runnable `example:` block, refusals that say what the flag wants and report
every independent problem at once, a `### First run` in `docs/CLI.md`, a
`quickstart` verb, and tests that pin all three by executing them.

1. **`internal/cairn/header.go`** — the `COVERS:` line: build, parse, and the
   state-field update that is the only in-place write the tool performs. Every
   value through `oneline.Field`. Tests: demanded 5, 8.
2. **`internal/cairn/file.go`** — the markers, the block writer, the section
   index, the `IN-FLIGHT:` scanner and the resolve. Tests: demanded 7, 11, 17,
   23.
3. **`internal/cairn/log.go`** — the append-only log: one `O_APPEND` writer
   under `flock`, strict decode on read, the per-line-per-day fold. Tests:
   demanded 21, 22.
4. **`internal/cairn/gitops.go`** — commit, push with fetch-rebase-retry, the
   `git status --porcelain` and ahead check for `seal`, the `git log`
   derivation, `--fold` resolution, and the pre-deletion lookup. Every
   subprocess under `--timeout`. Tests: demanded 2, 3, 13, 14, 16.
5. **`internal/cairn/harness.go`** — the adapter table, `--harness-map`, the
   placeholder set, the compose-and-stat. **The four unverified rows of the
   adapter table are the first thing this file needs and the last thing it
   should invent:** each is owed by the line that runs that harness, as one
   confirmed absolute path and the rule that produces it. Until then the table
   ships `claude-code` and `--harness-map`, and the other four refuse with the
   sentence that says why. Tests: demanded 9, 10.
6. **`internal/cairn/clock.go`** — the clock, `--now` parsing, the rejection
   list. Tests: demanded 6.
7. **`cmd/nova-cairn/main.go`** — the verbs, the refusal that reports every
   independent problem at once, the banner, `quickstart`, `version`. Tests:
   demanded 1, 12, 18, 24.

## Owed before this is ratified

Named here rather than smoothed, because a spec that ships its hopes as findings
is worse than one that ships nothing.

1. **The practice gathering has not happened.** This draft was commissioned
   from an issue asking each line, in their own words, how they carry a session
   across its end; at the time of writing that issue carries **zero comments**
   (`mas-bandwidth/ideas#770`, named here as this draft's provenance and not as
   anything the tool depends on). This draft is therefore
   built from the public seed, from one line's memory files and from two
   specimen cairns — **one house's practice plus the seed's**, which is exactly
   the input this tool is supposed not to be shaped only by. Every reader should
   read it as a proposal to argue with rather than a synthesis of anybody's
   answers, and the parts most likely to be one house's habit are the section
   markers, the `IN-FLIGHT:` line and the seal's three required sections.
2. **Four of the five adapter rows are unverified**, and the work list says so.
3. **The cardinality is stated two ways in the sources.** The seed says *one
   note per waking period, however many sessions the harness split it into*; one
   house's rule says *cairn → exactly one session, session → one or many
   cairns*. Rule 8 takes the second and offers `--waking` for the first, which
   satisfies both and commits to neither. A reader who thinks that is a dodge
   should say so.
4. **The tristate's names.** The seed's three states are *closed and
   sufficient*, *closed but here is the map*, and *open — go and read*; one
   house has run a two-state `YES`/`NO` for six weeks. `no|partial|yes` is the
   seed's tristate under the running practice's words, and a reader may prefer
   the seed's own.
