# The card manager — specification

The card layer keeps cards as plain data in a table and moves them as arrays. It is
five sub-layers, each a Go package first and a command second:

| part | sub-layer | package | what it is |
|---|---|---|---|
| 1 | definition | `internal/card/definition` | parse and pin a committed card file; the completion classification of each kind |
| 2 | request | `internal/card/request` | the typed request envelope, its canonical encoding and hash, receipts and refusals |
| 3 | **manager policy** | `internal/card/manager` | **this document**: one request plus one observed pre-state in, one decision out |
| 4 | store binding | `internal/card/binding` | one set read, one apply, operation identity, uncertain transport |
| 5 | verbs | a new binary under cmd/ | the command grammar a coordinator types |

This document specifies part 3 in full and parts 4 and 5 in outline. It is a design for
review: the packages and verbs it names are not in the tree, and nothing here is
advertised as available until each part lands with its own gate.

The rule the whole layer keeps is **batch always**. A card is data. The manager
transforms a selected array of cards in one store call; a single card is the degenerate
array of one. No function in this layer takes a single card, and no verb loops over
cards.

The second rule is **notify wherever judgment may be required**. Every transition,
forced move and selection outcome of this layer is classified as either **M**, purely
mechanical forward progress, or **J**, a point where judgment may be required. Every J
point produces a notification to the coordinator carrying what the judgment needs, and
this layer never resolves a J point by moving the card again on its own: no automatic
retry, requeue or re-dispatch. Otherwise the layer is a machine that shuffles cards
forever and makes no progress. Loops are therefore visible and bounded by design: each
card carries cycle counters maintained in the batch that causes them, and a declared,
versioned escalation policy marks a notification escalated when a counter crosses its
threshold. Marking and notifying is all this layer does with an escalation (§3.9; whether
it should also hold the card is open question Q19).

---

## 1. Scope and boundary

### 1.1 What part 3 is

The manager policy is a library of **pure functions**. Each takes one card-layer request
and one observed pre-state and returns exactly one of:

- a **write**: one nova-table batch manifest, its canonical bytes, and the receipt the
  caller should expect when the table commits it;
- a **refusal**: a typed refusal naming the operation, the card or batch, the cause, the
  expected and observed state, `changed=no`, and the next command;
- a **report**, for the read operations (inspect, check);
- a **replay**: the recorded result of this operation ID, when the pre-state carries it.

A write decision expresses every guard the decision relies on as data in the manifest:
expected table revision, expected member revisions, expected places, field guards, and
guard-only entries for every card that was read to reach the decision but does not
change. The table layer re-checks all of them against one pre-state inside one server
call. What the manager decided from its read is therefore what the store verifies at
write time; a change between read and write refuses the whole batch.

### 1.2 What is below it

The table layer (`internal/ntable`, the Lua library in `internal/nsprint/fn/lua/table.lua`)
supplies, in one application round trip each:

- `ns_table_read_set` (`ntable.ReadSet`): a read-only snapshot of an explicit member array
  or of named cells: table name, epoch, table revision, each member's revision, placement,
  score and application fields, and the missing members. At most 1,024 members.
- `ns_table_apply` (`ntable.ApplyBatch`): one conditional manifest of creates, moves,
  removals and field set/unset, every guard read from one pre-state, one commit, one
  table revision step, one receipt, and an operation record keyed by table, epoch and
  operation ID holding the exact request bytes and the result. The receipt is appended
  to the table's change stream (`table:<t>:changes`): the change event is the receipt,
  and it is the only source of notifications (§3.9).

The mechanical guards (epoch, table revision, member revision, place, field conditions,
absence, types, permissions, bounds, operation-ID conflict) are the table's. The table
knows nothing of card lifecycle, reviews, prose, jobs or leases.

### 1.3 What is above it

The store binding (part 4) turns a request into one or two reads and one apply, handles
operation identity and uncertain transport, and never decides policy. The verbs (part 5)
compile short commands into requests. The next layer (sprint and order) builds the
dependency graph and calls resolve over the scopes it chooses; dispatch later adds jobs,
slots, attempts, leases and worker launch. The console adds the view.

### 1.4 What it never does

- It never talks to a store, a forge, Git, a clock or a file. Every input is an argument.
- It never mutates one card at a time, splits a request into several writes, or chunks
  an over-limit request.
- It never opens, advances or clears an epoch. It observes the table's epoch.
- It never runs a daemon, a timer, a scan or a worker.
- It never evaluates prose (DONE-WHEN, PROBES). Readers judge prose and submit typed
  evidence; the manager validates identity, disposition and bindings.
- It never verifies an external observation. A verifier outside this package checks
  reviews, CI runs and landings against their sources; the manager checks that each
  record names a verifier the policy trusts and that its bindings match the pre-state.
- It never repairs drift. Check reports it.
- It never resolves a judgment point (§3.9) by moving a card again on its own. A forced
  move is the mechanical consequence of a negative observation (§3.4); what to do next
  is the coordinator's decision, a person's or an AI's, never this layer's.
- It never pushes a notification or wakes anyone. Notifications are derived from the
  table's receipt stream and read by the coordinator (§7.5); push delivery belongs to
  the console layer, which consumes the same stream.

### 1.5 Terms

| term | meaning |
|---|---|
| **lifecycle input** | a request entry that may move a card: `start`, `result`, `verdict-rework`, `verdict-accept`, `head`, `queue-rejected`, `landed`, `external-landing`, `complete`, `cancel`, `depfail` (the specification's "typed events") |
| **notification** | a record delivered to the coordinator that something happened to a card, derived from the receipt of the batch that did it; it moves nothing |
| **evidence** | a recorded observation about a card at a head: a read, a CI run, a sweep, a queue rejection, a landing |
| **forced move** | the one move a negative observation causes in the batch that records it (§3.4) |
| **progress** | a move forward along waiting, ready, working, review, merging, landed, or into done/completed |
| **return** | any other move of a card that stays open: review to ready, merging to review, a new head in review |
| **termination** | done/cancelled, done/depfailed, done/replaced |
| **M**, **J** | the class of a transition or outcome: mechanical, or judgment may be required (§3.9) |

The two meanings of "event" are kept apart: a lifecycle input asks for a move; a
notification reports one. Below, "event" appears only where a rule of the specification
is quoted, in the operation name "apply events", and in "the change event is the
receipt".

### 1.6 The rules this layer implements

The specification's rules, numbered so that every guard below can cite the rule it
implements. The last column names where each is kept.

**Data invariants**

| rule | the rule | kept by |
|---|---|---|
| D1 | an admitted primary is in exactly one owned cell of its stream table, with a matching reverse index in the active epoch | table one-place contract; admit creates with `expect.absent`; every move names its source place |
| D2 | location is state; there is no second writable state field | §2.3: no `state` field; the column is the state |
| D3 | prepared files and uncommitted requests are not admitted cards; admission creates records and placements together | admit is one create per card, record and placement in one commit |
| D4 | seven states; done is terminal off the ordinary line and carries outcome completed, cancelled, dependency-failed or replaced | §2.1 columns; every move to done sets `outcome` in the same entry |
| D5 | landed holds a verified landing identity | the `landed` and `external-landing` inputs set `landing` from a recorded landing record |
| D6 | landed and done are quiet with respect to this layer's operations | no transition leaves landed or done; §3 refuses `TERMINAL` |
| D7 | evidence records carry card digest, code head, issuer, disposition and source artifact identity | §2.4 record grammar |
| D8 | evidence admission, invalidation and sufficiency are manager policy | §3.4, §4 |

**Manager operations and guards**

| rule | the rule | kept by |
|---|---|---|
| G1 | every operation takes arrays or explicit complete scopes | every request shape in part 2 is an array or a scope |
| G2 | a request declares its scope, expected epoch, operation ID, expected per-card revisions and places as one array, and the observed table revision | §3.0 envelope guards |
| G3 | repeated IDs in a manifest refuse | §3.0 `TWICE`; the table refuses `TWICE` too |
| G4 | a singleton uses the same batch path; there is no single-card mutation API | §5: no function takes a card |
| G5 | admit validates every definition and identity, then creates every card in waiting | §3.1 |
| G6 | resolve selects, on a declared complete scope, the waiting cards whose dependencies are met, and moves that set to ready together; it builds no graph and schedules no scan | §3.2 |
| G7 | apply events applies typed start, result, verdict, head, CI, cancel and landing inputs as one transaction | §3.3; CI is evidence (§3.4) |
| G8 | record evidence records the array with its head and digest bindings, invalidating superseded decisions where required | §3.4; see open question Q1 |
| G9 | replace guards old records and new identities, ends old cards done/replaced with successor links and admits new cards in waiting, together | §3.5 |
| G10 | inspect and check read the selected set coherently and report placement, pinned definitions, evidence and structural drift | §3.7 |
| G11 | every guard of every operation reads the one pre-state | §3.0; the manifest's guards are the pre-state values |
| G12 | events chain across batches, never inside one | §3.3: one lifecycle input per card; guards never read this batch's effects |
| G13 | evidence recorded in a batch cannot authorize a transition in that batch | no lifecycle input reads a record written in its own batch; a negative record forces a retreat, which authorizes nothing (§3.4) |
| G14 | a dependency is met when landed, or done/completed for a kind the completion policy declares non-PR; replaced, cancelled and dependency-failed do not count | §3.2 `Met` |
| G15 | missing dependencies or a failed prerequisite give named blocked results; blocked and ineligible are declared selection outcomes | §3.2 outcomes |
| G16 | a malformed or stale request aborts the whole write set; no operation partitions failures into per-card successes | §3.0: one refusal refuses the request |
| G17 | conflicting events for one card in one batch refuse | §3.3 `CONFLICT` (lifecycle inputs); §3.4 V5 (records) |
| G18 | the transition list (eight bullets) | §3.3 table, T1–T8 |
| G19 | a card stays ready when no executor is available | no transition out of ready except start, cancel, external landing |
| G20 | replacement admits a new member and never returns a ready member to waiting | §3.5 |
| G21 | failed dependencies stay visible until a replacement or a dependency-failed termination | resolve reports them every time; only replace or depfail ends them |
| G22 | conflicting terminal identities refuse; identical already-recorded observations return their prior result | §3.3 and §3.4 `already` outcomes; `TERMINAL` refusal |
| G23 | two independent accepting reads plus required CI at the exact current code head and immutable card digest | §4 |
| G24 | each reader is a distinct recorded non-author identity; the configured required-reader list is satisfied | §4 |
| G25 | a count or silence never supplies a missing disposition | §4: only recorded records count |
| G26 | a new head invalidates head-specific acceptance and CI; a read of an old head cannot authorize merging | §3.3 T2, T3, T4 unset the old records; §4 filters by head |
| G27 | readers judge prose; the manager consumes typed results and enforces the completion policy | §1.4, §4 |
| G28 | external observations are explicit inputs with a named verifier and trust policy | §2.4 `verifier`; §4 trusted verifiers |
| G29 | the store mutation enforces every locally representable evidence binding | §2.4: evidence is member fields, guarded by revision and field guards |

**Proposed grammar and receipts**

| rule | the rule | kept by |
|---|---|---|
| P1 | vocabulary from table and member movement; arrays first; manifests for structured batches; scopes for complete selection | §7 |
| P2 | each input flag names one documented shape | §7.2 |
| P3 | each mutating input is an array with explicit IDs and expected revisions, never a list of shell commands | request shapes; §7 |
| P4 | commands carry the observed epoch and an operation ID; move carries typed events and source states | §7.3 |
| P5 | a batch is one Redis application round trip and one logical commit | §6 |
| P6 | every bound, type, permission, epoch, place, revision, identity and guard is validated before writes | §3 decisions, then the table's own validation |
| P7 | one authoritative receipt with every changed card's before and after, the request hash and the declared non-changing selection outcomes | §3.0 expected receipt, kept in the card operation record (§2.5); R2 would move it into the table's own |
| P8 | a refusal leaves the whole owned store image unchanged | §3.0; the table contract |
| P9 | one batch is one model action | §3 model mapping |
| P10 | replaying an operation ID with the same canonical request returns the previous result; different arguments refuse | §3.0 replay; §6 |
| P11 | operation identity is scoped by table and epoch | the table's operation record key |
| P12 | after a lost reply, reconcile the recorded operation; failed transport is not "no effect" | §6 |
| P13 | input and receipt bounds are explicit and checked before mutation; an over-limit request refuses with its remedy; no automatic chunking | §3 bounds |
| P14 | output names the batch, the counts, the receipt identity and per-card outcomes; refusals name the operation, card or batch, cause, change and remedy | §7.4 |
| P15 | exit 0 done or verified, 1 a negative check or lifecycle refusal, 2 unable to run; uncertain transport names the reconciliation | §7.4 |
| P16 | help touches no store; checks report drift and never repair | §7 |
| P17 | a named context file holds the selected repository, commit, table and epoch; stale context refuses with one set-refresh command | §7.1 |
| P18 | receipts carry the revisions needed to update the context, so the next command needs no per-card lookup | §7.1 |

---

## 2. The store layout the manager assumes

### 2.1 The stream table

One nova-table table holds one set of cards (the examples call it `cards`).

- **Rows are streams.** A card's row is chosen by its admission entry and never changes:
  every move the manager writes names the card's current row as both source and
  destination row. (Which input names the stream is open question Q2.)
- **One reserved row, `ops`,** hidden from the render (`nova-table row hide cards ops`),
  holds the card operation records (§2.5). No stream may be named `ops`.
- **Columns are the seven states**, declared in this order:

  | column | line | meaning |
  |---|---|---|
  | `waiting` | ordinary | admitted; dependencies not yet resolved |
  | `ready` | ordinary | dependencies met; stays here until started |
  | `working` | ordinary | a recorded start |
  | `review` | ordinary | a bound result at a head |
  | `merging` | ordinary | authorized by the review policy and the coordinator |
  | `landed` | ordinary, terminal | a verified landing identity |
  | `done` | **off the line**, terminal | outcome completed, cancelled, depfailed or replaced |

  `done` is declared last and hidden from the ordinary render (Q26)
  (`nova-table set cards --hide done`); `nova-table set cards --show done` and
  inspect show it. It remains an owned cell, read and written like any other.
- **Place is state (D2).** The card's column is its state. No member field records a
  state, a status or a phase. The manager reads a card's state only from the place the
  set read verifies against the owned cell.
- **Scores** order a cell. The manager creates a card with score equal to the table
  revision its admission batch commits (`pre.Revision + 1`), so cards of one batch tie and
  sort by ID, and earlier admissions sort first. Moves carry no score and keep it.
  (Open question Q3.)

### 2.2 The member key and prefix

The table is created with `--member-prefix card:`. The member identifier is the bare card
ID, so the member hash is `card:<id>`. Card IDs are ASCII letters, digits, underscore and
hyphen, 1 to 64 bytes (the table allows 256; the card profile bounds it at 64, open
question Q4), with no colon, comma or storage prefix. One member prefix serves one card
table: two tables sharing `card:` would share member hashes, so the manager refuses a
pre-state whose member prefix is not the policy's configured prefix (this needs the set
read to report the prefix, requirement R3).

### 2.3 Member fields the card layer owns

The table owns `epoch`, `revision` and `place:<table>`; the manager never names them. The
card layer owns exactly the application fields below. Every value is a string; every
field name is ASCII. An absent counter reads as zero, so admission writes none.

**Definition fields**, written once, never changed:

| field | type | byte bound | written by | meaning |
|---|---|---|---|---|
| `def` | `sha256:` + 64 lowercase hex | 71 | admit; replace (new card) | SHA-256 of the committed definition bytes |
| `blob` | Git object ID, 40 or 64 lowercase hex | 64 | admit; replace (new) | the committed blob the digest was taken from |
| `repo` | repository identity token | 256 | admit; replace (new) | the definition repository, as the admission manifest names it |
| `commit` | Git commit ID | 64 | admit; replace (new) | the commit the blob was read at |
| `path` | repository-relative path | 512 | admit; replace (new) | the definition file within that commit |
| `kind` | a kind name from the registry | 32 | admit; replace (new) | KIND, classified PR or non-PR by the policy |
| `deps` | sorted, comma-joined card IDs, or `-` | 520 (8 IDs, open question Q5) | admit; replace (new) | DEPENDS-ON; never follows a replacement link |
| `policy` | `sha256:` + 64 hex | 71 | admit; replace (new) | the digest of the manager policy the card is judged by (Q6) |
| `entry` | ENTRY attribute | 256 | admit; replace (new), only when present | an optional reference; never the identity |
| `pred` | card ID | 64 | replace (new) | the card this one replaces |
| `gen` | decimal | 4 | replace (new) | the replacement generation: the old card's `gen` plus one |

**Lifecycle fields**, written by lifecycle inputs and forced moves:

| field | type | byte bound | written by | meaning |
|---|---|---|---|---|
| `taker` | identity | 64 | start (T1) | who recorded the start; implies no allocation |
| `head` | Git commit ID, or `sha256:` digest of a result artifact for a non-PR kind | 71 | result (T2), head (T6), external land (T8) | the current code head |
| `artifact` | artifact identity token | 256 | result, head | the result artifact at the head (a PR reference, a report digest) |
| `result` | `done`, `failed` or `returned` | 8 | result | the worker's own disposition of the result |
| `authors` | sorted, comma-joined identities | 520 (8 IDs) | result, head | every identity that produced a head of this card, cumulative (Q7) |
| `why` | UTF-8 text, no control characters | 512 | rework, cancel, depfail; a forced move writes its cause | the reason for the last return or termination |
| `outcome` | `completed`, `cancelled`, `depfailed` or `replaced` | 16 | complete, cancel, depfail, replace (old) | set exactly when the card moves to done |
| `succ` | card ID | 64 | replace (old) | the successor that replaced this card |
| `landing` | Git commit ID | 64 | land (T5, T8) | the verified landing identity: the commit on the target that contains the head |
| `at` | decimal milliseconds, store clock | 16 | every changed entry | when the card last changed |
| `placed.at` | decimal milliseconds, store clock | 16 | every create and every move, forced or not | when the card entered its current place; age in place is read against it |

