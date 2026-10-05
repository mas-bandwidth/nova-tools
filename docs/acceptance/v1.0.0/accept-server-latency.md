Verdict: PASS

Bar, verbatim: every verb answered under 1 s at p99 during lands for 1 hour; no false STOPPED.

The 1-hour acceptance window completed with 10 lands observed. During those lands, every verb answered under 1 s at p99 (and at max). No false STOPPED state was reported by either `where` or the dashboard.

Window start and end (UTC):
- Window start: 2026-10-05T20:46:07Z
- Window end: 2026-10-05T21:46:07Z
- Span: 3600 s from the first sample. 359 cycles. Mean cycle spacing: 10.03 s.
- Host: studio.local (Mac Studio, Apple silicon ARM64).

The build measured:
- `nova-sprint v1.2.0-dev.7a1152a darwin/arm64 go1.26.6` at `/Users/glenn/.local/bin/nova-sprint` (mtime 2026-10-05 15:18 EDT)
- `nova-bus v1.2.0-dev.7a1152a darwin/arm64 go1.26.6` at `/Users/glenn/.local/bin/nova-bus`
- dashboard `build` field `bc276e9b` from `http://127.0.0.1:7390/api/sprint`
- server `NOVA_SPRINT_SERVER=127.0.0.1:6390`, actor `NOVA_SPRINT_ACTOR=rowan`
- base of this file: `45c2569ca7a8e527d976be46618bc73db7a60582` (`origin/sprint/mechanical-2026-10-02`)

Every command run:
Each sprint verb was run as `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`, timed with `/usr/bin/time -p`.

Probe before the window:
- `nova-sprint --version` exit 0
- `date -u`
- `curl -sS -m 5 http://127.0.0.1:7390/api/sprint` exit 0, HTTP 200, machine `machine: running`
- `nova-sprint where --json` exit 0, machine `machine: running`
- `nova-sprint stats --json` exit 0
- `nova-sprint log --json --since 1m` exit 0
- `nova-sprint card never-force-everywhereb --json` exit 0

Window, 359 cycles:
- `nova-sprint where --json`
- `nova-sprint stats --json`
- `nova-sprint log --json --since 1m`
- `nova-sprint card never-force-everywhereb --json`
- `curl -sS -m 30 http://127.0.0.1:7390/api/sprint`

After the last cycle:
- `nova-sprint log --json --since 2026-10-05T20:46:07Z` exit 0
- `nova-sprint where --json` exit 0
- `curl -sS -m 5 http://127.0.0.1:7390/api/sprint` exit 0, HTTP 200

Per criterion, measured value against bar with raw counts:
A land is a log line whose note type is `batch landed`, or whose `to` state ends in `:landed`.
p99 is nearest-rank: sort the walls ascending and take the 1-based index `ceil(0.99 * n)`. p50 uses `ceil(0.50 * n)`.
A sample is during a land when its start, from `date +%s`, is within 30 s of a land.
Exit 2 or a timeout counts as over 1 s. None timed out or exited nonzero.

Lands observed in the window (10 lands; requirement is at least 10):
1. `2026-10-05T20:50:16Z` card `missed-19-blocked-5293-cardsf2b` (stream `missed-2026-10-04`)
2. `2026-10-05T20:50:26Z` card `split-carry-5305-5308b` (stream `nova-sprint-split`)
3. `2026-10-05T20:52:59Z` card `eff-friend-children-on-fleetb-sb` (stream `missed-2026-10-04`)
4. `2026-10-05T20:58:09Z` card `fp-sec82-f3bzb` (stream `security2`)
5. `2026-10-05T20:58:47Z` card `pr-friend-stall-completeb` (stream `promote-red-2026-10-05`)
6. `2026-10-05T21:00:46Z` card `rate-emma-nova-sprint` (stream `rate-tools-dev-2026-10-05`)
7. `2026-10-05T21:01:39Z` card `fp-sec76-f7b` (stream `security2`)
8. `2026-10-05T21:08:18Z` card `split-secrets-tb` (stream `split`)
9. `2026-10-05T21:24:51Z` card `pr-fix-links-symlink-darwin` (stream `promote-red-2026-10-05`)
10. `2026-10-05T21:46:07Z` card `dash-glenn-tweaks-2026-10-04b` (stream `sprint-next`)

