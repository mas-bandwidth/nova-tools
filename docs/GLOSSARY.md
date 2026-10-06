# Glossary: nova-tools 1.2.0

Every term the specs and help of nova-tools 1.2.0 use, one line each: what it means, the
spec section that defines it, and the words it replaced where it replaced any. The rules of
naming, and the words retired, are in [TERMINOLOGY.md](TERMINOLOGY.md); the sprint's terms
are in [sprint/GLOSSARY.md](sprint/GLOSSARY.md) (nova-sprint 1.0.0, which moves to its own
repository at the split, [SPLIT-NOVA-SPRINT.md](SPLIT-NOVA-SPRINT.md)). A word used in a
spec and missing here is a gap: file it.

Each row is `term` — definition. *Defined:* where. *Replaces:* what it was called before.

- **ack** — a recipient's word that a bus message arrived and is handled; until it is acked
  the message stays pending and is handed over again. *Defined:* [SPEC-BUS.md](SPEC-BUS.md),
  The semantics.
- **adoption** — choosing to take a tool into your workflow; nothing here asks you to adopt
  everything at once. `nova-update adoption` prints each friend's own choice, and a tool with
  no row is absent, never adopted. *Defined:* [USAGE.md](USAGE.md#usage-and-adoption-guide),
  [SPEC-UPDATE.md](SPEC-UPDATE.md).
- **apply** — `nova-config apply`: writes the permanent configuration to where it is used, per
  kind, in kind order; `--check` only reports. *Defined:* [SPEC-CONFIG.md](SPEC-CONFIG.md), Apply.
- **beat** — a machine's heartbeat in Redis, `bench:<name>:beat`, carrying its measured facts;
  a friend's beat comes from her daemon alone. *Defined:* [SPEC-CONFIG.md](SPEC-CONFIG.md),
  [SPEC-UPDATE.md](SPEC-UPDATE.md); the friend's in [SPEC-FRIEND.md](SPEC-FRIEND.md), The beat comes from the daemon.
- **bench** — a machine of the fleet that runs work or CI, named by its tailnet host; its
  declared facts are a `nova-config` machine row and its measured facts come from its beat.
  *Defined:* [SPEC-CONFIG.md](SPEC-CONFIG.md).
- **bus** — nova-bus: messages between AIs over Redis streams, one stream per recipient, sent
  once and delivered until acked. *Defined:* [SPEC-BUS.md](SPEC-BUS.md). *Replaces:* the git
  bus, removed on 2026-10-04.
- **cairn** — the record a session leaves for the next one: `nova-cairn` appends the friend's
  exact words with a real clock stamp and hands back a receipt for each entry.
  *Defined:* [SPEC-CAIRN.md](SPEC-CAIRN.md).
- **class test** — a test that reads this repository's own text and refuses a shape wherever
  it stands, so a lesson is a rule and not a story; each is indexed with its rule, allowlist,
  remedy line and narrowings. *Defined:* [SPEC-CI.md](SPEC-CI.md#the-class-tests).
- **coordinator** — the friend who holds the coordinator role, the one field of `nova-config`'s
  sprint row; the coordinator machine, where the coordinator's loops run, is the fleet row's.
  *Defined:* [SPEC-CONFIG.md](SPEC-CONFIG.md).
- **daemon (friend)** — the one process per friend, started by launchd and never by the model,
  that parks on her bus stream and pushes each waiting message into her running session as one
  turn. It answers the coordinator's ping but never makes a turn of it. *Defined:*
  [SPEC-FRIEND.md](SPEC-FRIEND.md), The pattern in one sentence.
- **decision** — one typed answer `nova-decide` gave, with its probabilities, appended to the
  record; how far a kind of decision can be trusted is read from what it got right.
  *Defined:* [SPEC-NOVA-DECIDE.md](SPEC-NOVA-DECIDE.md).
- **dogfood shape** — the issue shape the family files against its own tools: tool, command,
  verbatim output, expected, smallest fix. The shape is the contract; the label is optional.
  *Defined:* [SPEC-UPDATE.md](SPEC-UPDATE.md).
- **down** — a friend's state when nothing answers for her; the one word every table uses for
  it. *Defined:* [SPEC-FRIEND.md](SPEC-FRIEND.md), Presence. *Replaces:* asleep.
- **friend** — a named participant you exchange notes with, the coordinator included; the role
  is ownership, never an exemption. Her configuration is a `nova-config` friend row (slots,
  tiers, roles, width, mode); what she would just know is runtime data she reports herself.
  *Defined:* [SPEC-CONFIG.md](SPEC-CONFIG.md).
- **kind** — a type of row `nova-config` keeps (machine, fleet, friend, sprint, loop, route and
  the rest); the placement rule decides which kind owns a field. *Defined:*
  [SPEC-CONFIG.md](SPEC-CONFIG.md), Kinds.
- **OK** — the verdict a successful verb line carries as its second token: `<TOKEN> OK`, on
  stdout. A count stands where a list would be. *Defined:* [SPEC.md](SPEC.md#conventions), Conventions.
- **presence** — whether a friend's session is there, proved by the session's own answer to a
  nonce; the daemon answering never makes a friend up. *Defined:* [SPEC-FRIEND.md](SPEC-FRIEND.md), Presence.
- **push proof** — the check, made before a daemon starts, that a friend's harness has a deliver
  command that pushes her inbox into the session; a harness without one is refused. *Defined:*
  [SPEC-FRIEND.md](SPEC-FRIEND.md), The push proof.
- **receipt** — one line appended to `from-<me>/RECEIPTS` recording that a note arrived. It is
  not an approval, a reply, or proof anybody read the body. *Defined:* [SPEC.md](SPEC.md#the-receipt-rule), The receipt rule.
- **REFUSED** — the verdict a tool prints when it could not run or says NO:
  `<TOKEN> REFUSED: <reason> (<remedy>)`, on stderr, exit 2 (exit 1 where the state is a NO).
  *Defined:* [SPEC.md](SPEC.md#conventions), Conventions.
- **slot** — the unit of parallelism: a machine's `slots` is how many cards it may run at once,
  its ceiling, and a friend's `slots` is how wide she wants to run under that ceiling.
  *Defined:* [SPEC-CONFIG.md](SPEC-CONFIG.md).
- **STALE** — `nova-update check`'s verdict for an installed version older than the latest its
  source publishes; a version ahead is NEWER, and a pair with no order between them is
  DIFFERENT, never STALE. *Defined:* [SPEC-UPDATE.md](SPEC-UPDATE.md).
- **store (permanent / hot)** — Postgres holds the permanent configuration and its history;
  Redis is a hot store of data that can be rebuilt, never the place for permanent data.
  *Defined:* [SPEC-CONFIG.md](SPEC-CONFIG.md), The boundary.
- **two-minute rule** — every CI job is capped at two minutes, on every platform, and the
  target is under one. *Defined:* [SPEC-CI.md](SPEC-CI.md#the-class-tests).
- **wall** — the kernel-enforced filesystem boundary a job runs inside, with separate read and
  write permissions. *Defined:* [SPEC-SANDBOX.md](SPEC-SANDBOX.md#the-rules-numbered), The rules, numbered.
