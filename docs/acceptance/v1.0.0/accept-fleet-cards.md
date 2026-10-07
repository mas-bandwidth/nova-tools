Verdict: HOLD

# nova-sprint v1.0.0 acceptance, check 2 of 6: the fleet's script and functional cards

PASS line (verbatim): "script and functional cards pass on every member, the functional tier in containers, no environment failure family for 2 hours."

Window: not opened. The measured window the PASS line needs is 2 hours ("WINDOW: 2 hours,
measured from the first sample"); the run that holds this card cannot carry a 2-hour window,
so the window was never started and no criterion is measured. Window start (attempted):
2026-10-07T03:10:14Z, the instant the live sprint dealt and the member took attempt 3. Window
end: none; no sample loop was run, because a loop that cannot reach its end is not a window.

Build measured: `nova-sprint v1.0.1-0.20261007022938-d7db23596a95 darwin/arm64 go1.27.1`
(the installed binary's own `nova-sprint --version`), read on the Studio, against the live
sprint server at `127.0.0.1:6390` and the live dashboard at `127.0.0.1:7390`.

The blocker, precisely. This card is held by `friend.zhi` on the Studio. The live sprint
store's own row for it (`nova-sprint where --json --cards`) reads
`{"id":"accept-fleet-cards-b.w3","member":"friend.zhi","since":"2026-10-07T03:10:14Z","deadline":"2026-10-07T05:10:14Z","state":"working"}`:
the card's working deadline is 2 hours from its take, not the brief's 180 minutes. Two
independent bounds then sit inside that:

1. The friend's lane cap. A lane's card is capped by its tier
   (`internal/friend/lane_cap.go`, `DefaultLaneCaps`), and this card's tier is `pro`, whose
   cap is 45 minutes. This is the first hard end: the daemon ends the turn at the cap
   (`capped at 45m0s (tier pro, overrun ...)`) and writes a HOLD.
2. The sprint's working deadline. A work card taken is late at `DeadlineUnfinished = 2h`
   from `first_taken` (`internal/sprint/steps_tick.go`), here 2026-10-07T05:10:14Z.

The 2-hour window alone consumes both bounds (its first sample is its start and its last is
its end 2 hours later); nothing is left for the required raw samples, the record, the commit
and the push, and the pro lane cap ends the run at about 03:55:14Z, a third of the way in.
Attempt 2 of this card died of exactly this class of bound: its report reads
`deadline: no RESULT.md shape`, run wall 1206s. The brief's own line "(the window, 2 hours,
plus 60 minutes)" describes 180 minutes, but neither the friend lane cap nor the sprint's
working deadline grants it.

## Every command run

All were run from `/Volumes/nova/ai/zhi/working/jobs/accept-fleet-cards-b.w3~15`, one at a
time, read-only, with each verb as
`NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`;
every raw output was filtered with `grep -v SECRETS` before it was kept.

1. `nova-sprint --version` -> `nova-sprint v1.0.1-0.20261007022938-d7db23596a95 darwin/arm64 go1.27.1`.
2. `date -u` -> `Wed Oct  7 03:17:47 UTC 2026`.
3. `nova-sprint where --json` -> the fleet table below; the read succeeded.
4. `nova-sprint stats --json` -> the fleet's per-member work rows; the read succeeded.
5. `nova-sprint where --json --cards` -> the fleet table and 80 dealt card rows; the read
   succeeded, and its `accept-fleet-cards-b.w3` row is the deadline above.
6. `nova-sprint log --json --since 2026-10-07T03:01:57Z` -> 775 log lines; the read succeeded.
7. `nova-sprint card accept-fleet-cards-b` -> the live card's own "now" line and timeline,
   quoted below.
8. `curl -s -m 5 http://127.0.0.1:7390/api/sprint` -> HTTP 200 with the dashboard's JSON.
9. `nova-bus log --max 0` -> refused: `--redis is required: NOVA_BUS_REDIS is unset ...`.
   The bus was not readable from this shell; it is not one of the two sources the window
   loop names (those are 5 and 6).

The clone and branch step was done: cloned `https://github.com/mas-bandwidth/nova-tools.git`
into `jobs/accept-fleet-cards-b.w3~15/repo`, fetched `sprint/mechanical-2026-10-02`, and
created `sprint/accept-fleet-cards-b.w3.g1.e15` at origin's tip
`dbd413b5da1f113a7be84c02b26c309ccc0fe972`.

## Per criterion: measured value against its bar

No window was opened, so no criterion was measured. The counts below are what the window
would have had to produce, not values.

| # | Criterion | Bar | Measured | Samples taken | Samples passing | Worst value and when |
|---|-----------|-----|----------|---------------|-----------------|----------------------|
| 1 | script cards finished ok, every up member | >= 1 per member in window | not measured | 0 of 25 (every 5 min for 2 h) | 0 | none; window not opened |
| 2 | functional cards finished ok, every up member | >= 1 per member in window | not measured | 0 of 25 | 0 | none; window not opened |
| 3 | functional tier ran in containers | every functional card finished in window shows its container run | not measured | 0 | 0 | none; no finished functional card was read |
| 4 | no environment failure family | zero for 2 h | not measured | 0 | 0 | none; no window |
| 5 | no member with zero cards finished | every up member finished >= 1 | not measured | 0 | 0 | none; no window |

The fleet rows that were to be measured, from `nova-sprint where --json` `tables.fleet` at
2026-10-07T03:11:39Z: up and so to be measured -- `batman` (done=196 ok=115 failed=81),
`hetzner` (done=842 ok=545 failed=297), `space` (done=1295 ok=830 failed=465), `superman`
(done=184 ok=44 failed=140), `vision` (done=531 ok=355 failed=176). Down and so not measured:
`captain` (status=down, done=0), `hulk` (status=down, done=0). Held: `studio` (status=held,
done=274). Those rows are one instant, not the window, and decide nothing.

## Raw sample lines that decide the HOLD

The deciding lines are the two bounds. They are kept in full.

- The live card's own placed deadline, from `nova-sprint where --json --cards`:
  `{"branch":"sprint/accept-fleet-cards-b.w3.g1.e15","deadline":"2026-10-07T05:10:14Z","id":"accept-fleet-cards-b.w3","member":"friend.zhi","primary":"accept-fleet-cards-b","since":"2026-10-07T03:10:14Z","state":"working","stream":"sprint-next"}`
- The live card's own "now" line, from `nova-sprint card accept-fleet-cards-b`:
  `member friend.zhi holds accept-fleet-cards-b.w3@1 (working), 7m34s of 2h0m0s running`
- The tier cap, `internal/friend/lane_cap.go` at the base
  `dbd413b5da1f113a7be84c02b26c309ccc0fe972`:
  `"flash": 15 * time.Minute,` / `"pro": 45 * time.Minute,` / `"heavy": 90 * time.Minute,`
  / `"frontier": 150 * time.Minute,`
- The sprint's working deadline, `internal/sprint/steps_tick.go`:
  `DeadlineUnfinished = 2 * time.Hour    // a work card taken and not finished`
- Attempt 2's end, from the live card's timeline:
  `deadline: no RESULT.md shape; stage: staged=95c684a6be6c ... native refused ...`
- The bus, refused:
  `LOG REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row ... cannot be read either; refusing to guess; run: nova-bus help`

There are no passing sample lines. The first and last passing sample: none.

## What was not measured

Everything the PASS line asks: no window start or end from a real 2-hour window; no
per-member script-card dealt/ok/failed count; no per-member functional-card dealt/ok/failed
count; no failure's family; no functional card's container run; no 2-hour continuity of any
member; no member's zero-card check. The reads that would feed the window (items 5 and 6
above) answered, so the block is the run's bounds, not access to the live system. The bus
log was additionally unreadable from this shell; it is not one of the window's named sources.

## What was done

- Read the card, the checkout, and the live system.
- Cloned `mas-bandwidth/nova-tools` into the job directory and cut the STATUS branch from
  origin's `sprint/mechanical-2026-10-02` tip `dbd413b5`.
- Ran every read source the card names, one at a time, and kept the raw outputs in
  `scratch/` under the job directory.
- Wrote this HOLD record and committed it on the card's branch, so the blocker is the
  evidence and the sentinel's need stays unmet.
