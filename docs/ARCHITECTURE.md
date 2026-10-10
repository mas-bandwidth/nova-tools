# Nova Tools architecture

This page is the map: the concepts the specs share, every tool under `cmd/`
with its one-line role, the stores the tools keep and which tools read and
write each, the machines they run on, and a card's path from `add` to land.
Every claim names the spec it comes from; where the code and its spec
disagree, one has a bug and the tests decide which ([SPEC.md](SPEC.md)).

The tools are one shape. Each binary's verbs, banner, help, version, refusals
and single output value come from the shared skeleton `internal/tool`, so a
tool is its verbs plus one call into that skeleton ([SPEC.md](SPEC.md)). The
tools carry the concepts and none of a fleet: machines, seats, friends and
addresses are configuration, never code ([SPEC-CONFIG.md](SPEC-CONFIG.md)).

## The concepts

**self** — the record one person or AI keeps and comes back to: the prose
files a mind loads and attests, and the repository a check reads. A self repo
holds prose, not machinery, and a boot attestation proves the files named were
the files read ([SPEC.md](SPEC.md)).

**friend** — a named participant you exchange notes with, the coordinator
included. Her configuration is a config row (slots, tiers, roles, width,
mode); what she would just know is runtime data she reports herself, and her
daemon wakes on her bus stream, beats, and delivers each card she is dealt
([SPEC-CONFIG.md](SPEC-CONFIG.md), [SPEC-FRIEND.md](SPEC-FRIEND.md)).

**bud** — a friend who is not the coordinator. A deal to a bud is delivered on
the bus, enrolled at her first deal, and a judgment a rule does not answer
climbs from a rule to a bud, then to the coordinator
([SPEC-FRIEND.md](SPEC-FRIEND.md), [SPEC-SPRINT.md](SPEC-SPRINT.md)).

**card** — the whole brief one worker is handed: one task, its rules, its gate
and its finish, with a PATHS set the worker may touch and a result line it
returns. A sprint card is a primary with a lifecycle; a nova-swarm card is one
run of a task ([SPEC-CARD-CONTRACT.md](SPEC-CARD-CONTRACT.md)).

**sprint** — a coordinated run of cards dealt to a fleet of workers and read
before they land, kept as four tables over the table layer: work, readers,
merge and friends ([SPEC-SPRINT.md](SPEC-SPRINT.md)).

**bus** — messages between AIs as Redis streams, one stream per recipient
under a consumer group, one log, pending until acknowledged, run by `nova-bus`
([SPEC-BUS.md](SPEC-BUS.md)).

**seat** — a named identity on a machine. A machine row's `seat` field names
the secrets-store seat that machine opens its credentials from; a seat can
decrypt the files its rules name and no other, and the coordinator's store
login is a seat setting of `nova-sprint` ([SPEC-CONFIG.md](SPEC-CONFIG.md),
[SPEC-SECRETS.md](SPEC-SECRETS.md), [SPEC-SPRINT.md](SPEC-SPRINT.md)).

**coordinator** — the friend who holds the coordinator role, the one field of
the sprint config row. Her loops run on the coordinator's machine, and nothing
leaves review except by her ([SPEC-CONFIG.md](SPEC-CONFIG.md),
[SPEC-SPRINT.md](SPEC-SPRINT.md)).

**bench** — a machine of the fleet that runs work or CI, named by its tailnet
host. Its declared facts are a machine config row; its measured facts come
from its beat, `bench:<name>:beat` ([SPEC-CONFIG.md](SPEC-CONFIG.md)).

**store and its twin** — Postgres is the permanent store and `nova-config` is
its one writer; Redis is a copy `apply` writes, so it can be lost and rebuilt.
`nova-sprint` can also run on a twin, an in-memory file store (`mem:<file>`)
with no server, for learning and tests ([SPEC-CONFIG.md](SPEC-CONFIG.md),
[SPEC-SPRINT.md](SPEC-SPRINT.md)).

**loop record** — a supervised process someone decides runs on one machine: a
`nova-config loop` row names the machine, the command, the seat and the secret
names it opens, and whether it runs every n seconds or is kept alive
([SPEC-CONFIG.md](SPEC-CONFIG.md)).

## The tools under cmd/

