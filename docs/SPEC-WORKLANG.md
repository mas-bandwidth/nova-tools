# nova-work — a work description language (SPEC-WORKLANG)

Glenn, 2026-09-17: *"Maybe the ultimate pattern is that we just describe the work to be done in
some high level language, and then feed this to each bench machine, and they chew on it."* This
file is that language: a **plan** describes a goal, a forest of nodes, and the contract each card
carries; the **kernel expands it into cards** and every bench pulls a ready node — pulled, never
assigned. The plan is data the kernel reads, never a program it evaluates. No code here: reader,
expander and card printer are contracts, the tests are named, the shapes are the deliverable.

## Part 1 — what the language must say (the contract)

A plan has exactly three top-level forms: one `:goal`, one or more `:node`s (or a `:derive` that
expands to nodes), and one `:clip` policy. Everything else is derived. The kernel closes the graph,
orders it, prints one card per node; a bench needs no coordinator to read that card.

**The goal, with its acceptance.** The goal is an O node of `:type :work-set`, written to the `goal`
index by reference, carrying `:id`, `:title`, and `:acceptance` in the one schema the work spec
already fixes: `(:id "c1" :kind :test|:job|:merged|:attested :subject "<thing>" :predicate
:passes|:succeeds|:merged-at|:attested-by)`. A goal with no acceptance is refused at load — a plan
naming no evidence names no finish line.

**Nodes, with needs and blocks.** Each node carries `:id` (stable, never reused, never display text)
and `:under` (its containment parent, or `(:open-root)`). `:needs` is the reference edge the work
spec calls `:deps`; `:blocks` is its inverse; the kernel derives the one not given. **A node is ready
only when every need is terminal accepted** (settled after merging and going green) and no reverted
need flagged it `needs-broken`; an open-PR need blocks readiness, not admission. A `:needs` cycle is
refused at load by rule 3, an absent need refused with field and offset, never silently dropped.

**Each node's kind.** `:kind` is one of exactly `go-fix`, `lisp-replay`, `docs`, `schema-leg`,
`audit`, `fold` — the six card classes. The kind fixes the card template, the output gates, and the
route class; an unknown kind is refused at load, never guessed (unknown keys are preserved and
ignored, as O already does). A container is `:epic` or `:work-set`, expanding to its children's cards.

**Repo and base.** `:repo "<owner>/<name>"` names the clone; `:base "<ref>"` names the ref the card
pins before it starts; both required on a leaf, absent on a container. The worker must see the base
from `git rev-parse HEAD`; a moved base is `BLOCKED` naming both heads, never a silent re-pin.

**Pinned inputs.** `:inputs` is the closed list of what the card may read: `(:spec "docs/SPEC-WORK.md:2110-2114")`,
`(:issue "mas-bandwidth/schema#898")`, `(:file "lisp/nova-work/src/session.lisp")`, `(:artifact "<node>")`.
A card reading a file it did not name is malformed; cost is the sum of the named inputs and nothing else.

**The output contract.** `:output` names three things. `:result` is the literal first line the card
prints, `{node}` and `{verdict}` substituted — `RESULT: {node} {verdict} <evidence>` — the line
harvesting reads. `:branch` is the rule `<role>/{slug}-{node}`, lower-case `[a-z0-9-]`, unique across
the plan; two nodes deriving the same name is refused at expansion. `:green` is the tests and gates
that must pass for `green`; a card printing `green` with a named gate not run is `plan-only`.

