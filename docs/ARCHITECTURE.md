# Nova Tools Architecture

This document explains how nova-tools fits together. It describes the core
concepts, the tools and their roles, the stores and machines they run on,
and how a card moves through the system.

## Concepts

**self** — a named participant who uses the tools; every tool records actions
under a self (or `as <self>`). [SPEC-CONFIG.md][SPEC-CONFIG]

**friend** — a named participant in the fleet who can take roles like
coordinator. A friend has configuration (slots, tiers, roles) and reports
runtime state. [SPEC-CONFIG.md][SPEC-CONFIG]

**bud** — a friend who has been accepted into the fleet after a mutual exchange
of notes; a bud appears in the friends table with its roles and configuration.
[SPEC-FRIEND.md][SPEC-FRIEND]

**card** — a unit of work with a brief, readers, and a lifecycle from add to
land. Cards are tracked through work, review, and merge tables.
[SPEC-SPRINT.md][SPEC-SPRINT]

**sprint** — a coordinated sequence of cards dealt to a fleet of workers, read
before they land, and moved through state machines. [SPEC-SPRINT.md][SPEC-SPRINT]

**bus** — Redis streams where notes and messages are sent once and delivered
until acknowledged. [SPEC-BUS.md][SPEC-BUS]

**seat** — a named identity on a machine that holds keys and permissions for
tools. A seat determines which store access and secrets are available.
[SPEC-SECRETS.md][SPEC-SECRETS]

**coordinator** — the friend who holds the coordinator role, deciding which
cards are ready to land. The coordinator runs on a dedicated machine.
[SPEC-CONFIG.md][SPEC-CONFIG]

**bench** — a machine in the fleet that runs work cards or CI jobs. Benches
report their capacity and status through beats. [SPEC-CONFIG.md][SPEC-CONFIG]

**store** — the persistent data layer. Nova-tools uses two stores: Postgres
for permanent configuration and Redis for runtime state.
[SPEC-CONFIG.md][SPEC-CONFIG]

**store's twin** — the Redis copy of the configuration stored in Postgres.
Tools read configuration from Redis; `nova-config apply` syncs Postgres to Redis.
[SPEC-CONFIG.md][SPEC-CONFIG]

**loop record** — a scheduled job row that defines a process to run at
intervals, with a seat, secrets, and command. [SPEC-CONFIG.md][SPEC-CONFIG]

## Tools

Each tool under `cmd/` has a single responsibility:

- **nova-bus** — sends and receives messages over Redis streams.
[SPEC-BUS.md][SPEC-BUS]

- **nova-friend** — runs a daemon for a friend to participate in the fleet.
[SPEC-FRIEND.md][SPEC-FRIEND]

- **nova-table** — manages ordered-set tables stored in Redis.
[SPEC-NOVA-TABLE.md][SPEC-NOVA-TABLE]

- **nova-work** — tracks GitHub issues in a tree file. (pre-alpha)
[SPEC-WORK-V1.md][SPEC-WORK-V1]

- **nova-sprint** — coordinates cards, readers, and landing.
[SPEC-SPRINT.md][SPEC-SPRINT]

- **nova-redis** — runs local Redis and manages expiring values.
[SPEC-REDIS.md][SPEC-REDIS]

- **nova-config** — manages fleet configuration in Postgres and applies to Redis.
[SPEC-CONFIG.md][SPEC-CONFIG]

- **nova-swarm** — runs one-task AI workers with sandbox and budget limits.
[SPEC-SWARM.md][SPEC-SWARM]

- **nova-secrets** — manages encrypted secrets from a git repository.
[SPEC-SECRETS.md][SPEC-SECRETS]

- **nova-tokens** — reports token spend from AI session logs.
[SPEC-TOKENS.md][SPEC-TOKENS]

- **nova-memory** — searches markdown notes and checks drafts.
[SPEC-LOCAL.md][SPEC-LOCAL]

- **nova-decide** — makes typed decisions with probabilities.
[SPEC-NOVA-DECIDE.md][SPEC-NOVA-DECIDE]

- **nova-cairn** — keeps session notes as plain files.
[SPEC-CAIRN.md][SPEC-CAIRN]

- **nova-check** — checks markdown records and repositories.
[SPEC-CHECK.md][SPEC-CHECK]

- **nova-self-talk** — flags self-verdict sentences in writing.
[SPEC-TOOLWORK.md][SPEC-TOOLWORK]

- **nova-fuse** — records decisions to stop reading untrusted sources.
[SPEC-ISA.md][SPEC-ISA]

- **nova-sandbox** — runs commands inside OS-enforced walls.
[SPEC-SANDBOX.md][SPEC-SANDBOX]

- **nova-ci** — tracks test-time budgets.
[SPEC-CI.md][SPEC-CI]

