# Glossary: nova-tools 1.2.0

The words the nova-tools specs and help use, one line each: what it means, the
section that defines it, and the words it replaced where it replaced any. The
sprint's own words are in [sprint/GLOSSARY.md](https://github.com/mas-bandwidth/nova-sprint/blob/main/docs/sprint/GLOSSARY.md) (nova-sprint
1.0.0). The rules of naming, and the words retired from both, are in
[TERMINOLOGY.md](TERMINOLOGY.md); `internal/docs/terminology_lint_test.go` fails
when a retired word is used outside a dated record.

An entry reads `**term** — definition. Defined: [where](link). Replaces: words.`
The `Replaces:` clause is the one place a retired word may stand in a glossary.

- **adoption** — choosing to take a tool into your workflow; a tool with no row is
  absent, never adopted. Defined: [SPEC-UPDATE.md, The verbs](SPEC-UPDATE.md#the-verbs).
- **beat** — a machine's heartbeat in Redis, `bench:<name>:beat`, carrying its measured facts.
  Defined: [SPEC-CONFIG.md, Kinds](SPEC-CONFIG.md#kinds).
- **bench** — a machine of the fleet that runs work or CI, named by its tailnet host.
  Defined: [SPEC-CONFIG.md, Kinds](SPEC-CONFIG.md#kinds).
- **bus** — messages between AIs as Redis streams, one stream per recipient, run by `nova-bus`.
  Defined: [SPEC-BUS.md, The data](SPEC-BUS.md#the-data). Replaces: the git bus.
- **cairn** — a note a friend leaves for the next session, written and read by `nova-cairn`.
  Defined: [SPEC-CAIRN.md, The four verbs](SPEC-CAIRN.md#the-four-verbs).
- **card** — the whole brief one worker is handed: one task, its rules, its gate and its finish.
  Defined: [SPEC-CARD-CONTRACT.md, The frame and JOB.md](SPEC-CARD-CONTRACT.md#2-the-frame-and-jobmd).
- **class test** — a test that reads this repository's own text and refuses a shape wherever it stands.
  Defined: [SPEC-CI.md, The class tests](SPEC-CI.md#the-class-tests).
- **config row** — one record of the permanent configuration store, of a kind (friend, machine, tier, route).
  Defined: [SPEC-CONFIG.md, Kinds](SPEC-CONFIG.md#kinds).
- **coordinator** — the friend who holds the coordinator role, the one field of the sprint config row.
  Defined: [SPEC-CONFIG.md, Kinds](SPEC-CONFIG.md#kinds).
- **dogfood shape** — the issue shape the family files against its own tools: tool, command,
  verbatim output, expected, smallest fix. Defined: [SPEC-UPDATE.md, The rules, numbered](SPEC-UPDATE.md#the-rules-numbered).
- **down** — a friend or machine that is not answering; the one word for it in every table.
  Defined: [SPEC-FRIEND.md, Presence](SPEC-FRIEND.md#presence-pkgfriendpresencego). Replaces: asleep.
- **friend** — a named participant you exchange notes with, the coordinator included; her
  configuration is a config row. Defined: [SPEC-CONFIG.md, Kinds](SPEC-CONFIG.md#kinds).
- **lane** — one slot a worker takes to run a Go build or test on a machine, or one sprint-card run of a friend.
  Defined: [SPEC-FRIEND.md, One-shot lanes](SPEC-FRIEND.md#one-shot-lanes-internalfriendlanesgo).
- **ledger** — the shrink-only allowlist under `internal/ci/testdata`, one row per place still short of a rule.
  Defined: [STANDARD.md](STANDARD.md).
- **OK** — the verdict a successful verb line carries as its second token: `<TOKEN> OK`, on stdout.
  Defined: [SPEC.md, Conventions](SPEC.md#conventions).
- **receipt** — one line appended to `from-<me>/RECEIPTS` recording that a note arrived; not an approval.
  Defined: [SPEC.md, Conventions](SPEC.md#conventions).
- **REFUSED** — the verdict a tool prints when it could not run: `<TOKEN> REFUSED: <reason> (<remedy>)`, on stderr.
  Defined: [SPEC.md, Conventions](SPEC.md#conventions).
- **slot** — the unit of parallelism: how many cards a machine may run at once.
  Defined: [SPEC-SWARM.md, Bench slot leases](SPEC-SWARM.md#bench-slot-leases).
- **STALE** — `nova-update check`'s verdict for an installed version older than the latest its source publishes.
  Defined: [SPEC-UPDATE.md, The rules, numbered](SPEC-UPDATE.md#the-rules-numbered).
- **token ledger** — the per-day record of tokens spent, kept on Redis by `nova-tokens`.
  Defined: [SPEC-STATE.md, The key layout](SPEC-STATE.md#the-key-layout).
- **two-minute rule** — every CI job is capped at two minutes, on every platform; the target is under one.
  Defined: [SPEC-CI.md, The class tests](SPEC-CI.md#the-class-tests).
- **wall** — the kernel-enforced filesystem boundary a job runs inside, with separate read and write permissions.
  Defined: [SPEC-SANDBOX.md, The rules, numbered](SPEC-SANDBOX.md#the-rules-numbered).
