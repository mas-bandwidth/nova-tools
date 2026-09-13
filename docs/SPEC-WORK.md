# nova-work — specification (DRAFT 11, 2026-09-13)

**Status: a draft under joint authorship, Rowan and Stella, on Glenn's word of 2026-09-13.**
Nothing here is built. The Schema NEW Fixed Tables roadmap is the pilot, and the pilot decides
what this document keeps. Sections marked *(Stella)* are hers; sections marked *(Rowan)* are
mine; the rest is shared. Every requirement that is Glenn's cites its source so a reader can check the words: a
nova-tools#177 comment by id, or one of the four bus notes in which Stella reports his live
words of 2026-09-13 — stella-5adca9a1f09d (resident S, periodic clips, verbs for structure),
stella-deed78e54cb4 (link versus absorb, the initial import that loses no data),
stella-0ace603bdc22 (one coordinator, one live reader/writer), stella-fe600103fa2e (*"the
work set is the PRIMARY FORM"*, superseding 5653982211's hesitation), and Stella's two reviews, stella-ff217e98685c (five
points on draft 1) and stella-d205f6120ee7 (two findings on draft 5). Where a later word
supersedes an earlier comment, the later word governs and the earlier is named. **Two things in this document are the authors' proposals and not Glenn's requirements,
and they are marked where they stand: the lease model (Rowan's, from #177 comment 5654176537)
and everything in the section *Additions of the authors'*.** Draft 5 integrates Stella's
resident-session amendment (her 9757de0, on Glenn's clarification after draft 3: *the
coordinator loads S from the work repository, manipulates it in memory through nova-work
verbs, and periodically clips to Git; reloading and reparsing on every verb defeats the
purpose; both structure and state need verbs*) with the data, counting, query and validation
contracts of drafts 1 to 4, which three Fable cold reads shaped (HOLD at 15:34Z, 15:47Z and
15:49Z, all recorded on PR #231 with their repairs).

**One recursive work set, S.** Restricted Lisp data holds what is **desired** (the work:
repositories, streams, features, tasks, down to whatever depth is useful) and what is
**observed** (events: structure changes, transitions, evidence, attempts, leases, heartbeats,
scope changes). Everything **derived** — a task's current state, counts, percentages, views,
plans — is computed and cached in memory and never written as authority; a roadmap table, an
owner queue, a stream report and a percentage are each a **projection** of S at a named scope
revision, and none of them is a second store. `ROADMAP.md` is regenerated, never edited; a
hand-typed percentage is a bug (5653970526). **S is the primary form, in a persistent versioned repository** (Glenn, 2026-09-13, via
stella-fe600103fa2e: *"the work set is the PRIMARY FORM"*, which supersedes #177 comment
5653982211's *evaluate … only after … demonstrated*). What 5653982211 listed — lossless
import/export, stable identity mapping, provenance retention, restart/replay and
reconstruction — is kept as the gate on **migration**, not on the requirement: his own rule for
the initial import is *"We must not lose data"* (stella-deed78e54cb4), and Stella's migration
section below carries it. Nothing in this draft migrates or imports anything.

This spec is normative once it leaves draft. It is a sibling of [SPEC.md](SPEC.md), whose
**Conventions** — exit codes, no guessed paths, the one-line guarantee, the field escape, the
cap-and-count law, the version line — govern here unchanged. If the code and this document
disagree, one of them has a bug, and the tests decide which.

## The failures it closes

| the failure, from the record | what closes it |
|---|---|
| a task disappears after a context loss, a priority change or a handoff (#177) | S is resident, journaled locally on every accepted mutation, and clipped to the repository; a node's `:id` is stable through every rename; every change is an event with an author and a stamp |
| the work set reparsed on every command, so studying S cost a parse per question (Glenn, via Stella 15:49Z) | one load per session; verbs act on resident objects; an unchanged indexed query costs zero parses and zero replays |
| two workers on one task, neither aware of the other (nova-board's 2026-09-10 morning) | one live **lease** per node; a second `take` is refused and names the holder; there is one coordinator and one live S, so the refusal is authoritative (Glenn, via Stella 15:53Z) |
| a stored owner read as "working on it" while nothing moves | *responsible* and *working* are two facts: `:responsible` is durable accountability set by a person's word; *working-now* is a heartbeat inside a window (5654012267) |
| a percentage no evidence supports; an average of fractions rounded to green (5653970526) | done needs evidence bound to the task's acceptance criteria and verified; language completion is green cells / applicable rows; cell progress shows its numerator and denominator |
| the denominator moved and nobody saw it | every event that changes a required set increments the scope revision; a baseline is an event; additions append at the bottom; removals carry a reason; `percent` prints the baseline row count beside the current one (5654160320, 5653990830, 5649089106) |
| an old attempt's result quietly satisfying a corrected task (5653982211) | a task carries a `:generation`; a `correct` event bumps it; evidence of an older generation cannot close the task |
| studying S became pairwise work (5653973972, 5654049969) | counted containment is a forest, references are a graph; a fold visits each node and edge once over indexes built at load and updated incrementally; no transitive descendant sets are materialised; evidence fetching is a separate, bounded pass |
| a deferral or a cancellation counted as progress | `:deferred`, `:cancelled` and `:superseded` are scope events; they never enter the done count and the baseline denominator stays printed |
| the roadmap table edited by hand and the data left behind | `render --check` fails on drift; the table lives between two markers and S owns it |
| a hand-written change to structure with no record of who made it or why | structure is changed only by verbs, each an event with an author, a stamp and a reason |

## The execution model *(Rowan, on Stella's amendment; her sections below govern where they say more)*

**There is exactly one active coordinator and one live reader/writer of S** (Glenn, via Stella
15:53Z: *"there must be one coordinator at a time. One reader/writer on the work set S in
memory via nova-work"; "Otherwise, we have races"*). Friends and workers submit results, evidence
and requested changes to that coordinator; they never open a second live S; other readers read
published, revision-labelled snapshots. **The mechanism that makes the rule hold across benches is proposed for the pilot, on the
substrate we already trust, and it fences MUTATION, not only publication.** The branch that
holds S carries an ownership record (`OWNER`: the coordinator's name, a **generation**, a
**token** drawn at random when the generation was taken, the stamp it was taken, and
**`until`**, the stamp the ownership lease expires). The token is written in two places and
nowhere else: the `OWNER` record on the branch and the taking session's own journal, so
**only a process holding that journal can resume the generation**; a second session under
the same name on another bench holds no journal with the token and is a taker, not a resumer.
**Two processes cannot hold one journal**: the session socket at `--session <path>` is also
the journal's lock, created exclusively at start, and a start that finds a live socket there
refuses (exit 1, naming the pid); a dead socket (no process answers) is removed and the
start proceeds as a resume. So the resume predicate is: the record names me, my journal holds
its token, and I hold the journal's lock (Stella's finding 1, comment 5654659093). Three rules:

1. **Taking.** `session start` fetches the tip, reads `OWNER`, and takes ownership only if the
   record names nobody, or its `until` plus `--skew` is in the past, or it names this session
   and this session's journal holds the record's token (that is a resume: same generation, no
   bump, `until` advanced); it then pushes one
   fast-forward commit that bumps the generation and sets `until = now + 2 × --every`, using
   a compare-and-swap push (`--force-with-lease=<branch>:<tip read>`: git refuses the push if
   the tip moved; **no history is ever rewritten** — the flag is the CAS, not a force). A
   refused push, or an `OWNER` whose lease is live and names another, is exit 1 naming the
   owner, generation and `until`, and the session never activates. A restart without the token waits like anyone else.
2. **Holding.** Every `--every`, the owner fetches the tip and **reconfirms** that `OWNER`
   still carries its generation and token, then pushes a fast-forward commit advancing
   `until` the same way. **The session's base is the sha of the last commit it pushed**, a
   reconfirm as much as a clip, so its own reconfirms never read as divergence; divergence is
   a tip this session did not write. **An owner that cannot reconfirm before its `until` fences itself**: it refuses every
   write AND every read (exit 1, `fenced`, naming its generation and the tip's if known),
   because a fenced session's resident S may be behind a new owner's and an answer from it
   would be a stale answer wearing a live one's clothes; it keeps its journal. Offline or
   partitioned, it fences at `until` without any network at all. So at no instant do two
   sessions accept mutations: the old owner is fenced by its clock at `until`, and a takeover
   is refused until `until` plus `--skew` has passed on the taker's clock. The bound this
   rests on is stated: the benches' clocks agree to within `--skew <duration>`, and the
   reconfirm cadence is `--every`. A fenced owner that later reconfirms successfully (its
   generation and token still on the tip, nobody took) unfences and continues; one that finds
   another generation stays fenced and exports.
3. **Publishing.** Every clip carries the generation and is pushed the same CAS way; a late
   clip from a fenced owner is refused by the moved tip. A friend's request that reaches a
   fenced session is refused, not queued.

What this does not do, said plainly: it cannot make a fenced session's accepted-but-unclipped
events shared; they are recovered by the export path below, never lost and never merged. What
it does do is what Glenn's rule requires: one live reader/writer at a time, by the clock and
the branch together, on no backend but git.

A **session** is that coordinator's supervised, long-lived process, and it owns the one S: it loads the snapshot at the
fetched tip once (`--file` is the snapshot's path inside `--repo`, read at that tip, never a
free file), replays its own journal beyond that snapshot once (recovery, and the only replay),
validates the set whole, builds the indexes, and answers verbs against its resident objects
thereafter; `--at` answers from the retained history in memory. **The CLI is a thin client**: `nova-work <verb> --session <path>` sends the verb to the
session listening at that path (a Unix socket the session creates at start; no default path)
and prints its one-line answer; a fresh CLI process is never a fresh parse. A reader who is
not the coordinator reads a published snapshot with `--snapshot <path>` in place of
`--session`, read-only, and its answers carry the snapshot's revision. The client is Go under
this repository's conventions; the session's own language is the pilot's decision (Stella:
*trusted implementation code may be Lisp*), and the version line is the client's; a session in another language answers `session
status` with its own `SESSION OK … build=<identity>` field, so every running binary says
which build it is. A session's identity, bounds, journal path, base revision and clip cadence are
explicit at start and readable at any time (`session status`), and it is stopped explicitly;
no always-on daemon is required, and a supervised session that a coordinator starts for a
sitting and stops at its end is enough (Stella, *Keep the work set alive*).

Every mutation is one typed event and carries a **request id**: `--request <id>` on every
mutation verb, drawn by the caller (a friend's request arrives with one), or drawn by the tool
and printed on the `OK` line when absent. The session validates the event against the current
local revision over the resident S as it would be with the event applied, appends it with its
request id to the **local recovery journal**, acknowledges only after the journal is durable,
then applies it to the resident objects, updates the affected indexes and invalidates the
affected derived values. A failed validation changes neither S nor the journal. A retry with a
request id the journal already holds is answered with the original `OK` line and applies
nothing, which is how a crash between durability and acknowledgement yields one event
(Stella, *Accept locally, then clip into Git*).

**The repository branch that holds S has one writer too: the active coordinator.** A **clip**
and the ownership commits of rules 1 and 2 above are the only writes to it, and a clip: it names a local event boundary, fetches the upstream revision, and
**refuses if upstream is not the session's base** — a moved upstream means a hand edit or a new
owner's take, and either is a handoff or a reload, never a merge — then validates
the resident S whole, writes one deterministic snapshot carrying the structure and the
retained event history, commits and pushes under `--git-timeout <seconds>` with `--attempts
<n>` (default 25 as the bus's; what an attempt retries here is the CAS push after a fetch
shows the tip unchanged but the push raced the same owner's own reconfirm commit, the one
moving-remote case this design allows), and records the shared revision and which local events it contains. A failed push leaves
accepted local work and the pending clip intact and reports *locally durable, not shared*; a
divergence prints the upstream sha and the session's base and stops; no history is ever
rewritten (the CAS push above only fast-forwards) and nothing is reconciled. A session stop and a coordinator handoff request a clip under the
same flags, so a stop is bounded by the same timeout and budget. **A session whose clip is
refused by divergence is fenced**: it accepts no further mutations (exit 1, `fenced`), keeps
its journal, and `session export --into <path>` writes its accepted events since its base as a
request bundle — each event with its request id, expected revision and payload — which the
active coordinator applies with `session replay --from <path>`, one request at a time,
validated fresh against the live S, refusing the stale ones by `--expect` and reporting each
verdict on its own line. Nothing is lost; nothing is merged without validation; the fenced
session's unshared work is a file, not a claim. `render` writes a working-tree
file that the next clip commits; it is not a second write path to the branch.

## The data *(Rowan)*

**Restricted Lisp, read as data.** A work file is a sequence of s-expressions made only of
lists, keywords, strings and integers, with `;` line comments, which the reader discards. The
reader refuses, at exit 2 with one line naming the byte offset, every dispatch macro (`#.`
first among them) and every other form the source forbids as evaluation (5653982211); the
full list of refused syntax is an authors' addition, listed at the end. Nothing read is ever
evaluated. **Every verb that reads a file takes the same three bounds**, `--max-bytes <n>
--max-depth <n> --max-nodes <n>`, none defaulted: a file past any of them is refused at exit 2
before parsing finishes, and a missing bound is `refusing to guess`. Unknown keys on a node
are preserved and ignored, so a team may carry its own fields; an unknown `:type` is a
refusal, because a type names the rules a node is checked by (5653990830).

**One resident set, one journal, one snapshot.** S lives in memory in a supervised session (the
execution model, below): its **structure** — nodes, `:id`, `:type`, `:children`, `:deps`,
`:acceptance`, `:responsible`, axes and cells — and its **log** — every event below — are both
held there, both changed only through typed verbs, and both written to the repository only by a
**clip**, as one deterministic snapshot carrying the structure and the retained history. Nothing
in the normal path is hand-written; the snapshot is data a person may read, and a hand edit
to it is a maintenance act outside the verbs, loaded and validated whole by the next session
start, never the way structure changes in use. Every mutation is an event: a structural
verb appends a structure event and a scope event where a required set changes, so every
transition is kept rather than overwritten (#177: *preserve transitions and corrections rather
than overwriting the history*).

**A node.**

```lisp
(:id "schema/fixed-tables/versioning/cpp"   ; stable, never reused, never carries display text
 :type :work-set                             ; the kinds are listed below
 :title "C++ versioning"                     ; display only; may change freely
 :children ("schema/cpp/read-older"          ; COUNTED CONTAINMENT: this node owns these
            "schema/cpp/refuse-newer")
 :deps ("schema/shared/lock-rules")          ; REFERENCE: needed, not owned, not counted here
 :responsible "emma"                         ; durable accountability, set by a person's word (an event records who set it)
 :category "feature"                         ; free label; taxonomy TBD (5654164074)
 :links ("https://github.com/mas-bandwidth/schema/issues/898"))   ; an issue is a link, never a type
```

**Containment and reference are two different edges, and the difference is the whole cost
model** (5654049969). `:children` is canonical containment: every node has at most one
containment parent, the containment edges form a forest, and a node is **counted once**, under
that parent, wherever else it is referenced. `:deps`, a roadmap cell's `:ref`, a view's
membership and any other pointer are references: they form a graph, they carry no count and no
cost, and they are validated for existence and for dependency state. Shared compiler and lock
work in Schema is owned once, under an explicit owning work set that every repository carries
(`<repo>/shared`, visible in every listing, 5654164074), and referenced from nine cells; it is
one task with one cost. Stella's *A cell is a reference, not another state store* below says
the same rule from the roadmap's side and is not restated here.

**Kinds.** Features, tasks, attempts and leaf subtasks are distinct units (5654012267), so
they are distinct kinds:

- `:work-set` — a container. Its completion is its required children's completion; an empty
  required set is **never** done.
- `:feature` — a work-set whose completion is what a roadmap row counts; the unit of "green
  feature cells / applicable rows".
- `:roadmap` — a typed view over its cells: `:axes` (ordered, named members), `:cells` mapping
  a coordinate to a `:ref`, `:scope-revision`, `:source-revision` (the tree the evidence was
  read against), `:completion-policy` (`:all-required-features` is the only policy in this
  draft). A cell references a node; it never contains state of its own. An unknown axis member, a
  duplicate coordinate and a missing required cell are refusals; an omitted cell is never
  complete (5653990830). A cell may be marked `:out-of-scope` by a recorded scope event, which
  is distinct from unstarted and from unknown, and **an out-of-scope cell leaves that axis
  member's applicable rows** (5654160320: *fully green features / applicable features*).
  Adding an axis member or a feature is a scope revision; removing one is not completion.
- `:task` — work with `:acceptance`, a list of the criteria that close it, **one schema**:
  `(:id "c1" :kind :test :subject "test:internal/lockfile/TestLockRule1@<rev>" :predicate
  :passes)`, where `:kind` is `:test`, `:job`, `:merged` or `:attested`, `:subject` names the
  exact thing the evidence must be about (a test name, a job name, a PR number, or for
  `:attested` the criterion text a reviewer signs), and `:predicate` is what must be true of it
  (`:passes`, `:succeeds`, `:merged-at`, `:attested-by`); `node add --acceptance` takes exactly
  this form, and an evidence pointer qualifies a criterion only when its kind matches, **its
  subject is the criterion's subject** (a passing test of another name qualifies nothing), its
  predicate holds at the named revision, and its `:generation` is the task's current one, so a
  `correct` event makes every earlier qualification void for the done claim (Stella's finding
  2, comment 5654659093); `:required` (default true), and a current state, generation,
  evidence set and blocked reason **all derived from its events**. A task with no `:children`
  is a **leaf subtask**, the unit `unit=leaves` counts; a task with children is counted by its
  leaves, never itself; so the four units of 5654012267 are `:feature`, `:task`, the leaf
  `:task`, and the `:attempt` event. A node may carry `:private true`; `render` never writes
  a private node or its descendants into an output file, and a public view that reaches one
  through a parent prints `private=<n>` and nothing of it (5653982211). A task has no stored worker; who is working
  on it is answered from leases only; who is responsible is `:responsible`, inherited down the
  containment forest until overridden.
- `:lease` — the ownership-of-execution record, below. *(The authors' proposal, not Glenn's.)*
- `:event` — the log. Every event carries `:kind`, `:node`, `:by`, `:stamp`, `:clock` (`:tool`
  or `:given`), `:request` (the request id), `:generation-owner` (the coordinator generation
  that accepted it), and the fields its kind needs:
  - `:transition` — `:to <state>`, `:reason`, and for `:blocked` a `:blocked-by` reference; a
    `:to :done` names the evidence event ids it stands on.
  - `:evidence` — `:pointer`, `:criterion` (an `:acceptance` id on the node), `:against <sha>`
    (the tree it was read against), `:generation` (the node's, at the time of writing),
    `:attempt` (optional).
  - `:attempt` — `:model`, `:bench`, `:started`, `:ended`, `:result` (a pointer), `:usage` (a
    pointer to a token record, #181), `:generation` (the task generation it answered).
  - `:correct` — a correction to a task: `:reason`; bumps the task's `:generation`
    (5653982211).
  - `:review-attest` — a reviewer's attestation that a result satisfies an `:attested`
    criterion: `:criterion`, `:result` (a pointer), `:against <sha>`, `:generation` (the
    node's, at the time of writing), `:by` the reviewer. **It is an evidence event**: it
    carries `:pointer` = its `:result` and is what a `:to :done` names for an `:attested`
    criterion, under the same generation rule as every other evidence event.
  - `:responsible` — `:to <name>` on a work-set or feature, on a person's word, `:reason`.
  - `:lease`, `:heartbeat`, `:release`, `:handoff` (carrying the new holder's `:deadline` and
    `:default`, so a handed lease is a whole lease) — the lease log, below.
  - `:baseline`, `:discovery`, `:remove`, `:defer`, `:cancel` (carrying `:evidence` that the
    worker stopped), `:reopen`, `:split`, `:supersede`, `:scope` — the scope log: a baseline records the required set of its node **as the tool
    computed it at that moment**, member by member; a split names the new children; a
    supersede names `:by`; every one of them increments the node's scope revision, because
    every one changes a required set. Splitting is decomposition, not discovery or completion
    (5653970526).

**States and transitions, derived.** A task's current state is the `:to` of its newest
transition, where **a scope event of kind `:defer`, `:cancel`, `:reopen` or `:supersede` is
also a transition for its node** (to `:deferred`, `:cancelled`, the reopened state, or
`:superseded`), or `:unknown` when it has none; `:unknown` is explicit and is neither
zero nor not-started (5653970526). Its generation is the count of its `:correct` events. The
allowed transitions are a table the validator holds: from `:todo` to `:doing`, `:blocked`,
`:cancel-requested`; from `:doing` to `:blocked`, `:review`, `:done`, `:cancel-requested`;
from `:blocked` to `:doing`, `:cancel-requested`; from `:review` to `:doing`, `:done`;
from `:cancel-requested` to `:doing` (withdrawn) or, by a `:cancel` scope event carrying
evidence that the worker stopped, to `:cancelled` (5653982211: *cancellation requests are
distinct from a confirmed stopped worker*); from `:unknown` to any non-terminal state by a
transition that carries evidence or a reason. **`:deferred`, `:cancelled` and `:superseded`
are never targets of `state`**: they are entered only by their scope events (`defer`,
`cancel`, `supersede`), which record the transition and move the revision; `:done` and
`:deferred` are left only by a `:reopen` scope event, which is itself the transition;
`:cancelled` and `:superseded` are terminal. A
`:to :done` transition must name evidence events whose criteria cover every `:acceptance`
entry of the task, and every evidence event carries the task's `:generation` at the time it
was written; a `:to :done` naming evidence of an older generation is refused, whether or not
an attempt is named (5653982211). A required task with no `:acceptance` entry can never be
done, and rule 16 names it. A transition to `:blocked` without `:blocked-by` is
refused.

**Evidence is a pointer the validator can fetch, bound to a criterion.** Resolving a pointer
proves that the thing exists; **the criterion it names is what it proves** (Stella, 15:38Z,
point 1), so an evidence event carries both, and an evidence event whose criterion is not an
`:acceptance` entry of its node is refused. This draft ships five schemes — `commit:<sha>`,
`run:<owner/repo>#<id>`, `pr:<owner/repo>#<n>@<sha>`, `file:<path>@<sha>`,
`test:<package>/<name>@<sha>` — and a sixth, `note:<scheme>:<id>`, for any team's message
store, so that no family's bus is named in the tool (5653970526). **A `note:` pointer is
accepted on a heartbeat and on an attempt's `:usage`, never as evidence for `:done`**
(5649089106: *status messages are not completion evidence*); rule 5 refuses it. **Resolution is a separate
pass from validation, and resolving is not qualifying** (Stella, stella-ff217e98685c points 1
and 2, and stella-d205f6120ee7 finding 1): `verify` marks a pointer *verified* only when the
thing it names **qualifies for the criterion's kind** — a `:test` criterion by a `test:` pointer
that passed at the named revision; a `:job` criterion by a `run:` pointer whose named job
succeeded at the named revision; a `:merged` criterion by a `pr:` pointer merged at the named
sha; an `:attested` criterion only by a `:review-attest` event naming the reviewer, the
criterion, the result pointer and the revision (a `note:` or a bare `commit:`/`file:` never
qualifies anything by itself, and a green aggregate run never qualifies a whole feature:
5649089106, *CI activity and status messages are not completion evidence*). Any other pairing
is *found-not-qualifying* and counts as unverified; a pointer the fetch cannot reach is
*unreachable* and counts the same. `check` never fetches;
`verify` fetches, under
`--max-fetch <n>` and `--fetch-timeout <seconds>` (both required, as SPEC-BOARD requires
`--gh-timeout`), through a cache at `--cache <path>` (required; no guessed path) keyed by
pointer and carrying its verified-at stamp; `verify --offline` reports cached verdicts and
fetches nothing. **A pointer that
does not resolve marks that evidence event `unverified` and changes no task's recorded
state** — the log is never rewritten by a fetch — **but no count is ever green on unverified
evidence**: in every rollup a `:done` whose evidence is not all verified (by `verify`, through
the cache named on the read) counts as `unknown`, never as done, and the answer prints
`done-unverified=<n>` beside `done=<n>` (5653970526: *report unknown or a clearly labelled
verified lower bound*; 5654176537 rule 4). The same holds for **stale** evidence, which is
local and needs no fetch: an evidence event whose `:against` is not the cell's
`:source-revision` is stale, `check` counts it (`stale=<n>`), and a `:done` standing on it
counts as `unknown` in every rollup (5653970526: *source changes can invalidate old proof*).

**The lease** *(Rowan's proposal, #177 comment 5654176537, changed here from that comment in
three ways: expiry is derived and never stored, so `:expired` is not a state; an expired lease
is a count and never a finding; `:extend-once` is defined)*. A lease is an event; its current
state is derived from its heartbeat, release and handoff events.

```lisp
(:kind :lease :id "l-3f9a1c0e7b2d"            ; the tool's own id, random hex, never a count
 :node "schema/fixed-tables/versioning/cpp"
 :by "emma" :stamp "2026-09-13T14:41:11Z" :clock :tool
 :deadline "2026-09-13T21:00:00Z"
 :default :release)                  ; :release | :extend-once | (:escalate "<name>")
(:kind :heartbeat :lease "l-3f9a1c0e7b2d"
 :by "emma" :stamp "2026-09-13T15:02:00Z" :clock :tool
 :evidence "note:bus:emma-841138a3b056")
```

A lease never expires into done. **Expiry is derived**: a lease whose deadline is behind the
read's clock, with no release or handoff, reads as expired in every answer, the node reads as
unowned for execution, `check` counts it (`expired=<n>`), and the responsibility on the node
is untouched (Stella, point 4: *expiry ends the claim, not accountability or evidence*).
`:extend-once` means: at the first expiry the lease reads as extended by the same length,
once, and a `stale` answer says so; at the second it reads as expired. Working-now means a
heartbeat inside `--window`; a live lease with no heartbeat in the window is *held, not
worked*, and the answer says so. One live lease per node; a handoff is a `:handoff` event
naming the new holder, so the transition is a record and not an overwrite. A lease with no
`:deadline` or no `:default` is refused at write time: a deadline with no default is a wait
with no end.

**The root.** Every top-level child of S is a repository work set, `(:type :work-set :repo
"<owner>/<name>")`, including repositories that hold research, planning or a friend's own
work; a cross-repository goal is a view over canonical owned nodes, never a copy
(5654164074, Glenn's preferred simplification, *to be prototyped before it is an invariant*).
S is the team's authorized known work, never a scan of every reachable repository. A GitHub
issue or PR is a `:links` entry on a node, not the node's type; intake from issues is in
Stella's section.

**Scope, baseline, focus** (5654160320). A roadmap or work set carries a scope revision,
derived: the count of its scope events. The first `:baseline` event records the initial
membership before execution; later discoveries **append at the bottom of every rendered
listing in discovery order**, reported as *added since baseline*, and reprioritising execution
never reorders the baseline rows; a removal carries a reason and never lowers a count
silently; a split that changes the leaf count prints both units (Stella, point 5). **Focus**
is a query (a node id, a repo, a category, an owner, a revision), not a copy: it selects a
subtree or a membership view over the same nodes, so identity, dependencies, responsibility
and evidence are the same in every view; a dependency outside the focus that blocks it is
reported on the row (`blocked-by=`), and `--at <revision>` answers as of a past scope
revision by replaying the log to it (5653982211: *views filter by … scope revision*).

**Clocks.** Every event carries `:stamp` and `:clock`. By default the tool reads its own clock
and records `:clock :tool`; `--now <stamp>` is optional and records `:clock :given`, for
replay and tests (Stella, point 3). Reads take the same `--now` for the same reason.

## Counting *(shared; Glenn's rules verbatim where they are his)*

- **Language completion** on a roadmap = `100 * green feature cells / applicable feature
  rows` for that axis member, where applicable rows are the active rows less those the axis
  member has a recorded out-of-scope event for. *Partial cells do not contribute fractions of
  a completed feature to this number* (5653970526). `percent` prints `green=<k> rows=<n>
  baseline-rows=<n0>` so a new denominator is visible beside the old (5649089106).
- **Cell progress** = completed required leaves / required leaves, printed as `k/n`, never as
  a lone percentage. A parent is green only when every required child and every dependency
  gate is satisfied. *Do not average nested percentages, round 99.9 to green, treat an empty
  checklist as done, or count one shared leaf repeatedly within a cell* (5653970526).
- **Completion of a focus** = completed required work / current required work, with the
  baseline denominator kept beside it for expansion and contraction (5654160320). **Current
  required work excludes `:deferred`, `:cancelled` and `:superseded` leaves** and `remaining`
  prints them under `deferred=<n>`, `cancelled=<n>` and `superseded=<n>`, kept apart, so the
  subtraction is visible; the baseline denominator
  still counts them. **Active rows** of a roadmap are its baseline rows plus discovered rows,
  less rows removed or superseded by a scope event; **a deferred row stays active and stays
  in the denominator, because it is not done** (Johnny Grok's HOLD on draft 9, bus note
  johnny-e51960925453: deferring an applicable row must not raise green/rows); **applicable
  rows** for an axis member are the active rows less those with an out-of-scope cell for that
  member, and every change to `rows=` is a scope event with an author and a reason.
- **Units are labelled** on every line: `unit=features` or `unit=leaves`; a comparison never
  changes unit silently.
- **Future cannot lower the active percentage; a deferred item cannot raise the completed
  count** (5653970526). Both are tests.
- **Unknown is a count of its own**, printed on every answer; `done-unverified=<n>` is the
  part of `unknown=` that is a recorded done standing on unverified or stale evidence, so the
  two are never added.

## Queries — the contract *(Rowan; Glenn's list from 5654012267)*

Every answer is computed whole before anything is printed, then printed as one `QUERY OK`
scope line and one `QUERY ROW` line per fact, capped and counted. The scope line is the `QUERY OK` line of the grammar below: scope revision, membership rule,
unit, source sha, freshest evidence stamp, `done=`, `done-unverified=`, `unknown=`, `deferred=`,
`stale=`, `rows=`, `shown=`, `parses=` and `emitted=`; `percent` adds `green=` and
`baseline-rows=`.

| ask | answers |
|---|---|
| `done --node X` / `remaining --node X` | completed and outstanding required work under X, by kind, capped and counted |
| `who --node X --window <dur>` | live leases on X and beneath it: holder, heartbeat age, deadline, default; then `held-not-worked` and `unowned` counts; and `responsible=` for X |
| `percent --node R --axis <member>` | the roadmap rollup for one axis member, with `green=<k> rows=<n> baseline-rows=<n0> done-unverified=<n>` and every partial cell's `k/n` and `unknown=<u>` |
| `size` / `size --node X` | total required leaves, done, unknown, unverified, deferred, cancelled, superseded, since-baseline |
| `stream --repo <owner/name>` / `--owner <name>` | the same, for one repository or one friend's own selected work, plus `responsible=` and live lease count (5654012267: *ownership for a named stream*) |
| `under --repo <owner/name> --category <label>` | compact listing of nodes by category with state (5654164074; taxonomy TBD) |
| `stale --window <dur>` | leases past deadline or past the heartbeat window, grouped by holder |
| `handoffs --since <revision>` | the lease transition log |

## The verbs *(Rowan; a draft shape, to be cut by the pilot)*

Session verbs run the process; every other verb is a client verb addressed to a session by
`--session <path>` (required; no default), or, for `check` and `query` only, to a published
snapshot by `--snapshot <path>` with the three bounds, read-only; under `--snapshot` the
verification cache is still named by `--cache <path>`, so a snapshot reader sees the same
verdicts the coordinator last fetched. **Every mutation verb takes `<write flags>` = `--as
<name> [--request <id>] [--expect <revision>] [--now <stamp>]`**: the request id is drawn by
the tool and printed when absent; `--expect` is the caller's expected local revision, optional
on the coordinator's own verbs and **required on every request in a `session replay` bundle**;
a request whose expectation is stale is refused at exit 1 naming the current revision
(5653982211: *apply rejects stale preconditions*). How a friend on another bench submits a
request is the bus: a request bundle is a file a note carries, and `session replay --from` is
its intake, so no second transport is invented here. Every client verb that lists takes `--max
<n>`, default 20, `0` means all, negative refused (SPEC.md, the cap-and-count law). Every
duration comes from a flag: `--window` is required by `who` and `stale`, `--by` by `take`,
`--every` by `session start`. `--now <stamp>` is optional on every verb and records `:clock
:given`; absent, the session's clock is used and recorded as `:clock :tool`.

```
nova-work session start  --session <path> --as <name> --file <path-in-repo> --journal <path> --cache <path> --repo <path> --remote <name> --branch <name>
                         --max-bytes <n> --max-depth <n> --max-nodes <n> --every <duration> --skew <duration> --git-timeout <seconds> [--attempts <n>] [--max <n>] [--now <stamp>]
nova-work session export --session <path> --into <path>
nova-work session replay --session <path> --from <path> --as <name> [--max <n>]
nova-work session status --session <path>
nova-work session stop   --session <path> --git-timeout <seconds> [--attempts <n>] [--no-clip]
nova-work clip           --session <path> --as <name> --git-timeout <seconds> [--attempts <n>] [--max <n>] [--now <stamp>]
nova-work check          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) --cache <path> [--max <n>]
nova-work verify         --session <path> --max-fetch <n> --fetch-timeout <seconds> --cache <path> [--offline] [--node <id>] [--max <n>]
nova-work query          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) --ask <kind>
                         [--node <id>] [--repo <o/n>] [--owner <name>] [--category <label>] [--axis <member>]
                         [--since <revision>] [--at <revision>] --cache <path> [--max <n>]   (who and stale: --window <duration>, required)
nova-work render         --session <path> --view <roadmap-id> --into <path> --start <marker> --end <marker> [--at <revision>] [--check]
nova-work node add       --session <path> <write flags> --id <id> --type <kind> --under <parent-id> [--title <text>] [--category <label>] [--required <true|false>] [--acceptance <id:kind:subject:predicate> ...] --reason <text>
nova-work decompose      --session <path> <write flags> --node <id> --into <id,...> --reason <text>
nova-work dep            --session <path> <write flags> --node <id> (--add <id> | --remove <id>) --reason <text>
nova-work axis           --session <path> <write flags> --roadmap <id> --axis <id> --add <member> --reason <text>
nova-work cell           --session <path> <write flags> --roadmap <id> --coord <member,member> (--ref <id> | --out-of-scope) --reason <text>
nova-work responsible    --session <path> <write flags> --node <id> --to <name> --reason <text>
nova-work take           --session <path> <write flags> --node <id> --by <duration|stamp> --default <release|extend-once|escalate:<name>>
nova-work heartbeat      --session <path> <write flags> --node <id> --evidence <pointer>
nova-work release        --session <path> <write flags> --node <id> [--handed <name> --by <duration|stamp> --default <release|extend-once|escalate:<name>>]
nova-work attest         --session <path> <write flags> --node <id> --criterion <id> --result <pointer> --against <sha>
nova-work attempt        --session <path> <write flags> --node <id> --model <name> --bench <name> --result <pointer> [--usage <pointer>]
nova-work evidence       --session <path> <write flags> --node <id> --pointer <pointer> --criterion <id> --against <sha> [--attempt <id>]
nova-work state          --session <path> <write flags> --node <id> --to <state> (--evidence <event-id> ... | --reason <text>) [--blocked-by <id>]
nova-work correct        --session <path> <write flags> --node <id> --reason <text>
nova-work event          --session <path> <write flags> --kind <baseline|discovery|remove|defer|cancel|reopen|split|supersede|scope> --node <id> --reason <text> [--by-node <id>] [--children <id,...>] (cancel: --evidence <pointer>, required)
nova-work version
nova-work help
```

**What a mutation does, stated exactly.** `node add`, `decompose`, `dep`, `axis`, `cell` and
`responsible` are structure verbs: each appends one structure event and, where it changes a
required set, one scope event, **as one request envelope**: one journal record holding both,
written all-or-none, replayed all-or-none, answered with one `OK` line, and answered again
with the same line on a retry of the same request id, so a crash between the two can never
leave the tree changed with the denominator and revision unchanged (Stella, 16:00Z, finding
2). `decompose` is a `split` (decomposition, never discovery or completion). `take`,
`heartbeat`, `release`, `attempt`, `evidence`, `state`, `correct` and `event` append one event
each. **The gate is one validation, of the resident S as it would be with the event applied**:
the session evaluates the structural rules over the affected nodes and either journals and
applies the event unchanged or refuses at exit 1 with the finding's line and changes nothing.
A red S elsewhere still refuses, because a red set is stopped, not written around; `check`
says where. `plan`, `apply` and `reconcile` (5653982211, the Terraform half) are **not in
this draft**: named here so a reader knows they are deferred, with their own section once the
pilot has shown what a plan must name.

**A lease is authoritative the moment the one coordinator accepts it**, because the ownership
record above admits one live S; `pushed=<rev|->` on its answer says only which clip carried it to the branch, which is
durability, not exclusivity. A request to take a node
arrives at the coordinator from a friend as a mutation request with a stable request id and
the friend's expected revision; the coordinator serializes it like any other (Stella, *One
coordinator, one live reader/writer*).

## The resident session *(Stella, from her amendment at 60b9027; governs the execution model where it says more than the section above)*

### Keep the work set alive

A supervised, long-lived Lisp session owns the parsed S, its stable-ID indexes,
current event position, derived state and cached projections. Load and validate
once at session start; subsequent verbs operate on those resident objects.
A CLI or connector can be a thin client of this session. A fresh CLI process
must not imply a fresh Lisp process or a complete reload. An always-on system
daemon is not required. Session startup, identity, bounds and shutdown must be
explicit and inspectable.

Both structure and state are manipulated through typed verbs. Creating a node,
decomposing a feature, changing a dependency or assigning responsibility cannot
require the coordinator to rewrite Lisp text by hand. The precise public verb
names remain a pilot decision. Trusted implementation code may be Lisp; imported
S and issue text remain bounded data, never executable forms.

After a mutation, update affected indexes and invalidate affected derived values.
An indexed lookup does not traverse S; a changed leaf does not unconditionally
reparse the file or replay the whole event history. Full validation and broad
queries may still require O(V+E) work. No constant-time promise applies to an
arbitrary structural edit or a change affecting most of the graph. Store derived
state in memory; it does not become a second authoritative work set.

### Accept locally, then clip into Git

Proposed durability mechanism: validate each mutation against the current local
revision, append its stable event ID and payload to a local recovery journal,
and acknowledge acceptance only after that journal is durable. Failed validation
changes neither accepted S nor its journal. The same event cannot apply twice.
This is a crash-recovery proposal, distinct from Glenn's required periodic clips.

A clip captures a named local event boundary, fetches the upstream revision,
checks it against the coordinator's expected shared base, validates the candidate,
and writes a deterministic snapshot with the retained event history. An unexpected
upstream work-state edit is an ownership/protocol conflict, not an invitation to
merge concurrent coordinators. Intake requests are applied by the sole owner. Commit and
push the resulting checkpoint. On success, record the new shared revision and
which local events it contains. Later accepted events remain pending for the
next clip. Do not discard recovery records before successful checkpointing.

A failed network request leaves local accepted work and the pending clip intact;
report it as locally durable but not shared. Retry with bounded exponential
backoff. A rejected push preserves the local work and names the upstream and base shas; never
force-push and never merge (the sentence "apply nonconflicting incoming events
incrementally" stood here in 9757de0 and is struck by stella-0ace603bdc22). An explicit
reload/rebuild is a recovery or maintenance operation, not the normal verb path.
Clip cadence is configurable; shutdown and coordinator handoff request a clip.
A Git checkpoint is not required for every small mutation.

Only one coordinator may access the live work set, as specified below. A lease
file plus periodic Git pushes cannot establish that exclusivity across benches.
Local recovery also does not guarantee another bench can recover unclipped events
after total loss of the original bench. The ownership and recovery mechanism must
be decided and tested before automatic takeover is enabled.

### Required measurements and replays

- Start once, run many queries/mutations: instrument actual parse count, full
  replay count, graph visits, journal writes and emitted bytes. Unchanged indexed
  queries cause zero additional parses or full replays.
- Incremental results equal a clean reconstruction of the same accepted revision;
  unrelated cached projections remain reusable after a local leaf mutation.
- Crash after journal durability but before acknowledgement, then retry the same
  request: one accepted event, no lost or duplicated mutation.
- Crash or disconnect during clip: recovery retains all accepted events and
  reports the last confirmed shared checkpoint honestly.
- A second coordinator attempts to open the live S: refuse access. Transfer
  ownership, then resume the former coordinator: reject its reads/writes and
  side-effect requests under the old ownership generation.
- Structural verbs update the tree and its evidence/scope history without manual
  source editing; the resulting ROADMAP remains reproducible.

These amend draft 3's file-per-verb interface, hand-written-only structure, full
checks around every append, and build-indexes-on-every-read wording. Integrate
those contracts together before approval; adding a resident wrapper around an
unchanged reparse-per-command engine does not satisfy the requirement. Dogfood
the workflow on Fixed Tables, record total builder plus review/repair cost, then
refine the production spec. This amendment does not authorize a production build.

### Reusable recursion, not a mandatory combinator

Glenn asks whether Y-combinator ideas can make the Lisp tooling more general.
The proposal is reusable recursive operators over typed nodes: a fold for
summaries, a bounded unfold for decomposition, and incremental propagation for
derived values. A recursive data structure is not itself the Y combinator.
Ordinary named recursion in Lisp suffices; a literal textbook Y combinator does
not terminate under eager evaluation without adaptation.

Separate traversal from per-kind policy. A completion fold and a cost fold share
traversal machinery but not counting rules: attempts contribute incurred cost,
while only acceptance-qualified work contributes completion. References do not
become extra owned children. Cache with revision and policy identity; invalidate
through the retained indexes. A generic operator must preserve these semantics.

For an acyclic dependency graph, use an affected topological pass instead of
repeatedly rescanning all of S until nothing changes. Any future fixed-point
solver over cyclic analysis facts requires a defined domain, convergence argument
or explicit iteration bound, and an honest non-convergence result. It does not
permit cycles in counted containment or declare tasks complete by convergence.
Decomposition is proposed work, with depth/work/cost bounds and stopping criteria;
it never grants itself permission to execute an expanding tree of tasks.

The bounded pilot should demonstrate two different summary policies over the
same tree, shared-reference counting, and equivalent full versus incremental
results after a change. Adopt the abstraction only if it reduces implementation
or coordination cost without obscuring acceptance evidence.


## The validator *(Rowan; every rule is a hurt already paid)*

The validator is one set of rules run two ways: **whole**, at session load and at every clip,
walking S once and printing one `WORK FAIL` line per finding, capped **per rule**, with one
count line always, exit 1 on any finding; and **on the candidate**, at every mutation, over the
resident S as it would be with the event applied, touching only the nodes the event reaches
(rule 12 for the node the event addresses; rules 1 to 11, 15, 16 and 18 for the nodes it names),
refusing the event at exit 1 with the finding's line and changing nothing. Rules 13, 14 and 17
are the reader's, exit 2, at load. The validator never fetches; what a pointer proves is
`verify`'s, and an unverified pointer is a count, never a finding.

1. **duplicate id** — two nodes, or two events, with one `:id`.
2. **dangling reference** — a `:children`, `:deps`, cell `:ref`, event `:node`, `:lease`,
   `:blocked-by`, `:by-node` or `:criterion` that names nothing.
3. **cycle** — `:children` edges are not a forest, or `:deps` edges contain a cycle (a
   dependency cycle is a deadlock nobody can finish).
4. **two parents** — a node under two `:children` lists.
5. **done without evidence** — a `:to :done` transition naming no evidence events, or naming
   ones whose criteria do not cover the node's `:acceptance`, or of an older generation than
   the node's, or whose pointer is a `note:` scheme.
6. **green parent, unfinished child** — a parent derived `:done` while a required child is not.
7. **empty required set** — a `:work-set`, a `:feature` or a cell with no required work
   derived done.
8. **bad cell** — an unknown axis member, a duplicate coordinate, a missing required cell.
9. **two live leases** on one node.
10. **lease without deadline or default.**
11. **invalid transition** — a `:transition` whose `:to` the table does not allow from the
    node's state as derived from the events before it, or `:blocked` without `:blocked-by`
    (5653982211: *incompatible states, invalid scope transitions*).
12. **scope change without event** — the required set of a roadmap or work set differs from its
    last `:baseline` plus its recorded scope events.
18. **stale at the moment of claiming** — a `:to :done` whose cited evidence is already stale
    (its `:against` is not the cell's `:source-revision`, a local comparison) or already
    recorded found-not-qualifying **in the session's verification cache** (named at `session
    start` by `--cache`, so the candidate gate reads a verdict `verify` already wrote and
    never fetches); refused in the candidate gate (5653982211: *stale evidence*). An evidence
    pointer the cache has never seen is unverified, which is a count, not a finding.
    After the claim, staleness that arrives with a later source revision is a count, not a
    finding, so a source bump never freezes the set; the rollup already demotes it.
13. *(reader, exit 2)* **reader payload** — `#.` or any other refused syntax.
14. *(reader, exit 2)* **bounds exceeded** — bytes, depth or nodes past the flags.
15. **conflicting revisions** — two baseline events for one node claiming different members
    at one revision (5653970526: *reject … conflicting revisions*); staleness of evidence is
    a count, not a finding, above.
16. **no acceptance** — a required `:task` with no `:acceptance` entry, which could never be
    done.
17. **unknown type** is the reader's, exit 2, like rules 13 and 14, and a bound of zero or
    less is refused the same way (SPEC.md: *a budget of zero or less is likewise refused*).

## Cost *(shared; 5653973972, 5654049969 and Stella's amendment)*

At load the session builds five indexes — id to node, containment adjacency, reverse
dependency, repository, category (5654164074) — and every walk goes through them. A mutation
updates only the affected index entries and invalidates only the affected derived values
(ancestors over containment, dependents over the reverse-dependency index, the projections
that reached them); an unchanged indexed query costs **zero parses and zero replays**, and the
session counts parses, replays, visits, journal writes and emitted bytes so the claim is
measured, never asserted (Stella, *Required measurements and replays*). Deriving a node's
current state from the journal is one pass at load, O(E_log), and incremental thereafter.
A full validation or fold visits every node and every edge once: O(V+E), with a visited set
for shared subgraphs, and detects cycles in the same walk. Counts roll up bottom-up over the
containment forest in O(V), cached per node keyed by its scope revision, the journal position
and the verification cache's revision, since a `verify` pass changes which done counts as done. **No transitive descendant set is materialised anywhere**; an ad hoc set query
walks the reached subgraph once, O(V_reached + E_reached), when it is asked. Propagation over
the dependency DAG is one affected topological pass, never a whole-S fixed point (Stella,
*Reusable recursion*). **These bounds are the resident graph's only**: fetching evidence is
`verify`'s, bounded by `--max-fetch` and `--fetch-timeout`, cached at `--cache`, and never
part of validation. Rendering a matrix costs its cell count; a clip costs its snapshot's size.
A dense dependency graph has a large E, and the session prints `edges=<n>` rather than
promising otherwise; every `OK` line prints `emitted=<bytes>`. No constant-time promise for
an arbitrary structural edit or a change reaching most of the graph. **Tests assert visit,
parse, replay and allocation counts, never wall time**, on four shapes: tree, shared DAG, deep
chain, high fan-out, and on one multi-command session.

## Output grammar

Every line's first token is the verb's (`SESSION`, `CLIP`, `WORK`, `VERIFY`, `QUERY`,
`RENDER`, `NODE`, `DECOMPOSE`, `DEP`, `AXIS`, `CELL`, `RESPONSIBLE`, `LEASE`, `HEARTBEAT`,
`RELEASE`, `ATTEMPT`, `EVIDENCE`, `STATE`, `CORRECT`, `EVENT`), the second is `OK` or `FAIL`,
or one of the informational tokens `ROW`, `NOTE` and `MORE`. `OK`, `ROW`, `NOTE` and `MORE`
go to stdout; `FAIL` and refusals go to stderr. Every count line prints on failure as on
success. Every `OK` line ends `emitted=<bytes>`. Every mutation's `OK` line carries the
event's id, its request id, the session's local revision after it, and `pushed=<rev|->`, the
revision of the last clip that reached the branch.

```
SESSION OK session=<path> owner=<name> generation=<n> file=<path> base=<sha> journal=<path> events=<n> pending=<n> nodes=<n> edges=<n> parses=<n> replays=<n> emitted=<bytes>
SESSION FAIL session=<path> owner=<name> generation=<n>: <reason>
EXPORT OK session=<path> into=<path> requests=<n> base=<sha> emitted=<bytes>
REPLAY OK from=<path> requests=<n> applied=<n> refused=<n> shown=<n> emitted=<bytes>   (exit 1 when refused > 0)
SESSION OK ... build=<identity> lease-until=<stamp> generation=<n> ...
REPLAY ROW request=<id> verdict=<applied|refused> rev=<n>: <reason>
ATTESTED OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> criterion=<id> against=<sha> emitted=<bytes>
CLIP OK session=<path> boundary=<request-id> events=<n> base=<sha> commit=<sha> pushed=<true|false> attempts=<n> emitted=<bytes>
CLIP FAIL session=<path> boundary=<request-id> events=<n> base=<sha> upstream=<sha> pushed=false attempts=<n>: <reason>
WORK OK nodes=<n> edges=<n> events=<n> leases=<n> expired=<n> stale=<n> scope=<rev> source=<sha> emitted=<bytes>
WORK FAIL <id>: rule <n>: <reason>
WORK FAIL nodes=<n> findings=<n> shown=<n> expired=<n> stale=<n>
VERIFY OK pointers=<n> verified=<n> unverified=<n> stale=<n> fetched=<n> cached=<n> emitted=<bytes>
VERIFY ROW <event-id> pointer=<p> verdict=<verified|unverified|stale> at=<stamp>
VERIFY FAIL pointers=<n> unverified=<n> shown=<n>
QUERY OK ask=<kind> scope=<rev> membership=<rule> unit=<unit> source=<sha> freshest=<stamp> done=<n> done-unverified=<n> unknown=<n> deferred=<n> cancelled=<n> superseded=<n> stale=<n> [green=<k> baseline-rows=<n0>] rows=<n> shown=<n> parses=<n> emitted=<bytes>
QUERY ROW <id> kind=<k> state=<s> k=<n> n=<n> unknown=<u> responsible=<name|-> holder=<name|unowned> pushed=<rev|-> heartbeat=<age|none> deadline=<stamp|-> blocked-by=<id|->
QUERY FAIL ask=<kind> rows=<n> shown=<n>: <reason>
RENDER OK view=<id> cells=<n> bytes=<n> into=<path> emitted=<bytes>
RENDER FAIL view=<id> cells=<n> drifted=<n> into=<path>
<MUTATION> OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|-> ... emitted=<bytes>
<MUTATION> FAIL node=<id>: rule <n>: <reason>
LEASE FAIL node=<id> holder=<name> since=<stamp> deadline=<stamp> live=<n>: held
<TOKEN> NOTE <caveat>
<TOKEN> MORE kind=<rule|row> shown=<n> total=<t> <remedy>
nova-work <build identity> <goos>/<goarch> <go version>
```

where `<MUTATION>` is one of `NODE`, `DECOMPOSE`, `DEP`, `AXIS`, `CELL`, `RESPONSIBLE`,
`LEASE`, `HEARTBEAT`, `RELEASE`, `ATTEMPT`, `EVIDENCE`, `ATTESTED`, `STATE`, `CORRECT`, `EVENT`, each
adding the fields its section names (`LEASE OK … holder= deadline= default= live=`, `STATE OK
… from= to= evidence=`, `ATTEMPT OK … by= result= generation=`, `EVIDENCE OK … criterion=
against=`, `CORRECT OK … generation=`, `EVENT OK … kind= scope=`).

Exit 0 the verb ran and passed; 1 it ran and said no (a finding, a refused take, a refused
transition, a stale expectation, drift, a divergence, a fenced session, and for `verify` its
own verdict `unverified=<n>` above zero, which is a report and not a `check` finding); 2 it could not run (a
missing flag, no such session, a refused snapshot, bounds exceeded, an unusable invocation,
which costs one line ending `run: nova-work help`). One line per event, escaped through
`internal/oneline`; every listing capped by `--max` with a `MORE` line naming the remedy; the
version line is `internal/buildinfo`'s.

## Acceptance replays *(shared; Glenn's list, 5653970526, and Stella's amendment)*

Fixtures, each tiny, each a test: nested completion; a failed gate; a shared dependency
counted once; unknown evidence; unverified evidence changing no recorded state and counting
as unknown in every rollup; stale evidence counted as unknown; a `note:` pointer refused as
evidence for done; a `run:` pointer to a failed run resolving and counting as unverified; a
structure-plus-scope envelope crashed between its two events replaying all-or-none; a task split by `decompose` (both units printed, revision moved); scope
expansion by `node add` (added since baseline visible, new rows at the bottom, `baseline-rows`
printed); deferral (cannot raise the done count, and cannot raise the percentage: a deferred row
stays in `rows=`); reopening; two processes opening one journal, the second refused; a
passing test of another name failing to qualify a criterion; a `correct` event voiding an
earlier qualification for the done claim; a correction bumping the
generation and an older attempt's result refused at `state --to done`; future work cannot
lower the active percentage; an out-of-scope cell leaving the applicable rows; a changed
nested task updates every affected view and no unrelated cached projection; `--at` replaying
to an earlier revision; malformed and cyclic data refused at load; a `#.` payload refused at
the reader; a `:deps` cycle refused; a cancel request withdrawn and a cancel confirmed; a deep
chain with no quadratic work (visit counts asserted); a lease past its deadline reads as
unowned, its responsibility unchanged, and its release is not blocked; `:extend-once` once; a
lease reads `pushed=-` until its clip reaches the branch; an invalid transition refused;
a refused mutation leaving S, the journal and the indexes unchanged; `render --check` fails
on one changed cell; **start once, run many** (zero parses and zero replays on unchanged
indexed queries, counts asserted); incremental results equal a clean reconstruction of the
same accepted revision; a crash after journal durability and before acknowledgement, then the
same request retried, yields one accepted event; a crash or disconnect during a clip retains
every accepted event and reports the last confirmed shared checkpoint honestly; a second
coordinator refused while one is active; a controlled handoff with the old owner fenced; an
old owner returning with a delayed request, refused by generation; structure
verbs produce a reproducible `ROADMAP.md` with no hand edit. The stall replays of 5649089106
belong to stall detection, deferred below, and are listed there so they are not lost.

## What this draft does not do

Stall detection and bounded recovery with its six replays (5649089106), `plan`/`apply`/
`reconcile` (5653982211), the cross-bench ownership backend and fencing generations (the rule is Glenn's; the git-lease backend above is this draft's proposal for the pilot and
Stella's section keeps the general requirement), the `link`/`absorb` intake modes and the
staged migration as code (their contracts are Stella's sections), the GitHub issue intake and correspondence adapter as code (its contract is Stella's
section below; the adapter is its own spec), token and cost joins beyond the attempt's
`:usage` pointer (#175, #181), and the categories taxonomy (5654164074) are later revisions,
each with its issue. Nothing here deletes, migrates or publishes an issue. Known work is not
authorized, active, scheduled or public by being in S.

## Additions of the authors', not in the source

So a reader never mistakes them for Glenn's requirements. Rowan's: the lease model whole
(5654176537, its three changes from that comment: expiry derived and never stored, an expired
lease a count and never a finding, `:extend-once` defined) and its tool-owned random ids; the
`:superseded` state, the `:supersede` event and the `:cancel-requested` state; the
one-validation-of-the-candidate gate; rule 16 and the `note:`-never-for-done rule; counting
an unverified or stale done as unknown; the five indexes and the in-memory aggregate cache;
`--cache <path>`; the exact list of refused reader syntax beyond `#.` (every dispatch macro,
`#'`, quote, backquote, package-prefixed symbols, ratios, floats, characters); `;` comments
discarded by the reader; the three bound flags and their no-default rule; unknown keys
preserved; deriving state, generation and scope revision from events; `:required` defaulting
to true; the `:review` state; `:responsible` and its inheritance (Stella's point 4); the
`file:` and `test:` pointer schemes and the generic `note:` scheme; `:criterion` binding on
evidence and `verify` as a separate pass with a cache (Stella's points 1 and 2); `:clock
:tool` with `--now` optional (Stella's point 3); the two-marker region in `ROADMAP.md`; the
`:repo` field on a top-level work set; the transition table's exact edges; `<repo>/shared`;
`--at <revision>`; `emitted=<bytes>` on every `OK` line; the structure verbs' names and
flags. Stella's: the local recovery journal, event ids and expected revisions, the named event
boundary per clip, the offline-clip rule, the fencing-generation ownership record as a proposal (the `OWNER`-on-the-branch form with
CAS push, the lease `until`, the self-fence at `until`, `--skew`, and the export/replay path
are Rowan's), fold/unfold/propagate as
operators, the measurement list, the `link`/`absorb` archive order, the migration dispositions; and in her sections below,
the pilot branch and sha, the prototype facts, the rate schedule and virtual cost, the
`NEXT-TOOLS.md` hand-off, and the fixed-table capability boundary. Each is open to be cut by
the pilot.

## Issues, intake, migration, and the one coordinator *(Stella, from her amendment at 60b9027; the one-coordinator rule is Glenn's and supersedes every earlier two-writer sentence)*

### Public issue correspondence survives intake

Glenn clarified: external people continue opening issues on public repositories
such as yojimbo. Intake represents and links that issue in S; it does not move or
delete the GitHub issue. S owns planning and decomposition, while GitHub retains
the public discussion and externally observed issue lifecycle.

Store a stable provider/repository/issue identity plus its current URL and last
observed remote revision. Use an explicit mapping: one public issue may require
many work nodes, and one work node or landed fix may address several issues.
Repeated intake updates the existing correspondence, never duplicates the work.
Retain external reports separately from the coordinator's accepted plan; remote
text is data and cannot execute verbs or silently change scope or authority.

Track correspondence actions as pending, confirmed or failed with request IDs
and receipts. Reporting a fix or closing an issue is a distinct outbound action
under the team's configured authority, tied to the required work and actual
landing/acceptance evidence. A locally completed attempt, a deferred work node,
or a scope reduction does not by itself close the public issue. Retry uncertain
outbound actions idempotently and preserve concurrent human changes. A reopened
issue creates a reconciliation signal; it neither disappears nor silently erases
previous completion evidence. Ordinary linked intake and clipping never delete an issue. The separately chosen
absorb operation below is the explicit exception.

### Link versus absorb

Glenn authorizes a second intake mode for issues created by him or participating
AI friends, on public or private repositories: `absorb` moves their lasting work
record into S and may remove the original GitHub issue. This is distinct from
`link`, the default for outside contributors. Author identity alone does not make
an issue selected for absorption; scope and intake mode must be explicit, with
team configuration identifying participating authors and applicable repositories.

Before deletion, retain the original issue identity, authorship, body, discussion,
labels, state, relevant relationships and available attachments in an archive
associated with S. Preserve provenance separately from planning. Unavailable or
unpreserved content is reported and leaves deletion pending, not silently skipped.
The destination must preserve the source's access scope; a private issue is not
published by becoming part of S.

Order the operation: capture source at a named remote revision; validate the
archive and mapping; commit and confirm the shared Git checkpoint; recheck for
source changes; then delete only the selected source issue under the applicable
authorization for that repository and selected batch. Configuration identifies
permitted actors and adapters; it does not itself grant authority. Record the
grant's scope and source reference as provenance, never as executable permission
from imported content. If the source changed, reconcile and checkpoint the added content first.
Keep the original external identity and an absorbed tombstone plus deletion
receipt so later intake cannot recreate the work. A network failure or uncertain
delete result preserves the archive and a pending reconciliation state. Do not
claim atomicity between Git and GitHub or restore an issue by inventing an author.
These semantics are a specification, not a request to absorb existing issues now.

### Initial migration: preserve first, reconcile, then choose absorption

Glenn's initial rollout is importing the team's existing work sets first, while
identifying external issues that remain on GitHub. No data may be lost. Migration
is not a bulk delete of the source issue lists.

Begin with an explicit inventory of authorized source work sets and issues. Each
entry records its source identity, destination node/mapping, capture revision,
content manifest and disposition: keep linked, eligible for absorption, or
unresolved. Outside-contributor issues stay linked; unknown ownership, mixed
provenance or unclear disposition remains unresolved without source deletion.
This is a migration classification, not a claim that an author's identity grants
new access or that all team issues have already been selected for deletion.

Import in resumable batches without deleting originals. Preserve original records
alongside the normalized representation, reconcile counts and content manifests,
deduplicate stable identities, and account explicitly for every inventory entry.
Record missing attachments or inaccessible discussion as incomplete. Verify the
shared checkpoint can be loaded and replayed, and that original records can be
retrieved from it, before declaring an import complete. Feature decomposition,
links, ownership, dependencies, open questions and completion evidence must not
be collapsed into a flat title/status list.

Absorption is a subsequent selected operation after this reconciliation, never
an import side effect. If the provider cannot support a safe revision boundary
against concurrent source changes, leave removal pending rather than claim a
lossless deletion. A backup receipt alone does not prove that newer comments were
captured. Report imported, linked, eligible, absorbed and unresolved separately;
retain batch checkpoints and provenance so interruption or retry does not lose
records or duplicate work. The prototype must exercise interruption and a source
edit during migration before it is trusted with removal.

### One coordinator, one live reader/writer

Glenn's explicit rule, superseding the earlier concurrent-coordinator proposal:
there is exactly one active coordinator and one reader/writer of resident S via
nova-work. Workers and friends submit results, evidence and requested changes to
that coordinator. They do not open another live S or mutate it directly. Other
readers use published, revision-labelled snapshots; these are not active planning
replicas. The owner serializes accepted operations, including incoming messages,
with stable request IDs and local expected revisions.

Failover transfers the role rather than adding another coordinator. A standby
may prepare from a published checkpoint but cannot activate its work set until
ownership is transferred and the former owner is fenced out. Old processes and
old delayed requests must not resume mutation, task dispatch or Git publication
under an obsolete ownership generation. A stale heartbeat or unanswered ping is
an availability signal, not proof of exclusive takeover authority.

Proposed mechanism: an authoritative ownership record with atomic acquisition
and monotonically increasing fencing generations, enforced at the live session
and consequential dispatch/publication boundaries. The exact cross-bench backend
is still a design decision. A local PID/file lock alone only protects one host;
a Git lease file alone does not prevent two offline owners. If exclusive ownership
cannot be established, do not activate a competing coordinator. Define ownership
loss behavior and recovery of unshared accepted events explicitly, then test
partition, crash, delayed delivery, controlled handoff and old-owner return.

The singleton rule is Glenn's requirement; the fencing implementation is our
proposal. It preserves automatic resilience as a goal without claiming that a
safe cross-bench failover protocol has already been built. All older references
to merging simultaneous coordinator edits are superseded by this section.

## Roadmap as a view; the Schema pilot *(Stella)*

**Practice first, then retrospective, then production.** Glenn reaffirmed this sequence on
2026-09-13: dogfood the hierarchy on Fixed Tables, inspect what worked and what did not,
bring those findings into the tool specs while fresh, then implement. This draft records
hypotheses alongside settled requirements. Its existence does not start production work or
replace the remaining Fixed Tables acceptance gates.

### Durable primary state and GitHub intake

Glenn's chosen direction is that **S is the primary form**, with a persistent versioned
repository home. `mas-bandwidth/work` is his proposed location; creating or populating it is
separate from this draft and belongs to an operator with the relevant repository or
organization authorization. The tool grants no such authority. Local in-memory structures and indexes are rebuildable working
copies. Task identities, source links, events, decisions and evidence records must survive
process loss, bench changes and coordinator handoff through the durable history.

People may continue to file GitHub issues. Import is intake into S, keyed by the immutable
forge repository/issue identity so retries do not duplicate tasks. Keep the original link,
source revision or update marker, and import receipt. Decomposition, ownership and planning
then live in S. Later issue edits are observed changes to reconcile, never an unconditional
overwrite of the work tree. Issue text remains data and cannot assign authority, execute code
or grant permissions. Publishing a summary back to GitHub is a separate, explicit adapter
operation with a receipt; importing does not close, delete or rewrite the source issue.

*(A paragraph on concurrent coordinators reconciling conflicts stood here in e167483 and is
struck by the one-coordinator rule of stella-0ace603bdc22; the clip refuses on divergence,
above.)* A checkpoint records the parent revision and change. Network failure leaves a pending
local checkpoint whose sync status is visible. Reads should remain useful offline; stale or
unavailable remote evidence is reported as such without erasing the last verified record.
The persistence protocol and recovery tests are production spec work informed by the pilot,
not guarantees supplied merely by putting a file in Git.

### Inventory before implementation

A new feature starts with its full deliverable inventory: capabilities, applicable axes,
required sub-work, shared prerequisites and acceptance evidence. Include capabilities already
completed when importing existing work. A defect backlog alone is not that inventory.
For Schema, ordinary scalar and container support must be visible beside version evolution,
refusal behavior and interoperability. Maps, unbounded arrays and pointer blobs are not
fixed-table capabilities merely because a backend supports them on another wire.

Pin the baseline membership, source revision and completion unit before starting the stream.
Append discoveries visibly; retain their discovery event and the original baseline. Splitting
an existing task into smaller tasks records decomposition, not newly completed work. Splitting
can change the number of leaves, so a comparison must either retain the baseline unit or show
that change explicitly. Reordering the execution queue does not rewrite discovery order.
Do not inflate progress by adding trivial rows or hide unfinished work by merging it into a
larger green row. A scope decision has an author, reason and reviewable diff.

### A cell is a reference, not another state store

A roadmap declares ordered named axes and maps each coordinate to an existing work node.
The axes are arbitrary; languages are the Schema example. A feature/language cell may focus
into sub-features, tasks and smaller streams recursively. Its owners, dependencies and
receipts are the same objects seen by repo and category queries.

For each cell, show completed required leaves, total required leaves and unknown leaves.
Green requires the full acceptance contract, including prerequisite gates. Per-language
feature completion counts whole green feature cells divided by required feature rows. It is
not the average of cell percentages. With unresolved evidence, show a verified lower bound
and an explicit unknown count; do not present that bound as estimated implementation progress.
A named unsupported surface is not a passed test. Excluding it from the denominator requires
a recorded scope decision, not a renderer convenience.

Shared compiler, lock, platform and final integration work has one canonical owner. A roadmap
can show it in an adjacent gate view and reference it from affected cells. It does not create
nine copies of the work or its token cost. If the language matrix counts runtime capabilities
only, label that scope and show the shared release gates alongside it; a green runtime matrix
alone must never print that the whole goal is done.

Useful focused views include remaining features in one repo, ideas for that repo, open bugs,
work in a category, one language's unfinished cells, and a cell's nested tasks. A listing is
compact and capped; it carries stable IDs and a way to focus further. Category taxonomy is
TBD. The query machinery filters; the reader should not have to scan S manually.

### Evidence and the imported starting point

The Schema pilot is on `codex/fixed-tables-roadmap-20260913`; the surveyed implementation is
`8ea5ed8e4656875088250f564e88a965a7135e7c` on `fixed-table-form`, not a completed merge to main.
`docs/roadmap.sexp` is its state input and the marked region in `ROADMAP.md` is its projection.
Original audit IDs, historical reports and superseding aliases remain retrievable. They are
provenance, not current completion assertions.

The initial 23 audit-family rows omitted ordinary delivered capabilities. Glenn identified
that gap; the survey appended 34 ordinary capability rows and preserved the versioning
contract's individual rows for nested work. Schema PR #1006 at `c77d81fc` now separates
source support (built, partial, missing) from qualified ordinary acceptance, with source
references, invoked assertions and remaining work per cell. Its original audit-family
reconciliation remains incomplete. This is an incomplete imported baseline being
repaired, not evidence that the implementation just expanded by the same amount. The source
records both the inventory correction and implementation work discovered during execution.

A receipt must identify what was checked, against which revision, with what result and which
acceptance criterion it supports. A resolvable PR, a test name or a green aggregate CI run
alone does not prove a whole feature. Check whether the named assertion ran, whether it can
fail the gate, and whether it covers the claimed behavior. Generation, valid-data round trips,
hostile-input certification and performance are different proofs. Preserve disagreement and
unknowns rather than turning an attractive summary into green cells.

### What the current prototype proves, and does not

Emma's renderer validates a bounded restricted-data graph, rejects duplicate IDs, dangling
references and cycles, checks complete matrix coordinates, and derives progress from required
leaves. It writes only between the roadmap markers. Its replay tests cover Unicode byte
preservation, deterministic repeated rendering, drift detection, empty evidence, partial
rollups and reachable target selection. Stella's follow-up closed a literal-EOF sentinel
collision and whitespace-only evidence acceptance. A missing requested roadmap now refuses
instead of falling back to a different one.

These are prototype facts, not production conformance claims. The pilot still uses shared
containment references and memoized descendant sets, which can cost quadratic space/time;
the production design's counted containment forest and reference graph address that
specific duplication cost. The pilot does not yet implement them, indexed repo/category queries, leases,
network evidence validation or incremental updates. Its AST-depth check occurs after the Lisp
reader, so it is not the production reader's pre-parse depth guarantee. The existing parser
and gate tests passing does not certify those missing properties.

### Retrospective required before production implementation

Use the method to carry the Fixed Tables work forward, including at least an inventory
correction, a completed cell, newly discovered work, a dependency or handoff, and a changed
focus. Record failures and repairs as they occur. At the retrospective, compare the same
questions and acceptance scope against the previous manual workflow, then change the spec.

Measure total tokens by friend/model/bench/repo and attempt through the attempt event's
`:usage` pointer where available, including source survey, coordination, review and
correction. A missing or unresolved usage pointer is unmeasured, never zero.
Keep raw observations so alternative reports remain possible. Price with a versioned rate
schedule, separating billed cost from virtual model-weighted cost. Record useful work per
accepted result, wall time, retries, stale answers and questions that needed human rescue.
Quality and total required tokens come first, average token cost next; reduce wall time when
it does not materially worsen those objectives. A cheaper first draft that costs more to
repair has not demonstrated an efficiency gain.

Each proposed tool capability should cite the observed friction, the smallest operation that
would remove it, its safety boundary, a measurable benefit and an acceptance replay. Keep the
capabilities that earned their place; revise or defer the rest. The post-Fixed-Tables review
feeds `NEXT-TOOLS.md` and the production specs before implementation begins.

## The measurement that decides *(shared)*

Before this leaves draft, on the Fixed Tables roadmap: tokens and wall time to update one
fact in S (an `evidence` event and a `state --to done`) and regenerate the table, against editing
the table by hand; whether "who is on the C leg" is answerable from leases alone without
reading the bus; whether a reader of the generated table found a number the source did not
support. The benefit is observed or the tool is not built (5653982211).
