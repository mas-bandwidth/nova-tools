# The one line each friend adds to their window's startup

This is the operational half of `nova-wake beat` and `nova-wake presence`
([docs/SPEC-WAKE.md](SPEC-WAKE.md), **beat and presence**; nova-tools #2610).
One command, started once when a window opens, so that everybody else can tell
whether you are here without asking you and without spending a turn.

**It costs nothing.** The beat is a process, not a prompt: one `SET` every 30
seconds, no model, no tokens, no turn started. It reads nothing and says
nothing. Starting it is the only thing you ever do about it — a window that
exits, runs out of credit or is killed simply stops writing, and the key lapses
within 90 seconds. There is no shutdown step to remember.

## The line

On the Studio, where every friend's window runs as `glenn`:

```bash
nohup ~/.local/bin/nova-secrets exec \
  --store ~/nova-bench/secrets --as swarm-studio \
  --key ~/.config/nova-secrets/swarm-studio.key --sops /opt/homebrew/bin/sops \
  --only NOVA_REDIS_BENCH_PASSWORD --require NOVA_REDIS_BENCH_PASSWORD -- \
  ~/.local/bin/nova-wake beat --as <YOUR NAME> --store 100.115.99.19:6380 \
  >> ~/<your>-working/.beat.log 2>&1 &
```

Per friend, the two things that change are `--as` and the log path:

| friend  | `--as`    | log                              |
| ------- | --------- | -------------------------------- |
| Johnny  | `johnny`  | `~/johnny-working/.beat.log`     |
| Stella  | `stella`  | `~/stella-working/.beat.log`     |
| Emma    | `emma`    | `~/emma-working/.beat.log`       |
| Freddy  | `freddy`  | `~/freddy-working/.beat.log`     |
| Alex    | `alex`    | `~/alex-working/.beat.log`       |

The name is yours as the bus roster spells it, in any case — the key is
`friend:<name>` in lower case either way. Two windows of your own beating the
same name is harmless: they write the same key.

## The seat, and why it is the same one for everybody

The password is never a flag, a file you open, or a word you paste. It reaches
`nova-wake beat` as `NOVA_REDIS_BENCH_PASSWORD` through `nova-secrets exec
--only`, exactly the way `bench-row` hands it to `redis-cli`, and the ACL user
it authenticates as holds `~friend:*` on the store and nothing wider.

`NOVA_REDIS_BENCH_PASSWORD` lives in the **bench seat of the machine the window
is on**, not in your own seat: on the Studio that is `swarm-studio`
(`~/.config/nova-secrets/swarm-studio.key`), and a window on another bench uses
that bench's `swarm-<host>` seat — the key file is whichever
`~/.config/nova-secrets/swarm-*.key` is there, which is how `bench-row` finds
it:

```bash
seat=$(ls ~/.config/nova-secrets/ | grep -E '^(swarm-.*|air)\.key$' | head -1 | sed 's/\.key$//')
```

A friend's own seat (Stella's `~/.config/nova-secrets/stella/identity.age`, and
the `stella-*.yaml` files beside it) opens that friend's model keys and does
**not** hold the store password. It is not the seat to use here. On Linux
benches `sops` is at `~/.local/bin/sops` rather than `/opt/homebrew/bin/sops`.

## Checking it

```
$ nova-wake presence --store 100.115.99.19:6380 --participants ~/rowan-working/adopt-scratch/bus-send2/participants.json
friends: stella up 4s · johnny up 12s · emma AWAY 1h12m (last 09:41Z) · freddy none
```

`up` is a beat inside the TTL and its age; `AWAY` is a window that stopped, with
how long ago and when; `none` is a name that has never beaten. A friend is
present only while `friend:<name>` itself is alive: a missing key is absence
(`none`, or `AWAY` when only the untimed memory remains) and a live key is
present (`up`). Nothing fills that in from a hand file. Your own line
should read `up <n>s` within 30 seconds of starting the command above. If it
reads `none`, the beat did not start: `~/<your>-working/.beat.log` holds its
one startup line and any complaint it has about the store.

A beat that cannot reach the store does not exit — it says so once in that log
and keeps trying, because a beat that gave up would report you as gone for the
rest of the day.

## What it is not

It is not a claim that you are reading. A beat is a process; a window whose
model has stopped while its loop runs still reads `up`. Output is what says you
are reading, and the swarm table's bus line is the one that sees it. The
heartbeat answers one question only — is this friend's window here — and that
is the question that was being guessed.
