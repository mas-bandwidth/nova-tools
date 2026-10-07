# nova-sprint dogfood — opencode, 2026-10-06

Read as a stranger: only `nova-sprint -h`, `nova-sprint help`, `nova-sprint <verb> -h`,
and the tool's pages under `docs/`. Built from the checkout at `ad2f20c6b` and used as
`nova-sprint devel linux/amd64 go1.26.6` on a Linux bench (`<bench>`).
Every verb ran at least once against `mem:test.twin`, the in-memory twin.
No live store and no server were touched.

## Findings

1. `nova-sprint add --stream s1 --count 1 --one`
   Printed:
   ```
   nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   ```
   I expected the documented no-Redis first run to admit a card, as help says "trying it without a Redis". Instead the add is refused for want of a push proof. The remedy `seat install` refuses a twin too.
   Grade: URGENT.

2. `nova-sprint selftest`
   Printed:
   ```
   SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   ```
   I expected `selftest` to succeed (exit 0) on a twin. Instead it fails at its add step, the same gate as finding 1.
   Grade: URGENT.

3. `nova-sprint where`
   Printed:
   ```
   SPRINT TABLE  coordinator boss
   
   STOPPED
   
   work | waiting | ready | working | review | merging | landed | cost | per landed
   -----+---------+-------+---------+--------+---------+--------+------+-----------
        |       0 |     0 |       0 |      0 |       0 |      0 |    0 |
   ```
   I expected the work table to show the init'd members and readers. It does.
   Grade: NEXT.

4. `nova-sprint log`
   Printed:
   ```
   19:36:24  ctl-m1: m1 changed by boss: kind=member, score=0, since=2026-10-07T19:36:24Z, status=down
   LOG OK lines=1 of=1
   ```
   I expected the log to show the init and add attempts. It shows only the member change.
   Grade: NEXT.

## What the tool got right

`where`, `log`, and `version` ran clean against the twin.

READ 7/10 — the help is comprehensive and every verb has `-h` with flags and examples, but the documented first run (init, add, start) fails at add with no remedy on a twin, and selftest fails the same way.

USE 3/10 — `where` and `log` ran clean, but every write verb (add, start, tick, take, finish, land) is refused by the push proof gate on a twin, so the documented first run cannot be exercised as a user.

urgent=2 next=2
