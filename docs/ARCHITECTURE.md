# Nova Tools Architecture

This page explains how nova-tools fits together. First the concepts that
the specs use, then the architecture: tools, stores, machines, and how a card flows.

## Concepts

Each concept below is defined in its spec page; the words come from
[docs/TERMINOLOGY.md](TERMINOLOGY.md).

**self** — The identity a friend runs under. It is the friend's
name in nova-config, and every message or record is signed as that self.
(SPEC-CONFIG, SPEC-BUS)

**friend** — A named participant in the fleet, human or AI. A friend has
a nova-config row with slots, tiers, and roles. Friends exchange notes
over nova-bus and report presence via nova-friend. (SPEC-CONFIG, SPEC-FRIEND)

**bud** — A friend's short-lived runtime instance: a process or container
that beats to the sprint server and answers pings. (SPEC-FRIEND)

**card** — A unit of work with a brief: paths to touch, a task, and a
result shape. Cards are dealt by nova-sprint to members.
(SPEC-SPRINT)

**sprint** — A coordinated batch of cards dealt to a fleet of workers.
The sprint tracks state (inbox, ready, running, landed) in tables.
(SPEC-SPRINT)

**bus** — The message system over Redis streams. One stream per recipient,
one log per sender. Messages are pending until acknowledged.
(SPEC-BUS)

**seat** — A named store and identity pair in nova-config. Tools use
--seat to select which Redis and which self to operate as.
(SPEC-CONFIG)

**coordinator** — The friend who holds the coordinator role. It assigns
cards, reads pushes, and decides when the sprint moves.
(SPEC-CONFIG, SPEC-SPRINT)

**bench** — A machine of the fleet that runs work or CI. It has a nova-config
machine row and reports measured facts via its beat. (SPEC-CONFIG)

**store** — The Redis data layer for the sprint. It holds sprint tables,
member state, and friend data. ACL users separate concerns.
(SPEC-SPRINT)

**twin** — A local, store-free copy of the sprint state for testing.
Nova-sprint can run with a twin to explore card flow without Redis.
(SPEC-SPRINT)

**loop record** — A log of a friend's or member's main loop runs. Each
line captures the iteration number, what changed, and timing.
(SPEC-FRIEND)

## Architecture

### Tools Under cmd/

| Tool | One-line role | Spec |
|------|---------------|------|
| nova-bus | Messages between AIs over Redis streams | SPEC-BUS |
| nova-friend | A friend's daemon for wake, beat, and proof of life | SPEC-FRIEND |
| nova-table | Tables of ordered sets, backed by Redis | SPEC-NOVA-TABLE |
| nova-work | GitHub issue capture in tree files | SPEC-WORK-V1 |
| nova-redis | Redis instance owner and scratch data | SPEC-REDIS |
| nova-config | Fleet configuration (Postgres) applied to Redis | SPEC-CONFIG |
| nova-swarm | Native card runner with sandbox and budgets | SPEC-SWARM |
| nova-card | Generate briefs from ledgers, findings, or help | SPEC-CARD-CONTRACT |
| nova-local | Run local models for workers | SPEC-LOCAL |
| nova-secrets | Encrypted secrets in git, handed to commands | SPEC-SECRETS |
| nova-tokens | Token spend metering from session logs | SPEC-TOKENS |
| nova-memory | Search markdown notes | SPEC-ISA |
| nova-decide | Typed decisions with probabilities | SPEC-NOVA-DECIDE |
| nova-cairn | Session notes as plain files | SPEC-CAIRN |
| nova-check | Checks over markdown and repos | SPEC-CHECK |
| nova-self-talk | Flag standing self-verdicts | SPEC-ISA |
| nova-fuse | Decisions to stop reading untrusted sources | SPEC-ISA |
| nova-sandbox | OS-enforced filesystem wall | SPEC-SANDBOX |
| nova-ci | CI budgets and slow test checks | SPEC-CI |
| nova-version | Installed version reporting | SPEC-VERSION |
| nova-update | Update tools to latest releases | SPEC-UPDATE |
| nova-doctor | Dependency checks and install instructions | SPEC-DOCTOR |
| nova-up | Set up nova on one machine | SPEC-UP |
| nova-sprint | Sprint coordination CLI | SPEC-SPRINT |

### Stores

**Redis stores** — Nova-sprint uses Redis for its tables. ACL users
separate concerns: one for sprint data, one for member data, one for friend
data. Nova-config applies the ACL users to the store.
(SPEC-SPRINT, SPEC-CONFIG, SPEC-REDISACL)

**Postgres via nova-config** — Permanent fleet configuration lives in
Postgres. Nova-config reads the rows and applies them to Redis.
(SPEC-CONFIG)

**Secrets store** — Encrypted secrets in a git repository. Nova-secrets
handed secrets to commands one at a time. (SPEC-SECRETS)

**Git** — Source control for this repository and for the secrets store.
(SPEC-CARD-CONTRACT, SPEC-SECRETS)

### Machines

**coordinator's machine** — Where the coordinator's loops run. It is the
fleet row in nova-config. (SPEC-CONFIG)

**benches** — Fleet machines that run work or CI. Each bench reports its
load and build info via its beat. (SPEC-CONFIG)

**tailnet** — The private network where benches and the coordinator
communicate. (SPEC-SPRINT, SPEC-FRIEND)

### Card Path Diagram

```
+-----------+     +-----------+     +-----------+     +-----------+     +-----------+
|  add      |---->|  inbox    |---->|  ready    |---->|  running    |---->|  land     |
+-----------+     +-----------+     +-----------+     +-----------+     +-----------+
     |               |               |               |               |
     v               v               v               v               v
  brief            read           push            judge          merge
  created          by coord       to bench        by member        to dev
```

A card is:
1. **added** with a brief (by nova-card or manually)
2. **read** by the coordinator, who places it in inbox
3. **pushed** to a bench when ready
4. **run** by a member, which checks the result and judges it
5. **merged** (landed) into dev when it passes the gate

Each transition writes a receipt; the coordinator reads receipts before
advancing the sprint. (SPEC-SPRINT)

## Links

- [Terminology](TERMINOLOGY.md)
- [Sprint contract](SPEC-SPRINT.md)
- [Config spec](SPEC-CONFIG.md)
- [Bus spec](SPEC-BUS.md)
