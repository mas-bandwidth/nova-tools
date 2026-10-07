# v1.0.0 acceptance: the fleet's script and functional cards

- Requirement: script and functional cards pass on every member, the functional tier in containers, no environment failure family for 2 hours
- Release: v1.0.0
- Measured: 2026-10-07T03:26:54Z
- From: the live sprint server at 127.0.0.1:6390 and the live dashboard at 127.0.0.1:7390, read on the machine that holds the card, with the installed nova-sprint v1.0.1-0.20261007022938-d7db23596a95 darwin/arm64 go1.27.1
- Tool: nova-sprint v1.0.1-0.20261007022938-d7db23596a95 (where --json, where --json --cards, where --json --cards --rows, card, log --json, stats --json), curl, jq, date -u
- Verdict: NOT MET
- Card verdict: HOLD

PASS line (verbatim): "script and functional cards pass on every member, the functional tier in containers, no environment failure family for 2 hours."

Window: not opened. Window start (attempted): 2026-10-07T03:22:50Z, the instant the live sprint dealt `accept-fleet-cards-b.w4` and its member took it. Window end: none; no sample loop was run, because the run that holds the card cannot carry the 2-hour window the PASS line needs. A 2-hour window started at the take would have ended at 2026-10-07T05:22:50Z, the card's own working deadline.

Build measured: `nova-sprint v1.0.1-0.20261007022938-d7db23596a95 darwin/arm64 go1.27.1`, the installed binary's own `nova-sprint --version`, read against the live sprint server at `127.0.0.1:6390` and the live dashboard at `127.0.0.1:7390`.

## Method

What this card measures is a live window, not a unit test: "WINDOW: 2 hours, measured from the first sample, start and end recorded as UTC from `date -u`." The measurement is a sampling loop: every 5 minutes for 2 hours, read `nova-sprint log --json --since <window start>` and `nova-sprint where --json --cards`, and count per member, separately for script and functional cards, dealt, finished ok, failed, and each failure's family; then confirm each functional card's container run. PASS needs, on every up member, at least one script card and one functional card finished ok in the window, no environment-family failure, and no member with zero cards finished.

The blocker, precisely. The live sprint store's own row for the card (`nova-sprint where --json --cards`) reads:

`{"branch":"sprint/accept-fleet-cards-b.w4.g1.e15","deadline":"2026-10-07T05:22:50Z","id":"accept-fleet-cards-b.w4","member":"friend.zhi","primary":"accept-fleet-cards-b","since":"2026-10-07T03:22:50Z","state":"working","stream":"sprint-next"}`

and the primary's own row (`nova-sprint where --json --cards --rows`) reads `tier=pro`, `tier_now=flash`, `rule_tier=pro`, `failure_tier=pro`, `grade=pro p=0.870`. Two independent bounds sit inside that, and the 2-hour window alone consumes both:

1. The friend's lane cap. A lane's card is capped by its tier (`internal/friend/lane_cap.go`, `DefaultLaneCaps`): flash 15 minutes, pro 45 minutes, heavy 90 minutes, frontier 150 minutes. The sprint records this card's tier as `pro`, whose cap is 45 minutes; its last drawn tier is `flash`, whose cap is 15 minutes. When the cap is reached the daemon signals the card's process group and the card's end is a HOLD naming `capped at <cap> (tier <t>, overrun <d>)`. Either cap ends the run at a third of the window or less.
2. The sprint's working deadline. A work card taken is late at `DeadlineUnfinished = 2 * time.Hour` from its take (`internal/sprint/steps_tick.go`), here 2026-10-07T05:22:50Z, exactly 2 hours after the card's `since`.

The 2-hour window alone consumes the deadline (its first sample is its start, its last is 2 hours later), leaving nothing for the raw samples, the record, the commit and the push; and the lane cap ends the lane well before half the window. No window was opened, so no criterion was measured, and neither a PASS nor a failed-window FAIL is truthful.

Every command run. All were run from the job directory `jobs/accept-fleet-cards-b.w4~15`, one at a time, read-only, with each sprint verb as `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`; every raw output was filtered with `grep -v SECRETS` before it was kept. The clone and branch step came first: `git clone -q https://github.com/mas-bandwidth/nova-tools.git repo`, then the STATUS branch `sprint/accept-fleet-cards-b.w4.g1.e15` cut at origin's `sprint/mechanical-2026-10-02` tip `e8f70f600ebf10b3beb60862508ce6019bb6ac7b`.

1. `nova-sprint --version` -> `nova-sprint v1.0.1-0.20261007022938-d7db23596a95 darwin/arm64 go1.27.1`.
2. `date -u` -> `Wed Oct  7 03:26:54 UTC 2026`.
3. `nova-sprint where --json` -> the fleet table recorded below; the read succeeded.
4. `nova-sprint where --json --cards` -> 79 dealt card rows including this card's own row above; the read succeeded.
5. `nova-sprint where --json --cards --rows` -> the primary's fields, including its tier; the read succeeded.
6. `nova-sprint card accept-fleet-cards-b` -> the primary's "now" line: `member friend.zhi holds accept-fleet-cards-b.w4@1 (working), 4m4s of 2h0m0s running`; the read succeeded.
7. `nova-sprint log --json --since 2026-10-07T03:22:50Z` -> the window log's first page; the read succeeded.
8. `nova-sprint stats --json` -> the epoch's numbers (epoch 15, 2948 primaries); the read succeeded.
9. `curl -s -m 5 http://127.0.0.1:7390/api/sprint` -> HTTP 200; the read succeeded.
10. `nova-bus log --max 0` -> refused: `LOG REFUSED: --redis is required: NOVA_BUS_REDIS is unset ...; refusing to guess; run: nova-bus help`. The bus is not readable from this shell; it is not one of the two sources the window loop names.

