# nova-hook

## What it is

nova-hook: hooks an identity in: the pushes its role needs, live and proven, without which no bus, friend or sprint tool runs

## Why use it

Every bus, friend and sprint tool refuses an identity that is not hooked in; this is how you hook in, see who is, and unhook.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-hook@latest
nova-hook version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-hook).

```text
$ nova-hook status --as m1
$ nova-hook hook --as m1 --role member --once
$ nova-hook unhook --as m1
```

A session hooks in under its Monitor with `nova-hook hook --as <name> --role <role>` and answers each `HOOK CHALLENGE` line it prints with the `nova-hook prove` line that challenge carries.

## Verbs

The reference for its verbs is [docs/CLI.md](../../docs/CLI.md#nova-hook).

- `status`
- `hook`
- `prove`
- `unhook`
- `version`

## Spec

The contract is [docs/SPEC-HOOK.md](../../docs/SPEC-HOOK.md); the module is [hook/](../../hook), the machine [tla/Hook.tla](../../tla/Hook.tla).
