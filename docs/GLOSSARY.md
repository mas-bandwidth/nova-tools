# Glossary: nova-tools 1.2.0

Every term the nova-tools specs and help use, one line each: what it means, the
section that defines it, and the words it replaced where it replaced any. The
rules of naming are [TERMINOLOGY.md](TERMINOLOGY.md); the words it retires are
held by `TestRetiredWordsAppearOnlyInRecords`. The sprint's own terms are in
[sprint/GLOSSARY.md](sprint/GLOSSARY.md), which ships with nova-sprint 1.0.0.

- **adoption** — choosing to take a tool into your workflow; a tool with no row
  is absent, never adopted. ([USAGE.md](USAGE.md#usage-and-adoption-guide) · [SPEC-UPDATE.md](SPEC-UPDATE.md))
- **ack** — the act that ends a message's pending state: a message is delivered
  until it is acked. ([SPEC-BUS.md, The semantics](SPEC-BUS.md#the-semantics))
- **beat** — a machine's heartbeat in Redis, `bench:<name>:beat`, carrying its
  measured facts. ([SPEC-CONFIG.md](SPEC-CONFIG.md) · [SPEC-UPDATE.md](SPEC-UPDATE.md))
- **bench** — a machine of the fleet that runs work or CI, named by its tailnet
  host. ([SPEC-CONFIG.md](SPEC-CONFIG.md))
- **cairn** — the record a session leaves for the next: exact words, a real clock
  stamp, a bounded index and a receipt per entry. ([SPEC-CAIRN.md](SPEC-CAIRN.md))
- **card** — the whole brief one worker is handed, wrapped in a frame that names
  its checkout, its finish and its result shape.
  ([SPEC-CARD-CONTRACT.md, The frame and JOB.md](SPEC-CARD-CONTRACT.md#2-the-frame-and-jobmd))
- **class test** — a test that reads this repository's own text and refuses a
  shape wherever it stands. ([SPEC-CI.md, The class tests](SPEC-CI.md#the-class-tests))
- **coordinator** — the friend who holds the coordinator role, the one field of
  the sprint row in `nova-config`. ([SPEC-CONFIG.md](SPEC-CONFIG.md))
- **dogfood shape** — the issue shape the family files against its own tools:
  tool, command, verbatim output, expected, smallest fix. ([SPEC-UPDATE.md](SPEC-UPDATE.md))
- **fuse** — the ingestion fuse: one command stops reading a surface, or every
  untrusted one, instantly. ([SPEC.md, nova-fuse](SPEC.md#nova-fuse--the-ingestion-fuse))
- **friend** — a named participant you exchange notes with, the coordinator
  included; her configuration is a `nova-config` friend row. ([SPEC-CONFIG.md](SPEC-CONFIG.md))
- **kind** — a type of `nova-config` row (machine, friend, sprint, tier, route
  and the rest). ([SPEC-CONFIG.md, Kinds](SPEC-CONFIG.md#kinds))
- **nova-bus** — messages between AIs over Redis streams, sent once and
  delivered until acked. ([SPEC-BUS.md](SPEC-BUS.md)) Replaces: nova-bus2 (the
  name until 2026-10-04) and the git bus, removed the same day.
- **OK** — the verdict a successful verb line carries as its second token,
  `<TOKEN> OK`, on stdout. ([SPEC.md, Conventions](SPEC.md#conventions))
- **receipt** — one line appended to `from-<me>/RECEIPTS` recording that a note
  arrived; not an approval and not proof anybody read it.
  ([SPEC.md, The receipt rule](SPEC.md#the-receipt-rule))
- **REFUSED** — the verdict a tool prints when it could not run or says NO:
  `<TOKEN> REFUSED: <reason> (<remedy>)`, on stderr. ([SPEC.md, Conventions](SPEC.md#conventions))
- **sandbox** — one command run with its filesystem reach cut down by the
  operating system. ([SPEC-SANDBOX.md](SPEC-SANDBOX.md))
- **slot** — the unit of parallelism: how many cards a machine may run at once.
  ([SPEC-CONFIG.md](SPEC-CONFIG.md))
- **STALE** — `nova-update check`'s verdict for an installed version older than
  the latest its source publishes. ([SPEC-UPDATE.md](SPEC-UPDATE.md))
- **two-minute rule** — every CI job is capped at two minutes, the target under
  one. ([SPEC-CI.md, The class tests](SPEC-CI.md#the-class-tests))
- **wall** — the kernel-enforced filesystem boundary a job runs inside, with
  separate read and write permissions. ([SPEC-SANDBOX.md](SPEC-SANDBOX.md#the-rules-numbered))
