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
launches at most one live card per lane at a time, holding the rest in order with `FILL HELD
card=card-<n>.md lane=<name> live=card-<n>.md` — both cards named the same way, by the filename the
queue holds; a live card's lane is read from its own card file under the live directory; a card whose
launcher failed is not live, so its lane is released and the card returns to the ready directory; a
card without `LANE` is launched as today; a `LANE` no row of the lanes file names is refused with
`FILL REFUSED card=card-<n>.md lane=<name> remedy="add the lane to <lanes file> or drop the LANE line"`,
once per card per lanes-file mtime rather than once every tick.

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