**Evidence and standing**, written by record evidence and by the inputs that change the
head:

| field | type | byte bound | written by | meaning |
|---|---|---|---|---|
| `ev.<16 hex>` | evidence record (§2.4) | 519 | record evidence; the `queue-rejected` input; unset by result, head and rework (§2.4) | one live evidence record |
| `standing` | the review standing at `head` (§4.3) | 512 | every entry that changes `head` or the `ev.*` set | the §4 evaluation, stored so that a receipt's field delta says what the batch changed about readiness |

**Cycle counters and logs**, maintained in the batch that causes them (§2.5):

| field | counts | byte bound | written by |
|---|---|---|---|
| `cy.start` | starts (ready to working) | 10 | T1 |
| `cy.result` | results (working to review) | 10 | T2 |
| `cy.fail` | results that report `failed` or `returned` | 10 | T2 |
| `cy.rework` | returns review to ready | 10 | T3 |
| `cy.mret` | returns merging to review, any cause | 10 | T6, forced moves |
| `cy.head` | new heads while in review (reads invalidated) | 10 | T6 in review |
| `cy.red` | red CI records, at any head | 10 | record evidence |
| `cy.redhead` | red CI records at the current head | 10 | record evidence; set by T2 and T6 from the records kept at the new head |
| `cy.redheads` | heads that have gone red while current | 10 | record evidence; T2, T6 |
| `cy.flaky` | a check both `ok` and `red` at one head | 10 | record evidence |
| `cy.reject` | reader rejections, at any head | 10 | record evidence |
| `log.red` | the last 8 reds: `<ms> <head12> <check> <artifact>`, `;`-separated, newest first | 2,600 | record evidence |
| `log.ret` | the last 8 returns: `<ms> <from>><to> <cause>`, newest first | 1,100 | T3, T6, forced moves |
| `last.green` | the latest green: `<ms> <head12> <check> <artifact>` | 325 | record evidence |

A card is **well formed** when: the definition fields are all present; `outcome` is
present exactly when the card is in done; `succ` is present exactly when `outcome` is
`replaced`; `landing` is present exactly when the card is in landed; every `ev.*` record
parses, carries the card's `def`, and its field name is its content hash; `standing`
equals the §4 evaluation of the live records; every counter is a decimal. Check reports
anything else as drift.

No field holds a state: place is state (D2). `standing` (Q29) and the counters are derived
facts about the card's history and evidence, kept beside it so that one set read and one
receipt carry them; check recomputes `standing` and reports a disagreement as drift.

### 2.4 Evidence as member fields

The table API guards member fields only: evidence kept anywhere else is not guarded by
the batch, and a prior unguarded read of it is no proof. So **every live evidence record
is a member field of the card it is about.** That makes every evidence guard a member
guard:

- the **member revision** pins every field of the card, so a manifest that expects
  revision *r* proves that the record set the manager evaluated, including the absence of
  any record it did not see (a later rejection, a red run), is the record set at commit;
- **field guards** (`equals`, `absent`) on `def`, `head`, `policy` and on each record the
  decision counted make the basis of the decision readable in the manifest and in the
  refusal (`FIELDGUARD` names the field).

A record is one line of space-separated tokens, none empty and none containing a space
or a control character:

```
v1 <kind> <issuer> <disposition> <head> <def> <verifier> <artifact>
```

| token | values | bound |
|---|---|---|
| `kind` | `read`, `ci`, `sweep`, `landing`; `queue`, written only by the `queue-rejected` input (§3.3) | 8 |
| `issuer` | a reader identity for `read`; `ci:<check>` for `ci`; the verifier name for `sweep`, `queue` and `landing` | 64 |
| `disposition` | `read`: `accept` or `reject`; `ci`: `ok` or `red`; `sweep`: `clean` or `negative`; `queue`: `reject`; `landing`: `landed` | 8 |
| `head` | the code head the observation was made at | 71 |
| `def` | the card's definition digest | 71 |
| `verifier` | the name of the verifier that checked the source | 32 |
| `artifact` | the source artifact identity: `review:<id>`, `run:<id>` (a CI run or job), `obs:<16 hex>` for a sweep, `land:<commit>` for a landing | 256 |

At most 519 bytes. Every token is drawn from letters, digits and `.:/@+_-`, so a record
needs no escaping inside a JSON string and its encoded size equals its length. The field name is `ev.` followed by the first 16 lowercase hex digits
of the SHA-256 of the record bytes, so an identical observation always lands on the same
field (a resubmission is an `already` outcome, never a second record) and two different
observations never share one (an `equals` guard on the full record catches a prefix
collision as a refusal). The record encoding, its parser and the evidence ID are the
request package's (assumption A-R7).

**Which records count.** Only records at the card's current `head` count toward the
review policy (§4). A record at another head is kept (it is evidence that arrived late,
or early, for a head that is not current) and counts for nothing until its head is
current. The inputs that change the head (result, head) unset every record **not at the
new head**, so a CI run that finished before its head was recorded survives the head
change; rework unsets every record. Each unset record's value survives in the receipt of
that batch, whose field delta carries the before value. At most 32 records are live per
card (`EVIDENCEFULL` names the remedy: a new head, a rework or a replacement).

**Who records evidence, and the trust policy.** This layer watches nothing. An outside
observer of the CI system, a reader, or the coordinator submits an evidence request, as a
`card evidence` command or through the library. Each record names the verifier that
checked it against its source (the forge's review, the CI system's run and check, Git's
landing). The verifier is a library outside part 3 that the evidence verb runs before
the decision; it refuses a false (no such run or review), stale (another head) or
mismatched (another repository or digest) observation before any request exists. The
manager then checks that the named verifier is one the card's policy trusts for that
kind, and that every binding matches the pre-state. A caller that spells "verified"
proves nothing: an unnamed or untrusted verifier refuses `UNTRUSTED`. The manager can
check names only; who may submit records at all is the store's access control (the seat
that may call the card layer's table functions).

**Where the records live.**

| record | where | written by |
|---|---|---|
| card record and placement | `card:<id>` and the owned cell | `ns_table_apply` |
| live evidence, standing, counters, logs | fields of `card:<id>` | `ns_table_apply` |
| superseded evidence, full red and return history | the field deltas of the receipts in `table:<t>:changes` | `ns_table_apply` |
| table operation record | the table's operation record for (table, epoch, operation ID): the exact manifest bytes and the table's result | `ns_table_apply` |
| card operation record | member `op.<operation ID>` in the reserved row `ops`: the card request hash, the committed revision and the card receipt (§2.5) | `ns_table_apply`, in the same batch |
| notifications | not stored: derived from receipts when read (§3.9) | none |
| definition bytes | the definition repository at the pinned commit | Git, outside this layer |
| review, CI and landing sources | the forge, the CI system and Git, named by `artifact` | outside this layer |

### 2.5 The card operation record

A card request can be large (128 evidence entries of 8 records encode to about 900 KB),
while one member field holds at most 64 KiB, and the table's own operation record holds
the table manifest, not the card request. A manifest is computed from a pre-state; a
retry after a lost reply reads a pre-state the first attempt already changed, computes a
different manifest, and the table would answer "operation conflict" to an identical card
request. So a replayed card request must be recognised **before** any manifest is
computed, from something the first attempt stored atomically with its effect.

That is the **card operation record**: a table member `op.<operation ID>` (a dot never
occurs in a card ID, so it cannot collide with a card), created by every mutating batch
in the reserved row `ops`, column `done`, with `expect.absent`, in the same manifest as
the batch's cards. Its fields:

| field | value | bound |
|---|---|---|
| `hash` | the card request hash, `sha256:` + 64 hex, over the canonical card request (part 2) | 71 |
| `kind` | the operation: `admit`, `resolve`, `events`, `evidence`, `replace` | 8 |
| `actor` | the request's actor | 64 |
| `rev` | the table revision the batch commits (`pre.Revision + 1`): with the epoch, the receipt's identity | 20 |
| `receipt.0` to `receipt.3` | the canonical card receipt (§3.0), in at most four chunks of at most 60 KiB | 240 KiB |

The prepare-read of every mutating operation names `op.<operation ID>` beside the cards
(no extra round trip). C1 reads it first:

- **present, same `hash`:** a replay. The manager returns the stored card receipt, with
  its identity (table, epoch, `rev`), and computes no manifest. Nothing is written.
- **present, other `hash`:** `OPCONFLICT`, naming the recorded kind, actor and revision.
- **absent:** decide; the manifest creates the record. Two concurrent attempts under one
  operation ID cannot both commit: the second's `expect.absent` on the record refuses
  (`MEMBEREXISTS`), and its retry reads the record and replays.

The record costs one changed entry per batch (so a batch carries at most 127 cards) and
one member per mutating operation in the `ops` cell of the epoch. It is expressible with
the table API as it stands. The table-layer alternative — an operation-level annotation
in the manifest, stored in the table's operation record and returned by a lookup inside
the set read (requirements R1 and R2) — would remove the member and restore the 128th
entry; the manager's C1 logic is the same either way. An operation-level field is not
expressible today: the manifest schema refuses unknown fields.

### 2.6 Counters in fields, history in the stream

Each counter is a member field because the escalation it feeds must be computed in the
batch that crosses the threshold, from the one pre-state, and must be carried by that
batch's receipt: the field delta shows `cy.redhead 1->2`, and the notification derived
from the receipt is marked escalated without reading anything else. A counter kept only
in the stream would need a scan of every receipt of the card to know its value, and a
counter kept outside the member would not be guarded by the batch.

Counters advance only in an accepted batch, exactly once per cause:

- a replayed operation ID returns the recorded result and writes nothing, so it advances
  nothing;
- an identical record resubmitted under a new operation ID is `already`: its field
  already exists, the entry is guard-only, and nothing advances;
- a refused batch writes nothing.

The logs (`log.red`, `log.ret`, `last.green`) are bounded rings of the most recent
entries, so that one set read answers "how many reds, at which heads, which run, which
check, when, and the last green" for a whole scope without reading the stream. Their
bounds (8 entries) keep a member far inside the table's 64 KiB value bound. The complete
history, every red and every return with its receipt, stays in the receipt stream, which
the notifications read walks from a cursor (§7.5). Nothing is stored twice except the
last 8 entries.

Time comes from the store's clock, read by the prepare-read (requirement R8; until then
the binding's clock, named in the receipt), passed to the manager as `pre.Now` and
written as `at` and `placed.at`. The manager itself reads no clock.

### 2.7 Size arithmetic and the card layer's bounds

The table's bounds: 128 entries with changes, 1,024 guard-only entries, 1 MiB canonical
manifest, 64 KiB per field value, 128 set fields per entry, 1,000 unset fields and 1,000
field guards per entry, 256-byte member IDs, 1,024 members per set read. Card IDs are at
most 64 bytes and operation IDs at most 128, so every member ID is within 256. The widest
card entry sets 20 fields (an evidence entry with a forced return), far inside 128.

| entry | largest encoded size | notes |
|---|---|---|
| card operation record | 241 KiB | the receipt chunks at their bound; a typical record is 2 to 40 KiB |
| admit (create) | 2.2 KiB | `deps` 520, `path` 512, `repo` 256, `entry` 256, two digests, `at`, `placed.at`, keys |
| replace pair | 2.7 KiB | old move with `outcome`, `succ` (0.4) and new create with `pred`, `gen` (2.3) |
| resolve move | 0.3 KiB | ID, revision, place, destination, `at`, `placed.at` |
| lifecycle input: start, cancel, depfail, complete, verdict-accept, landed | 0.9 KiB | ID, revision, place, guards on `def`, `head`, `policy`, `authors`; `why` 512 |
| lifecycle input: result, head, verdict-rework, queue-rejected | 4.4 KiB | `standing` 512, `log.ret` 1,100, counters, `why`, a record, 32 unsets |
| evidence entry, fixed part | 3.7 KiB | `standing`, `log.red` 2,600, `last.green`, counters, `at`, guards, a forced move |
| evidence record | 0.6 KiB | name, value and its `absent` guard |
| guard-only entry | 0.25 KiB | ID, revision, place, one field guard |

A `verdict-accept` or `complete` entry guards the counted records through the member revision, which
pins every field, not through one `equals` guard per record: that keeps the entry at
0.9 KiB whatever the policy counts, and the counted evidence IDs are named in the card
receipt instead.

**The largest request that always fits.** These are the card layer's own bounds, kept by
the request package, so that a request valid at the card layer is never refused by the
table for size. The manager's C11 check re-computes the encoded manifest and is the last
guarantee; it refuses `LIMIT` itself before the table sees anything.

| operation | card-layer bound | changed entries | worst encoded size |
|---|---|---|---|
| admit | 127 admissions | 127 + record | 127 × 2.2 + 241 = 520 KiB |
| replace | 63 pairs | 126 + record | 63 × 2.7 + 241 = 411 KiB |
| apply events (and cancel) | 127 inputs | 127 + record | 127 × 4.4 + 241 = 800 KiB |
| record evidence | 127 cards, 16 records per card, 512 records in all | 127 + record | 127 × 3.7 + 512 × 0.6 + 241 = 1,018 KiB |
| resolve | scope of 113 cards | at most 113 + record | 113 × 0.3 + 1,017 × 0.25 + 241 = 529 KiB |

Resolve's guard-only entries are the scope's cards that do not move plus their
dependencies outside the scope: with at most 8 dependencies per card, a scope of *S*
cards needs at most 9*S* entries, and 9 × 113 = 1,017 ≤ 1,024. A scope may be larger (up
to the 1,024-member read) when its cards share dependencies; the manager then counts the
actual guard-only entries after the reads and refuses `LIMIT` when they exceed 1,024,
naming the rows to split by. A scope whose **eligible** set exceeds 127 is refused
`LIMIT`, naming how many were eligible and the first 127 eligible IDs in ID order as the
explicit narrower request (P13; the alternative, a declared `deferred` outcome, is open
question Q8).

### 2.8 What the table API cannot express as it stands

These are listed in full under "Requires from the table layer" (§9.2); the design works
without each of them and states the cost.

- the card request hash and the non-changing selection outcomes in the table's own
  operation record and receipt, and a lookup of that record inside the set read (R1, R2);
  today the card operation record (§2.5) carries them, at one changed entry per batch;
- the member prefix, declared rows and columns, and complete-row selection in the set
  read (R3);
- a dependency in another epoch: today any read naming it refuses `MEMBEREPOCH` (R4);
- one-level reference following in the set read, so resolve needs one read, not two (R5);
- a field-name byte bound and a set-read response bound (R6);
- a bounded, read-only read of the receipt stream from a cursor, and a declared retention
  of that stream (R7), which the notifications read needs;
- the store's clock in the set read (R8).

---

## 3. The operations

Every operation below is one function of part 3: one request, one pre-state, one
decision. The examples share one table, `cards`, at epoch 3, with rows `build` and
`docs`, member prefix `card:`, the policy digest
`sha256:5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e`, the trusted
verifier `forge`, the required check `unit`, and the sweep required.

### 3.0 What every operation shares

**The envelope.** Every request (part 2) carries: the operation kind, the operation ID,
the table, the observed epoch, the observed table revision, the actor, and either an
array of entries with explicit card IDs and the caller's expected revision and place for
each, or a scope. The manager never fills an expectation the caller did not give.

**The prepare-read.** Part 3 derives the first read from the request (`manager.Scope`):
the named card IDs, or the named cells. It names a second read when the decision needs
cards the first read could not know (`manager.FollowUp`): the dependencies of the cards
read. The binding accepts two reads as one pre-state only when both report the same epoch
and table revision (§6); because the expected table revision fences every change to the
table, the union of the two reads is one pre-state. The first read of every mutating
operation also names the card operation record `op.<operation ID>` (§2.5). The
pre-state carries the store's clock (`pre.Now`).

**Common guards, in order.** Each operation runs these first; the first guard that fails
refuses the whole request (G16), naming every card that fails that guard.

| # | guard | refusal | rule |
|---|---|---|---|
| C1 | the card operation record `op.<operation ID>` is in the pre-state: same card request hash, return its stored receipt without computing a manifest; other hash, refuse | replay, or `OPCONFLICT` | P10, P11 |
| C2 | the request's epoch is the pre-state's epoch | `STALE` | G2 |
| C3 | the request's observed table revision is the pre-state's revision | `STALE` | G2 |
| C4 | the table is a card table: member prefix and the seven columns (needs R3; until then the table refuses unknown columns itself) | `LAYOUT` | D1, D4 |
| C5 | the request is within the card layer's bounds (§2.7) | `LIMIT` | P13 |
| C6 | no card ID appears twice in the request, counting both sides of a replacement pair | `TWICE` | G3 |
| C7 | every existing card the request names is in the pre-state, in this epoch | `NOTCARD`, `FOREIGN` | D1 |
| C8 | every expected revision and place equals the pre-state's | `REVISION`, `PLACE` | G2, G11 |
| C9 | every existing card named is pinned to this policy digest | `POLICY` | G27, Q6 |
| C10 | the operation's own guards (§3.1 to §3.6) | per operation | per operation |
| C11 | the encoded manifest is within the table's bounds: 128 changed, 1,024 guard-only, 1 MiB | `LIMIT` | P13 |

