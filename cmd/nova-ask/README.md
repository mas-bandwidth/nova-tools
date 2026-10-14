# nova-ask

## What it is

nova-ask: ask a friend over the psychic link (nova-bus) and wait for her answer, bounded; at the
deadline wake her at her harness window and wait once more; then exit, so the caller tells Glenn.

## Why use it

When she is up, the bus is enough; when she is down, a message waits on her stream forever. nova-ask
never waits forever and needs nobody's permission at 3am: it sends, waits `--deadline`, wakes, waits
`--deadline` again, and exits. The whole run is at most 2 x deadline + 10 s.

- exit 0 `ANSWERED <name> <id> after <s>s`: a message from her to you with `re=<id>` (a reply without `--re` does not count)
- exit 1 `NO ANSWER <name> after wake`: the caller's signal to notify Glenn
- exit 2 could not run, or `WAKE REFUSED ...`: the UI scripting needs the one-time Accessibility grant
  (System Settings > Privacy & Security > Accessibility) for the process the line names

The wait reads the log (`bus2:log`) forward, as `nova-bus log` does: it touches no consumer group, so
nothing meant for your own receiver is taken or acked. Wake adapters are per friend (`Adapters` in
window.go); one today: stella -> the Codex desktop window (`com.openai.codex`, window "ChatGPT"),
which gets the line "Rowan: please read your psychic link (bus message <id>) and reply with --re."
set as its chat input's value over the Accessibility API and sent with its Send button; nothing is
brought to the front.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-ask@latest
```

## First run

```text
$ nova-ask --as rowan --to stella --subject "ask-or-wake live proof" --body "..." --deadline 60s --redis 127.0.0.1:6381
SENT 01M4XXA8RXCHCX88Y844ESYGY9 to=stella at=2026-10-14T19:13:06Z
ANSWERED stella 01M4XXA8RXCHCX88Y844ESYGY9 after 22s
```

## Verbs

One: the flags above. The message is sent as `nova-bus send` sends it (kind request), under the same
hook gate and identity rules; see [docs/SPEC-BUS.md](../../docs/SPEC-BUS.md).

## Spec

The bus is [docs/SPEC-BUS.md](../../docs/SPEC-BUS.md).
