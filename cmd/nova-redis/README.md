# nova-redis

## What it is

nova-redis: run a local Redis store, and keep short-lived named values in it

## Why use it

Keep short-lived scratch data between commands.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-redis@latest
nova-redis version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-redis).

No fixture and no instance: the lines below are refusals `spill` and
`fn load` make BEFORE they dial anything, so they read the same on every bench.
`cmd/nova-redis/firstrun_test.go` runs each `$` line and compares the output.
A write with no owner, or with no TTL, is refused and nothing is stored; the
round trip against an instance (`spill`, `recall`, and `recall` refusing an
expired key under a controlled clock) is in `cmd/nova-redis/spill_test.go`
over a miniredis fake. `fn load` and `fn check` on a store are in
`cmd/nova-redis/fn_test.go` over a fake and, against a throwaway
redis-server, in `cmd/nova-redis/fn_functional_test.go`.

```text
$ nova-redis spill --addr 127.0.0.1:6379 --name note --ttl 10m --value hi
SPILL REFUSED: --owner is required and may not be empty or hold ':' or whitespace; every key carries an owner prefix; run: nova-redis help

$ nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 0s --value hi
SPILL REFUSED: --ttl is required and must be above zero; an unbounded key is a bug; run: nova-redis help

$ nova-redis fn load
FN-LOAD REFUSED: --addr is required: the store's address as <host:port>, such as 127.0.0.1:6379 (no default); refusing to guess; run: nova-redis help
```

## Verbs

The [nova-redis section of the command reference](../../docs/CLI.md#nova-redis) documents every verb's flags, effect and exit codes.

- `serve`
- `spill`
- `recall`
- `fn`
- `acl`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-REDIS.md](../../docs/SPEC-REDIS.md).
