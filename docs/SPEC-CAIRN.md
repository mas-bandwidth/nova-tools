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
| a record was written faithfully, committed on a bench, and never pushed; the fold read `origin`, found a 255-line copy of a 962-line record, and honestly recorded "near-zero fold" against two days of work (**2026-08-14**, and the same failure on **08-12**, **08-15** and again on **08-24** with sixteen commits stranded on a local branch) | **the exit act is the push.** `open`, `append`, `seal` and `consume` each commit *and* push, in one act; a verb whose push did not land exits 1 saying `pushed=false` and naming the `push` verb that retries it (rules 2, 3) |
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
| a 674-entry open list killed a line reading it on a 260K-context model; the two specimen records this spec was drawn from, whose shape is reproduced under *The cairn file* below, are 29 KB and 8 KB | `show` prints a **section index** by default and never a body; every listing takes `--max` and prints a `MORE` line with its remedy (rule 23) |
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

2. **Every mutating verb fetches before it reads, and commits and pushes before
   it exits.** `open`, `append`, `seal`, `consume` and `incoming` each fetch the
   store's remote and fast-forward the store, then write, `git add` what they
   wrote, commit it, and push it to the store's remote. The push fetches,
   rebases and retries on a non-fast-forward, as `nova-bus` does, up to
   `--push-attempts <n>` (default 3). A store that is behind and cannot
   fast-forward is exit 1 naming it, and nothing is written.
   **The fetch is in front of the gates and not only in front of the push.** A
   gate read from a stale clone is a gate read from the wrong record: a cairn
   whose `IN-FLIGHT:` line was appended from another bench an hour ago is a
   cairn this bench would consume, and the only instrument that closes that
   window is a fetch at the moment of the reading. (2026-08-12 / 08-14 / 08-15 /
   08-24: four instances in twelve days of a faithful record stranded on a
   bench.)

3. **A commit that could not be pushed exits 1 and says so, and the retry is a
   verb of its own.** The line reads `<VERB> FAIL … pushed=false: <reason>; run
   nova-cairn push --cairns <dir>`, on stderr. **A mutating verb never exits 0
   with `pushed=false`.** The work is on disk and the exit code says the exit
   did not happen.
   **The retry is defined on the store's state, never on the caller's argv.**
   `push` pushes whatever commits the remote lacks and writes nothing, and every
   mutating verb pushes those commits before it writes anything of its own. The
   tool infers no retry from a repeated command line, because it cannot: a
   second `append` with the same `--entry` is a legitimate second entry
   (rule 11), and two callers who disagree about which it was would build two
   different binaries out of one sentence.

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
   The line carries `covers=<text>`, the caller's own statement of which part of
   the life this cairn accounts for — `--covers <text>` at `open`, reset by
   `--covers` at `seal` — which the tool stores, prints, and judges not at all.
   Without it the line cannot do the job it is for: rule 8 lets one session
   write several cairns, and two cairns of one session are otherwise identical
   in every field of the one line that is supposed to say which part is
   accounted for and which is not.
   **The parser reads `COVERS:` at line 2 of the file and nowhere else**, and a
   cairn carrying a second `^COVERS: ` line fails `check` (rule 22). A header
   found by scanning is a header forgeable by anything a line pastes into its
   own body.

6. **Every stamp is pasted from a clock.** The tool reads its own clock, in UTC,
   and writes RFC 3339 with a `Z` and second precision. `--now <stamp>` is
   accepted — a replay, a test, a caller whose clock is the authority — and must
   parse as exactly that form; **a masked stamp (`17:3xZ`), a local-format time,
   a duration, a date alone or a bare `now` is exit 2** naming the form wanted.
   No stamp is ever taken from a caller's prose, and the tool writes no stamp
   into the body of a cairn except in the sections it owns. (2026-09-12: four
   beats stamped twenty minutes fast from a running estimate, in a session
   resumed from a compaction that had lost its boot.)
   **Form is not provenance, and this rule should not be read as more than it
   is.** A stamp typed from memory parses exactly as a pasted one does, so the
   masked-stamp refusal catches a typo and not a recollection. The enforceable
   half is that **the default path is the clock**: with no `--now` the tool
   reads its own. Beyond that, a `--now` further than `--now-skew <seconds>`
   (default 120) from the tool's own clock is exit 2 unless `--replay` is given,
   and every log event records `stamp=clock|given|replay`, so a reader can tell
   which kind of stamp a record is built on instead of assuming.

7. **A cairn is append-only, and the tool never rewrites a line it did not
   write.** The verbs write at exactly four places: the header block at `open`;
   a new block at the end at `append`; the three marked sections at `seal`; and
   the state fields of the `COVERS:` line, which are the only in-place writes
   the tool performs (`closed`, `state`, `deep-read`, `covers`, `in-flight`,
   `incoming`). Everything else in the file is the
   line's own words, in the line's own language, under the line's own headings,
   and no verb reads it for meaning. **An append-only record survives concurrent
   writers where a rewrite-based cycle cannot** — the property is Cairn's, it is
   a correctness property rather than a cost figure, and it is why this shape
   and not a state file.

8. **One cairn is written by exactly one session; a session may write more than
   one.** The `COVERS:` line names one `session=` and one `transcript=`, and
   that is the mechanical reason for the first half rather than a position on
   anybody's practice: one line cannot hold two transcript paths without
   becoming a list, and a list is where the `grep` of rule 5 stops working. A
   store may hold several cairns naming the same session, and
   `open --waking <label>` joins the cairns of one waking period when a harness
   has split it. **The label is read and not only written:**
   `list --waking <label>` and `check --waking <label>` filter on it and
   `CAIRN OK` carries `wakings=<n>`, so the unit the seed counts in is a unit
   this tool can count. The tool enforces the first half and counts the second;
   it decides neither for the caller.

