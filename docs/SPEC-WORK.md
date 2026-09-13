# nova-work — specification (DRAFT 4, 2026-09-13)

**Status: a draft under joint authorship, Rowan and Stella, on Glenn's word of 2026-09-13.**
Nothing here is built. The Schema NEW Fixed Tables roadmap is the pilot, and the pilot decides
what this document keeps. Sections marked *(Stella)* are hers; sections marked *(Rowan)* are
mine; the rest is shared. Every requirement that is Glenn's cites the nova-tools#177 comment
it comes from, by id, so a reader can check the words. **Two things in this document are the
authors' proposals and not Glenn's requirements, and they are marked where they stand: the
lease model (Rowan's, from #177 comment 5654176537) and everything in the section *Additions
of the authors'*.** Draft 4 follows three Fable cold reads (draft 1 HOLD at 15:34Z, draft 2 HOLD
at 15:47Z, draft 3 HOLD at 15:49Z; all recorded on PR #231) and Stella's five review points of 15:38Z; the second
read found that two of draft 2's own repairs deadlocked each other, so draft 3 re-derived the
write model and draft 4 closes the last write-path hole the third read found: **the tool only ever
appends events, a node's current state is derived from its events, and a write is gated by one
check of the file as it would be with the event appended.**

**One recursive work set, S.** A file of restricted Lisp data holds what is **desired** (the
work: repositories, streams, features, tasks, down to whatever depth is useful) and what is
**observed** (events: transitions, evidence, attempts, leases, heartbeats, scope changes).
Everything **derived** — a task's current state, counts, percentages, views, plans — is
computed on read and never written into the file; a roadmap table, an owner queue, a stream
report and a percentage are each a **projection** of S at a named scope revision, and none of
them is a second store. `ROADMAP.md` is regenerated, never edited; a hand-typed percentage is a
bug (5653970526). **Where S lives is two statements that do not yet agree, and both are kept:** #177 comment
5653982211 says to *evaluate making this the primary internal state only after lossless
import/export, stable identity mapping, provenance retention, conflict handling, restart/replay
and reconstruction have been demonstrated*; Stella reports Glenn's later live word of 2026-09-13
that S is the primary form with a persistent versioned repository home (her section below). Until
his later word is on #177 in his own words, the demonstration conditions bind, and nothing in
this draft migrates or imports anything.

This spec is normative once it leaves draft. It is a sibling of [SPEC.md](SPEC.md), whose
**Conventions** — exit codes, no guessed paths, the one-line guarantee, the field escape, the
cap-and-count law, the version line — govern here unchanged. If the code and this document
disagree, one of them has a bug, and the tests decide which.

## The failures it closes

| the failure, from the record | what closes it |
|---|---|
| a task disappears after a context loss, a priority change or a handoff (#177) | the file is the store; a node's `:id` is stable through every rename and display change; every change is an appended event with an author and a stamp |
| two workers on one task, neither aware of the other (nova-board's 2026-09-10 morning) | one live **lease** per node; a second `take` is refused and names the holder |
| a stored owner read as "working on it" while nothing moves | *responsible* and *working* are two facts: `:responsible` is durable accountability set by a person's word; *working-now* is a heartbeat inside a window (5654012267) |
| a percentage no evidence supports; an average of fractions rounded to green (5653970526) | done needs evidence bound to the task's acceptance criteria; language completion is green cells / applicable rows, and cell progress shows its numerator and denominator |
| the denominator moved and nobody saw it | every event that changes a required set increments the scope revision; a baseline is an event; additions append at the bottom; removals carry a reason; `percent` prints the baseline row count beside the current one (5654160320, 5653990830, 5649089106) |
| an old attempt's result quietly satisfying a corrected task (5653982211) | a task carries a `:generation`; a `correct` event bumps it; evidence from an attempt of an older generation cannot close the task |
| studying S became pairwise work (5653973972, 5654049969) | counted containment is a forest, references are a graph; one fold is O(V+E) over three indexes; no transitive descendant sets are materialised; evidence fetching is a separate, bounded pass |
| a deferral or a cancellation counted as progress | `:deferred`, `:cancelled` and `:superseded` are transitions recorded as scope events; they never enter the done count and the baseline denominator stays printed |
| the roadmap table edited by hand and the data left behind | `render --check` fails on drift; the table lives between two markers and the file owns it |

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

**Two halves of one file, and the tool writes only one of them.** The **structure** — nodes,
their `:id`, `:type`, `:children`, `:deps`, `:acceptance`, `:responsible`, axes and cells — is
written by hand, in the file, under review, and `check` is its gate. The **log** — every event
below — is written by the verbs, **append-only**: no verb ever edits or deletes a form that is
already in the file, so a comment, an unknown key and a hand-written node are never touched
by a write, and every transition is kept rather than overwritten (#177: *preserve transitions
and corrections rather than overwriting the history*). **A hand-written change to structure
that changes a required set is reconciled by the scope event that records it**: the edit and
the `event` verb are one act, and until the event is appended `check` reports rule 12 on that
node, which is the gate working, not a lock (the write rule below says how the event gets in).

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
  read against), `:completion-policy` (`:all-required-features` is the only policy in draft
  3). A cell references a node; it never contains state of its own. An unknown axis member, a
  duplicate coordinate and a missing required cell are refusals; an omitted cell is never
  complete (5653990830). A cell may be marked `:out-of-scope` by a recorded scope event, which
  is distinct from unstarted and from unknown, and **an out-of-scope cell leaves that axis
  member's applicable rows** (5654160320: *fully green features / applicable features*).
  Adding an axis member or a feature is a scope revision; removing one is not completion.
- `:task` — work with `:acceptance` (pointers to the criteria that close it, each with a
  short id), `:required` (default true), and a current state, generation, evidence set and
  blocked reason **all derived from its events**. A task has no stored worker; who is working
  on it is answered from leases only; who is responsible is `:responsible`, inherited down the
  containment forest until overridden.
- `:lease` — the ownership-of-execution record, below. *(The authors' proposal, not Glenn's.)*
- `:event` — the log. Every event carries `:kind`, `:node`, `:by`, `:stamp`, `:clock` (`:tool`
  or `:given`), and the fields its kind needs:
  - `:transition` — `:to <state>`, `:reason`, and for `:blocked` a `:blocked-by` reference; a
    `:to :done` names the evidence event ids it stands on.
  - `:evidence` — `:pointer`, `:criterion` (an `:acceptance` id on the node), `:against <sha>`
    (the tree it was read against), `:attempt` (optional).
  - `:attempt` — `:model`, `:bench`, `:started`, `:ended`, `:result` (a pointer), `:usage` (a
    pointer to a token record, #181), `:generation` (the task generation it answered).
  - `:correct` — a correction to a task: `:reason`; bumps the task's `:generation`
    (5653982211).
  - `:responsible` — `:to <name>` on a work-set or feature, on a person's word, `:reason`.
  - `:lease`, `:heartbeat`, `:release`, `:handoff` — the lease log, below.
  - `:baseline`, `:discovery`, `:remove`, `:defer`, `:reopen`, `:split`, `:supersede`,
    `:scope` — the scope log: a baseline records the required set of its node **as the tool
    computed it at that moment**, member by member; a split names the new children; a
    supersede names `:by`; every one of them increments the node's scope revision, because
    every one changes a required set. Splitting is decomposition, not discovery or completion
    (5653970526).

**States and transitions, derived.** A task's current state is the `:to` of its newest
`:transition` event, or `:unknown` when it has none; `:unknown` is explicit and is neither
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
`:acceptance` entry of its node is refused. Draft 3 ships five schemes — `commit:<sha>`,
`run:<owner/repo>#<id>`, `pr:<owner/repo>#<n>@<sha>`, `file:<path>@<sha>`,
`test:<package>/<name>@<sha>` — and a sixth, `note:<scheme>:<id>`, for any team's message
store, so that no family's bus is named in the tool (5653970526). **A `note:` pointer is
accepted on a heartbeat and on an attempt's `:usage`, never as evidence for `:done`**
(5649089106: *status messages are not completion evidence*); rule 5 refuses it. **Resolution is a separate
pass from validation** (Stella, point 2): `check` never fetches; `verify` fetches, under
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
  baseline denominator kept beside it for expansion and contraction (5654160320).
- **Units are labelled** on every line: `unit=features` or `unit=leaves`; a comparison never
  changes unit silently.
- **Future cannot lower the active percentage; a deferred item cannot raise the completed
  count** (5653970526). Both are tests.
- **Unknown and unverified are counts of their own**, printed on every answer.

## Queries — the contract *(Rowan; Glenn's list from 5654012267)*

Every answer is computed whole before anything is printed, then printed as one `QUERY OK`
scope line and one `QUERY ROW` line per fact, capped and counted. The scope line always carries: `scope=<revision> membership=<rule> unit=<unit>
source=<sha> freshest=<stamp> unknown=<n> unverified=<n> emitted=<bytes>`.

| ask | answers |
|---|---|
| `done --node X` / `remaining --node X` | completed and outstanding required work under X, by kind, capped and counted |
| `who --node X --window <dur>` | live leases on X and beneath it: holder, heartbeat age, deadline, default; then `held-not-worked` and `unowned` counts; and `responsible=` for X |
| `percent --node R --axis <member>` | the roadmap rollup for one axis member, with `green=<k> rows=<n> baseline-rows=<n0> done-unverified=<n>` and every partial cell's `k/n` and `unknown=<u>` |
| `size` / `size --node X` | total required leaves, done, unknown, unverified, deferred, since-baseline |
| `stream --repo <owner/name>` / `--owner <name>` | the same, for one repository or one friend's own selected work, plus `responsible=` and live lease count (5654012267: *ownership for a named stream*) |
| `under --repo <owner/name> --category <label>` | compact listing of nodes by category with state (5654164074; taxonomy TBD) |
| `stale --window <dur>` | leases past deadline or past the heartbeat window, grouped by holder |
| `handoffs --since <revision>` | the lease transition log |

## The verbs *(Rowan; a draft shape, to be cut by the pilot)*

Common to every verb that reads a file, written once here: `<file flags>` = `--file <S.sexp>
--max-bytes <n> --max-depth <n> --max-nodes <n>`, none defaulted, plus `[--now <stamp>]`.
Common to every verb that writes: `<write flags>` = `--as <name> [--now <stamp>]`. Every
listing verb takes `--max <n>`, default 20, `0` means all, negative refused (SPEC.md, the
cap-and-count law). Every duration comes from a flag: `--window` is required by `who` and
`stale`, `--by` by `take`.

```
nova-work check     <file flags> [--max <n>]
nova-work verify    <file flags> --max-fetch <n> --fetch-timeout <seconds> --cache <path> [--offline] [--node <id>] [--max <n>]
nova-work query     <file flags> --ask <kind> [--node <id>] [--repo <o/n>] [--owner <name>] [--category <label>]
                    [--axis <member>] [--window <duration>] [--since <revision>] [--at <revision>] --cache <path> [--max <n>]
nova-work render    <file flags> --view <roadmap-id> --into <path> --start <marker> --end <marker> [--at <revision>] [--check]
nova-work take      <file flags> <write flags> --node <id> --by <duration|stamp> --default <release|extend-once|escalate:<name>>
nova-work heartbeat <file flags> <write flags> --node <id> --evidence <pointer>
nova-work release   <file flags> <write flags> --node <id> [--handed <name>]
nova-work attempt   <file flags> <write flags> --node <id> --model <name> --bench <name> --result <pointer> [--usage <pointer>]
nova-work evidence  <file flags> <write flags> --node <id> --pointer <pointer> --criterion <id> --against <sha> [--attempt <id>]
nova-work state     <file flags> <write flags> --node <id> --to <state> (--evidence <event-id> ... | --reason <text>) [--blocked-by <id>]
nova-work correct   <file flags> <write flags> --node <id> --reason <text>
nova-work responsible <file flags> <write flags> --node <id> --to <name> --reason <text>
nova-work event     <file flags> <write flags> --kind <baseline|discovery|remove|defer|cancel|reopen|split|supersede|scope> --node <id> --reason <text> [--by-node <id>] [--children <id,...>]
nova-work version
nova-work help
```

**What writes do, stated exactly.** Every writing verb appends exactly one event form to the
end of the file and touches nothing else. **The gate is one check, of the file as it would be
with the event appended**: the verb reads the file, evaluates the structural rules below (never
a fetch) over the file plus the candidate event, and either appends the event unchanged or
refuses at exit 1 with the finding's line and writes nothing. So a hand-written discovery is
recorded in the only order it can be: edit the structure, then `event --kind discovery`, whose
candidate satisfies rule 12 for that node while the file alone did not; and a finding elsewhere
in the file that the candidate does not cause still refuses the write, because a red file is
stopped, not written around. Concurrency between writers is the persistence protocol's
(Stella's section: a checkpoint records its parent revision), which draft 4 leaves to the
repository and names as production work. `plan`, `apply` and `reconcile` (5653982211, the Terraform half) are **not in draft
3**: named here so a reader knows they are deferred, with their own section once the pilot
has shown what a plan must name.

## The validator *(Rowan; every rule is a hurt already paid)*

`check` walks S once and prints one `WORK FAIL` line per finding, capped **per rule**, and one
count line always. Exit 1 on any finding. Rules 13 and 14 are the reader's, not the
validator's: they refuse at exit 2 before any rule below runs. `check` never fetches; what a
pointer proves is `verify`'s, and an unverified pointer is a count, never a finding.

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
13. *(reader, exit 2)* **reader payload** — `#.` or any other refused syntax.
14. *(reader, exit 2)* **bounds exceeded** — bytes, depth or nodes past the flags.
15. **conflicting revisions** — two baseline events for one node claiming different members
    at one revision (5653970526: *reject … conflicting revisions*); staleness of evidence is
    a count, not a finding, above.
16. **no acceptance** — a required `:task` with no `:acceptance` entry, which could never be
    done.
17. **unknown type** is the reader's, exit 2, like rules 13 and 14, and a bound of zero or
    less is refused the same way (SPEC.md: *a budget of zero or less is likewise refused*).

## Cost *(shared; 5653973972 and 5654049969)*

The tool builds five indexes on every read — id to node, containment adjacency, reverse
dependency, repository and category (5654164074: *indexed repository/category selection*) —
and every walk goes through them; subtree aggregates (required, done, unknown, blocked,
freshest) are cached in memory keyed by the node's scope revision and the log length, and
rebuilt, never persisted (5653973972). Deriving every node's current state
from the log is one pass over the events, O(E_log). A full `check` or fold visits every node
and every edge once: O(V+E), with a visited set for shared subgraphs, and detects cycles in
the same walk. Counts roll up bottom-up over the containment forest in O(V). **No transitive
descendant set is materialised anywhere**; an ad hoc set query walks the reached subgraph
once, O(V_reached + E_reached), when it is asked. **These bounds are the local graph's only**
(Stella, point 2): fetching evidence is `verify`'s, bounded by `--max-fetch` and
`--fetch-timeout`, cached, and never part of `check`. Rendering a matrix costs its cell count.
A dense dependency graph has a large E, and the tool prints `edges=<n>` rather than
promising otherwise; every `OK` line prints `emitted=<bytes>` so the cost of the answer is
part of the answer. Incremental update after a changed leaf invalidates only affected
ancestors and dependents and recomputes in dependency order; no constant-time promise.
**Tests assert visit and allocation counts, never wall time**, on four shapes: tree, shared
DAG, deep chain, high fan-out.

## Output grammar

Every line's first token is the verb's (`WORK`, `VERIFY`, `QUERY`, `RENDER`, `LEASE`,
`HEARTBEAT`, `RELEASE`, `ATTEMPT`, `EVIDENCE`, `STATE`, `CORRECT`, `RESPONSIBLE`, `EVENT`),
the second is `OK` or `FAIL`, or one of the informational tokens `ROW`, `NOTE` and `MORE`.
`OK`, `ROW`, `NOTE` and `MORE` go to stdout; `FAIL` and refusals go to stderr. Every count
line prints on failure as on success. Every `OK` line ends `emitted=<bytes>`.

```
WORK OK nodes=<n> edges=<n> events=<n> leases=<n> expired=<n> stale=<n> scope=<rev> source=<sha> emitted=<bytes>
WORK FAIL <id>: rule <n>: <reason>
WORK FAIL nodes=<n> findings=<n> shown=<n> expired=<n> stale=<n>
VERIFY OK pointers=<n> verified=<n> unverified=<n> stale=<n> fetched=<n> cached=<n> emitted=<bytes>
VERIFY ROW <event-id> pointer=<p> verdict=<verified|unverified|stale> at=<stamp>
VERIFY FAIL pointers=<n> unverified=<n> shown=<n>
QUERY OK ask=<kind> scope=<rev> membership=<rule> unit=<unit> source=<sha> freshest=<stamp> done=<n> done-unverified=<n> unknown=<n> stale=<n> rows=<n> shown=<n> emitted=<bytes>
QUERY ROW <id> kind=<k> state=<s> k=<n> n=<n> unknown=<u> responsible=<name|-> holder=<name|unowned> heartbeat=<age|none> deadline=<stamp|-> blocked-by=<id|->
QUERY FAIL ask=<kind>: <reason>
RENDER OK view=<id> cells=<n> bytes=<n> into=<path> emitted=<bytes>
RENDER FAIL view=<id> cells=<n> drifted=<n> into=<path>
LEASE OK id=<id> node=<id> holder=<name> deadline=<stamp> default=<d> live=<n> emitted=<bytes>
LEASE FAIL node=<id> holder=<name> since=<stamp> deadline=<stamp> live=<n>: held
HEARTBEAT OK id=<id> lease=<id> evidence=<pointer> emitted=<bytes>
RELEASE OK id=<id> lease=<id> handed=<name|-> live=<n> emitted=<bytes>
ATTEMPT OK id=<id> node=<id> by=<name> result=<pointer> generation=<n> attempts=<n> emitted=<bytes>
EVIDENCE OK id=<id> node=<id> criterion=<id> against=<sha> evidence=<n> emitted=<bytes>
STATE OK id=<id> node=<id> from=<s> to=<s> evidence=<n> emitted=<bytes>
STATE FAIL node=<id>: rule <n>: <reason>
CORRECT OK id=<id> node=<id> generation=<n> emitted=<bytes>
RESPONSIBLE OK id=<id> node=<id> to=<name> emitted=<bytes>
EVENT OK id=<id> kind=<k> node=<id> scope=<rev> events=<n> emitted=<bytes>
<TOKEN> NOTE <caveat>
<TOKEN> MORE kind=<rule|row> shown=<n> total=<t> <remedy>
nova-work <build identity> <goos>/<goarch> <go version>
```

Exit 0 the verb ran and passed; 1 it ran and said no (a finding, a refused take, a refused
transition, drift, an unverified pointer under `verify`); 2 it could not run (a missing flag,
a refused file, bounds exceeded, an unusable invocation, which costs one line ending `run:
nova-work help`). One line per event, escaped through `internal/oneline`; every listing
capped by `--max` with a `MORE` line naming the remedy; the version line is
`internal/buildinfo`'s.

## Acceptance replays *(shared; Glenn's list, 5653970526)*

Fixtures, each tiny, each a test: nested completion; a failed gate; a shared dependency
counted once; unknown evidence; unverified evidence changing no state; stale evidence
counted as not green only under `--strict`; a task split (both units printed); scope expansion
(added since baseline visible, revision moved, new rows at the bottom, `baseline-rows`
printed); deferral (cannot raise the done count); reopening; a correction bumping the
generation and an older attempt's result refused at `state --to done`; future work cannot
lower the active percentage; an out-of-scope cell leaving the applicable rows; a changed
nested task updates every affected view; `--at` replaying to an earlier revision; malformed
and cyclic data refused; a `#.` payload refused at the reader; a deep chain with no quadratic
work (visit counts asserted); a lease past its deadline reads as unowned, its responsibility
unchanged, and its release is not blocked; `:extend-once` once; an invalid transition
refused; a refused write leaving the file byte-identical; a hand-written discovery recorded by its
event in the only order that passes; a `:deps` cycle refused; a cancel request withdrawn and
a cancel confirmed; `render --check` fails on
one changed cell; full reconstruction and incremental replay produce identical output. The
stall replays of 5649089106 (a long live job, an unread PR hold, a silent worker, a repeated
review error, sustained divergence, landing mode) belong to stall detection, deferred below,
and are listed there so they are not lost.

## What this draft does not do

Stall detection and bounded recovery with its six replays (5649089106), `plan`/`apply`/
`reconcile` (5653982211), the GitHub issue intake adapter (Stella's section names its
contract; the adapter is its own spec), token and cost joins beyond the attempt's `:usage`
pointer (#175, #181), and the categories taxonomy (5654164074) are later revisions, each with
its issue. Nothing here deletes, migrates or publishes an issue. Known work is not authorized,
active, scheduled or public by being in S.

## Additions of the authors', not in the source

So a reader never mistakes them for Glenn's requirements: the lease model whole (Rowan's,
5654176537, and its three changes above) and its tool-owned random ids; the `:superseded`
state, the `:supersede` event and the `:cancel-requested` state; the one-check-of-the-candidate
write gate; rule 16 and the `note:`-never-for-done rule; counting an unverified or stale done
as unknown; the five indexes and the in-memory aggregate cache; `--cache <path>`; the exact list of refused reader syntax beyond
`#.` (every dispatch macro, `#'`, quote, backquote, package-prefixed symbols, ratios, floats,
characters); `;` comments discarded by the reader; the three bound flags and their no-default
rule; unknown keys preserved; the append-only write model and the post-write check that
removes its own event; deriving state, generation and scope revision from events;
`:required` defaulting to true; the `:review` state; `:responsible` and its inheritance
(Stella's point 4); the `file:` and `test:` pointer schemes and the generic `note:` scheme;
`:criterion` binding on evidence and `verify` as a separate pass with a cache (Stella's
points 1 and 2); `:clock :tool` with `--now` optional (Stella's point 3); the two-marker
region in `ROADMAP.md`; the `:repo` field on a top-level work set; the transition table's
exact edges; `<repo>/shared` as the owning set for shared work; `--at <revision>`;
`emitted=<bytes>` on every `OK` line. Stella's section carries its own authors' additions
(the pilot branch and sha, the prototype facts, the rate schedule and virtual cost, the
`NEXT-TOOLS.md` hand-off, and the fixed-table capability boundary) as hers. Each is open to be cut by the pilot.

## Roadmap as a view; the Schema pilot *(Stella)*

**Practice first, then retrospective, then production.** Glenn reaffirmed this sequence on
2026-09-13: dogfood the hierarchy on Fixed Tables, inspect what worked and what did not,
bring those findings into the tool specs while fresh, then implement. This draft records
hypotheses alongside settled requirements. Its existence does not start production work or
replace the remaining Fixed Tables acceptance gates.

### Durable primary state and GitHub intake

Glenn's chosen direction is that **S is the primary form**, with a persistent versioned
repository home. `mas-bandwidth/work` is his proposed location; creating or populating it is
separate from this draft. Local in-memory structures and indexes are rebuildable working
copies. Task identities, source links, events, decisions and evidence records must survive
process loss, bench changes and coordinator handoff through the durable history.

People may continue to file GitHub issues. Import is intake into S, keyed by the immutable
forge repository/issue identity so retries do not duplicate tasks. Keep the original link,
source revision or update marker, and import receipt. Decomposition, ownership and planning
then live in S. Later issue edits are observed changes to reconcile, never an unconditional
overwrite of the work tree. Issue text remains data and cannot assign authority, execute code
or grant permissions. Publishing a summary back to GitHub is a separate, explicit adapter
operation with a receipt; importing does not close, delete or rewrite the source issue.

Concurrent coordinators must detect revision conflicts rather than overwrite each other's
work. A checkpoint records the parent revision and change; after a conflict, read the new
head and reconcile against the same stable IDs. Network failure leaves a pending local
checkpoint whose sync status is visible. Reads should remain useful offline; stale or
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
that gap; the next survey adds the missing capability inventory and preserves the versioning
contract's individual rows for nested work. This is an incomplete imported baseline being
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
it does not yet implement this draft's counted forest, indexed repo/category queries, leases,
network evidence validation or incremental updates. Its AST-depth check occurs after the Lisp
reader, so it is not the production reader's pre-parse depth guarantee. The existing parser
and gate tests passing does not certify those missing properties.

### Retrospective required before production implementation

Use the method to carry the Fixed Tables work forward, including at least an inventory
correction, a completed cell, newly discovered work, a dependency or handoff, and a changed
focus. Record failures and repairs as they occur. At the retrospective, compare the same
questions and acceptance scope against the previous manual workflow, then change the spec.

Measure total tokens by friend/model/bench/repo and attempt where available, including source
survey, coordination, review and correction; distinguish unavailable measurement from zero.
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
