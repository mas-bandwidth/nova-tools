# v1.0.0 acceptance: the fleet's script and functional cards pass on every member for two hours, the functional tier in containers

- Requirement: script and functional cards pass on every member, the functional tier in containers, no environment failure family for 2 hours
- Release: v1.0.0
- Measured: 2026-10-07T13:35:29Z
- From: the live sprint on the Studio, read with `nova-sprint` (server 127.0.0.1:6390, actor zhi), the dashboard at 127.0.0.1:7390 and `nova-bus`
- Tool: nova-sprint v1.0.1-0.20261007022938-d7db23596a95 darwin/arm64 go1.27.1; curl; jq
- Verdict: NOT MET
- Card verdict: HOLD
- Window start: none; the window was never opened
- Window end: none; the window was never opened
- Blocker: the card's PASS line needs a 2-hour measured window, and this card's lane cannot carry one. The live packet deals the card at tier `pro` (`nova-sprint friend cards zhi --json`, field `tier`, and `nova-sprint card accept-fleet-cards-b --json`, field `tier`), and a pro lane is capped at 45 minutes (`internal/friend/lane_cap.go`, `DefaultLaneCaps`: flash 15m, pro 45m, heavy 90m, frontier 150m at the base tip), with no `row_lane_caps` override in the live beat (`grep -c row_lane_caps` is 0 in `where --json --cards`, and the installed server binary carries none: `strings /Users/glenn/.local/bin/nova-sprint | grep -c row_lane_caps` -> 0). The daemon ends the lane when the card's wall reaches its cap and signals the turn's process group (`internal/friend/lanes.go` `capWatch`, `ln.cap = d.laneCap(ln.tier)`), so the run is ended about 45 minutes into a 2-hour window. The card's own live working deadline is 2 hours from its take (`since` 2026-10-07T13:33:09Z, `deadline` 2026-10-07T15:33:09Z), so even without the lane cap the window alone consumes the deadline. No sample was taken (0 of 25) and no criterion was measured, so neither a `Verdict: PASS` nor a failed-window `Verdict: FAIL` is truthful.

The verbatim PASS line this check is held to, from the card:

```
script and functional cards pass on every member, the functional tier in containers,
no environment failure family for 2 hours.
```

The card is a measured check on the live system over a stated window, not a unit test: its result is the evidence. The window is 2 hours, measured from the first sample. With the lane ended at its tier's cap before the window's end, there is no 2-hour window to report.

## Method

What the card asks. Every member is a fleet row of `nova-sprint where --json` (`tables.fleet`) whose status is up at the window's start. For 2 hours, `nova-sprint log --json --since <window start>` and `nova-sprint where --json --cards` are read every 5 minutes (25 samples over the window), and per member, separately for script cards and functional cards, the counts of dealt, finished ok, and failed are taken, with each failure's family as the log names it. For every functional card finished in the window, the log or the card shows its container run; a functional card run outside a container fails the check. PASS needs, on every member, at least one script card and one functional card finished ok in the window, no failure of an environment family, and no member with zero cards finished.

What was done here. The declared read sources were opened and answered, one at a time, as the card directs (`NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=zhi timeout 300 nova-sprint <verb>`). The card's live row, its packet tier, the fleet table at the read, the version and `date -u` were recorded as the first evidence lines. The lane-cap bound was read in the repository at the base tip this branch starts from and checked against the live system: the card's tier is `pro`, the pro cap is 45m, and no `row_lane_caps` override reaches the daemon. The window's length is therefore longer than the lane that would carry it.

The sampling loop was not started. Starting it and stopping at the lane cap would not produce the window the PASS line names, and the cap would end the run before STEP 3 and STEP 4 could be written; the card says to report what was not done and to use HOLD with the precise blocker when the measurement could not be run.

Bounds, as measured. Live card row: `{"branch":"sprint/accept-fleet-cards-b.w6.g1.e15","deadline":"2026-10-07T15:33:09Z","id":"accept-fleet-cards-b.w6","member":"friend.zhi","primary":"accept-fleet-cards-b","since":"2026-10-07T13:33:09Z","state":"working","stream":"sprint-next"}` (from `nova-sprint where --json --cards`). Live packet: `{"card":"accept-fleet-cards-b.w6","tier":"pro","job":"accept-fleet-cards-b.w6~15","branch":"sprint/accept-fleet-cards-b.w6.g1.e15","attempt":6,"gen":1,"epoch":15,"col":"working"}` (from `nova-sprint friend cards zhi --json`). Tier cap in the tree at the base tip: `internal/friend/lane_cap.go` `DefaultLaneCaps` -> `"pro": 45 * time.Minute`.

Fleet at the read (`where --json`, `at` 2026-10-07T09:35:29-04:00 = 2026-10-07T13:35:29Z). Up and so the members the window would measure: batman, hetzner, space, superman, vision. Down and so not measured: captain, hulk. Held and not dealt: studio.

The read sources that answered: `nova-sprint --version`, `where --json`, `where --json --cards`, `friend cards zhi --json`, `card accept-fleet-cards-b --json`, `stats --json`, `log --json --since 2026-10-07T13:30:00Z`, and the dashboard `curl -s http://127.0.0.1:7390/api/sprint` (HTTP 200). `nova-bus log --max 0` refused for a missing `--redis` (`NOVA_BUS_REDIS` unset and no `NOVA_SPRINT_REDIS`), and `nova-bus2 log` does not exist on this host (the rename has not reached it); the bus was not a source for any criterion here because no window ran.