**The manifest.** `table`, `epoch` and `expected_table_revision` are the pre-state's
(equal to the request's by C2 and C3); `operation_id` and `actor` are the request's.
Every entry for an existing card carries `expect.revision` and `expect.place` from the
pre-state. Every move keeps the card's row. Every changed entry sets `at` to `pre.Now`;
every create and move also sets `placed.at`. Every mutating manifest also creates the
card operation record (§2.5). Array order carries no meaning (every guard reads one
pre-state and each card appears once): the request's canonical form sorts entries by card
ID and records by evidence ID, so a replay with reordered entries is the same request,
and the manager emits manifest entries sorted by member ID, fields sorted by name, in one
canonical encoding. The same request and pre-state give the same bytes.

**The decision's shape.** Every card the request names, and every card read to reach the
decision, appears exactly once in the manifest: as a changed entry, or as a guard-only
entry that pins what the decision read. Every entry of the request has exactly one
outcome:

| outcome | changes the card | in the manifest as | meaning |
|---|---|---|---|
| `changed` | yes | changed entry | the operation's effect |
| `blocked` | no | guard-only entry, with the blocking dependency pinned too | a waiting card with an unmet, missing or failed dependency (resolve) |
| `ineligible` | no | guard-only entry | a card in the scope whose state is not the operation's source |
| `missing` | no | guard-only `expect.absent` entry | a named dependency that does not exist |
| `already` | no | guard-only entry with the recorded identity guarded | an identical observation is already recorded (G22) |
| `inapplicable` | no | guard-only entry | evidence for a card whose state takes no such record (§3.4) |

None of these is a refusal. A refusal is a request the manager cannot apply as a whole.

**The expected receipt** (`request.Receipt`) names the operation, the card request hash,
the table, the epoch, the table revision before and after (`after = before + 1`; the
receipt's identity is table, epoch and `after`), the outcome (`changed` or `noop`), the
counts (selected, eligible, changed, blocked, ineligible, missing, already,
inapplicable, guarded), and per card its outcome, its place and revision before and
after, the reason and the cards it is blocked on, the successor link on a replacement,
the landing identity on a landing, the counted evidence IDs on a verdict, what the batch
changed about its readiness (`standing` before and after), the notification kinds the
receipt will yield (§3.9), and the observed values the context keeps (revision, place,
head). Its one-line form carries the request hash and the receipt identity:
`ADD OK op=op-add-1 hash=sha256:9c41… receipt=cards/3/42 …`. After the apply, the binding compares the table's receipt with it (§6); a
disagreement is reported, never ignored.

**A decision that changes nothing.** When no card changes, the manager writes the batch
— its guard-only entries and its card operation record — only when some outcome is a
judgment point (§3.9: a card blocked on a failed, missing or foreign dependency;
inapplicable negative evidence), so that a receipt exists and the notification is derived
from it; otherwise it returns a report with `written=no`, and no table revision is spent
(open question Q9).

**Refusals are judgment points for the caller.** A refusal writes nothing (P8), so it
yields no receipt and no notification in the stream. It is returned to the caller with
what judgment needs. A `STALE` refusal names who and what changed the cards since the
caller's observation: the binding reads the receipts after the caller's cursor (§6) and
lists each receipt's actor, operation and changed cards. An `OPCONFLICT` refusal names
the recorded request's operation kind, actor and cards. (Whether refusals should also be
kept somewhere the coordinator reads is open question Q10.)

**Replay and lost reply, for every mutating operation.** The operation ID identifies
the request within (table, epoch). C1 recognises a replay from the card operation record
before any manifest is computed, and returns the stored card receipt. A lost reply is
`changed=unknown` until it is reconciled by running the same request under the same
operation ID: the record is there (replay) or it is not (the batch never committed, and
is decided afresh). At most one effect exists, because the record is created atomically
with the effect and refuses a second creation (§2.5, §6). A replay advances no counter
and yields no second notification: its receipt is the original one.

### 3.1 Admit

**Inputs.** `request.Admit`: the envelope and an array of admission entries, each a
pinned `definition.Admission` (ID, repository, commit, path, blob, digest, kind,
dependencies, entry) plus the stream row it is admitted into.

**Prepare-read.** `ReadSet(members = the admitted IDs and op.<operation ID>)`, at most
128 members. One read.

**Decision** (after C1 to C9):

| # | guard | outcome or refusal | rule |
|---|---|---|---|
| A1 | at most 127 entries | `LIMIT` | P13 |
| A2 | every ID is a valid card ID, and its file's contract-line ID equals it | `DEFINITION` | D3 |
| A3 | every admission is pinned: digest, blob, commit, repository and path present and well formed | `DEFINITION` | D3, G5 |
| A4 | every kind is classified PR or non-PR by the policy's completion classification | `KIND` | G27 |
| A5 | every dependency is a valid card ID, not the card itself; at most 8 (Q5) | `DEFINITION` | G14 |
| A6 | every stream is a declared row (needs R3; until then the table refuses `NOROW`) | `STREAM` | D1 |
| A7 | every ID is absent from the pre-state; present with the same `def` and row is `already` (open question Q11) | `EXISTS`, `FOREIGN`, or `already` | D1, G22 |

Dependencies are not checked for existence at admission: a missing dependency is a
blocked outcome of resolve (G15), and cards of one admission may depend on each other.

**Manifest**, for an admission of `c-lex` and `c-parse` into `build`, where `c-spec` was
already admitted with the same definition (a guard-only `already` entry). The pre-state
table revision is 41 and `pre.Now` is 1700000000000.

```json
{
  "schema": 1,
  "table": "cards",
  "epoch": "3",
  "expected_table_revision": "41",
  "operation_id": "op-add-1",
  "actor": "coordinator",
  "members": [
    {
      "id": "c-lex",
      "expect": {"absent": true},
      "create": {"row": "build", "col": "waiting", "score": 42},
      "set": {
        "at": "1700000000000",
        "blob": "ac4ae97285c19b13201deb9b192d921316db3447",
        "commit": "bf1c365741a4bfb5fee5c3150335ab4f867a4d9a",
        "def": "sha256:11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa",
        "deps": "-",
        "kind": "fix-red",
        "path": "cards/c-lex.md",
        "placed.at": "1700000000000",
        "policy": "sha256:5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e",
        "repo": "example/briefs"
      }
    },
    {
      "id": "c-parse",
      "expect": {"absent": true},
      "create": {"row": "build", "col": "waiting", "score": 42},
      "set": {
        "at": "1700000000000",
        "blob": "8dbc6058e03353809813416c6830708abaf9d223",
        "commit": "bf1c365741a4bfb5fee5c3150335ab4f867a4d9a",
        "def": "sha256:22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb",
        "deps": "c-lex",
        "kind": "fix-red",
        "path": "cards/c-parse.md",
        "placed.at": "1700000000000",
        "policy": "sha256:5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e",
        "repo": "example/briefs"
      }
    },
    {
      "id": "c-spec",
      "expect": {
        "revision": "4",
        "place": {"row": "docs", "col": "done"},
        "fields": {"def": {"equals": "sha256:44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd"}}
      }
    },
    {
      "id": "op.op-add-1",
      "expect": {"absent": true},
      "create": {"row": "ops", "col": "done", "score": 42},
      "set": {
        "actor": "coordinator",
        "hash": "sha256:9c41d0e2a7b35f6e8c1d2b3a4f5e6d7c8b9a0f1e2d3c4b5a69788796a5b4c3d2",
        "kind": "admit",
        "receipt.0": "v1 admit op-add-1 cards 3 41 42 changed;c-lex changed - build:waiting 0 1;c-parse changed - build:waiting 0 1;c-spec already docs:done 4 4",
        "rev": "42"
      }
    }
  ]
}
```

**Selection outcomes.** `already` for an ID admitted with the same digest and row.

**Refusals.** C1 to C9, A1 to A7, C11. `EXISTS` names the card, its digest in the store
and the digest requested, and the remedy (a replacement under a new ID, or drop the
entry).

**Expected receipt.** `ADD OK op=op-add-1 hash=sha256:9c41d0e2a7b3 receipt=cards/3/42
rev=41->42 selected=3 changed=2 already=1`;
`c-lex` and `c-parse` `-` to `build:waiting`, revision 0 to 1; `c-spec` already at
revision 4.

**Replay.** §3.0. A lost reply whose operation record is found returns the stored
receipt; one not found is decided afresh under the same operation ID.

**Model.** `AdmitBatch(A)` with `A` the changed IDs.

### 3.2 Resolve

**Inputs.** `request.Resolve`: the envelope and a scope: explicit card IDs, or rows
(meaning the `waiting` cell of each named row), or the whole table (every row's
`waiting` cell). The scope is complete: every waiting card in it is selected.

**Prepare-read.** First `ReadSet(selection = the waiting cell of each scope row)` or
`ReadSet(members = the IDs)`, at most 1,024 members, with `op.<operation ID>` (for a row
scope the record is read by ID in the second read); then `ReadSet(members = the
dependencies named in the scope cards' deps, less those already read)`, at most 1,024.
Two reads, one pre-state (§3.0); one read with R5.

**Decision.** A dependency `d` of card `c` is:

| class | condition in the pre-state | counts as met |
|---|---|---|
| met | `d` in landed; or `d` in done with `outcome=completed` and `kind` classified non-PR | yes (G14) |
| open | `d` in waiting, ready, working, review or merging | no |
| failed | `d` in done with `outcome` cancelled, depfailed or replaced | no (G14, G21) |
| missing | `d` in the pre-state's missing list | no (G15) |
| foreign | `d` exists in another epoch (needs R4; today the read refuses) | no (Q12) |

| # | guard | outcome or refusal | rule |
|---|---|---|---|
| S1 | the scope is complete and within one read (1,024 members) | `SCOPE`, `LIMIT` | G1, G6 |
| S2 | a card in the scope that is not in waiting (an ID scope) | `ineligible` | G15 |
| S3 | a waiting card whose every dependency is met | `changed`: to ready | G6, G14 |
| S4 | a waiting card with a dependency not met | `blocked`, naming each unmet dependency and its class | G15, G21 |
| S5 | at most 127 cards are `changed`, and at most 1,024 guard-only entries | `LIMIT`, naming how many were eligible and the first 127 eligible IDs as the explicit narrower request (Q8) | P13 |

A card moved to ready in this batch is not met for another card in this batch: every
class is read from the pre-state (G11, G12). Resolve never returns a card to waiting
(G20) and never moves a card out of ready (G19).

**Manifest**, for a scope of row `build` whose waiting cell holds `c-lex` (no
dependencies), `c-emit` (depends on `c-spec`, which is done/completed and of the non-PR
kind `text`) and `c-parse` (depends on `c-lex`, which is waiting in the pre-state). Two
guard-only entries: the blocked card and the met dependency.

```json
{
  "schema": 1,
  "table": "cards",
  "epoch": "3",
  "expected_table_revision": "50",
  "operation_id": "op-resolve-1",
  "actor": "coordinator",
  "members": [
    {
      "id": "c-emit",
      "expect": {"revision": "1", "place": {"row": "build", "col": "waiting"}},
      "move": {"row": "build", "col": "ready"},
      "set": {"at": "1700000600000", "placed.at": "1700000600000"}
    },
    {
      "id": "c-lex",
      "expect": {"revision": "1", "place": {"row": "build", "col": "waiting"}},
      "move": {"row": "build", "col": "ready"},
      "set": {"at": "1700000600000", "placed.at": "1700000600000"}
    },
    {
      "id": "c-parse",
      "expect": {"revision": "1", "place": {"row": "build", "col": "waiting"}}
    },
    {
      "id": "c-spec",
      "expect": {
        "revision": "4",
        "place": {"row": "docs", "col": "done"},
        "fields": {"kind": {"equals": "text"}, "outcome": {"equals": "completed"}}
      }
    },
    {
      "id": "op.op-resolve-1",
      "expect": {"absent": true},
      "create": {"row": "ops", "col": "done", "score": 51},
      "set": {
        "actor": "coordinator",
        "hash": "sha256:4be07a9d1c2e3f405162738495a6b7c8d9eaf0b1c2d3e4f5061728394a5b6c7d",
        "kind": "resolve",
        "receipt.0": "v1 resolve op-resolve-1 cards 3 50 51 changed;c-emit changed build:waiting build:ready 1 2;c-lex changed build:waiting build:ready 1 2;c-parse blocked build:waiting 1 1 on=c-lex:open",
        "rev": "51"
      }
    }
  ]
}
```

`c-parse`'s blocking dependency `c-lex` is pinned by `c-lex`'s own entry. A missing
dependency is pinned by a guard-only `{"id": "c-gone", "expect": {"absent": true}}`
entry; a failed one by its place in done and `outcome` `one_of` the failed outcomes.

**How "dependencies met" is atomic.** Each met dependency has a guard-only entry on its
place (landed, or done), its revision, and for done its `outcome` and `kind`. Each
blocking dependency is pinned the same way. Terminal cards never change (D6), so these
guards fail only on drift or a concurrent write, and then the whole batch refuses; the
expected table revision would refuse it as well.

**Bounds.** Changed entries are the eligible cards, at most 127, plus the operation
record. Guard-only entries are the blocked and ineligible cards plus every dependency not
itself changed, at most 1,024. Each read is at most 1,024 members. A scope of 113 cards
always fits (§2.7); a larger scope fits when its cards share dependencies, and the
manager counts before it writes. A scope with more than 127 eligible cards refuses
`LIMIT`, naming how many were eligible and the first 127 eligible IDs as the narrower
request; the coordinator issues that request as its own operation.

**Selection outcomes.** `blocked` (with `on=<id>:<class>` per dependency), `ineligible`,
`missing` for each missing dependency.

**Refusals.** C1 to C9, S1, S5, C11.

**Expected receipt.** `RESOLVE OK op=op-resolve-1 receipt=cards/3/51 rev=50->51
selected=3 eligible=2 changed=2 blocked=1`; `c-emit` and `c-lex` `build:waiting` to `build:ready`, revision 1
to 2; `c-parse` blocked on `c-lex:open`.

**Replay.** §3.0. A resolve replayed after a lost reply returns the recorded receipt,
even when later batches have changed eligibility.

**Model.** `ResolveSet(E)` with `E` the changed set. The blocked outcomes are the
model's pre-state classification, recorded in the receipt.

### 3.3 Apply events (lifecycle inputs)

**Inputs.** `request.Events`: the envelope and an array of lifecycle inputs, one per
card, each with the card ID, the input type, the declared source state, the expected
revision, and the input's payload. The types are the request package's (assumption
A-R4); the transitions they name through `request.Destination(type, source)`, and their
class (§3.9):

| # | input | from | to | class | own guards (pre-state) | fields written besides `at`, `placed.at` |
|---|---|---|---|---|---|---|
| T1 | `start` | ready | working | M | none | `taker`; `cy.start` +1 |
| T2 | `result` | working | review | M; J when `result` is `failed` or `returned` | payload head differs from `head`; `result`, `artifact`, `authors` present | `head`, `artifact`, `result`, `authors` (union); `cy.result` +1; `cy.fail` +1 when failed or returned; unset every `ev.*` not at the new head; `cy.redhead`, `cy.redheads` from the reds kept; `standing` |
| T3 | `verdict-rework` | review | ready | J (a return) | `why` present | `why`; unset every `ev.*`; `cy.rework` +1; `cy.redhead` 0; `log.ret`; `standing` |
| T4 | `verdict-accept` | review | merging | M: the input is the coordinator's judgment | kind classified PR; the review policy authorizes the card at `head` (§4) | none |
| T5 | `landed` | merging | landed | M | a recorded `landing` record at `head`; no negative record at `head` | `landing` |
| T6a | `queue-rejected` | merging | review | J (a return) | payload head equals `head`; `why`, verifier and artifact present | `why`; a `queue reject` record at `head` (it blocks); `cy.mret` +1; `log.ret`; `standing` |
| T6b | `head` | merging | review | J (a return) | payload head differs from `head` | `head`, `artifact`, `authors` (union); `why`; unset every `ev.*` not at the new head; `cy.mret` +1; red counters as T2; `log.ret`; `standing` |
| T6c | `head` | review | review | J (acceptance and CI at the old head no longer count) | payload head differs from `head` | as T6b, with `cy.head` +1 in place of `cy.mret` |
| T7a | `cancel` | waiting, ready, working, review, merging | done | J (a termination) | `why` present | `outcome=cancelled`, `why` |
| T7b | `depfail` | waiting | done | J (a termination) | the named dependency `because` is in `deps` and failed in the pre-state | `outcome=depfailed`, `why` |
| T7c | `complete` | review | done | M | kind classified non-PR; the review policy authorizes the card at `head` (§4) | `outcome=completed` |
| T8 | `external-landing` | waiting, ready, working | landed | J (landed without passing review here) | a recorded `landing` record for this card's `def`; dependencies are not checked (Q24) | `landing`, `head` (the landed head) |

A non-code card completed outside the system has no input of its own: it passes
`result`, the reads and `complete` like any other (open question Q23).
`external-landing` on a card in review refuses: review must pass `verdict-accept` first
(the specification's last transition clause; open question Q13 asks what records code
that landed while its card was in review). Cancel from merging is as the specification
lists it; open question Q14 asks whether it should require a dequeue first.

A CI run is never a lifecycle input: it is an observation at a head, recorded as
evidence (§3.4), and a red one for a card in merging forces the return in the evidence
batch. The queue's rejection is the lifecycle input `queue-rejected`: it writes the
rejection as a `queue reject` record at the head, so the head stays blocked, which is the
input's own consequence and authorizes nothing.

No input retries, requeues or re-dispatches a card by itself. Every J input and every J
outcome yields a notification; nothing in this layer answers it by moving the card again.

**Prepare-read.** `ReadSet(members = the input cards, every depfail's named dependency
and op.<operation ID>)`, at most 1,024 members. One read.

**Decision** (after C1 to C9):

| # | guard | outcome or refusal | rule |
|---|---|---|---|
| E1 | at most one input per card | `CONFLICT` | G17 |
| E2 | the input type is known and `Destination(type, place)` defines a transition from the card's pre-state column | `TRANSITION` | G18 |
| E3 | a card in landed or done: an input whose recorded identity is already there (`landed` or `external-landing` with the same `landing`, `cancel` on cancelled, `complete` on completed, `depfail` on depfailed) | `already` | G22 |
| E4 | any other input on a card in landed or done | `TERMINAL` | D6, G22 |
| E5 | the payload is well formed: head and identity grammar, `why` present and bounded where required | `INPUT` | P3 |
| E6 | the input's own guard in the table above, read from the pre-state fields and records | `HEAD`, `KIND`, `UNAUTHORIZED`, `NOLANDING`, `NEGATIVE`, `DEPFAIL`, `UNTRUSTED` | G18, G23 to G26, D5 |
| E7 | at most 127 inputs | `LIMIT` | P13 |

No input reads a record written in the same batch (G13), and no guard reads another
input's effect (G12): every E6 guard is a function of the pre-state and the input.
`UNAUTHORIZED` lists what is missing at the head, in the `standing` vocabulary (§4.3).

**Manifest**, for three inputs: `c-lex` starts; `c-emit` is accepted into merging on two
accepting reads, the required `unit` check and a clean sweep at its head; `c-late` ends
dependency-failed because its dependency `c-drop` was cancelled (a guard-only entry).
`pre.Now` is 1700003600000.

```json
{
  "schema": 1,
  "table": "cards",
  "epoch": "3",
  "expected_table_revision": "63",
  "operation_id": "op-move-1",
  "actor": "coordinator",
  "members": [
    {
      "id": "c-drop",
      "expect": {
        "revision": "3",
        "place": {"row": "docs", "col": "done"},
        "fields": {"outcome": {"one_of": ["cancelled", "depfailed", "replaced"]}}
      }
    },
    {
      "id": "c-emit",
      "expect": {
        "revision": "7",
        "place": {"row": "build", "col": "review"},
        "fields": {
          "authors": {"equals": "worker-2"},
          "def": {"equals": "sha256:33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc"},
          "head": {"equals": "4f92c044d819a16c426f859755d06090e1903b42"},
          "policy": {"equals": "sha256:5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e"}
        }
      },
      "move": {"row": "build", "col": "merging"},
      "set": {"at": "1700003600000", "placed.at": "1700003600000"}
    },
    {
      "id": "c-late",
      "expect": {
        "revision": "1",
        "place": {"row": "docs", "col": "waiting"},
        "fields": {"deps": {"equals": "c-drop"}}
      },
      "move": {"row": "docs", "col": "done"},
      "set": {
        "at": "1700003600000",
        "outcome": "depfailed",
        "placed.at": "1700003600000",
        "why": "dependency c-drop cancelled"
      }
    },
    {
      "id": "c-lex",
      "expect": {"revision": "2", "place": {"row": "build", "col": "ready"}},
      "move": {"row": "build", "col": "working"},
      "set": {"at": "1700003600000", "cy.start": "1", "placed.at": "1700003600000", "taker": "worker-1"}
    },
    {
      "id": "op.op-move-1",
      "expect": {"absent": true},
      "create": {"row": "ops", "col": "done", "score": 64},
      "set": {
        "actor": "coordinator",
        "hash": "sha256:d17f2c8e9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f708192a3b4c5d6e7f809",
        "kind": "events",
        "receipt.0": "v1 events op-move-1 cards 3 63 64 changed;c-drop guard docs:done 3 3;c-emit changed build:review build:merging 7 8 counted=1c4adacfc0528ee9,565ee23ca9d78018,a7ac7378f5e03930,ffb5a58fe8f24f33;c-late changed docs:waiting docs:done 1 2 outcome=depfailed;c-lex changed build:ready build:working 2 3",
        "rev": "64"
      }
    }
  ]
}
```

The `verdict-accept` entry's revision guard pins every field of `c-emit`, including its
four counted records (`ev.1c4adacfc0528ee9`, `ev.565ee23ca9d78018`,
`ev.a7ac7378f5e03930`, `ev.ffb5a58fe8f24f33`) and the absence of any rejection or red run
the read did not see; the receipt names the counted records. The field guards on `def`,
`head`, `policy` and `authors` name the rest of the decision's basis so that a refusal
says which part changed.

**Selection outcomes.** `already` for an identical terminal observation (E3).

**Refusals.** C1 to C9, E1, E2, E4 to E7, C11.

**Expected receipt.** `MOVE OK op=op-move-1 receipt=cards/3/64 rev=63->64 selected=3
changed=3 guarded=1`; `c-lex` `build:ready` to `build:working` revision 2 to 3; `c-emit`
`build:review` to `build:merging` 7 to 8, counted records named; `c-late` `docs:waiting`
to `docs:done` 1 to 2 with `outcome=depfailed`. Notifications: `started` (M) for `c-lex`,
`merging` (M) for `c-emit`, `depfailed` (J) for `c-late`.

**Replay.** §3.0. A lost reply after a verdict is reconciled under the same operation ID;
a second `verdict-accept` under a new ID refuses `PLACE` (the card is in merging).

**Model.** `ApplyEvents(B)` with `B` the input array.

### 3.4 Record evidence

**Inputs.** `request.Evidence`: the envelope and an array of entries, one per card: the
card ID, its expected place and revision (the revision may be omitted by an outside
observer that has not read the card, open question Q15; the manifest always carries the
pre-state's), and the records for it, at most 16, each typed (kind, issuer, disposition,
head, digest, verifier, artifact). Several records for one card go in one entry.

**Prepare-read.** `ReadSet(members = the cards and op.<operation ID>)`. One read.

**What a record does.** Every record is checked against the card's pre-state, and has
exactly one of four effects:

| effect | when | written | class |
|---|---|---|---|
| **counts** | the kind may be recorded in the card's state, at the card's `head` | the record, counters, `standing` | M for a positive record; J for a negative one |
| **other head** | the kind may be recorded in the card's state, at another head | the record (it counts for nothing until its head is current), counters | M; the notification says "not applicable to the current head" |
| **forces a return** | a negative record that counts, for a card in merging | the record, counters, `standing`, and the move merging to review | J |
| **inapplicable** | the card's state takes no record of this kind (waiting or ready for CI, any terminal card) | nothing: a guard-only entry pins the state | M; J when the record is negative |

| kind | states that take it | a negative at the head does |
|---|---|---|
| `read` | review, merging | in review: blocks; in merging: forces the return to review |
| `ci` | working, review, merging | in working: nothing yet (the result input will carry it into review); in review: blocks; in merging: forces the return to review |
| `sweep` | review | in review: blocks |
| `landing` | merging; waiting, ready, working | (not negative) in merging it counts at `head`; in the earlier states any head is the landed head |

A `queue` record is never submitted as evidence: the `queue-rejected` lifecycle input
writes it (§3.3).

**The forced move.** A negative observation at the current head for a card in merging —
a red CI run, or a reader's rejection — is recorded **and** moves the card from merging
to review in the same entry of the same batch, every guard
read from the one pre-state, one receipt. This keeps the model's invariant that a card in
merging is authorized: a card in merging with a negative at its head never exists, not
even between two batches.

This does not break G13, "evidence recorded in a batch cannot authorize a transition in
that batch". That rule forbids a record from *authorizing forward progress* it was
recorded alongside: an acceptance, a green run or a clean sweep can never enable a
`verdict-accept` or `complete` in its own batch, because those inputs are a different operation
and read only records already in their pre-state. A negative observation authorizes
nothing: it *forces a retreat*, which is a consequence of the record itself, exactly as
the record makes the card blocked in review. The forced move is always backward (merging
to review), never forward, and never to any other place.

The forced move does not unset the records at the head. The negative record must stay:
it is the blocker, and unsetting the head's records would let the card be merged again at
the same head. The acceptances beside it stay recorded and cannot authorize while a
negative stands at that head. This differs on purpose from rework (T3), which unsets
every record: rework is a verdict that the work must be redone, while a forced return is
an observation that the head failed. What clears a negative is a new head (T2 or T6,
which unsets every record not at the new head) or a rework; nothing else in this layer
clears one (open question Q16 for a waiver).

**What each record does to the counters**, in the entry that records it:

| record | counters |
|---|---|
| `ci red` at any head | `cy.red` +1; newest-first into `log.red` |
| `ci red` at the current head | also `cy.redhead` +1; `cy.redheads` +1 when `cy.redhead` was 0 |
| `ci red` and `ci ok` for one check at one head (in the pre-state or in this entry) | `cy.flaky` +1, once per check and entry |
| `ci ok` | `last.green` when it is the newest green |
| `read reject` at any head | `cy.reject` +1 |
| a forced return | `cy.mret` +1; newest-first into `log.ret` with the cause; `why` names the cause and its source; `placed.at` |

**Decision** (after C1 to C9):

| # | guard | outcome or refusal | rule |
|---|---|---|---|
| V1 | at most 127 cards, 16 records per card and 512 records in the request (§2.7); at most 32 live records per card after it | `LIMIT`, `EVIDENCEFULL` | P13 |
| V2 | each record is well formed; kind and disposition agree; the issuer is of the kind's issuer class, and no reader identity uses a reserved issuer (`ci:*` or a verifier name) | `EVIDENCE` | D7 |
| V3 | the record's verifier is one the card's policy trusts for that kind | `UNTRUSTED` | G28 |
| V4 | the record's digest is the card's `def` | `DIGEST` | G23, D7 |
| V5 | within one entry, at most one record per (kind, issuer, head): two observations of one check, or of one reader, at one head cannot be ordered inside one batch | `CONFLICT` | G12, G17 |
| V6 | a `sweep` names the digest of the card's live non-sweep records at the head, those in the pre-state together with this entry's own (§4.2) | `SWEEP` | G28 |
| V7 | the card's state and the record's head give its effect (tables above) | `changed` (counts, other head, forced return) or `inapplicable` | D8, G26 |
| V8 | a record already present with identical bytes | `already` | G22 |

A rejection after an acceptance from one reader at one head is recorded beside it and
blocks; an acceptance after a rejection, or a green after a red for one check, is
recorded and blocks nothing, but it clears nothing either: the negative stands (G25).

**The readiness delta.** For each card the receipt says what was recorded and what it
changed about the card's readiness, computed from the pre-state and this entry's records
(this computes a report, not a guard, and authorizes nothing): `standing` before and
after (§4.3), and one phrase:

```
c-parse: read accept by reader-a at bf1c36574; review now has 2 of 2 reads and ci:unit ok; needs a sweep before a verdict to merging
c-emit: ci:unit red at 4f92c044d819 (run:9003); returned merging -> review; blocked by ci:unit red run:9003; flaky: ci:unit ok and red at one head
c-fmt: ci:unit ok at 36e20656918e, an old head; recorded, not applicable to the current head
```

**Manifest**, for a red `unit` run on `c-emit` in merging at its head (a forced return;
`unit` was green at the same head, so the check is flaky), `reader-a`'s acceptance of
`c-parse` in review, and `reader-b`'s read on `c-lex`, already recorded (a guard-only
`already` entry). `pre.Now` is 1700007200000.

