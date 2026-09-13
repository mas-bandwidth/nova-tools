# nova-work — specification (DRAFT 2, 2026-09-13)

**Status: a draft under joint authorship, Rowan and Stella, on Glenn's word of 2026-09-13.**
Nothing here is built. The Schema NEW Fixed Tables roadmap is the pilot, and the pilot decides
what this document keeps. Sections marked *(Stella)* are hers to write; sections marked
*(Rowan)* are mine; the rest is shared. Every requirement that is Glenn's cites the
nova-tools#177 comment it comes from, by id, so a reader can check the words. **Draft 2 is
draft 1 after a Fable cold read (HOLD, 2026-09-13 15:4xZ); the read's findings and their
repairs are recorded on PR #231.** Where this draft adds something the source does not say,
the section **Additions of the author's** at the end lists it, so nobody mistakes an author's
choice for Glenn's requirement.

**One recursive work set, S.** A file of restricted Lisp data holds what is **desired** (the
work: repositories, streams, features, tasks, down to whatever depth is useful) and what is
**observed** (leases, heartbeats, attempts, evidence, events). Everything **derived** — counts,
percentages, views, plans — is computed on read and never written into the file; a roadmap
table, an owner queue, a stream report and a percentage are each a **projection** of S at a
named scope revision, and none of them is a second store. `ROADMAP.md` is regenerated, never
edited; a hand-typed percentage is a bug (5653970526).

This spec is normative once it leaves draft. It is a sibling of [SPEC.md](SPEC.md), whose
**Conventions** — exit codes, no guessed paths, the one-line guarantee, the field escape, the
cap-and-count law, the version line — govern here unchanged. If the code and this document
disagree, one of them has a bug, and the tests decide which.

## The failures it closes