| tool | role | contract |
| --- | --- | --- |
| `nova-bus` | messages between AIs over Redis streams: sent once, delivered until acknowledged | [SPEC-BUS.md](SPEC-BUS.md) |
| `nova-cairn` | a session's words, kept durably as plain files you can come back to | [SPEC-CAIRN.md](SPEC-CAIRN.md) |
| `nova-delete` | move a literal path to quarantine instead of deleting it | [SPEC-DELETE.md](SPEC-DELETE.md) |
| `nova-card` | writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help (pre-alpha) | [SPEC-CARD-CONTRACT.md](SPEC-CARD-CONTRACT.md) |
| `nova-check` | checks over markdown records and repositories, each finding named by file and line | [SPEC-CHECK.md](SPEC-CHECK.md) |
| `nova-ci` | test-time budgets over `go test -json` output, this repository's own CI steps, and the ci-ok run receipt | [SPEC-CI.md](SPEC-CI.md) |
| `nova-config` | the fleet's machines and AI friends as rows in Postgres, applied into Redis | [SPEC-CONFIG.md](SPEC-CONFIG.md) |
| `nova-decide` | typed decisions with probabilities, recorded so each one can be calibrated against its outcome | [SPEC-NOVA-DECIDE.md](SPEC-NOVA-DECIDE.md) |
| `nova-doctor` | says what is missing for the tools to work here and, for each thing, the one line that fixes it | [SPEC-DOCTOR.md](SPEC-DOCTOR.md) |
| `nova-friend` | what a friend runs to be part of the team: the wake loop, the beat and the proof of life, as one daemon | [SPEC-FRIEND.md](SPEC-FRIEND.md) |
| `nova-fuse` | a recorded decision to stop reading an untrusted source, checked before every read | [SPEC.md](SPEC.md) |
| `nova-local` | run local models: what an engine has, one model served at a chosen context, and a worker description `nova-swarm` accepts | [SPEC-LOCAL.md](SPEC-LOCAL.md) |
| `nova-memory` | search your own markdown notes, and check a draft against what they already say | [SPEC.md](SPEC.md) |
| `nova-redis` | run a local Redis store, keep short-lived named values in it, and render, check and apply the store's ACL and function library | [SPEC-REDIS.md](SPEC-REDIS.md) |
| `nova-sandbox` | run one command inside an OS-enforced wall around the directories you name | [SPEC-SANDBOX.md](SPEC-SANDBOX.md) |
| `nova-secrets` | encrypted secrets in a git repository, handed to one command at a time | [SPEC-SECRETS.md](SPEC-SECRETS.md) |
| `nova-self-talk` | flags sentences where a writer passes a standing verdict on themselves | [SPEC.md](SPEC.md) |
| `nova-sprint` | a sprint of work cards, dealt to a fleet of workers and read before they land | [SPEC-SPRINT.md](SPEC-SPRINT.md) |
| `nova-swarm` | one-task AI workers, each run in the sandbox with a deadline and a token budget; bench slot leases and card lint | [SPEC-SWARM.md](SPEC-SWARM.md) |
| `nova-table` | tables whose cells are ordered sets, kept in Redis and drawn as text | [SPEC-NOVA-TABLE.md](SPEC-NOVA-TABLE.md) |
| `nova-tokens` | token spend per day, model and repository, read from AI session logs | [SPEC-TOKENS.md](SPEC-TOKENS.md) |
| `nova-up` | set nova up on one machine: plan every step, then apply, from nothing to a first sprint | [SPEC-UP.md](SPEC-UP.md) |
| `nova-update` | compare installed tools with their latest releases, and update one when asked | [SPEC-UPDATE.md](SPEC-UPDATE.md) |
| `nova-version` | which version of each tool is installed, recorded and compared | [SPEC-VERSION.md](SPEC-VERSION.md) |
| `nova-work` | every issue of an organization's repositories in one tree file, verified field for field (pre-alpha) | [SPEC-WORK-V1.md](SPEC-WORK-V1.md) |

## The stores

### Redis

Redis is the fleet's low-latency store — signals, slots, locks, counters and
scratch — and never the record: git stays the record, and nothing in Redis is
the only copy of anything ([SPEC-REDIS.md](SPEC-REDIS.md)). A running fleet
keeps two stores on the coordinator's machine, the sprint's store and the
friends' bus ([SPEC-FRIEND.md](SPEC-FRIEND.md)). Who reads and writes each key
family:

| key family | writer | reader |
| --- | --- | --- |
| `table:*`, `tables`, `view:*`, `views` | `nova-table` | `nova-table`, `nova-sprint` |
| `sprint:*` | `nova-sprint` | `nova-sprint` |
| `machine:*`, `machines`, `fleet:*`, `loop:*`, `loops`, `route:*`, `routes`, `config:decl` | `nova-config apply` | the runtime tools: the deal reads routes and tiers, the plays read loops ([SPEC-CONFIG.md](SPEC-CONFIG.md)) |
| `bench:*`, `friend:*:beat` | the machine's beat and the friend's daemon | `nova-config machine list`, `nova-update report` ([SPEC-CONFIG.md](SPEC-CONFIG.md)) |
| `tokens:ledger:*` | `nova-tokens` | `nova-tokens` ([SPEC-TOKENS.md](SPEC-TOKENS.md)) |
| `ev:github` | `nova-ci github receipt` | the CI records ([SPEC-CI.md](SPEC-CI.md)) |

