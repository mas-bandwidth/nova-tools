# nova-sprint dogfood — opencode, 2026-10-06

Read as a stranger: only `nova-sprint -h`, `nova-sprint help`, `nova-sprint <verb> -h`,
and the tool's pages under `docs/`. Built from the checkout at `00841a91c` and used as
`nova-sprint v1.0.1-0.20261007210058-00841a91cd63 linux/amd64 go1.26.6` on a Linux bench (`<bench>`).
Every verb ran at least once against `mem:test.twin`, the in-memory twin.
No live store and no server were touched.

## Findings

1. `nova-sprint init --readers reader-a,reader-b --members m1:8`
   Printed:
   ```
   nova-sprint init REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   ```
   I expected the documented first run (mem:<file> twin) to admit init, as help says "trying it without a Redis". Instead init is refused for want of a push proof.
   Grade: URGENT.

2. `nova-sprint start`
   Printed:
   ```
   nova-sprint start REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   ```
   I expected `start` to set the machine RUNNING on a twin, as help says the twin is for learning and tests. Instead it is refused the same way as init.
   Grade: URGENT.

3. `nova-sprint stats`
   Printed:
   ```
   stages              | seconds (median max n)
   --------------------+-----------------------
   deal wait           |     5.0     5.0 n=1
   finish to two reads |     2.0     2.0 n=1
   accept to land      |     0.0     0.0 n=1
   total               |    10.0    10.0 n=1
   ...
   STATS OK epoch=0 primaries=1 members=1 readers=1 routes=1
   ```
   I expected the stats output to clearly label what each column measures. The stats are informative but the format is dense for a first-time user.
   Grade: NEXT.

## What held

`where`, `log`, `version`, `help`, `check`, `card`, `rules`, `bases`, `tick`, `stats` ran clean against the twin.

READ 7/10 — the help is comprehensive and every verb has `-h` with flags and examples, but the documented first run (init, add, start) fails at init with no remedy on a twin, and start fails the same way.

USE 3/10 — `where` and `log` and `stats` ran clean, but every write verb (init, add, start, seat install) is refused by the push proof gate on a twin, so the documented first run cannot be exercised as a user.

urgent=2 next=1
