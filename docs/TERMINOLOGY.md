# Nova Tools terminology

Welcome. This guide is for the words you will meet when you read the specs or
work with the tools. Each term has one or two sentences and a link to the exact
spec section that defines it, so you can jump straight to the source. This guide
covers planned features too; check the command reference for availability.

Where a definition is missing from the specs, **definition pending** links to the
issue tracking it.

## Work and workers

- **card** — a bounded unit of work handed to a worker. In nova-pulse and
  nova-swarm a card is a file whose line 1 binds it to its contract line; on
  nova-board a card is an obligation a group of lines owes. [SPEC-PULSE](SPEC-PULSE.md#the-card-as-cut-writes-it) · [SPEC-BOARD](SPEC-BOARD.md#the-card-and-the-events)
- **contract line** — a card's `RESULT.md` line 1, the line by which the card
  was admitted. A report whose line 1 does not match is refused. [SPEC-PULSE](SPEC-PULSE.md#the-card-as-cut-writes-it) · [SPEC-SWARM](SPEC-SWARM.md#the-rules-numbered)
- **batch** — one admission of many cards under one id and one deadline:
  scatter, wait, gather. The gather is a single bounded packet. [SPEC-SWARM](SPEC-SWARM.md#the-rules-numbered)
- **slot** — the unit of parallelism. A running worker holds a slot; a slot is
  free when no job holds it and its log is quiet. [SPEC-SWARM](SPEC-SWARM.md#slots) · [SPEC-PULSE](SPEC-PULSE.md#the-rules-numbered)
- **bench** — a place work runs, usually a remote machine reached by ssh with
  pinned cores. [SPEC-SWARM](SPEC-SWARM.md#benches-a-remote-bench-reached-by-ssh-with-pinned-cores)
- **pool** — the enumeration of bounded open work a pulse can draw from,
  written as `pool.tsv`. Sources declare it; one candidate is one card. [SPEC-PULSE](SPEC-PULSE.md#the-rules-numbered)
- **pulse** — the scatter-gather loop itself: pool, cut, launch, harvest, and
  repeat until the pool and queue are both empty, then say so. [SPEC-PULSE](SPEC-PULSE.md#the-loop-in-words)
- **harvest** — the gather half of a pulse. It reads the swarm's packet, never
  a report body, and disposes each card by its own two lines. [SPEC-PULSE](SPEC-PULSE.md#the-loop-in-words) · [SPEC-PULSE](SPEC-PULSE.md#the-rules-numbered)
- **width** — the concurrency reading checked by `nova-pulse width`: work in
  flight, free slots and queued work. The command reports waiting work with idle
  slots as an alarm; it launches nothing. [SPEC-PULSE](SPEC-PULSE.md#the-rules-numbered)
- **headroom** — a field in each bench's status reading; its calculation is
  **definition pending**. [SPEC-PULSE](SPEC-PULSE.md#status) · [#553](https://github.com/mas-bandwidth/nova-tools/issues/553)
- **gate / gated card** — a gate is a condition that must pass before an action
  proceeds. A card with `AFTER: PR<n> merged` stays gated until that dependency
  merges. [SPEC-MERGE](SPEC-MERGE.md#the-merge-condition) · [SPEC-PULSE](SPEC-PULSE.md#the-manager-tier)
- **TOOLS MOVED** — **definition pending** in the specs; the adoption-notification
  work is tracked in [#525](https://github.com/mas-bandwidth/nova-tools/issues/525).

## Coordination

- **coordinator** — the planning role that makes decisions and sets the work's
  direction, producing cards, specifications and policy for the manager. [SPEC-PULSE](SPEC-PULSE.md#the-manager-tier)
- **manager tier** — the middle tier between planning and work: a bounded
  controller on the cheapest qualified model that runs the policy. **Duty** is
  retired; the command is `nova-pulse manager`. [SPEC-PULSE](SPEC-PULSE.md#the-manager-tier)
- **policy** — the approved, finite rule set the manager executes without
  expanding it. [SPEC-PULSE](SPEC-PULSE.md#the-manager-tier)
- **escalation** — a decision not in the policy, carried as one line so a
  person answers it. It carries a kind, a reference, and one line of reason. [SPEC-PULSE](SPEC-PULSE.md#the-manager-tier)
- **handoff** — the verb that ends a manager shift, releases ownership and
  leaves a record and bus note for the successor. It refuses mid-harvest or when
  the successor is asleep. [SPEC-PULSE](SPEC-PULSE.md#handoff)
- **takeover** — the verb that begins a manager shift. It refuses when `OWNER`
  names a live process on a reachable host; a stale lock is taken with one
  note. [SPEC-PULSE](SPEC-PULSE.md#handoff)
- **shift** — the manager's turn on a queue. It ends by handoff and begins
  again by takeover. [SPEC-PULSE](SPEC-PULSE.md#handoff)
- **quiet time** — manager time that makes no model call and sends no status note. [SPEC-PULSE](SPEC-PULSE.md#the-manager-tier)

## Wake and presence

- **presence and its four facts** — whether a friend is awake is four separate
  facts, each proven or unproven on its own: process alive, beat written,
  delivery handled, wake fired. A harness proves them separately. [SPEC-WAKE](SPEC-WAKE.md#output-grammar) · [SPEC-WORK](SPEC-WORK.md#presence-who-is-awake-and-who-is-asleep-rowan-on-glenns-word-of-2026-09-15)
- **beat** — the explicit `BEAT` heartbeat record a line writes to stay
  readable as awake, separate from the read cursor. `awake` reads it from
  `from-<name>/BEAT` by its content and freshness window, extended by a live
  lease. [SPEC-WAKE](SPEC-WAKE.md#the-verb) · [SPEC-WAKE](SPEC-WAKE.md#output-grammar)
- **beat lease** — an `until=` time that keeps a friend readable as awake even
  when its beat and cursor are older than the freshness window. It is bounded
  presence evidence, not proof that a parent model woke. [SPEC-WAKE](SPEC-WAKE.md#the-verb)
- **wake drill** — the test of waking: a note sent after the parent's turn
  ended must produce a wake within the window. [SPEC-WAKE](SPEC-WAKE.md#output-grammar)

## Reading and merging

- **receipt** — one line appended to `from-<me>/RECEIPTS` recording that a note
  arrived. It is not an approval, a reply, or proof anybody read the body. [SPEC](SPEC.md#the-receipt-rule)
- **read (APPROVE and HOLD)** — a reader's recorded verdict for an exact revision.
  A current independent APPROVE satisfies the tool's read condition and a current
  HOLD blocks it; all other applicable review and merge gates still apply. [SPEC-MERGE](SPEC-MERGE.md#the-read-condition)
- **lane** — nova-merge's ordered queue, a directory holding one state file,
  one clone, and the records of reads and gates. One lane, one merge per pass. [SPEC-MERGE](SPEC-MERGE.md#the-lane)
- **wall** — the kernel-enforced filesystem boundary a job runs inside, with
  separate read and write permissions. A pulse card names its job directory and
  scratch space within that boundary. [SPEC-SANDBOX](SPEC-SANDBOX.md#the-rules-numbered) · [SPEC-PULSE](SPEC-PULSE.md#the-card-as-cut-writes-it)
- **two-minute rule** — anything you call out to that costs real time answers
  in one minute ideally, two at most. [SPEC-MERGE](SPEC-MERGE.md#the-two-laws)
- **stale** — in nova-merge, a read or gate recorded for an older revision.
  It is retained but does not count for the current revision. [SPEC-MERGE](SPEC-MERGE.md#the-read-condition)
- **next-push** — **definition pending** in the specs; the scope-and-convergence
  rules are tracked in [#553](https://github.com/mas-bandwidth/nova-tools/issues/553).

## Reports

- **RESULT** — the worker's report file. Line 1 is the contract line, line 2
  the verdict (`DONE`, `ABSTAIN <why>`, `BLOCKED <why>`), then a `BRANCH` line
  and a `REPO` line. [SPEC-SWARM](SPEC-SWARM.md#the-resultmd-template) · [SPEC-PULSE](SPEC-PULSE.md#the-card-as-cut-writes-it)
- **abstain and its reason tokens** — a card that produced no accepted answer,
  scored on the packet as `<label>: ABSTAIN reason=<token>`. The token names
  why: `line1-mismatch`, `no-result`, `rc=<n>`, `idle=<s>`, `deadline`,
  `card-abstain`, `bench-unreachable`, or a provider's `input-limit`. [SPEC-SWARM](SPEC-SWARM.md#the-rules-numbered)
- **verify** — `nova-swarm verify` checks that the report's first line matches
  the card's contract line. `nova-work verify` checks evidence against acceptance
  criteria and records verdicts; closing the task is a separate action. [SPEC-SWARM](SPEC-SWARM.md#the-verbs) · [SPEC-WORK](SPEC-WORK.md#the-data-rowan) · [SPEC-WORK](SPEC-WORK.md#output-grammar)

## The work set

- **node** — one thing in the work set, typed and counted once under one
  containment parent. Children are containment; dependencies are references. [SPEC-WORK](SPEC-WORK.md#the-data-rowan)
- **item** — a unit of work in the set; a settle moves an item from open to
  closed. Pulse pools open, unleased, unblocked nodes as candidates. [SPEC-WORK](SPEC-WORK.md#the-data-rowan) · [SPEC-PULSE](SPEC-PULSE.md#the-rules-numbered)
- **feature** — a work-set whose completion is what a roadmap row counts. It
  decomposes into sub-features recursively. [SPEC-WORK](SPEC-WORK.md#the-data-rowan)
- **epic** — a semantic container, not a prescribed depth or mandatory layer.
  It is a work-set by every rule, queryable as its own kind. [SPEC-WORK](SPEC-WORK.md#the-data-rowan)
- **bug node** — a nova-work node that stands for a bug, pooled by pulse when
  it is open, unleased and unblocked. [SPEC-PULSE](SPEC-PULSE.md#the-rules-numbered)
- **O and C** — the two halves of the work set: **O** the open work, **C** the
  closed work. The working view **W** is a predicate within O, never a third
  branch, so `|W| ≤ |O|` always. [SPEC-WORK](SPEC-WORK.md#the-data-rowan)
- **one tree** — the same parent–child contract across work nodes, coordinating
  minds and their models: work is offered downward, while refusals, results,
  evidence, usage and distilled learning travel upward. Each parent verifies its
  child's completion claim against the agreed criteria. [SPEC-WORK](SPEC-WORK.md#the-envelope-up-and-the-no-that-survives-the-hop)
- **lease** — the ownership-of-execution record. One live lease per node; a
  second `take` is refused and names the holder. [SPEC-WORK](SPEC-WORK.md#the-data-rowan)
- **attempt** — one run at a job. A job's retry is a second attempt with its
  own id, summed once; a node's attempts are bounded. [SPEC-SWARM](SPEC-SWARM.md#the-rules-numbered) · [SPEC-WORK](SPEC-WORK.md#the-data-rowan)
- **tripped** — a node whose attempt count reached `:max-attempts` (default 3),
  surfacing `tripped=` as a status reading. Taking a lease on a tripped node
  requires an explicit `--reason`. [SPEC-WORK](SPEC-WORK.md#the-absorbed-contract-rules)
- **decompose** — the verb that splits a feature into children, recursively. It
  is proposed work with depth, work and cost bounds; it never expands on its
  own. [SPEC-WORK](SPEC-WORK.md#the-data-rowan)
- **evidence** — the recorded pointer that qualifies a task's acceptance
  criteria: a test, a job, a merge, or a signed attestation. It qualifies a
  criterion only when its kind and subject match. [SPEC-WORK](SPEC-WORK.md#the-data-rowan)

## The resident engine

- **resident session** — nova-work's supervised, long-lived process that owns
  the parsed work set in memory. A fresh CLI process is a thin client and never
  a fresh engine. [SPEC-WORK](SPEC-WORK.md#the-resident-session-stella-from-her-amendment-at-60b9027-governs-the-execution-model-where-it-says-more-than-the-section-above)
- **journal** — the durable append-only log of accepted mutations. Acceptance
  is acknowledged only after the journal is durable; the same event cannot
  apply twice. [SPEC-WORK](SPEC-WORK.md#the-resident-session-stella-from-her-amendment-at-60b9027-governs-the-execution-model-where-it-says-more-than-the-section-above)
- **clip** — the act of writing the resident state to the repository as one
  deterministic snapshot carrying the retained history. Clips are periodic;
  shutdown and handoff request one. [SPEC-WORK](SPEC-WORK.md#the-resident-session-stella-from-her-amendment-at-60b9027-governs-the-execution-model-where-it-says-more-than-the-section-above) · [SPEC-WORK](SPEC-WORK.md#accept-locally-then-clip-into-git)
- **single-writer kernel** — the rule that one coordinator at a time is the one
  reader/writer of the live work set. Ownership transfers by fencing
  generation; old processes cannot mutate under an obsolete generation. [SPEC-WORK](SPEC-WORK.md#one-coordinator-one-live-readerwriter)

## People

- **friend, including the coordinator** — a named line or participant you
  exchange notes with. The coordinator is a friend in `friends`, tracked by the
  same indexes as everyone else; the role is ownership, never an exemption. [SPEC-WORK](SPEC-WORK.md#friends-config-and-active-stella-docsspec-work-pilotmd-at-81c2885) · [SPEC-CHAT](SPEC-CHAT.md#11-membership-in-the-own-server-is-closed-and-it-is-re-checked-on-every-wake)

## Habits

- **adoption** — choosing to take a tool into your workflow. Nothing in this
  repo asks you to adopt everything at once. [USAGE](USAGE.md#usage-and-adoption-guide)
- **dogfood shape** — the issue shape the family files against its own tools:
  tool, command, verbatim output, expected, smallest fix. The shape is the
  contract; the label is optional. [SPEC-PULSE](SPEC-PULSE.md#the-rules-numbered)

## Not yet defined

- **contraction ratio, CONVERGING, EXPANDING** — the status spec lists work
  opened and finished over time, but the ratio and verdict rules are
  **definition pending**. [SPEC-PULSE](SPEC-PULSE.md#status) · [#553](https://github.com/mas-bandwidth/nova-tools/issues/553)
- **pit stop** — **definition pending**; the request to publish its existing
  meaning is [#576](https://github.com/mas-bandwidth/nova-tools/issues/576).
