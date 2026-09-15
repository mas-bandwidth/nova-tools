# nova-work DELEGATION — coordinator notes and the current goal (DRAFT 3, 2026-09-15)

**Status: a draft, Rowan's, on Stella's ask of 2026-09-14 (stella-cc8561e3fde4), repaired on
her review of draft 1 (nova-tools#335, comment 5672078177, and stella-a8da9cb0e0a4) and of
draft 2 (comment 5673066509). Nothing here is built.** It is planning inside the v2
recursive-node boundary of nova-tools#321 and changes no v1 completion count. Every requirement that is Glenn's cites its source by comment
id so a reader can check the words: **5671991172** (the DELEGATION section itself, 23:03Z) and
**5672006742** (the coordinator notes and the shared current goal inside it, 23:05Z), both on
nova-tools#321. Where this draft decides something Glenn did not say, the sentence is marked
**(Rowan's decision, for review)** and listed together at the end; where a point is open, it
is marked **(open decision)** and listed there too. This file is the DELEGATION section of
[SPEC-WORK.md](SPEC-WORK.md) (PR #231) written as a sibling while that document is under joint
authorship; when its authors take it, it folds into that file and this file is deleted, not
kept beside it.

What draft 2 changed, keyed to the review: (1) a note's identity is an immutable content
digest and supersession is one atomic envelope through SPEC-WORK.md's own writer, not an
in-place edit of a shared file; (2) every example is restricted-Lisp data; (3) storage and
evaluation are bounded by named limits and `applicable` evaluates the whole active set before
it prints a verdict; (4) the current goal is specified — set, retrieve, update — with its
witnesses (since folded into SPEC-WORK.md; the section below points); (5) the incident paragraph is corrected against the bus record.

What draft 3 changed, keyed to Stella's review of draft 2 (5673066509): (1) `goal update
--stop` requests cancellation through the existing `:cancel-requested` transition and never
asserts a stopped worker; the pending stop is a value of `show`'s `stop=` field, so no harness
resumes work while confirmation is pending; both goal witnesses are repaired to say so; (2)
`notes supersede` constructs its replacement with the old note's `:kind`, inherited; (3) the
snapshot decision is closed by her selection: a route is eligible only on a live evaluation of
the current revision, a snapshot answer is planning only, and the false claim about the example
config is removed; (4) the example ids are labelled abbreviated, and verdict rows are under the
total response bound and refuse rather than print a partial answer.

The reason this exists, in Glenn's words (5672006742): a place *"where you write your own
notes to, as informed by our conversations"*, so that *"what I just told you about how and what
to delegate"* is kept, and a coordinator does not drift back to a route he has forbidden. The
day's history, as the bus records it: at 20:00Z Glenn asked that long work stay in Astra
children so Stella's session stayed available for him (stella-5e0d788049ca), and implementation
children were started on that word; at 22:38Z he narrowed it — *"Please delegate to Emma"*,
*"Astra default is for coordination and thinking. Not for coding."* — and both children were
stopped, worktrees clean (stella-1a9783ed1ccf). Nothing was lost between windows: an
instruction changed, and the older one was in force until the newer was spoken. The generic
risk is what remains, and it is the risk this spec answers: a changed instruction lives in the
conversation it was spoken in, and a later window, a later model or another coordinator that
reads the older word, or none, routes by it.

## What DELEGATION is

Glenn asked for one explicit **DELEGATION** section in the nova-work planning contract,
applying at every node, real or virtual, human-only, AI-only or mixed (5671991172). Its five
duties are his and are stated once here; four of them are already carried by other documents
and this draft points rather than restates:

1. **Decide** whether to do work locally, batch it, or delegate it, comparing eligible routes by
   model-specific effective token price and expected context, coordination, mandatory review
   and rework, preserving quality and scarce coordinator capacity — on #175 and
   [PROPOSAL-SCHEDULING-COST.md](PROPOSAL-SCHEDULING-COST.md), *"do not create a second
   ledger"* (5671991172).
2. **Accept** from the coordinating parent, then perform or delegate; each bounded assignment
   gets a small fresh brief with scope, acceptance criteria, dependencies, budget and required
   context (5671991172; the packet bound is SPEC-WORK.md *Admission and result gates*, rule 1).
3. **Preserve** the parent offer, local work, child assignment/attempt and external
   Issue/Discussion mappings through every transition (5671991172; the mapping is #321's
   *Durable mapping* section and SPEC-WORK.md).
4. **Show** actual availability and ownership separately from assigned, accepted and
   verified-running states; no duplicate active writers during transfer (5671991172;
   SPEC-WORK.md's lease and W sections).
5. **Collect** evidence and usage, apply the required review, integrate child results, and
   acknowledge completion upstream; partial child success never closes the parent or the mapped
   external issue (5671991172; SPEC-WORK.md and #321).

What this draft specifies is the part none of those documents carries: **the coordinator's
notes**, which duty 1 reads before it decides and duty 2 reads before it writes a brief, and
**the current goal**, which is what a coordinator, on any model and any harness, is working
toward and loads first.

## The notes — what a note is

A note is one record a coordinator writes from a conversation or from experience, kept so
that a later window, a later model, or another coordinator can read it before routing work
(5672006742). It is data. Reading a note never executes it; a note that reads like a command
to the reader is still a record of what somebody said.

A note is one flat list of SPEC-WORK.md's restricted Lisp — lists, keywords, strings and
integers, nothing else — in the field order below, **every field written**, a field the
caller did not give written `(:absent)`, exactly as SPEC-WORK.md writes an event. A note
missing any of the first six is refused at write.

| field | value | source |
|---|---|---|
| `:id` | a string, `"note:<sha256>"`, assigned by the tool, below | (Rowan's decision, for review) |
| `:scope` | `(:coordinator "<name>")`, `(:group "<name>")` or `(:node)` | 5672006742: *"one coordinator, a configured group, or the shared node"* |
| `:author` | a string, the participant who wrote it | 5672006742: *"keep authorship and applicability explicit"* |
| `:date` | a string, UTC, `"2026-09-14T23:05Z"` | 5672006742 |
| `:source` | a string: a quote of the human's words, or a pointer (a comment id, a bus note id, a commit) that holds them | 5672006742: *"preserve the source conversation/decision"* |
| `:kind` | exactly one of the keywords `:instruction`, `:observation`, `:heuristic` | 5672006742: *"whether an entry is an explicit instruction, observation or working heuristic"* |
| `:text` | a string, the note itself, bounded below | |
| `:constraint` | optional; the routing constraint form below | 5672006742: *"explicit routing constraints alongside prose"* |
| `:uncertain` | optional; a string, what the author does not know | 5672006742: *"record uncertainty rather than inventing a user decision"* |

`:state` is **not a field of the note**: it is derived from the log, `:active` unless a
supersede event names the note, then `(:superseded-by "<id>" "<date>")`, and every reader
prints it beside the note as SPEC-WORK.md's snapshot prints derived state beside a node. That
is what keeps the record immutable: nothing the tool writes later touches the note's own list.

**Identity.** `:id` is `note:` followed by SHA-256, lowercase hex, over the canonical
serialization of the note's own fields in the order `:scope`, `:author`, `:date`, `:source`,
`:kind`, `:text`, `:constraint`, `:uncertain`, absent fields `(:absent)`, printed by the same
deterministic printer SPEC-WORK.md's payload digest uses — the preimage is the content and
nothing the session assigns, never a file, never a position in one, so the id is the same on
every bench and every build and does not move as other notes are written **(Rowan's decision,
for review)**. A second write with the same preimage is the same note: refused `NOTES FAIL …:
already written note=<id>`, nothing written. An id is never reused because a preimage is
never rewritten.

`:kind :instruction` means the human said it; its `:source` must quote or point at the human's
words, and a note whose source is the author's own inference cannot be an instruction — it is
an `:observation` or a `:heuristic`, whichever the author claims, and the tool does not judge
which. **(Rowan's decision, for review)**: the tool checks the shape of the source (a quote or
a pointer is present), never its truth; truth is what the reads are for.

The names inside `:scope`, `:author`, `:source` and `:constraint` are **instance data**:
they are whatever the node configured, and nothing in nova-tools knows or prefers any of them
(5672006742: *"the data model supports arbitrary participants; product rules must not hard-code
our names or model choices"*). This is also the house law that nothing in nova-tools is
specific to us. A test that mentions a model name mentions a made-up one.

## Where the notes live, and who writes them

Notes are records of the resident work set, written by the one writer SPEC-WORK.md already
has and no other: the owning coordinator's session, one typed event per mutation, carrying a
request id, validated against the current local revision, appended to the local recovery
journal before it is acknowledged, published by a clip as part of the one revision-labelled
snapshot (SPEC-WORK.md, *The execution model*, and Stella's *One coordinator, one live
reader/writer*). **No new bus, no new repository, no new file that a second process writes.**
Draft 1's three files under `notes/` with their own writers are withdrawn: a group or node file
with several writers has no atomic supersession, and a file's own hash is not an identity
(Stella, 5672078177, point 1).

Notes are an index of the resident model in the sense SPEC-WORK.md gives `friends` and
`models`: **no node kind of the containment forest, no count, no roadmap cell, no required set
moves when one is written**. Their event kind is `:note` — `:change` (`:write` or
`:supersede`, one per event), `:note` (the id), `:scope`, `:author`, `:date`, `:source`,
`:kind`, `:text`, `:constraint`, `:uncertain`, `:superseded-by`, `:reason`, in that order for
the payload digest; its subject is a note identity, `:node` is `(:absent)`, and `NOTES OK`
prints `note=<id>` in place of `node=<id>` **(Rowan's decision, for review)**: this is the
shape SPEC-WORK.md gives every subject that is not a node, and the replays
`new-verbs-have-a-kind-and-a-field-order` and `new-verbs-retry-to-one-event` cover `:note`
as they cover `:friend`.

**Who writes.** A participant writes a note the way a friend submits a result: as a request
to the owning session, with `:by` the participant. The session checks `:by` against the
configured participants of the note's scope: a `(:coordinator "A")` note is written only by
A; a `(:group "G")` note only by a member of G, where G is a group registered by SPEC-WORK.md's
`friend --group` and not a second registry; a `(:node)` note by any registered participant.
Whether a participant may *read* another coordinator's notes is the node's access rule, not
this spec's — filtering a view never establishes an access boundary (#321, *Recursive
accounting and existing guardrails*), so this draft claims no privacy for a note that the
work repository's access does not give it. A note grants nothing (5672006742: notes *"never
create credentials, permissions or access"*).

Another bench reads notes as it reads everything else: from the published snapshot at a named
revision, or from the live session. There is no third road.

## Bounds

Nothing here is *"small by construction"*; it is small by refusal. Three limits are CONFIG
data of the node, none defaulted, a missing one `refusing to guess` (SPEC.md *Conventions*):

```lisp
(:notes :max-active 200 :max-text-bytes 2048 :max-constraint-nodes 64)
```

- `:max-active` bounds the **active** notes across all scopes of the node. A `write` that
  would exceed it is refused, `NOTES FAIL …: active=<n> past :max-active=<n>, supersede or
  retire one`, and nothing is written. A `supersede` never changes the active count.
- `:max-text-bytes` bounds `:text`, `:source` and `:uncertain` each; `:max-constraint-nodes`
  bounds the constraint form. Past either is refused at write, naming the field and both
  numbers.
- The snapshot, and every read of it, is under the session's own `--max-bytes --max-depth
  --max-nodes` (SPEC-WORK.md, *The data*), which the notes share with everything else and
  which refuses, never truncates.

Superseded notes are never deleted by the tool. They leave the resident set the way closed
events do: a clip moves a superseded note older than `--retain` to the closed archive under
the same versioned index root, in pages bounded by `--page-bytes` and `--page-records`, and
`list --all --from <stamp> --to <stamp>` reads them from there. So the resident set holds at
most `:max-active` active notes plus the superseded ones inside the retention window, and the
archive grows by pages that are read by date and never whole. No new index project: this is
the existing retention boundary with one more record kind in it.

## The verbs

Four verbs, one binary, `nova-work` (SPEC-WORK.md), under the `notes` subject; `NOTES` joins
SPEC-WORK.md's *Output grammar* as a verb token with the same `OK`/`FAIL`/`ROW`/`MORE` lines
and the same cap-and-count law. **(Rowan's decision, for review)** their names and shape;
the duties are Glenn's.

```
nova-work notes write      --as <name> --scope <scope> --kind <kind> --source <text> --date <utc> --text <text> [--constraint <form>] [--uncertain <text>] --request <id> --expect <rev>
nova-work notes list       --as <name> [--scope <scope>] [--all [--from <stamp> --to <stamp>]] --max <n>
nova-work notes supersede  --as <name> --id <old> --source <text> --date <utc> --text <text> [--constraint <form>] [--uncertain <text>] --reason <text> --request <id> --expect <rev>
nova-work notes applicable --as <name> --task-class <class> [--candidate <role>/<model> ...] --max <n>
```

**`write`** appends one note as one `:note :write` event. Refused (`NOTES FAIL`, exit 2, one
line naming the field) when `:source` or `:date` is missing (5672006742: *"preserve the
source conversation/decision, date"*); when `:kind` is not one of the three keywords; when
`--as` is not a configured writer of the scope; when a bound above is exceeded; when
`--expect` is stale (SPEC-WORK.md's `--expect`: the caller's expected revision, refused
`stale` naming the current one); and when the constraint form carries anything but `:deny`,
`:prefer` and `:reason` — a note *"records decisions and never create[s] credentials,
permissions or access"* (5672006742), so there is no `:allow`.

**`list`** prints the active notes for a scope, one `NOTES ROW` per note: id, kind, date,
author, the first line of the text, and `constraint` if one is present, capped by `--max`
and counted, `NOTES MORE` when cut. `--all` adds the superseded ones, each with the id and
date that superseded it, from the resident set and, with `--from`/`--to`, from the archive's
pages. **A superseded note is never printed as active**, by any verb, with or without `--all`
(5672006742: *"preserve the old entry as superseded and apply the current applicable
decision"*).

**`supersede`** is **one mutation, one envelope, two events**: a `:note :write` of the
replacement and a `:note :supersede` on the old id naming `:superseded-by` the new id. **The
replacement is constructed in full before anything is hashed**: its `:scope` and its `:kind`
are the old note's, inherited — `supersede` has no `--kind`, and this is the smallest choice
consistent with the rule below (Stella, 5673066509) — its `:author` is `--as`, and `:source`,
`:date`, `:text`, `:constraint` and `:uncertain` are the flags given, absent ones `(:absent)`;
its id is the digest of that complete list, and it is validated as `write` validates a note.
A note of another kind is a new `write`, not a supersede. The envelope is validated whole
against the resident set at `--expect`, journaled whole, applied whole; a failed validation writes neither event (SPEC-
WORK.md: *a failed validation changes neither O nor the journal*), and a multi-event envelope
never partly publishes. Refused when the old id is not active — so of two competing
supersedes of one note, the second is refused `not active: superseded-by <first>` and its
replacement is never written, and at no revision are an obsolete instruction and its
replacement both active, or either lost; when the new note has no source or date; and when
the replacement's `:kind` would be weaker than the old one's — a `:heuristic` or an
`:observation` cannot supersede an `:instruction` (5672006742: *"inferred heuristics cannot
silently override explicit instructions"*), which inheritance makes true by construction and
the validator still checks on the whole envelope. To change an instruction, the human must have
said something, and the new `:source` shows where. The old note's list is not touched; its
`:state` is derived.

**`applicable`** is the read before the route. Given a task class and, optionally, the
candidate routes the caller is choosing among, it loads **every** active note whose scope
covers `--as` (the coordinator's own scope, every group `--as` belongs to, and `(:node)`),
evaluates every constraint among them against every candidate, and only then prints: one
`NOTES ROW` per candidate, `eligible` or `excluded <note-id>` — never cut by `--max`, one line
per candidate the caller named — followed by the applicable notes as `NOTES ROW` lines under
`--max`, `NOTES MORE` when cut. The whole answer is under the session's total response bound
(`--max-bytes`, SPEC-WORK.md); when the verdict rows alone would not fit it, the verb refuses
`NOTES FAIL …: past --max-bytes` and prints no verdict at all — never a partial list of
`eligible` rows (Stella, 5673066509). **The cap is on the prose rows and never on the
evaluation**: the verdict for a candidate is computed from the complete active set, which `:max-active`
bounds and which the session holds whole, before any row is printed, so a deny in a note the
display cut still excludes (replay `applicable-cap-never-hides-a-deny`, below). Where the
complete set cannot be loaded — the session is not live and no `--snapshot` is given, a
snapshot past a bound, a missing notes index — the verb prints `NOTES FAIL …: <reason>` at
exit 2 and **no candidate is printed `eligible`**; a caller reads `unknown`, and unknown is
not eligible (SPEC-WORK.md gate 2: *unknown price is not cheap*, and here unknown policy is
not open). A candidate whose model is not registered in SPEC-WORK.md's `models` is `unknown`
for the same reason. The rule Glenn set (5672006742): *"retrieve the applicable notes before
selecting a route or constructing a job brief"*, and *"a configured restriction on a
model/role/task class must filter candidate routes before dispatch, with a visible reason for
exclusion"*. The reason is the note id, and the note is one `list` away. What the caller
carries into the brief is the constraint lines and the note ids, not the conversation the
notes came from (5672006742: *"without copying accumulated conversation history"*).

`applicable` answers from the live session, or from a published snapshot with `--snapshot`,
and every answer prints the revision it was evaluated at, `rev=<n>`, and where it came from,
`from=live` or `from=snapshot`. **Display and eligibility are separate.** A snapshot answer is
read-only planning: it shows what the constraints were at its revision, and no row of it makes
a route eligible, because the age of a snapshot alone cannot establish that a stop or a deny
has not been written since (Stella's decision, 5673066509, closing draft 2's open decision).
**The check boundary**: before a route is treated as currently eligible — before the route
selector prices it and before a card builder writes a brief for it — the caller evaluates the
complete current constraints against the owning session at its current revision, by the
existing owning-session and revision discipline: the eligible verdict carries `rev=<n>
from=live`, and the write that admits the route carries `--expect` that revision, so a stop or
a deny written between the check and the admission refuses it as `stale`. Where that live
evaluation is stale or unavailable, the policy state is unknown and the route is refused, as
above. A brief built from a snapshot answer is a draft until that check passes. This adds no
lease and no scheduler, and it promises nothing about an instruction that changes after
admission: that reaches the running work by a stop, not by this check. There is no
`:max-snapshot-age` in the example config and no age bound anywhere in this draft.

Where the route selection of duty 1 is built (on #175 and PROPOSAL-SCHEDULING-COST.md), it
calls `applicable` first and prices only the routes that came back `eligible`; SPEC-WORK.md's
gate 2 already says *eligibility is checked before price/preference*, and this is where the
eligibility comes from. A brief constructor (nova-swarm's card, SPEC-SWARM.md) that is handed
an `excluded` or `unknown` route refuses to build the card, naming the note or the reason.
**Narrative reminders alone do not filter** (5672006742): a note with prose and no
`:constraint` is printed by `applicable` for the coordinator to read, and it excludes nothing;
the coordinator who wants a route excluded writes the constraint form.

## The constraint form

Beside the prose, a note may carry one structured constraint, so the filter above has
something to filter by. **(Rowan's decision, for review)** its shape, restricted-Lisp data
like everything else — names are strings, task classes are keywords:

```lisp
(:constraint
  (:deny   (:model "<name>" ...) (:role "<name>" ...) (:task-class :<class> ...))
  (:prefer (:task-class :<class> ...) (:route "<role>/<model>" ...))
  (:reason "<text>"))
```

`:deny` names any of a model, a role, and a task class; a candidate route is excluded when it
matches every named axis (a `:deny` with `:model` and `:task-class` excludes that model for
those classes and nothing else). `:prefer` is advice to the route selector and excludes
nothing. Task classes are the vocabulary SPEC-WORK.md's `:model` events already carry in
`:task-class`, configured per node, not a second list here; an unknown class in a constraint
is refused at write, so a typo cannot open a hole. There is no `:allow`: eligibility is what
configuration and the active constraints leave, and a note cannot widen it. Model and role
names are instance data throughout.

Two active constraints that disagree are not resolved by the tool: `applicable` prints both
and marks the candidate `excluded` — a deny wins over a prefer, and a deny wins over silence —
and the coordinator supersedes one of them with a source. **(Rowan's decision, for review.)**

## The example instance — instance data, not product

The current instruction, as Glenn gave it on 2026-09-14 (5672006742, and the same words
relayed in stella-1a9783ed1ccf: *"Astra default is for coordination and thinking. Not for
coding."*). Every name in it is configuration of this node and appears in this document only
as the worked example; a test uses other names. Both forms below are valid restricted Lisp
as SPEC-WORK.md's reader defines it. **The ids shown are abbreviated illustrative ids, not
SHA-256 results**: the complete vectors — the full canonical preimage and its full 64-hex
digest — are generated by the test from these exact fields, and nothing on this page is a
digest to be trusted (Stella, 5673066509).

```lisp
(:id "note:3f1c…" ; abbreviated, illustrative; the test computes the full sha256 of the fields below
 :scope (:coordinator "Stella") :author "Stella" :date "2026-09-14T23:05Z"
 :source "nova-tools#321 comment 5672006742; Glenn, live, 22:38Z: 'Astra default is for coordination and thinking. Not for coding.'"
 :kind :instruction
 :text "Astra is for coordination and high-level planning and thought only. Coding, implementation and lower-level execution go to Emma, DeepSeek swarms or a suitable local model."
 :constraint (:constraint
              (:deny (:model "Astra") (:task-class :coding :implementation :execution))
              (:reason "Glenn, 2026-09-14: coordination only"))
 :uncertain (:absent))
;; derived, printed by the reader, never in the record: :state :active

(:id "note:9b40…" ; abbreviated, illustrative
 :scope (:node) :author "Stella" :date "2026-09-14T23:05Z"
 :source "nova-tools#321 comment 5672006742"
 :kind :instruction
 :text "Use small fresh contexts directly for bounded work. Compare the coordinator's and the worker's effective token prices plus context, handoff, required review and rework on the whole route; delegation at a cheaper rate can be worth it at more tokens. Keep the main session available for human communication."
 :constraint (:absent)
 :uncertain (:absent))
```

The first note is what the day's narrowing needed: with it active, `applicable --as Stella
--task-class coding --candidate coordinator/Astra` prints `excluded note:3f1c…`, and a card
for that route is refused before any child starts; the note's `:source` is where the older
instruction of 20:00Z was changed, so the next window reads the change and not only the
result. The second has no constraint and excludes nothing; it is read. Glenn's own phrasing of
the economics, *"cheap-token delegation may be worthwhile even at more tokens"*, is the
whole-route rule of PROPOSAL-SCHEDULING-COST.md, and the note points at it rather than
restating the arithmetic.

## The current goal

**The goal is specified in SPEC-WORK.md, *The current goal* under *The data*, and this file
points at it rather than restating it** (the fold Rowan drafted from this section's draft 3 on
Stella's clearance, stella-1858e1eeef8d; PR `rowan/spec-work-goal` against `spec/nova-work`).
There: the goal is a node of O and the current goal a scope-keyed reference in a `goal` index
beside `friends` and `models`, written by the `:goal` event kind; the verbs `goal set`, `goal
show` and `goal update` beside the other verbs, with `--expect` required on the two writes;
`--stop` as the transition table's own edge to `:cancel-requested` with no evidence, confirmed
cancellation staying `event --kind cancel --evidence`; the `GOAL` lines in *Output grammar*;
and the witnesses in *Acceptance replays* — `goal-crosses-harness`,
`goal-stale-update-refuses`, `goal-stop-is-a-request-not-evidence`,
`goal-update-writes-only-existing-kinds`, `goal-expect-is-required` and
`applicable-cap-never-hides-a-deny`. What the goal needs from this file is the note scope its
reference is keyed by and the constraint rows `goal show` prints through `applicable`, both
above; the `:note` event kind and the `NOTES` grammar still fold into SPEC-WORK.md with the
notes, which is the remaining half of 5672006742 and is not claimed here.

## What this draft does not do

- **No new bus, no new scheduler, no new writer.** Notes and the goal reference are records of
  the resident work set under the one session; the route selector is #175's, built on the
  existing proposal; nothing here dispatches.
- **No enforcement claim.** This is planning (5672006742: *"not a claim that persistence or
  routing enforcement ships today"*). Until `applicable` exists and the card builder calls it,
  the filter is a coordinator reading its own notes first, and that is still the rule.
- **No second ledger** (5671991172). Usage, attempts and prices stay where SPEC-WORK.md and
  SPEC-TOKENS.md keep them; a cross-model budget view keeps its declared units.
- **No permissions and no credential mechanism.** A note grants nothing; access is
  configuration and git.
- **No transaction engine.** Atomic supersession is one envelope through the writer that
  exists; nothing is added to make it so.
- **The ROADMAP refresh** continues independently (5672006742; PR #334 landed).

## Open decisions and Rowan's decisions, for review

**Open decisions**: none. Draft 2's one — the staleness a card builder accepts from a
snapshot-served `applicable` — is closed by Stella (5673066509): eligibility is a live
evaluation of the current revision; a snapshot answer is planning only, its revision shown.

**Rowan's decisions**, collected so a reader can strike any of them without touching a
requirement of Glenn's: the content-digest id and its preimage order; notes and the goal
reference as indexes of the resident set with `:note` and `:goal` event kinds and their field
orders; group membership taken from `friend --group`; the three bounds under `:notes` and the
archive road for superseded notes; the verb names and flags; the source check as a shape
check; the `:deny`/`:prefer`/no-`:allow` constraint form and its every-axis match; task
classes reused from `:model` events; deny-over-prefer when two constraints disagree;
`unknown` for an unregistered model; `update` as a thin verb over existing event kinds, its
`--stop` the request transition and never the confirmed cancel; the replacement's kind
inherited on `supersede`; the fold location. Reads are owed from Stella, Emma and Freddy on
the exact head.
