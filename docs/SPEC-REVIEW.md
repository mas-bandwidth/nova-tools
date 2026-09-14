# nova-review — specification (draft 5, 2026-09-13)

`nova-review` is one binary at the **review layer**. It builds the packet a
reader needs to read one entry at one head, records the reader's verdict with
its provenance and its findings, answers who has read which head, folds the
findings of several readers into one ledger, and reports what the reading
cost. It never forms an opinion about code, and it never merges anything.

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated, and beside
[SPEC-MERGE.md](SPEC-MERGE.md), whose **lane** this tool reads and writes into:
a read record keyed to a head sha, one immutable file on the lane's branch
(SPEC-MERGE rules 19 and 22), is nova-merge's record and this tool does not
make a second one. Where this tool needs a fact nova-merge does not hold, it is
below and it says which fact and why.

On 2026-09-11 and 2026-09-12 a team of six AI lines on five different models
reviewed pull requests by hand, the way a team reviews when the only tools are
a diff viewer, a comment box and a message bus. Every review reached a verdict.
The form works, and it fails in every way a review by hand fails. This tool is
those failures closed, one rule each.

| the failure, from the record | the rule that closes it |
|---|---|
| 25 duplicate findings across readers on one PR family (2026-09-11, swarm batch 1, SPEC-SWARM.md:982: "25 of 67 findings were duplicates of the owed list") | the packet carries every **open finding** with its id and disposition, a reader marks a repeat `dup <id>` and it folds onto the original (rules 3 and 9); `dedupe` shows which view saw each |
| reads recorded against the wrong head — the author had pushed since (2026-09-11: "an approve recorded at 12:31Z counted for a head pushed at 12:47Z") | a `verdict` carries `--head <sha>`, the full sha the reader had open, and binds to it; a packet names the head it was built for on its first line, and refuses to build for a head that is no longer the entry's (rules 1, 2) |
| a HOLD sat unread for 100 minutes and another 45, because it was a PR comment nobody polled (2026-09-12 20:32Z to 22:09Z) | a verdict is a **record on the lane's branch**, the place the coordinator already pulls every pass; the wake on it is nova-wake's (amendment note A), never a comment somebody remembers to read |
| a swarm's cheap reads were wrong five times in 67, "each a paraphrase" of a rule nobody opened, and three of the next batch's seven runs "ended with a plan and no findings" (2026-09-11 and 2026-09-12, SPEC-SWARM.md:985 and :1252-1253) | a verdict is refused unless every finding names `file:line` in the tree **at that head** and cites its requirement **verbatim from the spec at that head**, checked mechanically (rule 4); an APPROVE with no compared line is refused (rule 5) |
| an APPROVE with no quoted rule and no named line read the shape and missed wrong bytes (2026-09-11: "every miss was an approve that read the shape") | rule 5: an APPROVE names at least one `file:line` it compared and the rule it compared it against; Glenn, 2026-09-11: **"Looking hard at things and kicking the tires surfaces bugs. Glossing over stuff and not thinking hard doesn't."** |
| a spec was called ratified with a friend's row empty; silence was read as assent (2026-09-13, the afternoon both coordinators parked the review) | `roster`: silence is `pending`, never yes; a HOLD never expires; only an explicit APPROVE by each named reader **at this head** ratifies (rules 7, 8); Glenn, 2026-09-08: **"Everybody gets a review. We are better together."** |
| a child of the author's own model was counted as a friend's read; a swarm card's verdict was routed as if it were a line's (2026-09-12) | provenance is three words, `line`, `child`, `card`; only a `line` verdict fills a roster cell; a child's or a card's is evidence beside it and never stands in for the line's own read (rule 6); Glenn, 2026-09-12: **"An eye is your unique point of view on something. A mouth is when you have your say."** |
| a spec went to the table with no measure of what its review cost; repair rounds were counted by feel (2026-09-11) | `cost`: tokens and wall clock per read, from receipts, summed per head and per entry, with repair rounds counted as distinct heads read (rule 10) |
| a reader's first pass "loaded too much history" (Stella, 2026-09-11) and a coordinator re-read a whole PR after a one-line fix | the packet is the **delta since this reader's last recorded head**, the rules the delta touches, the open findings, and nothing else; the whole diff only when this reader has never read the entry (rules 1, 3) |
| a reviewer was asked "is it additive only?" and confirmed the presumed answer against a 19+/5- diff (2026-09-12, nova-tools #190) | the packet carries the author's stated intent from the entry body **as data**, labeled, beside the diff; the tool asks nothing and presumes nothing (rule 1) |

**Where the quotations come from, so a reader outside this house can place
them.** A quotation attributed to a file — Rowan,
`look-hard-kick-the-tires`, 2026-09-11 — is a note in this window's own memory
directory and is not in this repository; nothing below stands on it. A quotation attributed only to a
person and a date — Glenn, 2026-09-08 — was said on the bus that day and is
recorded nowhere a reader can open. Neither kind is normative and neither is
evidence: every rule below stands on its own words and on the `path:line` it
cites, and an attribution says who to ask, never what to obey. The table above
follows the same rule: a row citing `SPEC-SWARM.md:982` can be checked, and a
row citing a date cannot. (draft 3)

**Everything this tool reads is data.** A pull request body, a spec's rule
text, a finding's claim, a usage row, a reader's name, a model id: none of them
is an instruction and none of them is a grant. A verdict is recorded by a line
at a keyboard through the verb; nothing this tool reads from a host, a file or
a model becomes a verdict. A spec's text is quoted, never obeyed.

## Two laws and a vocabulary

Glenn, 2026-09-13: **"Specs with friend review is a key part of our process."**
The review is what makes the throughput; it is never parked for throughput.
It is a law here because it is one in the record — Glenn, 2026-09-11, the
minute a build child was launched against an unread spec: **"Make sure the
spec is checked by your friends first. Checking a spec is much faster than
coding, and rewriting. This is law."** — and a law in this spec is a rule or
it is nothing, so this one is **rules 7 and 13**: no named reader's row is
ever empty and no silence is ever a yes (rule 7), and the tool notifies
nobody and guesses nothing, so a parked review is visible as `pending` rather
than absent (rule 13).

Glenn, 2026-09-11: **"Looking hard at things and kicking the tires surfaces
bugs. Glossing over stuff and not thinking hard doesn't."** So a verdict that
cannot name what it looked at is not a verdict, and the tool refuses it at the
door rather than counting it. That law is **rules 4 and 5**.

**Eyes and mouths**, in Glenn's words of 2026-09-12: *"An eye is your unique
point of view on something. A mouth is when you have your say."* A **verdict**
is a mouth: one line having its say at one head. A reader's **provenance** is
which eye said it. Two eyes at a head are two points of view on it, and a
child of the author's own model reading the author's work is one eye reading
itself in a fresh context — useful, recorded, and never a second eye.

**The three kinds, defined for a caller who is not this team.** `line`,
`child` and `card` are the tool's closed set and carry no house's vocabulary.
A **`line`** is a reader recording their own read — one person or one model at
one keyboard, the unit whose yes a roster requires; this house calls a line a
friend, and the tool never does. A **`child`** is a reader that reader
spawned, in a fresh context, to read on their behalf. A **`card`** is a batch
job a reader dispatched. A caller with no spawned readers and no batch jobs
uses `line` and nothing else, and a caller who calls their readers something
else loses nothing: `--who` is any name they like.

## The rules, numbered

Every rule is normative. Each has one line in **tests this spec demands** near
the end. The date on a rule is the day it was learned.

1. **The packet is for one entry at one head, built by machinery, and it is
   the smallest sufficient one.** `packet` writes one file for one reader:
   the head sha on its first line; the range since that reader's last
   recorded head (or the base when they have none); the diff of that range;
   the verbatim text, with `path:line`, of every rule the diff touches (rule
   2 says how a rule is touched); every prior verdict on the entry with the
   head it was for and the reader who gave it; every open finding with its
   id, its disposition and who saw it; the author's stated intent, quoted
   from the entry body and labeled as the author's claim; and nothing else.
   It is bounded in bytes, and past the bound it holds the hunk list and the
   command that prints the rest. It asks the reader nothing.
   **Every section of it but one is a pure function of `(entry, head, range)`,
   so one packet at a head serves every reader whose range it covers** (draft
   5). "This head", "All verdicts at earlier heads", "Open findings", "Rules
   touched", "Diff" and "Not included" are computed from the entry, the head
   and the range and from nothing about who asked; "Your prior verdicts on
   this entry" is the one per-reader section, and `who=` on the first line is
   who asked and never who is bound. The packet carries that tuple's `id=`,
   and `packet --reuse <file>` hands a second reader with the same range the
   **same bytes** and reads no tree at all. What such a reader reuses is the
   delta, the ground this tool already read at that head, the base the packet
   pinned, and the findings list as it stood at `built=`; what no reader ever
   reuses is **another reader's verdict, or the validation of another reader's
   findings** — rule 4's checks are made against the record being written, at
   the moment it is written, and are inherited from nothing. (Stella,
   2026-09-11: "the smallest sufficient review packet is the diff since my
   reviewed sha, the unresolved finding ids with their dispositions, and
   links to the whole; my first pass loaded too much history." 2026-09-12,
   #190: a question that carries its answer gets it back.)
2. **A rule is touched mechanically, never inferred, and its extent is
   scoped to a heading the caller names.** The spec files the caller names
   with `--spec` are read **at the head** — the text the code claims to
   serve, not whatever is on the reader's disk. A rule is quoted when (a) a
   changed line of the diff cites its number in one of exactly these forms —
   `rule <n>`, `rule <n>'s`, `rules <n> and <m>`, `rules <n>, <m>[, <k>]...`,
   `rules <n>, <m> and <k>`, each optionally prefixed by `<spec basename> ` —
   (b) the spec file is itself in the diff and the hunk falls inside the
   rule's text, in which case both sides are quoted, or (c) the caller names
   it with `--rule <spec>:<n>`. A **bare** `rule <n>` names the one file given
   with `--spec` when exactly one was given; with two or more, only the
   `<spec basename> rule <n>` form is a citation and a bare one is no
   citation at all, because the alternative is a guess.
   **The extent.** `--spec <path>` takes an optional `#<heading>` suffix —
   `--spec 'docs/SPEC-MERGE.md#The rules, numbered'` — and the extent of rule
   `<n>` runs from the first line matching `^<n>. ` at column 0 **after that
   heading and before the next `## `** to the line before the next such line
   or the next heading. A `--spec` with no heading is refused, exit 2, when
   the file holds more than one line matching `^1. ` at column 0, naming
   every heading that opens one and the flag that resolves it. A spec's own
   shape is not enough: at origin/main `docs/SPEC-MERGE.md` holds four such
   sequences — `## The rules, numbered` (rule 1 at :82), `## What the
   prototype does that this spec forbids` (:1237), `## Tests this spec
   demands` (:1350) and `## The work list` (:1585) — so an unscoped
   `--rule docs/SPEC-MERGE.md:18` would name three different texts (:238,
   the rule this spec cites twice; :1292, a java leg; :1439, a test), and
   `:3` would name four. The tool never decides from what code does which
   rule it serves; a hunk that cites no rule is a hunk with no rule beside
   it, and the packet says so with `rules=0` on that file's own line in the
   packet's **Rules touched** section — one line per file of the diff, in the
   order git prints it, before any rule text (draft 3) — rather than
   guessing. (2026-09-11: five of 67 swarm findings were wrong, "each a
   paraphrase" — SPEC-SWARM.md:985; a rule quoted from memory is not the
   rule.)
3. **Open findings travel with the packet and a repeat is a `dup`.** A
   finding is open from the record that raised it until that same reader
   closes it explicitly, with a `close <id>` row of their own, and a later
   record that simply does not list it leaves it open (rule 8, drafts 4 and
   5). **An `ok` row is not a finding and is never open** (draft 5): it is
   rule 5's evidence that a line was compared and found right, it is counted
   in `ok=` and printed on its own `VERDICT ROW` line, it appears in no
   packet's open-findings table, it is never `carried=` and it is never
   `dup`ped. **A `nit` row is a finding and is open like any other**: it
   travels in the packet, it carries forward when a later record omits it, and
   it sustains no HOLD and blocks no APPROVE (rules 5 and 8). The packet's
   byte budget is the reason the line is drawn there and not elsewhere. The
   packet lists every
   open finding — id, `file:line`, quoted rule, claim, severity, who saw it,
   the author's disposition if any — so a reader who sees the same thing
   writes `dup <id>` in their findings file, which folds onto the original
   as a second view (rule 9) and is never a new finding. A `dup` is the only
   thing that folds two findings into one, and even then each keeps its own
   id (rule 9). (2026-09-11: 25 of 67 findings were duplicates of the list
   the readers were not shown — SPEC-SWARM.md:982.)
4. **Every finding is grounded at the head, and the tool checks the ground
   it was given.** A finding names its code line as `<path>:<line>` and the
   tool verifies, in the tree at `--head`, that the path exists and the line
   is within the file. A finding whose subject is a line the change
   **deleted** names it `base:<path>:<line>` and is checked in the tree at
   the packet's base instead — **the packet's base is the sha that packet
   recorded as `base=` on its first line when it was built, the left side of
   its range, and `verdict --base <sha>` is how a record names it; a `base:`
   form with no `--base` is refused, exit 2, naming the flag** (draft 5): a
   regression that removes a line has no line at
   the head, and a tool that cannot report it is a tool that hides the one
   kind of defect nobody can see in the after-image. A finding cites its
   requirement as `<spec path>:<line> "<text>"` and the tool verifies that
   the text **occurs on** that line of that file at that head, after trimming
   — from the file's line and from the quote alike — leading and trailing
   whitespace, a leading `> `, a leading list marker, and the emphasis runs
   `**` and `_`. It is a containment test after that trimming and never an
   equality test, so a reader may quote the sentence a bold run opens and the
   tool still finds it. **A requirement the head no longer holds keeps its
   provenance, and is never demoted to somebody's proposal** (draft 4). A
   finding whose requirement the change itself **deleted** cites
   `base:<spec path>:<line>`, in the same shape as the head-side form, and the
   tool verifies the quoted text on that line of that file **in the tree at
   the packet's base**, by the same containment test after the same trimming,
   and prints `rule=base`: a requirement the team agreed to is still the
   requirement a regression is measured against on the day a change removes
   it, and making the reader re-type it as a proposal would throw away the one
   thing that gives the finding its authority. A requirement in a document
   **this repository cannot open** — a spec in another repository, a standard,
   a vendor datasheet — cites `ext:<pin>:<path>:<line>`, where `<pin>` is a
   full 40-hex commit sha or a caller-supplied version token naming the exact
   revision; the tool records it, checks its **shape and nothing else** (a
   well-formed pin, a non-empty path, a positive line, a non-empty fourth
   field), opens nothing, resolves nothing, and prints
   `rule=external pin=<pin>`, so a document this tool never opened is never
   counted as a quotation it verified. Only a requirement nobody has agreed
   anywhere — a defect no numbered rule covers yet — cites `+<one sentence>`:
   a **proposed requirement**, which the tool records, never checks, and
   always prints as `rule=proposed` with the sentence, so that a requirement
   somebody is asking for is never counted as one somebody quoted. The kinds
   are a closed set, `rule_kind` is one of exactly `quoted`, `base`,
   `external` and `proposed`, each prints its own word, and the tool runs
   nothing for any of them: a reproducer named in a claim is text (what it
   deliberately does not do). A row that fails a
   check it was given is a refusal, exit 2, naming the row and the check, and
   nothing is recorded. The tool checks that the ground exists; it never
   checks that the claim is true. (2026-09-12, the cheap reads that returned
   plans; #183: "a polished summary or a second model's approval is not
   evidence that the right source was read.")
5. **An APPROVE names what it compared, and a HOLD carries a live row.** An
   APPROVE carries at least one row of state `ok`: a `file:line` the reader
   compared and the rule they compared it against, both checked by rule 4. An
   APPROVE with zero rows is refused, and an APPROVE
   standing over an open `block` or `fix` of the approver's own is refused too
   (rule 8, draft 5). A HOLD carries at least one row of
   severity `block` or `fix`, **or one `dup <id>` of a finding that is open
   at `block` or `fix`** — which is the commonest HOLD there is: a reader who
   re-reads at a new head, finds their own blocks unfixed and nothing new,
   has only `dup` rows, and their hold must stand. A `dup` counts for this
   rule at the severity of the finding it names and at no other, so a `dup`
   of a `nit` sustains nothing. An ABSTAIN carries no rows and a `--reason`.
   (Rowan, `look-hard-kick-the-tires`, 2026-09-11: every miss was an approve
   that read the shape, and an approve names the lines it compared. That is
   the rule drawn from Glenn's words at the head of this spec; his words that
   day are the two sentences quoted there.)
6. **Provenance is who, which model, and which kind of eye; only a line's
   own verdict fills a roster cell.** `verdict --who <name> --model <id>
   --kind line|child|card` are all required. `line` is a reader recording
   their own read; `child` is a reader a line spawned (`--of <line>`
   required); `card` is a swarm job (`--of <line> --job <id>` required). A
   `child` or `card` verdict is recorded with its own provenance, appears in
   `roster` as `evidence=<n>` beside the line it belongs to, and never sets
   that line's cell to yes, hold or abstain. **Only a `line` verdict of
   APPROVE or HOLD writes the nova-merge read record** (rule 11), so
   nova-merge's read condition never counts a child or a card. **A record
   carrying no `kind` at all is a `line` verdict** — a read record
   `nova-merge read` wrote before this tool existed — and rules 7 and 8 say
   what follows from that. (draft 3)
   (Rowan, `spec-read-before-code`, 2026-09-13, the rule widened from Glenn's
   words that day: a swarm's cheap read is a worker artifact with its own
   provenance, never a friend's review. Glenn, 2026-09-08: **"Each has their
   own contribution to make."**)
7. **Silence is pending, never yes; a HOLD never expires; an abstention is
   the reader's own say or the caller's policy, and the line says which.**
   `roster` takes `--readers <name,...>` (the named readers whose yes is
   required) and optionally `--reserved <name,...>` and `--deadline <utc
   stamp>`, which are **both or neither**: either one given without the other
   is refused, exit 2 (SPEC.md:88-91), since a reserve with no deadline can
   never be waived and a deadline with no reserve closes no row. (draft 3,
   polished) A named reader with no `line` verdict at any head is `pending`; a
   read record with no review record beside it is a `line` verdict and not an
   absence (rule 8), so a reader whose approve nova-merge counts is never
   `pending` here and `merge_read=satisfied` can never print beside that
   reader's `pending` row. (draft 3)
   One whose newest `line` verdict is a HOLD, at this head or an earlier one,
   is `hold`. One whose newest is an APPROVE at an **earlier** head is
   `pending` (their yes was for code nobody has). One whose newest is an
   APPROVE at **this** head is `yes`. **One whose newest `line` verdict is an
   ABSTAIN, at this head or at any earlier one, is `abstain` until they
   record again** — an explicit abstention is a verdict like the other two,
   it does not go stale with a head, and it is the only thing that spells
   `abstain`. A reserved reader with no `line` verdict of any kind is
   `pending` before the deadline and **`waived`** after it, printed with
   `policy=<id> by=reserved deadline=<stamp>`: the literal word `reserved` and the
   `--deadline` value, **never the silent reader's own name**, because the
   policy that closed the row is the caller's and a `by=` naming the reader
   would say the reader closed a row the reader never touched. (draft 3) That
   state is the CALLER's policy and not the reader's say, and a ledger that
   spells the two the same word cannot answer who closed the row.
   **`by=reserved` names the mechanism; a row an audit can settle also names
   the actor and the reason — and a run's stdout is not where that record
   lives** (draft 5). The policy is **a record on the lane branch**, written
   by the actor through a verb, exactly as a verdict is. `policy --who <name>
   --readers <name,...> --reserved <name,...> --deadline <stamp> --reason
   <text>` writes one immutable
   `policy-<who>-<head12>-<at>-<rand6>.json` under `<lane>/reviews/<entry>/`,
   the `answer-` pattern, holding `who`, `readers`, `reserved`, `deadline`,
   `reason`, `head` and `at`, and prints its id. `roster --policy <id>` takes
   that record in place of the reader flags: **`roster` takes either
   `--readers` (optionally with `--reserved` and `--deadline`, which stay both
   or neither) or `--policy <id>`, and the two together are refused, exit 2**.
   A waiver needs the record — **a reserved reader's row is `waived` only
   under `--policy <id>`** — so a `roster` given `--reserved` and `--deadline`
   with no policy record behind them leaves that row `pending` past the
   deadline: a row closed by a policy nobody wrote down is a row closed by
   nobody, and `ratified=true` is not a thing a shell's scrollback should be
   the only evidence for. Every `waived` row prints `policy=<id> by=reserved
   policy_by=<who> policy_reason=<reason>`, read from that record, and `ROSTER
   OK` carries `policy=<id>` and the same two fields when any row is `waived`,
   `-` for each when none is. **A `policy` record whose `who` is one of its
   own `readers` or of its own `reserved` is refused at the verb, exit 2,
   naming it**: a reserved reader may not be the actor who waived their own
   row, and a named reader who waives a peer records that decision under their
   own name rather than behind the policy's. The `reason` is the actor's own
   words, carried verbatim through `oneline` and checked against nothing.
   Rule 13 holds — the actor names the readers, the tool decides nothing,
   keeps no roster store, and `--policy` is an id into the lane the caller
   already named rather than a path it guessed — and rule 11 holds: one fact,
   one writer, and this fact is the actor's. No reader's row is ever closed by
   a mechanism with nobody behind it. (draft 5) `ratified=true` exactly
   when every named reader is `yes`, `abstain` or `waived`, no reader is
   `hold`, and none is `pending`. Nothing here defaults a read.
   **The entry's author does not fill a cell with a yes.** `roster` resolves
   the entry's author from the host as nova-merge does (SPEC-MERGE.md:806-811:
   "an approve whose `who` resolves to the author does not satisfy the
   condition"); an APPROVE whose `who` is the entry's author is recorded,
   counts in `evidence=`, and never sets a cell to `yes`. The author's own
   HOLD does set their cell to `hold`, because a hold anywhere blocks
   (SPEC-MERGE.md:789) and a reader is always free to stop their own change.
   **`roster` answers this layer's question and prints nova-merge's beside
   it.** `ROSTER OK` carries `merge_read=<satisfied|needs-read|hold>`,
   computed by SPEC-MERGE's read condition at SPEC-MERGE.md:786-795 — one
   approve, by a line who is not the author, for this entry's current head —
   with **`hold` for that condition's first line**, "a hold anywhere -> HOLD,
   never merges, whatever the checks say" (SPEC-MERGE.md:789), which is
   neither of the other two and is the answer a coordinator most needs to see
   spelled (draft 3) — because the
   two questions have different answers: an entry every named reader
   abstained on is `ratified=true` here and `NEEDS-READ` there, and a
   coordinator must read both numbers rather than infer one from the other.
   (Rowan, `a-hold-never-expires-into-approval`, 2026-09-11, on Stella's rule
   adopted that hour: a HOLD does not expire into approval at a deadline;
   silence means review pending. Glenn, 2026-09-11: **"votes are three
   things: yes, no, abstain. If not abstain, all must be yes."**, whose
   reserved line silent past the deadline is the `waived` state above.)
8. **Per reader and per kind, the newest record decides, and only that
   reader closes their own HOLD.** Records are ordered by `at` within one
   `(who, kind, of, job)`, and a reader's standing is the newest record whose
   `kind` is `line` and nothing else. **A record with no `kind` field is a
   `line` verdict**, with `model=-`: it is a read record `nova-merge read`
   wrote (SPEC-MERGE rule 19, older than this tool) with no review record
   beside it, and it enters that reader's ordering, fills their cell, closes
   and opens nothing, and moves their range exactly as any other `line`
   record does — so `packet`'s range and nova-merge's `last_read` cannot
   diverge on one, and rule 7's `pending` and `merge_read=satisfied` cannot
   both be true of one reader. An absent `kind` is the only absence this
   spec reads as a value, and that reading is the **read** record's alone: a
   **review** record whose `kind` is absent, or outside `line|child|card`,
   does not decode, and is a `<VERB> FOLD` refusal (rule 12). (draft 3,
   polished) `child` and `card` are written, never inferred.
   (draft 3) **A `child` or `card` record never enters a line's ordering, at
   any name.** A spawned reader that records
   under the name of the line that spawned it is still a `child`: its APPROVE
   cannot supersede that line's HOLD, cannot close a finding that line
   recorded, and cannot move that line's cell (rule 6). **A finding is open
   until an explicit closure record, and an omission closes nothing**
   (draft 4). Printing a
   closure after the fact does not make an accidental omission a resolution,
   so there is no closure by omission here at all — **and a verdict word is
   not a closure either** (draft 5). There is exactly **one** closure and it
   is a row: a **`close <id>`** in that reader's own later findings file,
   which closes exactly the finding it names and no other. An APPROVE closes
   nothing by being an APPROVE. Instead, **an APPROVE is refused, exit 2, when
   the reader recording it has an open finding of their own at `block` or
   `fix` which that findings file neither `close`s nor `dup`s**, one `VERDICT
   REFUSED approve=open id=<id>` line per open id under the cap (rule 12) and
   nothing recorded: an approve standing over the approver's own unanswered
   block is the thing rule 5 refuses at the door, and printing `CLOSED` after
   the fact is the thing an omission may not do. An APPROVE that closes its
   own blocks by row records, and **its open `nit` findings carry forward
   exactly as a HOLD's do**, each with its `VERDICT CARRIED` line: approving
   is not withdrawing a nit nobody mentioned. A later record that does not
   re-list a finding **carries it forward, still open, under its original id**
   (a re-listed finding keeps that id through `dup <id>`): silence in a
   findings file is not a withdrawal, and a reader who drops a row by accident
   must not thereby retract a block nobody has answered. `VERDICT OK` carries
   `closed=<n>` for what the record explicitly closed and `carried=<n>` for
   that reader's own findings still open which this record did not list, with
   one `VERDICT CARRIED id=<id> <path>:<line>` line per carried finding and
   one `VERDICT CLOSED id=<id> <path>:<line>` line per closed one, each kind
   capped by `--max` with its own `VERDICT MORE`, so a reader reads what still
   stands in their name in the same second they record. A `close <id>` of an
   id that is not open on this entry, or of a finding another reader recorded,
   is refused, exit 2, naming the row: only the reader who recorded a finding
   closes it. An author's `answer` records a disposition beside a finding and
   closes nothing. Two records with one `at` to the second fold
   hold-last, as SPEC-MERGE rule 18 folds red-last. (Rowan,
   `a-hold-never-expires-into-approval`, 2026-09-11: a HOLD is closed by a
   fold and the same reader's APPROVE, or by that reader's explicit
   withdrawal.)
9. **Findings group by a mechanical key, grouping is never equivalence, and
   the ledger says which view saw each.** `dedupe` **groups** findings on the
   entry by `(path, line, rule ref)` within one head and **folds** — makes
   one finding of two — only on an explicit `dup <id>`, a reader's row or an
   author's `answer --as dup --of <id>`, which folds for `seen=` and
   `unreported=` exactly as a reader's row does. **Every finding keeps its own
   immutable id for as long as it exists**: two independent defects on one expression
   share a key, print in one group as `members=<id,id>`, and are two
   findings, because saying that two claims are one thing is a judgment and
   this tool makes none. Claim text is never compared. Each group prints who saw
   it (`seen=`, every view as `who:model`), who did **not report** it
   (`unreported=`, every reader of that head whose own packet range covered
   that `path:line` and whose records do not name it) and how many were
   **blind** to it (`blind=<n>`, the readers of that head whose range did not
   cover the line). **`unreported` is a non-report and says nothing more**
   (draft 4): the tool does not know whether such a reader examined the line
   and disagreed, examined it and missed the defect, or never reached it, and
   a ledger that spelled a non-report as a failure to see would be making
   exactly the judgment this rule refuses to make. **`blind` means one thing
   only — outside that reader's declared range** — because rule 1 hands each
   reader a different range and a reader is blind precisely to what they were
   never handed; an in-range finding a reader did not validate is `unreported`
   and is never `blind`. (draft 4) A finding folded across heads by `dup`
   prints one `seen=`/`unreported=` pair per head, newest first, since "that
   head" would otherwise name no head at all. (Rowan, 2026-09-13: record, per finding,
   which view saw it and which did not report it, so the ledger learns which
   pairs of views are complementary and we stop paying for pairs that are not.)
10. **Cost is measured, never estimated, an absence is a dash, and a receipt
    is counted once.** A verdict may carry `--usage <file>`: **nova-swarm's
    usage file in its own shape, verbatim** — `<pool>/usage/<job>.tsv`, one
    header line and one row, tab-separated, the sixteen columns of SPEC-SWARM
    rule 12 in that order (`job`, `attempt`, `from`, `started`, `ended`,
    `end`, `rc`, `provider`, `model`, `repo`, `tokens_in`, `tokens_out`,
    `cache_write`, `cache_read`, `reasoning`, `usd`; SPEC-SWARM.md:167-171),
    with `-` for a field the provider did not report and `0` only for a
    reported zero, exactly as that rule writes it. A header that is not those
    sixteen names in that order is refused, naming the first column that
    differs; this tool reads the file nova-swarm writes and rewrites nothing.
    It reads nine fields — `job` and `attempt`, the swarm's own work id, then
    `model`, `tokens_in`, `tokens_out`, `cache_write`,
    `cache_read`, `reasoning` and `usd` (draft 3) — and reads `started` and
    `ended` for one purpose and no other: it **derives** `seconds` as
    `ended` minus `started`, the measured consumption; a row whose `started`
    or `ended` is `-` gives `seconds=-`. `--started <utc stamp>` on the verb
    is a different number and is kept apart: `wall` is the record's `at`
    minus `started`, the reader's own elapsed clock, and the two are never
    added together. A usage row whose `model` differs from `--model` is a
    refusal naming both, because a receipt for another model's work is not
    this read's cost. **`(job, attempt)` is not an identity, and this tool
    does not spend it as one** (draft 4). Two pools on two benches issue `j-1`
    on the same day, so a receipt is identified by a **stable join**: the
    caller names `--usage-source <id>` (the issuing system and pool the file
    came from, `nova-swarm:<pool>` for the file this rule reads) and `--bench
    <name>` (the machine the work ran on), the row supplies `job` and
    `attempt`, and the tool computes `digest`, the SHA-256 of the usage file's
    bytes, printed as its first twelve hex characters. The receipt identity is
    `<source>/<bench>/<job>/<attempt>` and the digest stands beside it.
    `--usage` without `--usage-source`, or without `--bench`, is refused, exit
    2, naming the missing flag, because an unjoinable number is a number no
    audit can follow back. **Or the caller passes `--receipt <id>` instead of
    `--usage`**: the id of a retained receipt in **the caller's accounting
    store** (nova-tokens in this house, and this spec asserts nothing about
    that program's behaviour — draft 5), recorded verbatim, **checked for
    shape and nothing else** — one to a hundred and twenty-eight characters
    drawn from `A-Za-z0-9`, `.`, `_`, `+`, `-`, `:` and `/`, no whitespace, at
    least one that is not a separator — and **never resolved by this tool**,
    which opens no accounting store and reaches no network for it — the entry
    then carries the link an auditor follows and carries no token numbers of
    its own, every receipt-fed slot a dash. `--usage` and `--receipt` together
    are refused, exit 2. **`cost` counts each distinct receipt identity once
    across everything it reports** — once per entry, and once across entries
    under `--all` — however many verdicts of however many entries carry it,
    and prints `reused=<n>` for the verdicts that shared one: measured
    consumption counted a second time is a number wrong in the direction that
    flatters us, and it is as wrong across two entries as inside one. Two
    records carrying one receipt identity with **different digests** are a
    refusal, `COST RECEIPT`, exit 2, naming both records and both digests: the
    join collided or the file changed under it, and either way the total is
    not this tool's to guess. `usd` is the provider's own figure, carried and
    never computed — this tool holds no rate card, multiplies nothing, and
    **price estimation is the accounting store's work (nova-tokens in this
    house) and stays there**; what this rule
    requires is the accounting link, not the price.
    `cost` prints per read what the receipt said, `-` where it said nothing,
    and sums per head and per entry with `dashes=<n>` so a total with an
    absence in it is never read as complete. **`dashes=` counts the dashes in
    the seven receipt-fed slots and no others** — `seconds`, `in`, `out`,
    `cache_write`, `cache_read`, `reasoning`, `usd` — one per read per slot,
    so a read with no `--usage` at all contributes seven; `wall` and
    `latency=` are this tool's own clocks, never the receipt's, and a dash in
    either is not counted there. (draft 3) Rounds are distinct heads that
    received at least one `line` verdict, and `evidence_rounds=<n>` counts
    the heads that received only `child` or `card` verdicts, so a repair
    round read entirely by children is visible rather than hidden. (Glenn,
    2026-09-11: token spend reporting is an obligation; #183:
    "repeated-reading cost is visible.")
11. **One fact, one writer, one file; the read record is nova-merge's and
    this tool writes it through nova-merge's own code.** A `line` verdict of
    APPROVE or HOLD writes the nova-merge read record — `who`, `verdict`,
    `head`, `note`, `at`, `file` (`internal/merge`'s `Read`,
    `internal/merge/state.go:145-152`) — under `<lane>/reads/<entry>/`, so
    nova-merge's fold, CAS push, outbox and checkout lock apply unchanged
    (SPEC-MERGE rule 22). **It is composed by `internal/merge` and not by this
    binary.** Today the record is built and marshalled in
    `cmd/nova-merge/verbs.go:384-386`, in a `main` package a second binary
    cannot import, so "one writer of that format" is a claim only a golden
    test could hold up; the work list moves that construction into
    `internal/merge` as `merge.ReadItem(entry, who, head, verdict, note
    string, s merge.Submission) (merge.Item, error)` — where `entry` is the
    directory name `merge.EntryDirName(id)` returns, which is exactly what
    `cmd/nova-merge/verbs.go:384` passes today, and never the raw pull request
    number or branch (draft 3) — nova-merge's `read` verb calls it, and nova-review calls the same function with the same
    submission. That is what makes the sentence true in the code rather than
    in a comparison. Rule 7's **policy record** is written the same way, through the same outbox,
    the same CAS loop and the same manifest, as the `policy` part of its own
    one-part submission (draft 5). Every verdict of every kind also writes one **review
    record** under `<lane>/reviews/<entry>/`, with the same submission id,
    holding what nova-merge does not: `model`, `kind`, `of`, `job`, `reason`,
    the findings, the usage, `started`, and `read=<path>` naming its read
    record when one exists. The review record carries the verdict word too,
    so it can be read alone; `roster` refuses a **pair** — a read record and
    the review record whose `read` field names it — that disagrees on any of
    `who`, `head`, `at` or the verdict word, naming both files and the field.
    `dedupe` and `cost` refuse the same pair the same way and on their own
    lines, `DEDUPE PAIR` and `COST PAIR`, the same fields and the same exit 2.
    **`packet` does not check the pair**: it hands one reader their diff, and a
    disagreement between two records of one submission is a question for the
    check verb and never a reason a reader cannot be handed the source.
    (draft 3) Why a second file
    and not a wider read record: nova-merge decodes its records with unknown
    fields refused (`internal/merge/records.go:755`) and blocks the entry on a
    record it cannot decode (SPEC-MERGE.md:1158-1164), so a field added to the
    read record would block every merge until nova-merge was rebuilt; and an
    ABSTAIN, a `child` and a `card` are verdicts nova-merge must never fold.
    The fold in nova-merge walks `reads/` and `gates/` and nothing else
    (`internal/merge/records.go:692` and `:715`), so `reviews/` is invisible
    to it by construction, and the lane's `.gitignore` is a denylist
    (`records.go:779`) so `reviews/` tracks with no change to it.
12. **Bounded output, bounded input, and a listing is a cap and a count.**
    `packet` writes at most `--max-bytes`, default 131072. It is a byte
    **budget** and not a listing ceiling, so **zero or less is refused**,
    never read as "unlimited" — SPEC.md:92, "A budget of zero or less is
    likewise refused" — and a value too small to hold the packet's fixed
    header (the first line, the head, the range, the "Not included" section)
    is refused naming the header's size, because a bound that cannot hold the
    metadata cannot hold a packet. `verdict`, `roster`, `dedupe` and `cost`
    take `--max <n>`, default 20, `0` for all and a negative one refused
    (SPEC.md:193, :198-199), and each prints one `<VERB> MORE` line naming the
    remedy; `packet` takes `--max <n>` on the same terms for three kinds
    inside the packet it writes — the prior-verdicts table, the open findings
    table and the rules section — and, the packet being a file and not stdout,
    the count line the template prints under each is that kind's MORE; only
    `prior` also prints a `PACKET MORE` on stdout. (draft 3, polished) **The
    fold refusal is a capped kind like every other**: at most `--max` `<VERB> FOLD file=` lines and then one `<VERB>
    MORE kind=fold shown=<n> total=<t>`, capped per kind as SPEC.md:206-211
    requires, because a bad upgrade writes fifty undecodable records as
    readily as one and fifty lines is the loud kind eating the quiet one. The
    exit is still 2 and `total=` is still the truth about the lane. (draft 3)
    **`verdict` caps `row`, `recorded`, `closed`,
    `carried` and `open` apart, and bounds its refusals and its input like
    everything else**: a refused findings file prints at most `--max`
    `VERDICT REFUSED row=` lines and one `VERDICT MORE kind=row shown=<n>
    total=<t> refused=<n>`, and the file itself is read at most `--max-rows
    <n>`, default 500, a longer one refused naming the count — it is text a
    model wrote, and a five-thousand-row findings file is the largest
    plausible state, not six rows. **A row count is not a byte count, and the
    bytes are bounded while they are read** (draft 4): `--max-input-bytes
    <n>`, default 1048576, and `--max-line-bytes <n>`, default 65536, bound
    every file this verb reads from a reader or a worker — the findings file
    and the `--usage` file alike. Both are enforced **during the read**,
    through a bounded reader that stops at the bound, so a refusal costs the
    bound and never the file: input past `--max-input-bytes` is refused
    `VERDICT REFUSED input=bytes file=<path> limit=<n>`, exit 2, and a single
    line past `--max-line-bytes` is refused `VERDICT REFUSED input=line
    row=<n> file=<path> limit=<n>` at that line, before the line is assembled.
    Nothing past the bound is allocated, held or recorded. Zero or less is
    refused for either (SPEC.md:92), both being budgets. One enormous line is
    the state `--max-rows` cannot see: five hundred rows of four megabytes
    each is five hundred rows. The bound is measured at that state: fifty
    readers' records on one entry, twelve heads, five thousand rows. (Glenn,
    2026-09-09: bounded output by design; counts not lists.)
13. **Nothing is guessed and nothing is notified.** Every path is a flag;
    there is no default lane, no default output file, no default spec, no
    default reader list. The tool sends no bus note and posts no comment: a
    HOLD's findings reach the author through the record on the lane branch,
    and a reader who wants to say more says it on the bus themselves. A tool
    that could comment could be made to argue (SPEC-MERGE, what it does not
    do).

## The verbs

```
nova-review packet  --lane <dir> (--pr <n>|--branch <name>) --who <name> --out <file> [--head <sha>] [--reuse <file>] [--spec <path>[#<heading>]]... [--rule <spec>:<n>]... [--max-bytes <n>] [--max <n>]
nova-review verdict --lane <dir> (--pr <n>|--branch <name>) --who <name> --model <id> --kind line|child|card [--of <line>] [--job <id>] --head <sha> [--base <sha>] --verdict approve|hold|abstain (--findings <file> | --reason <text>) [--note <text>] [--usage <file> --usage-source <id> --bench <name> | --receipt <id>] [--started <stamp>] [--max <n>] [--max-rows <n>] [--max-input-bytes <n>] [--max-line-bytes <n>]
nova-review answer  --lane <dir> (--pr <n>|--branch <name>) --who <name> --finding <id> --head <sha> --as fixed|declined|dup [--of <id>] [--note <text>]
nova-review policy  --lane <dir> (--pr <n>|--branch <name>) --who <name> --readers <name,...> --reserved <name,...> --deadline <stamp> --reason <text> [--head <sha>]
nova-review roster  --lane <dir> (--pr <n>|--branch <name>) (--readers <name,...> [--reserved <name,...> --deadline <stamp>] | --policy <id>) [--max <n>]
nova-review dedupe  --lane <dir> (--pr <n>|--branch <name>) [--head <sha>] [--max <n>]
nova-review cost    --lane <dir> ((--pr <n>|--branch <name>) | --all) [--max <n>]
nova-review version
nova-review help

every verb BUT version and help takes --lane <dir>; version and help take no
flag and no argument and refuse at exit 2 when given any, as SPEC.md:251-254
requires of every binary. Every verb that runs git or gh also takes
[--timeout <seconds>], default 120, for SPEC-MERGE's reason (SPEC-MERGE.md:458)
```

The binary is `nova-review`, and that is its only name.

**A new binary, not verbs on nova-merge, for one reason with three faces.**
nova-merge is the tool with the mutation guard: one function publishes to the
base, and its whole spec is built so nothing else can. Its own words are "it
does not review code" and "nothing in this tool reads a diff and forms an
opinion", and its `packet` is deliberately **pointers, never the diff**
(SPEC-MERGE rule 23). The review layer is the opposite kind of program: it
opens diffs and spec files at arbitrary heads, writes packet files wherever a
reader asks, reads usage rows and findings files a model wrote, and folds text
by keys. Putting that reading surface inside the binary that pushes to `main`
widens the one program that must stay narrow; putting it beside, sharing
`internal/merge` for the lane's records so there is one writer of that format,
keeps each tool's guarantee checkable by a test over its own sources. The
second face is the record: the read record stays nova-merge's, written through
nova-merge's code, and the review record is this tool's, in a directory
nova-merge's fold never walks — a division a second binary makes visible in
its own sources and a single binary could only promise. **The third face is
the packet**, and the two packets are one design in two layers: nova-merge's
is the **index** (SPEC-MERGE rule 23, `docs/SPEC-MERGE.md:421-422`: "A reader
is handed the smallest sufficient packet, and it is pointers, never the diff"
— one bounded `PACKET ENTRY` block per entry that needs a read, the range and
the holds as pointers, on stdout); `nova-review packet` is what dereferences
that index into one bounded file, and the two are tested to agree on `range=`
(demanded test 1). (draft 3) They can only agree if "this reader's last
recorded head" means what nova-merge's `last_read` means
(SPEC-MERGE.md:426-431), so it does: **the newest head at which this reader
recorded a `line` APPROVE or HOLD**, and nothing else. An ABSTAIN writes a
review record and no read record (rule 11), so an ABSTAIN never moves a
reader's range; neither does a `child` or a `card`.

**`--head` on `packet` is optional, and stale is a refusal.** Given, the tool
reads the entry's current head from the host and refuses, exit 1, `PACKET
STALE … current=<sha12>`, when they differ: a packet for a head nobody has is
work nobody can use, because a verdict for it authorizes nothing (SPEC-MERGE
rule 19). Absent, the head is read from the host and written on the packet's
first line, which is the sha the reader then hands to `verdict`. A sha read
from the host is a read, not a guess.

**`verdict --head` is required and is the full 40-character sha the reader had
open**, exactly as `nova-merge read --head`; the tool never fills it in. `VERDICT
OK` prints `current=true|false` against the head the host reports at record
time, so a reader who is already stale hears it at once and records again at
the new head rather than discovering it in `roster`.

**`verdict --base <sha>` is the packet's base, and it is the tree every
`base:` form is checked in** (draft 5). "The packet's base" is not a tree the
verb can derive: the base is per reader (rule 1), so for a second-round HOLD
it is that reader's own last recorded head and not the entry's base, and
`verdict` is never handed a packet. It is therefore **the sha `packet`
recorded as `base=` on its first line when it built that reader's packet** —
the left side of its `range=` — and the reader hands it back as the full
40-character sha, exactly as they hand back `--head`. A findings file holding
any `base:` form, in its second field or its third, **without `--base` is
refused, exit 2, naming the flag**; a `--base` that does not resolve to a
commit in the lane's clone, or whose `<base>..<head>` is not a range git can
walk, is refused, exit 2. The tool checks that the sha is a tree it can read
and nothing more: it does not require that a packet was ever built for it,
because a packet is a file a reader may not have kept, and a reader who reads
a range by hand is a reader this tool refuses nothing else to. `--base` is
recorded on the review record as `base`, so the tree a `base` row was checked
in is on the record beside the row, and `VERDICT OK` echoes it as
`base_tree=<sha12>`, `-` when the record holds no `base:` form.

**`answer` is the author's verb and it closes nothing.** It records, as one
immutable file under `<lane>/reviews/<entry>/`, that the author says a finding
is `fixed` at `--head <sha>`, `declined` with `--note`, or `dup` of `--of <id>`.
The packet shows the disposition beside the finding; the finding is open until
its reader says otherwise (rule 8). One writer per fact: the finding is the
reader's, the disposition is the author's, the closure is the reader's.

**`roster` is the check; `packet`, `dedupe` and `cost` are reports.** SPEC.md's
table is the whole of it: exit 1 is "the check ran and **failed** (that is the
check working)" (SPEC.md:85), and `roster` is asked one yes-or-no question —
is this entry ratified — so it exits 0 when `ratified=true` and 1 otherwise. A
roster waited on while readers read exits 1 on every poll, and that is the
honest answer to the question the verb was asked; a caller who wants the
listing without the verdict reads the `ROSTER READER` and `ROSTER OK` lines and
ignores the code. The other three are asked "what", not "whether", so they
**report and exit 0 whatever the lane holds**, exactly as SPEC-MERGE.md:520
says of `status`, `dry-run` and `packet` and as SPEC.md:278-279 says of
`nova-fuse status`: `dedupe` and `cost` compute no `ratified` and have no NO to
`policy` is a writer like `verdict` and `answer` and takes their codes: 0
recorded and pushed, 1 recorded and not pushed, 2 could not run (draft 5).
An unreadable record is neither: a `<VERB> FOLD` refusal is **exit 2**,
the Conventions' "unreadable input" (SPEC.md:86), on every one of the four —
the verb could not run, rather than ran and said no.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a packet written, a verdict, answer or policy recorded and pushed, a roster that is ratified, a dedupe or cost report printed **whatever it holds** |
| 1 | the verb ran and said **NO**: `roster` with `ratified=false`, a packet refused as stale, a record written but not pushed (`pushed=false`, re-run the same verb) |
| 2 | could not run: missing flag, unreadable lane, a directory that is not a lane, a findings row that fails rule 4 or rule 5, a `close <id>` of a finding that is not open or is another reader's (rule 8), a findings file past `--max-rows`, input past `--max-input-bytes` or a line past `--max-line-bytes` (rule 12), a usage file whose header is not SPEC-SWARM rule 12's, `--usage` without `--usage-source` or `--bench`, `--usage` with `--receipt`, one receipt identity with two digests (`COST RECEIPT`), a `--spec` with no heading in a file of several rule sequences, `--readers` together with `--policy`, `--reserved` without `--deadline` or the reverse, a `policy` whose `--who` is one of its own readers or reserved, a `--policy <id>` no record on the lane carries (rule 7), an APPROVE standing over that reader's own open `block` or `fix` that the file neither closes nor dups (rule 8), a `base:` row with no `--base` or a `--base` that names no commit (rule 4), a `--reuse` whose id is not this reader's or given beside `--spec`, `--rule` or `--max-bytes` (`PACKET REUSE`), a record that does not decode (`<VERB> FOLD`), a read-and-review pair that disagrees (`ROSTER PAIR`, `DEDUPE PAIR`, `COST PAIR`), a `--who` of `answer` or `policy`, or beginning `answer-` or `policy-`, `--max-bytes`, `--max-input-bytes` or `--max-line-bytes` of zero or less, a negative `--max`, bad invocation, `git` or `gh` absent |

A findings row that fails its check is exit 2 and not 1 because nothing was
recorded and nothing was judged: the invocation was unusable, and the remedy
is to fix the row. A refusal prints at most `--max` failing rows, one line
each — a reader can fix six rows as easily as one — and then one `VERDICT MORE
kind=row shown=<n> total=<t> refused=<n>`, because a findings file a model
wrote can hold five thousand rows and the count is the number the reader
needs (rule 12).

One machine-scannable line per event; first token names the verb, second is
`OK`, `FAIL`, `REFUSED`, `STALE` or an informational token listed here. `OK`
and informational lines go to stdout; `FAIL`, `REFUSED`, `STALE` go to stderr.
Every path, name, model id, claim and reason renders through
`internal/oneline`; every `key=value` field carrying stored text is a single
token by `oneline.Field`. `PACKET OK` and `PACKET MORE` share their first two
tokens with nova-merge's `packet` (`docs/SPEC-MERGE.md:588` and `:587`): both
binaries print the same two words with different fields, and the fields tell
them apart — `entry=` here against `entries=` there, and disjoint `kind=` sets
— so no line is ambiguous about which tool wrote it. (draft 3, polished)

```
PACKET OK entry=<n-or-name> id=<hex12> head=<sha12> base=<sha12> range=<r> files=<n> hunks=<n> rules=<n> prior=<n> open=<n> bytes=<n> cut=<n> reused=<true|false> out=<path>
PACKET MORE kind=<prior|fold> shown=<n> total=<t> nova-review packet --lane <dir> … --max 0
PACKET STALE entry=<n-or-name> asked=<sha12> current=<sha12>: the head moved; build the packet for the current head
PACKET REUSE asked=<hex12> found=<hex12> file=<path>: that packet was built for another (entry, head, range); build this reader's own
PACKET FOLD file=<path>: <reason>
PACKET REFUSED: <reason>
VERDICT OK entry=<n-or-name> who=<name> model=<id> kind=<line|child|card> verdict=<approve|hold|abstain> head=<sha12> base_tree=<sha12|-> current=<true|false> rows=<n> block=<n> fix=<n> nit=<n> ok=<n> dup=<n> base=<n> external=<n> proposed=<n> closed=<n> carried=<n> seconds=<n|-> wall=<n|-> receipt=<source>/<bench>/<job>/<attempt>|<receipt id>|- digest=<hex12|-> review=<path> read=<path|-> pushed=true
VERDICT ROW row=<n> id=<id> sev=<block|fix|nit|ok|dup|close> at=<path>:<line>|<finding id> side=<head|base|-> rule=<quoted|base|external|proposed|-> pin=<pin|-> ref=<the third field as written>
VERDICT CLOSED id=<id> <path>:<line> sev=<block|fix|nit|ok>: closed by this record (rule 8)
VERDICT CARRIED id=<id> <path>:<line> sev=<block|fix|nit|ok>: still open, not listed by this record (rule 8)
VERDICT FAIL entry=<n-or-name> who=<name> head=<sha12> review=<path> pushed=false: <reason>; re-run the same verb to push it
VERDICT REFUSED row=<n>: <path>:<line> <check>: <reason>
VERDICT REFUSED approve=open id=<id> <path>:<line> sev=<block|fix>: an approve cannot stand over this reader's own open finding; close it or dup it (rule 8)
VERDICT REFUSED input=bytes file=<path> limit=<n>: the input passed its byte budget while being read (rule 12)
VERDICT REFUSED input=line row=<n> file=<path> limit=<n>: one line passed its byte budget while being read (rule 12)
VERDICT MORE kind=row shown=<n> total=<t> refused=<n> nova-review verdict --lane <dir> … --max 0
VERDICT MORE kind=<recorded|closed|carried|open|fold> shown=<n> total=<t> nova-review verdict --lane <dir> … --max 0
VERDICT FOLD file=<path>: <reason>
VERDICT STAGED ORPHAN id=<submission id> parts=<n> removed=true: staged with no manifest; published nowhere (amendment B)
VERDICT REFUSED: <reason>
ANSWER OK entry=<n-or-name> finding=<id> who=<name> as=<fixed|declined|dup> head=<sha12> of=<id|-> file=<path> pushed=true
ANSWER FAIL entry=<n-or-name> finding=<id> file=<path> pushed=false: <reason>; re-run the same verb to push it
ANSWER FOLD file=<path>: <reason>
ANSWER STAGED ORPHAN id=<submission id> parts=<n> removed=true: staged with no manifest; published nowhere (amendment B)
ANSWER REFUSED: <reason>
POLICY OK entry=<n-or-name> id=<id> who=<name> readers=<n> reserved=<n> deadline=<stamp> head=<sha12> file=<path> pushed=true
POLICY FAIL entry=<n-or-name> id=<id> file=<path> pushed=false: <reason>; re-run the same verb to push it
POLICY STAGED ORPHAN id=<submission id> parts=<n> removed=true: staged with no manifest; published nowhere (amendment B)
POLICY FOLD file=<path>: <reason>
POLICY REFUSED: <reason>
ROSTER READER who=<name> state=<yes|hold|abstain|waived|pending> head=<sha12|-> at=<stamp|-> model=<id|-> open=<n> evidence=<n> reserved=<true|false> policy=<id|-> by=<reserved|-> policy_by=<name|-> policy_reason=<text|-> deadline=<stamp|-> author=<true|false>
ROSTER MORE kind=<reader|fold> shown=<n> total=<t> nova-review roster --lane <dir> … --max 0
ROSTER OK entry=<n-or-name> head=<sha12> yes=<n> hold=<n> abstain=<n> waived=<n> pending=<n> evidence=<n> ratified=<true|false> merge_read=<satisfied|needs-read|hold> policy=<id|-> deadline=<stamp|-> policy_by=<name|-> policy_reason=<text|->
ROSTER FOLD file=<path> ratified=false: <reason>
ROSTER PAIR read=<path> review=<path> field=<who|head|at|verdict>: the two records of one submission disagree
ROSTER REFUSED: <reason>
DEDUPE FINDING id=<id> key=<path>:<line>@<rule_kind>:<rule ref as written> sev=<block|fix|nit|ok> head=<sha12> members=<id,...> seen=<who:model,...> unreported=<who:model,...> blind=<n> dups=<n> open=<true|false> answer=<fixed|declined|dup|->
DEDUPE MORE kind=<finding|fold> shown=<n> total=<t> nova-review dedupe --lane <dir> … --max 0
DEDUPE FOLD file=<path>: <reason>
DEDUPE PAIR read=<path> review=<path> field=<who|head|at|verdict>: the two records of one submission disagree
DEDUPE OK entry=<n-or-name> head=<sha12> findings=<n> groups=<n> folded=<n> open=<n> base=<n> external=<n> proposed=<n> readers=<n>
COST READ entry=<n-or-name> who=<name> kind=<line|child|card> model=<id> head=<sha12> verdict=<word> receipt=<source>/<bench>/<job>/<attempt>|<receipt id>|- digest=<hex12|-> reused=<true|false> seconds=<n|-> wall=<n|-> in=<n|-> out=<n|-> cache_write=<n|-> cache_read=<n|-> reasoning=<n|-> usd=<v|->
COST HEAD entry=<n-or-name> head=<sha12> reads=<n> seconds=<n|-> wall=<n|-> latency=<n|-> in=<n|-> out=<n|-> cache_write=<n|-> cache_read=<n|-> reasoning=<n|-> usd=<v|-> dashes=<n>
COST MORE kind=<read|head|fold> shown=<n> total=<t> nova-review cost --lane <dir> … --max 0
COST FOLD file=<path>: <reason>
COST PAIR read=<path> review=<path> field=<who|head|at|verdict>: the two records of one submission disagree
COST RECEIPT id=<source>/<bench>/<job>/<attempt> a=<path> digest=<hex12> b=<path> digest=<hex12>: one receipt identity, two digests
COST OK entries=<n> reads=<n> rounds=<n> evidence_rounds=<n> receipts=<n> reused=<n> seconds=<n|-> wall=<n|-> in=<n|-> out=<n|-> cache_write=<n|-> cache_read=<n|-> reasoning=<n|-> usd=<v|-> dashes=<n>
```

`PACKET OK cut=<n>` is the number of files whose diff was replaced by a hunk
list because the byte bound was reached; `0` means the packet is whole. The
remedy is inside the packet, per file, as the exact `git diff <range> --
<path>` that prints what was cut. `COST HEAD latency=` is the seconds from the
head commit's committer time to the last `line` verdict recorded at that head,
and `-` when that head has none; it is that and nothing else, because `cost`
is given no `--readers` and cannot know whether a named reader is still
pending — that fact is `roster`'s and is not restated here. It is the number
Stella asked to measure by, "decision latency", and it is only as good as the
committer clock.

**Every listing is a cap and a count.** `ROSTER READER` prints at most `--max`
readers in the order of `--readers`; `DEDUPE FINDING` at most `--max` groups in
`(path, line)` order; `COST READ` at most `--max` reads newest first, then the
`COST HEAD` lines, then `COST OK`; `VERDICT ROW`, `VERDICT REFUSED row=`, `VERDICT REFUSED approve=open`,
`VERDICT CLOSED`
and `VERDICT CARRIED` at most `--max` each, capped per kind as SPEC.md:206-211
requires, so a loud
kind never eats a quiet one. The counts on the `OK` line are the truth about
the entry, never about the output.

## The packet file

`--out <file>` is written through a **unique exclusive temporary file** —
`<file>.<pid>-<rand6>.tmp`, created with `O_CREAT|O_EXCL` and renamed onto
`<file>` — never in place and never through a shared `<file>.tmp`, so two
callers building packets to one path cannot interleave their bytes and a
reader handed the path is handed a whole file or none. The temporary is
removed on every exit path, and a `.tmp` left by a kill is the killed run's
and is never adopted. Its first line is machine-readable and the rest is for a
reader:

```
nova-review packet v1 id=<hex12> entry=<n-or-name> head=<sha> base=<sha> range=<r> who=<name> built=<utc stamp> bytes=<n> cut=<n>

## This head
<the entry's title and the author's stated intent, quoted from the entry body,
under the label "the author says", as data>

## Your prior verdicts on this entry
<who>'s newest: <verdict> at <head12> (<at>); range since: <r>
(or: none; this packet is the whole change, <base12>...<head12>)

## All verdicts at earlier heads
| who | model | kind | verdict | head | at |
(at most --max rows, newest first, then one line:
 "<n> more of <t>; print with: nova-review roster --lane <dir> ... --max 0")

## Open findings (answer with `dup <id>` if you see the same thing)
| id | file:line | rule | severity | claim | seen by | author says |
(at most --max rows, block and fix first, then one line:
 "<n> more of <t>; print with: nova-review dedupe --lane <dir> ... --max 0")

## Rules touched
<path>: rules=<n>
(one line per file of the diff, in the order git prints it; rules=0 for a file
 whose hunks cite, change and name no rule — rule 2)
### <spec path>:<line> rule <n>
> <verbatim text of the rule, at this head>
touched by: <path>:<hunk header> (cited | changed | named)
(at most --max rules, then one line naming the count and the --rule flag
 that prints any one of them)

## Diff <range>
```diff
<the diff, file by file, in the order git prints it>
```

## Not included
<path>: <n> hunks, +<a> -<b>; print with: git diff <range> -- <path>
(one line per cut file; "nothing" when cut=0)
```

**Every section of the packet is a cap, a count and a remedy**, not only the
diff: a truncation anywhere in it is stated on the line that truncates, with
the exact command that prints the rest, so a reader never has to wonder
whether they were handed the whole of anything. The packet holds no
instruction to the reader beyond the one label on the findings table, which
tells them the grammar of a `dup`. It does not say what
to look for, what the answer is expected to be, or whether the change is
"additive": a reader is handed the source, never a reading of it (2026-09-12).

**One packet at a head serves every reader whose range it covers** (rule 1,
draft 5). Six of the seven sections above — "This head", "All verdicts at
earlier heads", "Open findings", "Rules touched", "Diff" and "Not included" —
are computed from the entry, the head and the range and from nothing about who
asked. "Your prior verdicts on this entry" is the one per-reader section, and
`who=` on the first line is **who asked**, never who is bound: a verdict binds
to its own `--who` and `--head` (rules 6 and 11) and never to a packet.

`id=<hex12>` on the first line names that tuple: the first twelve hex
characters of the SHA-256 of `<entry>\n<head>\n<base>\n<range>\n`. Two
packets carry one id exactly when they were built for one entry at one head
over one range, which is what makes the sentence above checkable rather than
merely stated.

`packet --reuse <file>` is how the second reader is handed it. The tool reads
that file's first line, computes the id this reader's own `(entry, head,
range)` gives, and when the two are equal copies **every reader-independent
section byte for byte**, writes this reader's own "Your prior verdicts on this
entry" section and `who=`, and **reads no tree at all**: no `git diff`, no
`git show`, no spec file opened at any head, nothing but the one host call
that resolves the entry's current head. It prints `PACKET OK … reused=true`.
When the two ids differ it is refused, exit 2, `PACKET REUSE asked=<id>
found=<id> file=<path>`, naming both, because a reader whose range differs
must have their own packet and never a mislabeled one; `--reuse` with
`--spec`, `--rule` or `--max-bytes` is refused the same way, since those flags
change what a packet would hold and a reused packet holds what it already
holds.

**What is reused and what never is.** A second reader reuses the delta, the
ground the first build already read at this head — the diff, the rule text
with its `path:line`, the base the packet pinned — and the open findings as
they stood when it was built. A second reader reuses **no verdict of
another's**, and no validation of another reader's findings: rule 4's checks
are made against the record being written, at the moment it is written, from
the trees at `--head` and `--base`, and are never inherited from a packet or
from another record. The packet is source; the verdict is the reader's own.
Where a packet is older than the newest record on the lane, what it carries is
the findings list **as of `built=`**, which is why `verdict` re-reads the fold
and why a stale open-findings table can only cost a reader a `dup` they did not
need to write — never a closure, a cell or a ratification.

## The findings file

`verdict --findings <file>` is a UTF-8 text file the reader writes, one row
per line, tab-separated, exactly five fields, at most `--max-rows <n>` rows
(default 500; a longer file is refused naming the count, rule 12), comment
lines beginning `#` ignored:

```
<state>	[base:]<path>:<line>	<spec path>:<line>	<rule text, verbatim>	<claim>
<state>	[base:]<path>:<line>	base:<spec path>:<line>	<rule text, verbatim>	<claim>
<state>	[base:]<path>:<line>	ext:<pin>:<path>:<line>	<rule text, verbatim>	<claim>
<state>	[base:]<path>:<line>	+	<the requirement, one sentence>	<claim>
dup	<finding id>	-	-	<one sentence, optional>
close	<finding id>	-	-	<one sentence, optional>
```

`<state>` is one of exactly `block`, `fix`, `nit`, `ok`, `dup`, `close`.
`block` and
`fix` are HOLD-grade; `nit` and `ok` may appear on an APPROVE; `ok` is a
line the reader compared and found right, which is the only kind of row an
APPROVE needs (rule 5); `dup` folds onto an open finding by id and takes no
path or rule; **`close` closes one open finding of this reader's own by id**
(rule 8), takes no path or rule, and is refused when the id is not open on
this entry or was recorded by another reader (draft 4). A sixth field is a
refusal naming the row; so is a fourth or a second.

**The second field** is `<path>:<line>`, checked in the tree at `--head`, or
`base:<path>:<line>`, checked in the tree at **the packet's base**, which is
the tree `--base <sha>` names (rule 4) — the form for a line the change
deleted, which rule 4 exists to keep reportable.

**The third field** names the requirement, and it is one of exactly four
forms — `rule_kind` is `quoted`, `base`, `external` or `proposed` (rule 4,
draft 4):

- `<spec path>:<line>` — `quoted`. The fourth field must be **found on** that
  line of that file at `--head`, compared after trimming, from the file's line
  and from the quote alike, leading and trailing whitespace, a leading `> `, a
  leading list marker, and the emphasis runs `**` and `_`, so that a reader may
  quote the sentence a bold run opens, and a quote that spans lines names the
  first line and quotes that line.
- `base:<spec path>:<line>` — `base`. The same check in the tree at the
  **packet's base**, the tree `--base <sha>` names: the form for a requirement
  this change deleted, which
  keeps the requirement's own provenance rather than demoting an agreed rule to
  somebody's proposal. Printed `rule=base`.
- `ext:<pin>:<path>:<line>` — `external`. A pinned requirement in a document
  this repository cannot open, `<pin>` a full 40-hex sha or a caller-supplied
  version token. Shape-checked only — a well-formed pin, a non-empty path, a
  positive line, a non-empty fourth field — never opened, never resolved.
  Printed `rule=external pin=<pin>`.
- `+` — `proposed`. The fourth field is one sentence the reader says the tree
  ought to require and does not. Recorded, checked for nothing, printed
  `rule=proposed`.

**A version token has a grammar, so "malformed" has a meaning** (draft 5). A
caller-supplied `<pin>` that is not a 40-hex sha is one to sixty-four
characters drawn from `A-Za-z0-9`, `.`, `_`, `+` and `-`, and from nothing
else: it holds **no `:`**, no whitespace and no `/`. The `<path>` of an `ext:`
row holds no `:` either. That is what makes `ext:<pin>:<path>:<line>` a
four-field split with no ambiguity, and a pin or a path outside those
characters is a malformed row, exit 2, naming it.

**Where a row's `rule=` word is printed** (draft 5, the word had no line).
Every row a record accepts prints one `VERDICT ROW` line — `row=`, the
finding's id, its severity, its `path:line` and `side=`, then `rule=` and
`pin=` and the third field as the reader wrote it — at most `--max` of them
and then one `VERDICT MORE kind=recorded shown=<n> total=<t>`, capped as rule
12 caps every kind. That is the line `rule=quoted`, `rule=base`,
`rule=external pin=<pin>` and `rule=proposed` are printed on. A `dup` or
`close` row prints `rule=- pin=-` and names the finding id it acts on in
`at=`.

`base=<n>`, `external=<n>` and `proposed=<n>` are counted separately on
`VERDICT OK` and `DEDUPE OK`, so nobody can read a pin this tool never opened,
or a request for a rule, as a quotation it verified. The tool opens no
document a pin names and runs nothing for any of the four forms.

A finding's id is `<submission id>.<row>`, where the submission id is the
record's `<at>-<rand6>` drawn once per verb (SPEC-MERGE rule 22), so an id
names its file. An id is immutable and a finding keeps it for as long as it
exists (rule 9).

**The tool checks the ground and never the claim.** Rule 4 is a check that
`path:line` and `spec:line "text"` exist at the head. The claim is text a
reader wrote, printed through `oneline`, and no code path in this tool
compares one claim to another or to the diff (rule 9's key omits it, and a
source test finds no such comparison).

## The lane, as this tool sees it

`--lane <dir>` is a lane `nova-merge init` made. This tool adds one tracked
directory to it and nothing else:

```
<lane>/reviews/<entry>/<who>-<head12>-<at>-<rand6>.json     one verdict, immutable, tracked and pushed
<lane>/reviews/<entry>/answer-<who>-<head12>-<at>-<rand6>.json   one answer, immutable, tracked and pushed
<lane>/reviews/<entry>/policy-<who>-<head12>-<at>-<rand6>.json   one required-reader policy, immutable, tracked and pushed (rule 7)
<lane>/reads/<entry>/<who>-<head12>-<at>-<rand6>.json       written for a `line` approve or hold, through internal/merge (rule 11)
```

A review record is written exactly as a read record is: first to the outbox,
then committed and pushed to the lane branch under the checkout lock in the
CAS loop of SPEC-MERGE rule 22, delivered only after the confirming fetch,
repaired by re-running the same verb.

**One submission, two items, two stems.** A `line` APPROVE or HOLD writes a
read record and a review record, and they are one submission — one
`<at>-<rand6>`, one outbox flush, one commit — so the lane branch never holds
one without the other for longer than a rejected push. Today it cannot:
`internal/merge/records.go:168` names the outbox item `sub.ID() +
filepath.Ext(it.Path)`, so two `.json` items of one submission are written to
`outbox/<id>.json` twice and the second overwrites the first before `flush()`
reads it back — one record would land, silently. **Amendment B is therefore a
prerequisite of rule 11 and not a convenience**, and it is written as a closed
set rather than a glob: the outbox item is `<submission id>-<part>.json`,
where `<part>` is **one of exactly `read`, `gate`, `review`, `answer` and
`policy`** — five part words, the only part set this spec names anywhere, with
`gate` in it because nova-merge's own gate submission flushes through the same
loop and a set that omitted it would refuse that flush (draft 3), and `policy`
in it because rule 7's policy record flushes through the same loop too (draft
5); and every item of one submission is restored, committed and confirmed
together. **`<submission id>-parts.json` is the sixth legal item name and it
is not a part** (draft 5): it is the submission's manifest, it names the parts
and no part names it, no part word is `parts`, and `parts` is reserved so that
no part can ever take that name. Said plainly: five parts and one manifest,
six legal item names, a closed set, and amendment B's refusal of "a file in
the outbox that is not a known part" reads against those six. Nothing is picked
up by sharing a stem, so the outbox gains no surface for a file nobody wrote on
purpose. Until it lands there is no fallback: a two-item flush is the thing
that does not work.

**A submission publishes whole or not at all, and a completeness record says
what whole means** (draft 4). A closed vocabulary of part names says which
names are legal; it says nothing about which parts **exist**, and a crash
between two staged parts is exactly the case where the difference decides what
lands. So the parts of a submission are declared before any of them is
flushed, in a **manifest part** `<submission id>-parts.json` holding, for every
part of that submission, its `part` word, the relative destination its own
`file` field names, and the SHA-256 digest of its bytes. The manifest is
written **last**, by create-and-rename, after every other part is on disk, and
the flush restores a submission **only** when the manifest is present and every
part it names is present with a matching digest. Anything else — a manifest
with a part missing, a part whose bytes do not match, a part the manifest does
not name, no manifest at all — is left in the outbox, **published nowhere**,
named in the refusal, and recovered by re-running the same verb.

**A staged submission with no manifest at all is an orphan, and its fate is
stated** (draft 5). A kill between the first part and the manifest's rename
leaves parts whose names are all legal parts of one submission id with no
`<id>-parts.json` beside them; nothing of that submission was ever published
and nothing of it ever can be, because the flush restores only a manifested
whole. The next flush on that lane therefore prints one informational line per
orphan — `<VERB> STAGED ORPHAN id=<submission id> parts=<n> removed=true` on
stdout — **removes those parts from the outbox, publishes no part of them, and
carries on**, exiting 0 when the rest of the flush succeeds. The remedy is the
one every unpushed record here has: re-run the same verb. It is removed rather
than left because the tool knows exactly what it is and that it wrote it,
which is what separates it from the other thing the outbox can hold: **a file
that is not a legal item name — a bare `<id>.json` an older binary wrote — is
never removed, is named in the refusal, and blocks every later flush on that
lane until a person removes it** (work list 0a). The tool sweeps up after
itself and never after anybody else.

**The gate's `.summary` sidecar is inside the manifest** (draft 5). It is the
`gate` part's own file under the name `<submission id>-gate.summary`, and the
manifest names it with its destination and its own SHA-256 digest like every
other file of the submission, so a gate whose summary is missing or altered
publishes no part of itself either.

That is what makes a crash between the read part and the review part harmless.
With no manifest the flush publishes neither, so **the lane never holds a read
record this tool staged without the review record of its submission**, and rule
8's reading of a lone `kind`-less read record as a legacy `line` verdict stays
true of nova-merge's own records and can never be manufactured by a
half-landed submission of this one. A partial staging cannot qualify a reader
as having read at any head, on any restart.

**Destinations stay confined to each part's own directory.** Each part word
maps to exactly one directory — `read` to `reads/<entry>/`, `gate` to
`gates/<entry>/`, `review` and `answer` to `reviews/<entry>/` — and a part
whose `file` is absolute, holds `..`, or falls outside the directory its own
part word maps to is a refusal that publishes **no part of that submission**,
over and above the check `destinationOf` (`records.go:267-279`) already makes.
The manifest's own destinations are checked the same way before the first part
is restored, so the set of paths a submission can write is known and confined
before anything is written.

**No report verb takes the checkout lock.** `packet`, `roster`, `dedupe` and
`cost` fetch the lane branch and fold the record files of the fetched tip in
memory; they write nothing, take no state lock, and **take no checkout lock**,
for the reason `internal/merge/records.go:519-528` already gives about
`packet`: "a reader asking for their own packet while the coordinator's pass
held the checkout got exit 2 and a lock refusal, which is the coordinator's
answer to a writer and not an answer a reader of a report should ever see."
`FoldTip` (`records.go:615`) takes the lock and so is not the function these
verbs use; the work list adds the lock-free tip fold beside it so there is one
implementation of the walk rather than two. A fold that catches a record
mid-write re-reads that one file once before it calls it a problem, which is
the same caveat `FoldReadOnly` carries. The head a packet is built for, and
the head a roster judges, comes from the host through nova-merge's host
interface, never from `state.json`, which is the coordinator's local fold.

The review record:

```json
{
  "version": 1,
  "entry": "951",
  "who": "emma",
  "model": "gemini-2.5-pro",
  "kind": "line",
  "of": "",
  "job": "",
  "verdict": "hold",
  "head": "cbde1fc6ba10c1430f9f90615c70706ea7aaa29e",
  "base": "10a3bf6f2c7d41e0b95a8f3c6d2e17b40a9c8e55",
  "at": "2026-09-13T14:01:02Z",
  "started": "2026-09-13T13:44:10Z",
  "note": "",
  "reason": "",
  "read": "reads/951/emma-cbde1fc6ba10-20260913T140102Z-a1b2c3.json",
  "findings": [
    {"id": "20260913T140102Z-a1b2c3.1", "state": "block",
     "side": "head", "path": "internal/merge/read.go", "line": 88,
     "spec": "docs/SPEC-MERGE.md", "spec_line": 798,
     "rule_kind": "quoted", "spec_pin": "",
     "rule": "A hold blocks, and nothing outvotes it.",
     "claim": "a stale hold is dropped from the fold when a newer approve by another reader exists"}
  ],
  "usage": {"source": "nova-swarm:batch-7", "bench": "studio",
            "job": "j-4471", "attempt": 1,
            "digest": "9c1f0a4b7e22", "model": "gemini-2.5-pro",
            "tokens_in": 41200, "tokens_out": 3810, "cache_write": null,
            "cache_read": null, "reasoning": null, "usd": null,
            "started": "2026-09-13T13:44:10Z", "ended": "2026-09-13T14:00:52Z",
            "seconds": 1002},
  "file": "reviews/951/emma-cbde1fc6ba10-20260913T140102Z-a1b2c3.json"
}
```

The example is checked, not sketched: the quoted text is on
`docs/SPEC-MERGE.md:798` at origin/main — `**A hold blocks, and nothing
outvotes it.** Not three approves, not a green` — and `:799` is the line after
it. It is a row rule 4 accepts because the quote is **found on** line 798
after the emphasis run is trimmed, which is the check rule 4 states and the
one demanded test 4 pins.

`side` is `head` or `base` and says which tree the `path:line` was checked in,
and `base` is the sha `--base` gave — the packet's own base — which is the tree
every `base` side and every `base:` requirement of this record was checked in;
it is empty when the record holds neither (rule 4, draft 5).
`rule_kind` is one of exactly `quoted`, `base`, `external` and `proposed`
(rule 4, draft 4), and it replaces draft 3's `proposed` boolean; `spec_pin`
carries the pin of an `external` row and is empty otherwise; `spec_line` is
absent and the sentence is in `rule` for a `proposed` row. `usage` is what the
file at `--usage` reported, in nova-swarm's own names, under the stable join of
rule 10: `source`, `bench`, `job` and `attempt` are the **receipt identity**
and `digest` is this tool's SHA-256 of the file's bytes, `seconds` is `ended`
minus `started` and is the only derived number in it, and `null` is the row's
`-`. A verdict given `--receipt <id>` instead carries
`{"receipt": "<id>"}` and no token field at all. `null` prints `-` and counts
a dash; `0` is a measurement. `read` is empty for `abstain`, `child` and `card`.

The answer record, under the same directory with the `answer-` stem, which is
how the fold keeps the three apart — it never tries two decoders on one file.

**`answer` and `policy` are therefore reserved words on `--who`, and the
refusal is at the verb.** `safeName` (`internal/merge/records.go:108-116`)
keeps `-` and the letters, so a `--who` of `answer` or of `answer-bob` writes
a review record named `answer-...` or `answer-bob-...` under
`reviews/<entry>/` — a file the fold decodes with the answer decoder and then
refuses, `<VERB> FOLD`, blocking every report on that entry over a name a
reader was free to choose — and a `--who` of `policy` or `policy-bob` does the
same against the policy decoder (draft 5). Every verb refuses a `--who` that
is exactly `answer` or `policy`, or that begins `answer-` or `policy-`, **exit
2**, naming the word and the stem it collides with, before any path is built.
The reserved set is those two words and those two prefixes, because those are
the two stems this tool spends; nothing else about a name is reserved and
`--who` is otherwise any name the caller likes (the vocabulary section).
(drafts 3 and 5)

```json
{
  "version": 1,
  "entry": "951",
  "who": "rowan",
  "finding": "20260913T140102Z-a1b2c3.1",
  "as": "fixed",
  "of": "",
  "head": "9f4a0c1d77b2e5a3c8106d4f2b9e77aa31c50ef8",
  "at": "2026-09-13T15:12:44Z",
  "note": "",
  "file": "reviews/951/answer-rowan-9f4a0c1d77b2-20260913T151244Z-d4e5f6.json"
}
```

`of` is the finding this one duplicates and is empty unless `as` is `dup`.

The policy record, under the same directory with the `policy-` stem, which
keeps its decoder apart from the other two exactly as the `answer-` stem does
(rule 7, draft 5):

```json
{
  "version": 1,
  "entry": "951",
  "who": "glenn",
  "readers": ["emma", "stella", "johnny"],
  "reserved": ["freddy"],
  "deadline": "2026-09-14T00:00:00Z",
  "reason": "freddy's harness is down; his row may be waived at the deadline",
  "head": "9f4a0c1d77b2e5a3c8106d4f2b9e77aa31c50ef8",
  "at": "2026-09-13T16:03:10Z",
  "file": "reviews/951/policy-glenn-9f4a0c1d77b2-20260913T160310Z-77bc1e.json"
}
```

Its id is the stem `<who>-<head12>-<at>-<rand6>`, which is what `roster
--policy <id>` names, and `roster` finds it in the fold it already makes of
`reviews/<entry>/` — no path is guessed and none is a flag (rule 13). The
record is immutable like every other here: a changed policy is a new record
with a new id, and the roster names the id that closed each row.

`version` is checked first in all three, and a number this binary does not know is
refused by number. Decoding is strict: an unknown field refuses. A record that
does not decode is never skipped: `packet`, `roster`, `dedupe` and `cost`
print `<VERB> FOLD file=<path>: <reason>` and refuse, **exit 2**, because the
unreadable file may be the hold (the same reasoning as SPEC-MERGE.md:1158-1164)
and because an unreadable input is what exit 2 is for (SPEC.md:86). `roster`
carries `ratified=false` **on the `ROSTER FOLD` line itself**, because an
exit-2 run prints no `ROSTER OK` at all and an `OK` line on a refused run
would be a second untruth beside the first: it could not run, so it says no,
on the line it has. (draft 3) The fold refusal is capped like every other
kind (rule 12): at most `--max` `<VERB> FOLD` lines and one `<VERB> MORE
kind=fold shown=<n> total=<t>`.

## What the coordinator's morning looks like

Written out once so the verbs are seen together. An author opens an entry and
adds it to the lane with `nova-merge add --needs-read`. For each named reader,
somebody — a script, the reader, the coordinator — runs `packet --who <name>
--out <file>` and points the reader at the file. The reader reads, writes their
findings file, runs `verdict --kind line`, and the record is on the lane
branch within the CAS loop's bound. The coordinator's `nova-merge run` pulls
it on the next pass and counts the read; `roster --readers …` says who is
still pending, and nova-wake (amendment note A) wakes the coordinator on the
new record. A HOLD's findings reach the author in the next packet the author
asks for (`packet --who <author>` lists them as open) and the author records
`answer --as fixed --head <new sha>` as they fix; the next packet for the
reader is the delta since their HOLD's head with their own findings marked
`fixed` by the author, and they record again. `dedupe` on the entry shows, per
finding, which eyes saw it; `cost` shows what the whole entry cost to review
across every round. Nothing in this paragraph is a bus note, and the only
transcription is the reader's own findings file.

## Amendment notes for other specs

These belong to other tools and are drafted here so the whole is visible in one
place; each is a short addition, additive and dated, to be landed in that spec
by its own PR and never duplicated in this tool. **B is a prerequisite**: rule
11 cannot be built before it lands, and the work list starts there.

**A. SPEC-WAKE, a fourth source: records on a lane branch.** "A record file
new at the fetched tip of a lane branch under `reads/`, `gates/` or `reviews/`
is a change; the value is the sorted list of record paths at the tip; the line
is `WAKE LANE lane=<dir> new=<n> reads=<n> reviews=<n> gates=<n>` with the
newest path named." This is the wake the 100-minute HOLD did not have
(2026-09-12): the record already lands where the coordinator pulls; the wake
makes the pull happen. nova-wake watches "three sources and no others" today
and the fourth is a fetch it already knows how to bound.

**B. SPEC-MERGE rule 22, a submission is several named parts with a
manifest.** "An outbox item is `<submission id>-<part>.json`, where `<part>`
is one of exactly `read`, `gate`, `review`, `answer` and `policy`. A
submission also writes `<submission id>-parts.json`, its **manifest**, which
is an item and **not a part** — the sixth and last legal item name, and no
part word is `parts` — naming every part of that submission with the relative
destination its `file` field gives and the SHA-256 digest of its bytes, the
gate's `.summary` sidecar among them; the manifest is written last, by
create-and-rename, and a submission is restored only when its manifest is
present and every part it names is present with a matching digest, each to the
directory its part word maps to — `read` to `reads/`, `gate` to `gates/`,
`review`, `answer` and `policy` to `reviews/` — committed and confirmed
together. A submission whose manifest is present but incomplete or mismatched
publishes no part of itself, is left in the outbox and is named in the
refusal. A submission with **no manifest at all** is a staged orphan: its
parts are named on one `STAGED ORPHAN` line, removed, and published nowhere,
and the flush continues. A file in the outbox that is not a legal item name is
left alone and named in the refusal." Today the loop names the
outbox item `sub.ID() + filepath.Ext(it.Path)`
(`internal/merge/records.go:168`), so two `.json` parts of one submission
overwrite each other and only one lands, which is why a `line` verdict cannot
be built at all until this amendment is in — it is a **prerequisite** of rule
11, not a convenience, and the read-and-review flush has no fallback to fall
back to. It is written as a closed set of parts rather than "any file sharing
the stem" on purpose: a glob over an outbox is a way for a file nobody wrote
to be committed to a branch, and this loop already refuses a destination that
is absolute or holds `..` (`records.go:275`). The `.summary` the loop knows by
name today becomes the `gate` part's sidecar under the same rule, named **in**
the manifest with its own digest, so a submission whose sidecar is missing or
altered publishes nothing (draft 5).

**C. SPEC-MERGE, the read record is unchanged and `status --reads` may name
this tool.** No field is added to the read record (rule 11 says why). `STATUS
ENTRY` gains nothing. The one optional addition is that `nova-merge status
--reads <entry>` prints `review=<path>` when a review record with the same
submission id exists beside a read, so a coordinator reading the merge tool's
listing can open the findings; a lane with no `reviews/` prints `review=-`.

**D. SPEC-BUS, nothing.** A verdict is a record and not a note (SPEC-MERGE rule
19, SPEC-MERGE.md:301: "an APPROVE with no findings is a command and no
note"). A reader who wants
to discuss a HOLD writes a note themselves. This tool adds no verb to the bus.

## What it deliberately does not do

- **It does not review code.** It checks that a finding's ground exists at the
  head; it never checks that the claim is true, and no line of it compares two
  claims.
- **It does not merge, gate, publish or touch the base.** It has no mutating
  call to a host; its only writes are packet files at `--out`, and records on
  the lane branch through the records code nova-merge already trusts.
- **It does not decide who the readers are.** `--readers` is the caller's, per
  call, or the caller's own `policy` record on the lane, written by an actor
  through the verb; the tool keeps no roster file, has no notion of a team, and
  writes no policy nobody ran the verb to write.
- **It does not notify.** No bus note, no PR comment, no wake. The wake is
  nova-wake's (amendment note A).
- **It does not default a read.** No deadline turns a silence into yes. A
  reserved line's silence past the deadline is `waived`, not `abstain`, and it
  is `waived` only because an actor wrote a `policy` record naming that line
  reserved, naming the deadline, and naming themselves: the row carries
  `policy=<id> by=reserved policy_by=<name> policy_reason=<text>
  deadline=<stamp>` — the policy's own word, the actor who selected or changed
  the policy, their reason and their stamp, **never the silent reader's name**,
  because a `by=` naming the reader would say the reader closed the row (drafts
  3, 4 and 5) — and an `abstain` is only ever a reader's own recorded verdict.
- **It does not read the reader's harness.** Cost comes from a usage row the
  caller hands it; the tool opens no session log and knows no provider.
- **It does not infer which rule a hunk serves.** Cited, changed, or named;
  otherwise `rules=0` and the hunk stands alone.
- **It does not run anything, so a reproducer is not evidence it can check.**
  A finding may describe an executable reproduction in its claim, and the
  claim is text: the tool compiles nothing, runs no test and executes no
  script at any head. Checking that a reproducer reproduces is a reader's job
  and a gate's, and a review tool that ran code a findings file named would be
  a tool a findings file could drive.
- **It does not produce a verdict from a swarm's RESULT.md.** A card's verdict
  is recorded by the card's owner running `verdict --kind card`, with the
  findings file the owner wrote or checked; the tool parses no worker report.
- **It does not shorten a finding's claim to nothing** and it does not
  paraphrase a rule; both are quoted through `oneline`, capped at
  `oneline.TailBytes` with the cut marked.

## Known limits

- **A HOLD at a stale head blocks the roster and the merge alike**, and it
  should: the reader's standing is theirs to change. A reader who has left the
  team leaves a HOLD nobody can close; the remedy is a person, not the tool.
- **Rule extent is the spec's declared shape under a heading the caller
  names** (draft 3): `^<n>. ` at column 0, after `--spec <path>#<heading>` and
  before the next `## `, to the next such line or the next heading. A file
  holding several such sequences with no heading named is a refusal and never
  a first-match — `docs/SPEC-MERGE.md` holds four (rule 2). A spec whose rules
  are not a numbered list at column 0 gets `rules=0`, and this spec says so
  rather than guessing.
- **A rule cited by number in a changed line may be the wrong number.** The
  packet quotes what the line cites; the reader decides whether the code
  serves it. That is the reader's job and the whole point.
- **`latency=` is measured from the committer clock**, which is the author's
  machine's word. It is the best number available from the wire alone.
- **A `dup` across heads trusts the reader's id.** The tool checks the id
  names an open finding on this entry and nothing more.
- **Two readers at one head cannot `dup` each other.** They read in parallel;
  neither packet held the other's finding, because neither had been recorded
  when the packets were built. One defect found twice is therefore two
  findings in one group, `members=` shows it, an author's `answer --as dup
  --of <id>` folds them when somebody notices, and until then each reader
  counts as not having reported the other's row. `unreported=` at a head read in
  parallel is an upper bound on what anybody failed to catch, and the ledger
  rule 9 exists for reads high by exactly that much; it is `unreported` and
  never `blind` precisely because the two readers were each handed the range
  (draft 4). The tool will not guess it away: deciding that two claims are one
  thing is the judgment rule 9 refuses to make.
- **`waived` is the caller's word, not the reader's.** A reserved line silent
  past the deadline ratifies the entry because an actor named them reserved,
  named the deadline, and named themselves in a `policy` record with a reason.
  The state is spelled differently from `abstain` and carries `policy=`,
  `by=`, `policy_by=` and `policy_reason=` so the record says who decided and
  why, but nothing in the tool can tell whether that actor was **entitled** to
  reserve that line. That is a person's judgment, recorded here and checked by
  people.
- **The policy record says what was decided and by whom, never that it was
  right** (draft 5). `roster` reads the policy an actor wrote and prints its id
  beside every row it closed; it keeps no roster store, invents no policy, and
  refuses to waive a row with no record behind it (rule 13, "it does not decide
  who the readers are"). A policy record is immutable like every other record
  here, so a changed policy is a new record with a new id and the roster names
  the one that closed the row.
- **`abstain=` on `ROSTER OK` undercounts a TEAM-SPEC (mas-bandwidth/standard)
  rule 5 tally, and the difference is `waived=`.** (draft 3) This tool spells a
  reserved line's silence past the deadline `waived` and counts it in
  `waived=`; TEAM-SPEC (mas-bandwidth/standard) rule 5 — cited by name and
  number with no `path:line`, because that file is in another repository and
  this one cannot open it (draft 3, polished) — spells the same state "abstain
  (by reserve)" and counts it among its abstentions. Both ratify, so
  `ratified=true` agrees with that rule and Glenn's of 2026-09-11; the two `abstain` numbers do not, and a person
  reading one against the other adds `waived=` to `abstain=` to get the team's
  tally. The spellings are apart on purpose — `by=` answers who closed the row
  — and this arithmetic is the price of that answer.
- **`merge_read=` is computed, not fetched.** `roster` applies SPEC-MERGE's
  read condition to the records it folded; it is nova-merge's own fold that
  gates a merge, and a disagreement between the two numbers means a record
  landed between the two reads.

## Tests this spec demands

One line per rule. Each must be seen red before it is trusted.

1. `TestThePacketIsTheDelta`: an entry read by `emma` at H1 and now at H2:
   `packet --who emma` writes a file whose first line names H2 and whose range
   is `H1..H2`; the diff section holds exactly `git diff H1..H2`'s bytes; for
   `stella`, who has never read it, the range is `<base>...H2`; the author's
   intent appears under the label "the author says" and a fixture body
   holding a sentence shaped as an instruction appears verbatim under that
   label and nowhere else; the file is written through a unique
   `<out>.<pid>-<rand6>.tmp` and a kill before the rename leaves no `--out`
   file and no shared `<out>.tmp`; two `packet` runs to one `--out` at once
   both succeed and the file is one of the two whole, never a mixture; with
   `--max-bytes` below the diff, `cut=<n>` is right, the cut files appear
   under "Not included" with the exact `git diff` command, and the file is at
   most `--max-bytes`; `--max-bytes 0`, `-1` and a value below the fixed
   header are each exit 2 with the reason; an `emma` who has only ABSTAINed
   has the same range as an `emma` who has never recorded, and
   `nova-merge packet`'s `range=` for her equals this packet's **after the
   test brings the coordinator's checkout to the tip** (a `nova-merge run
   --once`, or a fetch), because `nova-merge packet` is `FoldReadOnly` over
   the checkout (`internal/merge/records.go:519-521`) while these verbs fold
   the fetched tip: the two agree on a current checkout and the test pulls
   first rather than assuming one (draft 3).
   **`TestThePacketIsTheDelta/OnePacketServesEveryReaderInRange`** (rule 1,
   draft 5): two readers whose range at one head is the same get packets whose
   `id=` is equal and whose six reader-independent sections are **byte for
   byte identical**, while "Your prior verdicts on this entry" and `who=`
   differ; the second, built with `--reuse <first>`, makes **no tree read at
   all** — the test counts the run's `git` invocations and its opens under the
   clone and asserts none beyond the one host call that resolves the head, so
   a mutation that rebuilds the diff turns it red — and prints `PACKET OK …
   reused=true`; a `--reuse` of a packet built for another range is exit 2
   `PACKET REUSE asked=<id> found=<id>` and writes no file; `--reuse` with
   `--spec`, `--rule` or `--max-bytes` is exit 2; and no reader's verdict, and
   no validation of another reader's findings, is carried by a reused packet
   (the verdict binds to `--who` and `--head`, demanded test 6).
   **`/StaleAndCurrentArePrinted`** (draft 5, the two lines that had no
   demanded test): `packet --head <sha>` for a head that is not the entry's
   current writes no file and is exit 1 `PACKET STALE entry=<n> asked=<sha12>
   current=<sha12>`; a `packet` with no `--head` writes the entry's current
   head on its first line; `VERDICT OK` prints `current=true` at that head and
   `current=false` at an earlier one, and a mutation that prints `current=true`
   for a stale head turns the test red.
2. `TestARuleIsTouchedMechanically`: the fixture spec holds **two** sequences
   starting at `^1. ` at column 0, under `## The rules, numbered` and under
   `## Tests this spec demands`, with a different rule 3 in each — the shape
   this repo's own `docs/SPEC-MERGE.md` has four of, so a one-sequence fixture
   would go green against a file that does not exist here. `--spec
   <spec>#The rules, numbered` with a diff line saying `// rule 3` quotes the
   FIRST sequence's rule 3 and no other, with its `path:line`; `--spec
   <spec>#Tests this spec demands` on the same diff quotes the second's;
   `--spec <spec>` with no heading is exit 2 naming both headings; `--rule
   <spec>:5` resolves against the named heading; a bare `rule 3` in a diff
   line with two `--spec` flags prints `rules=0`, and `<basename> rule 3`
   quotes; `rules 3, 6 and 7` quotes three; a hunk that changes the spec
   inside rule 4 quotes both sides of rule 4; a hunk citing nothing prints
   `rules=0` on that file's own line in the packet's **Rules touched**
   section, one such line per file of the diff (draft 3); the spec is read at the head (a fixture where the
   spec on disk differs from the spec at the head quotes the head's text); a
   spec with no numbered list gives `rules=0` and no error.
3. `TestOpenFindingsTravel`: `stella` holds at H1 with two findings; the
   packet for `emma` at H2 lists both with ids; `emma`'s findings file with
   `dup <id1>` records a verdict whose `dup=1`, creates no new finding, and
   `dedupe` prints `id1` with `seen=stella:…,emma:… dups=1`; a `dup` of an id
   that is not open on this entry is exit 2 naming the row.
   **`TestOpenFindingsTravel/OkIsNeverOpenAndNitIs`** (rules 3 and 5, draft
   5): an APPROVE carrying two `ok` rows and one `nit` leaves `open=1` on the
   entry — the `nit` and neither `ok` — the two `ok` rows appear in no later
   packet's open-findings table and are `carried=0` at that reader's next
   record, and they are counted only in `ok=` on `VERDICT OK`; the `nit`
   appears in the next packet and is `carried=1` at that reader's next record
   that does not list it; a mutation that opens an `ok` row turns the test
   red, and so does one that drops the `nit` from the next packet.
4. `TestTheGroundIsChecked`: a row naming a path absent at the head, a line
   past the file's end, a spec line whose text differs by one character, each
   exit 2 with `VERDICT REFUSED row=<n>` and the check named; all three in one
   file print three lines; nothing is written to the outbox or the lane; a
   row whose quote is the rule's first line with `> ` stripped passes; **a row
   whose quote is a bold sentence quoted without its `**` markers passes**,
   against a fixture line shaped like `docs/SPEC-MERGE.md:798`, and a mutation
   that compares for equality rather than containment after trimming turns it
   red; a `base:<path>:<line>` row naming a line the change deleted passes and
   the same row without `base:` is refused; a `+` row records with
   `proposed=1` and `rule=proposed` and is never checked against any file; a
   claim that is false but grounded is recorded (the tool judged nothing); a
   findings file of `--max-rows` + 1 rows is exit 2 naming the count, and a
   file of 5,000 failing rows prints twenty `VERDICT REFUSED row=` lines and
   one `VERDICT MORE kind=row shown=20 total=5000 refused=5000`.
   **`TestTheGroundIsChecked/RequirementProvenance`** (rule 4, drafts 4 and
   5): with `--base <sha>`, the sha the packet's first line carried as
   `base=`, a third field of `base:<spec>:<line>` whose quote is on that line
   **in the tree at that base** and absent at the head records with
   `rule_kind=base` and prints `rule=base` on its own `VERDICT ROW` line, and
   the same requirement written head-side is exit 2 naming the row — so a
   requirement the change deleted is never forced into `rule=proposed`;
   **the same findings file run without `--base` is exit 2 naming the flag, a
   `--base` naming no commit in the clone is exit 2, and a `base:<path>:<line>`
   second field is checked in that same tree** — so test 4's `base:` cases are
   writable at last, and a mutation that falls back to the head for either
   `base:` form turns the test red (draft 5);
   an `ext:<40-hex>:<path>:<line>` row records with
   `rule=external pin=<pin>` while the run opens no file for it, and a
   mutation that tries to resolve the pin turns the test red; an `ext:` with
   a malformed pin — a token holding a `:`, a token holding a space, a
   sixty-five character token — an empty path, a line of `0` or an empty
   fourth field is exit 2 naming the row; a `+` row still records
   `rule=proposed`; **every recorded row prints one `VERDICT ROW` line
   carrying its `rule=` word and its `pin=`, and a mutation that prints the
   word nowhere turns the test red** (draft 5); `base=`,
   `external=` and `proposed=` on `VERDICT OK` count the three apart; the
   four words are the only `rule_kind` any record holds; and a source test
   finds no exec, no `go test` and no compile in the package, so no form of
   requirement causes a reproducer to be run.
5. `TestAnApproveNamesWhatItCompared`: `approve` with an empty findings file is
   exit 2 and the sentence; with one `ok` row it records; with a `block` row
   it is refused naming rule 5; `hold` with only `nit` rows is refused; **`hold`
   with only a `dup` of a finding open at `block` records**, and a mutation
   that refuses it turns the test red; `hold` with only a `dup` of an open
   `nit` is refused naming rule 5; `hold` with one `fix` row records;
   `abstain` with a findings file is refused and with `--reason` records with
   `rows=0`.
   **`TestAnApproveNamesWhatItCompared/CannotStandOverItsOwnBlock`** (rules 5
   and 8, draft 5): a reader with an open `block` of their own recording an
   APPROVE whose findings file neither `close`s nor `dup`s it is exit 2, one
   `VERDICT REFUSED approve=open id=<id> <path>:<line> sev=block` line per
   open id under the cap, with nothing written to the outbox or the lane; the
   same APPROVE carrying a `close <id>` row records with `closed=1`; an
   APPROVE over an open `nit` of theirs records and prints `VERDICT CARRIED`
   for it; and a mutation that closes the block by the verdict word, printing
   `VERDICT CLOSED` after the fact, turns the test red.
6. `TestOnlyALineFillsACell`: `verdict --kind child --of emma` and `--kind
   card --of emma --job j1` at H write review records and no read record, the
   nova-merge fold (`nova-merge status`) shows `reads=0a/0h`, and `roster
   --readers emma` shows `emma pending evidence=2`; `--kind child` without
   `--of` is exit 2 naming the flag; `--kind card` without `--job` is exit 2;
   `--model` missing is exit 2; then `emma --kind line approve` at H makes her
   `yes` and nova-merge's fold shows `reads=1a/0h`. **A read record written by
   `nova-merge read --who stella --head H --verdict approve`, with no review
   record beside it and no `kind` field at all, makes `stella` `yes` with
   `model=-`, moves her `packet` range to H, and is never `pending`** — and a
   mutation that reads an absent `kind` as an absent verdict turns the test
   red by printing `merge_read=satisfied` beside `stella pending` (rules 7, 8;
   draft 3).
7. `TestSilenceIsPending`: readers `a,b,c`, reserved `d`, deadline T: before
   T with `a yes@H, b hold@H1 (stale), c none, d none`: `yes=1 hold=1
   pending=2 abstain=0 waived=0 ratified=false` exit 1; after T, `d` is
   `waived by=reserved deadline=T`, never `abstain` and **never `by=d`** —
   a mutation that prints the silent reader's own name in `by=` turns the test
   red (draft 3) — `c` still `pending`, still exit 1; `b`
   approves at H, `c` approves at H: `ratified=true` exit 0; `a`'s approve was
   at H0 and the head is now H: `a pending head=H0`; **`c` recording an
   explicit ABSTAIN at H0 makes her `abstain` at H and at every later head
   until she records again**, and a mutation that ages an explicit ABSTAIN
   into `pending` turns the test red; every named reader `abstain` gives
   `ratified=true merge_read=needs-read` and exit 0; the entry's author
   approving at H leaves their cell out of the count with `author=true
   evidence=1`, and their HOLD at H sets it to `hold`; a hold anywhere prints
   `merge_read=hold`, not `needs-read` (draft 3); a mutation that reads a
   stale approve as yes turns the test red; a mutation that expires `b`'s hold
   at T turns it red.
   **`TestSilenceIsPending/PolicyIsARecord`** (rule 7, draft 5): `roster
   --readers a,b,c --reserved d --deadline T` with **no** `--policy` leaves
   `d` `pending` after T, never `waived`, and a mutation that waives a row
   with no policy record behind it turns the test red; `policy --who g
   --readers a,b,c --reserved d --deadline T --reason "<text>"` writes exactly
   one immutable `policy-g-<head12>-<at>-<rand6>.json` under
   `reviews/<entry>/`, as one submission with its manifest, and prints its id;
   `roster --policy <id>` after T prints `d` `waived policy=<id> by=reserved
   policy_by=g policy_reason=<text>` and `ROSTER OK` carries the same three,
   while a run whose rows are all filled prints `policy=<id> policy_by=-
   policy_reason=-`; `--policy` together with `--readers` is exit 2; a
   `policy` verb whose `--who` is one of its own `--readers` (`a`) or of its
   own `--reserved` (`d`) is exit 2 naming the flag; `roster --policy` of an
   id no record on the lane carries is exit 2 naming the id; a second `policy`
   record leaves the first unchanged and the roster names the id that closed
   the row; a `--who` of `policy` or of `policy-bob` is exit 2 on every verb,
   naming the reserved word and the `policy-` stem; and a mutation that
   ratifies an entry from a `waived` row whose `policy=` is `-`, or one that
   puts the silent reader's own name in `policy_by=`, turns the test red.
8. `TestNewestPerReaderDecides`: `stella hold@H1` with findings f1, f2;
   `stella hold@H2` re-listing `dup f1` and adding f3:
   **`/OmissionCarriesForward`** (rule 8, draft 4) — open is `{f1, f2, f3}`,
   f2 is **not** closed, that record prints `closed=0 carried=1` with one
   `VERDICT CARRIED id=f2 <path>:<line>` line, and a mutation that closes f2
   by omission turns the test red. **`/ClosureIsExplicit`** (rule 8, drafts 4
   and 5):
   a `close f2` row in stella's next findings file prints `closed=1` with one
   `VERDICT CLOSED id=f2` and leaves f1 and f3 open; a `close` of a finding
   `emma` recorded, and a `close` of an id not open on this entry, are each
   exit 2 naming the row, and a mutation that lets one reader close another's
   finding turns the test red; **`stella approve@H3` with f1 and f3 still open
   at `block` is exit 2, one `VERDICT REFUSED approve=open id=` line per id,
   and records nothing; the same APPROVE carrying `close f1` and `close f3`
   records with `closed=2` and open empty, and a mutation that closes them by
   the verdict word turns the test red** (draft 5); **a `child` verdict recorded with
   `--who stella` at H3 changes nothing — her standing, her open findings and
   her cell are the same before and after** — and a mutation that folds a
   child into a line's ordering turns it red; an author `answer --as fixed` on
   f1 before that changes `answer=fixed` and leaves `open=true`; two records
   with one `at` to the second fold hold-last and a mutation folding
   approve-last turns the test red.
9. `TestTheLedgerNamesTheBlind`: three readers at H whose packet ranges all
   covered `p:12`, two finding `(p:12, spec:40)` and one not: one `DEDUPE
   FINDING` with `seen=` naming two `who:model`, **`unreported=` naming the
   third** and `blind=0`. **`/UnreportedIsNotBlind`** (rule 9, draft 4): a
   fourth reader whose range did not cover `p:12` is `blind=1` and is never
   named in `unreported=`; the in-range non-reporter is `unreported=` and is
   never counted in `blind=`; a mutation that counts an in-range non-reporter
   as blind turns the test red, and so does one that names an out-of-range
   reader in `unreported=`; no line of the package spells an in-range
   non-report as a miss, a failure or a blindness (a source test over the
   printed field names); two claims with
   the same key and different text print one group with `members=` naming both
   ids and `folded=0`, and neither id disappears; an explicit `dup` of one
   onto the other prints `folded=1`; an author's `answer --as dup --of <id>`
   does the same; two with the same text and different lines are two groups; a
   dup-folded finding across two heads prints one `seen=`/`unreported=` pair per
   head; a source test finds no comparison of claim text in the package.
10. `TestCostIsMeasured`: a `line` verdict with a sixteen-column nova-swarm
    usage file and `--started` prints `seconds=` equal to the row's `ended`
    minus `started` and `wall=` equal to `at - started`, and the two differ in
    the fixture so a mutation that uses one for the other turns the test red;
    the five token fields and `usd` come from the row, a `-` in the row prints
    `-`, and a `0` in the row prints `0`; one without `--usage` prints
    **seven** dashes and `dashes=7` — `seconds` and the five token fields and
    `usd`, the seven receipt-fed slots, with `wall` counted in neither (draft
    3); a file whose header is not rule 12's sixteen names
    in order is exit 2 naming the first differing column, and the fixture's
    real nova-swarm file is accepted unchanged; a row whose `model` differs
    from `--model` is exit 2 naming both; two verdicts carrying one receipt
    identity give `receipts=1 reused=1` and sum its tokens once, and
    a mutation that sums twice turns the test red;
    **`TestCostIsMeasured/ReceiptJoinIsStable`** (rule 10, draft 4): two
    benches' usage files both carrying `job=j-1 attempt=1` give `receipts=2`
    and sum both, and a mutation that identifies a receipt by `(job, attempt)`
    alone turns the test red; `--usage` without `--usage-source`, and without
    `--bench`, is exit 2 naming the flag; one receipt identity carried by
    verdicts on **two different entries** is `receipts=1 reused=1` under `cost
    --all` and its tokens are summed **once**, and a mutation that sums it per
    entry turns the test red; two records with one identity and different
    digests print `COST RECEIPT` naming both paths and both digests, exit 2;
    `--receipt <id>` records the id verbatim with every receipt-fed slot `-`,
    `--usage` with `--receipt` is exit 2, and a source test finds no rate
    card, no multiplication of a token count by any number, and no call to
    nova-tokens or any accounting store; `COST HEAD` sums only the
    numbers present and carries `dashes=`; `rounds=` counts distinct heads
    with a `line` verdict and `evidence_rounds=` counts the child-only heads;
    `latency=<n>` equals the last `line` `at` at that head minus the head's
    committer time, and a head with no `line` verdict prints `latency=-`.
11. `TestOneFactOneWriter`: a `line hold` writes **two outbox items with
    distinct names before the flush** — `<id>-read.json` and
    `<id>-review.json`, both present, neither overwritten — and a mutation
    naming both by extension turns the test red; it produces exactly two new
    files on the lane branch with one submission id in both names, one commit;
    the read record is byte-identical to what `nova-merge read` writes for the
    same flags, both built by `merge.ReadItem` (a golden comparison, and a
    source test that neither binary marshals a `merge.Read` outside that
    function); `nova-merge run` on the coordinator's lane prints `pulled=2`
    and treats the hold as a hold; a review record hand-edited to `approve`
    beside a `hold` read record makes `roster` print `ROSTER PAIR …
    field=verdict` naming both files, exit 2, **and `dedupe` and `cost` print
    `DEDUPE PAIR` and `COST PAIR` with the same fields and the same exit while
    `packet` builds the packet, prints no pair line and exits 0** (draft 3); a
    review record with an unknown field is `ROSTER FOLD ... ratified=false`,
    exit 2, and `dedupe` and `cost` refuse it the same way while `packet`
    prints `PACKET FOLD`; **`--who answer` and `--who answer-bob` are each
    exit 2 on every verb, naming the reserved word and the `answer-` stem, and
    a mutation that lets either through writes a review record the fold refuses
    as an answer and turns the test red** (draft 3);
    **`TestOneFactOneWriter/PartialStagingPublishesNothing`** (rule 11 and
    amendment B, drafts 4 and 5): a kill after `<id>-read.json` is staged and
    before `<id>-parts.json` is renamed leaves the next flush publishing **no
    new file at all** — `reads/` in particular gains none, so `roster` still
    shows that reader `pending` and no lone read record can qualify them as a
    `line` reader on restart (rule 8) — and that flush prints one `VERDICT
    STAGED ORPHAN id=<id> parts=1 removed=true` line on stdout, **removes the
    staged part, publishes nothing and exits 0**, and re-running the same verb
    lands both parts in one commit; a mutation that leaves the orphan in the
    outbox, and one that publishes it, each turn the test red; a bare
    `<id>.json` an older binary left in the outbox is **not** an orphan, is
    never removed, is named in the refusal and blocks the flush until a person
    removes it, and a mutation that removes it turns the test red; the legal
    item names are the closed six — the `read`, `gate`, `review`, `answer` and
    `policy` parts and the `parts` manifest — a seventh name is a refusal, and
    a gate submission whose `<id>-gate.summary` sidecar is missing, or whose
    sidecar bytes do not match the digest the manifest gives, publishes no part
    of itself (draft 5); a manifest naming three parts with two present, a part
    whose bytes were edited after the manifest was written, and a part the
    manifest does not name are each a refusal that publishes no part of that
    submission; a `read` part whose `file` names `reviews/951/…`, one naming an
    absolute path and one holding `..` are each a refusal before any part is
    restored; a mutation that restores parts without reading the manifest, and
    one that checks the manifest's names but not its digests, each turn the
    test red; `reviews/` is absent from
    nova-merge's fold (a review record that does not decode does not block
    `nova-merge run`); a `roster` run while a `nova-merge run` pass holds the
    checkout succeeds, and a mutation that takes the checkout lock in any of
    the four report verbs turns the test red.
12. `TestBounded`: fifty readers' records on one entry across twelve heads:
    `roster --max 20` prints twenty `READER` lines and one `MORE`; `dedupe`
    and `cost` the same; `verdict` caps `row`, `recorded`, `closed`, `carried`
    and `open` separately, and a flat cap across the five turns the test red;
    the counts on each `OK` line
    say fifty; the packet for one reader is under `--max-bytes` with the
    prior-verdicts table collapsed to counts past `--max`; `--max -1` is exit
    2 and `--max 0` prints all fifty; **fifty undecodable records print at
    most twenty `<VERB> FOLD` lines and one `<VERB> MORE kind=fold shown=20
    total=50` on each of `packet`, `roster`, `dedupe` and `cost`, still exit
    2, and `roster`'s fold lines carry `ratified=false`** — a mutation that
    prints one line per bad record turns the test red (draft 3); lines and
    bytes measured and written into the commit.
    **`TestBounded/InputIsBoundedWhileRead`** (rule 12, draft 4, moved here
    from test 4 in draft 5 so that rule 12's line sits under rule 12's test):
    a findings file one byte past `--max-input-bytes` is exit 2 `VERDICT
    REFUSED input=bytes file=<path> limit=<n>`, and a 64 MiB fixture is
    refused having read at most the bound — the test asserts the bytes read,
    so a mutation that reads the file whole and measures afterwards turns it
    red; one line past `--max-line-bytes` inside an otherwise small file is
    exit 2 `input=line row=<n>` at that line; `--max-input-bytes 0`, `-1`,
    `--max-line-bytes 0` and `-1` are each exit 2; the same two bounds refuse
    an oversized `--usage` file the same way; and a file that passes
    `--max-rows` but not the byte bound is refused by bytes, because five
    hundred enormous rows are five hundred rows.
13. `TestNothingGuessedNothingSent`: every verb without `--lane` is exit 2
    `refusing to guess`; `packet` without `--out` is exit 2; `roster` with
    neither `--readers` nor `--policy` is exit 2, and `policy` without
    `--readers` is exit 2; a fake bus directory and a fake host record zero
    writes across every verb; a source test finds no call to `nova-bus`, no
    `gh pr comment`, no `gh pr review`, and no write outside `--out` and the
    lane.

## The work list

To build it in Go under `cmd/nova-review`, the way `cmd/nova-merge` is built:
standard library only, `internal/oneline` for every printed value,
`internal/bounded` for every listing, `internal/merge` for the lane's records,
host and checkout lock, `docs/ONBOARDING.md`'s first-day standard, and tests
that execute `docs/CLI.md`'s `### First run`.

0. **`internal/merge`, first and in its own PR, because nothing else can be
   built until it lands.** **Three** additive changes to the tool that already
   exists (draft 3). **(a)** Amendment B's named parts in `writeOutbox` and
   `outbox` (`records.go:162-265`), replacing `sub.ID() +
   filepath.Ext(it.Path)` with `<submission id>-<part>.json`, the closed
   five-part set **and the `<submission id>-parts.json` manifest with its
   digests**, so one submission can carry two `.json` records and a crash
   between them publishes neither. An `Item`
   (`records.go:130-133`) carries only `Path` and `Body`, so **the part is
   derived and never passed**: from the first directory of the record's own
   `file` field — `reads/` is `read`, `gates/` is `gate`, `reviews/` is
   `review` — and, inside `reviews/`, from the `answer-` and `policy-` stems,
   which are `answer` and `policy`. The gate's `.summary` sidecar, which the
   loop knows by name today (`SummaryFile`, `records.go:100-103`), is named
   `<submission id>-gate.summary` by the same rule and is named in the
   manifest with its own digest (draft 5). **The upgrade is not silent and
   must be in the release note**: a bare `<id>.json` an older binary left in
   an outbox is not a legal item name, so amendment B leaves it alone and
   names it in the refusal — and it then blocks every later flush on that lane
   until a person removes it. A **staged orphan** is the other case and is
   not that one: parts whose names are all legal parts of one submission id,
   with no manifest beside them, are named on a `STAGED ORPHAN` line, removed
   by that run, published nowhere, and the flush continues (draft 5). The tool
   removes what it knows it wrote and leaves alone what it does not.
   **(b)** `merge.ReadItem(entry, who, head, verdict, note string,
   s merge.Submission) (merge.Item, error)`, the read record's construction
   and marshal moved out of `cmd/nova-merge/verbs.go:384-386` so both binaries
   build that format through one function (rule 11); `entry` is the directory
   name `merge.EntryDirName(id)` returns — what that line passes today — and
   never the raw pull request number or branch. **(c)** A lock-free fold of a
   fetched tip beside `FoldTip` — the walk of `FoldTip` with `FoldReadOnly`'s
   reason and its single re-read — so a report verb never takes the checkout
   lock. Each with nova-merge's own tests green and a golden over the read
   record's bytes.
1. **`internal/review/record.go`** — the review, answer and policy records:
   strict decode, `version` first, `findings` with every field required and
   `side`/`rule_kind` in the closed sets and `spec_pin` beside them, `usage`
   in nova-swarm's names with the `source`/`bench`/`job`/`attempt` join and
   `digest`, or a bare `receipt` id, numbers or null and `seconds` derived, the file names from the submission
   id with the `answer-` and `policy-` stems keeping the three decoders apart,
   the policy record with its `readers`, `reserved`, `deadline` and `reason`,
   the write
   through `internal/merge`'s outbox and CAS loop as one submission with the
   read record when there is one (item 0a). Tests: demanded 11.
2. **`internal/review/findings.go`** — the findings file parser: five fields,
   the closed state set including `close`, `dup` rows, `base:` rows, the four
   third-field forms (`quoted`, `base:`, `ext:`, `+`), `--max-rows` and the
   `--max-input-bytes`/`--max-line-bytes` bounded reader, the ground check at
   a head (and at the base for a `base:` row or a `base:` requirement)
   through `git show <tree>:<path>` and a line count, the quote
   check as containment after the trimming rule including `**` and `_`, the
   shape-only check of an `ext:` pin, every failing row reported under the
   cap. Tests: demanded 4 and 5.
3. **`internal/review/rules.go`** — rule extent under a caller-named heading,
   the refusal when a file holds several sequences and no heading was named,
   the closed citation grammar including the comma form and the
   `<basename> rule <n>` disambiguator, the three ways a rule is touched, the
   quote with `path:line`. Tests: demanded 2.
4. **`internal/review/fold.go`** — the in-memory fold of `reads/` and
   `reviews/` at a fetched tip, through item 0c and never the checkout lock:
   per-reader-and-kind newest, the author resolved from the host, open
   findings, dup chains, answers, the read-and-review pair check; the roster
   states including `waived` with the `policy=<id>`, `policy_by=` and
   `policy_reason=` it reads from the policy record that closed the row
   and `merge_read=`; findings carried forward and closed only explicitly; the
   dedupe grouping with `members=`, `unreported=` scoped to each reader's own
   range and `blind=`; the cost sums with each receipt identity counted once
   across every entry reported. Tests: demanded 3, 6, 7, 8, 9, 10.
5. **`internal/review/packet.go`** — the packet file: the range from the fold
   (the newest `line` APPROVE or HOLD head, nothing else), the diff from the
   lane's clone, the byte bound with per-file cuts and the remedy command, the
   positive `--max-bytes` and the too-small-for-the-header refusal, the
   unique `O_EXCL` temporary and the rename. Tests: demanded 1 and 12.
6. **`cmd/nova-review/main.go`** — the verbs including `policy`, the flag refusals
   (the `--readers`/`--policy` exclusion, `--base` for a `base:` row, `--reuse`
   and its id check, the reserved `answer` and `policy` names), the output
   grammar exactly as above, `--max` and `--max-bytes`, the exit table, `help`
   and `version`. Tests: demanded 13; every refusal sentence; every exit code.
7. **`docs/CLI.md`'s `### First run`** — `init` a lane with a bare remote,
   `add` one entry, `packet`, a two-row findings file, `verdict`, `roster`;
   every path a flag; a test executes it.

## Ideas folded, 2026-09-13

| source | the idea, in six words | disposition |
|---|---|---|
| Stella, idea 1 | machinery observes and books; a decision packet | rule 1: `packet` is that packet, built from the fold and the host |
| Stella, idea 2 | one packet per item and revision, superseded not lost | rules 1, 3: a newer head's packet carries every open finding forward |
| Stella, idea 3 | one writer and one durable home per fact | rule 11: read record is nova-merge's; review record is this tool's; the finding is the reader's, the disposition the author's |
| Stella, idea 4 | smallest sufficient review packet | rule 1, and SPEC-MERGE rule 23 stays as the index |
| Stella, idea 5 | one structured result, no receipt-of-receipt | `VERDICT OK` is one line; no bus note is sent (rule 13) |
| Stella, idea 6 | measure by decisions, latency, usage | rule 10: `cost`, `latency=`, `rounds=` |
| Stella, 2026-09-11 | a HOLD never expires into approval | rules 7, 8 |
| Glenn, 2026-09-11 | votes are yes, no, abstain | rule 7: `abstain` verdict; reserved and deadline on `roster` |
| Glenn, 2026-09-12 | eyes and mouths | rule 6: `kind`, and the vocabulary section |
| Glenn, 2026-09-11 | look hard, kick the tires | rules 4, 5 |
| Rowan, 2026-09-13 | complementary views, not two checkers | rule 9: `seen=` and `unreported=` per finding, `blind=` for out-of-range |
| Rowan, 2026-09-12 | never presume the answer in a read question | rule 1: the packet asks nothing; the intent is labeled data |
| Rowan, 2026-09-12 | owned PR comments are the bus too | amendment note A: the record is the channel, nova-wake wakes on it |
| #183 | review packet with exact base/head, provenance, prior findings, cost visible | rules 1, 6, 10 |
| #183 | mechanical shape checks validate structure, not correctness | rule 4: the ground is checked, the claim never |
| #229 | a recorded delta review per merge after a freeze | not folded: a release-shape rule for nova-merge; `roster` at the candidate head is the evidence it would read |
| Freddy, DeepSeek 2026-09-11 | cache routine verdicts | not folded, as in SPEC-MERGE: a verdict is a person's per sha |

## Reads folded, 2026-09-13 (draft 1 to draft 2)

Three complete reads at `10a3bf6f`, each a HOLD, each opened against the tree
at origin/main: Fable (nova-tools#236 comment 5655199752), Opus (5655215551)
and Stella on GPT-6 Astra (5655185142). Every finding in the three is repaired
above or ruled out on the PR with its reason, and the repairs are by class
rather than by instance: the outbox collision is closed for every multi-record
submission and not for this one pair; the rule extent is scoped for every spec
and not for `SPEC-MERGE.md`; the quote check is a containment test for every
emphasis run and not for `**`. Three places the readers disagreed are decided
in the comment on #236 and not silently: the rule-extent repair (a caller-named
heading, not a hard-coded one), the `roster` exit code (the check stays at 1,
`dedupe` and `cost` drop to 0, an unreadable record is 2), and what a reserved
line's silence is called (`waived`, which ratifies as Glenn's rule of
2026-09-11 requires, spelled apart from the reader's own `abstain` as Stella
asked).

## Reads folded, 2026-09-13 (draft 3 to draft 4)

Stella's scoped disposition at `841d492b` against her seven findings of
`5655185142`, answered by id. Her finding 4 (provenance ordering) she cleared
at that head and nothing here touches it.

| her id | the remaining ask | the repair |
|---|---|---|
| 1 silence/waiver | `by=reserved` names a mechanism, not the actor an audit needs | rule 7: `--policy-by` and `--policy-reason` join `--reserved` and `--deadline` as all-four-or-none, print on every `waived` row and on `ROSTER OK`, and may not name a reader of either list |
| 2 identity/closure | closure by omission is not an explicit resolution; `blind` claims more than a non-report | rule 8: omission carries a finding forward open, `carried=<n>` and `VERDICT CARRIED`, closure only by that reader's APPROVE or a `close <id>` row; rule 9: an in-range non-report is `unreported=`, and `blind=` counts only readers outside their declared range |
| 3 absent requirement | a deleted agreed requirement is forced into `rule=proposed` | rule 4: `base:<spec>:<line>` checked at the base and `ext:<pin>:<path>:<line>` shape-checked and never opened, beside `+`; `rule_kind` is a closed four |
| 5 accounting | `(job, attempt)` is not unique across pools and benches | rule 10: the identity is `<source>/<bench>/<job>/<attempt>` with the file's digest, or a `--receipt <id>` into nova-tokens; each identity counted once across every entry reported; price stays in nova-tokens |
| 6 bounds | `--max-rows` does not bound one enormous line or file | rule 12: `--max-input-bytes` and `--max-line-bytes`, enforced on a bounded reader during the read, refusing before allocation |
| 7 atomic pair | a closed part vocabulary is not proof every part exists | rule 11 and amendment B: a `<submission id>-parts.json` manifest with per-part digests, written last, and a submission restored only whole — so a crash after staging the read publishes nothing, and destinations stay confined to each part word's own directory |

Draft 5 narrowed two of those repairs. Closure is a `close <id>` row and
nothing else: an APPROVE over an open block of the approver's own is refused
rather than closing it, so the "printing CLOSED after the fact" pattern is
gone from the APPROVE case as well as the HOLD case. And the policy an audit
reads is a record on the lane branch written by the actor, not the `roster`
run's own output.

No reproducer is executed for any requirement form (what it deliberately does
not do), and no new product scope is added: every repair above is a contract
on a rule that was already here.

## Reads folded, 2026-09-13 (draft 4 to draft 5)

Rowan's own read on Fable at `399c9b34` (nova-tools#236 comment 5657139511),
an APPROVE with seven MEDIUM and six LOW, every `path:line` it cites opened at
that head. Every MEDIUM is folded above and every LOW with it; none reopens a
decided question and none adds product scope.

| the number | the ask | the repair |
|---|---|---|
| M1 | a run's stdout is not the audit record of a waiver | rule 7: a `policy` verb writes one immutable `policy-<who>-<head12>-<at>-<rand6>.json` under `reviews/<entry>/`, the `answer-` pattern; `roster --policy <id>` replaces the four flags, and a row is `waived` only under a policy record |
| M2 | `verdict` cannot find "the packet's base" | rule 4: the packet's base is the sha the packet's first line carried as `base=`, recorded there at creation; `verdict --base <sha>` carries it back and is required for any `base:` row |
| M3 | `rule=` is printed by no line | the grammar gains `VERDICT ROW`, one capped line per recorded row, carrying `rule=` and `pin=` where the row's other fields are printed |
| M4 | the manifest is a fifth name and the orphan has no fate | six legal item names, closed: five parts and the `parts` manifest, which is not a part; a staging with no manifest is a `STAGED ORPHAN`, named, removed, published nowhere, on a run that exits 0 |
| M5 | are `ok` and `nit` rows open findings? | rule 3: an `ok` row is never a finding and is never carried; a `nit` is a finding, travels and carries forward, and blocks nothing |
| M6 | nothing says what a second reader reuses | rule 1 and the packet section: every section but "Your prior verdicts" is a pure function of `(entry, head, range)`, the packet's `id=` names that tuple, and `packet --reuse <file>` hands a second reader the same bytes with no tree read |
| M7 | APPROVE closes by verdict word | rule 8: the only closure is a `close <id>` row, and an APPROVE standing over that reader's own open `block` or `fix` is refused, exit 2, naming the ids |

The six LOWs are folded in place, in order: the fold is rules 3 and 9 (:28); a
caller-supplied version token has a grammar and holds no `:`, so "malformed
pin" has a meaning; the receipt id has a shape and the store is named "the
caller's accounting store (nova-tokens in this house)"; `PACKET STALE` and
`VERDICT OK current=` have their clauses in demanded test 1; rule 12's
`InputIsBoundedWhileRead` moves from test 4 to `TestBounded`; and the gate's
`.summary` sidecar is named **in** the manifest with its own digest. No LOW is
left open.

## Reads folded, 2026-09-13 (draft 2 to draft 3)

Two complete reads at `522bab88`, both HOLD, both opened against the tree at
origin/main: Fable (nova-tools#236 comment 5655353303) and Opus (5655363068).
The two agree finding for finding on what draft 2 broke, and nothing in either
reopens a decision: two passages were corrupted in the draft-2 repair itself —
rule 6's citation, spliced so the misattribution it was written to remove was
still on the page, and the "three faces" paragraph, whose third face was cut
mid-sentence and whose next sentence was left twice — and the rest are one
word, one field or one missing grammar line. Every finding is folded above as
the class, marked `draft 3` at the passage:

- rule 6's spliced quote is gone and Rowan's attribution stands alone; the
  three quotes no reader could open are placed as a class, once, before the
  laws, as memory and bus and not as evidence;
- the third face is restored with SPEC-MERGE rule 23's index sentence quoted
  at its line, and the duplicated sentence is deleted;
- the outbox part set is `read, gate, review, answer` at both sites and there
  is no three-part set anywhere;
- `waived` carries `by=reserved deadline=<stamp>`, never the silent reader's
  name, and Known limits says what `abstain=` then undercounts;
- `answer` is a reserved word on `--who`, refused at the verb, so a name can
  never collide with the `answer-` stem;
- a record with no `kind` is a `line` verdict, so `merge_read=satisfied`
  cannot print beside that reader's `pending` and the two ranges cannot
  diverge;
- the usage field count is nine, `dashes=` names its seven slots, `rules=0`
  has a line to print on, `DEDUPE PAIR` and `COST PAIR` are in the grammar,
  `merge_read=` has a `hold` value, the fold refusal is a capped kind, and
  `ROSTER FOLD` carries `ratified=false` on the line it has;
- the four nits: the work list's third change, `ReadItem`'s `entry`, the
  checkout-versus-tip clause in test 1, and Known limits' rule extent.
