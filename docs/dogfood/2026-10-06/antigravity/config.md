# nova-config dogfood, 2026-10-06 (antigravity)

This replaces attempt 2's file, which was a copy of another friend's report.
Every command below was run as printed against this checkout's own binary,
`nova-config v1.0.1-0.20261007153343-85fecb457dd2 linux/amd64 go1.26.6`, built
from tip `85fecb457dd2` of `sprint/mechanical-2026-10-02`. Read cold: only
`nova-config -h`, `nova-config help`, `nova-config <verb> -h` and
`docs/nova-config/README.md`. Every verb ran at least once against a `--file`
scratch store in a scratch directory; `apply`, `status` and `inventory` also ran
against a throwaway Redis on the loopback; the refusals were run too. No code
was changed.

The two headline findings the copied file carried do not reproduce at this tip.
`machine width m1` on a row whose width is 6 prints `CONFIG WIDTH machine=m1
width=6 member=true` at exit 0. A `--file` store keeps its rows and its history
across runs: `machine add` wrote rev=1, `machine set` rev=2, and a later
`machine history` from a new process still printed both rows. What follows is
what was observed.

## Findings

1. **A full `apply` writes some kinds, then refuses on the friend kind, and the
   actor the help's own examples use cannot apply one — URGENT**

   Command as typed:

       nova-config apply --file try5.json --redis 127.0.0.1:6391 --as a1

   Printed (first 3 lines):

       APPLY ADD kind=machine name=m1
       CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=1 ms=2
       APPLY SET kind=fleet name=fleet changed=store,coordinator,redis_port,pg_dsn,loops_dir

   and then, after the machine and fleet rows were already written to Redis:

       nova-config apply REFUSED: roles of lead: --as a1 is not a registered friend; apply as a friend that holds the coordinator role; run: nova-config apply --dry-run

   exit 1. A following `status` printed:

       nova-config status REFUSED: Redis is not at the store's revision for 2 kind(s); run: nova-config apply --file try5.json

   The same file applied end to end at exit 0 when the recorded actor was the
   sprint row's coordinator (`apply --as lead`).

   Expected: `--as a1` is the actor `nova-config help`'s first-run block and the
   README's first run use for every write, and the README says `--as` is "who is
   making the change; every write records it". A stranger expects a full `apply`
   either to deliver every kind or to refuse before it has delivered any.
   Instead the `friend` kind requires the actor to be a registered friend that is
   also the sprint row's coordinator — a rule no help page states — and the tool
   discovers it only after machine and fleet rows are already in Redis, leaving
   the applied copy half-written. As the store grew, `--as a1` refused with two
   different messages ("is not a registered friend", then "does not hold the
   coordinator role"), and only the coordinator's name completed the run.

2. **`tier set --routes ''` silently clears the tier instead of taking its
   enabled routes — URGENT**

   Commands as typed:

       nova-config route add rr --tier flash --provider p --model m --deadline 60 --as a1 --file try3.json
       nova-config tier set flash --routes '' --as a1 --file try3.json
       nova-config tier show flash --file try3.json

   Printed:

       CONFIG ADD kind=route name=rr rev=1
       CONFIG SET kind=tier name=flash rev=2 changed=routes
       TIER name=flash routes=- created=2023-11-14T22:13:20Z updated=2026-10-07T15:38:42Z

   Route `rr` was an enabled route of tier `flash` at that moment, so the tier
   was cleared rather than filled.

   Expected: `nova-config tier set -h` says of `--routes`, "empty takes the
   tier's enabled routes in name order". A stranger setting a tier with an empty
   list expects `routes=rr`; instead the tier ends empty and a deal over that
   tier has no route. Omitting the flag is refused ("set names no field"), and
   `--routes default` and `--routes all` are read as route names and refused, so
   the behaviour the help names is unreachable.

3. **`apply`'s own printed example refuses: the required `--as` is not in it —
   NEXT**

   Command as typed (the `example:` line of `nova-config apply -h`):

       nova-config apply --dry-run --redis 127.0.0.1:6379 --file try.json

   Printed:

       nova-config apply REFUSED: --as is required: the name the write is recorded under (or NOVA_FRIEND); run: nova-config apply -h

   exit 2.

   Expected: the usage line brackets `--as` as optional and the example omits it,
   but the verb refuses without it. The example should carry `--as a1`, or the
   help should say `--as` is required.

4. **A note containing a blank prints as `\x20` — NEXT**

   Command as typed:

       nova-config machine set m1 --note 'hello world' --as a1 --file try2.json

   Printed:

       CONFIG SET kind=machine name=m1 rev=4 changed=note

   and `nova-config machine show m1 --file try2.json` printed:

       MACHINE name=m1 user=nova seat=s1 slots=8 runners=0 width=- tla=false note=hello\x20world created=2026-10-07T15:38:42Z updated=2026-10-07T15:38:42Z loops=-

   The stored value is right (`"note": "hello world"` in the file), but the line
   and the history render the blank as `\x20`, so a note with two words is
   unreadable in `list`, `show` and `history`. Expected `note=hello world`.

5. **`--every` and `--deadline` want bare integers, but the refusals do not say
   the unit — NEXT**

   Command as typed:

       nova-config loop add reader-m1 --machine m1 --argv '["nova-swarm","member"]' --every 30s --as a1 --file try2.json

   Printed:

       nova-config loop add REFUSED: --every "30s": want a non-negative integer; run: nova-config loop add -h

   exit 2. `nova-config route add r --tier flash --provider p --model m --deadline 30m --as a1 --file try2.json`
   refuses the same way.

   Expected: `loop add -h` says "seconds between runs" and `route add -h` says
   "the seconds a card on this route may run", so a stranger types a duration.
   The refusal should name the unit it wants ("want seconds as a whole number"),
   or the flag should take a Go duration.

6. **The same unset width prints as `default` from `machine width` and `-` from
   `machine show` — NEXT**

   Commands as typed:

       nova-config machine add m2 --user nova --seat s2 --slots 4 --as a1 --file try2.json
       nova-config machine width m2 --file try2.json
       nova-config machine show m2 --file try2.json

   Printed:

       CONFIG WIDTH machine=m2 width=default member=true
       MACHINE name=m2 user=nova seat=s2 slots=4 runners=0 width=- tla=false note=- created=2026-10-07T15:38:42Z updated=2026-10-07T15:38:42Z loops=-

   Expected: one rendering for one value. `machine width m2 --json` reports
   `"width":0,"default":true`, so the row really is the default width; `show`
   printing `width=-` reads as "unset", which is how an unset field prints
   elsewhere.

## What the tool got right

- The first-run block in `nova-config help` runs as printed: `migrate --file`,
  `machine add`, `machine set`, `machine list` and `machine history` all answered
  0, and the file carried the rows and history forward.
- Every verb answered `-h` at exit 0 and touched nothing; `help <verb>` prints
  the same page.
- Refusals name every missing input in one run and say what each wants:
  `machine add m9 --user nova --file try.json` names `--as`, `--seat` and
  `--slots` in one line; a `--pg` carrying a password, `--pg` with `--file`, an
  unknown kind and an unknown verb each refuse with a remedy at exit 2.
- Store refusals are actionable: a duplicate `add` names `set`, `set` on a
  missing row names `add`, `remove` on a missing row names `list`.
- `--dry-run` writes nothing and says so (`CONFIG DRY-RUN ... wrote=nothing`),
  `--json` renders the same value as the lines, and `migrate` is idempotent
  (`from=35 to=35 applied=0`).
- `apply` against a throwaway loopback Redis delivered machine, fleet, friend,
  sprint, loop, route and tier when the actor was the coordinator;
  `inventory --fixture`, `inventory --host` and `inventory --list` printed the
  applied view, and `status` agreed with it.

READ 7/10 — the banner answers what the tool does, how it works and where its
state lives, and every verb's `-h` is complete; it is held off 8 by the `apply`
example that does not run and by `tier set` naming a behaviour `--routes ''`
does not have.

USE 6/10 — the `--file` store makes the whole tool runnable cold and the writing
verbs are honest; it is held down by `apply`, where the examples' actor cannot
deliver a friend row and a refused full apply leaves the applied copy
half-written.

urgent=2 next=4