- **nova-version** — reports installed tool versions.
[SPEC-VERSION.md][SPEC-VERSION]

- **nova-update** — compares and updates tools.
[SPEC-UPDATE.md][SPEC-UPDATE]

- **nova-local** — runs local model services.
[SPEC-LOCAL.md][SPEC-LOCAL]

- **nova-card** — generates briefs from ledgers, findings, or tool help. (pre-alpha)
[SPEC-CARD-CONTRACT.md][SPEC-CARD-CONTRACT]

- **nova-up** — plans and applies nova setup on a machine.
[SPEC-UP.md][SPEC-UP]

- **nova-doctor** — checks for missing dependencies and setup issues.
[SPEC-DOCTOR.md][SPEC-DOCTOR]

## Stores

### Redis stores and ACL users
Redis is the runtime store. The following stores are used:

| key pattern | purpose | owner |
|---|---|---|
| `bench:<name>:beat` | machine heartbeat | nova-friend |
| `nova-bus:<stream>:*` | message streams | nova-bus |
| `sprint:<stream>:*` | work/merge tables | nova-sprint |
| `config:*` | applied configuration | nova-config |
| `machine:<name>:*` | machine state | nova-sprint |

Postgres is the permanent store for configuration. The schema `config` is
owned by the `nova_config` role. [SPEC-CONFIG.md][SPEC-CONFIG]

### Postgres via nova-config
Postgres holds the fleet registry: machines, friends, routes, tiers, and loops.
`nova-config apply` syncs Postgres to Redis. [SPEC-CONFIG.md][SPEC-CONFIG]

### Secrets store
Encrypted secrets are stored in a private git repository and accessed via
`sops` and `age`. Each seat has its own key. [SPEC-SECRETS.md][SPEC-SECRETS]

### Git
The nova-tools repository itself is the source of truth for code and docs.
Releases are tagged and published to GitHub.

## Machines

**coordinator machine** — The machine where the coordinator's loops run. It
hosts the sprint server. [SPEC-CONFIG.md][SPEC-CONFIG]

**benches** — Fleet members that run work cards or CI jobs. Each bench
reports its capacity through beats. [SPEC-CONFIG.md][SPEC-CONFIG]

**tailnet** — The network connecting all machines. Every machine is reachable
by SSH over the tailnet. [SPEC-CONFIG.md][SPEC-CONFIG]

## Card lifecycle diagram

```
┌─────────────┐     ┌───────────┐     ┌─────────┐     ┌───────────┐     ┌────────┐     ┌─────────┐
│     add     │────▶│  waiting  │────▶│  ready  │────▶│  working  │────▶│ review │────▶│ merging │
└─────────────┘     └───────────┘     └─────────┘     └───────────┘     └────────┘     └─────────┘
                                                                                           │
                                                                                           ▼
                                                              ┌─────────┐     ┌──────────┐     ┌────────┐
                                                              │ landed  │◀────│  merge   │◀────│  stuck │
                                                              └─────────┘     └──────────┘     └────────┘
```

A card moves through the states: add → waiting → ready → working → review →
merging → landed. The coordinator decides when a card can transition. Cards
in review are read by friends. Cards in merging may get stuck and need manual
resolution. [SPEC-SPRINT.md][SPEC-SPRINT]

[SPEC-BUS]: SPEC-BUS.md
[SPEC-CARD-CONTRACT]: SPEC-CARD-CONTRACT.md
[SPEC-CONFIG]: SPEC-CONFIG.md
[SPEC-FRIEND]: SPEC-FRIEND.md
[SPEC-SPRINT]: SPEC-SPRINT.md
[SPEC-REDIS]: SPEC-REDIS.md
[SPEC-SECRETS]: SPEC-SECRETS.md
[SPEC-NOVA-TABLE]: SPEC-NOVA-TABLE.md
[SPEC-SWARM]: SPEC-SWARM.md
[SPEC-TOKENS]: SPEC-TOKENS.md
[SPEC-LOCAL]: SPEC-LOCAL.md
[SPEC-NOVA-DECIDE]: SPEC-NOVA-DECIDE.md
[SPEC-CAIRN]: SPEC-CAIRN.md
[SPEC-CHECK]: SPEC-CHECK.md
[SPEC-TOOLWORK]: SPEC-TOOLWORK.md
[SPEC-ISA]: SPEC-ISA.md
[SPEC-SANDBOX]: SPEC-SANDBOX.md
[SPEC-CI]: SPEC-CI.md
[SPEC-VERSION]: SPEC-VERSION.md
[SPEC-UPDATE]: SPEC-UPDATE.md
[SPEC-UP]: SPEC-UP.md
[SPEC-DOCTOR]: SPEC-DOCTOR.md
[SPEC-WORK-V1]: SPEC-WORK-V1.md
