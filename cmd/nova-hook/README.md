# nova-hook

## What it is

nova-hook: hooks an identity in: the pushes its role needs, live and proven, without which no bus, friend or sprint tool runs

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

The heartbeat: one `nova-hook serve --as <name>` (hooked in as coordinator or comms) challenges every hooked coordinator, friend and comms name once a minute, as a bus message with the subject `HOOK CHALLENGE nonce=<n>`, through the push the name already has. Only the model answers, from inside its own turn, with `nova-hook prove --as <name> --nonce <n>` within the minute; no daemon, loop or script may call `prove`. Two unanswered in a row unhook the name. Each real change is told once per pass: the coordinator gets one FRIEND DOWN / FRIEND BACK judgment (answer with `nova-bus send --re <id>`; OVERDUE once after two minutes), everyone else one message (COORDINATOR DOWN as a judgment: who takes over). `nova-hook friends --json` is every name's state. `nova-hook status --as <name>` fails with `NO CHALLENGE FOR <age>` when no challenge came for two intervals: the server is gone.

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