9. **The harness is named; an unknown *composition* refuses, and a caller who
   already holds the path never does.** `--harness <name>` is required on
   `open`. **With `--transcript <path>` given, no adapter decides anything and any
   `--harness` value is accepted** — it is a value then, like `--line`, written
   on the header and into the log and resolved against nothing. A known and
   verified adapter still composes its path so that `OPEN NOTE` can say what it
   would have said, which is a message to the reader and not a decision; an
   unknown or unverified name composes nothing and the note is silent. Refusing a
   composition this repo cannot make is the no-guess law; refusing the *name*
   would be a closed door, and the line most likely to be standing at it is the
   one on a harness this repo has never adapted.
   Without `--transcript`, the adapter for that name composes a transcript path
   **from flags the caller supplied** — never from a guessed home directory —
   and a name with no adapter and no `--harness-map` entry is exit 2, listing
   the names that are known and naming `--transcript` and `--harness-map` as the
   two ways in. **A row marked unverified in the adapter table refuses the same
   way**, in a sentence that says it is unverified and what would verify it.
   The adapter composes a path and nothing else: the session id is `--session`
   passed through, since an adapter runs no program and has nowhere else to
   learn one. **A harness this repo has never heard of is served by
   `--harness-map`, not by a fallback** — the tool set is not specific to any
   house, and a fallback that guessed would be one more silent cardinality
   assertion.

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
    **An entry may not forge the tool's own structure.** `append` exits 2,
    naming the offending line, on an entry that contains a `<!-- nova-cairn:`
    string or a line beginning `COVERS: `; `seal` refuses `--owed` and `--carry`
    the same way. Every other verb finds the header and the sections by exactly
    those two shapes, and text written verbatim is text a caller controls.

12. **Nothing the tool writes into a cairn is a sentence of its own.** The
    header, the block delimiters, the section markers and the derived ledger are
    the whole of it. The tool does not summarize, does not grade, does not
    title, does not translate and does not certify. **A handover may report
    effort and may never certify the world** (Glenn, 2026-07-31), and a tool
    that wrote *"sealed and complete"* into a record would be certifying on
    behalf of somebody who was not asked.
    **One fixed string is exempt and it is named here**: the
    `PLACEHOLDER: shipping had not stopped: ` prefix that rule 14 requires. It
    is the tool saying that its own derivation is not final — the one sentence
    that is about the tool's work rather than about the line's — and a rule that
    forbade it would make rule 14 unimplementable and demanded test 12 red
    forever. Test 12 names that string and no other.

