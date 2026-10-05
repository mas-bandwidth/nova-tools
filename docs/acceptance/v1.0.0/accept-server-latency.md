Verdict: HOLD

Bar, verbatim: every verb answered under 1 s at p99 during lands for 1 hour; no false STOPPED.

The window ran. It had one land, and the bar needs 20. The window was not stretched and was not restarted. HOLD is the count, not a pass.

Window start: 2026-10-05T18:14:12Z
Window end: 2026-10-05T19:14:12Z
Host: studio.local
Span: 3600 s from the first sample. 351 cycles. A cycle that finished in under 10 s slept out the rest; a longer cycle started the next sample at once. Mean spacing about 10.3 s. No verb overlapped another.

Build measured:
- nova-sprint v1.2.0-dev.d8b7e04 darwin/arm64 go1.26.6 at /Users/glenn/.local/bin/nova-sprint (mode rwxr-xr-x, mtime 2026-10-05 13:22 local)
- nova-bus v1.2.0-dev.d8b7e04 darwin/arm64 go1.26.6 at /Users/glenn/.local/bin/nova-bus (same mtime)
- dashboard `build` field 5b91f4b5, from http://127.0.0.1:7390/api/sprint at the end of the window
- server NOVA_SPRINT_SERVER=127.0.0.1:6390, actor NOVA_SPRINT_ACTOR=rowan
- base of this file: 6e3ac141f71e0aa89b0bad5556905e500e2634b8 (origin/sprint/mechanical-2026-10-02)

Commands. Each sprint verb was `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`, timed with `/usr/bin/time -p`. Wall is the `real` line, seconds.

Probe, once, before the window, all against 127.0.0.1:
- `nova-sprint --version` exit 0
- `nova-bus --version` exit 0
- `nova-sprint help` exit 0
- `nova-bus help` exit 0
- `curl` to `telnet://127.0.0.1:6390` exit 28 after 5 s (a listener, not an HTTP body). `lsof` showed nova-sprint listening on 127.0.0.1:6390. The other address that listener also binds was not connected to, and the server was not switched.
- `curl -sS -m 5 http://127.0.0.1:7390/api/sprint` exit 0, HTTP 200, 68535 bytes, 0.003 s
- `nova-sprint where --json` exit 0, real 0.04 s, 70108 bytes, machine `machine: running`
- `nova-sprint stats --json` exit 0, real 1.01 s, 12750 bytes
- `nova-sprint log --json --since 1m` exit 0, real 3.40 s, 151064 bytes, 236 lines
- `nova-sprint where --json --cards` exit 0, 79321 bytes. Fleet card `accept-dashboard.w5` was `working` since 2026-10-05T18:02:13Z, primary `accept-dashboard`.
- `nova-sprint card accept-dashboard.w5 --json` exit 1, real 0.11 s: `no primary accept-dashboard.w5`
- `nova-sprint card accept-dashboard --json` exit 0, real 3.52 s, 105588 bytes. This is the card timed below.
- `nova-bus log --max 1 --json` exit 2. It refused: NOVA_BUS_REDIS is unset and no `--redis` was passed. Redis was not opened.

Window, 351 times each:
- `nova-sprint where --json`
- `nova-sprint stats --json`
- `nova-sprint log --json --since 1m`
- `nova-sprint card accept-dashboard --json`
- `curl -sS -m 30 http://127.0.0.1:7390/api/sprint`

After the last cycle:
- `nova-sprint log --json --since 2026-10-05T18:14:12Z` exit 0, 6979 lines, first at 2026-10-05T14:14:12-04:00, last at 2026-10-05T15:14:14-04:00
- `nova-sprint where --json` exit 0
- `curl` of the dashboard exit 0, HTTP 200, 68535 bytes

Not run: TestAcceptanceRecordsAreWellFormed. No seat, adopt, finish, take, or other write verb.

A land is a log line whose note type is `batch landed`, or whose `to` state ends in `:landed`. p99 is nearest-rank: sort the walls ascending and take the 1-based index `ceil(0.99 * n)`. p50 uses `ceil(0.50 * n)`. A sample is during a land when its start, from `date +%s`, is within 30 s of a land. Log timestamps were truncated to the whole second (under 1 s). No sample sat on that 30 s edge, so the truncation adds or drops none. Exit 2 or a timeout counts as over 1 s. None of the during-land samples timed out or exited 2.

Lands in the window: 1. One note `batch landed` at 2026-10-05T14:28:35.058756-04:00 (2026-10-05T18:28:35Z), card `tests-reexec-guard-everywhere-w`, stream `sprint-v1-sre`, `merging` to `landed` (verb `merge`, then the same move drained by `tick drain`). A later `score` and `tick drain` stay on `:landed` and are not a second land. Stream-landed notes: 0. Bar is 20 lands. Count is 1.

Samples during that land: 6 per verb, all exit 0.

