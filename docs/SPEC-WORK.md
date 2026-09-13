# nova-work — specification (DRAFT 1, 2026-09-13)

**Status: a draft under joint authorship, Rowan and Stella, on Glenn's word of 2026-09-13.**
Nothing here is built. The Schema NEW Fixed Tables roadmap is the pilot, and the pilot decides
what this document keeps. Sections marked *(Stella)* are hers to write; sections marked
*(Rowan)* are mine; the rest is shared. Every requirement that is Glenn's cites the
nova-tools#177 comment it comes from, by id, so a reader can check the words.

**One recursive work set, S.** A file of restricted Lisp data holds what is **desired** (the
work: repositories, streams, features, tasks, down to whatever depth is useful), what is
**observed** (leases, heartbeats, attempts, evidence) and what is **derived** (counts, plans,
views). A roadmap table, an owner queue, a stream report and a percentage are each a
**projection** of S at a named scope revision, and none of them is a second store. `ROADMAP.md`
is regenerated, never edited; a hand-typed percentage is a bug (5653970526).

This spec is normative once it leaves draft. It is a sibling of [SPEC.md](SPEC.md), whose
**Conventions** — exit codes, no guessed paths, the one-line guarantee, the field escape, the
cap-and-count law — govern here unchanged. If the code and this document disagree, one of them
has a bug, and the tests decide which.

## The failures it closes

