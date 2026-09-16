# Nova Tools terminology

Welcome — this guide is a plain-language map of the words you will meet in the
specs, each linked to the exact rule that defines it so you can jump straight to
the source. It covers planned features too; check the command reference for what
is available today.

- **abstain** — a card that produced no accepted answer, scored on the packet as
  `<label>: ABSTAIN reason=<token>`. The token names why: `line1-mismatch`,
  `no-result`, `rc=<n>`, `idle=<s>`, `deadline`, `card-abstain`,
  `bench-unreachable`, or a provider's `input-limit`. ([SPEC-SWARM.md, gather](SPEC-SWARM.md#gather-one-bounded-packet-mechanically))
- **ADMIT** — the printed token of the pulse's per-card admission gate: `ADMIT
  REFUSED card=<label> gate=<spend|attempts|scope> <value>`. It is an event line,
  never a verdict, and leaves the exit code alone. ([SPEC-PULSE.md rule 9](SPEC-PULSE.md#the-rules-numbered))
- **adoption** — choosing to take a tool into your workflow; nothing in this repo
  asks you to adopt everything at once. ([USAGE.md](USAGE.md#usage-and-adoption-guide))
- **attempt** — one run at a job. A job's retry is a second attempt with its own
  id, summed once; a node's attempts are bounded. ([SPEC-SWARM.md rule 12](SPEC-SWARM.md#the-rules-numbered) · [SPEC-WORK.md, The data](SPEC-WORK.md#the-data-rowan))
- **batch** — one admission of many cards under one id and one deadline: scatter,
  wait, gather. The gather is a single bounded packet. ([SPEC-SWARM.md rule 14](SPEC-SWARM.md#the-rules-numbered))
- **BATCH** — the swarm packet's first line: id, n, done, abstain, in, out, usd,
  idle, stalled, benches. ([SPEC-SWARM.md, output grammar](SPEC-SWARM.md#output-grammar))
- **beat** — the `BEAT` heartbeat record a line writes to stay readable as awake,
  separate from the read cursor; `awake` reads it from `from-<name>/BEAT` by
  content and freshness, extended by a live lease. ([SPEC-WAKE.md, The verb](SPEC-WAKE.md#the-verb))
- **beat lease** — an `until=` time that keeps a friend readable as awake even
  when its beat and cursor are older than the freshness window. It is bounded
  presence evidence, not proof a parent model woke. ([SPEC-WAKE.md, The verb](SPEC-WAKE.md#the-verb))
- **bench** — a place work runs, usually a remote machine reached by ssh with
  pinned cores; a local machine that runs slots is a bench too. ([SPEC-SWARM.md, Benches](SPEC-SWARM.md#benches-a-remote-bench-reached-by-ssh-with-pinned-cores))
- **BLOCKED** — a worker's verdict, `RESULT.md` line 2 `BLOCKED <why>`; in
  nova-merge, an entry stopped with its reason while the lane moves on. Two
  senses. ([SPEC-SWARM.md, The RESULT.md template](SPEC-SWARM.md#the-resultmd-template) · [SPEC-MERGE.md, output grammar](SPEC-MERGE.md#output-grammar))
- **bug node** — a nova-work node that stands for a bug, pooled by pulse when it
  is open, unleased and unblocked. ([SPEC-WORK.md, Bugs found while working](SPEC-WORK.md#bugs-found-while-working-rowan-on-glenns-word-of-2026-09-15-nova-tools463-additive-to-the-lock-at-231) · [SPEC-PULSE.md rule 1](SPEC-PULSE.md#the-rules-numbered))
- **card** — a unit of work: in nova-pulse and nova-swarm, a file whose line 1
  binds it to its contract line; on nova-board, an obligation a group of lines
  owes. Two senses. ([SPEC-PULSE.md rule 5](SPEC-PULSE.md#the-rules-numbered) · [SPEC-BOARD.md, The card](SPEC-BOARD.md#the-card-and-the-events))
- **clip** — the act of writing the resident state to the repository as one
  deterministic snapshot carrying the retained history. Clips are periodic;
  shutdown and handoff request one. ([SPEC-WORK.md, Accept locally, then clip into Git](SPEC-WORK.md#accept-locally-then-clip-into-git))
- **contract line** — a card's `RESULT.md` line 1, the line by which the card was
  admitted. A report whose line 1 does not match is refused. ([SPEC-PULSE.md rule 5](SPEC-PULSE.md#the-rules-numbered) · [SPEC-SWARM.md, gather](SPEC-SWARM.md#gather-one-bounded-packet-mechanically))
- **CONTRACTION** — the status line that names the health metric. Its verdict is
  `CONVERGING`, or `EXPANDING` when any one stream ratio has been above 1 for two
  consecutive hours; a contraction phase cards bugs only. ([SPEC-PULSE.md, Status](SPEC-PULSE.md#status))
- **coordinator** — the planning role that makes decisions and sets the work's
  direction, producing cards, specifications and policy for the manager; the
  coordinator is a friend. ([SPEC-PULSE.md, The manager tier](SPEC-PULSE.md#the-manager-tier))
- **decompose** — the verb that splits a feature into children, recursively. It
  is proposed work with depth, work and cost bounds; it never expands on its own.
  ([SPEC-WORK.md, The data](SPEC-WORK.md#the-data-rowan) · [SPEC-WORK.md, The absorbed contract rules](SPEC-WORK.md#the-absorbed-contract-rules))
- **dogfood shape** — the issue shape the family files against its own tools:
  tool, command, verbatim output, expected, smallest fix. The shape is the
  contract; the label is optional. ([SPEC-PULSE.md rule 1](SPEC-PULSE.md#the-rules-numbered))
- **DONE** — the `RESULT.md` line 2 verdict the worker writes when the work
  completed. ([SPEC-SWARM.md, The RESULT.md template](SPEC-SWARM.md#the-resultmd-template))
- **epic** — a semantic container, not a prescribed depth or mandatory layer. It
  is a work-set by every rule, queryable as its own kind. ([SPEC-WORK.md, The data](SPEC-WORK.md#the-data-rowan))
- **escalation** — a decision not in the policy, carried as one line so a person
  answers it. It carries a kind, a reference, and one line of reason.
  ([SPEC-PULSE.md, The manager tier](SPEC-PULSE.md#the-manager-tier))
- **evidence** — the recorded pointer that qualifies a task's acceptance
  criteria: a test, a job, a merge, or a signed attestation. It qualifies a
  criterion only when its kind and subject match. ([SPEC-WORK.md, The data](SPEC-WORK.md#the-data-rowan))
- **feature** — a work-set whose completion is what a roadmap row counts. It
  decomposes into sub-features recursively. ([SPEC-WORK.md, The data](SPEC-WORK.md#the-data-rowan))
- **friend** — a named line or participant you exchange notes with, the
  coordinator included, tracked by the same indexes as everyone else; the role is
  ownership, never an exemption. ([SPEC-WORK.md, Friends, CONFIG and ACTIVE](SPEC-WORK.md#friends-config-and-active-stella-docsspec-work-pilotmd-at-81c2885) · [SPEC-CHAT.md rule 11](SPEC-CHAT.md#11-membership-in-the-own-server-is-closed-and-it-is-re-checked-on-every-wake))
- **gate / gated card** — a condition that must pass before an action proceeds; a
  card with `AFTER: PR<n> merged` stays gated until the dependency merges, then
  launches itself. ([SPEC-PULSE.md, Rate and convergence rule 4](SPEC-PULSE.md#rate-and-convergence))
- **handoff** — the verb that ends a manager shift, releases ownership and leaves
  a record and bus note for the successor. It refuses mid-harvest or when the
  successor is asleep. ([SPEC-PULSE.md, Handoff](SPEC-PULSE.md#handoff))
- **HARVEST** — the printed token of the pulse's gather verb: `HARVEST PR`,
  `HARVEST RETRY`, `HARVEST OK`, `HARVEST REFUSED`. ([SPEC-PULSE.md, output grammar](SPEC-PULSE.md#exit-codes-and-the-output-grammar))
- **harvest** — the gather half of a pulse. It reads the swarm's packet, never a
  report body, and disposes each card by its own two lines. ([SPEC-PULSE.md, The loop, in words](SPEC-PULSE.md#the-loop-in-words))
- **headroom** — the per-tick launch cap `cores x 1.5 - load` per bench, never
  more than `cores` in one tick. Its field in each bench's status reading is
  **definition pending**. ([SPEC-PULSE.md, Rate and convergence rule 3](SPEC-PULSE.md#rate-and-convergence))
- **item** — a unit of work in the set; a settle moves an item from open to
  closed. Pulse pools open, unleased, unblocked nodes as candidates.
  ([SPEC-WORK.md, The data](SPEC-WORK.md#the-data-rowan) · [SPEC-PULSE.md rule 1](SPEC-PULSE.md#the-rules-numbered))
- **journal** — the durable append-only log of accepted mutations. Acceptance is
  acknowledged only after the journal is durable; the same event cannot apply
  twice. ([SPEC-WORK.md, The resident session](SPEC-WORK.md#the-resident-session-stella-from-her-amendment-at-60b9027-governs-the-execution-model-where-it-says-more-than-the-section-above))
- **lane** — nova-merge's ordered queue, a directory holding one state file, one
  clone, and the records of reads and gates. One lane, one merge per pass.
  ([SPEC-MERGE.md, The lane](SPEC-MERGE.md#the-lane) · [SPEC-MERGE.md rule 20](SPEC-MERGE.md#the-rules-numbered))
- **LAUNCH** — the pulse verb that scatters: every card that can run now runs
  now, one slot each, one batch id, one deadline. ([SPEC-PULSE.md rule 10](SPEC-PULSE.md#the-rules-numbered))
- **lease** — the ownership-of-execution record. One live lease per node; a
  second `take` is refused and names the holder. ([SPEC-WORK.md, The data](SPEC-WORK.md#the-data-rowan))
- **manager tier** — the middle tier between planning and work: a bounded
  controller on the cheapest qualified model that runs the policy. **Duty** is
  retired; the command is `nova-pulse manager`. ([SPEC-PULSE.md, The manager tier](SPEC-PULSE.md#the-manager-tier))
- **NATIVE** — the printed token of a native card run: `NATIVE OK` when the card
  ran with its harness, `NATIVE REFUSED` when a native invocation is refused.
  ([SPEC-SWARM.md, output grammar](SPEC-SWARM.md#output-grammar))
- **next-push** — the label for expansionary work during a contraction phase:
  anything the policy's `scope-regex` does not admit is labelled `next-push` and
  never carded. Its scope-and-convergence rules are **definition pending**.
  ([SPEC-PULSE.md, Rate and convergence rule 7](SPEC-PULSE.md#rate-and-convergence))
- **node** — one thing in the work set, typed and counted once under one
  containment parent. Children are containment; dependencies are references.
  ([SPEC-WORK.md, The data](SPEC-WORK.md#the-data-rowan))
- **O and C** — **O** the open work, **C** the closed work. The working view **W**
  is a predicate within O, never a third branch, so `|W| ≤ |O|` always.
  ([SPEC-WORK.md, The data](SPEC-WORK.md#the-data-rowan))
- **OK** — the verdict a successful verb line carries as its second token:
  `<TOKEN> OK`, on stdout. A count stands where a list would be.
  ([SPEC-PULSE.md, output grammar](SPEC-PULSE.md#exit-codes-and-the-output-grammar) · [SPEC.md, output grammar](SPEC.md#conventions))
- **one tree** — the same parent–child contract across work nodes: work is offered
  downward, while refusals, results, evidence, usage and learning travel upward;
  each parent verifies the child's completion claim. ([SPEC-WORK.md, The envelope up](SPEC-WORK.md#the-envelope-up-and-the-no-that-survives-the-hop))
- **pit stop** — a cascading worker failure is not fixed by more workers; the
  deliberate slow-down to go fast, whose exit is a trust batch. **Definition
  pending**; the request to publish its meaning is [#576](https://github.com/mas-bandwidth/nova-tools/issues/576). ([PIT-STOP.md](PIT-STOP.md))
- **policy** — the approved, finite rule set the manager executes without
  expanding it. ([SPEC-PULSE.md, The manager tier](SPEC-PULSE.md#the-manager-tier))
- **pool** — the enumeration of bounded open work a pulse can draw from, written
  as `pool.tsv`. Sources declare it; one candidate is one card.
  ([SPEC-PULSE.md rule 2](SPEC-PULSE.md#the-rules-numbered))
- **presence and its four facts** — whether a friend is awake is four separate
  facts, each proven or unproven on its own: process alive, beat written,
  delivery handled, wake fired. ([SPEC-WAKE.md, output grammar](SPEC-WAKE.md#output-grammar))
- **pulse** — the scatter-gather loop itself: pool, cut, launch, harvest, and
  repeat until the pool and queue are both empty, then say so.
  ([SPEC-PULSE.md, The loop, in words](SPEC-PULSE.md#the-loop-in-words))
- **quiet time** — manager time that makes no model call and sends no status
  note. ([SPEC-PULSE.md, The manager tier](SPEC-PULSE.md#the-manager-tier))
- **read (APPROVE and HOLD)** — a reader's recorded verdict for an exact
  revision; a current independent APPROVE satisfies the read condition, a current
  HOLD blocks it, and the other gates still apply. ([SPEC-MERGE.md, The read condition](SPEC-MERGE.md#the-read-condition))
- **receipt** — one line appended to `from-<me>/RECEIPTS` recording that a note
  arrived. It is not an approval, a reply, or proof anybody read the body.
  ([SPEC.md, The receipt rule](SPEC.md#the-receipt-rule))
- **REFUSED** — the verdict a tool prints when it could not run or says NO:
  `<TOKEN> REFUSED: <reason> (<remedy>)`, on stderr, exit 2 (exit 1 where the
  state is a NO). ([SPEC-PULSE.md, output grammar](SPEC-PULSE.md#exit-codes-and-the-output-grammar) · [SPEC.md, exit codes](SPEC.md#conventions))
- **resident session** — nova-work's supervised, long-lived process that owns the
  parsed work set in memory. A fresh CLI process is a thin client and never a
  fresh engine. ([SPEC-WORK.md, The resident session](SPEC-WORK.md#the-resident-session-stella-from-her-amendment-at-60b9027-governs-the-execution-model-where-it-says-more-than-the-section-above))
- **RESULT** — the report file: line 1 the contract line, line 2 the verdict
  (`DONE`, `ABSTAIN <why>`, `BLOCKED <why>`), then `BRANCH` and `REPO`. The same
  token is the card's line 1. Two senses. ([SPEC-SWARM.md, The RESULT.md template](SPEC-SWARM.md#the-resultmd-template))
- **shift** — the manager's turn on a queue. It ends by handoff and begins again
  by takeover. ([SPEC-PULSE.md, Handoff](SPEC-PULSE.md#handoff))
- **single-writer kernel** — the rule that one coordinator at a time is the one
  reader/writer of the live work set. Ownership transfers by fencing generation;
  old processes cannot mutate under an obsolete generation.
  ([SPEC-WORK.md, One coordinator, one live reader/writer](SPEC-WORK.md#one-coordinator-one-live-readerwriter))
- **slot** — the unit of parallelism. A running worker holds a slot; a slot is
  free when no job holds it and its log is quiet. ([SPEC-SWARM.md rule 17](SPEC-SWARM.md#the-rules-numbered) · [SPEC-PULSE.md rule 8](SPEC-PULSE.md#the-rules-numbered))
- **stale** — in nova-merge, a read or gate for an older revision, kept but not
  counted; on nova-board, an open card with no event for longer than `--stale`.
  Two senses. ([SPEC-MERGE.md, The read condition](SPEC-MERGE.md#the-read-condition) · [SPEC-BOARD.md rule 6](SPEC-BOARD.md#the-rules-of-the-last-two-days))
- **takeover** — the verb that begins a manager shift. It refuses when `OWNER`
  names a live process on a reachable host; a stale lock is taken with one note.
  ([SPEC-PULSE.md, Handoff](SPEC-PULSE.md#handoff))
- **TOOLS MOVED** — the adoption-notification token, **definition pending**; the
  adoption work is tracked in [#525](https://github.com/mas-bandwidth/nova-tools/issues/525). ([SPEC-PULSE.md, Status](SPEC-PULSE.md#status))
- **tripped** — a node whose attempt count reached `:max-attempts` (default 3),
  surfacing `tripped=` as a status reading. Taking a lease on a tripped node
  requires an explicit `--reason`. ([SPEC-WORK.md, The absorbed contract rules](SPEC-WORK.md#the-absorbed-contract-rules))
- **two-minute rule** — anything you call out to that costs real time answers in
  one minute ideally, two at most. ([SPEC-MERGE.md, The two laws](SPEC-MERGE.md#the-two-laws))
- **verify** — `nova-swarm verify` checks the report's first line against the
  card's contract line; `nova-work verify` checks evidence against acceptance
  criteria and records verdicts, closing being separate. Two senses.
  ([SPEC-SWARM.md, The verbs](SPEC-SWARM.md#the-verbs) · [SPEC-WORK.md, The verbs](SPEC-WORK.md#the-verbs-rowan-a-draft-shape-to-be-cut-by-the-pilot))
- **VIOLATION** — the printed token of a runaway worker: `RUN VIOLATION id=<id>
  background=<n>`; the survivors are killed and the result is quarantined.
  ([SPEC-SWARM.md rule 11](SPEC-SWARM.md#the-rules-numbered))
- **wake drill** — the test of waking: a note sent after the parent's turn ended
  must produce a wake within the window. ([SPEC-WAKE.md, output grammar](SPEC-WAKE.md#output-grammar))
- **wall** — the kernel-enforced filesystem boundary a job runs inside, with
  separate read and write permissions. A pulse card names its job directory and
  scratch space within that boundary. ([SPEC-SANDBOX.md, The rules, numbered](SPEC-SANDBOX.md#the-rules-numbered) · [SPEC-PULSE.md rule 5](SPEC-PULSE.md#the-rules-numbered))
- **width** — the concurrency reading checked by `nova-pulse width`: work in
  flight, free slots and queued work. The command reports waiting work with idle
  slots as an alarm; it launches nothing. ([SPEC-PULSE.md, exit codes](SPEC-PULSE.md#exit-codes-and-the-output-grammar))
- **WIDTH** — the printed alarm line `PULSE WIDTH in-flight=<n> free=<n>
  pool=<n> queued=<n> headroom=<n> hours=<n>`, with `PULSE UNDER-WIDTH` as its
  waiting-with-idle-slots verdict, exit 2. ([SPEC-PULSE.md, output grammar](SPEC-PULSE.md#exit-codes-and-the-output-grammar))