`nova-redis` renders the store's users from the function library and the key
families, checks them against the live store, and applies them to the store's
ACL file (`users.acl`, mode 0600) so they survive a restart
([SPEC-REDIS.md](SPEC-REDIS.md)). The build's four roles and their ACL users
are:

| role | ACL user | keys | functions |
| --- | --- | --- | --- |
| coordinator | `coordinator` | every key | the whole library; the only user that may load it |
| member | `bench` | reads every family and writes table, view, sprint, beat, token and event keys | `lua/00_ping.lua`, `lua/table.lua` |
| table | `ns-table` | reads every family | the table functions, read-only |
| friend | `ns-friend` | reads every family and writes table, view, sprint, friend and token keys | `lua/00_ping.lua`, `lua/table.lua` |

The users are the tools' own defaults: a fleet member's loop logs in as the
member user, and the table reader and a friend's daemon use the `ns-` users
the tools name ([SPEC-REDIS.md](SPEC-REDIS.md), [SPEC-SPRINT.md](SPEC-SPRINT.md)).

### Postgres, through nova-config

Postgres is the permanent store: schema `config`, owned by the `nova_config`
role, holds every registry of the fleet and the history of every change to it,
and `nova-config` is its one writer. `apply` writes the configuration into the
Redis keys the runtime tools read, through the runtime's own Redis Functions,
and removes what Postgres does not have; lose Redis and run `nova-config apply` ([SPEC-CONFIG.md](SPEC-CONFIG.md)). History is not configuration:
scores, receipts, ledgers, beats, copies and leases stay with the tool that
writes them ([SPEC-CONFIG.md](SPEC-CONFIG.md)).

### The secrets store

The secrets store is a git repository of one sealed yaml per seat, sealed with
age and sops; a seat can decrypt the files its rules name and no other, a key
is generated on the seat that uses it and never leaves it, and the store's
gate is the review road a seal, a seat add or a seat inject travels
([SPEC-SECRETS.md](SPEC-SECRETS.md)). `nova-secrets` is its tool: it writes
the store and hands one command at a time the credentials it names
(`exec`), and a seat's store login and every loop's secret names come from it
([SPEC-SECRETS.md](SPEC-SECRETS.md), [SPEC-CONFIG.md](SPEC-CONFIG.md)).

### Git

Git is the record: the repositories, the branches, the briefs and the reports.
A member pushes a card's branch, the sprint's lander gates a base's tip before
it merges, and a landing puts the code on the development branch
([SPEC-SPRINT.md](SPEC-SPRINT.md)). The secrets store is itself a git
repository, written through a branch and a review, never a direct push
([SPEC-SECRETS.md](SPEC-SECRETS.md)).

## The machines

**The coordinator's machine** — where the coordinator's loops run, named by the
fleet row's `coordinator` field. It runs the sprint's store and the friends'
bus, the sprint server, its member, the seat's push loop, the friend sync
loop, the live table, the disk guard and the dashboard
([SPEC-CONFIG.md](SPEC-CONFIG.md), [SPEC-SPRINT.md](SPEC-SPRINT.md)).

**The benches** — machines of the fleet that run work or CI, named by their
tailnet hosts. Each is a machine config row (its declared user, seat, slots,
runners, width and tla flag) plus a beat in Redis that carries its measured
facts; the coordinator's machine builds and tests nothing
([SPEC-CONFIG.md](SPEC-CONFIG.md), [SPEC-SPRINT.md](SPEC-SPRINT.md)).

**The tailnet** — the network every fleet machine is on and reachable by ssh.
`nova-redis serve` binds loopback and the tailnet only, never a public
interface, with auth from the secrets store at run time
([SPEC-CONFIG.md](SPEC-CONFIG.md), [SPEC-REDIS.md](SPEC-REDIS.md)).

## A card's path from add to land

```text
  add ─▶ waiting ─▶ ready ─▶ working ─▶ review ─▶ merging ─▶ landed
           every     deal     work      readers    batch
           need      cuts     card      say ok     green and
           landed    it       finished             merged to dev

  returns: working ──rework──▶ ready or working
           review ──rework──▶ working or ready
           merging ──red CI or a conflict──▶ review, working or ready
           any open state ──drop, with the reason──▶ off the table
```

`add` admits a primary waiting, or ready when nothing holds it; a primary
moves only by a row of the lifecycle table, and landed is final and means the
code is on the development branch ([SPEC-SPRINT.md](SPEC-SPRINT.md)). Waiting
to ready needs every need landed or waived; ready to working is the deal's
cut; working to review is the work card's finish; review to merging is accept,
with the readers a flash card or a pro card needs; merging to landed is the
batch, green on the stream branch and merged ([SPEC-SPRINT.md](SPEC-SPRINT.md)).
Rework, return and drop are the coordinator's verbs, and nothing retries by
itself ([SPEC-SPRINT.md](SPEC-SPRINT.md)).