```json
{
  "schema": 1,
  "table": "cards",
  "epoch": "3",
  "expected_table_revision": "70",
  "operation_id": "op-ev-1",
  "actor": "ci-observer",
  "members": [
    {
      "id": "c-emit",
      "expect": {
        "revision": "8",
        "place": {"row": "build", "col": "merging"},
        "fields": {
          "def": {"equals": "sha256:33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc"},
          "ev.2d0843b2c42d7689": {"absent": true},
          "head": {"equals": "4f92c044d819a16c426f859755d06090e1903b42"}
        }
      },
      "move": {"row": "build", "col": "review"},
      "set": {
        "at": "1700007200000",
        "cy.flaky": "1",
        "cy.mret": "1",
        "cy.red": "1",
        "cy.redhead": "1",
        "cy.redheads": "1",
        "ev.2d0843b2c42d7689": "v1 ci ci:unit red 4f92c044d819a16c426f859755d06090e1903b42 sha256:33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc forge run:9003",
        "log.red": "1700007200000 4f92c044d819 unit run:9003",
        "log.ret": "1700007200000 merging>review ci:unit-red",
        "placed.at": "1700007200000",
        "standing": "reads=2/2 rejects=0 ci=unit:red sweep=stale need=- block=ci:unit@run:9003",
        "why": "ci:unit red run:9003 at 4f92c044d819"
      }
    },
    {
      "id": "c-lex",
      "expect": {
        "revision": "8",
        "place": {"row": "build", "col": "review"},
        "fields": {"ev.7d9d9ecfefcc487b": {"equals": "v1 read reader-b accept 3d3b60b2c381f8873f294894ef8428319a3b8546 sha256:11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa forge review:302"}}
      }
    },
    {
      "id": "c-parse",
      "expect": {
        "revision": "5",
        "place": {"row": "build", "col": "review"},
        "fields": {
          "def": {"equals": "sha256:22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb"},
          "ev.710150fcbb47f418": {"absent": true},
          "head": {"equals": "bf1c365741a4bfb5fee5c3150335ab4f867a4d9a"}
        }
      },
      "set": {
        "at": "1700007200000",
        "ev.710150fcbb47f418": "v1 read reader-a accept bf1c365741a4bfb5fee5c3150335ab4f867a4d9a sha256:22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb forge review:301",
        "standing": "reads=2/2 rejects=0 ci=unit:ok sweep=none need=sweep block=-"
      }
    },
    {
      "id": "op.op-ev-1",
      "expect": {"absent": true},
      "create": {"row": "ops", "col": "done", "score": 71},
      "set": {
        "actor": "ci-observer",
        "hash": "sha256:63a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5",
        "kind": "evidence",
        "receipt.0": "v1 evidence op-ev-1 cards 3 70 71 changed;c-emit changed build:merging build:review 8 9 recorded=2d0843b2c42d7689 forced=ci:unit-red;c-lex already build:review 8 8;c-parse changed build:review build:review 5 6 recorded=710150fcbb47f418",
        "rev": "71"
      }
    }
  ]
}
```

**Selection outcomes.** `already` (V8); `inapplicable` (V7), as a guard-only entry
pinning the card's place and revision.

**Refusals.** C1 to C9, V1 to V6, C11. Recording evidence never refuses because a card
moved on: a record for a card that has left the states that take it is `inapplicable`,
and one at another head is kept for that head. So an outside observer's batch of CI
results is never aborted by one card that was cancelled meanwhile (G16 still holds: a
malformed or stale request refuses whole).

**Expected receipt.**

```
EVIDENCE OK op=op-ev-1 receipt=cards/3/71 rev=70->71 selected=3 changed=2 already=1 records=2 returned=1 trips=2
CARD c-emit build:merging->build:review rev=8->9 recorded=ci:unit:red:run:9003 standing=reads=2/2,ci=unit:red,block=ci:unit@run:9003 esc=flaky
CARD c-lex already rev=8 record=read:reader-b:accept
CARD c-parse build:review rev=5->6 recorded=read:reader-a:accept standing=reads=2/2,ci=unit:ok,need=sweep
```

Notifications: `ci-red` and `returned` (J, escalated `flaky`) for `c-emit`;
`read-accept` (M) for `c-parse`.

**Two reds for one card in one batch.** Two red runs of one check at one head in one
entry refuse `CONFLICT` (V5): their order decides the counters and cannot be known inside
one batch; the observer sends the second in a later request. Reds of two different checks
at one head in one entry are both recorded, the counters advance by two, and the card
moves at most once. Two entries for one card refuse `TWICE` (C6).

**Replay.** §3.0. A record's field name is its content hash, so even a new operation ID
carrying the same records is an `already` outcome, never a second record, and it
advances no counter.

**Model.** `RecordEvidence(B)`, which gains the forced return as part of the same action
(§8.2). The model admits evidence only in review today; this design also admits records
in working and merging, and landing records in waiting, ready and working, which the
model follows.

### 3.5 Replace definitions

**Inputs.** `request.Replace`: the envelope and an array of pairs: the old card's ID,
expected revision and place, and a new admission entry (§3.1) under a new ID.

**Prepare-read.** `ReadSet(members = every old and new ID and op.<operation ID>)`. One
read.

**Decision** (after C1 to C9; C6 counts both sides of every pair):

| # | guard | outcome or refusal | rule |
|---|---|---|---|
| X1 | at most 63 pairs | `LIMIT` | P13 |
| X2 | the old card is done/replaced with `succ` equal to this pair's new ID, and the new card exists with the requested `def` and `pred` | `already` (both entries guard-only) | G22 |
| X3 | the old card is in waiting, ready, review or merging: not working, not terminal (open questions Q14 and Q17 on merging and review) | `REPLACE`, `TERMINAL` | G9, G20, D6 |
| X4 | the new admission passes A2 to A6, and the new ID is absent | as §3.1, `EXISTS` | G5, G9 |

In one batch every old card moves to done in its own row with `outcome=replaced` and
`succ`, and every new card is created in waiting with `pred` and `gen` (the old card's
`gen` plus one). The old members and their definition digests stay. No dependant's
`deps` changes: a card that depended on the old ID is blocked on a failed dependency (a J
notification at its next resolve) until it is itself replaced or ends dependency-failed
(G21). A replacement is a J point: its notification carries both IDs and the lineage
generation, and `gen` of 2 or more is escalated (§3.9).

