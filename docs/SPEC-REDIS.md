# nova-redis — specification

`nova-redis` owns the local Redis instance and its scratch verbs. Redis is the
fleet's low-latency store — a place for signals, slots, locks, counters and
scratch — and never the record. **Git stays the record; nothing in Redis is the
only copy of anything.**

This spec is normative. If the code and this document disagree, one of them
has a bug, and the tests decide which. It is a sibling of [SPEC.md](SPEC.md),
whose **Conventions** section — exit codes, no guessed paths, the one-line
output grammar, `internal/oneline` and `internal/bounded` — applies here
unchanged and is not restated. Related: [SPEC-SECRETS.md](SPEC-SECRETS.md)
(auth).

## The verbs

```
nova-redis serve  --bind <addr>[,<addr>...] --port <port> --dir <store-dir>
nova-redis spill  --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text>
nova-redis recall --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name>
nova-redis fn load  --addr <host:port> [--user <name>] [--password-env <NAME>]
nova-redis fn check --addr <host:port> [--user <name>] [--password-env <NAME>]
nova-redis version
nova-redis help
```

- `serve` launches the instance in the foreground under the rules of the next
  section: `--bind` names loopback and tailnet addresses only and has no
  default, the password comes from nova-secrets and reaches `redis-server` on
  stdin, and the store lives in `--dir` (no default) under the fleet store's
  rules: AOF on, no eviction, no TTL policy.
- `spill` writes a scratch value under an **owner prefix** and a required
  **TTL**; `recall` reads it back. The key form is `<owner>:<name>`. A spill
  with no owner, or with a missing, zero or negative TTL, is refused (exit 2)
  and writes nothing; an unbounded key is a bug. Scratch is scratch: nothing
  spilled is a record, and recall is allowed to miss — a missing or expired key
  is exit 1.
- `fn load` puts the `nova_sprint` function library this binary embeds on the
  store unless the store holds exactly its code (`LOADED`, `UNCHANGED` or
  `REPLACED`, with its digest); it is for the one place that deploys, and it
  replaces other code under the library's name. `fn check` changes nothing:
  `OK` (exit 0), `STALE` or `MISSING` (exit 1) with the store's digest and this
  binary's. A failure of either is one `FAILED` line on stderr with the remedy
  for its cause: exit 1 when the store answered with a refusal (`NOPERM`, a
  library it would not take), exit 2 when no answer came or the login was
  refused.
- Every verb that dials a store (`spill`, `recall`, `fn load`, `fn check`)
  refuses a missing or empty `--addr`, or one without a host and a port (exit
  2), before anything is dialled. The password is read from the variable
  `--password-env` names (default `NOVA_REDIS_PASSWORD_ENV`, else
  `NOVA_REDIS_PASSWORD`), never from an argument; `--user` names the ACL user
  to log in as (default `NOVA_REDIS_USER`; with neither, the store's default
  user). A `--password-env` that is not a variable name, a user name with
  whitespace, and a user whose password variable is empty are refused (exit 2)
  before anything is dialled. A store that cannot be reached, or a login it
  refuses, is one FAIL line on stderr with the next step (exit 2). A spill
  whose reply is lost after the store took it is `SPILL UNCONFIRMED` (exit 1):
  the write may have committed, so the remedy is a `recall`, never a second
  spill. Each scratch verb is one round trip.
- `version` and `help`, the two every binary in this family carries.

## Bind, auth and persistence

The instance is bound to **localhost** and the **tailnet** only, never a
public interface, with auth taken from **nova-secrets** at run time (no
plaintext on the bench and no secret in an argument). Persistence is on: the
instance is the fleet's store, and a key in it is not allowed to disappear, so
a restart on the same `--dir` replays every key.

`serve` is where these are enforced. The tailnet is the Tailscale ranges,
`100.64.0.0/10` and `fd7a:115c:a1e0::/48`; a wildcard, public or LAN address,
or a hostname, is refused before anything starts. The password is read from
`NOVA_REDIS_PASSWORD`, which `nova-secrets exec` fills; it is written into the
config `redis-server` reads on stdin (`redis-server -`), dropped from the
child's environment, and never written to a file. The config is the fleet
store's rules, one config owned by `nova-redis`: `dir` is `--dir`, absolute,
created 0700 when missing and never defaulted; `appendonly yes` with
`appendfsync everysec`, so a crash loses at most one second; `save 60 1`, an
RDB snapshot as the second copy; `maxmemory-policy noeviction`, so a full
instance refuses a write rather than drop a key; and no TTL policy, so store
keys do not expire. A SIGTERM to `serve` is a clean stop: `redis-server`
fsyncs the AOF, saves and exits 0. A bench runs it as

