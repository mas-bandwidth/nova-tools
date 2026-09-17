# nova-redis — specification (draft 1, 2026-09-17)

`nova-redis` is a proposed local instance and an internal package at the
**ephemeral layer** (name provisional, nova-tools #130). Glenn, 2026-09-12:
*"What if we had a local redis instance here that you could all talk to."*
*"Some place to spill to that isn't git."* Rowan's decision is **both**: the
internal package first, for the ephemeral uses, and then the `nova-redis`
binary that owns the instance. Redis is the family's nervous system and
scratch — a low-latency place for signals, slots, locks and counters — and
never the record. **Git stays the record; nothing in Redis is the only copy
of anything.**

This spec is normative. If the code and this document disagree, one of them
has a bug, and the tests decide which. It is a sibling of [SPEC.md](SPEC.md),
whose **Conventions** section — exit codes, no guessed paths, the one-line
output grammar, `internal/oneline` and `internal/bounded` — applies here
unchanged and is not restated. Related: [SPEC-WAKE.md](SPEC-WAKE.md) (the
doorbell), [SPEC-SWARM.md](SPEC-SWARM.md) (slots and locks, budgets),
[SPEC-SECRETS.md](SPEC-SECRETS.md) (auth), the efficiency case
[ideas#774](https://github.com/mas-bandwidth/ideas/issues/774).

## Why

The efficiency case is ideas#774. A window that learns nothing still pays a
model turn, and the attention layer already paid to make that turn cheap
(SPEC-WAKE). What is left is the state a *set* of lines shares between turns:
who is live, which slot is taken, whose lock is held, what a budget has spent,
where a plan is. Read from a local instance, each of those is one subprocess
with no model turn; read by polling a git worktree, each is a fetch, a diff
and a turn. Redis is adopted for latency and for having one place to spill to
that is not the record — not to hold anything the record does not.

## Layer 1 — the internal package (owed first)

The first delivery is an internal client contract, not a server. Every
ephemeral use names a key, an owner and a **file fallback**; the same call
adopts the local instance when it is reachable and degrades to the fallback
when it is not. A fallback is not a second design: it is the same contract
over a file, so an outage is a latency regression and never a lost queue or a
changed answer. The named ephemeral uses are:

- the **wake** doorbell — a change signal a blocking watcher can be woken by,
  falling back to the report/entry files `nova-wake` already watches;
- **swarm slots and locks** — which worker holds which slot and which lane
  holds which lock, falling back to the lock files the swarm already takes;
- **budgets** — the spend counters rule 13 already keeps (the former
  `nova-go` budgets, now owned by `nova-swarm`), falling back to the usage
  rows the record carries;
- **plan state** — where a plan is and what it has done, falling back to the
  plan file on disk.

Each use is a rule in this spec and a test in the package: write through the
instance, kill the instance, read the fallback, and assert the same value.
Nothing in the package may make Redis the authority; the record is.

## Layer 2 — the `nova-redis` binary (owns the instance)

The second delivery is the binary that owns one local instance. Its verbs:

- `status` reports whether the instance is reachable, what it is bound to, the
  owner and key counts, and the two safety facts — auth is required and
  **persistence** is off.
- `spill` writes a scratch value under an **owner prefix** and a required
  **TTL**; `recall` reads it back and refuses a missing or expired key. The
  key form is `<owner>:<name>`, and a write with no owner or no TTL is
  refused. Scratch is scratch: nothing spilled is a record, and recall is
  allowed to miss.
- `presence` lists the live lines seen by heartbeat keys that expire on their
  own, so a crashed line ages out without anyone writing a tombstone.
- `check` prints one line and earns its exit code, the Conventions' check:
  instance reachable, bound where this spec allows, auth on.
- `version` and `help`, the two every binary in this family carries.

## Bind, auth and persistence

The first run is bound to **localhost** and the **tailnet** only, never a
public interface, with auth taken from **nova-secrets** at run time (no
plaintext on the bench and no secret in an argument). Persistence is off: no
RDB and no AOF, so a restart is a clean slate by construction and the record
in git is provably untouched. If a value must survive a restart, it does not
belong in Redis.

## Rules

1. **Git stays the record; nothing in Redis is the only copy of anything.**
2. Every ephemeral key carries an owner prefix and a TTL; an unbounded key is
   a bug.
3. Every Layer 1 use has a file fallback with the same contract, tested by
   killing the instance and reading through it.
4. Auth comes from nova-secrets; the instance is never bound beyond localhost
   and the tailnet.
5. Persistence is off; a restart is a clean slate.
6. No test in this tool opens a network socket to a provider or to Redis; the
   tests use fakes and the file fallback.

## Tests

The spec slice is pinned by `TestNovaRedisSpecFirstSlice`, which reads this
file and fails if a named term — the internal uses, the fallbacks, the verbs,
the owner prefix and TTL, the bind and auth and persistence rules, and the
record rule — is missing. Later slices add: the fallback round-trip per use
(write through Redis, kill it, read the file); `spill`/`recall` with a TTL that
expires; `presence` ageing out a heartbeat; and `check` refusing an instance
bound beyond localhost and the tailnet or with persistence on.