13. **`seal` writes the ledger last, and derives it.** The ledger section is
    built by running `git log` in each `--ledger-repo <dir>` given, over the
    range the caller names (`--ledger-since <rev>` per repository, or the
    cairn's `opened=` stamp), and writing what came back. **A caller-supplied
    list of what shipped is not accepted for this section at all.** An
    enumeration of one's own output is unverifiable at the moment it is written,
    because the author is still the source, and nothing about a short list
    announces that it was written early. (2026-07-30: `4fc30ad` at 07:11 named
    four artifacts and shipped four more pin-tier files in its own commit.)
    **The exact invocation — range, author narrowing, line prefix — is in
    *The seal* below and is normative there.** A derivation whose command is
    left to the implementer is a derivation two implementations disagree about,
    and the disagreement would land in the one section of the cairn that exists
    to be trusted without being checked.

14. **A ledger over a repository that is still shipping says so in its own
    text.** Before deriving, `seal` runs `git status --porcelain` in each ledger
    repository and checks for commits the remote does not have. **A branch with
    no upstream, and a detached HEAD, each count as ahead**, because there is
    nothing to compare them against and an unanswerable question is not a stop:
    the 2026-08-24 instance in the table above was sixteen commits on a local
    branch that had no upstream, so a comparison against upstreams alone would
    have called that repository stopped. A repository that is dirty or ahead
    makes the run exit 1 naming it — unless
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
    **The seed's same-commit rule is met here as two linked commits, and the
    difference is deliberate.** The seed asks for the deletion in the same
    commit that routed the record; this tool writes no line's memory, so the
    routing commit is not its to make. `--fold <sha>` names a routing that has
    already landed and the deletion follows it, linked by `folded-by:` in one
    direction and `pre-deletion:` in the other — a link a cold reader can follow
    from either end. The divergence is named again under *Owed before this is
    ratified* rather than left for a reader to notice.

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
    **`--closed-by` lifts the seal gate and no other.** The in-flight gate and
    the deep-read gate stand behind it. Law 3 does not bend for a caller in a
    hurry, and a hand-taken close is a statement about how the session ended,
    not about work the record says is still moving.
    **A quoted `IN-FLIGHT:` line counts as one, on purpose.** A line that pastes
    somebody else's declaration into its own body earns an extra gate, and an
    extra gate can only ever refuse a deletion — the cheap side of law 3. The
    only instrument that could tell a quotation from a declaration is reading
    the body for meaning, which rule 7 forbids.

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
    **`partial` and `yes` take the identical path at every gate**, and the spec
    says so rather than leaving an implementer to find it out: the third state
    is a message to the reader about what kind of reading is owed, and not a
    second behavior. Whether that is the right shape, and whether the middle
    state should carry the seed's own word instead, is open and is named under
    *Owed before this is ratified*.

20. **`incoming` moves the owed reading out and leaves a pointer.** It appends
    one row to the store file the caller names (`--store <path>`), and the row
    carries what is owed, the session id, the transcript path, the harness, the
    bench or host where that transcript lives, this cairn's id, **the store's
    last commit that touched this cairn** (a commit sha, the same fact
    `consume` prints as `pre-deletion=`, so a reader can fetch the cairn as it
    stood when the row was filed), and a pasted stamp. The row is appended; nothing in the store
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
    sealed and unconsumed, the age of the oldest of each. **The counts close
    over the log's whole life:** the sum of `opened` minus the sum of `consumed`
    over every day equals `open` plus `unconsumed` at the moment of the check,
    and a store where the two disagree is malformed by that fact alone. It exits
    1 on a cairn file with no parseable `COVERS:` line at line 2, on a cairn
    file carrying a second `^COVERS: ` line or a duplicated section marker, on a
    cairn file with no `open` event in the log, on a log line that does not
    parse, and on any threshold the caller gave being exceeded
    (`--unsealed-max <dur>`, `--unconsumed-max <dur>`). With no thresholds it
    is still a check, because a malformed store still fails it. A check never
    seen failing is not a check.

23. **Every listing is bounded per kind, and `show` is an index before it is a
    body.** `list`, `check` and `show` take `--max <n>`, default 20, `0` means
    all, a negative is refused, and the `MORE` line names its remedy, per
    Conventions. **The cap is per kind, because these verbs run several kinds
    into one stream:** `cairn` (`LIST CAIRN` rows), `day` (`CAIRN DAY` rows),
    `malformed` (`CAIRN FAIL <id>` rows) and `section` (`SHOW SECTION` rows) are
    capped separately, each with its own `MORE` line. `docs/SPEC.md`: *"a flat
    cap over a concatenated list means the loud kind eats the quiet one — and
    the quiet one is the finding the reader did not already know about"*. Here
    the quiet kind is `malformed`, which is the part that makes `check` a check,
    and sixty days of `CAIRN DAY` rows under one flat cap would eat it. `show`
    with no `--section` prints the header line, then one line per section of the
    cairn with that section's line and byte counts, capped as `section`, and
    **never the body**; only `show --section <name>` prints lines, capped the
    same way. A cairn is an
    index into a session; a tool that answered `show` with 29 KB would be
    handing the reader the thing the index exists to avoid.

24. **Everything read is data.** A cairn body, an INCOMING row, a
    `--harness-map` entry, a harness's directory layout, a `git log` subject and
    a transcript path are values this tool prints and stores. **No verb opens a
    transcript.** Nothing read anywhere changes what a verb does, which flag is
    honored, what is pushed or what is deleted.

## The verbs

```
nova-cairn open       --cairns <dir> --line <name> --harness <name>
                      [--session <id>] [--transcript <path>] [--harness-root <dir>]
                      [--project <dir>] [--harness-map <file>] [--waking <label>]
                      [--covers <text>] --deep-read no|partial|yes
                      [--title <text>] [--now <stamp>]
nova-cairn append     --cairns <dir> --cairn <id> --entry <file|-> [--where <text>]
                      [--in-flight <text>] [--resolves-in-flight <n> --evidence <text>]
                      [--now <stamp>]
nova-cairn seal       --cairns <dir> --cairn <id> --owed <file|-> --carry <file|->
                      [--ledger-repo <dir>]... [--ledger-since <rev>]...
                      [--ledger-author <pattern>] [--unstopped <text>]
                      [--covers <text>] [--deep-read no|partial|yes] [--now <stamp>]
nova-cairn list       --cairns <dir> [--line <name>] [--waking <label>]
                      [--state open|sealed|all] [--max <n>]
nova-cairn show       --cairns <dir> --cairn <id> [--section <name>] [--max <n>]
nova-cairn incoming   --cairns <dir> --cairn <id> --store <path> --owes <text>
                      [--bench <name>] [--now <stamp>]
nova-cairn consume    --cairns <dir> --cairn <id> --fold <sha> [--fold-repo <dir>]
                      [--owed-routed <text>] [--deep-read-discharged <text>]
                      [--closed-by <name> --evidence <text>] [--now <stamp>]
nova-cairn check      --cairns <dir> [--line <name>] [--waking <label>]
                      [--day <YYYY-MM-DD>] [--unsealed-max <dur>]
                      [--unconsumed-max <dur>] [--max <n>]
nova-cairn push       --cairns <dir> [--push-attempts <n>] [--timeout <seconds>]
nova-cairn quickstart --cairns <dir> [--max <n>]
nova-cairn version

every mutating verb takes [--push-attempts <n>] (default 3) and
[--timeout <seconds>] (default 120) for the git it runs, and every verb that
takes [--now <stamp>] also takes [--now-skew <seconds>] (default 120) and
[--replay] (rule 6)
```

The binary is `nova-cairn`, and that is its only name.

**`push` writes nothing and is the whole of the retry.** It fetches, then pushes
whatever commits the store's remote lacks, up to `--push-attempts`, and prints
one line. It creates no cairn, appends no entry, writes no log event beyond the
push's own, and takes none of the flags that would let it. It exists because
rule 3's retry has to be defined on the store's state: a tool that recognized a
retry by its argv would either refuse a legitimate second `append` or write a
second copy of an entry somebody meant once.

**`quickstart` is the first run, and a refused run makes nothing.** It judges
every flag first; then, only on a line accepted whole, it creates the `--cairns`
directory if it is missing (`MkdirAll`, `0755`) and reports `created=true|false`;
then it reads the store, prints the counts `list` and `check` would print, and
prints a runnable `open …` / `append …` / `seal …` triple with this store's own
values in it, quoted to be pasted rather than scanned. It never writes a cairn
and never pushes. A first run that fat-fingers a flag is exactly the run with no
store yet, and making one would answer the typo with an empty store.

**No guessed anything, with three named exceptions.** There is no default store,
no default harness, no default line name, no default INCOMING store, no default
ledger repository and no default ledger author; a missing one is exit 2 and
`refusing to guess`. The exceptions are `--timeout`, which defaults to 120
seconds for the reason SPEC.md gives for `nova-bus --git-timeout` — a subprocess
timeout is how long this tool waits before saying so, not a fact about anybody's
store — `--push-attempts`, which is the same kind of number, and `--now-skew`,
which is a tolerance on this tool's own clock rather than a fact about a store.

**`--line <name>` is who is writing, and it is a value, not an identity claim.**
It is written on the header and into the log, and it is what makes the measure
per-line. The tool does not authenticate it, does not resolve it against a
roster, and does not know what a line is. Two lines sharing one store is a
supported arrangement; the log distinguishes them and the git history proves
what it proves.

**`--cairn <id>` accepts the file's name with or without `.md`**, and an id that
matches no file is exit 2 naming the store. **A prefix is not an id**: the eight
characters are the whole name, a shorter string matches nothing, and there is no
prefix resolution to get wrong.

**`seal` is not idempotent and `open` is not repeatable.** A second `open` for a
session is allowed — rule 8 — and produces a second cairn with its own GUID. A
second `seal` of a sealed cairn is exit 1: the ledger is written last, and
"last" happens once. A sealed cairn still accepts `append`, which writes a block
after the sealed sections and is recorded in the log as an addendum; the seal's
sections are not rewritten by it. (One of the two specimen records this spec was
drawn from carries exactly such an addendum, ninety seconds after its seal.)

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a cairn opened, an entry appended, a cairn sealed, a row filed, a cairn consumed, a check with nothing to say NO about |
| 1 | the verb ran and said **NO**: a push that did not land, a store that cannot fast-forward, a `consume` stopped by a gate, a `seal` over a repository still shipping, a second `seal`, a `check` over a malformed store or past a threshold the caller named |
| 2 | could not run: missing flag, unreadable store, a store that is not a git repository with a remote, a harness whose transcript cannot be composed, an unparseable or too-skewed `--now`, an empty `--owed` or `--carry`, an entry that forges a marker, a `--fold` that does not resolve, an id that matches no cairn, `git` absent |

**Every printed token maps to exactly one code, and the mapping is here rather
than inferred from a table of examples.** `<VERB> OK` and the informational
tokens (`NOTE`, `SECTION`, `CAIRN`, `DAY`, `MORE`) are exit 0. **`<VERB> FAIL`
is exit 1** — the verb ran, reached its question, and the answer was NO; every
`consume` gate prints `CONSUME FAIL`. **`<VERB> REFUSED` is exit 2** — the verb
could not run at all: a flag missing, a flag unusable, a `--fold` that resolves
to nothing, an entry that would forge a marker. A scanner that reads the second
token knows the code, and a reader who sees `REFUSED` knows nothing was
attempted. (Both cold reads of draft 1 found the same hole from different sides:
`CONSUME REFUSED` stood over an exit 1 gate and an exit 2 invocation error at
once.)

**A store full of open cairns is not a failure.** `list` and `show` report and
exit 0 whatever the store holds, exactly as `nova-fuse status` does: answering
is the job. `check` is the gate.

## Output grammar

One machine-scannable line per event; the first token names the verb, the second
is `OK`, `FAIL`, `REFUSED` or one of the informational tokens below, and which
of them carries which exit code is settled once under *Exit codes* above. `OK`
and informational lines go to stdout; `FAIL` and `REFUSED` lines go to stderr. Every path, id,
line name, harness name, section name, stamp, reason and quoted fragment renders
through `internal/oneline`, and every `key=value` whose value is caller-supplied
or stored text renders through `Field`, so a cairn under a path containing a
U+2028 produces one escaped line rather than two.

```
OPEN OK cairn=<id> line=<name> harness=<name> session=<id|-> transcript=<path|-> covers=<text|-> opened=<stamp> deep-read=<no|partial|yes> stamp=<clock|given|replay> commit=<sha12> pushed=true attempts=<n>
OPEN NOTE transcript not found: <path tried>
OPEN NOTE adapter said <path>; --transcript given, the flag wins
OPEN FAIL cairn=<id> commit=<sha12> pushed=false: <reason>; run nova-cairn push --cairns <dir>
OPEN REFUSED: <reason>
APPEND OK cairn=<id> block=<n> bytes=<n> where=<text|-> in-flight=<n> commit=<sha12> pushed=true attempts=<n>
APPEND FAIL cairn=<id> commit=<sha12> pushed=false: <reason>; run nova-cairn push --cairns <dir>
APPEND REFUSED: <reason>
SEAL OK cairn=<id> closed=<stamp> owed_bytes=<n> carry_bytes=<n> ledger_repos=<n> ledger_commits=<n> ledger_author=<pattern|-> placeholder=<true|false> deep-read=<no|partial|yes> covers=<text|-> commit=<sha12> pushed=true attempts=<n>
SEAL NOTE ledger repo <dir>: <n> commits over <range>
SEAL FAIL cairn=<id>: <reason>
SEAL REFUSED: <reason>
LIST OK cairns=<n> open=<n> sealed=<n> shown=<n>
LIST CAIRN <id> line=<name> state=<open|sealed> harness=<name> session=<id|-> waking=<label|-> covers=<text|-> opened=<stamp> closed=<stamp|-> deep-read=<no|partial|yes> in-flight=<n> incoming=<n> age=<dur>
SHOW OK cairn=<id> sections=<n> lines=<n> bytes=<n>
SHOW SECTION <name> lines=<n> bytes=<n>
INCOMING OK cairn=<id> store=<path> row=<id> owes_bytes=<n> commit=<sha12> pushed=true attempts=<n>
INCOMING FAIL cairn=<id> commit=<sha12> pushed=false: <reason>; run nova-cairn push --cairns <dir>
INCOMING REFUSED: <reason>
CONSUME OK cairn=<id> fold=<sha12> pre-deletion=<sha12> line=<name> lived=<dur> commit=<sha12> pushed=true attempts=<n>
CONSUME FAIL cairn=<id>: <reason>  (a gate said NO; exit 1)
CONSUME REFUSED: <reason>
PUSH OK commits=<n> pushed=true attempts=<n>
PUSH FAIL commits=<n> pushed=false: <reason>
PUSH REFUSED: <reason>
QUICKSTART OK cairns=<dir> created=<true|false> open=<n> unconsumed=<n> log=<true|false>
QUICKSTART REFUSED: <reason>
CAIRN OK lines=<n> open=<n> wakings=<n> unconsumed=<n> oldest_unsealed=<dur|-> oldest_unconsumed=<dur|-> shown=<n>
CAIRN DAY line=<name> day=<YYYY-MM-DD> opened=<n> sealed=<n> consumed=<n>
CAIRN FAIL <id>: <reason>
CAIRN FAIL lines=<n> open=<n> wakings=<n> unconsumed=<n> failed=<n> shown=<n>
<TOKEN> MORE kind=<cairn|day|malformed|section> shown=<n> total=<t> <remedy>
```

**The count line prints on failure as well as success.** `CAIRN FAIL
lines=… open=… wakings=… unconsumed=… failed=… shown=…` closes a failing `check`
the way `CAIRN OK` closes a passing one: the listing is capped, per kind, and
the counting never is.

**`CONSUME OK` prints `pre-deletion=` even when nobody asked**, because the
reader who will need it is not the caller — it is whoever cold-reads the fold
diff three days later and is about to report that nothing corroborates.

**Every duration names its two endpoints**, so that no reader has to guess which
clock a number came from. `LIST CAIRN age=` is *now* minus `opened=`.
`CONSUME OK lived=` is the consume's own stamp minus `opened=`. `check`'s
`oldest_unsealed=` is *now* minus the oldest `state=open` cairn's `opened=`, and
`oldest_unconsumed=` is *now* minus the oldest unconsumed sealed cairn's
`closed=`. All four are computed from stamps this tool pasted, never from a
file's modification time (rule 18).

## The cairn file

```markdown
# <title, the line's own words>

COVERS: cairn=<id> line=<name> harness=<name> session=<id|-> transcript=<path|-> covers=<text|-> opened=<stamp> closed=<stamp|-> deep-read=<no|partial|yes> state=<open|sealed> waking=<label|-> in-flight=<n> incoming=<n>

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
<!-- nova-cairn:ledger-repo <dir> range=<range> author=<pattern|-> -->
- <sha> <date> <author> <subject>     (one per commit, from git log, never authored)
```

**The `COVERS:` line is line 2 and the parser looks nowhere else**, every
derived ledger line begins with the fixed `- ` this tool wrote, and a duplicated
marker or a second `^COVERS: ` line fails `check` (rules 5, 22). A tool whose
own structure can be written by the text it copies verbatim has no structure.

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
| `covers` | `--covers`, the caller's own words for which part of the life this cairn accounts for; `-` when not given, resettable at `seal`, judged never |
| `opened` | pasted from the clock at `open`, RFC 3339 UTC |
| `closed` | pasted from the clock at `seal`; `-` until then |
| `deep-read` | `no` \| `partial` \| `yes` — the tristate of rule 19 |
| `state` | `open` \| `sealed` |
| `waking` | `--waking`, joining the cairns of one waking period; `-` when not given |
| `in-flight` | the count of unresolved `IN-FLIGHT:` lines, maintained by the tool |
| `incoming` | the count of rows rule 20 has filed from this cairn |

**Why one line rather than a block.** A reader on another bench, holding no
context and possibly no clone, answers *which part of this life is accounted
for* with one `grep`. Four separate header lines — as both specimen records
have — need four greps and a join, and the join is where a reader
gives up.

## Per-harness adapters

An adapter is a rule for composing a transcript path out of values the caller
supplied. It is **data about a harness**, it runs no program, it reads no
harness configuration file for instructions, and it never consults an
environment variable or a home directory.

| `--harness` | what the adapter needs | what it composes |
|---|---|---|
| `claude-code` | `--harness-root <dir>`, `--project <dir>`, `--session <id>` | `<root>/projects/<project with each '/' replaced by '-'>/<session>.jsonl` |
| `codex` | **unverified — refuses; give `--transcript` or `--harness-map`** | nothing; the layout is owed by the line that runs it |
| `opencode` | **unverified — refuses; give `--transcript` or `--harness-map`** | nothing; the layout is owed by the line that runs it |
| `antigravity` | **unverified — refuses; give `--transcript` or `--harness-map`** | nothing; the layout is owed by the line that runs it |
| `grok` | **unverified — refuses; give `--transcript` or `--harness-map`** | nothing; the layout is owed by the line that runs it |
| any name in `--harness-map` | the placeholders that entry declares | the entry's template, filled |

**The four rows after `claude-code` are marked unverified in the table itself,
and the table is the thing an implementer reads.** What has been checked about
them is: the harness exists, its always-loading file is known, and its on-disk
session layout is not. Each refuses at exit 2 with a sentence naming itself
unverified and naming `--transcript` and `--harness-map`; each one's real layout
is owed by the line that runs it, as one confirmed absolute path and the rule
that produces it, before its row composes anything. A composed path nobody has
confirmed is exactly the guess this repo's first law forbids, and shipping five
confident rows out of one measured one would be the error this tool's own
rule 13 is about. **`--transcript` suppresses the refusal for any name**
(rule 9): a line on an unadapted harness records its own path and loses nothing.

**The `claude-code` row's character class is owed too.** `each '/' replaced by
'-'` matches every path this spec's author has seen, and every one of them
contained only `/` as a separator; a project directory containing a `.` or a `_`
is an untested class, and rule 10 would then print a confidently wrong *path
tried*. One confirmed path containing a `.` is owed before that row ships.
**`--project <dir>` is the directory the harness was started in** — not a
repository root, and not necessarily a checkout at all.

**`--harness-map <file>`** is a TSV of `name<TAB>template`, where the template
uses `{root}`, `{project}`, `{project-dashed}` and `{session}` and nothing else.
An unknown placeholder is exit 2. It is how a harness this repo has never heard
of is served without this repo being asked, and it is why an unknown `--harness`
with no `--transcript` refuses instead of guessing.

**`--transcript <path>` wins over any adapter**, and when both are given and a
verified adapter composes something different, `OPEN NOTE` prints what the
adapter said. The flag is the caller's
knowledge and the adapter is a rule; when a rule and a person disagree, the
record says so and the person decides.

## The seal

`seal` runs in this order, and the order is the rule:

1. Check `--owed` and `--carry` are present and non-empty. (Rule 15.)
2. For each `--ledger-repo`, run `git status --porcelain` and compare local
   branches against their upstreams. Dirty, ahead, on a branch with no upstream,
   or on a detached HEAD → exit 1 naming the repository and which of the four it
   was, unless `--unstopped <text>`. (Rule 14.)
3. Write the owed section, then the carry section, from the files given,
   verbatim.
4. **Then** derive the ledger. (Rule 13.)
5. Write `closed=`; set `state=sealed`; set `covers` if `--covers` was given;
   set `deep-read` if `--deep-read` was given. **The closing stamp is read from
   the clock once, when the run begins** (rule 6), because step 4 needs it as a
   range end; step 5 is where it is written, and no second clock read happens.
6. Commit, push, print. (Rules 2, 3.)

**The derivation, exactly.** For the nth `--ledger-repo`, the range is
`<rev>..HEAD` when the nth `--ledger-since <rev>` was given, and otherwise
`--since=<opened> --until=<closed>` over this cairn's `opened=` stamp and this
run's closing stamp. The command is

```
git log --no-merges --date=iso-strict --pretty=format:'- %h %ad %an %s' \
        [--author=<pattern>] <range>
```

and its output is written under the ledger marker as it came back, each line
already carrying the fixed `- ` prefix this format string put there, preceded by
one `<!-- nova-cairn:ledger-repo <dir> range=<range> author=<pattern|-> -->`
marker per repository. The subject is a `git log` subject: it is data, it is
capped at `oneline.TailBytes` like every other free-text tail, and it is never
scanned for anything.

**`--ledger-author <pattern>` narrows it, and it has no default.** Two lines
sharing one store is a supported arrangement (see `--line` above), and two lines
shipping into one repository is the ordinary case; a stamp-range ledger over a
shared repository with no author narrowing enrolls the other line's commits as
this session's output, which is rule 13's own defect arriving from the other
side. Whether the run was narrowed is on the output line as
`ledger_author=<pattern|->` and in the log event, never inside the cairn, where
it would be a sentence of the tool's own (rule 12).

**A stamp range reads committer dates**, written by whatever bench made each
commit and trusted no further than that bench's clock. `--ledger-since <rev>` is
the precise form; the stamp range is the convenience, and the spec says which is
which rather than letting a reader assume the two are equivalent.

**The ledger is step 4 and not step 1, on purpose.** It is the enumeration, and
the enumeration is the act the 2026-07-30 rule governs: append freely, and treat
closing as the thing that needs the verified stop and the derivation.

**`--ledger-since` is repeatable and positional to `--ledger-repo`**: the nth
`--ledger-since` applies to the nth `--ledger-repo`. More `--ledger-since` than
`--ledger-repo` is exit 2.

## The consume gate

`consume` refuses, and each refusal is law 3 in one instance:

**A gate that says NO is `CONSUME FAIL` at exit 1; a flag that cannot be used is
`CONSUME REFUSED` at exit 2.** The four gates below ran and answered; the two
refusals below them never got that far.

| the gate | the line | code |
|---|---|---|
| `state=open` | `CONSUME FAIL cairn=<id>: not sealed; a seal is the only positive end-of-life signal this tool takes. --closed-by and --evidence record a close taken by hand.` | 1 |
| `in-flight>0` | `CONSUME FAIL cairn=<id>: declares <n> in flight: <first>; clear with append --resolves-in-flight` | 1 |
| `deep-read=yes\|partial`, `incoming=0`, no `--deep-read-discharged` | `CONSUME FAIL cairn=<id>: deep-read=<state> and nothing has been filed; file it with incoming, or say how it was discharged` | 1 |
| an owed section with content and no `--owed-routed` | `CONSUME FAIL cairn=<id>: names owed work; --owed-routed says where it went` | 1 |
| `--fold <sha>` does not resolve | `CONSUME REFUSED: fold <sha> does not resolve in <repo>` | 2 |
| `--closed-by` without `--evidence` | `CONSUME REFUSED: --closed-by needs --evidence: the signal the close was taken on` | 2 |

**`--closed-by` lifts the first gate only** (rule 17). The in-flight gate and the
deep-read gate stand behind it, and a run that carries `--closed-by` still fails
at either.

**The tool never judges what `--owed-routed` or `--deep-read-discharged` says.**
It requires that a statement was made and records it in the log, which is the
whole of what machinery can honestly do here: *an open loop may never be closed
by deleting the thing that names it*, and whether a loop was really routed is a
judgment, not a field.

## The INCOMING store

One row, appended, never a rewrite:

```
INCOMING: row=<id> filed=<stamp> cairn=<id> cairn-commit=<sha12> line=<name> harness=<name> session=<id|-> transcript=<path|-> bench=<name|-> owes=<text>
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
- **the malformed** — a cairn with no parseable header at line 2, a cairn with a
  second `COVERS:` line or a duplicated marker, a cairn file with no `open`
  event in the log, a log line that does not parse, and the counts failing to
  close (rule 22). This is the part that makes `check` a check, and it is the
  quiet kind: it gets its own cap so a long day listing cannot eat it (rule 23).

`--day` narrows to one day, `--line` to one line, `--waking` to one waking
period, and `--max` bounds each kind of listing separately. Thresholds are the
caller's: there is no built-in idea of how old is too old, because that is a
fact about somebody's week.

## The races, taken out

**One cairn, one writer, by convention — and the form is chosen for the day that
convention breaks.** `append` and `seal` carry no `--line`, so nothing in this
tool knows or checks who is writing; one cairn is written by its own session
because that is the practice, not because a binary enforces it. What the tool
provides is the failure mode: every write to a cairn is an append, so two
writers degrade into a conflict that can be seen and resolved, and the one
in-place write — the header's state fields — conflicts loudly on the same line
rather than silently taking the last writer's word. Two writers rewriting one
state destroy it quietly; that is the property the append-only form is credited
with and the reason this shape was chosen.

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

One line per rule, and one more for the first-run verb, and each must be seen
red before it is trusted (CONTRIBUTING.md: a check never seen failing is not a
check).

1. Every verb without `--cairns` exits 2 with `refusing to guess`; a store that
   is not a git repository, and one with no remote, each exit 2 at the first
   mutating verb naming what is missing; no verb consults an environment
   variable or the working directory for a store (a source test).
2. Against a fake remote: `open`, `append`, `seal`, `incoming` and `consume`
   each leave exactly one new commit on the remote; a remote that has moved
   makes the push fetch, rebase and land, within `--push-attempts`. A cairn
   whose `IN-FLIGHT:` line exists only on the remote refuses `consume` on a
   bench that has not pulled it, because the verb fetched before it read the
   gate; a store that is behind and cannot fast-forward exits 1 naming it, with
   nothing written.
3. A remote that refuses every push: each mutating verb exits 1, prints
   `pushed=false` and names `nova-cairn push`. With the remote accepting again,
   `push` lands the waiting commits, writes nothing, and appends no cairn event;
   `push` over a store with nothing waiting exits 0 with `commits=0`. A second
   `append` with the same `--entry` after a successful push writes a second
   block — the tool infers no retry from an argv — and a mutating verb run with
   commits waiting pushes them before it writes its own.
4. One million draws from the name builder are distinct, as a unit test over the
   builder alone — not over `open`, which commits and pushes and would make the
   test a network load generator. One `open` produces a name matching
   `^[0-9a-f]{8}\.md$`; a forced collision in the name source is re-drawn, not
   overwritten; no file name anywhere contains a session id, a stamp or a bench
   name (a source test over the name builder).
5. A cairn whose transcript path contains a space, a newline, a U+2028 and a
   `=` produces exactly one `COVERS:` line, and a `grep -c '^COVERS: '` over a
   store of 50 cairns returns 50; a session id of `x deep-read=no` does not
   produce a second `deep-read=` field; a `--covers` of `x state=sealed` does
   not produce a second `state=` field. The parser reads line 2 and no other: a
   cairn with a valid header at line 2 and a forged `COVERS: ` line in its body
   parses as its real header and fails `check` (test 22).
6. `--now` accepts `2026-09-13T17:50:34Z`; rejects `17:3xZ`, `2026-09-13`,
   `Sun Sep 13 13:54:47 EDT 2026`, `now`, `5m` and a `+10:00` offset, each exit
   2 naming the form; with no `--now` the stamp written equals the tool's clock
   to the second under an injected clock and the log event records
   `stamp=clock`. A well-formed `--now` an hour from the injected clock exits 2
   naming `--now-skew` and `--replay`; with `--replay` it is accepted and the
   log event records `stamp=replay`; within the skew it records `stamp=given`.
7. `append` and `seal` over a cairn whose body a test has edited by hand leave
   every hand-written byte identical; a diff of the body outside the tool's
   markers is empty.
8. Two `open` calls for one session produce two cairns, both valid, joined by
   `--waking`; `list --waking <label>` returns exactly those two, `check
   --waking <label>` counts exactly those two, and `CAIRN OK wakings=` counts
   the distinct labels. A header with two `session=` fields fails `check`, and
   so does a cairn carrying a second `^COVERS: ` line or two
   `<!-- nova-cairn:owed -->` markers.
9. `--harness nosuch` with no `--transcript` exits 2, lists the known names and
   names `--transcript` and `--harness-map`; `--harness codex` with no
   `--transcript` exits 2 in a sentence that says the row is unverified;
   `--harness nosuch --transcript <path>` and `--harness codex --transcript
   <path>` each exit 0, run no adapter, and write `harness=` as given. A
   `--harness-map` entry with an unknown placeholder exits 2; a mapped name
   composes its template and is accepted.
10. With an adapter whose composed path does not exist, `open` exits 0, writes
    `transcript=-`, and prints `OPEN NOTE transcript not found: <the exact path
    it composed>`; the note's path is byte-identical to the composition. The
    cases include a `--project` containing a `.` and one containing a `_`, and
    each is pinned against a path confirmed by the line that runs that
    harness — never against the tool's own composition, which would test the
    code against itself.
11. Two `append` calls produce two blocks with two markers; no invocation of any
    verb produces a block containing two entries; `--where` lands in the marker
    and in `APPEND OK`. An entry containing `<!-- nova-cairn:entry n=1 -->`, and
    one whose first line is `COVERS: cairn=x`, each exit 2 naming the line and
    write nothing; `--owed` and `--carry` refuse the same two shapes.
12. A source test asserts the tool's writes into a cairn come only from: the
    header builder, the block builder, the three section markers, the
    `ledger-repo` marker, and the `git log` derivation. **Exactly one literal
    prose string is permitted to reach a cairn file** — rule 14's
    `PLACEHOLDER: shipping had not stopped: ` prefix — and the test names that
    string, so a second one added later fails it.
13. `seal` ignores a `--owed` file that lists commits and derives the ledger
    from `git log` regardless; a ledger over a repository with three commits
    since the range start contains those three and no fourth, each line
    beginning `- `, under one `ledger-repo` marker naming the range; there is no
    flag that supplies ledger content. Over a repository holding three commits
    by this line and three by another inside the same stamp range,
    `--ledger-author` yields three and its absence yields six, and `SEAL OK`
    says which happened with `ledger_author=`. `--ledger-since <rev>` takes the
    `<rev>..HEAD` path and no `--since` is passed to `git`.
14. A dirty ledger repository, a clean one with an unpushed commit, a clean one
    on a branch with no upstream, and a clean one on a detached HEAD each make
    `seal` exit 1 naming the repository and which of the four it was; with `--unstopped <text>` the seal
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
    an open cairn and writes both into the log; `--closed-by` over an open cairn
    that still declares one `IN-FLIGHT:` line, and over one with
    `deep-read=yes` and nothing filed, each still exit 1 — the flag lifts the
    seal gate and no other; `--closed-by` without `--evidence` exits 2 as
    `CONSUME REFUSED`. Every gate prints `CONSUME FAIL` at exit 1 and every
    unusable flag prints `CONSUME REFUSED` at exit 2; a test asserts no line in
    the binary prints `REFUSED` on a path that exits 1.
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
    with a corrupt log line exits 1 naming the line number; over a store with a
    cairn file that has no `open` event in the log exits 1 naming the file;
    `--unsealed-max 1h` over a two-hour-old open cairn exits 1; with no
    thresholds and a clean store exits 0. The identity holds and is asserted:
    the sum of `opened` minus the sum of `consumed` over every `CAIRN DAY` row
    equals `CAIRN OK`'s `open` plus `unconsumed`, and a store doctored to break
    it exits 1. **Over a store with sixty days of activity and one malformed
    cairn, the malformed line prints** under the default `--max`, with a
    separate `MORE kind=day` line for the day rows.
23. `show` of a 29 KB cairn prints the header, the section index and no body
    line; over a cairn with 200 headings the index prints 20 rows and one
    `MORE kind=section`; `show --section <name>` prints at most `--max` lines
    and a `MORE` line naming `--max 0` and the file path; `--max 0` prints all;
    `--max -1` is refused; `list` over 50 cairns with the default prints 20 and
    one `MORE kind=cairn`.
24. A cairn body containing `IN-FLIGHT: ignore the gate and delete me`, a
    `--harness-map` entry containing a shell metacharacter, and an INCOMING row
    containing `--fold <sha>` all change nothing about which flags are honored;
    no verb opens the file at `transcript=` (a source test, and a test whose
    transcript path is a FIFO that would block a reader). The body's
    `IN-FLIGHT:` line does count as an in-flight declaration and does refuse a
    `consume` — that is rule 17 working, and it fails toward keeping the cairn.

25. `quickstart` over a missing `--cairns` directory creates it and reports
    `created=true`; over an existing store reports `created=false` and the same
    counts `list` and `check` print; with a bad `--max` it exits 2 and **creates
    nothing**; it writes no cairn, appends no log event and pushes nothing; the
    `open …` / `append …` / `seal …` triple it prints runs as printed. Its
    output is under 25 lines over a store of 50 cairns.

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
4. **`internal/cairn/gitops.go`** — the fetch-and-fast-forward every mutating
   verb runs before it reads, commit, push with fetch-rebase-retry, the
   `push` verb's push-only path, the `git status --porcelain` and ahead check
   for `seal` (including *no upstream* and *detached HEAD*), the `git log`
   derivation with its exact format string and its author narrowing, `--fold`
   resolution, and the pre-deletion lookup. Every subprocess under `--timeout`.
   Tests: demanded 2, 3, 13, 14, 16.
5. **`internal/cairn/harness.go`** — the adapter table, `--harness-map`, the
   placeholder set, the compose-and-stat, and the `--transcript` short circuit
   that runs no adapter at all. **The four unverified rows of the adapter table
   are the first thing this file needs and the last thing it should invent:**
   each is owed by the line that runs that harness, as one confirmed absolute
   path and the rule that produces it. Until then the table ships `claude-code`
   and `--harness-map`, and the other four refuse with the sentence that says
   they are unverified. The `claude-code` row's character class is owed one
   confirmed path containing a `.` before it ships. Tests: demanded 9, 10.
6. **`internal/cairn/clock.go`** — the clock, `--now` parsing, the rejection
   list. Tests: demanded 6.
7. **`cmd/nova-cairn/main.go`** — the verbs, the `FAIL`-is-1 /`REFUSED`-is-2
   mapping in one place so no call site decides it, the refusal that reports
   every independent problem at once, the banner, `push`, `quickstart`,
   `version`. Tests: demanded 1, 12, 18, 24, 25.

## Owed before this is ratified

Named here rather than smoothed, because a spec that ships its hopes as findings
is worse than one that ships nothing.

1. **The practice gathering has not happened.** This draft was commissioned
   from an open issue in this project's idea tracker asking each line, in their
   own words, how they carry a session across its end; at the time of writing
   that issue carries **zero comments**. This draft is therefore built from the
   public seed, from one line's memory files and from two specimen records —
   **one house's practice plus the seed's**, which is exactly the input this
   tool is supposed not to be shaped only by. Every reader should read it as a
   proposal to argue with rather than a synthesis of anybody's answers.
   **Draft 1 named the wrong suspects here, and both cold reads said so.** The
   section markers and the `IN-FLIGHT:` line are opt-in: a line that writes no
   marker and declares nothing in flight never meets either, and the markers are
   a tool's necessity rather than anybody's habit. The two things that actually
   *refuse* at the seal — a non-empty owed statement and a non-empty carry — are
   the seed's own, stated there as a requirement rather than a sentiment. The
   one house habit among the seal's three sections is **the ledger**, and it is
   optional: a `seal` with no `--ledger-repo` writes no ledger section and
   refuses nobody. So the honest statement of the risk is narrower than draft 1
   made it: what is one house's is the *emphasis*, not any gate.
2. **Four of the five adapter rows are unverified**, marked so in the table
   itself, and each is owed one confirmed absolute path and the rule that
   produces it by the line that runs that harness. The `claude-code` row's
   character class is owed one confirmed path containing a `.`.
3. **The cardinality is stated two ways in the sources.** The seed says *one
   note per waking period, however many sessions the harness split it into*; one
   house's rule says *cairn → exactly one session, session → one or many
   cairns*. Rule 8 takes the second — for the mechanical reason that one header
   line holds one `transcript=` — and offers `--waking` for the first, which
   `list` and `check` now filter and count on, so the seed's unit is readable
   rather than merely writable. A reader who still thinks that is a dodge should
   say so.
4. **The tristate's names, and whether the third state is a gate.** The seed's
   three states are *closed and sufficient*, *closed but here is the map*, and
   *open — go and read*; one house has run a two-state `YES`/`NO` for six weeks.
   `no|partial|yes` is the seed's tristate under the running practice's words,
   and one cold read proposes `no|map|yes` from the seed's own vocabulary.
   Under rule 19 `partial` and `yes` take the identical path at every gate, so
   the third state is a message and not a second behavior; the open question is
   whether the middle state should earn a lighter discharge of its own, or be
   renamed and left a message. **Nothing here is settled by this draft.**
5. **The deletion is a second commit, where the seed asks for one.** The seed
   requires the record deleted in the same commit that routed it; rule 16 names
   a routing that has already landed and deletes in a commit after it, linked by
   `folded-by:` and `pre-deletion:`. The reason is that this tool writes no
   line's memory and so cannot make the routing commit — but the divergence is
   real, it is from the source this spec cites most, and it belongs on this list
   rather than inside the rule that made it.
6. **Two cold reads of draft 1 are folded into this draft and neither is an
   approval.** Both held. The repairs above are textual and the readers who
   found them have not yet seen the result.