**Manifest**, for replacing `c-old` with `c-old2`, where the pair `c-doc` to `c-doc2` is
already recorded (two guard-only entries). A pair is two cards, so four card entries and
the operation record.

```json
{
  "schema": 1,
  "table": "cards",
  "epoch": "3",
  "expected_table_revision": "80",
  "operation_id": "op-replace-1",
  "actor": "coordinator",
  "members": [
    {
      "id": "c-doc",
      "expect": {
        "revision": "3",
        "place": {"row": "docs", "col": "done"},
        "fields": {"outcome": {"equals": "replaced"}, "succ": {"equals": "c-doc2"}}
      }
    },
    {
      "id": "c-doc2",
      "expect": {
        "revision": "1",
        "place": {"row": "docs", "col": "waiting"},
        "fields": {
          "def": {"equals": "sha256:8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b"},
          "pred": {"equals": "c-doc"}
        }
      }
    },
    {
      "id": "c-old",
      "expect": {
        "revision": "2",
        "place": {"row": "docs", "col": "waiting"},
        "fields": {"def": {"equals": "sha256:55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee55ee"}}
      },
      "move": {"row": "docs", "col": "done"},
      "set": {"at": "1700010800000", "outcome": "replaced", "placed.at": "1700010800000", "succ": "c-old2"}
    },
    {
      "id": "c-old2",
      "expect": {"absent": true},
      "create": {"row": "docs", "col": "waiting", "score": 81},
      "set": {
        "at": "1700010800000",
        "blob": "36e20656918e0a9ee13c113115a777c6c365d358",
        "commit": "3d3b60b2c381f8873f294894ef8428319a3b8546",
        "def": "sha256:66ff66ff66ff66ff66ff66ff66ff66ff66ff66ff66ff66ff66ff66ff66ff66ff",
        "deps": "-",
        "gen": "1",
        "kind": "text",
        "path": "cards/c-old2.md",
        "placed.at": "1700010800000",
        "policy": "sha256:5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e",
        "pred": "c-old",
        "repo": "example/briefs"
      }
    },
    {
      "id": "op.op-replace-1",
      "expect": {"absent": true},
      "create": {"row": "ops", "col": "done", "score": 81},
      "set": {
        "actor": "coordinator",
        "hash": "sha256:0e9d8c7b6a5f4e3d2c1b0a99887766554433221100ffeeddccbbaa9988776655",
        "kind": "replace",
        "receipt.0": "v1 replace op-replace-1 cards 3 80 81 changed;c-doc already docs:done 3 3 succ=c-doc2;c-doc2 already docs:waiting 1 1;c-old changed docs:waiting docs:done 2 3 succ=c-old2;c-old2 changed - docs:waiting 0 1 pred=c-old gen=1",
        "rev": "81"
      }
    }
  ]
}
```

**Selection outcomes.** `already` (X2).

**Refusals.** C1 to C9, X1, X3, X4, C11. Replacing a working card refuses `REPLACE`
with the remedy to cancel it, or to wait for its result.

**Expected receipt.** `REPLACE OK op=op-replace-1 receipt=cards/3/81 rev=80->81
selected=2 changed=2 already=1`; `c-old` `docs:waiting` to `docs:done` 2 to 3
`outcome=replaced succ=c-old2`; `c-old2` `-` to `docs:waiting` 0 to 1 `pred=c-old gen=1`.
Notifications: `replaced` (J) for `c-old`, `admitted` (M) for `c-old2`.

**Replay.** §3.0.

**Model.** `ReplaceDefinitions(P)`.

### 3.6 Cancel, as a lifecycle input

Cancel is not an operation of its own: it is the `cancel` input (T7a) in an apply-events
request, decided by `DecideEvents`. The verb `card cancel` compiles its IDs, or its
scope, into one events request with one `cancel` input per card, the reason, and the
context's observed revisions and places.

- A scope cancel selects every card of the scope rows in waiting, ready, working, review
  or merging; cards in landed or done are `ineligible` (guard-only), never refused.
- A card already done/cancelled is `already` (E3); a card landed, or done with another
  outcome, refuses `TERMINAL` (E4), because its terminal identity conflicts.
- `why` is required, 1 to 512 bytes.
- Every cancellation is a J point: its notification carries the reason and the actor,
  and each dependant of the cancelled card learns of it at its next resolve (blocked on a
  failed dependency, J).

**Manifest**, for cancelling `c-fmt` (waiting) and `c-lint` (working), with `c-tidy`
already cancelled (guard-only `already`):

```json
{
  "schema": 1,
  "table": "cards",
  "epoch": "3",
  "expected_table_revision": "90",
  "operation_id": "op-cancel-1",
  "actor": "coordinator",
  "members": [
    {
      "id": "c-fmt",
      "expect": {"revision": "1", "place": {"row": "build", "col": "waiting"}},
      "move": {"row": "build", "col": "done"},
      "set": {"at": "1700014400000", "outcome": "cancelled", "placed.at": "1700014400000", "why": "superseded by the plan"}
    },
    {
      "id": "c-lint",
      "expect": {"revision": "3", "place": {"row": "build", "col": "working"}},
      "move": {"row": "build", "col": "done"},
      "set": {"at": "1700014400000", "outcome": "cancelled", "placed.at": "1700014400000", "why": "superseded by the plan"}
    },
    {
      "id": "c-tidy",
      "expect": {
        "revision": "2",
        "place": {"row": "build", "col": "done"},
        "fields": {"outcome": {"equals": "cancelled"}}
      }
    },
    {
      "id": "op.op-cancel-1",
      "expect": {"absent": true},
      "create": {"row": "ops", "col": "done", "score": 91},
      "set": {
        "actor": "coordinator",
        "hash": "sha256:7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a291807f6e5d4c3b2a1908f7e6d5c4b3a29",
        "kind": "events",
        "receipt.0": "v1 events op-cancel-1 cards 3 90 91 changed;c-fmt changed build:waiting build:done 1 2 outcome=cancelled;c-lint changed build:working build:done 3 4 outcome=cancelled;c-tidy already build:done 2 2",
        "rev": "91"
      }
    }
  ]
}
```

**Expected receipt.** `CANCEL OK op=op-cancel-1 receipt=cards/3/91 rev=90->91 selected=3
changed=2 already=1`. Notifications: `cancelled` (J) for `c-fmt` and `c-lint`.

**Model.** `ApplyEvents(B)` with every input `cancel`.

### 3.7 Inspect and check

These are the read operations. They produce a report, never a manifest, and they have no
operation ID.

**Inspect.** Inputs: `request.Inspect`, a scope (IDs, rows, or the whole table).
Prepare-read: the scope's cards (every column of the named rows), at most 1,024, then
their dependencies, at most 1,024; one read with R5. The report (`manager.Report`, §5)
gives, per card:

- place, revision, age in place (`pre.Now − placed.at`) and idle time (`pre.Now − at`);
- the pinned definition identities (`def`, `blob`, `commit`, `path`, `kind`, `deps`,
  `policy`, `pred`, `succ`, `gen`);
- for a card in review or merging, the evidence standing at the current head: reads
  accepted and rejected (with the readers), the state of each required check, the sweep,
  what is still missing, and each blocker with its source (the check and run, or the
  reader and review);
- records at other heads, marked "not applicable to the current head";
- the red history summary (`cy.red`, `cy.redheads`, `cy.redhead`, `cy.flaky`, the last
  reds from `log.red`, `last.green`) and the returns (`cy.rework`, `cy.mret`, `cy.head`,
  `log.ret`), starts and results;
- the escalation marks the card's policy gives its counters and age (§3.9.5);
- each dependency's class; the lifecycle inputs permitted next; structural drift.

Inspect is the set-refresh command: its report carries the observations the context
stores (P17, P18), so it applies no C2, C3 or C8 guard. It refuses only `LAYOUT` and
`LIMIT` (a scope over 1,024 cards, with the remedy to name rows). Its output lines are in
§7.3.

**Check.** Inputs: `request.Check`, the table. The binding runs the table's own check
(`ns_table_check`, read-only) and a set read of every card of the table. The manager
reports drift in the card structure, and never repairs it:

| drift | meaning |
|---|---|
| `fields` | a definition field missing or malformed |
| `outcome` | done without `outcome`, or `outcome` outside done |
| `succ` | replaced without `succ`, or `succ` naming a card that does not exist or whose `pred` disagrees |
| `landing` | landed without `landing` |
| `deps` | a card in ready, working, review or merging with a dependency not met (the model's `ReadyImpliesDepsMet`) |
| `evidence` | an `ev.*` record that does not parse, carries another digest, or whose field name is not its content hash |
| `standing` | `standing` disagrees with the evaluation of the live records |
| `counters` | a counter that is not a decimal, or `cy.redhead` greater than `cy.red` |
| `kind` | a kind the card's policy does not classify |
| `unplaced` | a `card:` record with card fields and no placement |
| `operation` | an `op.*` member outside the `ops` row, or a record without `hash` and `rev` |

A table over 1,024 cards refuses `LIMIT` with the remedy to check by rows
(`check --context ctx.json --rows build`), because a set read never returns a prefix
marked complete (open question Q18). Exit 0 is clean, exit 1 is drift found.

### 3.8 The model's properties, as this design implements them

| model property | how the manager and table make it hold |
|---|---|
| the request and the receipt are part of the state | the card operation record holds the card request hash, the committed revision and the card receipt, created in the batch it describes; the table's own operation record holds the manifest bytes |
| an accepted step changes exactly the request's cards | changed card entries are exactly the request's `changed` outcomes; every other card read is a guard-only entry; the binding checks the receipt's changed set against the expectation |
| a refusal changes nothing | manager refusals precede any write; table refusals leave the store image unchanged by contract |
| a landing needs recorded evidence at that head | T5 and T8 require a `landing` record already in the pre-state, pinned by the member revision |
| a negative observation at the current head blocks completion and merging | any recorded `reject`, `red`, `negative` or queue `reject` at the head blocks T4 and T7c; a negative recorded for a card in merging forces the return in the same batch, so no card is in merging with a negative at its head; the sweep binds the recorded set to the verifier's observed set |
| replay returns the prior result | C1 reads the card operation record before any manifest is computed |
| reader eligibility | §4: distinct non-author identities from `authors` and the records, reserved issuers excluded, the required-reader list |
| one batch is one action | one manifest, one apply, one receipt per request |
| every return is notified | every return writes `log.ret` and a counter in its own entry, so its receipt carries it and the derived notification (§3.9) |

| operation | model action |
|---|---|
| admit | `AdmitBatch` |
| resolve | `ResolveSet` |
| apply events, cancel | `ApplyEvents` (with `queue-rejected` as the model's requeue, which records the rejection in the same step) |
| record evidence | `RecordEvidence`, including the forced return |
| replace | `ReplaceDefinitions` |
| a decision that changes nothing and has no J outcome | no action (the report writes nothing; Q9) |
| a decision that changes nothing but has a J outcome | `NoOpBatch` with its receipt |
| any refusal | the `Refuse` stutter |

### 3.9 Judgment points, notifications, cycle counters and escalation

#### 3.9.1 The rule and its test

Every transition, forced move, selection outcome, refusal and read-time condition of this
layer is exactly one of:

- **M**, purely mechanical forward progress, or a fact that needs no decision;
- **J**, a point where judgment may be required.

The rule: every J point produces a notification to the coordinator carrying what the
judgment needs, and this layer never resolves a J point by moving the card again on its
own. A forced return (§3.4) is the mechanical half of a J point — record, move back,
notify — and it is the last move this layer makes for that point; why the card went red
and what to do next is the coordinator's decision.

The test: a unit table enumerates every pair `(input type, source state)` that
`request.Destination` accepts, every forced move, every selection outcome, every refusal
code and every read-time condition, and holds each against the classification below; a
row missing from either side fails. For every J row, the notification derivation
(§3.9.3) applied to a receipt containing that transition yields a notification of the
named kind with every field the row names.

#### 3.9.2 The classification

| # | transition or outcome | class | notification | it carries | escalation (§3.9.5) |
|---|---|---|---|---|---|
| 1 | admit to waiting | M | `admitted` | card, row, `def`, kind, deps | `lineage` for a replacement's new card |
| 2 | resolve to ready | M | `ready` | card, the dependencies met | — |
| 3 | blocked on an open dependency | M | none in the stream; the resolve report lists it | dependency and its place | `stale` on the waiting card |
| 4 | blocked on a failed, missing or foreign dependency | J | `blocked` | dependency, its class, its outcome and `why` | — |
| 5 | ineligible, already, a replay | M | none; the receipt lists them | — | — |
| 6 | evidence inapplicable (a positive record) | M | `inapplicable` | the record, the card's state | — |
| 7 | evidence inapplicable (a negative record) | J | `inapplicable` | the record (issuer, check, run), the card's state | — |
| 8 | T1 start | M | `started` | taker | `no-result` |
| 9 | T2 result, `done` | M | `result` | head, artifact, authors, `standing` | — |
| 10 | T2 result, `failed` or `returned` | J | `result` | result, artifact, head | `failed-result` |
| 11 | T3 verdict-rework | J | `returned` (cause `rework`) | `why`, the records invalidated | `rework`, `returns` |
| 12 | readiness reached: `standing` has nothing missing and no blocker | J | `authorized` | `standing`, the records that count | — |
| 13 | T4 verdict-accept | M | `merging` | the counted records | — |
| 14 | T5 landed | M | `landed` | landing identity | — |
| 15 | T6a queue-rejected | J | `returned` (cause `queue`) | `why`, artifact, head | `merge-return`, `returns` |
| 16 | T6b head, from merging | J | `returned` (cause `head`) | old and new head | `merge-return`, `returns` |
| 17 | T6c head, in review | J | `head` | old and new head, the records invalidated | `returns` |
| 18 | T7a cancel | J | `cancelled` | `why`, actor | — |
| 19 | T7b depfail | J | `depfailed` | the failed dependency, `why` | — |
| 20 | T7c complete | M | `completed` | the counted records | — |
| 21 | T8 external-landing | J | `landed-external` | landing identity, head | always escalated |
| 22 | replace | J | `replaced` (old), `admitted` (new) | both IDs, `gen` | `lineage` |
| 23 | read accept at the head | M | `read-accept` | reader, review artifact, `standing` | — |
| 24 | read reject at the head, in review | J | `read-reject` | reader, review artifact, the blocker | `reject` |
| 25 | read reject at the head, in merging (forced return) | J | `read-reject` and `returned` (cause `reject`) | as 24, and the move | `reject`, `merge-return`, `returns` |
| 26 | ci ok at the head | M; J when the check is also red at that head | `ci-green` | check, run, `standing` | `flaky` |
| 27 | ci red at the head, in working or review | J | `ci-red` | check, run, head, the blocker | `red-same-head`, `red-total`, `flaky` |
| 28 | ci red at the head, in merging (forced return) | J | `ci-red` and `returned` (cause `ci`) | as 27, and the move | as 27, and `merge-return`, `returns` |
| 29 | a record at another head | M; J when negative | `other-head` | the record; "not applicable to the current head" | a red still counts in `red-total` |
| 30 | sweep clean / negative | M / J | `sweep` | the observed set digest; the blocker | — |
| 31 | landing record | M | `landing-recorded` | landing, head | — |
| 32 | a card past its age threshold in one place, with no receipt since (read time) | J | `stale` | place, age, threshold | `stale` |
| 33 | refusal `STALE` (epoch or revision) | J | the refusal, to the caller | who and what changed since the caller's observation | — |
| 34 | refusal `OPCONFLICT` | J | the refusal, to the caller | the recorded request's kind, actor and revision | — |
| 35 | every other refusal | J | the refusal, to the caller | cause, expected, observed, remedy | — |
| 36 | a write to a card by anything but the card layer (an ordinary table verb, or a batch without a card operation record) | J | `foreign-write` | verb, actor, receipt | always escalated |

Every return (11, 15, 16, 17, 25, 28) is J and is notified from the receipt that made it.
Every termination (18, 19, 22) is J. Forward progress (2, 8, 9, 13, 14, 20) is M.

#### 3.9.3 Notifications are derived from receipts

Every accepted batch yields exactly one receipt, and the table appends it to its change
stream: the change event is the receipt. A notification is **derived** from a receipt by
a pure function (`manager.Notifications`, §5), never from scans or polling of cards, and
is never stored. The derivation reads, per receipt:

- the card operation record's fields in the delta (`kind`, `actor`, the card receipt with
  its selection outcomes, including every blocked card and its reason);
- each card's place before and after (a move, a return, a termination);
- each `ev.*` field set (the record: kind, issuer, disposition, head, artifact);
- `standing` before and after (what the batch changed about readiness; `authorized`
  when it reaches nothing missing and no blocker);
- the counters after, from which the card's pinned policy gives the escalation marks.

Every notification carries: the receipt identity (table, epoch, revision after) and its
stream ID, the operation ID and actor, the card and its row, the kind, the class (M or
J), the severity (`info`, `judgment`, `escalated`) and marks, the place before and after,
the head, the evidence identity and issuer when a record is involved, `standing` before
and after, `why`, the counters the marks were computed from, and the receipt's time.

A notification's identity is (receipt stream ID, card, kind). The stream is append-only
and a derivation is deterministic, so reading a range twice yields the same
notifications with the same identities: a duplicated delivery is recognised by identity,
and a missed one is recovered by reading again from an earlier cursor. The cursor is the
coordinator's and lives in its context file; nothing in the store tracks who has read
what. A stream trimmed past the cursor refuses `CURSORGONE`, with the remedy to take the
current state from `card inspect` and continue from the stream's first entry (the stream's
retention is requirement R7).

This layer has no daemon. The coordinator pulls notifications with one command
(`card notifications`, §7.5); push delivery and waking the coordinator belong to the
console layer, which consumes the same stream the same way.