| the failure, from the record | what closes it |
|---|---|
| a task disappears after a context loss, a priority change or a handoff (#177) | the file is the store; a node's `:id` is stable through every rename and display change |
| two owners on one task, neither aware of the other (nova-board's 2026-09-10 morning) | one live **lease** per node; a second `take` is refused and names the holder |
| a stored owner read as "working on it" while nothing moves | there is no stored owner; *working-now* is a heartbeat inside a window (5654012267) |
| a percentage no evidence supports; an average of fractions rounded to green (5653970526) | done needs a resolvable evidence pointer; language completion is green cells / n, and cell progress shows its numerator and denominator |
| the denominator moved and nobody saw it | every event that changes a required set increments the scope revision; a baseline is an event; additions append at the bottom; removals carry a reason (5654160320, 5653990830) |
| studying S became pairwise work (5653973972, 5654049969) | counted containment is a forest, references are a graph; one fold is O(V+E) over three indexes; no transitive descendant sets are materialised |
| a deferral or a cancellation counted as progress | `:deferred` and `:cancelled` are states that never enter the done count, and moving work to Future is a scope transition, never achievement |
| the roadmap table edited by hand and the data left behind | `render --check` fails on drift; the table lives between two markers and the file owns it |

## The data *(Rowan)*

**Restricted Lisp, read as data.** A work file is a sequence of s-expressions made only of
lists, keywords, strings and integers. The reader refuses, at exit 2 with one line naming the
byte offset, every dispatch macro (`#.` first among them) and every other form the source
forbids as evaluation (5653982211); the full list of refused syntax is an author's addition,
listed at the end. Nothing read is ever evaluated. **Every verb that reads a file takes the
same three bounds**, `--max-bytes <n> --max-depth <n> --max-nodes <n>`, none defaulted: a file
past any of them is refused at exit 2 before parsing finishes, and a missing bound is `refusing
to guess`. Unknown keys on a node are preserved and ignored, so a team may carry its own
fields; an unknown `:type` is a refusal, because a type names the rules a node is checked by
(5653990830).

**A node.**

```lisp
(:id "schema/fixed-tables/versioning/cpp"   ; stable, never reused, never carries display text
 :type :work-set                             ; the kinds are listed below
 :title "C++ versioning"                     ; display only; may change freely
 :children ("schema/cpp/read-older"          ; COUNTED CONTAINMENT: this node owns these
            "schema/cpp/refuse-newer")
 :deps ("schema/shared/lock-rules")          ; REFERENCE: needed, not owned, not counted here
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
one task with one cost.

**Kinds.** Features, tasks, attempts and leaf subtasks are distinct units (5654012267), so
they are distinct kinds:

- `:work-set` — a container. Its completion is its required children's completion; an empty
  required set is **never** done.
- `:feature` — a work-set whose completion is what a roadmap row counts. A feature has
  sub-features and cells beneath it; it is the unit of "green feature cells / n".
- `:roadmap` — a typed view over its cells: `:axes` (ordered, named members), `:cells` mapping
  a coordinate to a `:ref`, `:scope-revision`, `:source-revision` (the tree the evidence was
  read against), `:completion-policy` (`:all-required-features` is the only policy in draft 2).
  A cell references a node; it never contains state of its own. An unknown axis member, a
  duplicate coordinate and a missing required cell are refusals; an omitted cell is never
  complete (5653990830). Cells may be marked `:out-of-scope`, which is distinct from unstarted
  and from unknown. Adding an axis member or a feature is a scope revision; removing one is not
  completion (5653990830).
- `:task` — work with a `:state`, optional `:children` (required sub-work), `:required`,
  `:evidence` (pointers), `:acceptance` (pointers to the tests that close it), and, when the
  state is `:blocked`, a required `:blocked-reason` and the `:blocked-by` reference
  (5653970526). A task has **no stored owner**: its owner is derived from leases, and only
  from leases.
- `:attempt` — the observed record of one try at a task: `:on`, `:by`, `:model`, `:bench`,
  `:started`, `:ended`, `:result` (a pointer), `:usage` (a pointer to a token record, #181),
  `:generation` (the correction generation it answered, 5653982211). An attempt never changes
  a task's state by itself; a `done` verb does, citing the attempt's result as evidence.
- `:lease` — the ownership record, below.
- `:event` — an append-only record of a scope change: `:kind` in `:baseline :discovery :remove
  :defer :reopen :split :scope`, `:at <revision>`, `:node`, `:reason`, `:by`, `:stamp`. A
  baseline event records the required set of its node **as the tool computed it at that
  moment**, member by member. Splitting is decomposition, not discovery or completion
  (5653970526).

**States and transitions.** `:unknown | :todo | :doing | :blocked | :review | :done |
:deferred | :cancelled | :superseded`. `:unknown` is explicit and is neither zero nor
not-started (5653970526). The allowed transitions are a table the validator holds: from
`:todo` to `:doing`, `:blocked`, `:deferred`, `:cancelled`; from `:doing` to `:blocked`,
`:review`, `:done`, `:deferred`, `:cancelled`; from `:blocked` to `:doing`, `:deferred`,
`:cancelled`; from `:review` to `:doing`, `:done`; from `:done` to `:doing` only by a
`reopen` event; from `:deferred` to `:todo` by a `reopen` event; from `:unknown` to any
state by a write that carries evidence or a reason; `:superseded` and `:cancelled` are
terminal. `:done` requires at least one evidence pointer, and every pointer must resolve. A
node in `:done` that also carries a `:blocked-reason` is an incompatible state, and a
finding. `:deferred`, `:cancelled` and `:superseded` leave the required set for counting and
stay in the file with their event.

**Evidence is a pointer the validator can fetch**, never a sentence. Draft 2 ships five
schemes — `commit:<sha>`, `run:<owner/repo>#<id>`, `pr:<owner/repo>#<n>@<sha>`,
`file:<path>@<sha>`, `test:<package>/<name>@<sha>` — and a sixth, `note:<scheme>:<id>`, for
any team's message store, so that no family's bus is named in the tool (5653970526). A
pointer that does not resolve makes its node `:unknown`, never `:done`. Evidence carries the
revision it was read against; a later `:source-revision` does not invalidate it by itself,
and a cell whose `:source-revision` is older than an evidence pointer it cites is a
conflicting revision and a finding (5653970526: *reject … conflicting revisions*). The
query output prints evidence freshness beside the count so a reader can see proof older than
the tree (*a merge is not automatic completion; source changes can invalidate old proof*).

**The lease.** *(from #177 comment 5654176537; rules 9 and 10 below enforce it)*

```lisp
(:id "schema/fixed-tables/versioning/cpp#lease-3"
 :type :lease
 :on "schema/fixed-tables/versioning/cpp"
 :owner "emma"
 :taken "2026-09-13T14:41:11Z"       ; pasted from a clock, never typed
 :deadline "2026-09-13T21:00:00Z"
 :default :release                   ; :release | :extend-once | (:escalate "<owner>")
 :evidence ("note:bus:emma-841138a3b056")
 :heartbeat "2026-09-13T15:02:00Z"   ; the newest live evidence; absent means none
 :state :live)                       ; :live | :released | :handed
```

A lease never expires into done. **Expiry is derived, never stored**: a lease whose deadline
is behind `--now` reads as expired in every answer, the node reads as unowned, and `check`
counts it on its count line (`expired=<n>`) rather than failing on it, so a write that retires
an expired lease is never blocked by the lease it retires. Working-now means a heartbeat
inside `--window`; a live lease with no heartbeat in the window is *held, not worked*, and the
answer says so. One live lease per node; a handoff is `:handed` on the old and a new lease on
the new owner, so the transition is a record and not an overwrite. A lease with no `:deadline`
or no `:default` is refused at write time: a deadline with no default is a wait with no end.

**The root.** Every top-level child of S is a repository work set, `(:type :work-set :repo
"<owner>/<name>")`, including repositories that hold research, planning or a friend's own
work; a cross-repository goal is a view over canonical owned nodes, never a copy
(5654164074, Glenn's preferred simplification, *to be prototyped before it is an invariant*).
S is the team's authorized known work, never a scan of every reachable repository. A GitHub
issue or PR is a `:links` entry on a node, not the node's type.

**Scope, baseline, focus** (5654160320). A roadmap or work set carries `:scope-revision`, an
integer that **increments on every event that changes its required set**: `:baseline`,
`:discovery`, `:remove`, `:defer`, `:reopen`, `:split` and `:scope` alike, so a denominator
cannot move without the revision moving with it. The first `:baseline` event records the
initial membership before execution; later discoveries **append at the bottom of every
rendered listing in discovery order**, reported as *added since baseline*, and reprioritising
execution never reorders the baseline rows; a removal carries a reason and never lowers a
count silently. **Focus** is a query (a node id, a repo, a category, an owner), not a copy: it
selects a subtree or a membership view over the same nodes, so identity, dependencies,
ownership and evidence are the same in every view, and a dependency outside the focus that
blocks it is reported.

## Counting *(shared; Glenn's rules verbatim where they are his)*

- **Language completion** on a roadmap = `100 * green feature cells / n active feature rows`
  for that language. *Partial cells do not contribute fractions of a completed feature to
  this number* (5653970526).
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
- **Unknown is a count of its own**, printed on every answer.

## Queries — the contract *(Rowan; Glenn's list from 5654012267)*

Every answer is one `QUERY OK` scope line, then one `QUERY ROW` line per fact, capped and
counted. The scope line always carries: `scope=<revision> membership=<rule> unit=<unit>
source=<sha> freshest=<stamp> unknown=<n> emitted=<bytes>`.

| ask | answers |
|---|---|
| `done --node X` / `remaining --node X` | completed and outstanding required work under X, by kind, capped and counted |
| `who --node X --window <dur>` | live leases on X and beneath it: owner, heartbeat age, deadline, default; then `held-not-worked` and `unowned` counts |
| `percent --node R --axis <member>` | the roadmap rollup for one axis member, with `green=<k> rows=<n>` and every partial cell's `k/n` |
| `size` / `size --node X` | total required leaves, done, unknown, deferred, since-baseline |
| `stream --repo <owner/name>` / `--owner <name>` | the same, for one repository or one friend's own selected work |
| `under --repo <owner/name> --category <label>` | compact listing of nodes by category with state (5654164074; taxonomy TBD) |
| `stale --window <dur>` | leases past deadline or past the heartbeat window, grouped by owner |
| `handoffs --since <revision>` | the lease transition log |

## The verbs *(Rowan; a draft shape, to be cut by the pilot)*

Common to every verb that reads a file, written once here: `--file <S.sexp> --max-bytes <n>
--max-depth <n> --max-nodes <n>`, none defaulted. Common to every verb that writes: `--as
<name> --now <stamp>`, because a stamp the tool reads from its own clock is a stamp nobody
can replay; the caller pastes one. Every listing verb takes `--max <n>`, default 20, `0`
means all, negative refused (SPEC.md, the cap-and-count law).

```
nova-work check     <file flags> [--max <n>]
nova-work query     <file flags> --ask <kind> [--node <id>] [--repo <o/n>] [--owner <name>] [--category <label>]
                    [--axis <member>] [--window <duration>] [--since <revision>] --now <stamp> [--max <n>]
nova-work render    <file flags> --view <roadmap-id> --into <path> --start <marker> --end <marker> --now <stamp> [--check]
nova-work take      <file flags> <write flags> --node <id> --by <duration|stamp> --default <release|extend-once|escalate:<name>>
nova-work heartbeat <file flags> <write flags> --node <id> --evidence <pointer>
nova-work release   <file flags> <write flags> --node <id> [--handed <name>]
nova-work attempt   <file flags> <write flags> --node <id> --model <name> --bench <name> --result <pointer> [--usage <pointer>] [--generation <n>]
nova-work state     <file flags> <write flags> --node <id> --to <state> (--evidence <pointer> ... | --reason <text>) [--blocked-by <id>]
nova-work event     <file flags> <write flags> --kind <baseline|discovery|remove|defer|reopen|split|scope> --node <id> --reason <text>
nova-work version
nova-work help
```

**What writes change, stated exactly.** No verb ever edits or deletes a node's `:id`,
`:type`, `:children` or `:deps`: identity and containment are edited by hand, in the file,
under review, and `check` is the gate. The verbs above append events, attempts and leases,
and update exactly these fields on existing nodes: `state` sets a task's `:state`,
`:evidence`, `:blocked-reason` and `:blocked-by`; `heartbeat` sets a lease's `:heartbeat`
and appends to its `:evidence`; `release` sets a lease's `:state`. Every write runs `check`
before and after, and a finding on either side refuses the write at exit 1 with the finding's
line; expired leases are counts, never findings, so they never block. `plan`, `apply` and
`reconcile` (5653982211, the Terraform half) are **not in draft 2**: named here so a reader
knows they are deferred, with their own section once the pilot has shown what a plan must name.

## The validator *(Rowan; every rule is a hurt already paid)*

`check` walks S once and prints one `WORK FAIL` line per finding, capped **per rule**, and one
count line always. Exit 1 on any finding. Rules 13 and 14 are the reader's, not the
validator's: they refuse at exit 2 before any rule below runs.

1. **duplicate id** — two nodes with one `:id`.
2. **dangling reference** — a `:children`, `:deps`, cell `:ref`, `:on`, `:blocked-by` or event `:node` that names no node.
3. **containment cycle** — `:children` edges are not a forest.
4. **two parents** — a node under two `:children` lists.
5. **done without evidence** — `:done` with no pointer, or a pointer that does not resolve.
6. **green parent, unfinished child** — a parent `:done` while a required child is not.
7. **empty required set** — a `:work-set`, a `:feature` or a cell with no required work marked done.
8. **bad cell** — an unknown axis member, a duplicate coordinate, a missing required cell.
9. **two live leases** on one node.
10. **lease without deadline or default.**
11. **invalid transition or incompatible state** — a `:state` the transition table does not allow from the node's last recorded state, or `:done` beside a `:blocked-reason`, or `:blocked` without one (5653982211).
12. **scope change without event** — the required set of a roadmap or work set differs from its last `:baseline` plus recorded events, or its `:scope-revision` did not move with them.
13. *(reader, exit 2)* **reader payload** — `#.` or any other refused syntax.
14. *(reader, exit 2)* **bounds exceeded** — bytes, depth or nodes past the flags.
15. **conflicting revisions** — a cell's `:source-revision` older than an evidence pointer it cites, or two nodes claiming different revisions for one baseline (5653970526).

## Cost *(shared; 5653973972 and 5654049969)*

The tool builds three indexes on every read — id to node, containment adjacency, and
reverse dependency — and every walk goes through them. A full `check` or fold visits every
node and every edge once: O(V+E), with a visited set for shared subgraphs, and detects
cycles in the same walk. Counts roll up bottom-up over the containment forest in O(V). **No
transitive descendant set is materialised anywhere**; an ad hoc set query walks the reached
subgraph once, O(V_reached + E_reached), when it is asked. Rendering a matrix costs its cell
count. A dense dependency graph has a large E, and the tool prints `edges=<n>` rather than
promising otherwise; every `OK` line prints `emitted=<bytes>` so the cost of the answer is
part of the answer. Incremental update after a changed leaf invalidates only affected
ancestors and dependents and recomputes in dependency order; no constant-time promise.
**Tests assert visit and allocation counts, never wall time**, on four shapes: tree, shared
DAG, deep chain, high fan-out.

## Output grammar

Every line's first token is the verb's (`WORK`, `QUERY`, `RENDER`, `LEASE`, `ATTEMPT`,
`STATE`, `EVENT`), the second is `OK` or `FAIL`, or one of the informational tokens `ROW`,
`NOTE` and `MORE`. `OK`, `ROW`, `NOTE` and `MORE` go to stdout; `FAIL` and refusals go to
stderr. Every count line prints on failure as on success.

```
WORK OK nodes=<n> edges=<n> leases=<n> expired=<n> events=<n> scope=<rev> source=<sha> emitted=<bytes>
WORK FAIL <id>: rule <n>: <reason>
WORK FAIL nodes=<n> findings=<n> shown=<n> expired=<n>
QUERY OK ask=<kind> scope=<rev> membership=<rule> unit=<unit> source=<sha> freshest=<stamp> unknown=<n> rows=<n> shown=<n> emitted=<bytes>
QUERY ROW <id> kind=<k> state=<s> k=<n> n=<n> owner=<name|unowned> heartbeat=<age|none> deadline=<stamp|->
QUERY FAIL ask=<kind>: <reason>
RENDER OK view=<id> cells=<n> bytes=<n> into=<path>
RENDER FAIL view=<id> cells=<n> drifted=<n> into=<path>
LEASE OK id=<id> on=<node> owner=<name> deadline=<stamp> default=<d> live=<n>
LEASE FAIL on=<node>: held by <name> since <stamp> (deadline <stamp>) live=<n>
ATTEMPT OK id=<id> on=<node> by=<name> result=<pointer> attempts=<n>
STATE OK id=<id> from=<s> to=<s> evidence=<n>
STATE FAIL id=<id>: rule <n>: <reason>
EVENT OK id=<id> kind=<k> node=<id> scope=<rev> events=<n>
<TOKEN> NOTE <caveat>
<TOKEN> MORE kind=<rule|row> shown=<n> total=<t> <remedy>
nova-work <build identity> <goos>/<goarch> <go version>
```

Exit 0 the verb ran and passed; 1 it ran and said no (a finding, a refused take, a refused
transition, drift); 2 it could not run (a missing flag, a refused file, bounds exceeded, an
unusable invocation, which costs one line ending `run: nova-work help`). One line per event,
escaped through `internal/oneline`; every listing capped by `--max` with a `MORE` line naming
the remedy; the version line is `internal/buildinfo`'s.

## Acceptance replays *(shared; Glenn's list, 5653970526)*

Fixtures, each tiny, each a test: nested completion; a failed gate; a shared dependency
counted once; unknown evidence; a task split; scope expansion (added since baseline visible,
revision moved, new rows at the bottom); deferral (cannot raise the done count); reopening;
future work cannot lower the active percentage; a changed nested task updates every affected
view; malformed and cyclic data refused; a `#.` payload refused at the reader; a deep chain
with no quadratic work (visit counts asserted); a lease past its deadline reads as unowned and
its release is not blocked; an invalid transition refused; `render --check` fails on one
changed cell; full reconstruction and incremental replay produce identical output. The stall
replays of 5649089106 (a long live job, an unread PR hold, a silent worker, a repeated review
error, sustained divergence, landing mode) belong to stall detection, deferred below, and are
listed there so they are not lost.

## What this draft does not do

Stall detection and bounded recovery with its six replays (5649089106), `plan`/`apply`/
`reconcile` (5653982211), a GitHub issue sync adapter, token and cost joins beyond the
attempt's `:usage` pointer (#175, #181), and the categories taxonomy (5654164074) are later
revisions, each with its issue. Nothing here deletes, migrates or publishes an issue. Known
work is not authorized, active, scheduled or public by being in S.

## Additions of the author's, not in the source *(Rowan)*

So a reader never mistakes them for Glenn's requirements: the exact list of refused reader
syntax beyond `#.` (every dispatch macro, `#'`, quote, backquote, package-prefixed symbols,
ratios, floats, characters); the three bound flags and their no-default rule; unknown keys
preserved; `:required` defaulting to true; the `:review` and `:superseded` states; the
`file:` and `test:` pointer schemes and the generic `note:` scheme; the two-marker region in
`ROADMAP.md`; the `:repo` field on a top-level work set; `check` before and after every
write; the transition table's exact edges; `<repo>/shared` as the owning set for shared
work; `emitted=<bytes>` on every `OK` line. Each is open to be cut by the pilot.

## Roadmap as a view; the Schema pilot *(Stella)*

*Stella's section: the feature inventory contract (her data contract of 2026-09-13 14:52Z, the
23 rows and the historical aliases as references), what the renderer proves, the acceptance
mapping from schema#898, and the measurement that decides whether S saved tokens against
hand-kept tables.*

## The measurement that decides *(shared)*

Before this leaves draft, on the Fixed Tables roadmap: tokens and wall time to update one
fact in S (`state --to done` with its evidence) and regenerate the table, against editing
the table by hand; whether "who is on the C leg" is answerable from leases alone without
reading the bus; whether a reader of the generated table found a number the source did not
support. The benefit is observed or the tool is not built (5653982211).