**Budget.** `:budget (:minutes n :tokens n :model-floor <class>)` — the wall, the token ceiling the
harness enforces, the lowest model class admitted. A card below its floor is refused, never silently
downgraded; `:tokens` has no default, `0` is refused (SPEC-SWARM's rule), a budget-less plan refuses.

**Affinity.** `:affinity (:bench <kind> :route <id>)` names which bench kind may pull the card
(local, a pinned-core ssh bench, unmetered) and which registered model route serves it. The route is
a projection from facts and dated probe evidence, never a lease; the bench kind orders the ready queue.

**Lanes.** A card may carry one `LANE: <name>` line, and the lanes file named by `--lanes` (default
`queue/control/lanes.tsv`) names each area on its own line as `<name>\t<path prefixes>`; a lane is a
`:resource` of capacity 1, a serial queue over the area those path prefixes name. `nova-pulse fill`
launches at most one live card per lane at a time, holding the rest in order with `FILL HELD card=<n>
lane=<name> live=<card>`; a live card's lane is read from its own card file under the live directory; a
card without `LANE` is launched as today; a `LANE` no row of the lanes file names is refused with
`FILL REFUSED card=<n> lane=<name> remedy="add the lane to <lanes file> or drop the LANE line"`.

**The clip rule.** `:clip :per-node` is the only value that runs today: **after each node's card lands
the kernel commits and harvests** — the session clips accepted events to the branch and the pool
gathers `RESULT.md` — so a plan stopped mid-way leaves every completed node on the branch.
`:clip :at-goal` refuses until a plan-level clip exists, and a clip-less plan refuses at load.

## Part 2 — three candidate syntaxes, the same three examples

Unchanged across the three forms: **(a)** a sweep of 25 open `schema` issues with no PR, one `go-fix`
node each; **(b)** the nova-work epic table, 89 rows grouped into epics with inter-epic needs; **(c)** a fold of N green sibling branches based on `dev` into one PR.

### 1. S-expressions (the kernel's own reader)

```lisp
; (a) the sweep: one :derive, 25 nodes, no hand-written row
(:plan :version 1
 (:goal :id "schema/versioning-green"
  :acceptance ((:id "a1" :kind :test :subject "test:schema/versioning@HEAD" :predicate :passes)))
 (:derive :as "schema/issue-{n}" :kind go-fix
  :from (:issues :repo "mas-bandwidth/schema" :label "schema" :state :open :has-pr false)
  :repo "mas-bandwidth/schema" :base "dev" :inputs ((:issue "{url}"))
  :output (:branch "rowan/{n}-{slug}" :green ("test:schema/versioning"))
  :budget (:minutes 30 :tokens 120000 :model-floor :sonnet)
  :affinity (:bench :verify :route "deepseek-flash"))
 (:clip :per-node))

; (b) the epic table, head only; rows 4..89 are the same shape, e01 blocks e02
(:plan :version 1
 (:goal :id "nova-work/spec-promises-kept"
  :acceptance ((:id "a1" :kind :test :subject "test:lisp/nova-work/all@HEAD" :predicate :passes)))
 (:node :id "e01-01" :under "e01" :kind go-fix :repo "mas-bandwidth/nova-tools" :base "dev"
  :needs () :blocks ("e02-01") :inputs ((:spec "docs/nova-work-next.md:12-20") (:issue 1010))
  :output (:branch "rowan/e01-01-socket" :green ("test:lisp/nova-work/socket"))
  :budget (:minutes 30 :tokens 120000 :model-floor :sonnet) :affinity (:bench :verify :route "deepseek-flash"))
 (:node :id "e02-01" :under "e02" :kind go-fix :repo "mas-bandwidth/nova-tools" :base "dev"
  :needs ("e01-01") :blocks () :inputs ((:spec "docs/nova-work-next.md:21-30"))
  :output (:branch "rowan/e02-01-ops" :green ("test:lisp/nova-work/ops"))
  :budget (:minutes 30 :tokens 120000 :model-floor :sonnet) :affinity (:bench :verify :route "deepseek-flash"))
 (:clip :per-node))

; (c) the fold: one :fold over the sibling branch set
(:plan :version 1
 (:goal :id "nova-work/round-7-merged"
  :acceptance ((:id "a1" :kind :merged :subject "pr:round-7" :predicate :merged-at)))
 (:fold :as "round-7" :kind fold :over (:branches :prefix "rowan/" :base "dev" :green true)
  :repo "mas-bandwidth/nova-tools" :base "dev" :inputs ((:artifact "{branch}"))
  :output (:branch "rowan/round-7" :green ("gate:merge-clean"))
  :budget (:minutes 45 :tokens 180000 :model-floor :sonnet) :affinity (:bench :local :route "deepseek-flash"))
 (:clip :per-node))
```

### 2. Indented form (for humans)

```yaml
# (a)
plan: {version: 1}
goal: {id: schema/versioning-green,
       acceptance: [{id: a1, kind: test, subject: "test:schema/versioning@HEAD", predicate: passes}]}
nodes:
  - derive: {as: "schema/issue-{n}", over: {issues: true, repo: mas-bandwidth/schema, label: schema, state: open, has_pr: false}}
    kind: go-fix
    repo: mas-bandwidth/schema
    base: dev
    inputs: [{issue: "{url}"}]
    output: {branch: "rowan/{n}-{slug}", green: [test:schema/versioning]}
    budget: {minutes: 30, tokens: 120000, model_floor: sonnet}
    affinity: {bench: verify, route: deepseek-flash}
clip: per-node

# (b) head; rows 4..89 follow the same block
plan: {version: 1}
goal: {id: nova-work/spec-promises-kept,
       acceptance: [{id: a1, kind: test, subject: "test:lisp/nova-work/all@HEAD", predicate: passes}]}
nodes:
  - {id: e01-01, under: e01, kind: go-fix, repo: mas-bandwidth/nova-tools, base: dev, needs: [], blocks: [e02-01],
     inputs: [{spec: "docs/nova-work-next.md:12-20"}, {issue: 1010}],
     output: {branch: "rowan/e01-01-socket", green: [test:lisp/nova-work/socket]},
     budget: {minutes: 30, tokens: 120000, model_floor: sonnet}, affinity: {bench: verify, route: deepseek-flash}}
  - {id: e02-01, under: e02, kind: go-fix, repo: mas-bandwidth/nova-tools, base: dev, needs: [e01-01], blocks: [],
     inputs: [{spec: "docs/nova-work-next.md:21-30"}], output: {branch: "rowan/e02-01-ops", green: [test:lisp/nova-work/ops]},
     budget: {minutes: 30, tokens: 120000, model_floor: sonnet}, affinity: {bench: verify, route: deepseek-flash}}
clip: per-node

# (c)
plan: {version: 1}
goal: {id: nova-work/round-7-merged,
       acceptance: [{id: a1, kind: merged, subject: "pr:round-7", predicate: merged-at}]}
fold:
  as: round-7
  kind: fold
  over: {branches: {prefix: "rowan/", base: dev, green: true}}
  repo: mas-bandwidth/nova-tools
  base: dev
  output: {branch: rowan/round-7, green: [gate:merge-clean]}
  budget: {minutes: 45, tokens: 180000, model_floor: sonnet}
  affinity: {bench: local, route: deepseek-flash}
clip: per-node
```

### 3. Rule form (facts plus rules that derive nodes)

```lisp
; (a) every open issue with no PR is one node — the whole sweep is one rule
(issue "schema#898" :repo "mas-bandwidth/schema" :label "schema" :state :open :pr :none)
(issue "schema#900" :repo "mas-bandwidth/schema" :label "schema" :state :open :pr 1201)
(rule node(I :kind go-fix :repo R :base "dev" :inputs ((:issue U))
        :output (:branch (fmt "rowan/{n}-{slug}" I) :green ("test:schema/versioning")))
  :- (issue I :repo R :label "schema" :state :open :pr :none))

; (b) rows and inter-epic dependencies are facts; a row is a node by one rule
(epic "E01" :repo "mas-bandwidth/nova-tools")
(row "E01-01" :epic "E01" :kind go-fix :spec "docs/nova-work-next.md:12-20" :issue 1010)
(dep "E02-01" "E01-01")
(rule node(R ...) :- (row R :epic E) (epic E))
(rule needs(R D) :- (dep R D))

; (c) fold every green sibling branch under one fold node
(branch "rowan/a" :base "dev" :green true)
(branch "rowan/b" :base "dev" :green true)
(rule fold(F) :- (branch B :base "dev" :green true) (count B :n) (n > 1))
```

## Part 3 — comparison

| axis | 1. S-expressions | 2. Indented | 3. Rules |
| --- | --- | --- | --- |
| what a human writes | verbose but exact; 89 rows is typing, `:derive` is one line | least typing, easy to read and diff, no parens | smallest for sweeps ("one line"), worst to review; facts are noisy |
| what the kernel derives | `:blocks` from `:needs`, ids from `:derive`, cards from nodes, one pass | the same, plus the indent-to-list reader | node set, needs, joins and the fold are fixpoint output; needs stratification and a bound |
| what a bench consumes | the printed card alone: node plus contract | the same card; the bench never sees YAML | the same card; the bench never sees the facts |
| conflicts and joins | a cycle is a graph refusal; joins are just more `:needs` | the same, but a mis-indent is a silent depth bug | joins are the point; conflicts need negation and a termination bound |
| a Jev decision for an unresolved choice | one `:decide (:type :choice :question Q :options (...))`; the card blocks on `choice=` | the same field, indented | the decision is a fact a rule guards on — powerful, but it hides which choice gated the node |
| how the same file replays | the existing bounded no-eval reader; expansion is pure and byte-identical | a second parser where the rule is one bounded Lisp reader; YAML aliases are a second evaluation surface | a second engine with fixpoints; replay needs the fact set pinned with the rules or ids move |

**Recommendation: the S-expression form, with a bounded `:derive` selection as the rule form's one
real convenience.** The kernel already reads restricted Lisp under three bounds and refuses every
evaluation form, so a plan is a file it can open today and a bench can parse with the same tiny
reader. Expansion is a pure deterministic pass over read data, so the same plan and pinned facts
replay to byte-identical cards with no fresh ids. `:derive` and `:fold` give the sweep and the fold
their one-line expression without a second engine, and every derived node carries the same explicit
fields as a hand-written one. Negation stays out: an issue is in the sweep because the fact says
`:pr false`, not because a rule failed. The rule form remains the design target for a later
`:facts`+`:rules` mode, because its joins are better once the fact set is large, but it is not worth
a second evaluator on day one — one grammar, one error surface, one replay rule, Datalog later.

**Expansion of example (b) into the first three cards.** The kernel orders the forest by needs; it
prints one card per node, and `e01-01` has no need, so it is ready first.

```text
card e01-01  kind=go-fix  repo=mas-bandwidth/nova-tools  base=dev  needs=()  ready=yes
RULES: read only the named inputs; branch rule rowan/e01-01-socket; print the RESULT line; stop on a refused read.
IN:    docs/nova-work-next.md:12-20, issue 1010
OUT:   RESULT: e01-01 {verdict} <evidence>; branch rowan/e01-01-socket; green: test:lisp/nova-work/socket
BUDGET: 30m, 120000 tokens, model-floor sonnet   AFFINITY: bench=verify route=deepseek-flash

card e01-02  kind=go-fix  repo=mas-bandwidth/nova-tools  base=dev  needs=(e01-01)  ready=no (need e01-01 open)
RULES: same shape; branch rule rowan/e01-02-session; printed but not pullable until e01-01 is terminal accepted.
IN:    docs/nova-work-next.md:12-20   OUT: RESULT: e01-02 {verdict} <evidence>; green: test:lisp/nova-work/session

card e02-01  kind=go-fix  repo=mas-bandwidth/nova-tools  base=dev  needs=(e01-01)  ready=no
RULES: same shape; this row is where the inter-epic dependency e02/epic -> e01/epic first blocks.
IN:    docs/nova-work-next.md:21-30   OUT: RESULT: e02-01 {verdict} <evidence>; green: test:lisp/nova-work/ops
```

**Red tests for the parser and the expander.** One line each, seen red first, against the existing
restricted-Lisp reader and a fixture plan, no network and no model call.

- `worklang-reader-refuses-a-dispatch-macro` — a `#.` anywhere is refused at exit 2 naming the byte offset, the refusal the work file already owes.
- `worklang-reader-enforces-the-three-bounds` — a plan past `--max-bytes`, `--max-depth` or `--max-nodes` is refused before parsing finishes, naming the bound and the file, never truncated.
- `worklang-unknown-kind-is-a-refusal` — `:kind bogus` is refused naming the field; an unknown key beside it is preserved and ignored, unchanged.
- `worklang-needs-absent-node-is-a-refusal` — a `:needs` naming an absent id is refused naming the field and the id; neither the graph nor the journal moves.
- `worklang-needs-cycle-refuses-at-load` — a two-node cycle is refused by validator rule 3 before publication, the refusal the `:deps` field already carries.
- `worklang-blocks-and-needs-are-one-edge` — `A :needs (B)` and `B :blocks (A)` expand to the same ready order.
- `worklang-derive-expands-to-one-node-per-issue` — the (a) sweep over 25 open no-PR issues yields exactly 25 nodes, one card each; a 26th issue with a PR yields none.
- `worklang-fold-expands-to-one-pr-node-over-n-branches` — (c) over N green siblings yields one fold node whose inputs are the N branch artifacts; a non-green sibling is excluded.
- `worklang-duplicate-branch-name-refuses` — two nodes deriving the same `:branch` string is refused at expansion naming both, before any card is written.
- `worklang-expansion-is-deterministic-and-replayable` — the same plan and pinned facts expand twice to byte-identical cards; a re-expansion after one fact changes appends only the new card, minting no id.
- `worklang-ready-excludes-a-node-whose-need-is-open` — `e02-01` is printed but not pullable while `e01-01` is an open PR, and becomes pullable when `e01-01` settles after merging and going green.
- `worklang-card-carries-its-budget-and-floor` — a card carries its minutes, tokens and model floor, and a route below the floor is refused, never downgraded.

**The smallest first slice that could run tomorrow.** One subcommand, `nova-work plan expand --file
work.work --out cards/`, using the existing three-bounded reader and no new engine: it accepts
hand-written `:node`s only (no `:derive`, `:fold` or `:facts`), builds the needs/blocks graph,
refuses a cycle or an absent need, and writes one card directory per node with the RESULT line,
branch rule, green list, budget and affinity. The parser tests and `worklang-ready-excludes-a-node-whose-need-is-open`
are its red tests; benches pull the cards through the pool they already have.

## Part 4 — Amendment 1 (2026-09-18): resources, writes, identity, tools, owners, no barriers

Three sources, one amendment. **Ideas #783** is Patrick's read of the roadmap against Tandem, his
graph execution engine: admission takes a *resource vector*, not a slot; one authority per physical
capacity, with nested grants drawn from the parent's reservation; capacity is retained while an
outcome is uncertain and released only on confirmed termination; input/output *collections* whose
members are unknown before execution; a tool-provided *semantic key* for invalidation; *retained*
initialized workers accounted apart from active ones. **Glenn, 2026-09-18** fixed the lane rule:
a lane is a resource of capacity 1 over an area of the tree, `queue/control/lanes.tsv` is the map,
and unrelated lanes scatter/gather. **Stella's lease rule** fixed the uncertain one: a lease's
expiry is UNKNOWN until termination is proved, so expiry alone may never re-grant capacity.

Part 1 stands unchanged; this part adds keys and states, and removes one thing — the barrier.

### The set this amendment is written against

The real work set is `/Users/glenn/rowan-working/work/pitstop-2026-09-17.lisp`, **79 units**, and it
is not written in Part 1's `(:plan ...)` form at all. It is written as

```lisp
(work-set "pitstop-2026-09-17"
  :title "..." :under (:open-root) :inputs (...) :done-when (:all-children-closed)
  :units ((unit "verb:hygiene" :needs () :pr 1253 :replaces "bench-hygiene.sh" ...)
          (unit "verb:fill"    :needs ("verb:capacity") :pr 1248 ...)
          ...))
```

Measured over those 79 units, with the reader this amendment lands: 61 carry `:title`, 54 carry
`:needs`, 28 a `:pr`, 20 a `:lane`, 14 an `:owner`, **1** a `:budget`, **1** an `:affinity`, and
**0 — not one unit — carries `:acceptance`**. Nothing carries `:writes`, `:resources`, `:tools`,
`:attempts` or `:state`, because until now there was nothing to carry. Before this amendment
`ParsePlan` refused the file outright with *"not a plan: the top form must be (:plan ...)"*: the
coordinator's own work set was data no tool in this repo could open. That is the first thing the
amendment fixes, and every rule below is written so that file still reads after it.

### The rules

Each rule states the contract, gives the grammar for its key, and names the red test that must be
seen red first. Tests named `worklang-*` live in `internal/worklang` and are landed with this
amendment; tests named `jobs-*` are the kernel's side of the same rule — the scheduling semantics —
and are **not** implemented here: this slice is parse-only.

**A1. The work-set form is a plan.** `(work-set "<id>" ... :units (<unit> ...))` is a plan the
reader reads, and `(unit "<id>" ...)` is a node of it. `:units` is a list of unit forms; a form
inside a `:derive` template is not a unit of the set. Every key of this amendment is additive: it is
admitted on a `:node` and on a `unit` alike, so one grammar serves both forms and a file written
before the amendment reads unchanged after it.

```lisp
(work-set "<id>" :title "<text>" :under (:open-root) :units ((unit "<id>" <key> <value> ...) ...))
```

*Red tests:* `worklang-reads-the-real-work-set`; `worklang-node-accepts-the-amendment-keys`.

**A2. A unit has an identity.** The id is the second element of the form, a non-empty string,
minted once and never reused, never display text (the title is the display text). A unit with no id,
an empty id, or an id another unit already carries is refused naming the id and the byte. A renamed
unit is a **new id** carrying `:was "<old id>"`; the old id is never re-pointed, because attempts,
receipts and evidence are filed under it.

```lisp
(unit "verb:fill" :was "verb:fill-loop" ...)
```

*Red test:* `worklang-unit-id-is-stable-and-required`.

**A3. An attempt is a record, not a counter.** `:attempts` is the list of tries at a unit, in order.
Each carries `:n`, the `:rung` that ran it (the mind or model class), its `:owner`, `:started`, an
`:outcome` from the closed set `green | red | refused | abandoned | uncertain`, and — for every
outcome but `uncertain` — a `:proof` of termination: the exit, the merge, the reaped pid, the
fencing token. An outcome that claims to have ended without proving it is refused, and the refusal
names the word the author must write instead: `uncertain`. This is #783's "confirmed termination or
fencing before reuse" written into the file rather than left to a reaper's judgment.

```lisp
:attempts ((:n 1 :rung "flash" :owner "swarm:flash" :started "2026-09-18T09:10Z"
            :outcome :red :proof (:kind :exit :value "1"))
           (:n 2 :rung "child:opus" :owner "Rowan" :started "2026-09-18T10:40Z"
            :outcome :uncertain))
```

*Red test:* `worklang-attempt-without-a-termination-proof-is-uncertain`.
*Kernel:* `jobs-an-uncertain-attempt-is-never-re-granted-on-expiry`.

**A4. `uncertain` is a state.** `:state` is one of `open | ready | live | blocked | uncertain |
closed | refused | abandoned`. `uncertain` is its own state and not a spelling of open or failed: it
is what a unit is when its last attempt cannot prove termination. A unit in it **keeps its
reservation** — the lane, the vector, the warm state — until termination is proved or a fence is
written. Stella's rule in one line: an expiry is UNKNOWN until termination, so the clock alone never
frees capacity.

```lisp
(unit "verb:fill" :state :uncertain ...)
```

*Red test:* `worklang-uncertain-is-a-state`.
*Kernel:* `jobs-uncertain-keeps-its-resources`.

**A5. Admission takes a vector, not a slot.** `:resources` is what the unit actually consumes:
`:cpu`, `:memory-gb`, `:disk-gb`, `:network`, `:gpu`, any named scarce resource as an integer, and
`:class` for a named scarce class with its `:n` (the darwin runner, an uplink, a GPU queue). A
download asks for network and no cpu; a compiler asks for cpu and memory. A slot is the degenerate
one-dimensional case of this vector and stops being the unit of admission. Every value is an
integer; an entry that is not a `(:<name> <value>)` list is refused, because an admission request is
never a bare token.

```lisp
:resources ((:lane "pulse") (:cpu 2) (:memory-gb 4) (:disk-gb 10)
            (:network 1) (:gpu 0) (:class "darwin-runner" :n 1))
```

*Red test:* `worklang-resources-are-a-vector-not-a-slot`.
*Kernel:* `jobs-a-download-asks-for-network-and-no-cpu`.

**A6. A lane is a resource of capacity 1 over an area of the tree.** One entry of that vector is
`(:lane "<name>")`, and its capacity is always 1. `queue/control/lanes.tsv` is the map: one row per
lane, `<name>\t<path prefixes>`, today `merge, swarm, pulse, bus, ci, work, decide, docs, sandbox` —
adding a lane (a `schema` lane, say) is adding a row, and a `:lane` no row names is refused naming
the lanes file and the remedy. Within a lane units are serial; across lanes they scatter and gather.
The plain `:lane "<name>"` key the set already writes means exactly this resource, so the 20 units
that carry it today need no edit; naming the lane twice and differently is refused, because one unit
sits in one area of the tree and two capacity-1 reservations is double accounting.

```lisp
:resources ((:lane "docs"))        ; the same thing as :lane "docs", capacity 1, area docs/
```

*Red test:* `worklang-lane-is-a-resource-of-capacity-one`.
*Kernel:* `jobs-one-live-unit-per-lane`; `jobs-unrelated-lanes-scatter`.

**A7. `:writes` is what the unit edits, and it serializes across lanes.** `:writes` is the closed
list of repo-relative paths a unit writes to. Two units whose `:writes` intersect **serialize even
when their lanes differ**, because the lane is an area of the tree and two areas can still touch one
file. A path is relative, never absolute and never escaping its repo; either is refused naming the
path. The reader holds the set; the scheduler computes the intersection.

```lisp
:writes ("docs/SPEC-WORKLANG.md" "internal/worklang/")
```

*Red test:* `worklang-writes-are-paths-under-the-repo`.
*Kernel:* `jobs-intersecting-writes-serialize-across-lanes`.

**A8. One authority per capacity; a nested grant draws from its parent.** Every shared physical
capacity has exactly one allocator, and every other scheduler on that machine — a CI runner set, an
external engine such as Tandem — holds a **delegated sub-budget** from it, never an independent
count. A child's grant is drawn from its parent's reservation and returned to it; reserving the same
capacity twice is a refusal, not a wait. Admission of a vector is atomic: all dimensions or none,
never a partial grant a unit then waits inside.

```lisp
:resources ((:cpu 4) (:memory-gb 8))
:under-grant "bench:hulk/alloc-7"      ; this unit's vector is drawn from that reservation
```

*Kernel red tests:* `jobs-admission-is-atomic-no-partial-grant`; `jobs-a-nested-grant-draws-from-its-parent`;
`jobs-double-reservation-is-a-refusal-not-a-wait`.

**A9. No global barriers.** A unit goes when **its own** needs are closed and **its own** resources
are free. There is no phase, no round, no wave, no "everything in this set must finish first". A
work set's `:done-when` is a report of the set's finish line, never a gate on its members;
`:needs` is the only ordering, and it is per-unit. The pit-stop set's own shape is the argument:
54 of 79 units name needs and the rest are independent, so a barrier would idle most of the fleet to
wait for the slowest member of a group it has nothing to do with.

*Kernel red tests:* `jobs-a-ready-unit-goes-with-no-global-barrier`; `jobs-done-when-is-a-report-not-a-gate`.

**A10. `:tools` names the verbs a unit needs, at a version, with a key.** A unit that calls
`nova-merge batch` needs `nova-merge` installed at or above a version; a unit built against a tool
whose behaviour changed must be invalidated even when the version string did not move, so a tool may
supply a **semantic key** and `:key` pins it. A tool with no `:at` is refused: a tool that pins
nothing invalidates nothing. Bootstrapping is the same key by another name — an in-repo tool a unit
builds is a `:needs` on the unit that builds it, plus the `:tools` entry that names the built
version.

```lisp
:tools ((:nova-work :at "0.4.0" :key "worklang-reader-2026-09-18") (:go :at "1.26"))
```

*Red test:* `worklang-tools-name-a-verb-and-a-version`.
*Kernel:* `jobs-a-tool-key-move-invalidates-the-unit`; `jobs-a-unit-refuses-on-a-tool-below-its-version`.

**A11. Output collections are named before the run, their members after it.** `:collects` names an
output whose members cannot be listed in advance: the receipts a sweep writes, the shards a test
run produces, the artifacts a build emits. The collection is named, its directory is named, and its
members are `:unknown-before-run`; the harvester binds the members to the unit's revision when the
run ends. A collection with no `:name` or no `:under` is refused.

```lisp
:collects ((:name "receipts" :under "out/receipts" :members :unknown-before-run))
```

*Red test:* `worklang-a-collection-names-its-members-after-the-run`.
*Kernel:* `jobs-a-collection-binds-its-members-at-harvest`.

**A12. Warm state is accounted apart from active state.** `:warm` splits what a unit **retains**
between runs — a kept worktree, a loaded compiler, an initialized GPU worker, a warm model context —
from what it is **actively** using while it runs. They are charged separately: warmth that is not
running must never be counted as running capacity, and capacity that is running must never be freed
because something is merely warm. `:warm` with only one of the two halves is refused, because the
split is the whole point of the key.

```lisp
:warm (:retained ((:worktree "mas-bandwidth/nova-tools") (:image "toolchain@sha")) :active ((:cpu 2)))
```

*Red test:* `worklang-warm-state-is-retained-apart-from-active`.
*Kernel:* `jobs-retained-warm-state-is-not-charged-as-active`.

**A13. `:owner` is a mind.** One spelling for every kind of worker: a friend (`"Emma"`, `"Stella"`),
a child rung (`"child:opus"`, `"child:sol"`), a swarm (`"swarm:flash"`), or `"all"`. `nova-work ask`
(#1338) reads the work set and answers for the owner it finds; the pull worker reads the same form
to decide what it may take. One form, two readers, no second registry of names — a new mind is a
registry row, not a new key. An `:owner` that is not a string is refused.

```lisp
(unit "pull:worker" :owner "Emma" ...)   (unit "lanes:spec" :owner "child:opus" ...)
```

*Red test:* `worklang-owner-is-a-mind`.
*Kernel:* `work-ask-reads-the-same-set-as-the-pull-worker`.

**A14. A unit names its evidence.** `:acceptance` on a unit is the same schema the goal already
carries: `(:id "a1" :kind :test|:job|:merged|:attested :subject "<thing>" :predicate
:passes|:succeeds|:merged-at|:attested-by)`. A unit with no acceptance names no finish line, so
whether it is done is a judgement someone makes rather than evidence a tool reads — which is exactly
the state of the real set today, where **0 of 79** units carry one. The reader reports the gap per
unit (`WithoutAcceptance`) in this slice and the kernel refuses a unit without acceptance at load
once the set has been filled in; a criterion with a kind or predicate outside the two closed sets is
refused now.

```lisp
:acceptance ((:id "a1" :kind :test :subject "test:internal/pulse@HEAD" :predicate :passes))
```

*Red test:* `worklang-acceptance-is-read-and-the-real-set-has-none`.
*Kernel:* `jobs-a-unit-without-acceptance-is-refused-at-load`.

### Worked example: three real units of the pit-stop set, rewritten

Three units taken verbatim from `pitstop-2026-09-17.lisp`, then rewritten under the amendment.
Nothing in the "before" is wrong; everything the scheduler needs is simply absent from it.

```lisp
;; before — as the set stands today
(unit "verb:hygiene" :needs () :pr 1253 :replaces "bench-hygiene.sh" :findings ("F01" "F02")
      :budget (:minutes 30 :tokens 120000 :model-floor :sonnet) :affinity (:bench :linux :route "deepseek-flash"))
(unit "lanes:spec" :needs ("lanes:fill" "spec:worklang-amend") :lane "docs"
      :title "SPEC-WORKLANG: a lane is a :resource of capacity 1 over an area of the tree")
(unit "pull:worker" :owner "Emma" :lane "swarm" :needs ("promote:main") :deadline "2026-09-18T18:00Z"
      :title "benches pull work: nova-swarm pull on leases and heartbeats (SPEC-JOBS s2+s3)")
```

```lisp
;; after — the same three units, amended
(unit "verb:hygiene"
  :needs () :pr 1253 :replaces "bench-hygiene.sh" :findings ("F01" "F02")
  :owner "swarm:flash"
  :resources ((:lane "pulse") (:cpu 2) (:memory-gb 4) (:disk-gb 10))
  :writes ("cmd/nova-pulse/" "internal/pulse/hygiene.go")
  :tools ((:nova-pulse :at "0.9.2") (:go :at "1.26"))
  :collects ((:name "receipts" :under "out/receipts" :members :unknown-before-run))
  :warm (:retained ((:worktree "mas-bandwidth/nova-tools")) :active ((:cpu 2)))
  :acceptance ((:id "a1" :kind :test :subject "test:internal/pulse@HEAD" :predicate :passes))
  :attempts ((:n 1 :rung "flash" :owner "swarm:flash" :started "2026-09-18T09:10Z"
              :outcome :red :proof (:kind :exit :value "1")))
  :budget (:minutes 30 :tokens 120000 :model-floor :sonnet)
  :affinity (:bench :linux :route "deepseek-flash"))

(unit "lanes:spec"
  :needs ("lanes:fill" "spec:worklang-amend")
  :owner "child:opus"
  :resources ((:lane "docs") (:cpu 1))
  :writes ("docs/SPEC-WORKLANG.md" "docs/SPEC-JOBS.md" "internal/worklang/")
  :tools ((:nova-work :at "0.4.0" :key "worklang-reader-2026-09-18"))
  :acceptance ((:id "a1" :kind :test :subject "test:internal/docs@HEAD" :predicate :passes))
  :title "SPEC-WORKLANG: a lane is a :resource of capacity 1 over an area of the tree")

(unit "pull:worker"
  :needs ("promote:main") :deadline "2026-09-18T18:00Z"
  :owner "Emma"
  :resources ((:lane "swarm") (:cpu 4) (:memory-gb 8) (:class "darwin-runner" :n 0))
  :writes ("cmd/nova-swarm/" "internal/swarm/pull.go")
  :tools ((:nova-swarm :at "0.9.2") (:go :at "1.26"))
  :warm (:retained ((:worktree "mas-bandwidth/nova-tools") (:image "toolchain@sha")) :active ((:cpu 4)))
  :acceptance ((:id "a1" :kind :test :subject "test:internal/swarm/pull@HEAD" :predicate :passes)
               (:id "a2" :kind :merged :subject "pr:pull-worker" :predicate :merged-at))
  :state :uncertain
  :attempts ((:n 1 :rung "Emma" :owner "Emma" :started "2026-09-18T12:00Z" :outcome :uncertain))
  :title "benches pull work: nova-swarm pull on leases and heartbeats (SPEC-JOBS s2+s3)")
```

What the rewrite bought, in the three units alone: `verb:hygiene` and `lanes:spec` no longer contend
for one opaque slot, because one wants the `pulse` lane with two cores and the other the `docs` lane
with one; `lanes:spec` and this amendment would serialize on `docs/SPEC-WORKLANG.md` even though
their lanes differ, by A7; `pull:worker`'s attempt is `uncertain` with no proof, so under A3 and A4
its four cores and its `swarm` lane stay reserved instead of being re-granted on a clock; each of
the three now says what evidence closes it, which not one of the 79 did.

### What this slice implements, and what it does not

The reader in `internal/worklang` accepts and shape-checks every key above, reads the `(work-set
... :units ...)` form, and reports the units that name no acceptance. It schedules nothing: no
admission, no lane serialization, no writes intersection, no lease, no reaper, no grant. Those are
the `jobs-*` tests named above, they belong to the kernel, and they are Stella's and Emma's.

**The kernel's first slice has since landed** (`internal/jobs.Admission`, SPEC-JOBS section 9,
"What the kernel slice implements"): A5's vector, A6's lane of capacity 1, A7's writes
intersection, A8's atomic and nested grant, and A9's barrier-free pass are green, and
`nova-work set check --ready` computes the ready set THROUGH admission. A3's lease, A4's
uncertain reservation, A10's tool key, A11's harvest, A12's warm split and A14's refusal
at load are still red.

### Rule numbering is pinned

`internal/docs/worklang_amendment_test.go` pins this part: the rules are A1 to A14 in order, each
one states its rule and names at least one red test, and the grammar block of each new key is
present. A rule may be added only at the end, and a rule may not be renumbered — the numbers are
referred to from cards, from `nova-work ask`, and from SPEC-JOBS section 9.