Samples during lands: 49 per verb, all exit 0.

Per criterion details:

where:
- Bar: p99 under 1 s during lands
- Samples taken: 359 total, 49 during lands
- Samples passing: 49 of 49 during lands (100% passing)
- Worst value: 0.10 s at 2026-10-05T20:52:29Z
- Failing samples: 0
- Passing samples count: 49. First: `2026-10-05T20:49:47Z where exit 0 wall 0.06`. Last: `2026-10-05T21:46:06Z where exit 0 wall 0.09`.

stats:
- Bar: p99 under 1 s during lands
- Samples taken: 359 total, 49 during lands
- Samples passing: 49 of 49 during lands (100% passing)
- Worst value: 0.38 s at 2026-10-05T21:01:12Z
- Failing samples: 0
- Passing samples count: 49. First: `2026-10-05T20:49:47Z stats exit 0 wall 0.23`. Last: `2026-10-05T21:46:06Z stats exit 0 wall 0.31`.

log:
- Bar: p99 under 1 s during lands
- Samples taken: 359 total, 49 during lands
- Samples passing: 49 of 49 during lands (100% passing)
- Worst value: 0.47 s at 2026-10-05T21:45:46Z
- Failing samples: 0
- Passing samples count: 49. First: `2026-10-05T20:49:47Z log exit 0 wall 0.42`. Last: `2026-10-05T21:46:06Z log exit 0 wall 0.46`.

card `never-force-everywhereb`:
- Bar: p99 under 1 s during lands
- Samples taken: 359 total, 49 during lands
- Samples passing: 49 of 49 during lands (100% passing)
- Worst value: 0.59 s at 2026-10-05T20:57:41Z
- Failing samples: 0
- Passing samples count: 49. First: `2026-10-05T20:49:47Z card exit 0 wall 0.51`. Last: `2026-10-05T21:46:06Z card exit 0 wall 0.41`.

No false STOPPED:
- `where` reported `machine: running` on all 359 cycles.
- The dashboard reported HTTP 200 and `machine: running` on all 359 cycles.
- Log notes of type `the machine stopped` and verb `stop`: 0.
- STOPPED observations: 0. False STOPPED: 0.
- First machine row: `2026-10-05T20:46:07Z machine: running` dashboard HTTP 200. Last: `2026-10-05T21:46:06Z machine: running` dashboard HTTP 200.

Summary statistics (p50, p99, max):

During lands (the bar):
- where: n=49, p50 0.08 s, p99 0.10 s, max 0.10 s
- stats: n=49, p50 0.29 s, p99 0.38 s, max 0.38 s
- log: n=49, p50 0.38 s, p99 0.47 s, max 0.47 s
- card: n=49, p50 0.44 s, p99 0.59 s, max 0.59 s

Whole hour (all 359 cycles):
- where: n=359, p50 0.06 s, p99 0.10 s, max 0.10 s
- stats: n=359, p50 0.20 s, p99 0.63 s, max 0.68 s
- log: n=359, p50 0.26 s, p99 0.74 s, max 0.78 s
- card: n=359, p50 0.31 s, p99 0.74 s, max 0.85 s

What was not measured:
- `nova-bus log` with Redis. Binary runs, but NOVA_BUS_REDIS was unset and live Redis was not opened by this card.
- Any server address other than `127.0.0.1:6390` and dashboard `http://127.0.0.1:7390/api/sprint`. The server was not switched.
- Write verbs (`add`, `merge`, `score`, `seat`, `take`, `release`) and server process restarts.
- `TestAcceptanceRecordsAreWellFormed` (placeholder, not run).