where. Bar: p99 under 1 s. Taken 351, during land 6, passing 6, nonzero during land 0. p50 0.06 s, p99 0.12 s, max 0.12 s at 2026-10-05T18:28:17Z. Meets the time bar on this one land. Passing count 6. First: `2026-10-05T18:28:07Z where exit 0 wall 0.06`. Last: `2026-10-05T18:28:57Z where exit 0 wall 0.06`.

stats. Bar: p99 under 1 s. Taken 351, during land 6, passing 1, nonzero during land 0. p50 1.18 s, p99 1.34 s, max 1.34 s at 2026-10-05T18:28:37Z. Over the bar. Failing lines, in full:
- `2026-10-05T18:28:07Z stats exit 0 wall 1.24`
- `2026-10-05T18:28:27Z stats exit 0 wall 1.13`
- `2026-10-05T18:28:37Z stats exit 0 wall 1.34`
- `2026-10-05T18:28:47Z stats exit 0 wall 1.18`
- `2026-10-05T18:28:57Z stats exit 0 wall 1.30`
The one passing line: `2026-10-05T18:28:17Z stats exit 0 wall 0.97`.

log. Bar: p99 under 1 s. Taken 351, during land 6, passing 0, nonzero during land 0. p50 2.97 s, p99 3.05 s, max 3.05 s at 2026-10-05T18:28:08Z. Over the bar. Failing lines, in full:
- `2026-10-05T18:28:08Z log exit 0 wall 3.05`
- `2026-10-05T18:28:18Z log exit 0 wall 2.97`
- `2026-10-05T18:28:28Z log exit 0 wall 2.73`
- `2026-10-05T18:28:38Z log exit 0 wall 2.84`
- `2026-10-05T18:28:48Z log exit 0 wall 2.97`
- `2026-10-05T18:28:58Z log exit 0 wall 3.00`

card `accept-dashboard`. Bar: p99 under 1 s. Taken 351, during land 6, passing 0, nonzero during land 0. p50 3.57 s, p99 3.71 s, max 3.71 s at 2026-10-05T18:28:21Z. Over the bar. Failing lines, in full:
- `2026-10-05T18:28:11Z card exit 0 wall 3.55`
- `2026-10-05T18:28:21Z card exit 0 wall 3.71`
- `2026-10-05T18:28:31Z card exit 0 wall 3.58`
- `2026-10-05T18:28:41Z card exit 0 wall 3.70`
- `2026-10-05T18:28:51Z card exit 0 wall 3.42`
- `2026-10-05T18:29:01Z card exit 0 wall 3.57`

No false STOPPED. `where` reported `machine: running` on all 351 cycles. The dashboard reported HTTP 200 and `machine: running` on all 351 (body 68534 or 68535 bytes). Log notes of type `the machine stopped`, and verb `stop`: 0. STOPPED observations: 0. False STOPPED: 0. There is no STOPPED line to list. First machine row: `2026-10-05T18:14:12Z machine: running` dashboard HTTP 200. Last: `2026-10-05T19:14:02Z machine: running` dashboard HTTP 200.

Outside the land, still in the hour, three reads exited 2 (counts as over 1 s) when the server on 127.0.0.1:6390 did not answer. They are not inside the 30 s land window:
- `2026-10-05T18:51:30Z stats exit 2 wall 1.07` (EOF from `http://127.0.0.1:6390/verbs`)
- `2026-10-05T18:51:31Z log exit 2 wall 0.01` (connection refused)
- `2026-10-05T18:51:31Z card exit 2 wall 0.01` (connection refused)
The next cycle answered again. `where` itself had four exit-0 samples at or over 1 s, none of them during the land: wall 2.25 at 18:22:35Z, 2.80 at 18:32:47Z, 3.33 at 18:42:52Z, 1.03 at 18:50:13Z.

Whole hour, not the bar (the bar is the during-land p99, and that set has one land): where n=351 p50 0.06 p99 1.03 max 3.33, 4 samples at or over 1 s; stats n=351 p50 1.26 p99 3.08 max 3.87, 347 at or over 1 s; log n=351 p50 2.97 p99 5.29 max 15.33, 350 at or over 1 s; card n=351 p50 3.63 p99 6.49 max 13.71, 350 at or over 1 s.

What was not measured:
- A p99 over 20 lands. The hour contained 1 land.
- `nova-bus log`. The binary runs. The log verb refused without a Redis address, and Redis was not opened.
- Any address other than 127.0.0.1:6390 and http://127.0.0.1:7390/api/sprint. The server was not switched.
- `nova-sprint card` on the fleet id `accept-dashboard.w5` (exit 1, no primary). The timed card is the primary `accept-dashboard`. Whether that fleet card stayed `working` for the whole hour was not re-read.
- Write verbs, a server start, and TestAcceptanceRecordsAreWellFormed.
