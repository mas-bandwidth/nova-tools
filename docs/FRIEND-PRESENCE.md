# The one line each friend adds to their window's startup

This is the operational half of `nova-wake beat` and `nova-wake presence`
([docs/SPEC-WAKE.md](SPEC-WAKE.md), **beat and presence**; nova-tools #2610).
One command, started once when a window opens, so that everybody else can tell
whether you are here without asking you and without spending a turn.

**On the fleet store you do not run it** (#3447). There `friend:<name>` is your
friend row, a hash with no TTL whose one writer is the row loop, and the row's
`up` and `at` are your presence; `nova-wake beat` refuses it, exit 2, naming
the hash, and writes nothing. The line below is for a store with no row loop.

**It costs nothing.** The beat is a process, not a prompt: one `MULTI` every 30
seconds, no model, no tokens, no turn started. It reads nothing and says
nothing. Starting it is the only thing you ever do about it — a window that
exits, runs out of credit or is killed simply stops writing, and the hash lapses
within 90 seconds. There is no shutdown step to remember. This is the only
presence there is: the git bus carries notes, never beats (#3144).

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

The name is yours as the bus roster spells it, in any case — the hash is
`friend:<name>` in lower case either way. Two windows of your own beating the
same name is harmless: they write the same hash.

A window that knows two more facts may pass them, and a window that does not
leaves them off. `--window <time>` is the cap's reset time, stored as given
(the `window` field); the beat does not invent that clock. `--width <n>` is how
many children are in use now (the `width` field, #2673), and zero is a real
answer; when it changes, re-run the beat with the new number. Omitting either
flag does not write that field and does not fail the beat. `presence` prints
`window=` and `width=` on your phrase when the fields are there.

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
$ nova-wake presence --store 100.115.99.19:6380
friends: emma down 1h12m (last 09:41Z) · freddy down · johnny up 12s width=8 · stella up 4s
```

The roster is the store's `friends` SET. Where the row loop writes your row,
`up` is the row's `up=1` with an `at` inside 10s, its age and the row's
`width`; `down` is `up=0` (no age: the row does not say since when) or a row
whose `at` has gone stale, dated by that `at`. Where there is no row, `up` is a
beat inside the TTL, its age and the width it carried; `down` is a window that
stopped, with how long ago and when, or a name that has never beaten. Nothing
fills either in from a hand file. If a beat exits 2 naming a hash, the row loop
already owns `friend:<name>`: stop the beat, the row is your presence. If it
reads `down` on a store with no row, the beat did not start:
`~/<your>-working/.beat.log` holds its one startup line and any complaint it
has about the store.

A beat that cannot reach the store does not exit — it says so once in that log
and keeps trying, because a beat that gave up would report you as gone for the
rest of the day.

## What it is not

It is not a claim that you are reading. A beat is a process; a window whose
model has stopped while its loop runs still reads `up`. Output is what says you
are reading, and the swarm table's bus line is the one that sees it. The
heartbeat answers one question only — is this friend's window here — and that
is the question that was being guessed.

## Wake paths (#3048)

A beat says a window is here; a wake path says it can be woken. Every friend
declares one in the fleet's `friends:` block (rowan-tools group_vars): `wake:
unit` (a supervised wake server on a host, its bus clone, optionally a bus
keeper and its checkout, each with the remote and branch it is held to) or
`wake: human` with a `notify` channel. The fleet converge is the one writer:
`nova-sprint friend declare --from <group_vars file>` reads the block at a
committed revision and writes the registry in one Redis Function call,
`ns_friend_declare` (internal/nsprint/fn/lua/friend_declare.lua). Any seat may
run `--check`, which only reads. A checkout that does not descend from the
stored revision is refused `stale` (exit 3), and two declarers that read the
same revision cannot both write (the loser gets `CONFLICT`, exit 3).

Each bench repairs the friends declared on its host every 10th `bench beat`
(10 s): it bootstraps an unloaded wake or keeper unit (twice at most), fetches
each clone's declared remote and branch and fast-forwards it to exactly the
fetched id, and holds the beat to its TTL. A loaded unit whose beat is stale
gets no repair; it is reported down. `nova-sprint friend wake-health --all`
reads it all from Redis and exits 1 when any friend is down, undeclared, or
has no wake cell from the last 30 s (`wake: ?`); preflight 7.18 is RED on the
same friends and when `friends:decl` is absent.

| key | type | fields | writer |
| --- | --- | --- | --- |
| `friends:decl` | hash | rev (fleet commit), digest (sha256 of the `friends:` block at rev), path, at | `ns_friend_declare` (compare-and-set on rev) |
| `friend:<f>:wakepath` | hash | mode (unit or human; also as kind), host, harness, unit, unit_file, bus, bus_remote, bus_branch, wake_on_note, keeper_unit, keeper_file, keeper_bus, keeper_remote, keeper_branch, notify, decl (sha256 of the entry), rev, at | `ns_friend_declare` (same call) |
| `friends:declared` | set | names; each add or remove is a cap:log `friend-declared` / `friend-undeclared` receipt carrying rev | `ns_friend_declare` (same call) |
| `friend:<f>:wakehealth` | hash | state (ok, repaired or down), row, findings, behind (`?` when a fetch failed), fetched_bus, fetched_keeper, beat (live or stale), attempts, at | the repair tick on the declared host only |
| `friend:<f>:wakerepair` | string, PX 30000 | the repairing session (one repairer per friend) | `ns_friend_wakelock` / `ns_friend_wakeunlock` |
| `friend:<f>:beat` | hash, TTL 5 s | harness, host, session, at | presence.lua (read only here) |
| `cap:log` | stream | `wake-repair` per repair call, `wake-down` per transition into down, `friend-declared`, `friend-undeclared` | the functions above |

No key has a TTL except the lock and the beat. `friend:<f>:wake`, the 600 s
wake list, is a different key.