#### 3.9.4 Progress, returns and cycle counters

A card makes **progress** when it moves forward along waiting, ready, working, review,
merging, landed, or reaches done/completed. Every other move of an open card is a
**return**; done/cancelled, done/depfailed and done/replaced are **terminations**. Any
move not in the classification is refused, so these three kinds cover every move.

Shuffling without progress is visible per card in the counters of §2.3, each maintained
in the batch that causes it:

| a loop | the counters that show it |
|---|---|
| returns to ready from review | `cy.rework` |
| returns to review from merging | `cy.mret` |
| new heads in review (reads invalidated) | `cy.head` |
| reds, at the same head or at new heads | `cy.red`, `cy.redhead`, `cy.redheads`, `cy.flaky`, `log.red`, `last.green` |
| reader rejections | `cy.reject` |
| starts without a result | `cy.start − cy.result` |
| failed or returned results | `cy.fail` |
| replacements in a lineage | `gen` |

#### 3.9.5 Escalation is data

A notification's severity is `info` for M, `judgment` for J, and `escalated` for J when
any mark below holds for the card after the batch. The marks and their thresholds are
part of the manager policy (§4.1): declared, versioned, pinned per card by the `policy`
digest, so the same receipt always derives the same marks. The defaults below are
proposals for the owner to confirm (Q30).

| mark | holds when | default threshold |
|---|---|---|
| `red-same-head` | `cy.redhead` ≥ *n*: the same failure again, or a flaky check | 2 |
| `red-total` | `cy.red` ≥ *n*: each fix attempt failing | 3 |
| `flaky` | `cy.flaky` ≥ *n*: one check both green and red at one head | 1 |
| `merge-return` | `cy.mret` ≥ *n*: the second return from merging | 2 |
| `rework` | `cy.rework` ≥ *n* | 2 |
| `reject` | `cy.reject` ≥ *n* | 2 |
| `no-result` | `cy.start − cy.result` ≥ *n* | 2 |
| `failed-result` | `cy.fail` ≥ *n* | 2 |
| `returns` | `cy.rework + cy.mret + cy.head` ≥ *n*: the third return of any kind | 3 |
| `lineage` | `gen` ≥ *n* | 2 |
| `stale` | age in place ≥ the column's threshold (escalated at twice it) | waiting 7 d, ready 1 d, working 4 h, review 1 d, merging 2 h |

Reaching a threshold does not stop or move the card. This layer marks and notifies, and
nothing else acts on an escalation here. Whether a card past a threshold should also be
**held** — refusing further lifecycle inputs from anyone but a coordinator until a
coordinator records a decision — is open question Q19.

#### 3.9.6 The health of a scope

Over the receipts read, the notifications read counts, for the scope: forward moves,
returns, terminations, cards stale now, and cards with an escalated mark now:

```
HEALTH since=1700000000000-0 receipts=14 forward=12 returns=4 terminations=1 stale=2 escalated=1
```

A scope whose returns keep pace with its forward moves is shuffling; the line makes that
visible without reading the cards one by one.

---

## 4. The review policy as data

### 4.1 The policy document

The manager policy is one versioned document, encoded canonically; its SHA-256 is the
policy digest every card pins at admission (`policy`). A decision is always made with the
policy the card pins (C9), so a card is judged by the rules it was admitted under.

| field | meaning | default |
|---|---|---|
| `version` | the policy's version name | — |
| `member_prefix` | the card table's member prefix | `card:` |
| `completion` | the versioned completion classification from the definition package: each kind PR or non-PR | the registry's kinds; `read`, `probe`, `text`, `tone`, `report` non-PR |
| `quorum` | distinct accepting non-author reads required at the head; at least 2 | 2 |
| `required_readers` | reader identities whose acceptance is required, at most 8 | none |
| `required_checks` | CI check names that must be `ok` at the head for a PR kind, at most 8 | none |
| `verifiers` | per record kind, the verifier names trusted | none: every record refuses `UNTRUSTED` until one is named |
| `sweep` | whether a clean sweep of the head's records is required before a verdict (Q22) | required |
| `escalation` | the thresholds of §3.9.5 | as §3.9.5 |
| `stale` | the age thresholds per column | as §3.9.5 |
| `coordinators` | the identities that act as coordinator (used only if Q19 decides to hold) | none |

### 4.2 Evaluating a card from its fields

For a card with head *H*, digest *D* and authors *A*, let *R* be its live records with
head *H* and digest *D* (records at other heads count for nothing). Then:

| term | computed as |
|---|---|
| accepted readers | the distinct issuers of `read accept` records in *R*, less *A*, less the reserved issuers (`ci:*`, verifier names) |
| rejections | the `read reject` records in *R* |
| a check's state | `red` if *R* has a `ci red` for it; else `ok` if *R* has a `ci ok` for it; else `none` |
| negatives | every `read reject`, `ci red`, `sweep negative` and `queue reject` in *R* |
| the observed set digest | the first 16 hex digits of the SHA-256 of the comma-joined, sorted evidence IDs of the non-sweep records in *R* |
| the sweep | `clean` when *R* has a `sweep clean` naming the current observed set digest; `stale` when its sweeps name an older set; `negative` when a `sweep negative` is present; else `none` |
| authorized | accepted readers ≥ `quorum`, and `required_readers` ⊆ accepted readers, and no negatives, and every required check `ok` when the kind is PR (Q25), and the sweep `clean` when the policy requires it |

The rules this implements:

- **two independent accepting reads plus required CI at the exact current head and
  immutable digest** (G23): only records carrying *H* and *D* are in *R*;
- **distinct recorded non-author identities** (G24): readers are a set of issuer
  identities, the authors of every head of the card are excluded, and no reserved issuer
  is a reader;
- **the required-reader list** (G24): each named reader's acceptance must be in *R*,
  besides the quorum;
- **a count or silence never supplies a missing disposition** (G25): only a recorded
  record with an explicit disposition counts;
- **a new head invalidates** (G26): `result` and `head` unset every record not at the new
  head, and nothing at another head is in *R*; a read of an old head cannot authorize;