| the failure, from the record | what closes it |
|---|---|
| a task disappears after a context loss, a priority change or a handoff (#177) | the file is the store; a node's `:id` is stable through every rename and display change |
| two owners on one task, neither aware of the other (nova-board's 2026-09-10 morning) | one live **lease** per node; a second `take` is refused and names the holder |
| a stored owner read as "working on it" while nothing moves | *working-now* is a heartbeat inside a window, never an assignment (5654012267) |
| a percentage no evidence supports; an average of fractions rounded to green (5653970526) | done needs a resolvable evidence pointer; language completion is green cells / n, and cell progress shows its numerator and denominator |
| the denominator moved and nobody saw it | scope is a revision; a baseline is an event; additions append; removals carry a reason (5654160320) |
| studying S became pairwise work (5653973972, 5654049969) | counted containment is a forest, references are a graph; one fold is O(V+E); no transitive descendant sets are materialised |
| a deferral or a cancellation counted as progress | `:deferred` and `:cancelled` are states that never enter the done count, and moving work to Future is a scope transition, never achievement |
| the roadmap table edited by hand and the data left behind | `render --check` fails on drift; the table lives between two markers and the file owns it |

## The data *(Rowan)*

**Restricted Lisp, read as data.** A work file is a sequence of s-expressions made only of
lists, keywords, strings and integers. The reader refuses, at exit 2 with one line naming the
byte offset, anything else: `#.` and every other dispatch macro, `#'`, quote and backquote,
symbols with a package prefix, ratios, floats, characters. Nothing read is ever evaluated.
Three bounds are flags, not defaults: `--max-bytes`, `--max-depth`, `--max-nodes`; a file past
any of them is refused before parsing finishes. Unknown keys on a node are preserved and
ignored, so a team may carry its own fields; an unknown `:type` is a refusal, because a type
names the rules a node is checked by (5653990830).

**A node.**

```lisp
(:id "schema/fixed-tables/versioning/cpp"   ; stable, never reused, never carries display text
 :type :work-set                             ; :work-set | :roadmap | :task | :lease | :event
 :title "C++ versioning"                     ; display only; may change freely
 :children ("schema/cpp/read-older"          ; COUNTED CONTAINMENT: this node owns these
            "schema/cpp/refuse-newer")
 :deps ("schema/shared/lock-rules")          ; REFERENCE: needed, not owned, not counted here
 :category "feature")                        ; free label; taxonomy TBD (5654164074)
```

**Containment and reference are two different edges, and the difference is the whole cost
model** (5654049969). `:children` is canonical containment: every node has at most one
containment parent, the containment edges form a forest, and a node is **counted once**, under
that parent, wherever else it is referenced. `:deps`, a roadmap cell's `:ref`, a view's
membership and any other pointer are references: they form a graph, they carry no count and no
cost, and they are validated for existence and for dependency state. Shared compiler and lock
work in Schema is owned once and referenced from nine cells; it is one task with one cost.

**Kinds.**

- `:work-set` — a container. Its completion is its required children's completion; an empty
  required set is **never** done.
- `:roadmap` — a typed view over its cells: `:axes` (ordered, named members), `:cells` mapping
  a coordinate to a `:ref`, `:scope-revision`, `:source-revision` (the tree the evidence was
  read against), `:completion-policy` (`:all-required-features` is the only policy in draft 1).
  A cell references a node; it never contains state of its own. An unknown axis member, a
  duplicate coordinate and a missing required cell are refusals; an omitted cell is never
  complete (5653990830). Cells may be marked `:out-of-scope`, which is distinct from unstarted
  and from unknown.
- `:task` — work with a `:state`, optional `:children` (required sub-work), `:required` (default
  true), `:evidence` (pointers) and `:owner` (derived from leases; a stored `:owner` with no
  live lease is reported as *assigned, not worked*).
- `:lease` — the ownership record, below.
- `:event` — an append-only record of a scope change: `:kind` in `:baseline :discovery :remove
  :defer :reopen :split :scope`, `:at <revision>`, `:node`, `:reason`, `:by`, `:stamp`. A
  baseline names its members. Splitting is decomposition, not discovery or completion
  (5653970526).

**States.** `:unknown | :todo | :doing | :blocked | :review | :done | :deferred | :cancelled |
:superseded`. `:unknown` is explicit and is neither zero nor not-started (5653970526). `:done`
requires at least one evidence pointer, and every pointer must resolve. `:deferred`,
`:cancelled` and `:superseded` leave the required set for counting and stay in the file with
their event.

**Evidence is a pointer the validator can fetch**, never a sentence: `commit:<sha>`,
`run:<owner/repo>#<id>`, `pr:<owner/repo>#<n>@<sha>`, `bus:<note-id>`, `file:<path>@<sha>`,
`test:<package>/<name>@<sha>`. A pointer that does not resolve makes its node `:unknown`, never
`:done`. Evidence carries the revision it was read against; a later `:source-revision` does not
invalidate it by itself, but the query output prints evidence freshness beside the count so a
reader can see proof older than the tree (5653970526: *a merge is not automatic completion;
source changes can invalidate old proof*).

**The lease.** *(from #177 comment 5654176537; rules 9 to 11 below enforce it)*

```lisp
(:id "schema/fixed-tables/versioning/cpp#lease-3"
 :type :lease
 :on "schema/fixed-tables/versioning/cpp"
 :owner "emma"
 :taken "2026-09-13T14:41:11Z"       ; pasted from a clock, never typed
 :deadline "2026-09-13T21:00:00Z"
 :default :release                   ; :release | :extend-once | (:escalate "<owner>")
 :evidence ("bus:emma-841138a3b056")
 :heartbeat "2026-09-13T15:02:00Z"   ; the newest live evidence; absent means none
 :state :live)                       ; :live | :released | :expired | :handed
```

A lease never expires into done: past its deadline with no completion evidence it reads as
`:expired` and the node as unowned. Working-now means a heartbeat inside `--window`; a live
lease with no heartbeat in the window is *held, not worked*, and the answer says so. One live
lease per node; a handoff is `:handed` on the old and a new lease on the new owner, so the
transition is a record and not an overwrite. A lease with no `:deadline` or no `:default` is
refused at write time: a deadline with no default is a wait with no end.

**The root.** Every top-level child of S is a repository work set, `(:type :work-set :repo
"<owner>/<name>")`, including repositories that hold research, planning or a friend's own
work; a cross-repository goal is a view over canonical owned nodes, never a copy
(5654164074, Glenn's preferred simplification, *to be prototyped before it is an invariant*).
S is the team's authorized known work, never a scan of every reachable repository. A GitHub
issue or PR is a link on a node, not the node's type.

**Scope, baseline, focus** (5654160320). A roadmap or work set carries `:scope-revision`, an
integer that increments on every event of kind `:scope`. The first `:baseline` event records
the initial membership before execution; later discoveries append and are reported as *added
since baseline*; a removal carries a reason and never lowers a count silently. **Focus** is a
query (a node id, a repo, a category, an owner), not a copy: it selects a subtree or a
membership view over the same nodes, so identity, dependencies, ownership and evidence are
the same in every view, and a dependency outside the focus that blocks it is reported.

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

Every answer is one line per fact plus one scope line, and the scope line always carries:
`scope=<revision> membership=<rule> unit=<unit> source=<sha> freshest=<stamp> unknown=<n>`.

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

```
nova-work check   --file <S.sexp> --max-bytes <n> --max-depth <n> --max-nodes <n> [--max <n>]
nova-work query   --file <S.sexp> --ask <kind> [--node <id>] [--repo <o/n>] [--owner <name>] [--category <label>]
                  [--axis <member>] [--window <duration>] [--since <revision>] [--max <n>]
nova-work render  --file <S.sexp> --view <roadmap-id> --into <path> --start <marker> --end <marker> [--check]
nova-work take    --file <S.sexp> --as <name> --node <id> --by <duration|stamp> --default <release|extend-once|escalate:<name>> --now <stamp>
nova-work heartbeat --file <S.sexp> --as <name> --node <id> --evidence <pointer> --now <stamp>
nova-work release --file <S.sexp> --as <name> --node <id> --now <stamp> [--handed <name>]
nova-work event   --file <S.sexp> --as <name> --kind <discovery|remove|defer|reopen|split|scope|baseline> --node <id> --reason <text> --now <stamp>
nova-work help
```

Every path is a flag; there is no default file. `--now` is required on every write, because a
stamp the tool reads from its own clock is a stamp nobody can replay; the caller pastes one.
Writes are appends to the file's event log and lease list; no verb edits or deletes a node,
and `check` is run by every writing verb before and after its write. `plan`, `apply` and
`reconcile` (5653982211, the Terraform half) are **not in draft 1**: they are named here so a
reader knows they are deferred and not forgotten, and they get their own section when the
pilot has shown what a plan must name.

## The validator *(Rowan; every rule is a hurt already paid)*

`check` walks S once and prints one `WORK FAIL` line per finding, capped, and one count line
always. Exit 1 on any finding.

1. **duplicate id** — two nodes with one `:id`.
2. **dangling reference** — a `:children`, `:deps`, cell `:ref`, `:on` or event `:node` that names no node.
3. **containment cycle** — `:children` edges are not a forest.
4. **two parents** — a node under two `:children` lists.
5. **done without evidence** — `:done` with no pointer, or a pointer that does not resolve.
6. **green parent, unfinished child** — a parent `:done` while a required child is not.
7. **empty required set** — a `:work-set` or a cell with no required work marked done.
8. **bad cell** — an unknown axis member, a duplicate coordinate, a missing required cell.
9. **two live leases** on one node.
10. **lease without deadline or default.**
11. **lease past deadline still live** — reported as `:expired` in every answer; the file is not rewritten by `check`.
12. **scope change without event** — the required set of a roadmap differs from its last `:baseline` plus recorded events.
13. **reader payload** — `#.` or any evaluated form; refused at the reader, exit 2, never at the validator.
14. **bounds exceeded** — bytes, depth or nodes past the flags.
15. **conflicting revisions** — a cell's `:source-revision` older than an evidence pointer it cites, printed as a warning line, not a failure.

## Cost *(shared; 5653973972 and 5654049969)*

A full `check` or fold visits every node and every edge once: O(V+E), with a visited set for
shared subgraphs, and detects cycles in the same walk. Counts roll up bottom-up over the
containment forest in O(V). **No transitive descendant set is materialised anywhere**; an
ad hoc set query walks the reached subgraph once, O(V_reached + E_reached), when it is asked.
Rendering a matrix costs its cell count. A dense dependency graph has a large E, and the
tool prints E rather than promising otherwise. Incremental update after a changed leaf
invalidates only affected ancestors and dependents and recomputes in dependency order; no
constant-time promise. **Tests assert visit and allocation counts, never wall time**, on four
shapes: tree, shared DAG, deep chain, high fan-out.

## Output grammar

```
WORK OK nodes=<n> edges=<n> leases=<n> events=<n> scope=<rev> source=<sha>
WORK FAIL <id>: <rule number> <reason>
WORK FAIL nodes=<n> findings=<n> shown=<n>
WORK WARN <id>: <reason>
QUERY OK ask=<kind> scope=<rev> membership=<rule> unit=<unit> source=<sha> freshest=<stamp> unknown=<n>
QUERY <kind> <id> state=<s> k=<n> n=<n> owner=<name|unowned> heartbeat=<age|none> ...
RENDER OK view=<id> cells=<n> bytes=<n> into=<path>
RENDER FAIL <path>: drift (<n> lines differ)
LEASE OK id=<id> on=<node> owner=<name> deadline=<stamp> default=<d>
LEASE REFUSED on=<node>: held by <name> since <stamp> (deadline <stamp>)
EVENT OK id=<id> kind=<k> node=<id> scope=<rev>
<TOKEN> MORE kind=<kind> shown=<n> total=<t> <remedy>
```

Exit 0 the verb ran and passed; 1 it ran and said no (a finding, a refused take, drift); 2 it
could not run. One line per event, escaped through `internal/oneline`; every listing capped by
`--max` with a `MORE` line naming the remedy; the count line prints on failure as on success.

## Acceptance replays *(shared; Glenn's list, 5653970526 and 5649089106)*

Fixtures, each tiny, each a test: nested completion; a failed gate; a shared dependency
counted once; unknown evidence; a task split; scope expansion (added since baseline visible);
deferral (cannot raise the done count); reopening; future work cannot lower the active
percentage; a changed nested task updates every affected view; malformed and cyclic data
refused; a `#.` payload refused at the reader; a deep chain with no quadratic work (visit
counts asserted); a long live job with a fresh heartbeat is not a stall; a lease past its
deadline reads as unowned; `render --check` fails on one changed cell; full reconstruction and
incremental replay produce identical output.

## What this draft does not do

Stall detection and bounded recovery (5649089106), `plan`/`apply`/`reconcile` (5653982211),
a GitHub issue sync adapter, token and cost joins (#175, #181) and the categories taxonomy
(5654164074) are later revisions, each with its issue. Nothing here deletes, migrates or
publishes an issue. Known work is not authorized, active, scheduled or public by being in S.

## Roadmap as a view; the Schema pilot *(Stella)*

*Stella's section: the feature inventory contract (her data contract of 2026-09-13 14:52Z, the
23 rows and the historical aliases as references), what the renderer proves, the acceptance
mapping from schema#898, and the measurement that decides whether S saved tokens against
hand-kept tables.*

## The measurement that decides *(shared)*

Before this leaves draft, on the Fixed Tables roadmap: tokens and wall time to update one
fact in S and regenerate the table, against editing the table by hand; whether "who is on the
C leg" is answerable from leases alone without reading the bus; whether a reader of the
generated table found a number the source did not support. The benefit is observed or the
tool is not built (5653982211).
