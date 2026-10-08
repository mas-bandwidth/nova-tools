# nova-friend

## What it is

nova-friend: what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon

## Why use it

Be reachable as a friend: woken by a message, counted present, proven alive.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-friend@latest
nova-friend version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-friend).

Run by `cmd/nova-friend/firstrun_test.go` on a throwaway redis-server whose
`friends` set names ada and bob (what `nova-config apply` writes for two friend
rows), its address in `NOVA_BUS_REDIS`, so the lines read as a reader types
them. The sitting is the canary by hand, with no daemon running: a dry-run
install prints the plan for bob's agent; a dry-run uninstall the plan to undo
it; ada, as the coordinator, pings bob with a nonce; bob's session answers
with `pong` (one note to ada, and the pong file under the home directory,
`./home/.nova-friend/bob`); `wait-pong` finds it on the log from bob's own
stream; `status` says no daemon has run as bob (exit 1). `./` is a directory of the test's own, and so are the
home directory and the uid the plan names. The run-owned values are the
message `id=` (a ULID from the store's time), `at=`, and `took=`.

```text
$ nova-friend install --as bob --harness opencode --dir ./bob --dry-run
INSTALL OK label=com.nova.friend-bob plist=./home/Library/LaunchAgents/com.nova.friend-bob.plist launchd_log=./home/Library/Logs/nova-friend-bob.log dry_run=true
INSTALL PLAN command="write ./home/Library/LaunchAgents/com.nova.friend-bob.plist"
INSTALL PLAN command="launchctl bootout gui/501/com.nova.friend-bob"
INSTALL PLAN command="launchctl bootstrap gui/501 ./home/Library/LaunchAgents/com.nova.friend-bob.plist"
INSTALL NOTE the agent runs: nova-friend run --as bob --harness opencode --dir ./bob --width 0, with --redis and --server as given here

$ nova-friend uninstall --as bob --dry-run
UNINSTALL OK label=com.nova.friend-bob plist=./home/Library/LaunchAgents/com.nova.friend-bob.plist dry_run=true
UNINSTALL PLAN command="launchctl bootout gui/501/com.nova.friend-bob"
UNINSTALL PLAN command="rm ./home/Library/LaunchAgents/com.nova.friend-bob.plist"

$ nova-friend ping --as ada --to bob --nonce abc123
PING OK nonce=abc123 id=01M42EJZ1D4JEFR6ESF1YJ3YJA to=bob at=2026-10-04T03:40:12Z
PING NOTE wait for it: nova-friend wait-pong --from bob --nonce abc123

$ nova-friend pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4
PONG OK nonce=abc123 to=ada id=01M42EJZ1F8FXB5T0F6EXJCRS1 at=2026-10-04T03:40:12Z

$ nova-friend wait-pong --from bob --nonce abc123 --timeout 2s
WAIT-PONG OK nonce=abc123 from=bob at=2026-10-04T03:40:12Z took=1ms queue=2 working=1 width=4 daemon=false

$ nova-friend status --as bob --dir ./bob
! STATUS NONE: no daemon has run as bob (no status file in ./home/.nova-friend/bob); run: nova-friend install --as bob --harness <h> --dir ./bob
```

## Verbs

The [nova-friend section of the command reference](../../docs/CLI.md#nova-friend) documents every verb's flags, effect and exit codes.

- `run`
- `install`
- `uninstall`
- `check`
- `host`
- `ping`
- `ping-install`
- `ping-uninstall`
- `pong`
- `wait-pong`
- `status`
- `refuse-go`
- `resume`
- `serve`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-FRIEND.md](../../docs/SPEC-FRIEND.md).