## Results

No sample was taken and no criterion was measured. The row is the bound that stopped the run, not a measurement of the fleet.

| Criterion | Bar | Measured | Met |
|---|---|---|---|
| Window | 2 hours, start and end as UTC | never opened: lane cap 45m (`pro`) and working deadline 2h from take | no |
| Samples | 25 over the window (one per 5 minutes) | 0 taken, 0 passing, worst value none | not measured |
| Per member, script cards: dealt / ok / failed | at least one ok on every member | not measured | not measured |
| Per member, functional cards: dealt / ok / failed | at least one ok on every member | not measured | not measured |
| Functional tier in containers | every functional card finished in the window ran in a container | not measured, no functional card finished in a window | not measured |
| Environment failure families | none for 2 hours | not measured | not measured |
| Members with zero cards finished | none | not measured; no member dealt in the window because no window ran | not measured |

What was not measured: the window's start and end, the 25 samples, every per-member script and functional count, every failure family, every container run, and the 2-hour continuity. No criterion of the PASS line has a measured value against its bar; the only measured facts are the two bounds, the card's tier, and that the declared read sources answered.

## Raw

Version and the read's UTC instant, the first lines of the evidence:

```
=== date -u ===
Wed Oct  7 13:35:29 UTC 2026
=== nova-sprint --version ===
nova-sprint v1.0.1-0.20261007022938-d7db23596a95 darwin/arm64 go1.27.1
```

The live card row and its packet tier:

```
$ NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=zhi nova-sprint where --json --cards
{"branch":"sprint/accept-fleet-cards-b.w6.g1.e15","deadline":"2026-10-07T15:33:09Z","id":"accept-fleet-cards-b.w6","member":"friend.zhi","primary":"accept-fleet-cards-b","since":"2026-10-07T13:33:09Z","state":"working","stream":"sprint-next"}

$ nova-sprint friend cards zhi --json
{"card": "accept-fleet-cards-b.w6", "tier": "pro", "job": "accept-fleet-cards-b.w6~15", "branch": "sprint/accept-fleet-cards-b.w6.g1.e15", "attempt": 6, "gen": 1, "epoch": 15, "col": "working"}

$ nova-sprint card accept-fleet-cards-b --json  (fields)
tier=pro  tier_now=flash  failure_tier=pro  attempt=6
```

The tier cap in the tree at the base tip `0b3f2f934f1bbe1b39d16cef2ef353cc754ee934`:

```
$ sed -n '24,30p' internal/friend/lane_cap.go
var DefaultLaneCaps = map[string]time.Duration{
	"flash":    15 * time.Minute,
	"pro":      45 * time.Minute,
	"heavy":    90 * time.Minute,
	"frontier": 150 * time.Minute,
}
```

No `row_lane_caps` override reaches the daemon:

```
$ grep -c row_lane_caps scratch/where.json scratch/where_cards.json
scratch/where.json:0
scratch/where_cards.json:0
$ strings /Users/glenn/.local/bin/nova-sprint | grep -c row_lane_caps
0
$ grep -rn row_lane_caps internal/nsprint internal/sprint
(no output)
```

The fleet at the read and the members the window would have measured:

```
$ nova-sprint where --json  (tables.fleet; at 2026-10-07T09:35:29-04:00 = 2026-10-07T13:35:29Z)
batman	up	ok=116	failed=81	done=197
captain	down	ok=0	failed=0	done=0
hetzner	up	ok=547	failed=298	done=845
hulk	down	ok=0	failed=0	done=0
space	up	ok=831	failed=467	done=1298
studio	held	ok=153	failed=121	done=274
superman	up	ok=45	failed=142	done=187
vision	up	ok=356	failed=178	done=534
```

The declared read sources, each with its exit:

```
$ nova-sprint where --json                       -> exit 0
$ nova-sprint where --json --cards               -> exit 0
$ nova-sprint friend cards zhi --json            -> exit 0
$ nova-sprint card accept-fleet-cards-b --json   -> exit 0
$ nova-sprint stats --json                       -> exit 0
$ nova-sprint log --json --since 2026-10-07T13:30:00Z -> exit 0
$ curl -s -o scratch/dash.json -w '%{http_code}' http://127.0.0.1:7390/api/sprint -> HTTP 200
$ nova-sprint log --json (first bytes)
{"lines":[{"kind":"happened","at":"2026-10-07T09:30:24.041898-04:00","epoch":15,"op":"tick-overdue-...
$ nova-sprint stats --json (first bytes)
{"epoch":15,"primaries":2948,"stages":{"deal_wait":{"median_s":810,"max_s":189301,"n":2480},...
```

The bus source, and its refusal:

```
$ NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=zhi timeout 60 nova-bus log --max 0
LOG REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help
(exit 2)
$ timeout 60 nova-bus2 log --max 0
timeout: exec(nova-bus2): No such file or directory
(exit 127)
```

No sampling loop was run, so there are no per-sample lines and no failure families to quote: the window was never opened.
