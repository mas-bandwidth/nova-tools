# nova-work DELEGATION — coordinator notes (DRAFT 1, 2026-09-14)

**Status: a draft, Rowan's, on Stella's ask of 2026-09-14 (stella-cc8561e3fde4). Nothing here
is built.** It is planning inside the v2 recursive-node boundary of nova-tools#321 and changes
no v1 completion count. Every requirement that is Glenn's cites its source by comment id so a
reader can check the words: **5671991172** (the DELEGATION section itself, 23:03Z) and
**5672006742** (the coordinator notes inside it, 23:05Z), both on nova-tools#321. Where this
draft decides something Glenn did not say, the sentence is marked **(Rowan's decision, for
review)** and listed together at the end. This file is the DELEGATION section of
[SPEC-WORK.md](SPEC-WORK.md) (PR #231) written as a sibling while that document is under
joint authorship; when its authors take it, it folds into that file under their one-file
convention and this file is deleted, not kept beside it.

The reason this exists, in Glenn's words (5672006742): a place *"where you write your own
notes to, as informed by our conversations"*, so that *"what I just told you about how and what
to delegate"* is kept, and a coordinator does not drift back to a route he has forbidden. The
hurt it answers is the same day's: two implementation children were started on a model whose
standing instruction was coordination only, because the instruction lived in one window's
conversation and not in a place the next window reads (stella-1a9783ed1ccf).

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
notes**, which duty 1 reads before it decides and duty 2 reads before it writes a brief.

## The notes — what a note is

A note is one record a coordinator writes from a conversation or from experience, kept so
that a later window, a later model, or another coordinator can read it before routing work
(5672006742). It is data. Reading a note never executes it; a note that reads like a command
to the reader is still a record of what somebody said.

Every note carries these fields, and a note missing any of the first six is refused at write:

| field | what it holds | source |
|---|---|---|
| `:id` | a stable id, assigned by the tool, never reused | (Rowan's decision, for review): the note file's own hash-derived id, as nova-bus assigns note ids |
| `:scope` | who the note belongs to and applies to: `(:coordinator <name>)`, `(:group <name>)` or `(:node)` for the shared node | 5672006742: *"one coordinator, a configured group, or the shared node"* |
| `:author` | the participant who wrote it | 5672006742: *"keep authorship and applicability explicit"* |
| `:date` | when it was written, UTC | 5672006742 |
| `:source` | the conversation or decision it comes from: a quote of the human's words, or a pointer (a comment id, a bus note id, a commit) that holds them | 5672006742: *"preserve the source conversation/decision"* |
| `:kind` | exactly one of `instruction`, `observation`, `heuristic` | 5672006742: *"whether an entry is an explicit instruction, observation or working heuristic"* |
| `:state` | `active`, or `(superseded-by <id> <date>)` | 5672006742: *"active/superseded status"* |
| `:text` | the note itself, prose, bounded | |
| `:constraint` | optional; a structured routing constraint beside the prose, below | 5672006742: *"explicit routing constraints alongside prose"* |
| `:uncertain` | optional; what the author does not know about the decision, in words | 5672006742: *"record uncertainty rather than inventing a user decision"* |

`:kind instruction` means the human said it; its `:source` must quote or point at the human's
words, and a note whose source is the author's own inference cannot be an instruction — it is
an `observation` or a `heuristic`, whichever the author claims, and the tool does not judge
which. **(Rowan's decision, for review)**: the tool checks the shape of the source (a quote or
a pointer is present), never its truth; truth is what the reads are for.

The names inside `:scope`, `:author`, `:source` and `:constraint` are **instance data**:
they are whatever the node configured, and nothing in nova-tools knows or prefers any of them
(5672006742: *"the data model supports arbitrary participants; product rules must not hard-code
our names or model choices"*). This is also the house law that nothing in nova-tools is
specific to us. A test that mentions a model name mentions a made-up one.

## Where the notes live

Notes are files in git, under one directory the tool reads, in the work repository the node
already keeps (SPEC-WORK.md's repository; no new repository and **no new bus**). **(Rowan's
decision, for review)** the layout:

```
<work repo>/notes/coordinators/<name>.sexp    one file per coordinator; written by that coordinator only
<work repo>/notes/groups/<name>.sexp          one file per configured group; written by its members
<work repo>/notes/node.sexp                   one shared file for the node; written by any participant
```

One file per coordinator plus one shared is the minimum; groups are the configured middle
(5672006742). The directory name and the participant-to-file mapping are configuration
(`(:notes :dir "notes" :participants (...))` in the node's config, beside its other CONFIG
data), and the tool refuses a path it was not configured with rather than guessing one
(SPEC.md *Conventions*: no guessed paths). The serialization is SPEC-WORK.md's restricted
Lisp, one note per form, **append-only**: a supersede appends a new form and rewrites the old
form's `:state` field in place, and that is the only in-place edit the tool makes. A note is
never deleted by the tool. The file is small by construction: a coordinator's active notes are
the standing instructions it has been given, not a transcript.

A note's scope and its file agree, or the write is refused: a `(:coordinator A)` note lives in
A's file and no other; a `(:group G)` note lives in G's file; a `(:node)` note in the shared
file. Authorship is checked against the configured participants of that file, so one
coordinator cannot write another's notes and a non-member cannot write a group's. Whether a
coordinator may *read* another's file is the node's access rule, not this spec's — filtering
a view never establishes an access boundary (#321, *Recursive accounting and existing
guardrails*), so this draft claims no privacy for a coordinator's file that git does not give
it.

## The verbs

Four verbs, one binary, `nova-work` (SPEC-WORK.md), under the `notes` subject. **(Rowan's
decision, for review)** their names and shape; the duties are Glenn's.

```
nova-work notes write     --as <name> --scope <scope> --kind <kind> --source <text|pointer> --date <utc> [--constraint <form>] [--uncertain <text>] --text <text>
nova-work notes list      --as <name> [--scope <scope>] [--all]
nova-work notes supersede --as <name> --id <old> --source <text|pointer> --date <utc> --text <text> [--constraint <form>]
nova-work notes applicable --as <name> --task-class <class> [--candidate <role>/<model> ...]
```

**`write`** appends one note. Refused (`NOTES REFUSED`, exit 2, one line naming the field) when
`:source` or `:date` is missing (5672006742: *"preserve the source conversation/decision,
date"*); when `:kind` is not one of the three words; when the scope and the file disagree, or
`--as` is not a configured writer of that file; and when the text or constraint would grant
anything — a note *"records decisions and never create[s] credentials, permissions or
access"* (5672006742), so a constraint form has no `:allow` that widens beyond configuration,
only `:deny` and `:prefer`, below.

**`list`** prints the active notes for a scope, one line each: id, kind, date, author, the
first line of the text, and `constraint` if one is present. `--all` adds the superseded ones,
each marked with the id and date that superseded it. **A superseded note is never printed as
active**, by either verb, with or without `--all` (5672006742: *"preserve the old entry as
superseded and apply the current applicable decision"*). The output is capped and counted
(SPEC.md's cap-and-count law).

**`supersede`** appends a new note carrying the same scope, and marks the old one
`(superseded-by <new> <date>)`. The old text is kept whole. Refused when the old id is not
active; when the new note has no source or date; and when the new note's `:kind` is weaker
than the old one's — a `heuristic` or an `observation` cannot supersede an `instruction`
(5672006742: *"inferred heuristics cannot silently override explicit instructions"*). To change
an instruction, the human must have said something, and its `:source` shows where.

**`applicable`** is the read before the route. Given a task class and, optionally, the
candidate routes the caller is choosing among, it prints the active notes whose scope covers
`--as` (the coordinator's own file, every group the coordinator belongs to, and the node's
shared file) and, for each candidate, `eligible` or `excluded <note-id>`. The rule Glenn set
(5672006742): *"retrieve the applicable notes before selecting a route or constructing a job
brief"*, and *"a configured restriction on a model/role/task class must filter candidate routes
before dispatch, with a visible reason for exclusion"*. The reason is the note id, and the
note is one `list` away. What the caller carries into the brief is the constraint lines and
the note ids, not the conversation the notes came from (5672006742: *"without copying
accumulated conversation history"*).

Where the route selection of duty 1 is built (on #175 and PROPOSAL-SCHEDULING-COST.md), it
calls `applicable` first and prices only the routes that came back `eligible`; SPEC-WORK.md's
gate 2 already says *eligibility is checked before price/preference*, and this is where the
eligibility comes from. A brief constructor (nova-swarm's card, SPEC-SWARM.md) that is handed
an excluded route refuses to build the card, naming the note. **Narrative reminders alone do
not filter** (5672006742): a note with prose and no `:constraint` is printed by `applicable`
for the coordinator to read, and it excludes nothing; the coordinator who wants a route
excluded writes the constraint form.

## The constraint form

Beside the prose, a note may carry one structured constraint, so the filter above has
something to filter by. **(Rowan's decision, for review)** its shape:

```
(:constraint
  (:deny   (:model <name> ...) (:role <name> ...) (:task-class <class> ...))
  (:prefer (:task-class <class> ...) (:route <role>/<model> ...))
  (:reason <text>))
```

`:deny` names any of a model, a role, and a task class; a candidate route is excluded when it
matches every named axis (a `:deny` with `:model` and `:task-class` excludes that model for
those classes and nothing else). `:prefer` is advice to the route selector and excludes
nothing. Task classes are a configured vocabulary of the node (`coordination`, `planning`,
`review`, `coding`, `implementation`, `execution` are the ones the example below needs); an
unknown class in a constraint is refused at write, so a typo cannot open a hole. There is no
`:allow`: eligibility is what configuration and the ledger of active constraints leave, and a
note cannot widen it. Model and role names are instance data throughout.

Two active constraints that disagree are not resolved by the tool: `applicable` prints both
and marks the candidate `excluded` — a deny wins over a prefer, and a deny wins over silence —
and the coordinator supersedes one of them with a source. **(Rowan's decision, for review.)**

## The example instance — instance data, not product

The current instruction, as Glenn gave it on 2026-09-14 (5672006742, and the same words
relayed in stella-1a9783ed1ccf: *"Astra default is for coordination and thinking. Not for
coding."*). Every name in it is configuration of this node and appears in this document only
as the worked example; a test uses other names.

```
(:note (:id n-example-1)
  (:scope (:coordinator Stella)) (:author Stella) (:date 2026-09-14T23:05Z)
  (:source "nova-tools#321 comment 5672006742; Glenn, live, 22:38Z: 'Astra default is for coordination and thinking. Not for coding.'")
  (:kind instruction) (:state active)
  (:text "Astra is for coordination and high-level planning and thought only. Coding, implementation and lower-level execution go to Emma, DeepSeek swarms or a suitable local model.")
  (:constraint
    (:deny (:model Astra) (:task-class coding implementation execution))
    (:reason "Glenn, 2026-09-14: coordination only")))

(:note (:id n-example-2)
  (:scope (:node)) (:author Stella) (:date 2026-09-14T23:05Z)
  (:source "nova-tools#321 comment 5672006742")
  (:kind instruction) (:state active)
  (:text "Use small fresh contexts directly for bounded work. Compare the coordinator's and the worker's effective token prices plus context, handoff, required review and rework on the whole route; delegation at a cheaper rate can be worth it at more tokens. Keep the main session available for human communication."))
```

The first note is what the day's hurt needed: with it active, `applicable --as Stella
--task-class coding --candidate coordinator/Astra` prints `excluded n-example-1`, and a card
for that route is refused before any child starts. The second has no constraint and excludes
nothing; it is read. Glenn's own phrasing of the economics, *"cheap-token delegation may be
worthwhile even at more tokens"*, is the whole-route rule of PROPOSAL-SCHEDULING-COST.md, and
the note points at it rather than restating the arithmetic.

## What this draft does not do

- **No new bus and no new scheduler.** Notes are files in the work repository; the route
  selector is #175's, built on the existing proposal; nothing here dispatches.
- **No enforcement claim.** This is planning (5672006742: *"not a claim that persistence or
  routing enforcement ships today"*). Until `applicable` exists and the card builder calls it,
  the filter is a coordinator reading its own file first, and that is still the rule.
- **No second ledger** (5671991172). Usage, attempts and prices stay where SPEC-WORK.md and
  SPEC-TOKENS.md keep them.
- **No permissions.** A note grants nothing; access is configuration and git.
- **The shared current goal** (5672006742, *"Shared current goal across models"*: set,
  retrieve and update the current goal with a stable identity and revision, cross-model and
  cross-harness, with conflict handling) is a requirement of the same comment and is **owed
  separately**. It belongs in SPEC-WORK.md's work set as a goal record, not in a notes file;
  this draft only says that the applicable notes are among what a goal's reader loads. It is
  named here so it is not lost, and it is not specified here.
- **The ROADMAP refresh** continues independently (5672006742; PR #334 landed).

## Rowan's decisions, for review

Collected so a reader can strike any of them without touching a requirement of Glenn's:
the id scheme; the three-file layout under `notes/` and its config key; the verb names and
flags; the source check as a shape check; the `:deny`/`:prefer`/no-`:allow` constraint form and
its every-axis match; deny-over-prefer when two constraints disagree; the task-class vocabulary
as node configuration. Reads are owed from Stella, Emma and Freddy on the exact head.