## Results

No window was opened, so no criterion was measured. The counts below are what the window would have had to produce, not values. Samples taken: 0 of the 25 the window calls for (every 5 minutes for 2 hours). Samples passing: 0. Worst value: none, because no window was opened.

| # | Criterion (PASS line) | Bar | Measured | Samples taken | Samples passing | Worst value and when |
|---|-----------------------|-----|----------|---------------|-----------------|----------------------|
| 1 | script cards finished ok, every up member | at least 1 per member in the window | not measured | 0 of 25 | 0 | none; window not opened |
| 2 | functional cards finished ok, every up member | at least 1 per member in the window | not measured | 0 of 25 | 0 | none; window not opened |
| 3 | the functional tier ran in containers | every functional card finished in the window shows its container run | not measured | 0 | 0 | none; no finished functional card was read |
| 4 | no environment failure family | zero for 2 hours | not measured | 0 | 0 | none; no window |
| 5 | no member with zero cards finished | every up member finished at least 1 | not measured | 0 | 0 | none; no window |

The fleet rows the window would have measured, from `nova-sprint where --json` `tables.fleet` at 2026-10-07T03:23:23Z: up and so to be measured -- `batman` (done=196 ok=115 failed=81), `hetzner` (done=842 ok=545 failed=297), `space` (done=1295 ok=830 failed=465), `superman` (done=185 ok=45 failed=140), `vision` (done=531 ok=355 failed=176). Down and so not measured: `captain` (status=down, done=0), `hulk` (status=down, done=0). Held: `studio` (status=held, done=274). Those rows are one instant, not the window, and decide nothing.

## Raw

The deciding lines are the two bounds. They are kept in full, as the tools printed them.

The live card's own placed deadline, from `nova-sprint where --json --cards`:

```
{"branch":"sprint/accept-fleet-cards-b.w4.g1.e15","deadline":"2026-10-07T05:22:50Z","id":"accept-fleet-cards-b.w4","member":"friend.zhi","primary":"accept-fleet-cards-b","since":"2026-10-07T03:22:50Z","state":"working","stream":"sprint-next"}
```

The live card's own "now" line, from `nova-sprint card accept-fleet-cards-b`:

```
  member friend.zhi holds accept-fleet-cards-b.w4@1 (working), 4m4s of 2h0m0s running
```

The primary's tier, from `nova-sprint where --json --cards --rows`:

```
{"id":"accept-fleet-cards-b","state":"working","stream":"sprint-next","f":{"tier":"pro","tier_now":"flash","rule_tier":"pro","failure_tier":"pro","work":"accept-fleet-cards-b.w4","head":"f0039f21001a0de55638cc1b39074e0d40280eb1","grade":"pro p=0.870 op=accept-fleet-cards-b@grade.2b625b1387e9","taken_at":"2026-10-07T03:10:14Z","routes":"flash-glm53-openrouter","rule_answer":"failed: rework on a tier up at 2026-10-07T03:05:58Z"}}
```

The tier cap, `internal/friend/lane_cap.go` at the base `e8f70f600ebf10b3beb60862508ce6019bb6ac7b`:

```
"flash":    15 * time.Minute,
"pro":      45 * time.Minute,
"heavy":    90 * time.Minute,
"frontier": 150 * time.Minute,
```

The sprint's working deadline, `internal/sprint/steps_tick.go`:

```
DeadlineUnfinished = 2 * time.Hour    // a work card taken and not finished
```

The bus, refused:

```
LOG REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help
```

There are no passing sample lines. The first and last passing sample: none.

## What was not measured

Everything the PASS line asks: no window start or end from a real 2-hour window; no per-member script-card dealt/ok/failed count; no per-member functional-card dealt/ok/failed count; no failure's family; no functional card's container run; no 2-hour continuity of any member; no member's zero-card check. The read sources that would feed the window (items 3 through 9 above) answered, so the block is the run's own bounds, not access to the live system. The bus log was additionally unreadable from this shell; it is not one of the window's named sources.

The repository's own acceptance-record gate (`go test -count=1 -timeout 600s ./internal/ci -run TestAcceptanceRecordsAreWellFormed`) is a real check over every file under `docs/acceptance/*/*.md`, though this card's DONE-WHEN calls it a placeholder and says it is not run by the card. The record above is written in the gate's shape (title line, field list, `## Method`, `## Results`, `## Raw` with a fenced raw block) and carries `Verdict: NOT MET`, so the gate stays green while the card's own `Verdict: HOLD` is kept beside it; the card's literal format (`Verdict: PASS` as the first line) would have failed that gate. The gate's exact line is in the report beside this record.