- **a negative blocks** (G23, the model's repaired property): any negative in *R* blocks
  `verdict-accept` and `complete`, whatever acceptances sit beside it; a negative
  recorded for a card in merging forces its return (§3.4);
- **completeness of the observations**: the sweep binds the recorded set to the set the
  verifier observed at the head, so leaving a negative out of the record needs the
  verifier to leave it out too.

### 4.3 The standing, as stored and printed

`standing` is the evaluation above, written by every entry that changes the head or the
record set, so that a receipt's field delta says what the batch changed about readiness.
Its grammar, at most 512 bytes:

```
reads=<accepted>/<quorum> rejects=<n> ci=<check>:<ok|red|none>[,...] sweep=<clean|stale|negative|none> need=<item>[,...]|- block=<source>[,...]|-
```

`need` lists what is missing: `reads`, `reader:<id>`, `ci:<check>`, `sweep`. `block`
lists each negative with its source: `ci:<check>@<artifact>`, `read:<reader>@<artifact>`,
`queue@<artifact>`, `sweep@<artifact>`. A card is authorized exactly when `need=-` and
`block=-`. Check recomputes `standing` from the records and reports a disagreement as
drift.

| card | records at the head | standing |
|---|---|---|
| `c-parse` before §3.4 | `reader-b` accept, `unit` ok | `reads=1/2 rejects=0 ci=unit:ok sweep=none need=reads,sweep block=-` |
| `c-parse` after | and `reader-a` accept | `reads=2/2 rejects=0 ci=unit:ok sweep=none need=sweep block=-` |
| `c-emit` before §3.4 | two accepts, `unit` ok, a clean sweep | `reads=2/2 rejects=0 ci=unit:ok sweep=clean need=- block=-` |
| `c-emit` after | and `unit` red run 9003 | `reads=2/2 rejects=0 ci=unit:red sweep=stale need=- block=ci:unit@run:9003` |

### 4.4 Why the evaluation is atomic

The evaluation reads only member fields of the card and the policy the card pins. The
`verdict-accept` and `complete` entries carry:

- `expect.revision`: the member revision pins every field, so the record set, `authors`,
  `head` and `standing` at commit are the ones evaluated, and a record that arrived after
  the read (a rejection, a red run) makes the batch refuse;
- `expect.place`: the card is still in review;
- field guards on `head`, `def`, `policy` and `authors`, naming the decision's basis in a
  refusal;
- and C9 has already refused a policy other than the card's pinned one.

The counted evidence IDs are named in the card receipt. Nothing the decision relied on is
outside the manifest's guards.

---

## 5. The Go API of `internal/card/manager`

Signatures only. No function takes a single card; every mutating decision is over the
request's array or scope. The request shapes are constructible only through the request
package's validating constructors, so a `request.Admit` value always carries its
canonical bytes and hash (assumption A-R1): the policy cannot be handed an unvalidated
request.

```go
package manager

// PreState is what the prepare-read returned: one or two set reads that agree on
// epoch and table revision, and the store's clock.
type PreState struct {
	Table        string
	Epoch        uint64
	Revision     uint64
	MemberPrefix string              // from the set read (R3); empty until then
	Rows, Cols   []string            // declared rows and columns (R3); nil until then
	Now          int64               // store clock, ms (R8); else the binding's clock
	Cards        []Card              // sorted by ID
	Missing      []string            // sorted
	Foreign      []string            // IDs in another epoch (R4)
	Operation    *OperationRecord    // op.<operation ID>, when present
}

type Card struct {
	ID       string
	Revision uint64
	Row      string // the stream
	State    string // the column; "" when unplaced
	Score    float64
	Fields   map[string]string
}

type OperationRecord struct {
	Hash, Kind, Actor string
	Revision          uint64 // the committed table revision
	Receipt           []byte // the canonical card receipt
}

type Policy struct { /* §4.1 */ }

func LoadPolicy(canonical []byte) (Policy, error)
func (p Policy) Digest() string

// Decision is exactly one of: a write, a refusal, a report, a replay.
type Decision struct {
	Write   *Write
	Refusal *request.Refusal
	Report  *Report
	Replay  *request.Receipt // the stored receipt of this operation ID
}

type Write struct {
	Manifest  ntable.BatchManifest
	Canonical []byte          // the exact bytes to apply
	Expect    request.Receipt // what the table's receipt must agree with
}

// Report is the result of inspect and check, and of a decision that writes nothing.
type Report struct {
	Table    string
	Epoch    uint64
	Revision uint64
	Written  bool // false: nothing was applied
	Cards    []CardReport
	Drift    []Drift
}

type CardReport struct {
	ID, Row, State string
	Revision       uint64
	AgeInPlace     time.Duration // pre.Now - placed.at
	Idle           time.Duration // pre.Now - at
	Pin            definition.Pin   // def, blob, commit, path, kind, deps, policy
	Lineage        Lineage          // pred, succ, gen
	Head           string
	Standing       Standing         // §4.3, recomputed
	OtherHeads     []request.Record // kept, not applicable to the current head
	Reds           RedHistory       // counters, log.red, last.green
	Returns        ReturnHistory    // counters, log.ret
	Counters       Counters         // every cy.* field
	Marks          []Mark           // §3.9.5, from the card's policy
	Deps           []DepClass       // met, open, failed, missing, foreign
	Next           []request.InputType
	Drift          []Drift
}

// Scope is the first read a request needs; FollowUp the second, when the
// decision needs cards the first read could not know (dependencies).
func Scope(req request.Request) ntable.ReadSetScope
func FollowUp(pre PreState, req request.Request) []string

// Touches is every card the decision will pin, with its role: the request's own
// cards (both sides of a replacement) and the dependencies it guards. One uniform
// enumeration: no consumer switches on the operation or loops per card.
func Touches(pre PreState, req request.Request) []Touch

func DecideAdmit(pre PreState, req request.Admit, policy Policy) Decision
func DecideResolve(pre PreState, req request.Resolve, policy Policy) Decision
func DecideEvents(pre PreState, req request.Events, policy Policy) Decision   // cancel included
func DecideEvidence(pre PreState, req request.Evidence, policy Policy) Decision
func DecideReplace(pre PreState, req request.Replace, policy Policy) Decision
func DecideInspect(pre PreState, req request.Inspect, policy Policy) Decision
func DecideCheck(pre PreState, req request.Check, policy Policy) Decision

// Evaluate is §4.2 over the fields of a set of cards, for inspect and for tests.
func Evaluate(cards []Card, policy Policy) []Standing

// Notifications derives the notifications and the health line of §3.9 from a
// range of receipts, with the policies the cards pin, at the store time now.
func Notifications(receipts []ntable.StreamReceipt, cards []Card, policies PolicySet, now int64) ([]Notification, Health)

// Confirm compares a committed table receipt with the expected card receipt.
func Confirm(expect request.Receipt, got ntable.Receipt) error
```

`request.Request` is the interface every validated shape satisfies (assumption A-R1):
`Kind()`, `OperationID()`, `Table()`, `Epoch()`, `ObservedRevision()`, `Actor()`,
`Hash()`, `Canonical()`, and `Cards()`, which enumerates every card the request names with
its role and its expected place and revision. `Scope`, `FollowUp` and `Touches` are built
on `Cards()` alone.

---

## 6. The store binding (part 4 of 5)

`internal/card/binding` runs one request against one store. It decides nothing.

**One verb, in order.**

1. **Read.** One `ns_table_read_set` (`FCALL_RO`) over `manager.Scope(req)`, including
   `op.<operation ID>` for a mutating request. For resolve and inspect, a second read of
   `manager.FollowUp(pre, req)`. When the two reads disagree on epoch or table revision,
   the pair is read once more; a second disagreement refuses `BUSY` (`changed=no`), with
   the same command as the remedy. A read never loops.
2. **Decide.** Call the operation's `Decide` function with the pre-state and the policy
   the cards pin.
3. **Refusal, report or replay:** return it; nothing is written. A `STALE` refusal is
   completed with who and what: one read of the receipt stream after the caller's cursor
   (R7), listing each receipt's actor, operation and changed cards.
4. **Pending.** Before the apply, the caller (part 5) writes the operation ID and the
   request hash into its context file as pending, by atomic rename.
5. **Apply.** One `ns_table_apply` (`FCALL`) of `Write.Canonical`.
6. **Confirm.** Compare the table's receipt with `Write.Expect` (`manager.Confirm`);
   clear the pending mark; return the card receipt and the context's new observations.

**Failure classes.** `changed` is derived from the class, never guessed:

| class | cause | `changed` | exit | remedy |
|---|---|---|---|---|
| refusal | a manager or table guard refused | `no` | 1 | the refusal's own command |
| transport | the connection failed or the deadline passed before a reply | `unknown` | 2 | `card reconcile --context <file>` |
| store error | the store answered with an error after the script began staging writes (a script error is not rolled back) | `unknown` | 2 | `card reconcile --context <file>`, then `check` |
| disagreement | the committed receipt differs from the expected one | `yes` | 2 | `check --context <file>` and the receipt shown |
| busy | two reads disagreed twice | `no` | 1 | the same command |

**Operation identity and reconciliation.** The operation ID is the request's. A lost reply
leaves the context's pending mark; the next command in that context refuses until it is
reconciled. `card reconcile` re-runs the pending request under the same operation ID: its
prepare-read finds the card operation record (a replay: the stored receipt) or does not
(the batch never committed: decide and apply afresh). At most one effect exists, because
the record is created with the effect and refuses a second creation. No transport retry
invents an operation ID.

**Deadlines.** One dial through `internal/redisconn`, bounded; each call carries a
deadline (default 5 s for a read, 10 s for an apply); the verb's `--timeout` bounds the
whole. A deadline passed during an apply is transport failure, `changed=unknown`.

**Round trips per verb** (application round trips after the dial):

| verb | reads | apply | total | with R5 |
|---|---|---|---|---|
| `card add` | 1 | 1 | 2 | 2 |
| `card resolve` | 2 | 1 | 3 | 2 |
| `card move`, `card cancel` | 1 | 1 | 2 | 2 |
| `card evidence` | 1 | 1 | 2 | 2 |
| `card replace` | 1 | 1 | 2 | 2 |
| `card inspect` | 2 | 0 | 2 | 1 |
| `card notifications` | 1 (stream read and set read, pipelined) | 0 | 1 | 1 |
| `check` | 1 (`ns_table_check` and set read, pipelined) | 0 | 1 | 1 |
| `card reconcile` | 1 | 0 or 1 | 1 or 2 | 1 or 2 |
| `card context` | 1 | 0 | 1 | 1 |
| a `STALE` refusal | +1 (the receipts since the cursor) | 0 | +1 | +1 |
| `help`, `version`, `-h` | 0 | 0 | 0 | 0 |

A replay is found by the prepare-read: one round trip, no apply.

---

## 7. The verbs (part 5 of 5)

### 7.1 The binary

The verbs live in a **new binary under cmd/**, a Go program over the three card packages
and `internal/ntable`, importing nothing of dispatch. None of the living tools fits:

| living tool | why not the host |
|---|---|
| nova-table | the general table; it handles mechanical conditions and must not know card lifecycle, reviews or policy |
| nova-swarm | the worker pool of the dispatch layer; its workers run inside a sandbox and must not hold the coordinator's write powers, and the card layer is below dispatch |
| nova-config | permanent configuration in Postgres, applied into Redis; cards are live state |
| nova-bus | messages over Git; no table |
| the rest | unrelated purposes (checks, CI, secrets, sandbox, tokens, memory, release) |

The binary's name is open question Q20: the names a reader would expect belong to tools
parked under deprecated/, which are not a base, and the documentation rule refuses a
parked tool's name until its cmd/ directory exists. The examples below write the verb
path without the binary name, as the specification's grammar does.

`card seed` is the disposable-store setup of the interactive session (§8.5; its spelling
is Q28). The grammar follows CLI-STYLE: `<tool> <verb path> [flags] [positionals]`, flags before
positionals, one spelling per flag and one thing per flag, `-h` on every verb at exit 0
with no side effects, and help touches no store.

### 7.2 The context file and the input shapes

**The context file** (`--context <file>`) holds what the coordinator selected and
observed, so that commands stay short, and nothing is guessed:

| field | meaning | updated by |
|---|---|---|
| `store` | `addr`, `user`, `password_env` (a variable's name, never a value), or `seat` | `card context` |
| `table`, `epoch` | the selected table and its observed epoch | `card context`, every receipt |
| `revision` | the observed table revision | every receipt, `card inspect` |
| `cursor` | the receipt stream position the coordinator has read to | `card notifications` |
| `repo`, `commit` | the definition repository (its identity and local path) and the commit to admit from | `card context` |
| `policy` | the policy file's path and digest | `card context` |
| `actor` | the acting identity (`--as` overrides) | `card context` |
| `cards` | per card: observed revision, place and head | every receipt, `card inspect` |
| `pending` | an unconfirmed operation: its ID and request hash | set before an apply, cleared by its receipt or by `card reconcile` |

It is written by atomic rename (`internal/atomicfile`). A stale context refuses with one
set-refresh command (`card inspect --context ctx.json`, or naming the cards), never a
silent refresh (P17). Receipts carry every revision the next command needs (P18).

**The input shapes**, one flag each (P2); every shape is an array with explicit IDs, or a
scope (P3), and the positional and flag forms of each verb compile to exactly the same
shape:

| flag | shape |
|---|---|
| `--admissions <file>` | an array of admissions: card file path, stream |
| `--events <file>` | an array of lifecycle inputs: card, input type, source state, expected revision, payload |
| `--evidence <file>` | an array of evidence entries: card, expected place and revision, records |
| `--replacements <file>` | an array of pairs: old card, expected revision and place, new card file, stream |
| `--scope <file>` | a scope: card IDs, or rows, or the whole table |

**Common flags.** `--context <file>` (required by every store verb, in place of the
specification's `--table`, Q31); `--op <id>` (the
operation ID; when omitted, `h-` and the first 20 hex digits of the request's hash taken
without its operation ID, so an identical retry has the same ID: open question Q21);
`--as <identity>`; `--timeout <duration>`; `--max <n>` (displayed items, default 20, 0
all). `--rows <row>,...` selects rows as a scope where a verb takes one.

### 7.3 The grammar

```
card context       --out <file> --table <table> (--addr <host:port> [--user <name>] [--password-env <NAME>] | --seat <name>) --repo <dir> --commit <commit> --policy <file> --as <identity>
card add           --context <file> [--op <id>] (--stream <row> <card-file>... | --admissions <file>)
card resolve       --context <file> [--op <id>] [--rows <row>,... | --scope <file>] [<id>,...]
card move          --context <file> [--op <id>] --to <state> [--input <type>] [--head <commit>] [--artifact <ref>] [--by <id>,...] [--result done|failed|returned] [--why <text>] [--because <id>] [--verifier <name>] [--rows <row>,...] [<id>,...]
card move          --context <file> [--op <id>] --events <file>
card evidence      --context <file> [--op <id>] (--read accept|reject --by <reader> | --ci ok|red --check <name> | --sweep | --landing <commit>) --artifact <ref> --verifier <name> [--head <commit>] [--rows <row>,...] [<id>,...]
card evidence      --context <file> [--op <id>] --evidence <file>
card cancel        --context <file> [--op <id>] --why <text> [--rows <row>,...] [<id>,...]
card replace       --context <file> [--op <id>] --replacements <file>
card inspect       --context <file> [--history] [--rows <row>,... | --scope <file>] [<id>,...]
card notifications --context <file> [--since <cursor>] [--rows <row>,...] [--only judgment|escalated] [--cards] [--max <n>]
card reconcile     --context <file>
card seed          --dir <dir> --addr <host:port> [--cards <n>] [--clean]
check              --context <file> [--rows <row>,...]
help               [<verb>]
version
```

**`card move --to`.** The lifecycle input is named by the card's observed state and
`--to` (Q27); where two inputs share a pair, `--input` names it, and a missing `--input`
is refused naming the choices:

| observed state | `--to` | input | payload flags |
|---|---|---|---|
| ready | working | `start` | `--by` (taker) |
| working | review | `result` | `--head`, `--artifact`, `--by` (authors), `--result` |
| review | ready | `verdict-rework` | `--why` |
| review | merging | `verdict-accept` | — |
| merging | landed | `landed` | — |
| merging | review | `queue-rejected` or `head` (`--input` required) | `--why`, `--artifact`, `--verifier` / `--head`, `--artifact`, `--by` |
| review | review | `head` | `--head`, `--artifact`, `--by` |
| review | done | `complete` | — |
| waiting | done | `depfail` (`--input depfail`) | `--because`, `--why` |
| waiting, ready, working | landed | `external-landing` | — |

**Outputs and exits** (P14, P15, CLI-STYLE e, f, l). Every mutating verb prints one summary
line, then one `CARD` line per selected card, on stdout:

```
<TOKEN> OK op=<id> hash=<request hash, 12 hex> receipt=<table>/<epoch>/<rev> rev=<before>-><after> selected=<n> eligible=<n> changed=<n> blocked=<n> already=<n> trips=<n>
CARD <id> <from>-><to> rev=<a>-><b> [outcome=… succ=… landing=… recorded=… standing=… esc=…]
CARD <id> blocked on=<dep>:<class>[,...] | already | ineligible state=<state> | inapplicable record=<kind>
```

A refusal is one line on stderr, with the observed state and the next command:
`<TOKEN> REFUSED op=<id> code=<CODE> card=<id> expected=<x> observed=<y> changed=no run: <command>`;
a `STALE` refusal adds one `BY` line per intervening receipt
(`BY receipt=cards/3/64 actor=coordinator-2 op=op-move-7 cards=c-emit,c-lex`). An
uncertain write is `<TOKEN> UNCONFIRMED op=<id> class=transport|store changed=unknown run:
card reconcile --context ctx.json`. Exit 0: done, replayed, or clean; 1: a lifecycle
refusal or a negative check; 2: could not run, or unconfirmed.

**Per verb.** Each verb's three examples are: one card, several, and a whole scope.

| verb | examples | output tokens |
|---|---|---|
| `card context` | `card context --out ctx.json --table cards --addr 127.0.0.1:6390 --repo ./briefs --commit bf1c365741a4 --policy policy.json --as coordinator`; the same with `--seat coord`; the same over another table | `CONTEXT OK out=ctx.json table=cards epoch=3 rev=41 cards=0 trips=1` |
| `card add` | `card add --context ctx.json --stream build cards/c-lex.md`; `card add --context ctx.json --stream build cards/c-lex.md cards/c-parse.md cards/c-emit.md`; `card add --context ctx.json --admissions admissions.json` | `ADD`, `CARD` |
| `card resolve` | `card resolve --context ctx.json c-emit`; `card resolve --context ctx.json c-emit,c-lex,c-parse`; `card resolve --context ctx.json` (every row) | `RESOLVE`, `CARD … blocked on=…` |
| `card move` | `card move --context ctx.json --to working c-lex`; `card move --context ctx.json --to working c-lex,c-fmt,c-tidy`; `card move --context ctx.json --to working --rows build` (every ready card of `build`; the rest `ineligible`) | `MOVE`, `CARD` |
| `card evidence` | `card evidence --context ctx.json --ci red --check unit --artifact run:9003 --verifier forge c-emit`; `card evidence --context ctx.json --read accept --by reader-a --artifact review:301 --verifier forge c-parse,c-lex,c-emit`; `card evidence --context ctx.json --evidence ci-runs.json` (an observer's batch over a scope) | `EVIDENCE`, `CARD … recorded=… standing=…` |
| `card cancel` | `card cancel --context ctx.json --why 'superseded by the plan' c-fmt`; the same with `c-fmt,c-lint,c-tidy`; the same with `--rows docs` | `CANCEL`, `CARD` |
| `card replace` | `card replace --context ctx.json --replacements one.json`; `… --replacements three.json`; `… --replacements docs.json` | `REPLACE`, `CARD … succ=…` |
| `card inspect` | `card inspect --context ctx.json c-emit`; `card inspect --context ctx.json c-emit,c-lex,c-parse`; `card inspect --context ctx.json` (the whole table) | `INSPECT`, `CARD`, `RED`, `RETURN` |
| `card notifications` | `card notifications --context ctx.json --rows build --only judgment`; `card notifications --context ctx.json`; `card notifications --context ctx.json --since 0-0 --cards` | `NOTIFICATIONS`, `NOTE`, `CARD`, `HEALTH`, `MORE` |
| `card reconcile` | `card reconcile --context ctx.json` (one pending operation; there is never more than one) | `RECONCILE OK op=… replay=yes\|no` |
| `check` | `check --context ctx.json --rows build`; `check --context ctx.json --rows build,docs`; `check --context ctx.json` | `CHECK OK\|FAIL drift=<n>`, `DRIFT` |

**`card inspect` output**, the third example over a scope, where `c-emit` has gone red
three times at two heads:

```
INSPECT OK table=cards epoch=3 rev=97 now=1700020000000 selected=3 trips=2
CARD c-emit place=build:review age=40m rev=13 head=9a1b2c3d4e5f kind=fix-red standing=reads=2/2,ci=unit:red,sweep=stale block=ci:unit@run:9007 reds=3 red-heads=2 red-here=2 flaky=1 mret=2 last-red=9a1b2c3d4e5f:unit:run:9007 last-green=4f92c044d819:unit:run:9001 esc=red-same-head,red-total,merge-return,flaky,returns next=head,verdict-rework,cancel
CARD c-lex place=build:working age=35m rev=3 taker=worker-1 starts=1 results=0 next=result,cancel
CARD c-parse place=build:review age=2h10m rev=6 head=bf1c36574 standing=reads=2/2,ci=unit:ok,sweep=none need=sweep next=verdict-rework,head,cancel
```

With `--history`, each card adds its logs, newest first:

```
RED c-emit at=1700019000000 head=9a1b2c3d4e5f check=unit run=run:9007
RED c-emit at=1700016000000 head=9a1b2c3d4e5f check=unit run=run:9005
RED c-emit at=1700007200000 head=4f92c044d819 check=unit run=run:9003
RETURN c-emit at=1700016000000 from=merging to=review cause=ci:unit-red
RETURN c-emit at=1700007200000 from=merging to=review cause=ci:unit-red
```

The first example (`c-emit` alone) prints its `CARD` line and, with `--history`, these
five lines; the second prints three `CARD` lines.

### 7.4 The five everyday things, one command each

| the coordinator wants to | one command |
|---|---|
| admit several briefs | `card add --context ctx.json --stream build cards/c-lex.md cards/c-parse.md cards/c-emit.md` |
| see where everything is | `card inspect --context ctx.json` (and the table itself: `nova-table watch cards --every 1s`) |
| move a set | `card move --context ctx.json --to working c-lex,c-fmt,c-tidy` |
| record a read for several cards | `card evidence --context ctx.json --read accept --by reader-a --artifact review:301 --verifier forge c-parse,c-lex,c-emit` |
| cancel several | `card cancel --context ctx.json --why 'superseded by the plan' c-fmt,c-lint,c-tidy` |

### 7.5 How the coordinator learns what happened

A CI result, red or green, a reader's accept or reject, every move and every refusal of
someone else's batch reach the coordinator as notifications derived from the receipt
stream (§3.9.3). The one command over a whole scope:

```
card notifications --context ctx.json
```

reads every receipt after the context's cursor — at most 64 receipts, one round trip
(the stream read and a set read of the scope, pipelined; R7) — derives the
notifications, prints them, and advances the cursor. A `MORE` line says when more
remain; the next run continues from `next=`. Reading from an older cursor repeats the
same notifications with the same identities, so a missed or duplicated delivery is
harmless. `--only judgment` prints only J points, `--only escalated` only escalated ones;
`--cards` adds one line per card of the scope by place, with age in place, cycle counters
and marks.

```
NOTIFICATIONS OK since=1700007100000-0 next=1700007200000-0 receipts=1 notes=3 judgment=2 escalated=2 trips=1
NOTE id=1700007200000-0/c-emit/ci-red card=c-emit row=build kind=ci-red class=J sev=escalated marks=flaky head=4f92c044d819 ev=2d0843b2c42d7689 issuer=ci:unit run=run:9003 standing=reads=2/2,ci=unit:red,block=ci:unit@run:9003 op=op-ev-1 actor=ci-observer
NOTE id=1700007200000-0/c-emit/returned card=c-emit row=build kind=returned class=J sev=escalated marks=flaky from=build:merging to=build:review cause=ci:unit-red mret=1 op=op-ev-1 actor=ci-observer
NOTE id=1700007200000-0/c-parse/read-accept card=c-parse row=build kind=read-accept class=M sev=info issuer=reader-a review=review:301 standing=reads=2/2,ci=unit:ok,need=sweep op=op-ev-1 actor=ci-observer
HEALTH since=1700007100000-0 receipts=1 forward=0 returns=1 terminations=0 stale=0 escalated=1
```

The three examples are the table's first row in §7.3: the J points of one row since the
cursor, everything since the cursor, and the whole history of the scope with its cards.
Until the table provides the stream read (R7), `card inspect` is the fallback: it shows
the same counters, logs, standing and marks per card, without the per-receipt history.

---

## 8. Test plan

Unit tests own no sockets and finish in well under a second per package; every test
opens with `t.Parallel()`. Functional tests use one throwaway `redis-server` per package
(`internal/testredis`). TLC runs on a bench, never on the working machine.

### 8.1 Part 3, the policy (unit)

- **Guard tables.** One table per `Decide` function with a row for every guard (C1 to
  C11, A1 to A7, S1 to S5, E1 to E7, V1 to V8, X1 to X4): the pre-state, the request, and
  the expected decision; every refusal row checks the code, the card, expected and
  observed, `changed=no` and the remedy command.
- **Outcomes.** Every outcome of §3.0 and every effect of §3.4 (counts, other head,
  forced return, inapplicable) has a row, with the manifest entry it produces.
- **Manifest invariants,** asserted on every decision any test makes: changed card
  entries are exactly the `changed` outcomes; every existing card carries revision and
  place; no move changes a row; every write creates the card operation record; guard-only
  entries carry no change; the encoded size and counts are within §2.7.
- **Determinism.** The same request and pre-state give the same bytes; a request with its
  entries or records permuted gives the same bytes.
- **Bounds.** N = 1 and each always-fits maximum (127 admissions, 63 pairs, 127 inputs,
  127 evidence cards with 512 records, a 113-card resolve scope with 8 dependencies each)
  produce a write within the table's bounds; one over each is refused by the manager and
  never reaches the table.
- **Evidence and red handling:** a red at the current head in merging moves the card to
  review in the same entry; a red at an old head moves nothing and says "not applicable
  to the current head"; a red for a card in review moves nothing and blocks; a red for a
  card in waiting is `inapplicable`; two reds of one check at one head in one entry refuse
  `CONFLICT`; reds of two checks both count and move the card once; a reader's reject in
  merging forces the return; a green after a red at one head records and clears nothing;
  `cy.flaky` advances once per check and entry.
- **Counters:** a replayed operation ID advances nothing; an `already` record advances
  nothing; every counter of §2.3 advances in exactly the transitions §3.3 and §3.4 name.
- **Standing:** `Evaluate` against a table of record sets (quorum, author exclusion,
  reserved issuers, required readers, required checks, sweep clean, stale and negative,
  records at other heads).
- **The classification test** of §3.9.1, and the notification derivation for every row of
  §3.9.2, including severity and marks at each threshold of §3.9.5.

**Property test against the model.** Sixteen fixed seeds, 128 steps each, batch sizes 1
to 3 and, separately, up to the maxima: each step draws a random request (admissions,
resolves over scopes, lifecycle inputs, evidence with random heads and dispositions,
replacements, replays of earlier operation IDs, stale revisions), decides it against the
current image, applies the manifest to an in-memory image by the table contract, and
checks:

- the step against a Go transcription of `CardManager`'s transition relation: an
  accepted decision is exactly one enabled action (`AdmitBatch`, `ResolveSet`,
  `ApplyEvents`, `RecordEvidence` with its forced return, `ReplaceDefinitions`,
  `NoOpBatch`) with the same effect on the abstract state; a refusal is the stutter;
- the model's invariants on the abstract state after every step;
- every return in the step's receipt yields a return notification from that receipt;
- no card's returns exceed a threshold without an escalated notification having been
  derived for it.

The seed and the inputs are printed on failure. The in-memory image is checked against
owned Redis on the same seeds in the functional tier (§8.2), so the pure tier cannot drift
from the table. Recorded traces are validated against the model with TLC on a bench.

### 8.2 The model's additions

`CardManager.tla` gains, beside the properties listed in §3.8:

| property | statement | reversed witness |
|---|---|---|
| forced return | a negative at the head recorded for a card in merging moves it to review in the same step | "red recorded, card still in merging" (violates `MergingIsAuthorized`) |
| counters only with a receipt | a counter changes only in a step that writes a receipt | "counter advanced without a receipt" |
| replay changes nothing | a replayed operation ID changes no card and no counter | "replay advances a counter" |
| no shuffle without notification | every return step's receipt carries that return | "return without its log entry" |
| bounded shuffle | a card whose returns reach the threshold has an escalated notification in the receipt of the step that reached it | "threshold crossed silently" |
| the card operation record | every accepted mutating step creates exactly one operation record, and a present record makes the same request a stutter returning its receipt | "second effect under one operation ID" |

The named instance stays three cards, batch sizes 1 to 3, two streams, two epochs, two
heads and two reviewers, with a threshold of 2 so that bounded shuffle is reachable.

### 8.3 Part 4, the binding (functional, owned Redis)

- **Trips.** Round trips per verb as §6, counted by `internal/redisconn`, at N = 1 and at
  each maximum.
- **Refusals leave the store image unchanged.** Every key dumped before and after, for
  every refusal code, including a late invalid entry, a failing guard on a forced return
  (nothing recorded, nothing moved, no counter), and an operation record that already
  exists with another hash.
- **Replay and lost reply.** The reply dropped after the apply: the verb reports
  `changed=unknown`; `card reconcile` finds the record and returns the stored receipt;
  exactly one effect in the store; counters advanced once. The same operation ID run
  again after later batches returns the stored receipt byte for byte. Two concurrent runs
  of one operation ID: one commits, the other replays.
- **Store error.** A script failure after staging is reported as `store`, `changed=unknown`.
- **Busy.** Two reads that disagree twice refuse `BUSY`.
- **Receipt replay.** Every batch receipt maps to one `CardManager` action through the Go
  replay checker; refusals and uncertain observations map to stutters.

### 8.4 Part 5, the verbs

- Every verb answers `-h` at exit 0 with no store touched (`internal/testverbhelp`); the
  First run section in docs/CLI.md is executed by the tool's first-run test.
- Every printed remedy is run through the CLI and does what it names (CLI-STYLE k).
- The context file: atomic writes; a pending operation blocks the next command until
  reconciled; a stale context refuses with its `BY` lines and the refresh command.
- `card seed` refuses an occupied target and cleans up repeatably.

### 8.5 The interactive session

On an explicitly owned, disposable store, with the table visible at one-second refresh,
the maintainer drives every verb and refusal; every hole is recorded, fixed, and its case
repeated until the session ends clean. Commands and elapsed seconds are recorded for the
drills the specification names (one card from admission to landed; ten admitted and five
moved in one command each; five changes named in one breath, performed in one command).

**Setup.**

```
nova-redis serve --bind 127.0.0.1 --port 6390 --dir demo/store
nova-redis fn load --addr 127.0.0.1:6390
card seed --dir demo --addr 127.0.0.1:6390
nova-table watch cards --every 1s
```

`card seed` creates the table (`card:` prefix, the seven columns, rows `build`, `docs`,
`tests` and `ops`), the context `demo/ctx.json`, the policy, a Git repository of twelve
committed card files, and the input files the drills use; it prints the paths, the table,
the epoch and the cleanup command.

**The seed.**

| card | stream | kind | depends on | exercises |
|---|---|---|---|---|
| `b-lexer` | build | fix-red | — | admission to landed |
| `b-parser` | build | fix-red | `b-lexer` | blocked until a landing |
| `b-emitter` | build | fix-red | `b-parser`, `d-grammar` | two dependencies across streams |
| `b-cli` | build | fix-red | `b-emitter` | a chain |
| `d-grammar` | docs | text | — | non-PR completion |
| `d-guide` | docs | text | `d-grammar` | a non-PR dependency met |
| `d-errors` | docs | report | `b-parser` | a dependency in another stream |
| `d-faq` | docs | tone | `x-gone` | a missing dependency |
| `t-lexer` | tests | fix-red | `b-lexer` | resolve after a landing |
| `t-parser` | tests | mutation-kill | `b-parser` | a blocked card cancelled |
| `t-fuzz` | tests | sweep | — | red CI, repeated reds |
| `t-old` | tests | fix-red | — | replaced by `t-new` |

**The asks, one command each.**

| # | the maintainer asks | the command |
|---|---|---|
| 1 | admit all twelve | `card add --context demo/ctx.json --admissions demo/admissions.json` |
| 2 | where is everything? | `card inspect --context demo/ctx.json` |
| 3 | what can start? | `card resolve --context demo/ctx.json` |
| 4 | why is `d-faq` blocked? | `card inspect --context demo/ctx.json d-faq` |
| 5 | start the three ready cards of build, docs and tests | `card move --context demo/ctx.json --to working b-lexer,d-grammar,t-fuzz` |
| 6 | their results are in | `card move --context demo/ctx.json --events demo/results.json` |
| 7 | CI: green on `b-lexer`, red on `t-fuzz` | `card evidence --context demo/ctx.json --evidence demo/ci.json` |
| 8 | what happened? | `card notifications --context demo/ctx.json` |
| 9 | two readers accept `b-lexer` and `d-grammar` | `card evidence --context demo/ctx.json --evidence demo/reads.json` |
| 10 | can `b-lexer` merge? | `card inspect --context demo/ctx.json b-lexer` |
| 11 | sweep and merge it | `card evidence --context demo/ctx.json --sweep --artifact obs:auto --verifier forge b-lexer`, then `card move --context demo/ctx.json --to merging b-lexer` |
| 12 | CI went red in the queue | `card evidence --context demo/ctx.json --ci red --check unit --artifact run:12 --verifier forge b-lexer` |
| 13 | how often has `t-fuzz` gone red, and at which heads? | `card inspect --context demo/ctx.json --history t-fuzz` |
| 14 | a fix is pushed to `b-lexer` | `card move --context demo/ctx.json --to review --head 9a1b2c3d4e5f --artifact pr:1 --by worker-1 b-lexer` |
| 15 | it landed | `card evidence --context demo/ctx.json --landing 5d6e7f8a9b0c --artifact land:5d6e7f8a9b0c --verifier forge b-lexer`, then `card move --context demo/ctx.json --to landed b-lexer` |
| 16 | `d-grammar` is complete | `card move --context demo/ctx.json --to done d-grammar` |
| 17 | what is ready now? | `card resolve --context demo/ctx.json` |
| 18 | cancel `d-faq` and `t-parser` | `card cancel --context demo/ctx.json --why 'out of scope' d-faq,t-parser` |
| 19 | replace `t-old` | `card replace --context demo/ctx.json --replacements demo/replace.json` |
| 20 | only what needs my judgment, in tests | `card notifications --context demo/ctx.json --rows tests --only judgment` |
| 21 | someone else changed cards (a second context moves `b-parser`) | the next `card move` refuses `STALE` with its `BY` lines; `card inspect --context demo/ctx.json` refreshes |
| 22 | the reply was lost | `card reconcile --context demo/ctx.json` |
| 23 | a duplicate ID, an unknown stream, an over-limit batch | `card add --context demo/ctx.json --admissions demo/dup.json`; `card add --context demo/ctx.json --stream nowhere demo/cards/x-one.md`; `card add --context demo/ctx.json --admissions demo/over.json` |
| 24 | evidence at the wrong head | `card evidence --context demo/ctx.json --read accept --by reader-a --artifact review:9 --verifier forge --head 0badc0ffee00 b-emitter` |
| 25 | ten more, then move five | `card seed --dir demo --addr 127.0.0.1:6390 --cards 10`, `card add --context demo/ctx.json --admissions demo/ten.json`, `card resolve --context demo/ctx.json`, `card move --context demo/ctx.json --to working n-01,n-02,n-03,n-04,n-05` |
| 26 | how healthy is the build stream? | `card notifications --context demo/ctx.json --rows build --cards` |
| 27 | is the table sound? | `check --context demo/ctx.json` |
| 28 | clean up | `card seed --dir demo --addr 127.0.0.1:6390 --clean` |

---

## 9. Open questions, requirements and assumptions

### 9.1 Open questions

Each is a place where the specification is silent, ambiguous, or at odds with the table
contract or the model. The design above follows the recommendation and cites the question
where it does.

| # | question | options | recommendation |
|---|---|---|---|
| Q1 | What does "invalidate superseded decisions in the same transaction where required" mean for record evidence? | (a) record evidence removes nothing: negatives block, head changes and rework unset; (b) a later record from one issuer at one head replaces the earlier (last wins) | (a): append-only, as the model's evidence property |
| Q2 | Which input names a card's stream? | (a) the admission entry names the row; (b) a STREAM header field in the card file | (a): the row is placement, not definition |
| Q3 | What orders a cell? | (a) the committing table revision; (b) the store clock; (c) a caller priority | (a): deterministic; ordering beyond admission belongs to sprint and order |
| Q4 | Card ID bound | (a) 64 bytes; (b) the table's 256 | (a) |
| Q5 | DEPENDS-ON bound | (a) 8: a 113-card resolve always fits; (b) 32: only 31 cards always fit; (c) none, with manager refusals | (a) |
| Q6 | Where is the review policy pinned? | (a) per card at admission; (b) one per table; (c) per request | (a): a card is judged by the rules it was admitted under, and the pin is guardable |
| Q7 | Who is an author for reader eligibility? | (a) every identity that produced any head of the card; (b) the current head's authors only | (a): conservative independence |
| Q8 | A resolve whose eligible set exceeds 127 | (a) refuse `LIMIT`, naming the count and the first 127 eligible IDs; (b) a declared `deferred` outcome, moving the first 127 by ID order | (a): the specification forbids chunking; (b) is a convenience for the sprint layer |
| Q9 | A decision that changes no card | (a) write it (guards and operation record) only when an outcome is a judgment point; (b) always write (a revision spent, other prepared manifests go stale); (c) never write | (a) |
| Q10 | Should refusals be kept where the coordinator reads them? | (a) delivered to the caller only, the store unchanged (P8); (b) also appended to a log outside the owned store image | (a) here; (b) belongs to the console layer, reading the caller's output |
| Q11 | Admitting an ID already admitted with the same definition and row | (a) `already`; (b) refuse `EXISTS` | (a); any difference refuses |
| Q12 | A dependency in another epoch | (a) never met: blocked as `foreign`, a judgment point (needs R4); (b) met when landed in an earlier epoch | (a); an old ID is never re-admitted, a replacement takes a new ID |
| Q13 | Code landed while its card was in review without the reads | (a) allow `external-landing` from review, always escalated; (b) a new input `landed-unreviewed`; (c) cancel, recording the landing in `why` | (a): the landing is a fact; the escalation is the judgment |
| Q14 | Cancel or replace from merging, while the queue may still land the change | (a) not from merging: `queue-rejected` (dequeue) first; (b) allow, and a later landing record for a terminal card is an escalated `inapplicable` notification | (a) |
| Q15 | Must an evidence entry carry the card's expected revision? | (a) optional: an observer's expectation is the head and digest it observed; (b) required, as every other entry | (a); the manifest always guards the pre-state revision |
| Q16 | What clears a negative at the same head (a flaky check)? | (a) only a new head or a rework; (b) a coordinator waiver record naming the negative, itself a judgment point | (a) for the first version; (b) if the interactive session shows flaky checks |
| Q17 | Replacing a card in review | (a) directly; (b) rework first, then replace | (a), as the specification and the model allow |
| Q18 | Check of a table over 1,024 cards | (a) refuse, naming rows; (b) a paged check with a cursor and a final revision equality | (a) |
| Q19 | Should a card past an escalation threshold be **held**? | (a) mark and notify only; (b) the batch that crosses a threshold also sets `hold`; lifecycle inputs from actors not in the policy's `coordinators` then refuse `HELD` until a coordinator's `release` input with a reason | (b): it is data guarded in the manifest, changes nothing for a coordinator acting by hand, and stops an automated upper layer from looping a card |
| Q20 | The binary's name | (a) return the parked card tool's name to cmd/ as a new tool, the old code not a base; (b) a new name; (c) the rebuilt sprint tool when it returns | (a): the verbs are the card layer's, and that is the name a reader expects |
| Q21 | The operation ID | (a) `--op` required on every mutating command; (b) optional, defaulting to one derived from the request hash without its ID | (b): an identical retry gets the same ID, a different request another |
| Q22 | Is the sweep required? | (a) yes: a negative cannot be left out without the verifier leaving it out; (b) no: the recorded set is trusted | (a) |
| Q23 | A non-code card completed outside the system | (a) no input: `result`, reads and `complete`; (b) an `external-complete` input with a recorded completion | (a) for the first version |
| Q24 | External landing with dependencies unmet | (a) allowed, a judgment point; (b) refused | (a): refusing a fact hides it |
| Q25 | Required CI for non-PR kinds | (a) PR kinds only, as the model; (b) per kind in the policy | (a) |
| Q26 | Where done sits | (a) the seventh column, hidden in the default render; (b) a separate table | (a) |
| Q27 | `card move --to` | (a) infer the input from the observed state and `--to`, `--input` when ambiguous; (b) `--input` always | (a) |
| Q28 | The setup helper | (a) the verb `card seed`; (b) a program under tools/ | (a): tested with the binary, disposable targets only |
| Q29 | `standing` as a stored field | (a) stored and checked, so a receipt carries the readiness change; (b) computed at read time only | (a): a receipt's delta alone cannot say what changed about readiness otherwise |
| Q30 | The escalation and stale thresholds | the defaults of §3.9.5 | confirm, or name others |
| Q31 | The specification's `--table <table>` and `check <table>` | (a) the context file names the table; (b) keep `--table` beside the context | (a): one source that cannot disagree with itself |

### 9.2 Requires from the table layer

| # | requirement | why | without it |
|---|---|---|---|
| R1 | a read-only lookup of the table's operation record by (table, epoch, operation ID) inside the set read | replay recognised before any manifest | the card operation record member (§2.5) |
| R2 | an operation-level annotation in the manifest: a bounded opaque string stored with the operation record and returned in the receipt, unguarded | the card request hash and the non-changing outcomes in the authoritative receipt | the card operation record member, one changed entry per batch |
| R3 | the set read returns the member prefix and the declared rows and columns with their hidden flags, and accepts complete-row selection | C4, A6, and complete scopes without enumerating cells | the table's own `NOROW`, `NOCOL` refusals; scopes enumerate cells |
| R4 | the set read reports members of another epoch as `foreign` instead of refusing `MEMBEREPOCH` | a dependency in an earlier epoch must not make every read of its dependant refuse | any such dependency refuses the whole read |
| R5 | the set read follows one named reference field one level (`deps`) within its bound | resolve and inspect in one read | two reads |
| R6 | a field-name byte bound; a set-read response bound or field projection; Go and server bounds that agree | predictable sizes | the manager's own bounds only |
| R7 | a read-only, bounded read of the receipt stream from a cursor (stream ID or table revision), returning receipts with their deltas and the next cursor, and a declared retention of the stream | notifications, and who changed what on a `STALE` refusal | `card inspect` only; `STALE` without its `BY` lines |
| R8 | the store's clock in the set read | one clock for `at`, `placed.at` and ages | the binding's clock, named in the receipt |
| R9 | refusals typed by code, with member, expected and observed as fields, not prose | the manager maps table refusals to card refusals exactly | parsing refusal text |

### 9.3 Assumptions about the sibling packages

**`internal/card/definition`** (part 1):

| # | assumption |
|---|---|
| A-D1 | `Parse(blob []byte) (Card, error)` reads the v2 profile and refuses by line and key; v3 refuses |
| A-D2 | `Pin(repo, commit, path)` reads the committed blob, not a working file, follows no symlink out of the repository, and returns an `Admission` |
| A-D3 | `Admission` holds ID, repository identity, commit, path, blob (Git object ID), digest (`sha256:` over the blob bytes), kind, dependencies (sorted, unique, at most 8, not the card itself), entry and title |
| A-D4 | `CompletionPolicy` is versioned, has a digest, and `Classify(kind)` returns PR or non-PR, refusing an unknown or unclassified kind; it is built from `internal/hygiene/kinds.txt` through its shared accessor |
| A-D5 | `ValidID` accepts ASCII letters, digits, `_` and `-`, 1 to 64 bytes |
| A-D6 | `Pin` is also a value type for the pinned identities that inspect reports |

**`internal/card/request`** (part 2):

| # | assumption |
|---|---|
| A-R1 | every shape (`Admit`, `Resolve`, `Events`, `Evidence`, `Replace`, `Inspect`, `Check`) is constructible only by a validating constructor, carries its canonical bytes and hash, and satisfies `Request` (`Kind`, `OperationID`, `Table`, `Epoch`, `ObservedRevision`, `Actor`, `Hash`, `Canonical`, `Cards`) |
| A-R2 | the canonical form sorts entries by card ID and records by evidence ID; the hash is SHA-256 over it; a hash without the operation ID is available for the default operation ID |
| A-R3 | the constructors enforce the bounds of §2.7, so a valid request always fits the table |
| A-R4 | the input types are `start`, `result`, `verdict-rework`, `verdict-accept`, `landed`, `queue-rejected`, `head`, `cancel`, `depfail`, `complete`, `external-landing`; `Destination(type, source)` implements the table of §3.3; CI is never an input |
| A-R5 | each input carries its source state, expected revision and typed payload (head, artifact, authors, result, why, because, verifier) |
| A-R6 | an evidence entry carries the card, its expected place, an optional expected revision, and at most 16 records |
| A-R7 | `Record` has the §2.4 encoding, `ID()` (16 hex of its SHA-256), and a parser; its tokens are drawn from letters, digits and `.:/@+_-` |
| A-R8 | `Receipt` and `Refusal` are the §3.0 and §6 types; `Refusal` carries code, operation, card, expected, observed, `changed` derived from its cause class (refused, transport, store error), remedy and `BY` receipts; the card receipt's canonical encoding fits the operation record's 240 KiB |
| A-R9 | `Scope` is card IDs, rows, or the whole table |
| A-R10 | `Cards()` enumerates every card the request names with its role (subject, old, new) and its expected place and revision |
| A-R11 | states, outcomes and identity grammars (readers, authors, issuers: ASCII tokens of at most 64 bytes, no comma) are its enumerations |