```
nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis serve --bind 127.0.0.1,<tailnet-address> --port 6379 --dir /var/lib/nova-redis
```

## Rules

1. **Git stays the record; nothing in Redis is the only copy of anything.**
2. Every ephemeral (scratch) key carries an owner prefix and a TTL; an
   unbounded scratch key is a bug. Store keys carry no TTL: the store has no
   TTL policy.
3. Auth comes from nova-secrets; the instance is never bound beyond localhost
   and the tailnet.
4. Persistence is on (AOF, no eviction); a restart on the same `--dir` keeps
   every key.
5. No unit test in this tool opens a network socket to a provider or to a
   fleet Redis; the unit tests use fakes, and the functional tests (the
   restart, the connection failures) run a throwaway `redis-server` on
   loopback in the test's temp dir.

## Tests this spec demands

`cmd/nova-redis/spill_test.go` proves 1 to 5 and 9 on the production path with
an injected clock, `cmd/nova-redis/serve_test.go` proves 6, 7 and 8, and
`cmd/nova-redis/serve_functional_test.go` and
`cmd/nova-redis/connfail_functional_test.go` prove 10 to 12 against a real
`redis-server` on loopback; `cmd/nova-redis/fn_test.go` proves 13 to 16, and
`cmd/nova-redis/fn_functional_test.go` proves 17 on a real `redis-server`.

1. `TestAddrRefusedWhenMissingOrEmpty` — a missing, empty or blank `--addr`, or one without a host and a port, is refused at exit 2 before anything is dialled.
2. `TestSpillRefusedWithoutOwner` — a spill with no owner is refused and writes nothing.
3. `TestSpillRefusedWithoutTTL` — a spill with a missing, zero or negative TTL is refused and writes nothing.
4. `TestRecallRefusesAnExpiredKey` — `recall` of a key whose TTL has expired is a miss, exit 1.
5. `TestEveryEphemeralKeyCarriesOwnerAndTTL` — every key a spill writes is `<owner>:<name>` and carries a TTL; an unbounded key is a bug.
6. `TestBoundToLocalhostAndTailnetOnly` — bound to localhost and the tailnet only, never a public interface.
7. `TestAuthFromNovaSecretsNeverAPlaintextArgument` — auth taken from nova-secrets at run time: no plaintext on the bench and no secret in an argument.
8. `TestPersistenceIsAOFWithNoEviction` — the config is the fleet store's rules: AOF on, fsync every second, RDB every 60 s, no eviction, `--dir` required and absolute.
9. `TestSpillAndRecallAreOneRoundTripEach` — each scratch verb is one round trip after the handshake.
10. `TestRestartOnTheSameDirKeepsTheStore` — a key written through `serve --dir <d>` is intact after a stop and a restart on the same `--dir`, with no TTL.
11. `TestOpenFailureIsOneLineExitTwo` — a store that cannot be reached, or a login it refuses, is one FAIL line with the next step, exit 2.
12. `TestSpillWhoseExecReplyIsLostIsUnconfirmed` — a spill whose reply is lost after the store took it is `SPILL UNCONFIRMED`, exit 1, with a read-back as its remedy.
13. `TestFnLoadAndCheckOnAStore` — `fn load` loads the embedded library and `fn check` reads it back as current, changing nothing.
14. `TestFnRefusesBeforeTheDial` — `fn load` and `fn check` refuse a bad address or login flag before anything is dialled.
15. `TestFnFailuresNameTheStateAndTheRemedy` — a failure of either `fn` verb is one `FAILED` line naming the store's state and the remedy for its cause.
16. `TestEveryVerbLogsInAsTheUserItIsGiven` — every verb that dials a store logs in as the `--user` it is given.
17. `TestFnVerbsOnARedisServer` — `fn load` and `fn check` against a real `redis-server`.
