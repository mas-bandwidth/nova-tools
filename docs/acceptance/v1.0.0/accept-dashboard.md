Verdict: FAIL

every number on the dashboard traced to the store.

Window start: 2026-10-05T18:12:26Z
Window end: 2026-10-05T18:42:50Z
Build measured: nova-sprint v1.2.0-dev.d8b7e04 darwin/arm64 go1.26.6
Bus binary: nova-bus v1.2.0-dev.d8b7e04 darwin/arm64 go1.26.6. nova-bus2 is not installed.
Branch base: the tip of origin/sprint/mechanical-2026-10-02 this attempt started from. The numbers below are the live binaries, not a rebuild of that commit.

## Criterion

Bar, verbatim: every number on the dashboard traced to the store.

A number is a JSON number on `/api/sprint`, or a string that is only a count, a percentage, or a dollar amount, plus the counts and the percentage inside `summary` and `buffer`. It passes at a snapshot when it equals the store field named beside it. A difference passes only when the sprint log between that snapshot's dashboard read and its `where` read accounts for the change. A snapshot passes when every number passes. The bar is three passing snapshots.

Samples taken: 3. Samples passing: 0.

- Snapshot 1, window start 2026-10-05T18:12:26Z: equal=1867 differ=104, of which untraced=4. Worst absolute delta: data.ready dash=17 store=59 delta=-42. Log lines whose stamp falls between the dashboard read and the where read: kinds=decided=1 accounting=0 from=2026-10-05T14:12:26 to=2026-10-05T14:12:26 lines=1. The four reads together spanned 4 seconds.
- Snapshot 2: equal=1864 differ=107, of which untraced=4. Worst absolute delta: data.ready dash=17 store=59 delta=-42. Log lines between those two reads: kinds=decided=3 happened=2 judgment=2 accounting=0 from=2026-10-05T14:22:30 to=2026-10-05T14:22:33 lines=7. Curl and where finished in 3 seconds; the four reads spanned 8 seconds because the log since the window start had grown.
- Snapshot 3: equal=1866 differ=105, of which untraced=4. Worst absolute delta: data.tables.fleet.hetzner.load dash=0.5% store=96.7% delta=-96.2 (a live load cell; the dashboard's copy is still 0.5%). Log lines between those two reads: kinds=decided=1 accounting=0 from=2026-10-05T14:32:38 to=2026-10-05T14:32:39 lines=1. Curl and where finished in 1 second; the four reads spanned 12 seconds on a log of about 1.9MB.

Worst count gap: `data.ready` (the same count is `data.buffer.ready`). Dashboard ready was 17, 17, 17. Store ready was 59, 59, 53. Dashboard landed stayed 1544, 1544, 1544. Store landed was 1547, 1547, 1548. The summary percentage matches each document's own counts: 100*1544/1868 is 82.7%, and 100*1548/1870 is 82.8%. Paths that differ in at least one snapshot: 111. Paths equal in all three: 1860. First equal path: data.buffer.twice_width. Last equal path: data.width.

The between-read logs are the few seconds of each snapshot. They are tick judgments. They contain no move, no queue, and no landing. A separate log from the frozen snapshot time 2026-10-05T17:23:40Z through the window end does contain 4 batch-landed notes, which is the store moving while the dashboard's landed stayed 1544. Those notes are not between the two reads of any snapshot, so they do not excuse the gap. The gaps are not what changed between the two reads.

## Why the dashboard does not follow the store

127.0.0.1:7390 is `server.py` with `DASHBOARD_UPSTREAM` pointed at the other dashboard on this machine (tailnet address, port 7390). That process runs `ns.sh`, which appends `--costs` to `where`. Installed `nova-sprint` refuses `--costs` (exit 2, unknown flag). The poll log line is `2026-10-05 01:23:41 PM read failed: where exited 2`, and every minute summary after that has failed equal to reads. `server.py` keeps the last good snapshot and still answers HTTP 200. All three reads in this window have `data.at` 2026-10-05T13:23:40.964379-04:00 / 2026-10-05T13:23:40.964379-04:00 / 2026-10-05T13:23:40.964379-04:00. `throughput` stayed 0.0, 0.0, 0.0 because the poller's ring saw that frozen `landed`, not the store.

`tiers` and `cost_by_tier` on the frozen work rows were traced to `where.stream_costs` (current `where --json` keeps them there, not inlined on the work row). Friend, provider, and critical numbers were traced by name or id. `okpct` is `pct(ok/ok+failed)` to one decimal. `summary` is `landed/all`, `100*landed/all` to one decimal, `held=N`, and the ETA from `etaMinutes` at `LandingRate`. `buffer` is `ready/(2*width)`. `readSeconds`, `minInterval`, `throughput`, and `throughputMinutes` are the poller's fields, not store fields.

## Commands

Each sprint verb was `env -u NOVA_SPRINT_REDIS -u NOVA_REDIS_ADDR -u NOVA_BUS_REDIS NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint`. The probe and the window used 127.0.0.1 only. No write verb. Redis was not opened. No server was started.

Probe, before the window: `nc -z -G 3 127.0.0.1 6390` connected; `curl` of `http://127.0.0.1:7390/api/sprint` returned HTTP 200, 68535 bytes; both binaries printed v1.2.0-dev.d8b7e04. A `where --json --costs` probe exited 2.

```
sample1 curl -sS -m 20 http://127.0.0.1:7390/api/sprint
sample1 NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json --cards
sample1 NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json
sample1 NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint log --json --since 2026-10-05T18:12:26Z --max 0
sample2 curl -sS -m 20 http://127.0.0.1:7390/api/sprint
sample2 NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json --cards
sample2 NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json
sample2 NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint log --json --since 2026-10-05T18:12:26Z --max 0
sample3 curl -sS -m 20 http://127.0.0.1:7390/api/sprint
sample3 NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json --cards
sample3 NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json
sample3 NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint log --json --since 2026-10-05T18:12:26Z --max 0
extra NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint log --json --since 2026-10-05T17:23:40Z --max 0
```

Sample clocks:

```
s1
sample 1 begin 2026-10-05T18:12:26Z
2026-10-05T18:12:26Z curl -sS -m 20 http://127.0.0.1:7390/api/sprint
2026-10-05T18:12:26Z curl_done exit=0 http_code:200 size:68535
2026-10-05T18:12:26Z nova-sprint where --json --cards
2026-10-05T18:12:26Z where_done exit=0 bytes=   78619
2026-10-05T18:12:26Z nova-sprint stats --json
2026-10-05T18:12:27Z stats_done exit=0 bytes=   12748
2026-10-05T18:12:27Z nova-sprint log --json --since 2026-10-05T18:12:26Z --max 0
2026-10-05T18:12:30Z log_done exit=0 bytes=    5675
span_s=4
s2
sample 2 begin 2026-10-05T18:22:30Z
2026-10-05T18:22:30Z curl -sS -m 20 http://127.0.0.1:7390/api/sprint
2026-10-05T18:22:30Z curl_done exit=0 http_code:200 size:68535
2026-10-05T18:22:30Z nova-sprint where --json --cards
2026-10-05T18:22:33Z where_done exit=0 bytes=   77871
2026-10-05T18:22:33Z nova-sprint stats --json
2026-10-05T18:22:35Z stats_done exit=0 bytes=   12749
2026-10-05T18:22:35Z nova-sprint log --json --since 2026-10-05T18:12:26Z --max 0
2026-10-05T18:22:38Z log_done exit=0 bytes= 1039470
span_s=8
s3
sample 3 begin 2026-10-05T18:32:38Z
2026-10-05T18:32:38Z curl -sS -m 20 http://127.0.0.1:7390/api/sprint
2026-10-05T18:32:38Z curl_done exit=0 http_code:200 size:68534
2026-10-05T18:32:38Z nova-sprint where --json --cards
2026-10-05T18:32:39Z where_done exit=0 bytes=   79848
2026-10-05T18:32:39Z nova-sprint stats --json
2026-10-05T18:32:44Z stats_done exit=0 bytes=   12749
2026-10-05T18:32:44Z nova-sprint log --json --since 2026-10-05T18:12:26Z --max 0
2026-10-05T18:32:50Z log_done exit=0 bytes= 1886937
span_s=12
```

Curl, where, stats, and log exited 0 at every snapshot.

Dashboard lines that decide the sprint headline:

```
s1 {"ok":true,"error":null,"fetchedAt":"2026-10-05T18:12:25.267213+00:00","readSeconds":0.006,"minInterval":1.0,"throughput":0.0,"throughputMinutes":60.0,"build":"5b91f4b5","at":"2026-10-05T13:23:40.964379-04:00","landed":1544,"all":1868,"held":44,"ready":17,"summary":"1544/1868 82.7% held=44 -> ETA 13h48m"}
s2 {"ok":true,"error":null,"fetchedAt":"2026-10-05T18:22:30.533317+00:00","readSeconds":0.009,"minInterval":1.0,"throughput":0.0,"throughputMinutes":60.0,"build":"5b91f4b5","at":"2026-10-05T13:23:40.964379-04:00","landed":1544,"all":1868,"held":44,"ready":17,"summary":"1544/1868 82.7% held=44 -> ETA 13h48m"}
s3 {"ok":true,"error":null,"fetchedAt":"2026-10-05T18:32:38.319646+00:00","readSeconds":0.01,"minInterval":1.0,"throughput":0.0,"throughputMinutes":60.0,"build":"5b91f4b5","at":"2026-10-05T13:23:40.964379-04:00","landed":1544,"all":1868,"held":44,"ready":17,"summary":"1544/1868 82.7% held=44 -> ETA 13h48m"}
```

Store lines:

```
s1 {"at":"2026-10-05T14:12:26.324189-04:00","landed":1547,"all":1869,"held":44,"ready":59,"width":0,"epoch":15,"summary":"1547/1869 82.8% held=44 -> ETA 13h51m"}
s2 {"at":"2026-10-05T14:22:33.87483-04:00","landed":1547,"all":1869,"held":44,"ready":59,"width":0,"epoch":15,"summary":"1547/1869 82.8% held=44 -> ETA 13h53m"}
s3 {"at":"2026-10-05T14:32:39.632145-04:00","landed":1548,"all":1870,"held":44,"ready":53,"width":0,"epoch":15,"summary":"1548/1870 82.8% held=44 -> ETA 13h55m"}
```

## Numbers that differ

Failing paths, in full, side by side.

| path | store field | s1 dash | s1 store | s2 dash | s2 store | s3 dash | s3 store |
|---|---|---|---|---|---|---|---|
| data.all | all | 1868 | 1869 | 1868 | 1869 | 1868 | 1870 |
| data.buffer.ready | ready | 17 | 59 | 17 | 59 | 17 | 53 |
| data.friends.0.ready | friends[name=emma].ready | 6 | 0 | 6 | 0 | 6 | 0 |
| data.friends.0.working | friends[name=emma].working | 10 | 0 | 10 | 0 | 10 | 0 |
| data.friends.1.working | friends[name=freddy].working | 5 | 0 | 5 | 0 | 5 | 0 |
| data.friends.2.failed | friends[name=johnny].failed | 28 | 29 | 28 | 29 | 28 | 29 |
| data.friends.2.ready | friends[name=johnny].ready | 8 | 0 | 8 | 2 | 8 | 2 |
| data.friends.2.working | friends[name=johnny].working | 8 | 5 | 8 | 8 | 8 | 8 |
| data.friends.3.ready | friends[name=rowan].ready | 6 | 0 | 6 | 0 | 6 | 0 |
| data.friends.3.working | friends[name=rowan].working | 6 | 0 | 6 | 0 | 6 | 0 |
| data.friends.4.failed | friends[name=rowan-space].failed | 8 | 11 | 8 | 11 | 8 | 11 |
| data.friends.4.ok | friends[name=rowan-space].ok | 25 | 35 | 25 | 36 | 25 | 36 |
| data.friends.4.ready | friends[name=rowan-space].ready | 8 | 0 | 8 | 0 | 8 | 0 |
| data.friends.4.working | friends[name=rowan-space].working | 8 | 6 | 8 | 0 | 8 | 0 |
| data.friends.5.failed | friends[name=stella].failed | 37 | 42 | 37 | 42 | 37 | 42 |
| data.friends.5.ok | friends[name=stella].ok | 31 | 32 | 31 | 33 | 31 | 33 |
| data.friends.5.ready | friends[name=stella].ready | 2 | 0 | 2 | 0 | 2 | 0 |
| data.friends.5.working | friends[name=stella].working | 2 | 1 | 2 | 0 | 2 | 0 |
| data.landed | landed | 1544 | 1547 | 1544 | 1547 | 1544 | 1548 |
| data.ready | ready | 17 | 59 | 17 | 59 | 17 | 53 |
| data.summary.all | all | 1868 | 1869 | 1868 | 1869 | 1868 | 1870 |
| data.summary.eta_minutes | etaMinutes%60 | 48 | 51 | 48 | 53 | 48 | 55 |
| data.summary.landed | landed | 1544 | 1547 | 1544 | 1547 | 1544 | 1548 |
| data.summary.pct | 100*landed/all, one decimal | 82.7% | 82.8% | 82.7% | 82.8% | 82.7% | 82.8% |
| data.tables.fleet.batman.load | tables.fleet.batman.load | 1.0% | 1.0% | 1.0% | 0.0% | 1.0% | 0.0% |
| data.tables.fleet.hetzner.load | tables.fleet.hetzner.load | 0.5% | 0.6% | 0.5% | 0.6% | 0.5% | 96.7% |
| data.tables.fleet.space.load | tables.fleet.space.load | 1.0% | 2.5% | 1.0% | 1.5% | 1.0% | 2.6% |
| data.tables.fleet.studio.load | tables.fleet.studio.load | 43.0% | 33.0% | 43.0% | 28.0% | 43.0% | 37.0% |
| data.tables.fleet.superman.load | tables.fleet.superman.load | 2.0% | 2.0% | 2.0% | 3.0% | 2.0% | 2.0% |
| data.tables.fleet.vision.load | tables.fleet.vision.load | 18.4% | 0.5% | 18.4% | 0.8% | 18.4% | 2.1% |
| data.tables.friends.emma.ready | tables.friends.emma.ready | 6 | 0 | 6 | 0 | 6 | 0 |
| data.tables.friends.emma.working | tables.friends.emma.working | 10 | 0 | 10 | 0 | 10 | 0 |
| data.tables.friends.freddy.working | tables.friends.freddy.working | 5 | 0 | 5 | 0 | 5 | 0 |
| data.tables.friends.johnny.done | tables.friends.johnny.done | 117 | 118 | 117 | 118 | 117 | 118 |
| data.tables.friends.johnny.failed | tables.friends.johnny.failed | 28 | 29 | 28 | 29 | 28 | 29 |
| data.tables.friends.johnny.okpct | tables.friends.johnny.okpct | 76.1% | 75.4% | 76.1% | 75.4% | 76.1% | 75.4% |
| data.tables.friends.johnny.ready | tables.friends.johnny.ready | 8 | 0 | 8 | 2 | 8 | 2 |
| data.tables.friends.johnny.working | tables.friends.johnny.working | 8 | 5 | 8 | 8 | 8 | 8 |
| data.tables.friends.rowan-space.done | tables.friends.rowan-space.done | 33 | 46 | 33 | 47 | 33 | 47 |
| data.tables.friends.rowan-space.failed | tables.friends.rowan-space.failed | 8 | 11 | 8 | 11 | 8 | 11 |
| data.tables.friends.rowan-space.ok | tables.friends.rowan-space.ok | 25 | 35 | 25 | 36 | 25 | 36 |
| data.tables.friends.rowan-space.okpct | tables.friends.rowan-space.okpct | 75.8% | 76.1% | 75.8% | 76.6% | 75.8% | 76.6% |
| data.tables.friends.rowan-space.ready | tables.friends.rowan-space.ready | 8 | 0 | 8 | 0 | 8 | 0 |
| data.tables.friends.rowan-space.working | tables.friends.rowan-space.working | 8 | 6 | 8 | 0 | 8 | 0 |
| data.tables.friends.rowan.ready | tables.friends.rowan.ready | 6 | 0 | 6 | 0 | 6 | 0 |
| data.tables.friends.rowan.working | tables.friends.rowan.working | 6 | 0 | 6 | 0 | 6 | 0 |
| data.tables.friends.stella.done | tables.friends.stella.done | 68 | 74 | 68 | 75 | 68 | 75 |
| data.tables.friends.stella.failed | tables.friends.stella.failed | 37 | 42 | 37 | 42 | 37 | 42 |
| data.tables.friends.stella.ok | tables.friends.stella.ok | 31 | 32 | 31 | 33 | 31 | 33 |
| data.tables.friends.stella.okpct | tables.friends.stella.okpct | 45.6% | 43.2% | 45.6% | 44.0% | 45.6% | 44.0% |
| data.tables.friends.stella.ready | tables.friends.stella.ready | 2 | 0 | 2 | 0 | 2 | 0 |
| data.tables.friends.stella.working | tables.friends.stella.working | 2 | 1 | 2 | 0 | 2 | 0 |
| data.tables.merge.reliability-now.merged | tables.merge.reliability-now.merged | 2 | 5 | 2 | 5 | 2 | 5 |
| data.tables.merge.reliability-now.returned | tables.merge.reliability-now.returned | 1 | 2 | 1 | 2 | 1 | 2 |
| data.tables.merge.reliability-now.stuck | tables.merge.reliability-now.stuck | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.merge.sprint-v1-sre.merged | tables.merge.sprint-v1-sre.merged | 8 | 8 | 8 | 8 | 8 | 9 |
| data.tables.readers.reader-emma.asked | tables.readers.reader-emma.asked | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.readers.reader-emma.broken | tables.readers.reader-emma.broken | 7 | 8 | 7 | 8 | 7 | 8 |
| data.tables.readers.reader-emma.ok | tables.readers.reader-emma.ok | 58 | 61 | 58 | 61 | 58 | 62 |
| data.tables.readers.reader-johnny.asked | tables.readers.reader-johnny.asked | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.readers.reader-johnny.broken | tables.readers.reader-johnny.broken | 36 | 41 | 36 | 43 | 36 | 43 |
| data.tables.readers.reader-johnny.ok | tables.readers.reader-johnny.ok | 87 | 88 | 87 | 89 | 87 | 89 |
| data.tables.readers.reader-johnny.reading | tables.readers.reader-johnny.reading | 2 | 2 | 2 | 0 | 2 | 0 |
| data.tables.readers.reader-rowan.asked | tables.readers.reader-rowan.asked | 4 | 2 | 4 | 3 | 4 | 2 |
| data.tables.work.friend-test.review | tables.work.friend-test.review | 0 | 1 | 0 | 1 | 0 | 1 |
| data.tables.work.friend-test.working | tables.work.friend-test.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.heavy-emma.ready | tables.work.heavy-emma.ready | 0 | 1 | 0 | 1 | 0 | 0 |
| data.tables.work.heavy-emma.working | tables.work.heavy-emma.working | 1 | 0 | 1 | 0 | 1 | 1 |
| data.tables.work.lint.review | tables.work.lint.review | 5 | 6 | 5 | 6 | 5 | 6 |
| data.tables.work.lint.working | tables.work.lint.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.machinery2.review | tables.work.machinery2.review | 0 | 1 | 0 | 1 | 0 | 1 |
| data.tables.work.machinery2.working | tables.work.machinery2.working | 2 | 1 | 2 | 1 | 2 | 1 |
| data.tables.work.no-shell-v1-2-0.ready | tables.work.no-shell-v1-2-0.ready | 0 | 2 | 0 | 2 | 0 | 2 |
| data.tables.work.no-shell-v1-2-0.working | tables.work.no-shell-v1-2-0.working | 2 | 0 | 2 | 0 | 2 | 0 |
| data.tables.work.nova-sprint-split.ready | tables.work.nova-sprint-split.ready | 0 | 2 | 0 | 2 | 0 | 1 |
| data.tables.work.nova-sprint-split.review | tables.work.nova-sprint-split.review | 4 | 6 | 4 | 6 | 4 | 6 |
| data.tables.work.nova-sprint-split.working | tables.work.nova-sprint-split.working | 4 | 0 | 4 | 0 | 4 | 1 |
| data.tables.work.promote-red-2026-10-05.ready | tables.work.promote-red-2026-10-05.ready | 0 | 1 | 0 | 1 | 0 | 1 |
| data.tables.work.promote-red-2026-10-05.working | tables.work.promote-red-2026-10-05.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.rate-tools-dev-2026-10-05.ready | tables.work.rate-tools-dev-2026-10-05.ready | 0 | 17 | 0 | 17 | 0 | 12 |
| data.tables.work.rate-tools-dev-2026-10-05.review | tables.work.rate-tools-dev-2026-10-05.review | 26 | 30 | 26 | 31 | 26 | 31 |
| data.tables.work.rate-tools-dev-2026-10-05.working | tables.work.rate-tools-dev-2026-10-05.working | 27 | 6 | 27 | 5 | 27 | 10 |
| data.tables.work.reliability-now.landed | tables.work.reliability-now.landed | 2 | 5 | 2 | 5 | 2 | 5 |
| data.tables.work.reliability-now.merging | tables.work.reliability-now.merging | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.reliability-now.ready | tables.work.reliability-now.ready | 14 | 28 | 14 | 28 | 14 | 29 |
| data.tables.work.reliability-now.review | tables.work.reliability-now.review | 8 | 12 | 8 | 12 | 8 | 12 |
| data.tables.work.reliability-now.tiers.flash | stream_costs.reliability-now.tiers.flash | 54 | 55 | 54 | 55 | 54 | 56 |
| data.tables.work.reliability-now.waiting | tables.work.reliability-now.waiting | 12 | 11 | 12 | 11 | 12 | 11 |
| data.tables.work.reliability-now.working | tables.work.reliability-now.working | 18 | 0 | 18 | 0 | 18 | 0 |
| data.tables.work.rowan-only-to-product.ready | tables.work.rowan-only-to-product.ready | 3 | 4 | 3 | 4 | 3 | 4 |
| data.tables.work.rowan-only-to-product.working | tables.work.rowan-only-to-product.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.security2.ready | tables.work.security2.ready | 0 | 1 | 0 | 1 | 0 | 1 |
| data.tables.work.security2.working | tables.work.security2.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.sprint-v1-docs.review | tables.work.sprint-v1-docs.review | 0 | 0 | 0 | 1 | 0 | 1 |
| data.tables.work.sprint-v1-docs.working | tables.work.sprint-v1-docs.working | 1 | 1 | 1 | 0 | 1 | 0 |
| data.tables.work.sprint-v1-integrity.ready | tables.work.sprint-v1-integrity.ready | 0 | 1 | 0 | 1 | 0 | 1 |
| data.tables.work.sprint-v1-integrity.working | tables.work.sprint-v1-integrity.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.sprint-v1-sre.landed | tables.work.sprint-v1-sre.landed | 8 | 8 | 8 | 8 | 8 | 9 |
| data.tables.work.sprint-v1-sre.review | tables.work.sprint-v1-sre.review | 4 | 5 | 4 | 5 | 4 | 4 |
| data.tables.work.sprint-v1-sre.working | tables.work.sprint-v1-sre.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.sprint-v1-verbs.ready | tables.work.sprint-v1-verbs.ready | 0 | 1 | 0 | 1 | 0 | 1 |
| data.tables.work.sprint-v1-verbs.working | tables.work.sprint-v1-verbs.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.sprint-v1-yes.ready | tables.work.sprint-v1-yes.ready | 0 | 1 | 0 | 1 | 0 | 1 |
| data.tables.work.sprint-v1-yes.working | tables.work.sprint-v1-yes.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tables.work.today-efficiency.review | tables.work.today-efficiency.review | 0 | 1 | 0 | 1 | 0 | 1 |
| data.tables.work.today-efficiency.working | tables.work.today-efficiency.working | 1 | 0 | 1 | 0 | 1 | 0 |
| data.tiers.flash | tiers.flash | 1184 | 1185 | 1184 | 1185 | 1184 | 1186 |
| minInterval | (none) | 1.0 |  | 1.0 |  | 1.0 |  |
| readSeconds | (none) | 0.006 |  | 0.009 |  | 0.01 |  |
| throughput | computation from data.landed over the poller's hour ring | 0.0 |  | 0.0 |  | 0.0 |  |
| throughputMinutes | (none) | 60.0 |  | 60.0 |  | 60.0 |  |

## Full table

Every measured path. First equal path: data.buffer.twice_width. Last equal path: data.width. Equal at all three snapshots: 1860.

| path | store field | s1 dash | s1 store | s1 | s2 dash | s2 store | s2 | s3 dash | s3 store | s3 |
|---|---|---|---|---|---|---|---|---|---|---|
| data.all | all | 1868 | 1869 | differ | 1868 | 1869 | differ | 1868 | 1870 | differ |
| data.buffer.ready | ready | 17 | 59 | differ | 17 | 59 | differ | 17 | 53 | differ |
| data.buffer.twice_width | 2*width | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.critical.0.behind | critical[id=add-lint-catches-brief-defects].behind | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.critical.1.behind | critical[id=finish-lint-before-read].behind | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.critical.2.behind | critical[id=fg-delivery-no-progress-not-a-cap].behind | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.critical.3.behind | critical[id=setup-nova-up-local].behind | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.critical.4.behind | critical[id=machine-gate-before-read].behind | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.epoch | epoch | 15 | 15 | equal | 15 | 15 | equal | 15 | 15 | equal |
| data.friends.0.failed | friends[name=emma].failed | 20 | 20 | equal | 20 | 20 | equal | 20 | 20 | equal |
| data.friends.0.ok | friends[name=emma].ok | 303 | 303 | equal | 303 | 303 | equal | 303 | 303 | equal |
| data.friends.0.ready | friends[name=emma].ready | 6 | 0 | differ | 6 | 0 | differ | 6 | 0 | differ |
| data.friends.0.width | friends[name=emma].width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.friends.0.working | friends[name=emma].working | 10 | 0 | differ | 10 | 0 | differ | 10 | 0 | differ |
| data.friends.1.failed | friends[name=freddy].failed | 31 | 31 | equal | 31 | 31 | equal | 31 | 31 | equal |
| data.friends.1.ok | friends[name=freddy].ok | 73 | 73 | equal | 73 | 73 | equal | 73 | 73 | equal |
| data.friends.1.ready | friends[name=freddy].ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.1.report.width | friends[name=freddy].report.width | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.friends.1.report.working | friends[name=freddy].report.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.1.width | friends[name=freddy].width | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.friends.1.working | friends[name=freddy].working | 5 | 0 | differ | 5 | 0 | differ | 5 | 0 | differ |
| data.friends.10.failed | friends[name=rowan-personal].failed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.10.ok | friends[name=rowan-personal].ok | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.10.ready | friends[name=rowan-personal].ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.10.width | friends[name=rowan-personal].width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.friends.10.working | friends[name=rowan-personal].working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.2.failed | friends[name=johnny].failed | 28 | 29 | differ | 28 | 29 | differ | 28 | 29 | differ |
| data.friends.2.ok | friends[name=johnny].ok | 89 | 89 | equal | 89 | 89 | equal | 89 | 89 | equal |
| data.friends.2.ready | friends[name=johnny].ready | 8 | 0 | differ | 8 | 2 | differ | 8 | 2 | differ |
| data.friends.2.width | friends[name=johnny].width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.friends.2.working | friends[name=johnny].working | 8 | 5 | differ | 8 | 8 | equal | 8 | 8 | equal |
| data.friends.3.failed | friends[name=rowan].failed | 23 | 23 | equal | 23 | 23 | equal | 23 | 23 | equal |
| data.friends.3.ok | friends[name=rowan].ok | 56 | 56 | equal | 56 | 56 | equal | 56 | 56 | equal |
| data.friends.3.ready | friends[name=rowan].ready | 6 | 0 | differ | 6 | 0 | differ | 6 | 0 | differ |
| data.friends.3.width | friends[name=rowan].width | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.friends.3.working | friends[name=rowan].working | 6 | 0 | differ | 6 | 0 | differ | 6 | 0 | differ |
| data.friends.4.failed | friends[name=rowan-space].failed | 8 | 11 | differ | 8 | 11 | differ | 8 | 11 | differ |
| data.friends.4.ok | friends[name=rowan-space].ok | 25 | 35 | differ | 25 | 36 | differ | 25 | 36 | differ |
| data.friends.4.ready | friends[name=rowan-space].ready | 8 | 0 | differ | 8 | 0 | differ | 8 | 0 | differ |
| data.friends.4.width | friends[name=rowan-space].width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.friends.4.working | friends[name=rowan-space].working | 8 | 6 | differ | 8 | 0 | differ | 8 | 0 | differ |
| data.friends.5.failed | friends[name=stella].failed | 37 | 42 | differ | 37 | 42 | differ | 37 | 42 | differ |
| data.friends.5.ok | friends[name=stella].ok | 31 | 32 | differ | 31 | 33 | differ | 31 | 33 | differ |
| data.friends.5.ready | friends[name=stella].ready | 2 | 0 | differ | 2 | 0 | differ | 2 | 0 | differ |
| data.friends.5.width | friends[name=stella].width | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.friends.5.working | friends[name=stella].working | 2 | 1 | differ | 2 | 0 | differ | 2 | 0 | differ |
| data.friends.6.failed | friends[name=alex].failed | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.friends.6.ok | friends[name=alex].ok | 17 | 17 | equal | 17 | 17 | equal | 17 | 17 | equal |
| data.friends.6.ready | friends[name=alex].ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.6.width | friends[name=alex].width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.friends.6.working | friends[name=alex].working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.7.failed | friends[name=zhi].failed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.friends.7.ok | friends[name=zhi].ok | 29 | 29 | equal | 29 | 29 | equal | 29 | 29 | equal |
| data.friends.7.ready | friends[name=zhi].ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.7.width | friends[name=zhi].width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.friends.7.working | friends[name=zhi].working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.8.failed | friends[name=rowan-mas].failed | 122 | 122 | equal | 122 | 122 | equal | 122 | 122 | equal |
| data.friends.8.ok | friends[name=rowan-mas].ok | 201 | 201 | equal | 201 | 201 | equal | 201 | 201 | equal |
| data.friends.8.ready | friends[name=rowan-mas].ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.8.width | friends[name=rowan-mas].width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.friends.8.working | friends[name=rowan-mas].working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.9.failed | friends[name=rowan-next].failed | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.friends.9.ok | friends[name=rowan-next].ok | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.friends.9.ready | friends[name=rowan-next].ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.friends.9.width | friends[name=rowan-next].width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.friends.9.working | friends[name=rowan-next].working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.held | held | 44 | 44 | equal | 44 | 44 | equal | 44 | 44 | equal |
| data.landed | landed | 1544 | 1547 | differ | 1544 | 1547 | differ | 1544 | 1548 | differ |
| data.providers.0.spend_hour | providers[name=abliteration-ai].spend_hour | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.providers.1.spend_hour | providers[name=inception].spend_hour | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.providers.2.spend_hour | providers[name=opencode].spend_hour | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.providers.3.spend_hour | providers[name=openrouter].spend_hour | 177.51684689003093 | 177.51684689003093 | equal | 177.51684689003093 | 177.51684689003093 | equal | 177.51684689003093 | 177.51684689003093 | equal |
| data.ready | ready | 17 | 59 | differ | 17 | 59 | differ | 17 | 53 | differ |
| data.seat.generation | seat.generation | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.summary.all | all | 1868 | 1869 | differ | 1868 | 1869 | differ | 1868 | 1870 | differ |
| data.summary.eta_hours | etaMinutes/60 | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.summary.eta_minutes | etaMinutes%60 | 48 | 51 | differ | 48 | 53 | differ | 48 | 55 | differ |
| data.summary.held | held | 44 | 44 | equal | 44 | 44 | equal | 44 | 44 | equal |
| data.summary.landed | landed | 1544 | 1547 | differ | 1544 | 1547 | differ | 1544 | 1548 | differ |
| data.summary.pct | 100*landed/all, one decimal | 82.7% | 82.8% | differ | 82.7% | 82.8% | differ | 82.7% | 82.8% | differ |
| data.tables.fleet.batman.done | tables.fleet.batman.done | 136 | 136 | equal | 136 | 136 | equal | 136 | 136 | equal |
| data.tables.fleet.batman.failed | tables.fleet.batman.failed | 38 | 38 | equal | 38 | 38 | equal | 38 | 38 | equal |
| data.tables.fleet.batman.load | tables.fleet.batman.load | 1.0% | 1.0% | equal | 1.0% | 0.0% | differ | 1.0% | 0.0% | differ |
| data.tables.fleet.batman.ok | tables.fleet.batman.ok | 98 | 98 | equal | 98 | 98 | equal | 98 | 98 | equal |
| data.tables.fleet.batman.okpct | tables.fleet.batman.okpct | 72.1% | 72.1% | equal | 72.1% | 72.1% | equal | 72.1% | 72.1% | equal |
| data.tables.fleet.batman.ready | tables.fleet.batman.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.batman.width | tables.fleet.batman.width | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.fleet.batman.withdrawn | tables.fleet.batman.withdrawn | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.batman.working | tables.fleet.batman.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.captain.done | tables.fleet.captain.done | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.captain.failed | tables.fleet.captain.failed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.captain.ok | tables.fleet.captain.ok | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.captain.okpct | tables.fleet.captain.okpct | 0.0% | 0.0% | equal | 0.0% | 0.0% | equal | 0.0% | 0.0% | equal |
| data.tables.fleet.captain.ready | tables.fleet.captain.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.captain.width | tables.fleet.captain.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.fleet.captain.withdrawn | tables.fleet.captain.withdrawn | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.captain.working | tables.fleet.captain.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.hetzner.done | tables.fleet.hetzner.done | 750 | 750 | equal | 750 | 750 | equal | 750 | 750 | equal |
| data.tables.fleet.hetzner.failed | tables.fleet.hetzner.failed | 231 | 231 | equal | 231 | 231 | equal | 231 | 231 | equal |
| data.tables.fleet.hetzner.load | tables.fleet.hetzner.load | 0.5% | 0.6% | differ | 0.5% | 0.6% | differ | 0.5% | 96.7% | differ |
| data.tables.fleet.hetzner.ok | tables.fleet.hetzner.ok | 519 | 519 | equal | 519 | 519 | equal | 519 | 519 | equal |
| data.tables.fleet.hetzner.okpct | tables.fleet.hetzner.okpct | 69.2% | 69.2% | equal | 69.2% | 69.2% | equal | 69.2% | 69.2% | equal |
| data.tables.fleet.hetzner.ready | tables.fleet.hetzner.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.hetzner.width | tables.fleet.hetzner.width | 16 | 16 | equal | 16 | 16 | equal | 16 | 16 | equal |
| data.tables.fleet.hetzner.withdrawn | tables.fleet.hetzner.withdrawn | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.hetzner.working | tables.fleet.hetzner.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.hulk.done | tables.fleet.hulk.done | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.hulk.failed | tables.fleet.hulk.failed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.hulk.ok | tables.fleet.hulk.ok | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.hulk.okpct | tables.fleet.hulk.okpct | 0.0% | 0.0% | equal | 0.0% | 0.0% | equal | 0.0% | 0.0% | equal |
| data.tables.fleet.hulk.ready | tables.fleet.hulk.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.hulk.width | tables.fleet.hulk.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.fleet.hulk.withdrawn | tables.fleet.hulk.withdrawn | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.hulk.working | tables.fleet.hulk.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.space.done | tables.fleet.space.done | 1187 | 1187 | equal | 1187 | 1187 | equal | 1187 | 1187 | equal |
| data.tables.fleet.space.failed | tables.fleet.space.failed | 381 | 381 | equal | 381 | 381 | equal | 381 | 381 | equal |
| data.tables.fleet.space.load | tables.fleet.space.load | 1.0% | 2.5% | differ | 1.0% | 1.5% | differ | 1.0% | 2.6% | differ |
| data.tables.fleet.space.ok | tables.fleet.space.ok | 806 | 806 | equal | 806 | 806 | equal | 806 | 806 | equal |
| data.tables.fleet.space.okpct | tables.fleet.space.okpct | 67.9% | 67.9% | equal | 67.9% | 67.9% | equal | 67.9% | 67.9% | equal |
| data.tables.fleet.space.ready | tables.fleet.space.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.space.width | tables.fleet.space.width | 24 | 24 | equal | 24 | 24 | equal | 24 | 24 | equal |
| data.tables.fleet.space.withdrawn | tables.fleet.space.withdrawn | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.space.working | tables.fleet.space.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.studio.done | tables.fleet.studio.done | 274 | 274 | equal | 274 | 274 | equal | 274 | 274 | equal |
| data.tables.fleet.studio.failed | tables.fleet.studio.failed | 121 | 121 | equal | 121 | 121 | equal | 121 | 121 | equal |
| data.tables.fleet.studio.load | tables.fleet.studio.load | 43.0% | 33.0% | differ | 43.0% | 28.0% | differ | 43.0% | 37.0% | differ |
| data.tables.fleet.studio.ok | tables.fleet.studio.ok | 153 | 153 | equal | 153 | 153 | equal | 153 | 153 | equal |
| data.tables.fleet.studio.okpct | tables.fleet.studio.okpct | 55.8% | 55.8% | equal | 55.8% | 55.8% | equal | 55.8% | 55.8% | equal |
| data.tables.fleet.studio.ready | tables.fleet.studio.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.studio.width | tables.fleet.studio.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.fleet.studio.withdrawn | tables.fleet.studio.withdrawn | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.studio.working | tables.fleet.studio.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.superman.done | tables.fleet.superman.done | 145 | 145 | equal | 145 | 145 | equal | 145 | 145 | equal |
| data.tables.fleet.superman.failed | tables.fleet.superman.failed | 115 | 115 | equal | 115 | 115 | equal | 115 | 115 | equal |
| data.tables.fleet.superman.load | tables.fleet.superman.load | 2.0% | 2.0% | equal | 2.0% | 3.0% | differ | 2.0% | 2.0% | equal |
| data.tables.fleet.superman.ok | tables.fleet.superman.ok | 30 | 30 | equal | 30 | 30 | equal | 30 | 30 | equal |
| data.tables.fleet.superman.okpct | tables.fleet.superman.okpct | 20.7% | 20.7% | equal | 20.7% | 20.7% | equal | 20.7% | 20.7% | equal |
| data.tables.fleet.superman.ready | tables.fleet.superman.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.superman.width | tables.fleet.superman.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.fleet.superman.withdrawn | tables.fleet.superman.withdrawn | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.superman.working | tables.fleet.superman.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.vision.done | tables.fleet.vision.done | 500 | 500 | equal | 500 | 500 | equal | 500 | 500 | equal |
| data.tables.fleet.vision.failed | tables.fleet.vision.failed | 153 | 153 | equal | 153 | 153 | equal | 153 | 153 | equal |
| data.tables.fleet.vision.load | tables.fleet.vision.load | 18.4% | 0.5% | differ | 18.4% | 0.8% | differ | 18.4% | 2.1% | differ |
| data.tables.fleet.vision.ok | tables.fleet.vision.ok | 347 | 347 | equal | 347 | 347 | equal | 347 | 347 | equal |
| data.tables.fleet.vision.okpct | tables.fleet.vision.okpct | 69.4% | 69.4% | equal | 69.4% | 69.4% | equal | 69.4% | 69.4% | equal |
| data.tables.fleet.vision.ready | tables.fleet.vision.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.vision.width | tables.fleet.vision.width | 16 | 16 | equal | 16 | 16 | equal | 16 | 16 | equal |
| data.tables.fleet.vision.withdrawn | tables.fleet.vision.withdrawn | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.fleet.vision.working | tables.fleet.vision.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.alex.done | tables.friends.alex.done | 23 | 23 | equal | 23 | 23 | equal | 23 | 23 | equal |
| data.tables.friends.alex.failed | tables.friends.alex.failed | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.friends.alex.ok | tables.friends.alex.ok | 17 | 17 | equal | 17 | 17 | equal | 17 | 17 | equal |
| data.tables.friends.alex.okpct | tables.friends.alex.okpct | 73.9% | 73.9% | equal | 73.9% | 73.9% | equal | 73.9% | 73.9% | equal |
| data.tables.friends.alex.ready | tables.friends.alex.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.alex.width | tables.friends.alex.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.friends.alex.working | tables.friends.alex.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.emma.done | tables.friends.emma.done | 323 | 323 | equal | 323 | 323 | equal | 323 | 323 | equal |
| data.tables.friends.emma.failed | tables.friends.emma.failed | 20 | 20 | equal | 20 | 20 | equal | 20 | 20 | equal |
| data.tables.friends.emma.ok | tables.friends.emma.ok | 303 | 303 | equal | 303 | 303 | equal | 303 | 303 | equal |
| data.tables.friends.emma.okpct | tables.friends.emma.okpct | 93.8% | 93.8% | equal | 93.8% | 93.8% | equal | 93.8% | 93.8% | equal |
| data.tables.friends.emma.ready | tables.friends.emma.ready | 6 | 0 | differ | 6 | 0 | differ | 6 | 0 | differ |
| data.tables.friends.emma.width | tables.friends.emma.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.friends.emma.working | tables.friends.emma.working | 10 | 0 | differ | 10 | 0 | differ | 10 | 0 | differ |
| data.tables.friends.freddy.done | tables.friends.freddy.done | 104 | 104 | equal | 104 | 104 | equal | 104 | 104 | equal |
| data.tables.friends.freddy.failed | tables.friends.freddy.failed | 31 | 31 | equal | 31 | 31 | equal | 31 | 31 | equal |
| data.tables.friends.freddy.ok | tables.friends.freddy.ok | 73 | 73 | equal | 73 | 73 | equal | 73 | 73 | equal |
| data.tables.friends.freddy.okpct | tables.friends.freddy.okpct | 70.2% | 70.2% | equal | 70.2% | 70.2% | equal | 70.2% | 70.2% | equal |
| data.tables.friends.freddy.ready | tables.friends.freddy.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.freddy.width | tables.friends.freddy.width | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.tables.friends.freddy.working | tables.friends.freddy.working | 5 | 0 | differ | 5 | 0 | differ | 5 | 0 | differ |
| data.tables.friends.johnny.done | tables.friends.johnny.done | 117 | 118 | differ | 117 | 118 | differ | 117 | 118 | differ |
| data.tables.friends.johnny.failed | tables.friends.johnny.failed | 28 | 29 | differ | 28 | 29 | differ | 28 | 29 | differ |
| data.tables.friends.johnny.ok | tables.friends.johnny.ok | 89 | 89 | equal | 89 | 89 | equal | 89 | 89 | equal |
| data.tables.friends.johnny.okpct | tables.friends.johnny.okpct | 76.1% | 75.4% | differ | 76.1% | 75.4% | differ | 76.1% | 75.4% | differ |
| data.tables.friends.johnny.ready | tables.friends.johnny.ready | 8 | 0 | differ | 8 | 2 | differ | 8 | 2 | differ |
| data.tables.friends.johnny.width | tables.friends.johnny.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.friends.johnny.working | tables.friends.johnny.working | 8 | 5 | differ | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.friends.rowan-mas.done | tables.friends.rowan-mas.done | 323 | 323 | equal | 323 | 323 | equal | 323 | 323 | equal |
| data.tables.friends.rowan-mas.failed | tables.friends.rowan-mas.failed | 122 | 122 | equal | 122 | 122 | equal | 122 | 122 | equal |
| data.tables.friends.rowan-mas.ok | tables.friends.rowan-mas.ok | 201 | 201 | equal | 201 | 201 | equal | 201 | 201 | equal |
| data.tables.friends.rowan-mas.okpct | tables.friends.rowan-mas.okpct | 62.2% | 62.2% | equal | 62.2% | 62.2% | equal | 62.2% | 62.2% | equal |
| data.tables.friends.rowan-mas.ready | tables.friends.rowan-mas.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.rowan-mas.width | tables.friends.rowan-mas.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.friends.rowan-mas.working | tables.friends.rowan-mas.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.rowan-next.done | tables.friends.rowan-next.done | 18 | 18 | equal | 18 | 18 | equal | 18 | 18 | equal |
| data.tables.friends.rowan-next.failed | tables.friends.rowan-next.failed | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.friends.rowan-next.ok | tables.friends.rowan-next.ok | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.friends.rowan-next.okpct | tables.friends.rowan-next.okpct | 72.2% | 72.2% | equal | 72.2% | 72.2% | equal | 72.2% | 72.2% | equal |
| data.tables.friends.rowan-next.ready | tables.friends.rowan-next.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.rowan-next.width | tables.friends.rowan-next.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.friends.rowan-next.working | tables.friends.rowan-next.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.rowan-personal.done | tables.friends.rowan-personal.done | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.rowan-personal.failed | tables.friends.rowan-personal.failed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.rowan-personal.ok | tables.friends.rowan-personal.ok | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.rowan-personal.okpct | tables.friends.rowan-personal.okpct | 0.0% | 0.0% | equal | 0.0% | 0.0% | equal | 0.0% | 0.0% | equal |
| data.tables.friends.rowan-personal.ready | tables.friends.rowan-personal.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.rowan-personal.width | tables.friends.rowan-personal.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.friends.rowan-personal.working | tables.friends.rowan-personal.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.rowan-space.done | tables.friends.rowan-space.done | 33 | 46 | differ | 33 | 47 | differ | 33 | 47 | differ |
| data.tables.friends.rowan-space.failed | tables.friends.rowan-space.failed | 8 | 11 | differ | 8 | 11 | differ | 8 | 11 | differ |
| data.tables.friends.rowan-space.ok | tables.friends.rowan-space.ok | 25 | 35 | differ | 25 | 36 | differ | 25 | 36 | differ |
| data.tables.friends.rowan-space.okpct | tables.friends.rowan-space.okpct | 75.8% | 76.1% | differ | 75.8% | 76.6% | differ | 75.8% | 76.6% | differ |
| data.tables.friends.rowan-space.ready | tables.friends.rowan-space.ready | 8 | 0 | differ | 8 | 0 | differ | 8 | 0 | differ |
| data.tables.friends.rowan-space.width | tables.friends.rowan-space.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.friends.rowan-space.working | tables.friends.rowan-space.working | 8 | 6 | differ | 8 | 0 | differ | 8 | 0 | differ |
| data.tables.friends.rowan.done | tables.friends.rowan.done | 79 | 79 | equal | 79 | 79 | equal | 79 | 79 | equal |
| data.tables.friends.rowan.failed | tables.friends.rowan.failed | 23 | 23 | equal | 23 | 23 | equal | 23 | 23 | equal |
| data.tables.friends.rowan.ok | tables.friends.rowan.ok | 56 | 56 | equal | 56 | 56 | equal | 56 | 56 | equal |
| data.tables.friends.rowan.okpct | tables.friends.rowan.okpct | 70.9% | 70.9% | equal | 70.9% | 70.9% | equal | 70.9% | 70.9% | equal |
| data.tables.friends.rowan.ready | tables.friends.rowan.ready | 6 | 0 | differ | 6 | 0 | differ | 6 | 0 | differ |
| data.tables.friends.rowan.width | tables.friends.rowan.width | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.friends.rowan.working | tables.friends.rowan.working | 6 | 0 | differ | 6 | 0 | differ | 6 | 0 | differ |
| data.tables.friends.stella.done | tables.friends.stella.done | 68 | 74 | differ | 68 | 75 | differ | 68 | 75 | differ |
| data.tables.friends.stella.failed | tables.friends.stella.failed | 37 | 42 | differ | 37 | 42 | differ | 37 | 42 | differ |
| data.tables.friends.stella.ok | tables.friends.stella.ok | 31 | 32 | differ | 31 | 33 | differ | 31 | 33 | differ |
| data.tables.friends.stella.okpct | tables.friends.stella.okpct | 45.6% | 43.2% | differ | 45.6% | 44.0% | differ | 45.6% | 44.0% | differ |
| data.tables.friends.stella.ready | tables.friends.stella.ready | 2 | 0 | differ | 2 | 0 | differ | 2 | 0 | differ |
| data.tables.friends.stella.width | tables.friends.stella.width | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.friends.stella.working | tables.friends.stella.working | 2 | 1 | differ | 2 | 0 | differ | 2 | 0 | differ |
| data.tables.friends.zhi.done | tables.friends.zhi.done | 31 | 31 | equal | 31 | 31 | equal | 31 | 31 | equal |
| data.tables.friends.zhi.failed | tables.friends.zhi.failed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.friends.zhi.ok | tables.friends.zhi.ok | 29 | 29 | equal | 29 | 29 | equal | 29 | 29 | equal |
| data.tables.friends.zhi.okpct | tables.friends.zhi.okpct | 93.5% | 93.5% | equal | 93.5% | 93.5% | equal | 93.5% | 93.5% | equal |
| data.tables.friends.zhi.ready | tables.friends.zhi.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.friends.zhi.width | tables.friends.zhi.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.friends.zhi.working | tables.friends.zhi.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.adopt-2026-10-04.merged | tables.merge.adopt-2026-10-04.merged | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.merge.adopt-2026-10-04.queued | tables.merge.adopt-2026-10-04.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.adopt-2026-10-04.returned | tables.merge.adopt-2026-10-04.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.adopt-2026-10-04.stuck | tables.merge.adopt-2026-10-04.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.bus.merged | tables.merge.bus.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.bus.queued | tables.merge.bus.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.bus.returned | tables.merge.bus.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.bus.stuck | tables.merge.bus.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.busdogfood2.merged | tables.merge.busdogfood2.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.busdogfood2.queued | tables.merge.busdogfood2.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.busdogfood2.returned | tables.merge.busdogfood2.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.busdogfood2.stuck | tables.merge.busdogfood2.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ci.merged | tables.merge.ci.merged | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.merge.ci.queued | tables.merge.ci.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ci.returned | tables.merge.ci.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ci.stuck | tables.merge.ci.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.classes.merged | tables.merge.classes.merged | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.classes.queued | tables.merge.classes.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.classes.returned | tables.merge.classes.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.classes.stuck | tables.merge.classes.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.contract.merged | tables.merge.contract.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.contract.queued | tables.merge.contract.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.contract.returned | tables.merge.contract.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.contract.stuck | tables.merge.contract.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.coverage.merged | tables.merge.coverage.merged | 149 | 149 | equal | 149 | 149 | equal | 149 | 149 | equal |
| data.tables.merge.coverage.queued | tables.merge.coverage.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.coverage.returned | tables.merge.coverage.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.coverage.stuck | tables.merge.coverage.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.coverage2.merged | tables.merge.coverage2.merged | 113 | 113 | equal | 113 | 113 | equal | 113 | 113 | equal |
| data.tables.merge.coverage2.queued | tables.merge.coverage2.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.coverage2.returned | tables.merge.coverage2.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.coverage2.stuck | tables.merge.coverage2.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.dead.merged | tables.merge.dead.merged | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.merge.dead.queued | tables.merge.dead.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.dead.returned | tables.merge.dead.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.dead.stuck | tables.merge.dead.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.debt.merged | tables.merge.debt.merged | 168 | 168 | equal | 168 | 168 | equal | 168 | 168 | equal |
| data.tables.merge.debt.queued | tables.merge.debt.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.debt.returned | tables.merge.debt.returned | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.merge.debt.stuck | tables.merge.debt.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.diary.merged | tables.merge.diary.merged | 146 | 146 | equal | 146 | 146 | equal | 146 | 146 | equal |
| data.tables.merge.diary.queued | tables.merge.diary.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.diary.returned | tables.merge.diary.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.diary.stuck | tables.merge.diary.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.docs.merged | tables.merge.docs.merged | 58 | 58 | equal | 58 | 58 | equal | 58 | 58 | equal |
| data.tables.merge.docs.queued | tables.merge.docs.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.docs.returned | tables.merge.docs.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.docs.stuck | tables.merge.docs.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.findings.merged | tables.merge.findings.merged | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.merge.findings.queued | tables.merge.findings.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.findings.returned | tables.merge.findings.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.findings.stuck | tables.merge.findings.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.fixes.merged | tables.merge.fixes.merged | 24 | 24 | equal | 24 | 24 | equal | 24 | 24 | equal |
| data.tables.merge.fixes.queued | tables.merge.fixes.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.fixes.returned | tables.merge.fixes.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.fixes.stuck | tables.merge.fixes.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.flash50.merged | tables.merge.flash50.merged | 50 | 50 | equal | 50 | 50 | equal | 50 | 50 | equal |
| data.tables.merge.flash50.queued | tables.merge.flash50.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.flash50.returned | tables.merge.flash50.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.flash50.stuck | tables.merge.flash50.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.fleetnames.merged | tables.merge.fleetnames.merged | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.merge.fleetnames.queued | tables.merge.fleetnames.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.fleetnames.returned | tables.merge.fleetnames.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.fleetnames.stuck | tables.merge.fleetnames.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.frictions2.merged | tables.merge.frictions2.merged | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.frictions2.queued | tables.merge.frictions2.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.frictions2.returned | tables.merge.frictions2.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.frictions2.stuck | tables.merge.frictions2.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friend-reserve.merged | tables.merge.friend-reserve.merged | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.friend-reserve.queued | tables.merge.friend-reserve.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friend-reserve.returned | tables.merge.friend-reserve.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friend-reserve.stuck | tables.merge.friend-reserve.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friend-test.merged | tables.merge.friend-test.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friend-test.queued | tables.merge.friend-test.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friend-test.returned | tables.merge.friend-test.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friend-test.stuck | tables.merge.friend-test.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friends-general-v1-2-0.merged | tables.merge.friends-general-v1-2-0.merged | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.friends-general-v1-2-0.queued | tables.merge.friends-general-v1-2-0.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friends-general-v1-2-0.returned | tables.merge.friends-general-v1-2-0.returned | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.friends-general-v1-2-0.stuck | tables.merge.friends-general-v1-2-0.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friends-sprint-v1-0-0.merged | tables.merge.friends-sprint-v1-0-0.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.friends-sprint-v1-0-0.queued | tables.merge.friends-sprint-v1-0-0.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friends-sprint-v1-0-0.returned | tables.merge.friends-sprint-v1-0-0.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friends-sprint-v1-0-0.stuck | tables.merge.friends-sprint-v1-0-0.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friends-v1-2-0-reliability.merged | tables.merge.friends-v1-2-0-reliability.merged | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.merge.friends-v1-2-0-reliability.queued | tables.merge.friends-v1-2-0-reliability.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.friends-v1-2-0-reliability.returned | tables.merge.friends-v1-2-0-reliability.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.friends-v1-2-0-reliability.stuck | tables.merge.friends-v1-2-0-reliability.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ftsync.merged | tables.merge.ftsync.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ftsync.queued | tables.merge.ftsync.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ftsync.returned | tables.merge.ftsync.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ftsync.stuck | tables.merge.ftsync.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.general.merged | tables.merge.general.merged | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.merge.general.queued | tables.merge.general.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.general.returned | tables.merge.general.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.general.stuck | tables.merge.general.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.harness.merged | tables.merge.harness.merged | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.merge.harness.queued | tables.merge.harness.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.harness.returned | tables.merge.harness.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.harness.stuck | tables.merge.harness.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.heavy-alex.merged | tables.merge.heavy-alex.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.heavy-alex.queued | tables.merge.heavy-alex.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.heavy-alex.returned | tables.merge.heavy-alex.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.heavy-alex.stuck | tables.merge.heavy-alex.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.heavy-emma.merged | tables.merge.heavy-emma.merged | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.merge.heavy-emma.queued | tables.merge.heavy-emma.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.heavy-emma.returned | tables.merge.heavy-emma.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.heavy-emma.stuck | tables.merge.heavy-emma.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.heavy-rowan.merged | tables.merge.heavy-rowan.merged | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.merge.heavy-rowan.queued | tables.merge.heavy-rowan.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.heavy-rowan.returned | tables.merge.heavy-rowan.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.heavy-rowan.stuck | tables.merge.heavy-rowan.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.help.merged | tables.merge.help.merged | 15 | 15 | equal | 15 | 15 | equal | 15 | 15 | equal |
| data.tables.merge.help.queued | tables.merge.help.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.help.returned | tables.merge.help.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.help.stuck | tables.merge.help.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.libs.merged | tables.merge.libs.merged | 7 | 7 | equal | 7 | 7 | equal | 7 | 7 | equal |
| data.tables.merge.libs.queued | tables.merge.libs.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.libs.returned | tables.merge.libs.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.libs.stuck | tables.merge.libs.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.lint.merged | tables.merge.lint.merged | 51 | 51 | equal | 51 | 51 | equal | 51 | 51 | equal |
| data.tables.merge.lint.queued | tables.merge.lint.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.lint.returned | tables.merge.lint.returned | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.lint.stuck | tables.merge.lint.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.machinery2.merged | tables.merge.machinery2.merged | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.merge.machinery2.queued | tables.merge.machinery2.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.machinery2.returned | tables.merge.machinery2.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.machinery2.stuck | tables.merge.machinery2.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.meaning.merged | tables.merge.meaning.merged | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.merge.meaning.queued | tables.merge.meaning.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.meaning.returned | tables.merge.meaning.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.meaning.stuck | tables.merge.meaning.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.missed-2026-10-04.merged | tables.merge.missed-2026-10-04.merged | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.merge.missed-2026-10-04.queued | tables.merge.missed-2026-10-04.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.missed-2026-10-04.returned | tables.merge.missed-2026-10-04.returned | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.merge.missed-2026-10-04.stuck | tables.merge.missed-2026-10-04.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.models.merged | tables.merge.models.merged | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.merge.models.queued | tables.merge.models.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.models.returned | tables.merge.models.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.models.stuck | tables.merge.models.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.modeltests.merged | tables.merge.modeltests.merged | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.modeltests.queued | tables.merge.modeltests.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.modeltests.returned | tables.merge.modeltests.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.modeltests.stuck | tables.merge.modeltests.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.names.merged | tables.merge.names.merged | 31 | 31 | equal | 31 | 31 | equal | 31 | 31 | equal |
| data.tables.merge.names.queued | tables.merge.names.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.names.returned | tables.merge.names.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.names.stuck | tables.merge.names.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ncci.merged | tables.merge.ncci.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.ncci.queued | tables.merge.ncci.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ncci.returned | tables.merge.ncci.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ncci.stuck | tables.merge.ncci.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ncex.merged | tables.merge.ncex.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.ncex.queued | tables.merge.ncex.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ncex.returned | tables.merge.ncex.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ncex.stuck | tables.merge.ncex.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ncreal.merged | tables.merge.ncreal.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.ncreal.queued | tables.merge.ncreal.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ncreal.returned | tables.merge.ncreal.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ncreal.stuck | tables.merge.ncreal.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.negatives.merged | tables.merge.negatives.merged | 63 | 63 | equal | 63 | 63 | equal | 63 | 63 | equal |
| data.tables.merge.negatives.queued | tables.merge.negatives.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.negatives.returned | tables.merge.negatives.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.negatives.stuck | tables.merge.negatives.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.no-shell-v1-2-0.merged | tables.merge.no-shell-v1-2-0.merged | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.no-shell-v1-2-0.queued | tables.merge.no-shell-v1-2-0.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.no-shell-v1-2-0.returned | tables.merge.no-shell-v1-2-0.returned | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.no-shell-v1-2-0.stuck | tables.merge.no-shell-v1-2-0.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.nongo.merged | tables.merge.nongo.merged | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.merge.nongo.queued | tables.merge.nongo.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.nongo.returned | tables.merge.nongo.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.nongo.stuck | tables.merge.nongo.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.notes.merged | tables.merge.notes.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.notes.queued | tables.merge.notes.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.notes.returned | tables.merge.notes.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.notes.stuck | tables.merge.notes.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.nova-sprint-split.merged | tables.merge.nova-sprint-split.merged | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.nova-sprint-split.queued | tables.merge.nova-sprint-split.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.nova-sprint-split.returned | tables.merge.nova-sprint-split.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.nova-sprint-split.stuck | tables.merge.nova-sprint-split.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.nova.merged | tables.merge.nova.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.nova.queued | tables.merge.nova.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.nova.returned | tables.merge.nova.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.nova.stuck | tables.merge.nova.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.onething.merged | tables.merge.onething.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.onething.queued | tables.merge.onething.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.onething.returned | tables.merge.onething.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.onething.stuck | tables.merge.onething.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.promote-red-2026-10-05.merged | tables.merge.promote-red-2026-10-05.merged | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.merge.promote-red-2026-10-05.queued | tables.merge.promote-red-2026-10-05.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.promote-red-2026-10-05.returned | tables.merge.promote-red-2026-10-05.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.promote-red-2026-10-05.stuck | tables.merge.promote-red-2026-10-05.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.prose.merged | tables.merge.prose.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.prose.queued | tables.merge.prose.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.prose.returned | tables.merge.prose.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.prose.stuck | tables.merge.prose.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.quality.merged | tables.merge.quality.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.quality.queued | tables.merge.quality.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.quality.returned | tables.merge.quality.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.quality.stuck | tables.merge.quality.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rate-tools-dev-2026-10-05.merged | tables.merge.rate-tools-dev-2026-10-05.merged | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.rate-tools-dev-2026-10-05.queued | tables.merge.rate-tools-dev-2026-10-05.queued | 14 | 14 | equal | 14 | 14 | equal | 14 | 14 | equal |
| data.tables.merge.rate-tools-dev-2026-10-05.returned | tables.merge.rate-tools-dev-2026-10-05.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rate-tools-dev-2026-10-05.stuck | tables.merge.rate-tools-dev-2026-10-05.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-alex.merged | tables.merge.ratings-alex.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.ratings-alex.queued | tables.merge.ratings-alex.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-alex.returned | tables.merge.ratings-alex.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-alex.stuck | tables.merge.ratings-alex.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-emma.merged | tables.merge.ratings-emma.merged | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.tables.merge.ratings-emma.queued | tables.merge.ratings-emma.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-emma.returned | tables.merge.ratings-emma.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-emma.stuck | tables.merge.ratings-emma.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-freddy.merged | tables.merge.ratings-freddy.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-freddy.queued | tables.merge.ratings-freddy.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-freddy.returned | tables.merge.ratings-freddy.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-freddy.stuck | tables.merge.ratings-freddy.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-johnny.merged | tables.merge.ratings-johnny.merged | 15 | 15 | equal | 15 | 15 | equal | 15 | 15 | equal |
| data.tables.merge.ratings-johnny.queued | tables.merge.ratings-johnny.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-johnny.returned | tables.merge.ratings-johnny.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-johnny.stuck | tables.merge.ratings-johnny.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-stella.merged | tables.merge.ratings-stella.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-stella.queued | tables.merge.ratings-stella.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-stella.returned | tables.merge.ratings-stella.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-stella.stuck | tables.merge.ratings-stella.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-zhi.merged | tables.merge.ratings-zhi.merged | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.merge.ratings-zhi.queued | tables.merge.ratings-zhi.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-zhi.returned | tables.merge.ratings-zhi.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ratings-zhi.stuck | tables.merge.ratings-zhi.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.read.merged | tables.merge.read.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.read.queued | tables.merge.read.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.read.returned | tables.merge.read.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.read.stuck | tables.merge.read.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.reads.merged | tables.merge.reads.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.reads.queued | tables.merge.reads.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.reads.returned | tables.merge.reads.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.reads.stuck | tables.merge.reads.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.reland-2026-10-04.merged | tables.merge.reland-2026-10-04.merged | 87 | 87 | equal | 87 | 87 | equal | 87 | 87 | equal |
| data.tables.merge.reland-2026-10-04.queued | tables.merge.reland-2026-10-04.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.reland-2026-10-04.returned | tables.merge.reland-2026-10-04.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.reland-2026-10-04.stuck | tables.merge.reland-2026-10-04.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.reliability-now.merged | tables.merge.reliability-now.merged | 2 | 5 | differ | 2 | 5 | differ | 2 | 5 | differ |
| data.tables.merge.reliability-now.queued | tables.merge.reliability-now.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.reliability-now.returned | tables.merge.reliability-now.returned | 1 | 2 | differ | 1 | 2 | differ | 1 | 2 | differ |
| data.tables.merge.reliability-now.stuck | tables.merge.reliability-now.stuck | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.merge.repair-rolling.merged | tables.merge.repair-rolling.merged | 26 | 26 | equal | 26 | 26 | equal | 26 | 26 | equal |
| data.tables.merge.repair-rolling.queued | tables.merge.repair-rolling.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.repair-rolling.returned | tables.merge.repair-rolling.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.repair-rolling.stuck | tables.merge.repair-rolling.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rerate2.merged | tables.merge.rerate2.merged | 33 | 33 | equal | 33 | 33 | equal | 33 | 33 | equal |
| data.tables.merge.rerate2.queued | tables.merge.rerate2.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rerate2.returned | tables.merge.rerate2.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rerate2.stuck | tables.merge.rerate2.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rlci.merged | tables.merge.rlci.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.rlci.queued | tables.merge.rlci.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rlci.returned | tables.merge.rlci.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rlci.stuck | tables.merge.rlci.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rlclean.merged | tables.merge.rlclean.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.rlclean.queued | tables.merge.rlclean.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rlclean.returned | tables.merge.rlclean.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rlclean.stuck | tables.merge.rlclean.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rltests.merged | tables.merge.rltests.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.rltests.queued | tables.merge.rltests.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rltests.returned | tables.merge.rltests.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rltests.stuck | tables.merge.rltests.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rowan-only-to-product.merged | tables.merge.rowan-only-to-product.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rowan-only-to-product.queued | tables.merge.rowan-only-to-product.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rowan-only-to-product.returned | tables.merge.rowan-only-to-product.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rowan-only-to-product.stuck | tables.merge.rowan-only-to-product.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtclose.merged | tables.merge.rtclose.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.rtclose.queued | tables.merge.rtclose.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtclose.returned | tables.merge.rtclose.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtclose.stuck | tables.merge.rtclose.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtdead.merged | tables.merge.rtdead.merged | 7 | 7 | equal | 7 | 7 | equal | 7 | 7 | equal |
| data.tables.merge.rtdead.queued | tables.merge.rtdead.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtdead.returned | tables.merge.rtdead.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtdead.stuck | tables.merge.rtdead.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtfix.merged | tables.merge.rtfix.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.rtfix.queued | tables.merge.rtfix.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtfix.returned | tables.merge.rtfix.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtfix.stuck | tables.merge.rtfix.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtfleet.merged | tables.merge.rtfleet.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.rtfleet.queued | tables.merge.rtfleet.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtfleet.returned | tables.merge.rtfleet.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtfleet.stuck | tables.merge.rtfleet.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtgate.merged | tables.merge.rtgate.merged | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.rtgate.queued | tables.merge.rtgate.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtgate.returned | tables.merge.rtgate.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtgate.stuck | tables.merge.rtgate.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtport.merged | tables.merge.rtport.merged | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.rtport.queued | tables.merge.rtport.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtport.returned | tables.merge.rtport.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.rtport.stuck | tables.merge.rtport.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.scripts-to-verbs.merged | tables.merge.scripts-to-verbs.merged | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.merge.scripts-to-verbs.queued | tables.merge.scripts-to-verbs.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.scripts-to-verbs.returned | tables.merge.scripts-to-verbs.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.scripts-to-verbs.stuck | tables.merge.scripts-to-verbs.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.seams.merged | tables.merge.seams.merged | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.merge.seams.queued | tables.merge.seams.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.seams.returned | tables.merge.seams.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.seams.stuck | tables.merge.seams.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.security.merged | tables.merge.security.merged | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.merge.security.queued | tables.merge.security.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.security.returned | tables.merge.security.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.security.stuck | tables.merge.security.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.security2.merged | tables.merge.security2.merged | 71 | 71 | equal | 71 | 71 | equal | 71 | 71 | equal |
| data.tables.merge.security2.queued | tables.merge.security2.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.security2.returned | tables.merge.security2.returned | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.security2.stuck | tables.merge.security2.stuck | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.shrink.merged | tables.merge.shrink.merged | 7 | 7 | equal | 7 | 7 | equal | 7 | 7 | equal |
| data.tables.merge.shrink.queued | tables.merge.shrink.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.shrink.returned | tables.merge.shrink.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.shrink.stuck | tables.merge.shrink.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.skeleton.merged | tables.merge.skeleton.merged | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.skeleton.queued | tables.merge.skeleton.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.skeleton.returned | tables.merge.skeleton.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.skeleton.stuck | tables.merge.skeleton.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.skips.merged | tables.merge.skips.merged | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.merge.skips.queued | tables.merge.skips.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.skips.returned | tables.merge.skips.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.skips.stuck | tables.merge.skips.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.split.merged | tables.merge.split.merged | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.merge.split.queued | tables.merge.split.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.split.returned | tables.merge.split.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.split.stuck | tables.merge.split.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-next.merged | tables.merge.sprint-next.merged | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.merge.sprint-next.queued | tables.merge.sprint-next.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-next.returned | tables.merge.sprint-next.returned | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.sprint-next.stuck | tables.merge.sprint-next.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-1-0.merged | tables.merge.sprint-v1-1-0.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-1-0.queued | tables.merge.sprint-v1-1-0.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-1-0.returned | tables.merge.sprint-v1-1-0.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-1-0.stuck | tables.merge.sprint-v1-1-0.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-comfort.merged | tables.merge.sprint-v1-comfort.merged | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.merge.sprint-v1-comfort.queued | tables.merge.sprint-v1-comfort.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-comfort.returned | tables.merge.sprint-v1-comfort.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-comfort.stuck | tables.merge.sprint-v1-comfort.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-docs.merged | tables.merge.sprint-v1-docs.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-docs.queued | tables.merge.sprint-v1-docs.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-docs.returned | tables.merge.sprint-v1-docs.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-docs.stuck | tables.merge.sprint-v1-docs.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-integrity.merged | tables.merge.sprint-v1-integrity.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.sprint-v1-integrity.queued | tables.merge.sprint-v1-integrity.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-integrity.returned | tables.merge.sprint-v1-integrity.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-integrity.stuck | tables.merge.sprint-v1-integrity.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-jev.merged | tables.merge.sprint-v1-jev.merged | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.sprint-v1-jev.queued | tables.merge.sprint-v1-jev.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-jev.returned | tables.merge.sprint-v1-jev.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-jev.stuck | tables.merge.sprint-v1-jev.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-models.merged | tables.merge.sprint-v1-models.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-models.queued | tables.merge.sprint-v1-models.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-models.returned | tables.merge.sprint-v1-models.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.sprint-v1-models.stuck | tables.merge.sprint-v1-models.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-processor.merged | tables.merge.sprint-v1-processor.merged | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.sprint-v1-processor.queued | tables.merge.sprint-v1-processor.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-processor.returned | tables.merge.sprint-v1-processor.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.sprint-v1-processor.stuck | tables.merge.sprint-v1-processor.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-release.merged | tables.merge.sprint-v1-release.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-release.queued | tables.merge.sprint-v1-release.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-release.returned | tables.merge.sprint-v1-release.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.sprint-v1-release.stuck | tables.merge.sprint-v1-release.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-safety.merged | tables.merge.sprint-v1-safety.merged | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.sprint-v1-safety.queued | tables.merge.sprint-v1-safety.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-safety.returned | tables.merge.sprint-v1-safety.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.sprint-v1-safety.stuck | tables.merge.sprint-v1-safety.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-setup.merged | tables.merge.sprint-v1-setup.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.sprint-v1-setup.queued | tables.merge.sprint-v1-setup.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-setup.returned | tables.merge.sprint-v1-setup.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-setup.stuck | tables.merge.sprint-v1-setup.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-simplicity.merged | tables.merge.sprint-v1-simplicity.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-simplicity.queued | tables.merge.sprint-v1-simplicity.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-simplicity.returned | tables.merge.sprint-v1-simplicity.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.sprint-v1-simplicity.stuck | tables.merge.sprint-v1-simplicity.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-sre.merged | tables.merge.sprint-v1-sre.merged | 8 | 8 | equal | 8 | 8 | equal | 8 | 9 | differ |
| data.tables.merge.sprint-v1-sre.queued | tables.merge.sprint-v1-sre.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-sre.returned | tables.merge.sprint-v1-sre.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.sprint-v1-sre.stuck | tables.merge.sprint-v1-sre.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-verbs.merged | tables.merge.sprint-v1-verbs.merged | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.merge.sprint-v1-verbs.queued | tables.merge.sprint-v1-verbs.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-verbs.returned | tables.merge.sprint-v1-verbs.returned | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.sprint-v1-verbs.stuck | tables.merge.sprint-v1-verbs.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-wallclock.merged | tables.merge.sprint-v1-wallclock.merged | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.sprint-v1-wallclock.queued | tables.merge.sprint-v1-wallclock.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-wallclock.returned | tables.merge.sprint-v1-wallclock.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-wallclock.stuck | tables.merge.sprint-v1-wallclock.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-yes.merged | tables.merge.sprint-v1-yes.merged | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.merge.sprint-v1-yes.queued | tables.merge.sprint-v1-yes.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-v1-yes.returned | tables.merge.sprint-v1-yes.returned | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.sprint-v1-yes.stuck | tables.merge.sprint-v1-yes.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-verbs-2026-10-04.merged | tables.merge.sprint-verbs-2026-10-04.merged | 16 | 16 | equal | 16 | 16 | equal | 16 | 16 | equal |
| data.tables.merge.sprint-verbs-2026-10-04.queued | tables.merge.sprint-verbs-2026-10-04.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-verbs-2026-10-04.returned | tables.merge.sprint-verbs-2026-10-04.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.sprint-verbs-2026-10-04.stuck | tables.merge.sprint-verbs-2026-10-04.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.status.merged | tables.merge.status.merged | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.merge.status.queued | tables.merge.status.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.status.returned | tables.merge.status.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.status.stuck | tables.merge.status.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szcfloat.merged | tables.merge.szcfloat.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.szcfloat.queued | tables.merge.szcfloat.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szcfloat.returned | tables.merge.szcfloat.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szcfloat.stuck | tables.merge.szcfloat.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szci.merged | tables.merge.szci.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.szci.queued | tables.merge.szci.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szci.returned | tables.merge.szci.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szci.stuck | tables.merge.szci.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szclean.merged | tables.merge.szclean.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.szclean.queued | tables.merge.szclean.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szclean.returned | tables.merge.szclean.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szclean.stuck | tables.merge.szclean.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szdegen.merged | tables.merge.szdegen.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.szdegen.queued | tables.merge.szdegen.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szdegen.returned | tables.merge.szdegen.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szdegen.stuck | tables.merge.szdegen.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szex.merged | tables.merge.szex.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.szex.queued | tables.merge.szex.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szex.returned | tables.merge.szex.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szex.stuck | tables.merge.szex.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szgolden.merged | tables.merge.szgolden.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.szgolden.queued | tables.merge.szgolden.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szgolden.returned | tables.merge.szgolden.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szgolden.stuck | tables.merge.szgolden.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szterm.merged | tables.merge.szterm.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.szterm.queued | tables.merge.szterm.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szterm.returned | tables.merge.szterm.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szterm.stuck | tables.merge.szterm.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szvalid.merged | tables.merge.szvalid.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.szvalid.queued | tables.merge.szvalid.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szvalid.returned | tables.merge.szvalid.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.szvalid.stuck | tables.merge.szvalid.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tenv.merged | tables.merge.tenv.merged | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.merge.tenv.queued | tables.merge.tenv.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tenv.returned | tables.merge.tenv.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tenv.stuck | tables.merge.tenv.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.testnames.merged | tables.merge.testnames.merged | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.merge.testnames.queued | tables.merge.testnames.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.testnames.returned | tables.merge.testnames.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.testnames.stuck | tables.merge.testnames.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tests.merged | tables.merge.tests.merged | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.merge.tests.queued | tables.merge.tests.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tests.returned | tables.merge.tests.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tests.stuck | tables.merge.tests.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tla.merged | tables.merge.tla.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tla.queued | tables.merge.tla.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tla.returned | tables.merge.tla.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tla.stuck | tables.merge.tla.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-efficiency.merged | tables.merge.today-efficiency.merged | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.merge.today-efficiency.queued | tables.merge.today-efficiency.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-efficiency.returned | tables.merge.today-efficiency.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-efficiency.stuck | tables.merge.today-efficiency.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-machinery.merged | tables.merge.today-machinery.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-machinery.queued | tables.merge.today-machinery.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-machinery.returned | tables.merge.today-machinery.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-machinery.stuck | tables.merge.today-machinery.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-tiers.merged | tables.merge.today-tiers.merged | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.merge.today-tiers.queued | tables.merge.today-tiers.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-tiers.returned | tables.merge.today-tiers.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.today-tiers.stuck | tables.merge.today-tiers.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.toolkit.merged | tables.merge.toolkit.merged | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.merge.toolkit.queued | tables.merge.toolkit.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.toolkit.returned | tables.merge.toolkit.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.toolkit.stuck | tables.merge.toolkit.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-docs.merged | tables.merge.tools-v1-2-0-docs.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.tools-v1-2-0-docs.queued | tables.merge.tools-v1-2-0-docs.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-docs.returned | tables.merge.tools-v1-2-0-docs.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-docs.stuck | tables.merge.tools-v1-2-0-docs.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-setup.merged | tables.merge.tools-v1-2-0-setup.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-setup.queued | tables.merge.tools-v1-2-0-setup.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-setup.returned | tables.merge.tools-v1-2-0-setup.returned | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.merge.tools-v1-2-0-setup.stuck | tables.merge.tools-v1-2-0-setup.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-stranger.merged | tables.merge.tools-v1-2-0-stranger.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-stranger.queued | tables.merge.tools-v1-2-0-stranger.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-stranger.returned | tables.merge.tools-v1-2-0-stranger.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.tools-v1-2-0-stranger.stuck | tables.merge.tools-v1-2-0-stranger.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ttime.merged | tables.merge.ttime.merged | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.merge.ttime.queued | tables.merge.ttime.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ttime.returned | tables.merge.ttime.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.ttime.stuck | tables.merge.ttime.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.use.merged | tables.merge.use.merged | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.use.queued | tables.merge.use.queued | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.use.returned | tables.merge.use.returned | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.merge.use.stuck | tables.merge.use.stuck | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-batman-2.asked | tables.readers.reader-batman-2.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-batman-2.broken | tables.readers.reader-batman-2.broken | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-batman-2.ok | tables.readers.reader-batman-2.ok | 23 | 23 | equal | 23 | 23 | equal | 23 | 23 | equal |
| data.tables.readers.reader-batman-2.reading | tables.readers.reader-batman-2.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-batman.asked | tables.readers.reader-batman.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-batman.broken | tables.readers.reader-batman.broken | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.readers.reader-batman.ok | tables.readers.reader-batman.ok | 224 | 224 | equal | 224 | 224 | equal | 224 | 224 | equal |
| data.tables.readers.reader-batman.reading | tables.readers.reader-batman.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-batman.width | tables.readers.reader-batman.width | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.readers.reader-emma.asked | tables.readers.reader-emma.asked | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.readers.reader-emma.broken | tables.readers.reader-emma.broken | 7 | 8 | differ | 7 | 8 | differ | 7 | 8 | differ |
| data.tables.readers.reader-emma.ok | tables.readers.reader-emma.ok | 58 | 61 | differ | 58 | 61 | differ | 58 | 62 | differ |
| data.tables.readers.reader-emma.reading | tables.readers.reader-emma.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-freddy.asked | tables.readers.reader-freddy.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-freddy.broken | tables.readers.reader-freddy.broken | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-freddy.ok | tables.readers.reader-freddy.ok | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-freddy.reading | tables.readers.reader-freddy.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-hetzner-2.asked | tables.readers.reader-hetzner-2.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-hetzner-2.broken | tables.readers.reader-hetzner-2.broken | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-hetzner-2.ok | tables.readers.reader-hetzner-2.ok | 43 | 43 | equal | 43 | 43 | equal | 43 | 43 | equal |
| data.tables.readers.reader-hetzner-2.reading | tables.readers.reader-hetzner-2.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-hetzner.asked | tables.readers.reader-hetzner.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-hetzner.broken | tables.readers.reader-hetzner.broken | 14 | 14 | equal | 14 | 14 | equal | 14 | 14 | equal |
| data.tables.readers.reader-hetzner.ok | tables.readers.reader-hetzner.ok | 361 | 361 | equal | 361 | 361 | equal | 361 | 361 | equal |
| data.tables.readers.reader-hetzner.reading | tables.readers.reader-hetzner.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-hetzner.width | tables.readers.reader-hetzner.width | 16 | 16 | equal | 16 | 16 | equal | 16 | 16 | equal |
| data.tables.readers.reader-johnny.asked | tables.readers.reader-johnny.asked | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.readers.reader-johnny.broken | tables.readers.reader-johnny.broken | 36 | 41 | differ | 36 | 43 | differ | 36 | 43 | differ |
| data.tables.readers.reader-johnny.ok | tables.readers.reader-johnny.ok | 87 | 88 | differ | 87 | 89 | differ | 87 | 89 | differ |
| data.tables.readers.reader-johnny.reading | tables.readers.reader-johnny.reading | 2 | 2 | equal | 2 | 0 | differ | 2 | 0 | differ |
| data.tables.readers.reader-rowan-coord.asked | tables.readers.reader-rowan-coord.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan-coord.broken | tables.readers.reader-rowan-coord.broken | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan-coord.ok | tables.readers.reader-rowan-coord.ok | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan-coord.reading | tables.readers.reader-rowan-coord.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan-mas.asked | tables.readers.reader-rowan-mas.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan-mas.broken | tables.readers.reader-rowan-mas.broken | 22 | 22 | equal | 22 | 22 | equal | 22 | 22 | equal |
| data.tables.readers.reader-rowan-mas.ok | tables.readers.reader-rowan-mas.ok | 183 | 183 | equal | 183 | 183 | equal | 183 | 183 | equal |
| data.tables.readers.reader-rowan-mas.reading | tables.readers.reader-rowan-mas.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan-next.asked | tables.readers.reader-rowan-next.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan-next.broken | tables.readers.reader-rowan-next.broken | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.readers.reader-rowan-next.ok | tables.readers.reader-rowan-next.ok | 42 | 42 | equal | 42 | 42 | equal | 42 | 42 | equal |
| data.tables.readers.reader-rowan-next.reading | tables.readers.reader-rowan-next.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan-space.asked | tables.readers.reader-rowan-space.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan-space.broken | tables.readers.reader-rowan-space.broken | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.readers.reader-rowan-space.ok | tables.readers.reader-rowan-space.ok | 43 | 43 | equal | 43 | 43 | equal | 43 | 43 | equal |
| data.tables.readers.reader-rowan-space.reading | tables.readers.reader-rowan-space.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-rowan.asked | tables.readers.reader-rowan.asked | 4 | 2 | differ | 4 | 3 | differ | 4 | 2 | differ |
| data.tables.readers.reader-rowan.broken | tables.readers.reader-rowan.broken | 20 | 20 | equal | 20 | 20 | equal | 20 | 20 | equal |
| data.tables.readers.reader-rowan.ok | tables.readers.reader-rowan.ok | 20 | 20 | equal | 20 | 20 | equal | 20 | 20 | equal |
| data.tables.readers.reader-rowan.reading | tables.readers.reader-rowan.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-space-2.asked | tables.readers.reader-space-2.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-space-2.broken | tables.readers.reader-space-2.broken | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-space-2.ok | tables.readers.reader-space-2.ok | 52 | 52 | equal | 52 | 52 | equal | 52 | 52 | equal |
| data.tables.readers.reader-space-2.reading | tables.readers.reader-space-2.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-space.asked | tables.readers.reader-space.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-space.broken | tables.readers.reader-space.broken | 14 | 14 | equal | 14 | 14 | equal | 14 | 14 | equal |
| data.tables.readers.reader-space.ok | tables.readers.reader-space.ok | 463 | 463 | equal | 463 | 463 | equal | 463 | 463 | equal |
| data.tables.readers.reader-space.reading | tables.readers.reader-space.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-space.width | tables.readers.reader-space.width | 24 | 24 | equal | 24 | 24 | equal | 24 | 24 | equal |
| data.tables.readers.reader-stella.asked | tables.readers.reader-stella.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-stella.broken | tables.readers.reader-stella.broken | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.readers.reader-stella.ok | tables.readers.reader-stella.ok | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-stella.reading | tables.readers.reader-stella.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-superman-2.asked | tables.readers.reader-superman-2.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-superman-2.broken | tables.readers.reader-superman-2.broken | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-superman-2.ok | tables.readers.reader-superman-2.ok | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.tables.readers.reader-superman-2.reading | tables.readers.reader-superman-2.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-superman.asked | tables.readers.reader-superman.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-superman.broken | tables.readers.reader-superman.broken | 15 | 15 | equal | 15 | 15 | equal | 15 | 15 | equal |
| data.tables.readers.reader-superman.ok | tables.readers.reader-superman.ok | 177 | 177 | equal | 177 | 177 | equal | 177 | 177 | equal |
| data.tables.readers.reader-superman.reading | tables.readers.reader-superman.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-superman.width | tables.readers.reader-superman.width | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.readers.reader-vision-2.asked | tables.readers.reader-vision-2.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-vision-2.broken | tables.readers.reader-vision-2.broken | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-vision-2.ok | tables.readers.reader-vision-2.ok | 41 | 41 | equal | 41 | 41 | equal | 41 | 41 | equal |
| data.tables.readers.reader-vision-2.reading | tables.readers.reader-vision-2.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-vision.asked | tables.readers.reader-vision.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-vision.broken | tables.readers.reader-vision.broken | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.readers.reader-vision.ok | tables.readers.reader-vision.ok | 375 | 375 | equal | 375 | 375 | equal | 375 | 375 | equal |
| data.tables.readers.reader-vision.reading | tables.readers.reader-vision.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-vision.width | tables.readers.reader-vision.width | 16 | 16 | equal | 16 | 16 | equal | 16 | 16 | equal |
| data.tables.readers.reader-zhi.asked | tables.readers.reader-zhi.asked | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-zhi.broken | tables.readers.reader-zhi.broken | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-zhi.ok | tables.readers.reader-zhi.ok | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.readers.reader-zhi.reading | tables.readers.reader-zhi.reading | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.adopt-2026-10-04.cost | tables.work.adopt-2026-10-04.cost | $7.18 | $7.18 | equal | $7.18 | $7.18 | equal | $7.18 | $7.18 | equal |
| data.tables.work.adopt-2026-10-04.cost_by_tier.flash | stream_costs.adopt-2026-10-04.cost_by_tier.flash | $1.25 | $1.25 | equal | $1.25 | $1.25 | equal | $1.25 | $1.25 | equal |
| data.tables.work.adopt-2026-10-04.cost_by_tier.pro | stream_costs.adopt-2026-10-04.cost_by_tier.pro | $5.93 | $5.93 | equal | $5.93 | $5.93 | equal | $5.93 | $5.93 | equal |
| data.tables.work.adopt-2026-10-04.landed | tables.work.adopt-2026-10-04.landed | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.adopt-2026-10-04.merging | tables.work.adopt-2026-10-04.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.adopt-2026-10-04.per_landed | tables.work.adopt-2026-10-04.per_landed | $1.44 | $1.44 | equal | $1.44 | $1.44 | equal | $1.44 | $1.44 | equal |
| data.tables.work.adopt-2026-10-04.ready | tables.work.adopt-2026-10-04.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.adopt-2026-10-04.review | tables.work.adopt-2026-10-04.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.adopt-2026-10-04.tiers.flash | stream_costs.adopt-2026-10-04.tiers.flash | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.adopt-2026-10-04.tiers.pro | stream_costs.adopt-2026-10-04.tiers.pro | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.adopt-2026-10-04.waiting | tables.work.adopt-2026-10-04.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.adopt-2026-10-04.working | tables.work.adopt-2026-10-04.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.bus.cost | tables.work.bus.cost | $2.88 | $2.88 | equal | $2.88 | $2.88 | equal | $2.88 | $2.88 | equal |
| data.tables.work.bus.cost_by_tier.pro | stream_costs.bus.cost_by_tier.pro | $2.88 | $2.88 | equal | $2.88 | $2.88 | equal | $2.88 | $2.88 | equal |
| data.tables.work.bus.landed | tables.work.bus.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.bus.merging | tables.work.bus.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.bus.per_landed | tables.work.bus.per_landed | $2.88 | $2.88 | equal | $2.88 | $2.88 | equal | $2.88 | $2.88 | equal |
| data.tables.work.bus.ready | tables.work.bus.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.bus.review | tables.work.bus.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.bus.tiers.pro | stream_costs.bus.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.bus.waiting | tables.work.bus.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.bus.working | tables.work.bus.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.busdogfood2.landed | tables.work.busdogfood2.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.busdogfood2.merging | tables.work.busdogfood2.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.busdogfood2.ready | tables.work.busdogfood2.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.busdogfood2.review | tables.work.busdogfood2.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.busdogfood2.tiers.pro | stream_costs.busdogfood2.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.busdogfood2.waiting | tables.work.busdogfood2.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.busdogfood2.working | tables.work.busdogfood2.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ci.cost | tables.work.ci.cost | $48.84 | $48.84 | equal | $48.84 | $48.84 | equal | $48.84 | $48.84 | equal |
| data.tables.work.ci.cost_by_tier.flash | stream_costs.ci.cost_by_tier.flash | $0.42 | $0.42 | equal | $0.42 | $0.42 | equal | $0.42 | $0.42 | equal |
| data.tables.work.ci.cost_by_tier.pro | stream_costs.ci.cost_by_tier.pro | $28.33 | $28.33 | equal | $28.33 | $28.33 | equal | $28.33 | $28.33 | equal |
| data.tables.work.ci.cost_by_tier.untiered | stream_costs.ci.cost_by_tier.untiered | $11.67 | $11.67 | equal | $11.67 | $11.67 | equal | $11.67 | $11.67 | equal |
| data.tables.work.ci.landed | tables.work.ci.landed | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.tables.work.ci.merging | tables.work.ci.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ci.per_landed | tables.work.ci.per_landed | $4.44 | $4.44 | equal | $4.44 | $4.44 | equal | $4.44 | $4.44 | equal |
| data.tables.work.ci.ready | tables.work.ci.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ci.review | tables.work.ci.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ci.tiers.pro | stream_costs.ci.tiers.pro | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.work.ci.waiting | tables.work.ci.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ci.working | tables.work.ci.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.classes.cost | tables.work.classes.cost | $8.97 | $8.97 | equal | $8.97 | $8.97 | equal | $8.97 | $8.97 | equal |
| data.tables.work.classes.cost_by_tier.flash | stream_costs.classes.cost_by_tier.flash | $0.50 | $0.50 | equal | $0.50 | $0.50 | equal | $0.50 | $0.50 | equal |
| data.tables.work.classes.cost_by_tier.pro | stream_costs.classes.cost_by_tier.pro | $8.48 | $8.48 | equal | $8.48 | $8.48 | equal | $8.48 | $8.48 | equal |
| data.tables.work.classes.landed | tables.work.classes.landed | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.classes.merging | tables.work.classes.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.classes.per_landed | tables.work.classes.per_landed | $4.49 | $4.49 | equal | $4.49 | $4.49 | equal | $4.49 | $4.49 | equal |
| data.tables.work.classes.ready | tables.work.classes.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.classes.review | tables.work.classes.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.classes.tiers.flash | stream_costs.classes.tiers.flash | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.classes.waiting | tables.work.classes.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.classes.working | tables.work.classes.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.contract.landed | tables.work.contract.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.contract.merging | tables.work.contract.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.contract.ready | tables.work.contract.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.contract.review | tables.work.contract.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.contract.waiting | tables.work.contract.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.contract.working | tables.work.contract.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage.cost | tables.work.coverage.cost | $27.29 | $27.29 | equal | $27.29 | $27.29 | equal | $27.29 | $27.29 | equal |
| data.tables.work.coverage.cost_by_tier.flash | stream_costs.coverage.cost_by_tier.flash | $16.20 | $16.20 | equal | $16.20 | $16.20 | equal | $16.20 | $16.20 | equal |
| data.tables.work.coverage.cost_by_tier.pro | stream_costs.coverage.cost_by_tier.pro | $11.09 | $11.09 | equal | $11.09 | $11.09 | equal | $11.09 | $11.09 | equal |
| data.tables.work.coverage.landed | tables.work.coverage.landed | 149 | 149 | equal | 149 | 149 | equal | 149 | 149 | equal |
| data.tables.work.coverage.merging | tables.work.coverage.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage.per_landed | tables.work.coverage.per_landed | $0.19 | $0.19 | equal | $0.19 | $0.19 | equal | $0.19 | $0.19 | equal |
| data.tables.work.coverage.ready | tables.work.coverage.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage.review | tables.work.coverage.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage.tiers.flash | stream_costs.coverage.tiers.flash | 149 | 149 | equal | 149 | 149 | equal | 149 | 149 | equal |
| data.tables.work.coverage.waiting | tables.work.coverage.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage.working | tables.work.coverage.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage2.cost | tables.work.coverage2.cost | $8.53 | $8.53 | equal | $8.53 | $8.53 | equal | $8.53 | $8.53 | equal |
| data.tables.work.coverage2.cost_by_tier.flash | stream_costs.coverage2.cost_by_tier.flash | $8.53 | $8.53 | equal | $8.53 | $8.53 | equal | $8.53 | $8.53 | equal |
| data.tables.work.coverage2.landed | tables.work.coverage2.landed | 113 | 113 | equal | 113 | 113 | equal | 113 | 113 | equal |
| data.tables.work.coverage2.merging | tables.work.coverage2.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage2.per_landed | tables.work.coverage2.per_landed | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal |
| data.tables.work.coverage2.ready | tables.work.coverage2.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage2.review | tables.work.coverage2.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage2.tiers.flash | stream_costs.coverage2.tiers.flash | 113 | 113 | equal | 113 | 113 | equal | 113 | 113 | equal |
| data.tables.work.coverage2.waiting | tables.work.coverage2.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.coverage2.working | tables.work.coverage2.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.dead.cost | tables.work.dead.cost | $8.04 | $8.04 | equal | $8.04 | $8.04 | equal | $8.04 | $8.04 | equal |
| data.tables.work.dead.cost_by_tier.flash | stream_costs.dead.cost_by_tier.flash | $0.35 | $0.35 | equal | $0.35 | $0.35 | equal | $0.35 | $0.35 | equal |
| data.tables.work.dead.cost_by_tier.pro | stream_costs.dead.cost_by_tier.pro | $8.74 | $8.74 | equal | $8.74 | $8.74 | equal | $8.74 | $8.74 | equal |
| data.tables.work.dead.landed | tables.work.dead.landed | 7 | 7 | equal | 7 | 7 | equal | 7 | 7 | equal |
| data.tables.work.dead.merging | tables.work.dead.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.dead.per_landed | tables.work.dead.per_landed | $1.34 | $1.34 | equal | $1.34 | $1.34 | equal | $1.34 | $1.34 | equal |
| data.tables.work.dead.ready | tables.work.dead.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.dead.review | tables.work.dead.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.dead.tiers.flash | stream_costs.dead.tiers.flash | 7 | 7 | equal | 7 | 7 | equal | 7 | 7 | equal |
| data.tables.work.dead.waiting | tables.work.dead.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.dead.working | tables.work.dead.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.debt.cost | tables.work.debt.cost | $82.55 | $82.55 | equal | $82.55 | $82.55 | equal | $82.55 | $82.55 | equal |
| data.tables.work.debt.cost_by_tier.flash | stream_costs.debt.cost_by_tier.flash | $28.59 | $28.59 | equal | $28.59 | $28.59 | equal | $28.59 | $28.59 | equal |
| data.tables.work.debt.cost_by_tier.pro | stream_costs.debt.cost_by_tier.pro | $83.55 | $83.55 | equal | $83.55 | $83.55 | equal | $83.55 | $83.55 | equal |
| data.tables.work.debt.landed | tables.work.debt.landed | 168 | 168 | equal | 168 | 168 | equal | 168 | 168 | equal |
| data.tables.work.debt.merging | tables.work.debt.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.debt.per_landed | tables.work.debt.per_landed | $0.50 | $0.50 | equal | $0.50 | $0.50 | equal | $0.50 | $0.50 | equal |
| data.tables.work.debt.ready | tables.work.debt.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.debt.review | tables.work.debt.review | 15 | 15 | equal | 15 | 15 | equal | 15 | 15 | equal |
| data.tables.work.debt.tiers.flash | stream_costs.debt.tiers.flash | 27 | 27 | equal | 27 | 27 | equal | 27 | 27 | equal |
| data.tables.work.debt.tiers.pro | stream_costs.debt.tiers.pro | 156 | 156 | equal | 156 | 156 | equal | 156 | 156 | equal |
| data.tables.work.debt.waiting | tables.work.debt.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.debt.working | tables.work.debt.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.diary.cost | tables.work.diary.cost | $38.01 | $38.01 | equal | $38.01 | $38.01 | equal | $38.01 | $38.01 | equal |
| data.tables.work.diary.cost_by_tier.flash | stream_costs.diary.cost_by_tier.flash | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal |
| data.tables.work.diary.cost_by_tier.untiered | stream_costs.diary.cost_by_tier.untiered | $36.48 | $36.48 | equal | $36.48 | $36.48 | equal | $36.48 | $36.48 | equal |
| data.tables.work.diary.landed | tables.work.diary.landed | 147 | 147 | equal | 147 | 147 | equal | 147 | 147 | equal |
| data.tables.work.diary.merging | tables.work.diary.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.diary.per_landed | tables.work.diary.per_landed | $0.27 | $0.27 | equal | $0.27 | $0.27 | equal | $0.27 | $0.27 | equal |
| data.tables.work.diary.ready | tables.work.diary.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.diary.review | tables.work.diary.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.diary.tiers.flash | stream_costs.diary.tiers.flash | 142 | 142 | equal | 142 | 142 | equal | 142 | 142 | equal |
| data.tables.work.diary.tiers.pro | stream_costs.diary.tiers.pro | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.diary.waiting | tables.work.diary.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.diary.working | tables.work.diary.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.docs.cost | tables.work.docs.cost | $16.20 | $16.20 | equal | $16.20 | $16.20 | equal | $16.20 | $16.20 | equal |
| data.tables.work.docs.cost_by_tier.untiered | stream_costs.docs.cost_by_tier.untiered | $15.23 | $15.23 | equal | $15.23 | $15.23 | equal | $15.23 | $15.23 | equal |
| data.tables.work.docs.landed | tables.work.docs.landed | 59 | 59 | equal | 59 | 59 | equal | 59 | 59 | equal |
| data.tables.work.docs.merging | tables.work.docs.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.docs.per_landed | tables.work.docs.per_landed | $0.28 | $0.28 | equal | $0.28 | $0.28 | equal | $0.28 | $0.28 | equal |
| data.tables.work.docs.ready | tables.work.docs.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.docs.review | tables.work.docs.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.docs.tiers.flash | stream_costs.docs.tiers.flash | 56 | 56 | equal | 56 | 56 | equal | 56 | 56 | equal |
| data.tables.work.docs.tiers.pro | stream_costs.docs.tiers.pro | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.docs.waiting | tables.work.docs.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.docs.working | tables.work.docs.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.findings.cost | tables.work.findings.cost | $1.38 | $1.38 | equal | $1.38 | $1.38 | equal | $1.38 | $1.38 | equal |
| data.tables.work.findings.cost_by_tier.flash | stream_costs.findings.cost_by_tier.flash | $1.38 | $1.38 | equal | $1.38 | $1.38 | equal | $1.38 | $1.38 | equal |
| data.tables.work.findings.landed | tables.work.findings.landed | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.work.findings.merging | tables.work.findings.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.findings.per_landed | tables.work.findings.per_landed | $0.13 | $0.13 | equal | $0.13 | $0.13 | equal | $0.13 | $0.13 | equal |
| data.tables.work.findings.ready | tables.work.findings.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.findings.review | tables.work.findings.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.findings.tiers.flash | stream_costs.findings.tiers.flash | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.findings.tiers.pro | stream_costs.findings.tiers.pro | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.findings.waiting | tables.work.findings.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.findings.working | tables.work.findings.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.fixes.cost | tables.work.fixes.cost | $18.38 | $18.38 | equal | $18.38 | $18.38 | equal | $18.38 | $18.38 | equal |
| data.tables.work.fixes.cost_by_tier.flash | stream_costs.fixes.cost_by_tier.flash | $4.72 | $4.72 | equal | $4.72 | $4.72 | equal | $4.72 | $4.72 | equal |
| data.tables.work.fixes.cost_by_tier.pro | stream_costs.fixes.cost_by_tier.pro | $13.67 | $13.67 | equal | $13.67 | $13.67 | equal | $13.67 | $13.67 | equal |
| data.tables.work.fixes.landed | tables.work.fixes.landed | 24 | 24 | equal | 24 | 24 | equal | 24 | 24 | equal |
| data.tables.work.fixes.merging | tables.work.fixes.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.fixes.per_landed | tables.work.fixes.per_landed | $0.77 | $0.77 | equal | $0.77 | $0.77 | equal | $0.77 | $0.77 | equal |
| data.tables.work.fixes.ready | tables.work.fixes.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.fixes.review | tables.work.fixes.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.fixes.tiers.pro | stream_costs.fixes.tiers.pro | 25 | 25 | equal | 25 | 25 | equal | 25 | 25 | equal |
| data.tables.work.fixes.waiting | tables.work.fixes.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.fixes.working | tables.work.fixes.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.flash50.cost | tables.work.flash50.cost | $12.58 | $12.58 | equal | $12.58 | $12.58 | equal | $12.58 | $12.58 | equal |
| data.tables.work.flash50.cost_by_tier.flash | stream_costs.flash50.cost_by_tier.flash | $10.23 | $10.23 | equal | $10.23 | $10.23 | equal | $10.23 | $10.23 | equal |
| data.tables.work.flash50.cost_by_tier.pro | stream_costs.flash50.cost_by_tier.pro | $2.36 | $2.36 | equal | $2.36 | $2.36 | equal | $2.36 | $2.36 | equal |
| data.tables.work.flash50.landed | tables.work.flash50.landed | 51 | 51 | equal | 51 | 51 | equal | 51 | 51 | equal |
| data.tables.work.flash50.merging | tables.work.flash50.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.flash50.per_landed | tables.work.flash50.per_landed | $0.26 | $0.26 | equal | $0.26 | $0.26 | equal | $0.26 | $0.26 | equal |
| data.tables.work.flash50.ready | tables.work.flash50.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.flash50.review | tables.work.flash50.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.flash50.tiers.flash | stream_costs.flash50.tiers.flash | 50 | 50 | equal | 50 | 50 | equal | 50 | 50 | equal |
| data.tables.work.flash50.waiting | tables.work.flash50.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.flash50.working | tables.work.flash50.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.fleetnames.cost | tables.work.fleetnames.cost | $1.58 | $1.58 | equal | $1.58 | $1.58 | equal | $1.58 | $1.58 | equal |
| data.tables.work.fleetnames.cost_by_tier.flash | stream_costs.fleetnames.cost_by_tier.flash | $1.67 | $1.67 | equal | $1.67 | $1.67 | equal | $1.67 | $1.67 | equal |
| data.tables.work.fleetnames.landed | tables.work.fleetnames.landed | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.work.fleetnames.merging | tables.work.fleetnames.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.fleetnames.per_landed | tables.work.fleetnames.per_landed | $0.32 | $0.32 | equal | $0.32 | $0.32 | equal | $0.32 | $0.32 | equal |
| data.tables.work.fleetnames.ready | tables.work.fleetnames.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.fleetnames.review | tables.work.fleetnames.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.fleetnames.tiers.flash | stream_costs.fleetnames.tiers.flash | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.work.fleetnames.waiting | tables.work.fleetnames.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.fleetnames.working | tables.work.fleetnames.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.frictions2.cost | tables.work.frictions2.cost | $2.69 | $2.69 | equal | $2.69 | $2.69 | equal | $2.69 | $2.69 | equal |
| data.tables.work.frictions2.cost_by_tier.pro | stream_costs.frictions2.cost_by_tier.pro | $2.69 | $2.69 | equal | $2.69 | $2.69 | equal | $2.69 | $2.69 | equal |
| data.tables.work.frictions2.landed | tables.work.frictions2.landed | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.frictions2.merging | tables.work.frictions2.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.frictions2.per_landed | tables.work.frictions2.per_landed | $0.90 | $0.90 | equal | $0.90 | $0.90 | equal | $0.90 | $0.90 | equal |
| data.tables.work.frictions2.ready | tables.work.frictions2.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.frictions2.review | tables.work.frictions2.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.frictions2.tiers.pro | stream_costs.frictions2.tiers.pro | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.frictions2.waiting | tables.work.frictions2.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.frictions2.working | tables.work.frictions2.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-reserve.cost | tables.work.friend-reserve.cost | $8.59 | $8.59 | equal | $8.59 | $8.59 | equal | $8.59 | $8.59 | equal |
| data.tables.work.friend-reserve.cost_by_tier.pro | stream_costs.friend-reserve.cost_by_tier.pro | $8.59 | $8.59 | equal | $8.59 | $8.59 | equal | $8.59 | $8.59 | equal |
| data.tables.work.friend-reserve.landed | tables.work.friend-reserve.landed | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.friend-reserve.merging | tables.work.friend-reserve.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-reserve.per_landed | tables.work.friend-reserve.per_landed | $2.87 | $2.87 | equal | $2.87 | $2.87 | equal | $2.87 | $2.87 | equal |
| data.tables.work.friend-reserve.ready | tables.work.friend-reserve.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-reserve.review | tables.work.friend-reserve.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-reserve.tiers.pro | stream_costs.friend-reserve.tiers.pro | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.friend-reserve.waiting | tables.work.friend-reserve.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-reserve.working | tables.work.friend-reserve.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-test.landed | tables.work.friend-test.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-test.merging | tables.work.friend-test.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-test.ready | tables.work.friend-test.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-test.review | tables.work.friend-test.review | 0 | 1 | differ | 0 | 1 | differ | 0 | 1 | differ |
| data.tables.work.friend-test.tiers.pro | stream_costs.friend-test.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.friend-test.waiting | tables.work.friend-test.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friend-test.working | tables.work.friend-test.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.friends-general-v1-2-0.cost | tables.work.friends-general-v1-2-0.cost | $6.08 | $6.08 | equal | $6.08 | $6.08 | equal | $6.08 | $6.08 | equal |
| data.tables.work.friends-general-v1-2-0.cost_by_tier.flash | stream_costs.friends-general-v1-2-0.cost_by_tier.flash | $1.99 | $1.99 | equal | $1.99 | $1.99 | equal | $1.99 | $1.99 | equal |
| data.tables.work.friends-general-v1-2-0.cost_by_tier.pro | stream_costs.friends-general-v1-2-0.cost_by_tier.pro | $29.83 | $29.83 | equal | $29.83 | $29.83 | equal | $29.83 | $29.83 | equal |
| data.tables.work.friends-general-v1-2-0.landed | tables.work.friends-general-v1-2-0.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.friends-general-v1-2-0.merging | tables.work.friends-general-v1-2-0.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-general-v1-2-0.per_landed | tables.work.friends-general-v1-2-0.per_landed | $3.04 | $3.04 | equal | $3.04 | $3.04 | equal | $3.04 | $3.04 | equal |
| data.tables.work.friends-general-v1-2-0.ready | tables.work.friends-general-v1-2-0.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-general-v1-2-0.review | tables.work.friends-general-v1-2-0.review | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.work.friends-general-v1-2-0.tiers.pro | stream_costs.friends-general-v1-2-0.tiers.pro | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.friends-general-v1-2-0.waiting | tables.work.friends-general-v1-2-0.waiting | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.friends-general-v1-2-0.working | tables.work.friends-general-v1-2-0.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-sprint-v1-0-0.landed | tables.work.friends-sprint-v1-0-0.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.friends-sprint-v1-0-0.merging | tables.work.friends-sprint-v1-0-0.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-sprint-v1-0-0.ready | tables.work.friends-sprint-v1-0-0.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-sprint-v1-0-0.review | tables.work.friends-sprint-v1-0-0.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-sprint-v1-0-0.tiers.pro | stream_costs.friends-sprint-v1-0-0.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.friends-sprint-v1-0-0.waiting | tables.work.friends-sprint-v1-0-0.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-sprint-v1-0-0.working | tables.work.friends-sprint-v1-0-0.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-v1-2-0-reliability.landed | tables.work.friends-v1-2-0-reliability.landed | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.friends-v1-2-0-reliability.merging | tables.work.friends-v1-2-0-reliability.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-v1-2-0-reliability.ready | tables.work.friends-v1-2-0-reliability.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.friends-v1-2-0-reliability.review | tables.work.friends-v1-2-0-reliability.review | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.friends-v1-2-0-reliability.tiers.flash | stream_costs.friends-v1-2-0-reliability.tiers.flash | 14 | 14 | equal | 14 | 14 | equal | 14 | 14 | equal |
| data.tables.work.friends-v1-2-0-reliability.tiers.pro | stream_costs.friends-v1-2-0-reliability.tiers.pro | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.friends-v1-2-0-reliability.waiting | tables.work.friends-v1-2-0-reliability.waiting | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.friends-v1-2-0-reliability.working | tables.work.friends-v1-2-0-reliability.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ftsync.landed | tables.work.ftsync.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.ftsync.merging | tables.work.ftsync.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ftsync.ready | tables.work.ftsync.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ftsync.review | tables.work.ftsync.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ftsync.waiting | tables.work.ftsync.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ftsync.working | tables.work.ftsync.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.general.cost | tables.work.general.cost | $36.93 | $36.93 | equal | $36.93 | $36.93 | equal | $36.93 | $36.93 | equal |
| data.tables.work.general.cost_by_tier.flash | stream_costs.general.cost_by_tier.flash | $1.48 | $1.48 | equal | $1.48 | $1.48 | equal | $1.48 | $1.48 | equal |
| data.tables.work.general.cost_by_tier.untiered | stream_costs.general.cost_by_tier.untiered | $35.46 | $35.46 | equal | $35.46 | $35.46 | equal | $35.46 | $35.46 | equal |
| data.tables.work.general.landed | tables.work.general.landed | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.general.merging | tables.work.general.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.general.per_landed | tables.work.general.per_landed | $9.24 | $9.24 | equal | $9.24 | $9.24 | equal | $9.24 | $9.24 | equal |
| data.tables.work.general.ready | tables.work.general.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.general.review | tables.work.general.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.general.tiers.pro | stream_costs.general.tiers.pro | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.general.waiting | tables.work.general.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.general.working | tables.work.general.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.harness.cost | tables.work.harness.cost | $1.47 | $1.47 | equal | $1.47 | $1.47 | equal | $1.47 | $1.47 | equal |
| data.tables.work.harness.cost_by_tier.flash | stream_costs.harness.cost_by_tier.flash | $1.47 | $1.47 | equal | $1.47 | $1.47 | equal | $1.47 | $1.47 | equal |
| data.tables.work.harness.landed | tables.work.harness.landed | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.work.harness.merging | tables.work.harness.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.harness.per_landed | tables.work.harness.per_landed | $0.30 | $0.30 | equal | $0.30 | $0.30 | equal | $0.30 | $0.30 | equal |
| data.tables.work.harness.ready | tables.work.harness.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.harness.review | tables.work.harness.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.harness.tiers.flash | stream_costs.harness.tiers.flash | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.harness.waiting | tables.work.harness.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.harness.working | tables.work.harness.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-alex.cost | tables.work.heavy-alex.cost | $0.37 | $0.37 | equal | $0.37 | $0.37 | equal | $0.37 | $0.37 | equal |
| data.tables.work.heavy-alex.cost_by_tier.pro | stream_costs.heavy-alex.cost_by_tier.pro | $0.37 | $0.37 | equal | $0.37 | $0.37 | equal | $0.37 | $0.37 | equal |
| data.tables.work.heavy-alex.landed | tables.work.heavy-alex.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.heavy-alex.merging | tables.work.heavy-alex.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-alex.per_landed | tables.work.heavy-alex.per_landed | $0.37 | $0.37 | equal | $0.37 | $0.37 | equal | $0.37 | $0.37 | equal |
| data.tables.work.heavy-alex.ready | tables.work.heavy-alex.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-alex.review | tables.work.heavy-alex.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-alex.tiers.pro | stream_costs.heavy-alex.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.heavy-alex.waiting | tables.work.heavy-alex.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-alex.working | tables.work.heavy-alex.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-emma.cost | tables.work.heavy-emma.cost | $18.12 | $18.12 | equal | $18.12 | $18.12 | equal | $18.12 | $18.12 | equal |
| data.tables.work.heavy-emma.cost_by_tier.pro | stream_costs.heavy-emma.cost_by_tier.pro | $28.33 | $28.33 | equal | $28.33 | $28.33 | equal | $28.33 | $28.33 | equal |
| data.tables.work.heavy-emma.landed | tables.work.heavy-emma.landed | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.work.heavy-emma.merging | tables.work.heavy-emma.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-emma.per_landed | tables.work.heavy-emma.per_landed | $1.40 | $1.40 | equal | $1.40 | $1.40 | equal | $1.40 | $1.40 | equal |
| data.tables.work.heavy-emma.ready | tables.work.heavy-emma.ready | 0 | 1 | differ | 0 | 1 | differ | 0 | 0 | equal |
| data.tables.work.heavy-emma.review | tables.work.heavy-emma.review | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.heavy-emma.tiers.pro | stream_costs.heavy-emma.tiers.pro | 18 | 18 | equal | 18 | 18 | equal | 18 | 18 | equal |
| data.tables.work.heavy-emma.waiting | tables.work.heavy-emma.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-emma.working | tables.work.heavy-emma.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 1 | equal |
| data.tables.work.heavy-rowan.cost | tables.work.heavy-rowan.cost | $5.51 | $5.51 | equal | $5.51 | $5.51 | equal | $5.51 | $5.51 | equal |
| data.tables.work.heavy-rowan.cost_by_tier.pro | stream_costs.heavy-rowan.cost_by_tier.pro | $5.51 | $5.51 | equal | $5.51 | $5.51 | equal | $5.51 | $5.51 | equal |
| data.tables.work.heavy-rowan.landed | tables.work.heavy-rowan.landed | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.heavy-rowan.merging | tables.work.heavy-rowan.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-rowan.per_landed | tables.work.heavy-rowan.per_landed | $1.38 | $1.38 | equal | $1.38 | $1.38 | equal | $1.38 | $1.38 | equal |
| data.tables.work.heavy-rowan.ready | tables.work.heavy-rowan.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-rowan.review | tables.work.heavy-rowan.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.heavy-rowan.tiers.pro | stream_costs.heavy-rowan.tiers.pro | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.heavy-rowan.waiting | tables.work.heavy-rowan.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.heavy-rowan.working | tables.work.heavy-rowan.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.help.cost | tables.work.help.cost | $17.48 | $17.48 | equal | $17.48 | $17.48 | equal | $17.48 | $17.48 | equal |
| data.tables.work.help.cost_by_tier.flash | stream_costs.help.cost_by_tier.flash | $5.77 | $5.77 | equal | $5.77 | $5.77 | equal | $5.77 | $5.77 | equal |
| data.tables.work.help.cost_by_tier.pro | stream_costs.help.cost_by_tier.pro | $11.72 | $11.72 | equal | $11.72 | $11.72 | equal | $11.72 | $11.72 | equal |
| data.tables.work.help.landed | tables.work.help.landed | 15 | 15 | equal | 15 | 15 | equal | 15 | 15 | equal |
| data.tables.work.help.merging | tables.work.help.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.help.per_landed | tables.work.help.per_landed | $1.17 | $1.17 | equal | $1.17 | $1.17 | equal | $1.17 | $1.17 | equal |
| data.tables.work.help.ready | tables.work.help.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.help.review | tables.work.help.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.help.tiers.pro | stream_costs.help.tiers.pro | 15 | 15 | equal | 15 | 15 | equal | 15 | 15 | equal |
| data.tables.work.help.waiting | tables.work.help.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.help.working | tables.work.help.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.libs.cost | tables.work.libs.cost | $1.37 | $1.37 | equal | $1.37 | $1.37 | equal | $1.37 | $1.37 | equal |
| data.tables.work.libs.cost_by_tier.flash | stream_costs.libs.cost_by_tier.flash | $1.72 | $1.72 | equal | $1.72 | $1.72 | equal | $1.72 | $1.72 | equal |
| data.tables.work.libs.cost_by_tier.pro | stream_costs.libs.cost_by_tier.pro | $0.57 | $0.57 | equal | $0.57 | $0.57 | equal | $0.57 | $0.57 | equal |
| data.tables.work.libs.landed | tables.work.libs.landed | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.work.libs.merging | tables.work.libs.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.libs.per_landed | tables.work.libs.per_landed | $0.20 | $0.20 | equal | $0.20 | $0.20 | equal | $0.20 | $0.20 | equal |
| data.tables.work.libs.ready | tables.work.libs.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.libs.review | tables.work.libs.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.libs.tiers.flash | stream_costs.libs.tiers.flash | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.work.libs.waiting | tables.work.libs.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.libs.working | tables.work.libs.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.lint.cost | tables.work.lint.cost | $10.42 | $10.42 | equal | $10.42 | $10.42 | equal | $10.42 | $10.42 | equal |
| data.tables.work.lint.cost_by_tier.flash | stream_costs.lint.cost_by_tier.flash | $11.35 | $11.35 | equal | $11.35 | $11.35 | equal | $11.35 | $11.35 | equal |
| data.tables.work.lint.cost_by_tier.pro | stream_costs.lint.cost_by_tier.pro | $1.16 | $1.16 | equal | $1.16 | $1.16 | equal | $1.16 | $1.16 | equal |
| data.tables.work.lint.landed | tables.work.lint.landed | 52 | 52 | equal | 52 | 52 | equal | 52 | 52 | equal |
| data.tables.work.lint.merging | tables.work.lint.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.lint.per_landed | tables.work.lint.per_landed | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal |
| data.tables.work.lint.ready | tables.work.lint.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.lint.review | tables.work.lint.review | 5 | 6 | differ | 5 | 6 | differ | 5 | 6 | differ |
| data.tables.work.lint.tiers.flash | stream_costs.lint.tiers.flash | 57 | 57 | equal | 57 | 57 | equal | 57 | 57 | equal |
| data.tables.work.lint.waiting | tables.work.lint.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.lint.working | tables.work.lint.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.machinery2.cost | tables.work.machinery2.cost | $28.30 | $28.30 | equal | $28.30 | $28.30 | equal | $28.30 | $28.30 | equal |
| data.tables.work.machinery2.cost_by_tier.flash | stream_costs.machinery2.cost_by_tier.flash | $0.05 | $0.05 | equal | $0.05 | $0.05 | equal | $0.05 | $0.05 | equal |
| data.tables.work.machinery2.cost_by_tier.pro | stream_costs.machinery2.cost_by_tier.pro | $28.26 | $28.26 | equal | $28.26 | $28.26 | equal | $28.26 | $28.26 | equal |
| data.tables.work.machinery2.landed | tables.work.machinery2.landed | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.work.machinery2.merging | tables.work.machinery2.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.machinery2.per_landed | tables.work.machinery2.per_landed | $2.58 | $2.58 | equal | $2.58 | $2.58 | equal | $2.58 | $2.58 | equal |
| data.tables.work.machinery2.ready | tables.work.machinery2.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.machinery2.review | tables.work.machinery2.review | 0 | 1 | differ | 0 | 1 | differ | 0 | 1 | differ |
| data.tables.work.machinery2.tiers.flash | stream_costs.machinery2.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.machinery2.tiers.pro | stream_costs.machinery2.tiers.pro | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.tables.work.machinery2.waiting | tables.work.machinery2.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.machinery2.working | tables.work.machinery2.working | 2 | 1 | differ | 2 | 1 | differ | 2 | 1 | differ |
| data.tables.work.meaning.cost | tables.work.meaning.cost | $1.73 | $1.73 | equal | $1.73 | $1.73 | equal | $1.73 | $1.73 | equal |
| data.tables.work.meaning.cost_by_tier.flash | stream_costs.meaning.cost_by_tier.flash | $0.49 | $0.49 | equal | $0.49 | $0.49 | equal | $0.49 | $0.49 | equal |
| data.tables.work.meaning.cost_by_tier.pro | stream_costs.meaning.cost_by_tier.pro | $1.24 | $1.24 | equal | $1.24 | $1.24 | equal | $1.24 | $1.24 | equal |
| data.tables.work.meaning.landed | tables.work.meaning.landed | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.meaning.merging | tables.work.meaning.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.meaning.per_landed | tables.work.meaning.per_landed | $0.22 | $0.22 | equal | $0.22 | $0.22 | equal | $0.22 | $0.22 | equal |
| data.tables.work.meaning.ready | tables.work.meaning.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.meaning.review | tables.work.meaning.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.meaning.tiers.flash | stream_costs.meaning.tiers.flash | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.work.meaning.waiting | tables.work.meaning.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.meaning.working | tables.work.meaning.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.missed-2026-10-04.cost | tables.work.missed-2026-10-04.cost | $1.64 | $1.64 | equal | $1.64 | $1.64 | equal | $1.64 | $1.64 | equal |
| data.tables.work.missed-2026-10-04.cost_by_tier.flash | stream_costs.missed-2026-10-04.cost_by_tier.flash | $1.11 | $1.11 | equal | $1.11 | $1.11 | equal | $1.11 | $1.11 | equal |
| data.tables.work.missed-2026-10-04.cost_by_tier.pro | stream_costs.missed-2026-10-04.cost_by_tier.pro | $7.66 | $7.66 | equal | $7.66 | $7.66 | equal | $7.66 | $7.66 | equal |
| data.tables.work.missed-2026-10-04.landed | tables.work.missed-2026-10-04.landed | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.missed-2026-10-04.merging | tables.work.missed-2026-10-04.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.missed-2026-10-04.per_landed | tables.work.missed-2026-10-04.per_landed | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal |
| data.tables.work.missed-2026-10-04.ready | tables.work.missed-2026-10-04.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.missed-2026-10-04.review | tables.work.missed-2026-10-04.review | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.tables.work.missed-2026-10-04.tiers.flash | stream_costs.missed-2026-10-04.tiers.flash | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.work.missed-2026-10-04.tiers.pro | stream_costs.missed-2026-10-04.tiers.pro | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.missed-2026-10-04.waiting | tables.work.missed-2026-10-04.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.missed-2026-10-04.working | tables.work.missed-2026-10-04.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.models.cost | tables.work.models.cost | $42.90 | $42.90 | equal | $42.90 | $42.90 | equal | $42.90 | $42.90 | equal |
| data.tables.work.models.cost_by_tier.flash | stream_costs.models.cost_by_tier.flash | $0.42 | $0.42 | equal | $0.42 | $0.42 | equal | $0.42 | $0.42 | equal |
| data.tables.work.models.cost_by_tier.pro | stream_costs.models.cost_by_tier.pro | $31.71 | $31.71 | equal | $31.71 | $31.71 | equal | $31.71 | $31.71 | equal |
| data.tables.work.models.cost_by_tier.untiered | stream_costs.models.cost_by_tier.untiered | $6.50 | $6.50 | equal | $6.50 | $6.50 | equal | $6.50 | $6.50 | equal |
| data.tables.work.models.landed | tables.work.models.landed | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.models.merging | tables.work.models.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.models.per_landed | tables.work.models.per_landed | $10.73 | $10.73 | equal | $10.73 | $10.73 | equal | $10.73 | $10.73 | equal |
| data.tables.work.models.ready | tables.work.models.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.models.review | tables.work.models.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.models.tiers.pro | stream_costs.models.tiers.pro | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.models.waiting | tables.work.models.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.models.working | tables.work.models.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.modeltests.cost | tables.work.modeltests.cost | $14.73 | $14.73 | equal | $14.73 | $14.73 | equal | $14.73 | $14.73 | equal |
| data.tables.work.modeltests.cost_by_tier.flash | stream_costs.modeltests.cost_by_tier.flash | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal |
| data.tables.work.modeltests.cost_by_tier.pro | stream_costs.modeltests.cost_by_tier.pro | $15.52 | $15.52 | equal | $15.52 | $15.52 | equal | $15.52 | $15.52 | equal |
| data.tables.work.modeltests.landed | tables.work.modeltests.landed | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.modeltests.merging | tables.work.modeltests.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.modeltests.per_landed | tables.work.modeltests.per_landed | $4.91 | $4.91 | equal | $4.91 | $4.91 | equal | $4.91 | $4.91 | equal |
| data.tables.work.modeltests.ready | tables.work.modeltests.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.modeltests.review | tables.work.modeltests.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.modeltests.tiers.flash | stream_costs.modeltests.tiers.flash | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.modeltests.waiting | tables.work.modeltests.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.modeltests.working | tables.work.modeltests.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.names.cost | tables.work.names.cost | $5.15 | $5.15 | equal | $5.15 | $5.15 | equal | $5.15 | $5.15 | equal |
| data.tables.work.names.cost_by_tier.untiered | stream_costs.names.cost_by_tier.untiered | $5.15 | $5.15 | equal | $5.15 | $5.15 | equal | $5.15 | $5.15 | equal |
| data.tables.work.names.landed | tables.work.names.landed | 32 | 32 | equal | 32 | 32 | equal | 32 | 32 | equal |
| data.tables.work.names.merging | tables.work.names.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.names.per_landed | tables.work.names.per_landed | $0.17 | $0.17 | equal | $0.17 | $0.17 | equal | $0.17 | $0.17 | equal |
| data.tables.work.names.ready | tables.work.names.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.names.review | tables.work.names.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.names.tiers.flash | stream_costs.names.tiers.flash | 31 | 31 | equal | 31 | 31 | equal | 31 | 31 | equal |
| data.tables.work.names.waiting | tables.work.names.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.names.working | tables.work.names.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncci.cost | tables.work.ncci.cost | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal |
| data.tables.work.ncci.cost_by_tier.flash | stream_costs.ncci.cost_by_tier.flash | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal |
| data.tables.work.ncci.landed | tables.work.ncci.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.ncci.merging | tables.work.ncci.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncci.per_landed | tables.work.ncci.per_landed | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal |
| data.tables.work.ncci.ready | tables.work.ncci.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncci.review | tables.work.ncci.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncci.tiers.flash | stream_costs.ncci.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.ncci.waiting | tables.work.ncci.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncci.working | tables.work.ncci.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncex.cost | tables.work.ncex.cost | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal |
| data.tables.work.ncex.cost_by_tier.flash | stream_costs.ncex.cost_by_tier.flash | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal |
| data.tables.work.ncex.landed | tables.work.ncex.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.ncex.merging | tables.work.ncex.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncex.per_landed | tables.work.ncex.per_landed | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal |
| data.tables.work.ncex.ready | tables.work.ncex.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncex.review | tables.work.ncex.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncex.tiers.flash | stream_costs.ncex.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.ncex.waiting | tables.work.ncex.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncex.working | tables.work.ncex.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncreal.cost | tables.work.ncreal.cost | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal |
| data.tables.work.ncreal.cost_by_tier.flash | stream_costs.ncreal.cost_by_tier.flash | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal |
| data.tables.work.ncreal.landed | tables.work.ncreal.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.ncreal.merging | tables.work.ncreal.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncreal.per_landed | tables.work.ncreal.per_landed | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal |
| data.tables.work.ncreal.ready | tables.work.ncreal.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncreal.review | tables.work.ncreal.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncreal.tiers.flash | stream_costs.ncreal.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.ncreal.waiting | tables.work.ncreal.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ncreal.working | tables.work.ncreal.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.negatives.cost | tables.work.negatives.cost | $9.35 | $9.35 | equal | $9.35 | $9.35 | equal | $9.35 | $9.35 | equal |
| data.tables.work.negatives.cost_by_tier.untiered | stream_costs.negatives.cost_by_tier.untiered | $9.35 | $9.35 | equal | $9.35 | $9.35 | equal | $9.35 | $9.35 | equal |
| data.tables.work.negatives.landed | tables.work.negatives.landed | 65 | 65 | equal | 65 | 65 | equal | 65 | 65 | equal |
| data.tables.work.negatives.merging | tables.work.negatives.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.negatives.per_landed | tables.work.negatives.per_landed | $0.15 | $0.15 | equal | $0.15 | $0.15 | equal | $0.15 | $0.15 | equal |
| data.tables.work.negatives.ready | tables.work.negatives.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.negatives.review | tables.work.negatives.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.negatives.tiers.flash | stream_costs.negatives.tiers.flash | 63 | 63 | equal | 63 | 63 | equal | 63 | 63 | equal |
| data.tables.work.negatives.waiting | tables.work.negatives.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.negatives.working | tables.work.negatives.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.no-shell-v1-2-0.landed | tables.work.no-shell-v1-2-0.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.no-shell-v1-2-0.merging | tables.work.no-shell-v1-2-0.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.no-shell-v1-2-0.ready | tables.work.no-shell-v1-2-0.ready | 0 | 2 | differ | 0 | 2 | differ | 0 | 2 | differ |
| data.tables.work.no-shell-v1-2-0.review | tables.work.no-shell-v1-2-0.review | 7 | 7 | equal | 7 | 7 | equal | 7 | 7 | equal |
| data.tables.work.no-shell-v1-2-0.tiers.flash | stream_costs.no-shell-v1-2-0.tiers.flash | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.tables.work.no-shell-v1-2-0.tiers.pro | stream_costs.no-shell-v1-2-0.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.no-shell-v1-2-0.waiting | tables.work.no-shell-v1-2-0.waiting | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.no-shell-v1-2-0.working | tables.work.no-shell-v1-2-0.working | 2 | 0 | differ | 2 | 0 | differ | 2 | 0 | differ |
| data.tables.work.nongo.cost | tables.work.nongo.cost | $49.53 | $49.53 | equal | $49.53 | $49.53 | equal | $49.53 | $49.53 | equal |
| data.tables.work.nongo.cost_by_tier.untiered | stream_costs.nongo.cost_by_tier.untiered | $41.31 | $41.31 | equal | $41.31 | $41.31 | equal | $41.31 | $41.31 | equal |
| data.tables.work.nongo.landed | tables.work.nongo.landed | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.work.nongo.merging | tables.work.nongo.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nongo.per_landed | tables.work.nongo.per_landed | $4.96 | $4.96 | equal | $4.96 | $4.96 | equal | $4.96 | $4.96 | equal |
| data.tables.work.nongo.ready | tables.work.nongo.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nongo.review | tables.work.nongo.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nongo.tiers.pro | stream_costs.nongo.tiers.pro | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.work.nongo.waiting | tables.work.nongo.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nongo.working | tables.work.nongo.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.notes.landed | tables.work.notes.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.notes.merging | tables.work.notes.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.notes.ready | tables.work.notes.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.notes.review | tables.work.notes.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.notes.waiting | tables.work.notes.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.notes.working | tables.work.notes.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nova-sprint-split.cost | tables.work.nova-sprint-split.cost | $2.50 | $2.50 | equal | $2.50 | $2.50 | equal | $2.50 | $2.50 | equal |
| data.tables.work.nova-sprint-split.cost_by_tier.pro | stream_costs.nova-sprint-split.cost_by_tier.pro | $2.50 | $2.50 | equal | $2.50 | $2.50 | equal | $2.50 | $2.50 | equal |
| data.tables.work.nova-sprint-split.landed | tables.work.nova-sprint-split.landed | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.nova-sprint-split.merging | tables.work.nova-sprint-split.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nova-sprint-split.per_landed | tables.work.nova-sprint-split.per_landed | $1.25 | $1.25 | equal | $1.25 | $1.25 | equal | $1.25 | $1.25 | equal |
| data.tables.work.nova-sprint-split.ready | tables.work.nova-sprint-split.ready | 0 | 2 | differ | 0 | 2 | differ | 0 | 1 | differ |
| data.tables.work.nova-sprint-split.review | tables.work.nova-sprint-split.review | 4 | 6 | differ | 4 | 6 | differ | 4 | 6 | differ |
| data.tables.work.nova-sprint-split.tiers.flash | stream_costs.nova-sprint-split.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.nova-sprint-split.tiers.pro | stream_costs.nova-sprint-split.tiers.pro | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.nova-sprint-split.waiting | tables.work.nova-sprint-split.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nova-sprint-split.working | tables.work.nova-sprint-split.working | 4 | 0 | differ | 4 | 0 | differ | 4 | 1 | differ |
| data.tables.work.nova.cost | tables.work.nova.cost | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal |
| data.tables.work.nova.cost_by_tier.flash | stream_costs.nova.cost_by_tier.flash | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal |
| data.tables.work.nova.landed | tables.work.nova.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.nova.merging | tables.work.nova.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nova.per_landed | tables.work.nova.per_landed | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal |
| data.tables.work.nova.ready | tables.work.nova.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nova.review | tables.work.nova.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nova.tiers.flash | stream_costs.nova.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.nova.waiting | tables.work.nova.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.nova.working | tables.work.nova.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.onething.cost | tables.work.onething.cost | $6.60 | $6.60 | equal | $6.60 | $6.60 | equal | $6.60 | $6.60 | equal |
| data.tables.work.onething.cost_by_tier.pro | stream_costs.onething.cost_by_tier.pro | $6.60 | $6.60 | equal | $6.60 | $6.60 | equal | $6.60 | $6.60 | equal |
| data.tables.work.onething.landed | tables.work.onething.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.onething.merging | tables.work.onething.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.onething.per_landed | tables.work.onething.per_landed | $6.60 | $6.60 | equal | $6.60 | $6.60 | equal | $6.60 | $6.60 | equal |
| data.tables.work.onething.ready | tables.work.onething.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.onething.review | tables.work.onething.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.onething.tiers.pro | stream_costs.onething.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.onething.waiting | tables.work.onething.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.onething.working | tables.work.onething.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.promote-red-2026-10-05.landed | tables.work.promote-red-2026-10-05.landed | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.work.promote-red-2026-10-05.merging | tables.work.promote-red-2026-10-05.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.promote-red-2026-10-05.ready | tables.work.promote-red-2026-10-05.ready | 0 | 1 | differ | 0 | 1 | differ | 0 | 1 | differ |
| data.tables.work.promote-red-2026-10-05.review | tables.work.promote-red-2026-10-05.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.promote-red-2026-10-05.tiers.flash | stream_costs.promote-red-2026-10-05.tiers.flash | 15 | 15 | equal | 15 | 15 | equal | 15 | 15 | equal |
| data.tables.work.promote-red-2026-10-05.waiting | tables.work.promote-red-2026-10-05.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.promote-red-2026-10-05.working | tables.work.promote-red-2026-10-05.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.prose.landed | tables.work.prose.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.prose.merging | tables.work.prose.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.prose.ready | tables.work.prose.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.prose.review | tables.work.prose.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.prose.waiting | tables.work.prose.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.prose.working | tables.work.prose.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.quality.cost | tables.work.quality.cost | $0.10 | $0.10 | equal | $0.10 | $0.10 | equal | $0.10 | $0.10 | equal |
| data.tables.work.quality.cost_by_tier.flash | stream_costs.quality.cost_by_tier.flash | $0.10 | $0.10 | equal | $0.10 | $0.10 | equal | $0.10 | $0.10 | equal |
| data.tables.work.quality.landed | tables.work.quality.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.quality.merging | tables.work.quality.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.quality.per_landed | tables.work.quality.per_landed | $0.10 | $0.10 | equal | $0.10 | $0.10 | equal | $0.10 | $0.10 | equal |
| data.tables.work.quality.ready | tables.work.quality.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.quality.review | tables.work.quality.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.quality.tiers.flash | stream_costs.quality.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.quality.waiting | tables.work.quality.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.quality.working | tables.work.quality.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rate-tools-dev-2026-10-05.landed | tables.work.rate-tools-dev-2026-10-05.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.rate-tools-dev-2026-10-05.merging | tables.work.rate-tools-dev-2026-10-05.merging | 14 | 14 | equal | 14 | 14 | equal | 14 | 14 | equal |
| data.tables.work.rate-tools-dev-2026-10-05.ready | tables.work.rate-tools-dev-2026-10-05.ready | 0 | 17 | differ | 0 | 17 | differ | 0 | 12 | differ |
| data.tables.work.rate-tools-dev-2026-10-05.review | tables.work.rate-tools-dev-2026-10-05.review | 26 | 30 | differ | 26 | 31 | differ | 26 | 31 | differ |
| data.tables.work.rate-tools-dev-2026-10-05.tiers.flash | stream_costs.rate-tools-dev-2026-10-05.tiers.flash | 24 | 24 | equal | 24 | 24 | equal | 24 | 24 | equal |
| data.tables.work.rate-tools-dev-2026-10-05.tiers.frontier | stream_costs.rate-tools-dev-2026-10-05.tiers.frontier | 48 | 48 | equal | 48 | 48 | equal | 48 | 48 | equal |
| data.tables.work.rate-tools-dev-2026-10-05.tiers.pro | stream_costs.rate-tools-dev-2026-10-05.tiers.pro | 24 | 24 | equal | 24 | 24 | equal | 24 | 24 | equal |
| data.tables.work.rate-tools-dev-2026-10-05.waiting | tables.work.rate-tools-dev-2026-10-05.waiting | 28 | 28 | equal | 28 | 28 | equal | 28 | 28 | equal |
| data.tables.work.rate-tools-dev-2026-10-05.working | tables.work.rate-tools-dev-2026-10-05.working | 27 | 6 | differ | 27 | 5 | differ | 27 | 10 | differ |
| data.tables.work.ratings-alex.cost | tables.work.ratings-alex.cost | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal |
| data.tables.work.ratings-alex.cost_by_tier.flash | stream_costs.ratings-alex.cost_by_tier.flash | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal |
| data.tables.work.ratings-alex.landed | tables.work.ratings-alex.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.ratings-alex.merging | tables.work.ratings-alex.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-alex.per_landed | tables.work.ratings-alex.per_landed | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal |
| data.tables.work.ratings-alex.ready | tables.work.ratings-alex.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-alex.review | tables.work.ratings-alex.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-alex.tiers.flash | stream_costs.ratings-alex.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.ratings-alex.waiting | tables.work.ratings-alex.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-alex.working | tables.work.ratings-alex.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-emma.cost | tables.work.ratings-emma.cost | $1.22 | $1.22 | equal | $1.22 | $1.22 | equal | $1.22 | $1.22 | equal |
| data.tables.work.ratings-emma.cost_by_tier.flash | stream_costs.ratings-emma.cost_by_tier.flash | $1.22 | $1.22 | equal | $1.22 | $1.22 | equal | $1.22 | $1.22 | equal |
| data.tables.work.ratings-emma.landed | tables.work.ratings-emma.landed | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.work.ratings-emma.merging | tables.work.ratings-emma.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-emma.per_landed | tables.work.ratings-emma.per_landed | $0.11 | $0.11 | equal | $0.11 | $0.11 | equal | $0.11 | $0.11 | equal |
| data.tables.work.ratings-emma.ready | tables.work.ratings-emma.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-emma.review | tables.work.ratings-emma.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-emma.tiers.flash | stream_costs.ratings-emma.tiers.flash | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.tables.work.ratings-emma.waiting | tables.work.ratings-emma.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-emma.working | tables.work.ratings-emma.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-freddy.landed | tables.work.ratings-freddy.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.ratings-freddy.merging | tables.work.ratings-freddy.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-freddy.ready | tables.work.ratings-freddy.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-freddy.review | tables.work.ratings-freddy.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-freddy.waiting | tables.work.ratings-freddy.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-freddy.working | tables.work.ratings-freddy.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-johnny.cost | tables.work.ratings-johnny.cost | $1.00 | $1.00 | equal | $1.00 | $1.00 | equal | $1.00 | $1.00 | equal |
| data.tables.work.ratings-johnny.cost_by_tier.flash | stream_costs.ratings-johnny.cost_by_tier.flash | $1.00 | $1.00 | equal | $1.00 | $1.00 | equal | $1.00 | $1.00 | equal |
| data.tables.work.ratings-johnny.landed | tables.work.ratings-johnny.landed | 16 | 16 | equal | 16 | 16 | equal | 16 | 16 | equal |
| data.tables.work.ratings-johnny.merging | tables.work.ratings-johnny.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-johnny.per_landed | tables.work.ratings-johnny.per_landed | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal |
| data.tables.work.ratings-johnny.ready | tables.work.ratings-johnny.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-johnny.review | tables.work.ratings-johnny.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-johnny.tiers.flash | stream_costs.ratings-johnny.tiers.flash | 15 | 15 | equal | 15 | 15 | equal | 15 | 15 | equal |
| data.tables.work.ratings-johnny.waiting | tables.work.ratings-johnny.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-johnny.working | tables.work.ratings-johnny.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-stella.landed | tables.work.ratings-stella.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.ratings-stella.merging | tables.work.ratings-stella.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-stella.ready | tables.work.ratings-stella.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-stella.review | tables.work.ratings-stella.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-stella.waiting | tables.work.ratings-stella.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-stella.working | tables.work.ratings-stella.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-zhi.cost | tables.work.ratings-zhi.cost | $0.73 | $0.73 | equal | $0.73 | $0.73 | equal | $0.73 | $0.73 | equal |
| data.tables.work.ratings-zhi.cost_by_tier.flash | stream_costs.ratings-zhi.cost_by_tier.flash | $0.73 | $0.73 | equal | $0.73 | $0.73 | equal | $0.73 | $0.73 | equal |
| data.tables.work.ratings-zhi.landed | tables.work.ratings-zhi.landed | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.work.ratings-zhi.merging | tables.work.ratings-zhi.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-zhi.per_landed | tables.work.ratings-zhi.per_landed | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal |
| data.tables.work.ratings-zhi.ready | tables.work.ratings-zhi.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-zhi.review | tables.work.ratings-zhi.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-zhi.tiers.flash | stream_costs.ratings-zhi.tiers.flash | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.work.ratings-zhi.waiting | tables.work.ratings-zhi.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ratings-zhi.working | tables.work.ratings-zhi.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.read.landed | tables.work.read.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.read.merging | tables.work.read.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.read.ready | tables.work.read.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.read.review | tables.work.read.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.read.waiting | tables.work.read.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.read.working | tables.work.read.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reads.landed | tables.work.reads.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.reads.merging | tables.work.reads.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reads.ready | tables.work.reads.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reads.review | tables.work.reads.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reads.waiting | tables.work.reads.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reads.working | tables.work.reads.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reland-2026-10-04.cost | tables.work.reland-2026-10-04.cost | $40.01 | $40.01 | equal | $40.01 | $40.01 | equal | $40.01 | $40.01 | equal |
| data.tables.work.reland-2026-10-04.cost_by_tier.flash | stream_costs.reland-2026-10-04.cost_by_tier.flash | $7.50 | $7.50 | equal | $7.50 | $7.50 | equal | $7.50 | $7.50 | equal |
| data.tables.work.reland-2026-10-04.cost_by_tier.pro | stream_costs.reland-2026-10-04.cost_by_tier.pro | $35.52 | $35.52 | equal | $35.52 | $35.52 | equal | $35.52 | $35.52 | equal |
| data.tables.work.reland-2026-10-04.landed | tables.work.reland-2026-10-04.landed | 87 | 87 | equal | 87 | 87 | equal | 87 | 87 | equal |
| data.tables.work.reland-2026-10-04.merging | tables.work.reland-2026-10-04.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reland-2026-10-04.per_landed | tables.work.reland-2026-10-04.per_landed | $0.46 | $0.46 | equal | $0.46 | $0.46 | equal | $0.46 | $0.46 | equal |
| data.tables.work.reland-2026-10-04.ready | tables.work.reland-2026-10-04.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reland-2026-10-04.review | tables.work.reland-2026-10-04.review | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.reland-2026-10-04.tiers.flash | stream_costs.reland-2026-10-04.tiers.flash | 89 | 89 | equal | 89 | 89 | equal | 89 | 89 | equal |
| data.tables.work.reland-2026-10-04.waiting | tables.work.reland-2026-10-04.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reland-2026-10-04.working | tables.work.reland-2026-10-04.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.reliability-now.landed | tables.work.reliability-now.landed | 2 | 5 | differ | 2 | 5 | differ | 2 | 5 | differ |
| data.tables.work.reliability-now.merging | tables.work.reliability-now.merging | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.reliability-now.ready | tables.work.reliability-now.ready | 14 | 28 | differ | 14 | 28 | differ | 14 | 29 | differ |
| data.tables.work.reliability-now.review | tables.work.reliability-now.review | 8 | 12 | differ | 8 | 12 | differ | 8 | 12 | differ |
| data.tables.work.reliability-now.tiers.flash | stream_costs.reliability-now.tiers.flash | 54 | 55 | differ | 54 | 55 | differ | 54 | 56 | differ |
| data.tables.work.reliability-now.tiers.pro | stream_costs.reliability-now.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.reliability-now.waiting | tables.work.reliability-now.waiting | 12 | 11 | differ | 12 | 11 | differ | 12 | 11 | differ |
| data.tables.work.reliability-now.working | tables.work.reliability-now.working | 18 | 0 | differ | 18 | 0 | differ | 18 | 0 | differ |
| data.tables.work.repair-rolling.cost | tables.work.repair-rolling.cost | $2.83 | $2.83 | equal | $2.83 | $2.83 | equal | $2.83 | $2.83 | equal |
| data.tables.work.repair-rolling.cost_by_tier.flash | stream_costs.repair-rolling.cost_by_tier.flash | $2.83 | $2.83 | equal | $2.83 | $2.83 | equal | $2.83 | $2.83 | equal |
| data.tables.work.repair-rolling.landed | tables.work.repair-rolling.landed | 26 | 26 | equal | 26 | 26 | equal | 26 | 26 | equal |
| data.tables.work.repair-rolling.merging | tables.work.repair-rolling.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.repair-rolling.per_landed | tables.work.repair-rolling.per_landed | $0.11 | $0.11 | equal | $0.11 | $0.11 | equal | $0.11 | $0.11 | equal |
| data.tables.work.repair-rolling.ready | tables.work.repair-rolling.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.repair-rolling.review | tables.work.repair-rolling.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.repair-rolling.tiers.pro | stream_costs.repair-rolling.tiers.pro | 26 | 26 | equal | 26 | 26 | equal | 26 | 26 | equal |
| data.tables.work.repair-rolling.waiting | tables.work.repair-rolling.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.repair-rolling.working | tables.work.repair-rolling.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rerate2.cost | tables.work.rerate2.cost | $16.83 | $16.83 | equal | $16.83 | $16.83 | equal | $16.83 | $16.83 | equal |
| data.tables.work.rerate2.cost_by_tier.flash | stream_costs.rerate2.cost_by_tier.flash | $7.40 | $7.40 | equal | $7.40 | $7.40 | equal | $7.40 | $7.40 | equal |
| data.tables.work.rerate2.cost_by_tier.pro | stream_costs.rerate2.cost_by_tier.pro | $9.44 | $9.44 | equal | $9.44 | $9.44 | equal | $9.44 | $9.44 | equal |
| data.tables.work.rerate2.landed | tables.work.rerate2.landed | 33 | 33 | equal | 33 | 33 | equal | 33 | 33 | equal |
| data.tables.work.rerate2.merging | tables.work.rerate2.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rerate2.per_landed | tables.work.rerate2.per_landed | $0.51 | $0.51 | equal | $0.51 | $0.51 | equal | $0.51 | $0.51 | equal |
| data.tables.work.rerate2.ready | tables.work.rerate2.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rerate2.review | tables.work.rerate2.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rerate2.tiers.flash | stream_costs.rerate2.tiers.flash | 33 | 33 | equal | 33 | 33 | equal | 33 | 33 | equal |
| data.tables.work.rerate2.waiting | tables.work.rerate2.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rerate2.working | tables.work.rerate2.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlci.cost | tables.work.rlci.cost | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal |
| data.tables.work.rlci.cost_by_tier.flash | stream_costs.rlci.cost_by_tier.flash | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal |
| data.tables.work.rlci.landed | tables.work.rlci.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.rlci.merging | tables.work.rlci.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlci.per_landed | tables.work.rlci.per_landed | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal |
| data.tables.work.rlci.ready | tables.work.rlci.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlci.review | tables.work.rlci.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlci.tiers.flash | stream_costs.rlci.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.rlci.waiting | tables.work.rlci.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlci.working | tables.work.rlci.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlclean.cost | tables.work.rlclean.cost | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal |
| data.tables.work.rlclean.cost_by_tier.flash | stream_costs.rlclean.cost_by_tier.flash | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal |
| data.tables.work.rlclean.landed | tables.work.rlclean.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.rlclean.merging | tables.work.rlclean.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlclean.per_landed | tables.work.rlclean.per_landed | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal |
| data.tables.work.rlclean.ready | tables.work.rlclean.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlclean.review | tables.work.rlclean.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlclean.tiers.flash | stream_costs.rlclean.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.rlclean.waiting | tables.work.rlclean.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rlclean.working | tables.work.rlclean.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rltests.cost | tables.work.rltests.cost | $0.25 | $0.25 | equal | $0.25 | $0.25 | equal | $0.25 | $0.25 | equal |
| data.tables.work.rltests.cost_by_tier.flash | stream_costs.rltests.cost_by_tier.flash | $0.25 | $0.25 | equal | $0.25 | $0.25 | equal | $0.25 | $0.25 | equal |
| data.tables.work.rltests.landed | tables.work.rltests.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.rltests.merging | tables.work.rltests.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rltests.per_landed | tables.work.rltests.per_landed | $0.25 | $0.25 | equal | $0.25 | $0.25 | equal | $0.25 | $0.25 | equal |
| data.tables.work.rltests.ready | tables.work.rltests.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rltests.review | tables.work.rltests.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rltests.tiers.flash | stream_costs.rltests.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.rltests.waiting | tables.work.rltests.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rltests.working | tables.work.rltests.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rowan-only-to-product.landed | tables.work.rowan-only-to-product.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rowan-only-to-product.merging | tables.work.rowan-only-to-product.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rowan-only-to-product.ready | tables.work.rowan-only-to-product.ready | 3 | 4 | differ | 3 | 4 | differ | 3 | 4 | differ |
| data.tables.work.rowan-only-to-product.review | tables.work.rowan-only-to-product.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.rowan-only-to-product.tiers.flash | stream_costs.rowan-only-to-product.tiers.flash | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.rowan-only-to-product.waiting | tables.work.rowan-only-to-product.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rowan-only-to-product.working | tables.work.rowan-only-to-product.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.rtclose.landed | tables.work.rtclose.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.rtclose.merging | tables.work.rtclose.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtclose.ready | tables.work.rtclose.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtclose.review | tables.work.rtclose.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtclose.tiers.flash | stream_costs.rtclose.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.rtclose.waiting | tables.work.rtclose.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtclose.working | tables.work.rtclose.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtdead.cost | tables.work.rtdead.cost | $0.47 | $0.47 | equal | $0.47 | $0.47 | equal | $0.47 | $0.47 | equal |
| data.tables.work.rtdead.cost_by_tier.flash | stream_costs.rtdead.cost_by_tier.flash | $0.47 | $0.47 | equal | $0.47 | $0.47 | equal | $0.47 | $0.47 | equal |
| data.tables.work.rtdead.landed | tables.work.rtdead.landed | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.work.rtdead.merging | tables.work.rtdead.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtdead.per_landed | tables.work.rtdead.per_landed | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal |
| data.tables.work.rtdead.ready | tables.work.rtdead.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtdead.review | tables.work.rtdead.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtdead.tiers.flash | stream_costs.rtdead.tiers.flash | 7 | 7 | equal | 7 | 7 | equal | 7 | 7 | equal |
| data.tables.work.rtdead.waiting | tables.work.rtdead.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtdead.working | tables.work.rtdead.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfix.cost | tables.work.rtfix.cost | $0.13 | $0.13 | equal | $0.13 | $0.13 | equal | $0.13 | $0.13 | equal |
| data.tables.work.rtfix.cost_by_tier.flash | stream_costs.rtfix.cost_by_tier.flash | $0.13 | $0.13 | equal | $0.13 | $0.13 | equal | $0.13 | $0.13 | equal |
| data.tables.work.rtfix.landed | tables.work.rtfix.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.rtfix.merging | tables.work.rtfix.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfix.per_landed | tables.work.rtfix.per_landed | $0.13 | $0.13 | equal | $0.13 | $0.13 | equal | $0.13 | $0.13 | equal |
| data.tables.work.rtfix.ready | tables.work.rtfix.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfix.review | tables.work.rtfix.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfix.tiers.flash | stream_costs.rtfix.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.rtfix.waiting | tables.work.rtfix.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfix.working | tables.work.rtfix.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfleet.cost | tables.work.rtfleet.cost | $0.39 | $0.39 | equal | $0.39 | $0.39 | equal | $0.39 | $0.39 | equal |
| data.tables.work.rtfleet.cost_by_tier.flash | stream_costs.rtfleet.cost_by_tier.flash | $0.39 | $0.39 | equal | $0.39 | $0.39 | equal | $0.39 | $0.39 | equal |
| data.tables.work.rtfleet.landed | tables.work.rtfleet.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.rtfleet.merging | tables.work.rtfleet.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfleet.per_landed | tables.work.rtfleet.per_landed | $0.39 | $0.39 | equal | $0.39 | $0.39 | equal | $0.39 | $0.39 | equal |
| data.tables.work.rtfleet.ready | tables.work.rtfleet.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfleet.review | tables.work.rtfleet.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfleet.tiers.flash | stream_costs.rtfleet.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.rtfleet.waiting | tables.work.rtfleet.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtfleet.working | tables.work.rtfleet.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtgate.cost | tables.work.rtgate.cost | $0.26 | $0.26 | equal | $0.26 | $0.26 | equal | $0.26 | $0.26 | equal |
| data.tables.work.rtgate.cost_by_tier.flash | stream_costs.rtgate.cost_by_tier.flash | $0.26 | $0.26 | equal | $0.26 | $0.26 | equal | $0.26 | $0.26 | equal |
| data.tables.work.rtgate.landed | tables.work.rtgate.landed | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.rtgate.merging | tables.work.rtgate.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtgate.per_landed | tables.work.rtgate.per_landed | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal |
| data.tables.work.rtgate.ready | tables.work.rtgate.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtgate.review | tables.work.rtgate.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtgate.tiers.flash | stream_costs.rtgate.tiers.flash | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.rtgate.waiting | tables.work.rtgate.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtgate.working | tables.work.rtgate.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtport.cost | tables.work.rtport.cost | $0.14 | $0.14 | equal | $0.14 | $0.14 | equal | $0.14 | $0.14 | equal |
| data.tables.work.rtport.cost_by_tier.flash | stream_costs.rtport.cost_by_tier.flash | $0.14 | $0.14 | equal | $0.14 | $0.14 | equal | $0.14 | $0.14 | equal |
| data.tables.work.rtport.landed | tables.work.rtport.landed | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.rtport.merging | tables.work.rtport.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtport.per_landed | tables.work.rtport.per_landed | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal |
| data.tables.work.rtport.ready | tables.work.rtport.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtport.review | tables.work.rtport.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtport.tiers.flash | stream_costs.rtport.tiers.flash | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.rtport.waiting | tables.work.rtport.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.rtport.working | tables.work.rtport.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.scripts-to-verbs.landed | tables.work.scripts-to-verbs.landed | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.scripts-to-verbs.merging | tables.work.scripts-to-verbs.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.scripts-to-verbs.ready | tables.work.scripts-to-verbs.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.scripts-to-verbs.review | tables.work.scripts-to-verbs.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.scripts-to-verbs.tiers.flash | stream_costs.scripts-to-verbs.tiers.flash | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.scripts-to-verbs.waiting | tables.work.scripts-to-verbs.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.scripts-to-verbs.working | tables.work.scripts-to-verbs.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.seams.cost | tables.work.seams.cost | $2.78 | $2.78 | equal | $2.78 | $2.78 | equal | $2.78 | $2.78 | equal |
| data.tables.work.seams.cost_by_tier.flash | stream_costs.seams.cost_by_tier.flash | $1.30 | $1.30 | equal | $1.30 | $1.30 | equal | $1.30 | $1.30 | equal |
| data.tables.work.seams.cost_by_tier.pro | stream_costs.seams.cost_by_tier.pro | $1.49 | $1.49 | equal | $1.49 | $1.49 | equal | $1.49 | $1.49 | equal |
| data.tables.work.seams.landed | tables.work.seams.landed | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.seams.merging | tables.work.seams.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.seams.per_landed | tables.work.seams.per_landed | $0.70 | $0.70 | equal | $0.70 | $0.70 | equal | $0.70 | $0.70 | equal |
| data.tables.work.seams.ready | tables.work.seams.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.seams.review | tables.work.seams.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.seams.tiers.flash | stream_costs.seams.tiers.flash | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.seams.waiting | tables.work.seams.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.seams.working | tables.work.seams.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.security.cost | tables.work.security.cost | $1.56 | $1.56 | equal | $1.56 | $1.56 | equal | $1.56 | $1.56 | equal |
| data.tables.work.security.cost_by_tier.flash | stream_costs.security.cost_by_tier.flash | $1.38 | $1.38 | equal | $1.38 | $1.38 | equal | $1.38 | $1.38 | equal |
| data.tables.work.security.cost_by_tier.pro | stream_costs.security.cost_by_tier.pro | $0.18 | $0.18 | equal | $0.18 | $0.18 | equal | $0.18 | $0.18 | equal |
| data.tables.work.security.landed | tables.work.security.landed | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.work.security.merging | tables.work.security.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.security.per_landed | tables.work.security.per_landed | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal |
| data.tables.work.security.ready | tables.work.security.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.security.review | tables.work.security.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.security.tiers.pro | stream_costs.security.tiers.pro | 13 | 13 | equal | 13 | 13 | equal | 13 | 13 | equal |
| data.tables.work.security.waiting | tables.work.security.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.security.working | tables.work.security.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.security2.cost | tables.work.security2.cost | $37.17 | $37.17 | equal | $37.17 | $37.17 | equal | $37.17 | $37.17 | equal |
| data.tables.work.security2.cost_by_tier.flash | stream_costs.security2.cost_by_tier.flash | $1.43 | $1.43 | equal | $1.43 | $1.43 | equal | $1.43 | $1.43 | equal |
| data.tables.work.security2.cost_by_tier.pro | stream_costs.security2.cost_by_tier.pro | $35.84 | $35.84 | equal | $35.84 | $35.84 | equal | $35.84 | $35.84 | equal |
| data.tables.work.security2.landed | tables.work.security2.landed | 71 | 71 | equal | 71 | 71 | equal | 71 | 71 | equal |
| data.tables.work.security2.merging | tables.work.security2.merging | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.security2.per_landed | tables.work.security2.per_landed | $0.53 | $0.53 | equal | $0.53 | $0.53 | equal | $0.53 | $0.53 | equal |
| data.tables.work.security2.ready | tables.work.security2.ready | 0 | 1 | differ | 0 | 1 | differ | 0 | 1 | differ |
| data.tables.work.security2.review | tables.work.security2.review | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.security2.tiers.flash | stream_costs.security2.tiers.flash | 26 | 26 | equal | 26 | 26 | equal | 26 | 26 | equal |
| data.tables.work.security2.tiers.pro | stream_costs.security2.tiers.pro | 52 | 52 | equal | 52 | 52 | equal | 52 | 52 | equal |
| data.tables.work.security2.waiting | tables.work.security2.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.security2.working | tables.work.security2.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.shrink.cost | tables.work.shrink.cost | $4.35 | $4.35 | equal | $4.35 | $4.35 | equal | $4.35 | $4.35 | equal |
| data.tables.work.shrink.cost_by_tier.flash | stream_costs.shrink.cost_by_tier.flash | $1.70 | $1.70 | equal | $1.70 | $1.70 | equal | $1.70 | $1.70 | equal |
| data.tables.work.shrink.cost_by_tier.pro | stream_costs.shrink.cost_by_tier.pro | $2.90 | $2.90 | equal | $2.90 | $2.90 | equal | $2.90 | $2.90 | equal |
| data.tables.work.shrink.landed | tables.work.shrink.landed | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.work.shrink.merging | tables.work.shrink.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.shrink.per_landed | tables.work.shrink.per_landed | $0.63 | $0.63 | equal | $0.63 | $0.63 | equal | $0.63 | $0.63 | equal |
| data.tables.work.shrink.ready | tables.work.shrink.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.shrink.review | tables.work.shrink.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.shrink.tiers.flash | stream_costs.shrink.tiers.flash | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.work.shrink.waiting | tables.work.shrink.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.shrink.working | tables.work.shrink.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skeleton.cost | tables.work.skeleton.cost | $26.87 | $26.87 | equal | $26.87 | $26.87 | equal | $26.87 | $26.87 | equal |
| data.tables.work.skeleton.cost_by_tier.flash | stream_costs.skeleton.cost_by_tier.flash | $0.53 | $0.53 | equal | $0.53 | $0.53 | equal | $0.53 | $0.53 | equal |
| data.tables.work.skeleton.cost_by_tier.untiered | stream_costs.skeleton.cost_by_tier.untiered | $26.34 | $26.34 | equal | $26.34 | $26.34 | equal | $26.34 | $26.34 | equal |
| data.tables.work.skeleton.landed | tables.work.skeleton.landed | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.skeleton.merging | tables.work.skeleton.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skeleton.per_landed | tables.work.skeleton.per_landed | $8.96 | $8.96 | equal | $8.96 | $8.96 | equal | $8.96 | $8.96 | equal |
| data.tables.work.skeleton.ready | tables.work.skeleton.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skeleton.review | tables.work.skeleton.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skeleton.tiers.pro | stream_costs.skeleton.tiers.pro | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.skeleton.waiting | tables.work.skeleton.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skeleton.working | tables.work.skeleton.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skips.cost | tables.work.skips.cost | $5.81 | $5.81 | equal | $5.81 | $5.81 | equal | $5.81 | $5.81 | equal |
| data.tables.work.skips.cost_by_tier.flash | stream_costs.skips.cost_by_tier.flash | $0.80 | $0.80 | equal | $0.80 | $0.80 | equal | $0.80 | $0.80 | equal |
| data.tables.work.skips.cost_by_tier.pro | stream_costs.skips.cost_by_tier.pro | $1.12 | $1.12 | equal | $1.12 | $1.12 | equal | $1.12 | $1.12 | equal |
| data.tables.work.skips.cost_by_tier.untiered | stream_costs.skips.cost_by_tier.untiered | $3.90 | $3.90 | equal | $3.90 | $3.90 | equal | $3.90 | $3.90 | equal |
| data.tables.work.skips.landed | tables.work.skips.landed | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.work.skips.merging | tables.work.skips.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skips.per_landed | tables.work.skips.per_landed | $0.65 | $0.65 | equal | $0.65 | $0.65 | equal | $0.65 | $0.65 | equal |
| data.tables.work.skips.ready | tables.work.skips.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skips.review | tables.work.skips.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skips.tiers.pro | stream_costs.skips.tiers.pro | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.skips.waiting | tables.work.skips.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.skips.working | tables.work.skips.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.split.cost | tables.work.split.cost | $0.53 | $0.53 | equal | $0.53 | $0.53 | equal | $0.53 | $0.53 | equal |
| data.tables.work.split.cost_by_tier.flash | stream_costs.split.cost_by_tier.flash | $0.53 | $0.53 | equal | $0.53 | $0.53 | equal | $0.53 | $0.53 | equal |
| data.tables.work.split.landed | tables.work.split.landed | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.work.split.merging | tables.work.split.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.split.per_landed | tables.work.split.per_landed | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal |
| data.tables.work.split.ready | tables.work.split.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.split.review | tables.work.split.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.split.tiers.flash | stream_costs.split.tiers.flash | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.work.split.waiting | tables.work.split.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.split.working | tables.work.split.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-next.cost | tables.work.sprint-next.cost | $14.06 | $14.06 | equal | $14.06 | $14.06 | equal | $14.06 | $14.06 | equal |
| data.tables.work.sprint-next.cost_by_tier.flash | stream_costs.sprint-next.cost_by_tier.flash | $0.47 | $0.47 | equal | $0.47 | $0.47 | equal | $0.47 | $0.47 | equal |
| data.tables.work.sprint-next.cost_by_tier.pro | stream_costs.sprint-next.cost_by_tier.pro | $32.46 | $32.46 | equal | $32.46 | $32.46 | equal | $32.46 | $32.46 | equal |
| data.tables.work.sprint-next.landed | tables.work.sprint-next.landed | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.work.sprint-next.merging | tables.work.sprint-next.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-next.per_landed | tables.work.sprint-next.per_landed | $2.35 | $2.35 | equal | $2.35 | $2.35 | equal | $2.35 | $2.35 | equal |
| data.tables.work.sprint-next.ready | tables.work.sprint-next.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-next.review | tables.work.sprint-next.review | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.sprint-next.tiers.flash | stream_costs.sprint-next.tiers.flash | 7 | 7 | equal | 7 | 7 | equal | 7 | 7 | equal |
| data.tables.work.sprint-next.tiers.pro | stream_costs.sprint-next.tiers.pro | 18 | 18 | equal | 18 | 18 | equal | 18 | 18 | equal |
| data.tables.work.sprint-next.waiting | tables.work.sprint-next.waiting | 12 | 12 | equal | 12 | 12 | equal | 12 | 12 | equal |
| data.tables.work.sprint-next.working | tables.work.sprint-next.working | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.sprint-v1-1-0.landed | tables.work.sprint-v1-1-0.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-1-0.merging | tables.work.sprint-v1-1-0.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-1-0.ready | tables.work.sprint-v1-1-0.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-1-0.review | tables.work.sprint-v1-1-0.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-1-0.waiting | tables.work.sprint-v1-1-0.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-1-0.working | tables.work.sprint-v1-1-0.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-comfort.cost | tables.work.sprint-v1-comfort.cost | $11.74 | $11.74 | equal | $11.74 | $11.74 | equal | $11.74 | $11.74 | equal |
| data.tables.work.sprint-v1-comfort.cost_by_tier.pro | stream_costs.sprint-v1-comfort.cost_by_tier.pro | $11.74 | $11.74 | equal | $11.74 | $11.74 | equal | $11.74 | $11.74 | equal |
| data.tables.work.sprint-v1-comfort.landed | tables.work.sprint-v1-comfort.landed | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.work.sprint-v1-comfort.merging | tables.work.sprint-v1-comfort.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-comfort.per_landed | tables.work.sprint-v1-comfort.per_landed | $1.96 | $1.96 | equal | $1.96 | $1.96 | equal | $1.96 | $1.96 | equal |
| data.tables.work.sprint-v1-comfort.ready | tables.work.sprint-v1-comfort.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-comfort.review | tables.work.sprint-v1-comfort.review | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-comfort.tiers.flash | stream_costs.sprint-v1-comfort.tiers.flash | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.sprint-v1-comfort.tiers.pro | stream_costs.sprint-v1-comfort.tiers.pro | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.sprint-v1-comfort.waiting | tables.work.sprint-v1-comfort.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-comfort.working | tables.work.sprint-v1-comfort.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-docs.landed | tables.work.sprint-v1-docs.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-docs.merging | tables.work.sprint-v1-docs.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-docs.ready | tables.work.sprint-v1-docs.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-docs.review | tables.work.sprint-v1-docs.review | 0 | 0 | equal | 0 | 1 | differ | 0 | 1 | differ |
| data.tables.work.sprint-v1-docs.tiers.pro | stream_costs.sprint-v1-docs.tiers.pro | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-docs.waiting | tables.work.sprint-v1-docs.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-docs.working | tables.work.sprint-v1-docs.working | 1 | 1 | equal | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.sprint-v1-integrity.landed | tables.work.sprint-v1-integrity.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-integrity.merging | tables.work.sprint-v1-integrity.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-integrity.ready | tables.work.sprint-v1-integrity.ready | 0 | 1 | differ | 0 | 1 | differ | 0 | 1 | differ |
| data.tables.work.sprint-v1-integrity.review | tables.work.sprint-v1-integrity.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-integrity.tiers.flash | stream_costs.sprint-v1-integrity.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-integrity.tiers.pro | stream_costs.sprint-v1-integrity.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-integrity.waiting | tables.work.sprint-v1-integrity.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-integrity.working | tables.work.sprint-v1-integrity.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.sprint-v1-jev.cost | tables.work.sprint-v1-jev.cost | $0.22 | $0.22 | equal | $0.22 | $0.22 | equal | $0.22 | $0.22 | equal |
| data.tables.work.sprint-v1-jev.cost_by_tier.flash | stream_costs.sprint-v1-jev.cost_by_tier.flash | $0.01 | $0.01 | equal | $0.01 | $0.01 | equal | $0.01 | $0.01 | equal |
| data.tables.work.sprint-v1-jev.cost_by_tier.pro | stream_costs.sprint-v1-jev.cost_by_tier.pro | $0.22 | $0.22 | equal | $0.22 | $0.22 | equal | $0.22 | $0.22 | equal |
| data.tables.work.sprint-v1-jev.landed | tables.work.sprint-v1-jev.landed | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.sprint-v1-jev.merging | tables.work.sprint-v1-jev.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-jev.per_landed | tables.work.sprint-v1-jev.per_landed | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal |
| data.tables.work.sprint-v1-jev.ready | tables.work.sprint-v1-jev.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-jev.review | tables.work.sprint-v1-jev.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-jev.tiers.pro | stream_costs.sprint-v1-jev.tiers.pro | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.sprint-v1-jev.waiting | tables.work.sprint-v1-jev.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-jev.working | tables.work.sprint-v1-jev.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-models.cost_by_tier.pro | stream_costs.sprint-v1-models.cost_by_tier.pro | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal |
| data.tables.work.sprint-v1-models.landed | tables.work.sprint-v1-models.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-models.merging | tables.work.sprint-v1-models.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-models.ready | tables.work.sprint-v1-models.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-models.review | tables.work.sprint-v1-models.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-models.tiers.pro | stream_costs.sprint-v1-models.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-models.waiting | tables.work.sprint-v1-models.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-models.working | tables.work.sprint-v1-models.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-processor.cost | tables.work.sprint-v1-processor.cost | $2.74 | $2.74 | equal | $2.74 | $2.74 | equal | $2.74 | $2.74 | equal |
| data.tables.work.sprint-v1-processor.cost_by_tier.flash | stream_costs.sprint-v1-processor.cost_by_tier.flash | $0.45 | $0.45 | equal | $0.45 | $0.45 | equal | $0.45 | $0.45 | equal |
| data.tables.work.sprint-v1-processor.cost_by_tier.pro | stream_costs.sprint-v1-processor.cost_by_tier.pro | $6.70 | $6.70 | equal | $6.70 | $6.70 | equal | $6.70 | $6.70 | equal |
| data.tables.work.sprint-v1-processor.landed | tables.work.sprint-v1-processor.landed | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.sprint-v1-processor.merging | tables.work.sprint-v1-processor.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-processor.per_landed | tables.work.sprint-v1-processor.per_landed | $0.92 | $0.92 | equal | $0.92 | $0.92 | equal | $0.92 | $0.92 | equal |
| data.tables.work.sprint-v1-processor.ready | tables.work.sprint-v1-processor.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-processor.review | tables.work.sprint-v1-processor.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-processor.tiers.flash | stream_costs.sprint-v1-processor.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-processor.tiers.heavy | stream_costs.sprint-v1-processor.tiers.heavy | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.sprint-v1-processor.tiers.pro | stream_costs.sprint-v1-processor.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-processor.waiting | tables.work.sprint-v1-processor.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-processor.working | tables.work.sprint-v1-processor.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-release.landed | tables.work.sprint-v1-release.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-release.merging | tables.work.sprint-v1-release.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-release.ready | tables.work.sprint-v1-release.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-release.review | tables.work.sprint-v1-release.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-release.tiers.flash | stream_costs.sprint-v1-release.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-release.tiers.pro | stream_costs.sprint-v1-release.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-release.waiting | tables.work.sprint-v1-release.waiting | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-release.working | tables.work.sprint-v1-release.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-safety.cost_by_tier.flash | stream_costs.sprint-v1-safety.cost_by_tier.flash | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal | $0.08 | $0.08 | equal |
| data.tables.work.sprint-v1-safety.cost_by_tier.pro | stream_costs.sprint-v1-safety.cost_by_tier.pro | $0.14 | $0.14 | equal | $0.14 | $0.14 | equal | $0.14 | $0.14 | equal |
| data.tables.work.sprint-v1-safety.landed | tables.work.sprint-v1-safety.landed | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.sprint-v1-safety.merging | tables.work.sprint-v1-safety.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-safety.ready | tables.work.sprint-v1-safety.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-safety.review | tables.work.sprint-v1-safety.review | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.sprint-v1-safety.tiers.flash | stream_costs.sprint-v1-safety.tiers.flash | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.sprint-v1-safety.tiers.pro | stream_costs.sprint-v1-safety.tiers.pro | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.sprint-v1-safety.waiting | tables.work.sprint-v1-safety.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-safety.working | tables.work.sprint-v1-safety.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-setup.landed | tables.work.sprint-v1-setup.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-setup.merging | tables.work.sprint-v1-setup.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-setup.ready | tables.work.sprint-v1-setup.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-setup.review | tables.work.sprint-v1-setup.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-setup.tiers.flash | stream_costs.sprint-v1-setup.tiers.flash | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-setup.tiers.heavy | stream_costs.sprint-v1-setup.tiers.heavy | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-setup.waiting | tables.work.sprint-v1-setup.waiting | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-setup.working | tables.work.sprint-v1-setup.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-simplicity.cost_by_tier.pro | stream_costs.sprint-v1-simplicity.cost_by_tier.pro | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal |
| data.tables.work.sprint-v1-simplicity.landed | tables.work.sprint-v1-simplicity.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-simplicity.merging | tables.work.sprint-v1-simplicity.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-simplicity.ready | tables.work.sprint-v1-simplicity.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-simplicity.review | tables.work.sprint-v1-simplicity.review | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-simplicity.tiers.flash | stream_costs.sprint-v1-simplicity.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-simplicity.tiers.pro | stream_costs.sprint-v1-simplicity.tiers.pro | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-simplicity.waiting | tables.work.sprint-v1-simplicity.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-simplicity.working | tables.work.sprint-v1-simplicity.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-sre.cost_by_tier.flash | stream_costs.sprint-v1-sre.cost_by_tier.flash | $0.18 | $0.18 | equal | $0.18 | $0.18 | equal | $0.18 | $0.18 | equal |
| data.tables.work.sprint-v1-sre.cost_by_tier.pro | stream_costs.sprint-v1-sre.cost_by_tier.pro | $0.34 | $0.34 | equal | $0.34 | $0.34 | equal | $0.34 | $0.34 | equal |
| data.tables.work.sprint-v1-sre.landed | tables.work.sprint-v1-sre.landed | 8 | 8 | equal | 8 | 8 | equal | 8 | 9 | differ |
| data.tables.work.sprint-v1-sre.merging | tables.work.sprint-v1-sre.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-sre.ready | tables.work.sprint-v1-sre.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-sre.review | tables.work.sprint-v1-sre.review | 4 | 5 | differ | 4 | 5 | differ | 4 | 4 | equal |
| data.tables.work.sprint-v1-sre.tiers.flash | stream_costs.sprint-v1-sre.tiers.flash | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.sprint-v1-sre.tiers.pro | stream_costs.sprint-v1-sre.tiers.pro | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.sprint-v1-sre.waiting | tables.work.sprint-v1-sre.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-sre.working | tables.work.sprint-v1-sre.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.sprint-v1-verbs.cost | tables.work.sprint-v1-verbs.cost | $6.08 | $6.08 | equal | $6.08 | $6.08 | equal | $6.08 | $6.08 | equal |
| data.tables.work.sprint-v1-verbs.cost_by_tier.flash | stream_costs.sprint-v1-verbs.cost_by_tier.flash | $0.20 | $0.20 | equal | $0.20 | $0.20 | equal | $0.20 | $0.20 | equal |
| data.tables.work.sprint-v1-verbs.cost_by_tier.pro | stream_costs.sprint-v1-verbs.cost_by_tier.pro | $5.89 | $5.89 | equal | $5.89 | $5.89 | equal | $5.89 | $5.89 | equal |
| data.tables.work.sprint-v1-verbs.landed | tables.work.sprint-v1-verbs.landed | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.sprint-v1-verbs.merging | tables.work.sprint-v1-verbs.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-verbs.per_landed | tables.work.sprint-v1-verbs.per_landed | $0.68 | $0.68 | equal | $0.68 | $0.68 | equal | $0.68 | $0.68 | equal |
| data.tables.work.sprint-v1-verbs.ready | tables.work.sprint-v1-verbs.ready | 0 | 1 | differ | 0 | 1 | differ | 0 | 1 | differ |
| data.tables.work.sprint-v1-verbs.review | tables.work.sprint-v1-verbs.review | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-verbs.tiers.flash | stream_costs.sprint-v1-verbs.tiers.flash | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tables.work.sprint-v1-verbs.tiers.pro | stream_costs.sprint-v1-verbs.tiers.pro | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.sprint-v1-verbs.waiting | tables.work.sprint-v1-verbs.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-verbs.working | tables.work.sprint-v1-verbs.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.sprint-v1-wallclock.cost | tables.work.sprint-v1-wallclock.cost | $1.52 | $1.52 | equal | $1.52 | $1.52 | equal | $1.52 | $1.52 | equal |
| data.tables.work.sprint-v1-wallclock.cost_by_tier.flash | stream_costs.sprint-v1-wallclock.cost_by_tier.flash | $0.01 | $0.01 | equal | $0.01 | $0.01 | equal | $0.01 | $0.01 | equal |
| data.tables.work.sprint-v1-wallclock.cost_by_tier.pro | stream_costs.sprint-v1-wallclock.cost_by_tier.pro | $1.57 | $1.57 | equal | $1.57 | $1.57 | equal | $1.57 | $1.57 | equal |
| data.tables.work.sprint-v1-wallclock.landed | tables.work.sprint-v1-wallclock.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-wallclock.merging | tables.work.sprint-v1-wallclock.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-wallclock.per_landed | tables.work.sprint-v1-wallclock.per_landed | $0.76 | $0.76 | equal | $0.76 | $0.76 | equal | $0.76 | $0.76 | equal |
| data.tables.work.sprint-v1-wallclock.ready | tables.work.sprint-v1-wallclock.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-wallclock.review | tables.work.sprint-v1-wallclock.review | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-wallclock.tiers.flash | stream_costs.sprint-v1-wallclock.tiers.flash | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.sprint-v1-wallclock.tiers.pro | stream_costs.sprint-v1-wallclock.tiers.pro | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.sprint-v1-wallclock.waiting | tables.work.sprint-v1-wallclock.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-wallclock.working | tables.work.sprint-v1-wallclock.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-yes.cost | tables.work.sprint-v1-yes.cost | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal |
| data.tables.work.sprint-v1-yes.cost_by_tier.flash | stream_costs.sprint-v1-yes.cost_by_tier.flash | $0.96 | $0.96 | equal | $0.96 | $0.96 | equal | $0.96 | $0.96 | equal |
| data.tables.work.sprint-v1-yes.cost_by_tier.pro | stream_costs.sprint-v1-yes.cost_by_tier.pro | $16.37 | $16.37 | equal | $16.37 | $16.37 | equal | $16.37 | $16.37 | equal |
| data.tables.work.sprint-v1-yes.landed | tables.work.sprint-v1-yes.landed | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.sprint-v1-yes.merging | tables.work.sprint-v1-yes.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-yes.per_landed | tables.work.sprint-v1-yes.per_landed | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal |
| data.tables.work.sprint-v1-yes.ready | tables.work.sprint-v1-yes.ready | 0 | 1 | differ | 0 | 1 | differ | 0 | 1 | differ |
| data.tables.work.sprint-v1-yes.review | tables.work.sprint-v1-yes.review | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.sprint-v1-yes.tiers.flash | stream_costs.sprint-v1-yes.tiers.flash | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.sprint-v1-yes.tiers.frontier | stream_costs.sprint-v1-yes.tiers.frontier | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-v1-yes.tiers.heavy | stream_costs.sprint-v1-yes.tiers.heavy | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.sprint-v1-yes.waiting | tables.work.sprint-v1-yes.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-v1-yes.working | tables.work.sprint-v1-yes.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.sprint-verbs-2026-10-04.cost | tables.work.sprint-verbs-2026-10-04.cost | $35.36 | $35.36 | equal | $35.36 | $35.36 | equal | $35.36 | $35.36 | equal |
| data.tables.work.sprint-verbs-2026-10-04.cost_by_tier.flash | stream_costs.sprint-verbs-2026-10-04.cost_by_tier.flash | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal |
| data.tables.work.sprint-verbs-2026-10-04.cost_by_tier.pro | stream_costs.sprint-verbs-2026-10-04.cost_by_tier.pro | $39.90 | $39.90 | equal | $39.90 | $39.90 | equal | $39.90 | $39.90 | equal |
| data.tables.work.sprint-verbs-2026-10-04.landed | tables.work.sprint-verbs-2026-10-04.landed | 16 | 16 | equal | 16 | 16 | equal | 16 | 16 | equal |
| data.tables.work.sprint-verbs-2026-10-04.merging | tables.work.sprint-verbs-2026-10-04.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-verbs-2026-10-04.per_landed | tables.work.sprint-verbs-2026-10-04.per_landed | $2.21 | $2.21 | equal | $2.21 | $2.21 | equal | $2.21 | $2.21 | equal |
| data.tables.work.sprint-verbs-2026-10-04.ready | tables.work.sprint-verbs-2026-10-04.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-verbs-2026-10-04.review | tables.work.sprint-verbs-2026-10-04.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.sprint-verbs-2026-10-04.tiers.pro | stream_costs.sprint-verbs-2026-10-04.tiers.pro | 17 | 17 | equal | 17 | 17 | equal | 17 | 17 | equal |
| data.tables.work.sprint-verbs-2026-10-04.waiting | tables.work.sprint-verbs-2026-10-04.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.sprint-verbs-2026-10-04.working | tables.work.sprint-verbs-2026-10-04.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.status.cost | tables.work.status.cost | $87.84 | $87.84 | equal | $87.84 | $87.84 | equal | $87.84 | $87.84 | equal |
| data.tables.work.status.cost_by_tier.flash | stream_costs.status.cost_by_tier.flash | $1.63 | $1.63 | equal | $1.63 | $1.63 | equal | $1.63 | $1.63 | equal |
| data.tables.work.status.cost_by_tier.pro | stream_costs.status.cost_by_tier.pro | $4.81 | $4.81 | equal | $4.81 | $4.81 | equal | $4.81 | $4.81 | equal |
| data.tables.work.status.cost_by_tier.untiered | stream_costs.status.cost_by_tier.untiered | $81.41 | $81.41 | equal | $81.41 | $81.41 | equal | $81.41 | $81.41 | equal |
| data.tables.work.status.landed | tables.work.status.landed | 11 | 11 | equal | 11 | 11 | equal | 11 | 11 | equal |
| data.tables.work.status.merging | tables.work.status.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.status.per_landed | tables.work.status.per_landed | $8.79 | $8.79 | equal | $8.79 | $8.79 | equal | $8.79 | $8.79 | equal |
| data.tables.work.status.ready | tables.work.status.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.status.review | tables.work.status.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.status.tiers.pro | stream_costs.status.tiers.pro | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.work.status.waiting | tables.work.status.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.status.working | tables.work.status.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szcfloat.cost | tables.work.szcfloat.cost | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal |
| data.tables.work.szcfloat.cost_by_tier.flash | stream_costs.szcfloat.cost_by_tier.flash | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal |
| data.tables.work.szcfloat.landed | tables.work.szcfloat.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.szcfloat.merging | tables.work.szcfloat.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szcfloat.per_landed | tables.work.szcfloat.per_landed | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal | $0.21 | $0.21 | equal |
| data.tables.work.szcfloat.ready | tables.work.szcfloat.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szcfloat.review | tables.work.szcfloat.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szcfloat.tiers.flash | stream_costs.szcfloat.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.szcfloat.waiting | tables.work.szcfloat.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szcfloat.working | tables.work.szcfloat.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szci.cost | tables.work.szci.cost | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal |
| data.tables.work.szci.cost_by_tier.flash | stream_costs.szci.cost_by_tier.flash | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal |
| data.tables.work.szci.landed | tables.work.szci.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.szci.merging | tables.work.szci.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szci.per_landed | tables.work.szci.per_landed | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal |
| data.tables.work.szci.ready | tables.work.szci.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szci.review | tables.work.szci.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szci.tiers.flash | stream_costs.szci.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.szci.waiting | tables.work.szci.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szci.working | tables.work.szci.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szclean.cost | tables.work.szclean.cost | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal |
| data.tables.work.szclean.cost_by_tier.flash | stream_costs.szclean.cost_by_tier.flash | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal |
| data.tables.work.szclean.landed | tables.work.szclean.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.szclean.merging | tables.work.szclean.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szclean.per_landed | tables.work.szclean.per_landed | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal | $0.09 | $0.09 | equal |
| data.tables.work.szclean.ready | tables.work.szclean.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szclean.review | tables.work.szclean.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szclean.tiers.flash | stream_costs.szclean.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.szclean.waiting | tables.work.szclean.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szclean.working | tables.work.szclean.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szdegen.cost | tables.work.szdegen.cost | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal |
| data.tables.work.szdegen.cost_by_tier.flash | stream_costs.szdegen.cost_by_tier.flash | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal |
| data.tables.work.szdegen.landed | tables.work.szdegen.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.szdegen.merging | tables.work.szdegen.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szdegen.per_landed | tables.work.szdegen.per_landed | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal | $0.12 | $0.12 | equal |
| data.tables.work.szdegen.ready | tables.work.szdegen.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szdegen.review | tables.work.szdegen.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szdegen.tiers.flash | stream_costs.szdegen.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.szdegen.waiting | tables.work.szdegen.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szdegen.working | tables.work.szdegen.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szex.cost | tables.work.szex.cost | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal |
| data.tables.work.szex.cost_by_tier.flash | stream_costs.szex.cost_by_tier.flash | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal |
| data.tables.work.szex.landed | tables.work.szex.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.szex.merging | tables.work.szex.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szex.per_landed | tables.work.szex.per_landed | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal | $0.03 | $0.03 | equal |
| data.tables.work.szex.ready | tables.work.szex.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szex.review | tables.work.szex.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szex.tiers.flash | stream_costs.szex.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.szex.waiting | tables.work.szex.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szex.working | tables.work.szex.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szgolden.cost | tables.work.szgolden.cost | $0.34 | $0.34 | equal | $0.34 | $0.34 | equal | $0.34 | $0.34 | equal |
| data.tables.work.szgolden.cost_by_tier.flash | stream_costs.szgolden.cost_by_tier.flash | $0.34 | $0.34 | equal | $0.34 | $0.34 | equal | $0.34 | $0.34 | equal |
| data.tables.work.szgolden.landed | tables.work.szgolden.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.szgolden.merging | tables.work.szgolden.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szgolden.per_landed | tables.work.szgolden.per_landed | $0.34 | $0.34 | equal | $0.34 | $0.34 | equal | $0.34 | $0.34 | equal |
| data.tables.work.szgolden.ready | tables.work.szgolden.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szgolden.review | tables.work.szgolden.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szgolden.tiers.flash | stream_costs.szgolden.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.szgolden.waiting | tables.work.szgolden.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szgolden.working | tables.work.szgolden.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szterm.cost | tables.work.szterm.cost | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal |
| data.tables.work.szterm.cost_by_tier.flash | stream_costs.szterm.cost_by_tier.flash | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal |
| data.tables.work.szterm.landed | tables.work.szterm.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.szterm.merging | tables.work.szterm.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szterm.per_landed | tables.work.szterm.per_landed | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal | $0.07 | $0.07 | equal |
| data.tables.work.szterm.ready | tables.work.szterm.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szterm.review | tables.work.szterm.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szterm.tiers.flash | stream_costs.szterm.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.szterm.waiting | tables.work.szterm.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szterm.working | tables.work.szterm.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szvalid.cost | tables.work.szvalid.cost | $0.15 | $0.15 | equal | $0.15 | $0.15 | equal | $0.15 | $0.15 | equal |
| data.tables.work.szvalid.cost_by_tier.flash | stream_costs.szvalid.cost_by_tier.flash | $0.15 | $0.15 | equal | $0.15 | $0.15 | equal | $0.15 | $0.15 | equal |
| data.tables.work.szvalid.landed | tables.work.szvalid.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.szvalid.merging | tables.work.szvalid.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szvalid.per_landed | tables.work.szvalid.per_landed | $0.15 | $0.15 | equal | $0.15 | $0.15 | equal | $0.15 | $0.15 | equal |
| data.tables.work.szvalid.ready | tables.work.szvalid.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szvalid.review | tables.work.szvalid.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szvalid.tiers.flash | stream_costs.szvalid.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.szvalid.waiting | tables.work.szvalid.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.szvalid.working | tables.work.szvalid.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tenv.cost | tables.work.tenv.cost | $26.74 | $26.74 | equal | $26.74 | $26.74 | equal | $26.74 | $26.74 | equal |
| data.tables.work.tenv.cost_by_tier.pro | stream_costs.tenv.cost_by_tier.pro | $33.55 | $33.55 | equal | $33.55 | $33.55 | equal | $33.55 | $33.55 | equal |
| data.tables.work.tenv.landed | tables.work.tenv.landed | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.work.tenv.merging | tables.work.tenv.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tenv.per_landed | tables.work.tenv.per_landed | $2.98 | $2.98 | equal | $2.98 | $2.98 | equal | $2.98 | $2.98 | equal |
| data.tables.work.tenv.ready | tables.work.tenv.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tenv.review | tables.work.tenv.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tenv.tiers.flash | stream_costs.tenv.tiers.flash | 7 | 7 | equal | 7 | 7 | equal | 7 | 7 | equal |
| data.tables.work.tenv.tiers.pro | stream_costs.tenv.tiers.pro | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.tenv.waiting | tables.work.tenv.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tenv.working | tables.work.tenv.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.testnames.cost | tables.work.testnames.cost | $9.39 | $9.39 | equal | $9.39 | $9.39 | equal | $9.39 | $9.39 | equal |
| data.tables.work.testnames.cost_by_tier.untiered | stream_costs.testnames.cost_by_tier.untiered | $9.39 | $9.39 | equal | $9.39 | $9.39 | equal | $9.39 | $9.39 | equal |
| data.tables.work.testnames.landed | tables.work.testnames.landed | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.testnames.merging | tables.work.testnames.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.testnames.per_landed | tables.work.testnames.per_landed | $2.35 | $2.35 | equal | $2.35 | $2.35 | equal | $2.35 | $2.35 | equal |
| data.tables.work.testnames.ready | tables.work.testnames.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.testnames.review | tables.work.testnames.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.testnames.tiers.pro | stream_costs.testnames.tiers.pro | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.testnames.waiting | tables.work.testnames.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.testnames.working | tables.work.testnames.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tests.cost | tables.work.tests.cost | $0.54 | $0.54 | equal | $0.54 | $0.54 | equal | $0.54 | $0.54 | equal |
| data.tables.work.tests.cost_by_tier.untiered | stream_costs.tests.cost_by_tier.untiered | $0.54 | $0.54 | equal | $0.54 | $0.54 | equal | $0.54 | $0.54 | equal |
| data.tables.work.tests.landed | tables.work.tests.landed | 10 | 10 | equal | 10 | 10 | equal | 10 | 10 | equal |
| data.tables.work.tests.merging | tables.work.tests.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tests.per_landed | tables.work.tests.per_landed | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal | $0.06 | $0.06 | equal |
| data.tables.work.tests.ready | tables.work.tests.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tests.review | tables.work.tests.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tests.tiers.flash | stream_costs.tests.tiers.flash | 9 | 9 | equal | 9 | 9 | equal | 9 | 9 | equal |
| data.tables.work.tests.waiting | tables.work.tests.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tests.working | tables.work.tests.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tla.landed | tables.work.tla.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.tla.merging | tables.work.tla.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tla.ready | tables.work.tla.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tla.review | tables.work.tla.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tla.waiting | tables.work.tla.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tla.working | tables.work.tla.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-efficiency.cost | tables.work.today-efficiency.cost | $12.23 | $12.23 | equal | $12.23 | $12.23 | equal | $12.23 | $12.23 | equal |
| data.tables.work.today-efficiency.cost_by_tier.pro | stream_costs.today-efficiency.cost_by_tier.pro | $12.23 | $12.23 | equal | $12.23 | $12.23 | equal | $12.23 | $12.23 | equal |
| data.tables.work.today-efficiency.landed | tables.work.today-efficiency.landed | 5 | 5 | equal | 5 | 5 | equal | 5 | 5 | equal |
| data.tables.work.today-efficiency.merging | tables.work.today-efficiency.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-efficiency.per_landed | tables.work.today-efficiency.per_landed | $2.45 | $2.45 | equal | $2.45 | $2.45 | equal | $2.45 | $2.45 | equal |
| data.tables.work.today-efficiency.ready | tables.work.today-efficiency.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-efficiency.review | tables.work.today-efficiency.review | 0 | 1 | differ | 0 | 1 | differ | 0 | 1 | differ |
| data.tables.work.today-efficiency.tiers.pro | stream_costs.today-efficiency.tiers.pro | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.work.today-efficiency.waiting | tables.work.today-efficiency.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-efficiency.working | tables.work.today-efficiency.working | 1 | 0 | differ | 1 | 0 | differ | 1 | 0 | differ |
| data.tables.work.today-machinery.landed | tables.work.today-machinery.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.today-machinery.merging | tables.work.today-machinery.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-machinery.ready | tables.work.today-machinery.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-machinery.review | tables.work.today-machinery.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-machinery.waiting | tables.work.today-machinery.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-machinery.working | tables.work.today-machinery.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-tiers.cost | tables.work.today-tiers.cost | $2.80 | $2.80 | equal | $2.80 | $2.80 | equal | $2.80 | $2.80 | equal |
| data.tables.work.today-tiers.cost_by_tier.pro | stream_costs.today-tiers.cost_by_tier.pro | $2.80 | $2.80 | equal | $2.80 | $2.80 | equal | $2.80 | $2.80 | equal |
| data.tables.work.today-tiers.landed | tables.work.today-tiers.landed | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.today-tiers.merging | tables.work.today-tiers.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-tiers.per_landed | tables.work.today-tiers.per_landed | $0.70 | $0.70 | equal | $0.70 | $0.70 | equal | $0.70 | $0.70 | equal |
| data.tables.work.today-tiers.ready | tables.work.today-tiers.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-tiers.review | tables.work.today-tiers.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-tiers.tiers.pro | stream_costs.today-tiers.tiers.pro | 4 | 4 | equal | 4 | 4 | equal | 4 | 4 | equal |
| data.tables.work.today-tiers.waiting | tables.work.today-tiers.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.today-tiers.working | tables.work.today-tiers.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.toolkit.cost | tables.work.toolkit.cost | $26.81 | $26.81 | equal | $26.81 | $26.81 | equal | $26.81 | $26.81 | equal |
| data.tables.work.toolkit.cost_by_tier.flash | stream_costs.toolkit.cost_by_tier.flash | $0.24 | $0.24 | equal | $0.24 | $0.24 | equal | $0.24 | $0.24 | equal |
| data.tables.work.toolkit.cost_by_tier.pro | stream_costs.toolkit.cost_by_tier.pro | $32.89 | $32.89 | equal | $32.89 | $32.89 | equal | $32.89 | $32.89 | equal |
| data.tables.work.toolkit.landed | tables.work.toolkit.landed | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.work.toolkit.merging | tables.work.toolkit.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.toolkit.per_landed | tables.work.toolkit.per_landed | $5.37 | $5.37 | equal | $5.37 | $5.37 | equal | $5.37 | $5.37 | equal |
| data.tables.work.toolkit.ready | tables.work.toolkit.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.toolkit.review | tables.work.toolkit.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.toolkit.tiers.flash | stream_costs.toolkit.tiers.flash | 6 | 6 | equal | 6 | 6 | equal | 6 | 6 | equal |
| data.tables.work.toolkit.waiting | tables.work.toolkit.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.toolkit.working | tables.work.toolkit.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-docs.cost | tables.work.tools-v1-2-0-docs.cost | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal |
| data.tables.work.tools-v1-2-0-docs.cost_by_tier.pro | stream_costs.tools-v1-2-0-docs.cost_by_tier.pro | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal |
| data.tables.work.tools-v1-2-0-docs.landed | tables.work.tools-v1-2-0-docs.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-docs.merging | tables.work.tools-v1-2-0-docs.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-docs.per_landed | tables.work.tools-v1-2-0-docs.per_landed | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal |
| data.tables.work.tools-v1-2-0-docs.ready | tables.work.tools-v1-2-0-docs.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-docs.review | tables.work.tools-v1-2-0-docs.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-docs.tiers.flash | stream_costs.tools-v1-2-0-docs.tiers.flash | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.tools-v1-2-0-docs.tiers.pro | stream_costs.tools-v1-2-0-docs.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-docs.waiting | tables.work.tools-v1-2-0-docs.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-docs.working | tables.work.tools-v1-2-0-docs.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-setup.cost_by_tier.flash | stream_costs.tools-v1-2-0-setup.cost_by_tier.flash | $0.01 | $0.01 | equal | $0.01 | $0.01 | equal | $0.01 | $0.01 | equal |
| data.tables.work.tools-v1-2-0-setup.cost_by_tier.pro | stream_costs.tools-v1-2-0-setup.cost_by_tier.pro | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal | $0.04 | $0.04 | equal |
| data.tables.work.tools-v1-2-0-setup.landed | tables.work.tools-v1-2-0-setup.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-setup.merging | tables.work.tools-v1-2-0-setup.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-setup.ready | tables.work.tools-v1-2-0-setup.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-setup.review | tables.work.tools-v1-2-0-setup.review | 3 | 3 | equal | 3 | 3 | equal | 3 | 3 | equal |
| data.tables.work.tools-v1-2-0-setup.tiers.flash | stream_costs.tools-v1-2-0-setup.tiers.flash | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.tools-v1-2-0-setup.tiers.heavy | stream_costs.tools-v1-2-0-setup.tiers.heavy | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-setup.tiers.pro | stream_costs.tools-v1-2-0-setup.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-setup.waiting | tables.work.tools-v1-2-0-setup.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-setup.working | tables.work.tools-v1-2-0-setup.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-stranger.landed | tables.work.tools-v1-2-0-stranger.landed | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-stranger.merging | tables.work.tools-v1-2-0-stranger.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-stranger.ready | tables.work.tools-v1-2-0-stranger.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.tools-v1-2-0-stranger.review | tables.work.tools-v1-2-0-stranger.review | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-stranger.tiers.flash | stream_costs.tools-v1-2-0-stranger.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-stranger.tiers.pro | stream_costs.tools-v1-2-0-stranger.tiers.pro | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-stranger.waiting | tables.work.tools-v1-2-0-stranger.waiting | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.tools-v1-2-0-stranger.working | tables.work.tools-v1-2-0-stranger.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ttime.cost | tables.work.ttime.cost | $0.35 | $0.35 | equal | $0.35 | $0.35 | equal | $0.35 | $0.35 | equal |
| data.tables.work.ttime.cost_by_tier.flash | stream_costs.ttime.cost_by_tier.flash | $0.35 | $0.35 | equal | $0.35 | $0.35 | equal | $0.35 | $0.35 | equal |
| data.tables.work.ttime.landed | tables.work.ttime.landed | 2 | 2 | equal | 2 | 2 | equal | 2 | 2 | equal |
| data.tables.work.ttime.merging | tables.work.ttime.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ttime.per_landed | tables.work.ttime.per_landed | $0.35 | $0.35 | equal | $0.35 | $0.35 | equal | $0.35 | $0.35 | equal |
| data.tables.work.ttime.ready | tables.work.ttime.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ttime.review | tables.work.ttime.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ttime.tiers.flash | stream_costs.ttime.tiers.flash | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.ttime.waiting | tables.work.ttime.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.ttime.working | tables.work.ttime.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.use.landed | tables.work.use.landed | 1 | 1 | equal | 1 | 1 | equal | 1 | 1 | equal |
| data.tables.work.use.merging | tables.work.use.merging | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.use.ready | tables.work.use.ready | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.use.review | tables.work.use.review | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.use.waiting | tables.work.use.waiting | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tables.work.use.working | tables.work.use.working | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| data.tiers.flash | tiers.flash | 1184 | 1185 | differ | 1184 | 1185 | differ | 1184 | 1186 | differ |
| data.tiers.frontier | tiers.frontier | 49 | 49 | equal | 49 | 49 | equal | 49 | 49 | equal |
| data.tiers.heavy | tiers.heavy | 8 | 8 | equal | 8 | 8 | equal | 8 | 8 | equal |
| data.tiers.pro | tiers.pro | 549 | 549 | equal | 549 | 549 | equal | 549 | 549 | equal |
| data.width | width | 0 | 0 | equal | 0 | 0 | equal | 0 | 0 | equal |
| minInterval | (none) | 1.0 |  | differ | 1.0 |  | differ | 1.0 |  | differ |
| readSeconds | (none) | 0.006 |  | differ | 0.009 |  | differ | 0.01 |  | differ |
| throughput | computation from data.landed over the poller's hour ring | 0.0 |  | differ | 0.0 |  | differ | 0.0 |  | differ |
| throughputMinutes | (none) | 60.0 |  | differ | 60.0 |  | differ | 60.0 |  | differ |

## What was not measured

`nova-bus log` was not run. It would open the bus store, and this measurement was not allowed to open Redis. `nova-bus --version` did run. `nova-bus2` is not installed. `/api/sprint` serves no bus count, so no bus number was left untraced for lack of that log.

`TestAcceptanceRecordsAreWellFormed` was not run. This card does not run that placeholder.

Landing stamps and STOPPED spans are not fields of `where --json`, so the ETA was compared as the summary string `where` serves, not recomputed from those stamps. The percentage inside each summary was the one-decimal form of `100*landed/all` for that document's own landed and all.
