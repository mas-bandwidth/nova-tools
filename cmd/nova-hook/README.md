# nova-hook

## What it is

nova-hook: hooks an acting identity to the live, proven pushes its role needs; some bus mailbox checks are advisory

## Why use it

Tools gate the acting identity on the live, proven pushes its role needs. Some
`nova-bus send` and `recv` mailbox checks are advisory by default;
`--require-push` or `NOVA_BUS_REQUIRE_PUSH=1` makes them strict. This is how you
hook in, see who is, and unhook.

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

The heartbeat: `nova-hook serve` runs under its own service identity (`nova-hook`, needing no hooked session) and challenges every hooked coordinator, friend and comms name once every `--every` (default 1m), as a bus message with the subject `HOOK CHALLENGE nonce=<n>`, through the push the name already has. Only the model answers, from inside its own turn, with `nova-hook prove --as <name> --nonce <n>` within the interval; no daemon, loop or script may call `prove`. A name with no answer within `--down-after` (default 5m) reads down and is unhooked at the next pass. Readers judge whether the server itself is stale from `--every` (two intervals), separate from `--down-after`. Each real change is told once per pass: the coordinator gets one FRIEND DOWN / FRIEND BACK judgment (answer with `nova-bus send --re <id>`; OVERDUE once after two minutes), everyone else one message (COORDINATOR DOWN as a judgment: who takes over). `nova-hook friends --json` is every name's state. `nova-hook status --as <name>` fails with `NO CHALLENGE FOR <age>` when no challenge came for two intervals: the server is gone.

## Verbs

The reference for its verbs is [docs/CLI.md](../../docs/CLI.md#nova-hook).

- `status`
- `hook`
- `prove`
- `unhook`
- `friends`
- `serve`
- `version`

## Spec

The contract is [docs/SPEC-HOOK.md](../../docs/SPEC-HOOK.md); the module is [hook/](../../hook), the machine [tla/Hook.tla](../../tla/Hook.tla).
