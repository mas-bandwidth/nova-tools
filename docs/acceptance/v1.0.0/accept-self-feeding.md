Verdict: FAIL

no judgment older than 15 minutes, the idle alarm fires and clears correctly, friend ready queues stay at 2x width.

Window start: 2026-10-05T18:12:00Z
Window end: 2026-10-05T19:12:00Z

The window is the first sample through the sample 3600 seconds later. The start and end lines are `date -u` on the sampler (`2026-10-05T18:12:00Z` at sample 0, `2026-10-05T19:12:00Z` when sample 60 returned). Lines inside a criterion use `where`'s own `at`, in UTC, which is up to a few seconds later. The loop stopped at the hour. `nova-sprint log` and `nova-sprint stats` ran next and the loop finished at 2026-10-05T19:12:05Z. The window was not stretched and was not restarted.

## Build measured

- nova-sprint v1.2.0-dev.d8b7e04 darwin/arm64 go1.26.6 (`/Users/glenn/.local/bin/nova-sprint`)
- nova-bus v1.2.0-dev.d8b7e04 darwin/arm64 go1.26.6 (`/Users/glenn/.local/bin/nova-bus`)
- dashboard `build` 5b91f4b5 on all 61 responses from `http://127.0.0.1:7390/api/sprint`
- server `NOVA_SPRINT_SERVER=127.0.0.1:6390`, actor `NOVA_SPRINT_ACTOR=rowan`, `NOVA_SPRINT_REDIS` and `NOVA_REDIS_ADDR` unset
- base of this branch: `6e3ac141f71e0aa89b0bad5556905e500e2634b8` (`origin/sprint/mechanical-2026-10-02` at the start of the attempt)

A probe at 2026-10-05T18:05:32Z read both addresses before the window. `nova-sprint where --json --cards` exited 0 (80085 bytes, JSON). `curl` to the dashboard exited 0, HTTP 200 (68535 bytes, JSON, `ok` true). The window was started only after that probe could be read.

## Commands

Every sprint verb was one at a time, read-only. No seat, adopt, finish, take, or other write. No Redis address was passed and Redis was not opened. Nothing was started.

```
NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan nova-sprint --version
nova-bus version
NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json --cards
curl -sS -m 30 http://127.0.0.1:7390/api/sprint
```

The where and curl pair ran at second 0 and every 60 seconds through second 3600: 61 samples, indexes 0..60. Every where exited 0. Every curl exited 0 with HTTP 200. No sample failed to read.

After the last sample:

```
NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint log --json --since 2026-10-05T18:12:00Z
NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 120 nova-sprint stats --json
timeout 180 nova-bus log --max 0
```

`nova-sprint log` exited 0, 7142 lines, 4828929 bytes. `nova-sprint stats` exited 0, 12753 bytes. `nova-bus log --max 0` exited 2 and wrote nothing. Its stderr:

```
LOG REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help
```

`TestAcceptanceRecordsAreWellFormed` was not run.

## Judgments

Bar: no open judgment older than 15 minutes at any sample. Age is now minus the judgment's since.

`where --json --cards` judgments are `{id, kind, card}` and carry no `since`. The id is a sprint operation id (`internal/sprint/store/defaults.go` `NewID`: base36 unix nanoseconds, pid, 8 random bytes). That stamp matched `note.at` within 9ms on a log line read before the window (`tick-ask-dlx3lcrmy7pc-...` note.at `2026-10-05T13:58:12.539056-04:00`, id time `2026-10-05T17:58:12.530544Z`). None of the 7 distinct open judgment ids in the window appeared as a `note.id` in the window log, so those notes' `at` fields were not re-read. The id stamp is the since used below.

The same log, independently, raised `a judgment still holds: raised again` at 18:14, 18:24, 18:34, 18:44, 18:54 and 19:04 UTC, each saying that note had been open since `2026-10-05T17:24:00Z` and that judgments wait past their deadline (196 at the first of those lines, 212 at the last). That since is already older than 15 minutes at the first sample.

Samples taken: 61. Samples passing: 0. Samples failing: 61.
Worst: 1051.7 minutes, sample 60 at 2026-10-05T19:12:00Z, card `accept-friends-e2e`, kind `a work card is past its deadline`, since 2026-10-05T01:40:21Z, id `tick-deadlines-dlwisnm7bgig-99863-bd47393196de3adc~15-1.1`.
At sample 0 (2026-10-05T18:12:00Z) 12 judgments were open and all 12 were older than 15 minutes; the oldest was 991.7 minutes. At sample 60 (2026-10-05T19:12:00Z) 4 were open and all 4 were older than 15 minutes; the oldest was 1051.7 minutes. Open count across the hour ranged from 4 to 12.

Passing samples: 0. There is no first or last passing sample.

Failing samples, in full:

```
2026-10-05T18:12:00Z sample 0 open=12 older_than_15m=12 oldest_min=991.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:13:00Z sample 1 open=11 older_than_15m=11 oldest_min=992.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:14:01Z sample 2 open=11 older_than_15m=11 oldest_min=993.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:15:01Z sample 3 open=11 older_than_15m=11 oldest_min=994.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:16:00Z sample 4 open=11 older_than_15m=11 oldest_min=995.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:17:02Z sample 5 open=10 older_than_15m=10 oldest_min=996.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:18:02Z sample 6 open=10 older_than_15m=10 oldest_min=997.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:19:02Z sample 7 open=10 older_than_15m=10 oldest_min=998.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:20:03Z sample 8 open=10 older_than_15m=10 oldest_min=999.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:21:02Z sample 9 open=10 older_than_15m=10 oldest_min=1000.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:22:03Z sample 10 open=10 older_than_15m=10 oldest_min=1001.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:23:01Z sample 11 open=10 older_than_15m=10 oldest_min=1002.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:24:02Z sample 12 open=10 older_than_15m=10 oldest_min=1003.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:25:01Z sample 13 open=10 older_than_15m=10 oldest_min=1004.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:26:02Z sample 14 open=10 older_than_15m=10 oldest_min=1005.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:27:02Z sample 15 open=10 older_than_15m=10 oldest_min=1006.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:28:02Z sample 16 open=10 older_than_15m=10 oldest_min=1007.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:29:01Z sample 17 open=10 older_than_15m=10 oldest_min=1008.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:30:01Z sample 18 open=10 older_than_15m=10 oldest_min=1009.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:31:01Z sample 19 open=10 older_than_15m=10 oldest_min=1010.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:32:01Z sample 20 open=10 older_than_15m=10 oldest_min=1011.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:33:03Z sample 21 open=10 older_than_15m=10 oldest_min=1012.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:34:02Z sample 22 open=10 older_than_15m=10 oldest_min=1013.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:35:02Z sample 23 open=10 older_than_15m=10 oldest_min=1014.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:36:02Z sample 24 open=10 older_than_15m=10 oldest_min=1015.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:37:02Z sample 25 open=9 older_than_15m=9 oldest_min=1016.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:38:02Z sample 26 open=8 older_than_15m=8 oldest_min=1017.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:39:01Z sample 27 open=8 older_than_15m=8 oldest_min=1018.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:40:00Z sample 28 open=5 older_than_15m=5 oldest_min=1019.6 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:41:00Z sample 29 open=5 older_than_15m=5 oldest_min=1020.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:42:00Z sample 30 open=5 older_than_15m=5 oldest_min=1021.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:43:04Z sample 31 open=5 older_than_15m=5 oldest_min=1022.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:44:03Z sample 32 open=5 older_than_15m=5 oldest_min=1023.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:45:01Z sample 33 open=5 older_than_15m=5 oldest_min=1024.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:46:06Z sample 34 open=4 older_than_15m=4 oldest_min=1025.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:47:02Z sample 35 open=4 older_than_15m=4 oldest_min=1026.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:48:00Z sample 36 open=4 older_than_15m=4 oldest_min=1027.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:49:02Z sample 37 open=4 older_than_15m=4 oldest_min=1028.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:50:03Z sample 38 open=4 older_than_15m=4 oldest_min=1029.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:51:04Z sample 39 open=4 older_than_15m=4 oldest_min=1030.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:52:00Z sample 40 open=4 older_than_15m=4 oldest_min=1031.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:53:02Z sample 41 open=4 older_than_15m=4 oldest_min=1032.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:54:00Z sample 42 open=4 older_than_15m=4 oldest_min=1033.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:55:00Z sample 43 open=4 older_than_15m=4 oldest_min=1034.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:56:00Z sample 44 open=4 older_than_15m=4 oldest_min=1035.6 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:57:00Z sample 45 open=4 older_than_15m=4 oldest_min=1036.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:58:00Z sample 46 open=4 older_than_15m=4 oldest_min=1037.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T18:59:00Z sample 47 open=4 older_than_15m=4 oldest_min=1038.6 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:00:00Z sample 48 open=4 older_than_15m=4 oldest_min=1039.6 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:01:00Z sample 49 open=4 older_than_15m=4 oldest_min=1040.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:02:00Z sample 50 open=4 older_than_15m=4 oldest_min=1041.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:03:00Z sample 51 open=4 older_than_15m=4 oldest_min=1042.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:04:01Z sample 52 open=4 older_than_15m=4 oldest_min=1043.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:05:00Z sample 53 open=4 older_than_15m=4 oldest_min=1044.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:06:01Z sample 54 open=4 older_than_15m=4 oldest_min=1045.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:07:00Z sample 55 open=4 older_than_15m=4 oldest_min=1046.6 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:08:00Z sample 56 open=4 older_than_15m=4 oldest_min=1047.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:09:00Z sample 57 open=4 older_than_15m=4 oldest_min=1048.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:10:00Z sample 58 open=4 older_than_15m=4 oldest_min=1049.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:11:00Z sample 59 open=4 older_than_15m=4 oldest_min=1050.6 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
2026-10-05T19:12:00Z sample 60 open=4 older_than_15m=4 oldest_min=1051.7 card=accept-friends-e2e kind=a work card is past its deadline since=2026-10-05T01:40:21Z
```

## Idle alarm

Bar: every raise true when raised, every clear following the condition ending, and at least one raise and one clear in the window. The notes are `the fleet is idle` and `the fleet is working again` (`internal/sprint/idle.go`). If none occurs, the card says to report that and not to provoke one.

The window log's 7142 lines contain neither note. A search of `kind`, `verb`, `note.type` and `note.what` for `idle` found nothing. Raises: 0. Clears: 0.

`where.width` (width of fleet members that are up) was 0 on all 61 samples (`buffer` `59/0` at sample 0, `52/0` at sample 60; ready primaries stayed between 52 and 59). The idle-alarm predicate requires width above 0, so it was false on that reading for the whole window, which matches no note.

The dashboard did not show a raise or a clear either. All 61 responses have `ok` true, and `data.at` is `2026-10-05T13:23:40.964379-04:00` on every one of them while `fetchedAt` moves with the sample. The payload is one frozen snapshot from 17:23:40Z. It has no alarm list. It is not a live reading of the hour.

Friend-stall notes did occur and are not the idle alarm: 9 lines of type `friend stall` (rowan-space stalled from 25m to 35m and unstarted cards taken back; emma and freddy released to up at 18:29:53Z; rowan stalled from 20m to 35m and unstarted cards taken back). They were not counted as idle-alarm raises.

This criterion did not pass. No alarm was provoked.

## Ready queues

Bar: each friend whose status is not `held` has ready at least twice its width at every sample, except within 2 minutes after a deal onto that friend. Counts are the live friends table from `where --json` (`friends[].ready`, `friends[].width`, `friends[].status`). `tables.friends` on the same object carries the same ready, width and working numbers, with the hold reason appended to status. The dashboard table does not: it is the frozen 17:23:40Z snapshot above, and it disagrees (johnny ready 8 there, ready 0 on the live table at sample 0).

Samples taken: 61. Samples passing: 0. Samples failing: 61.
Not-held friend-rows: 534. Rows with ready >= 2x width: 0. Rows below 2x: 534. Of those below, 8 fall inside 2 minutes after a deal and 526 do not.
Up friends only, the same bar: 283 up friend-rows, 283 of them below 2x, samples on which every up friend was at 2x or inside the deal exception: 0.
The other reading, ready+working >= 2x width (a member holds up to twice its width, working and ready together): rows meeting it among not-held rows: 0 of 534.

Held the whole hour, excluded: alex (width 8), zhi (width 8). rowan was held on 15 samples and not held on 46; only the not-held samples are in the bar.

Worst gap (2x minus ready) per friend, live table:

| friend | not-held samples | below 2x | excused by a deal | max ready | worst gap | when | ready | width | working | status |
|---|---:|---:|---:|---:|---:|---|---:|---:|---:|---|
| emma | 61 | 61 | 2 | 0 | 16 | 2026-10-05T18:12:00Z sample 0 | 0 | 8 | 0 | down |
| freddy | 61 | 61 | 0 | 0 | 24 | 2026-10-05T18:12:00Z sample 0 | 0 | 12 | 0 | down |
| johnny | 61 | 61 | 2 | 2 | 16 | 2026-10-05T18:12:00Z sample 0 | 0 | 8 | 5 | up |
| rowan | 46 | 46 | 2 | 0 | 12 | 2026-10-05T18:12:00Z sample 0 | 0 | 6 | 0 | down |
| rowan-mas | 61 | 61 | 0 | 0 | 16 | 2026-10-05T18:12:00Z sample 0 | 0 | 8 | 0 | down |
| rowan-next | 61 | 61 | 0 | 0 | 16 | 2026-10-05T18:12:00Z sample 0 | 0 | 8 | 0 | down |
| rowan-personal | 61 | 61 | 0 | 0 | 16 | 2026-10-05T18:12:00Z sample 0 | 0 | 8 | 0 | down |
| rowan-space | 61 | 61 | 0 | 0 | 32 | 2026-10-05T18:47:02Z sample 35 | 0 | 16 | 0 | up |
| stella | 61 | 61 | 2 | 0 | 4 | 2026-10-05T18:12:00Z sample 0 | 0 | 2 | 1 | up |

Deals in the window log (`verb` `tick deal` onto a friend row):

- 2026-10-05T18:22:20Z johnny (one card to `friend.johnny:ready`, others straight to `friend.johnny:working`)
- 2026-10-05T18:29:53Z rowan (`friend.rowan:working`)
- 2026-10-05T18:29:54Z emma (`friend.emma:working`)
- 2026-10-05T19:05:04Z stella (`friend.stella:working`)

No friend not held reached ready >= 2x width on any sample. johnny's best ready was 2 against width 8 (2x 16), on 14 samples, with working 8. rowan-space's width was 8 on 35 samples and 16 on 26; ready stayed 0.

Passing samples: 0. There is no first or last passing sample.

Samples below 2x, not inside the deal exception, in full:

```
2026-10-05T18:12:00Z sample 0 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:12:00Z sample 0 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:12:00Z sample 0 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:12:00Z sample 0 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:12:00Z sample 0 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:12:00Z sample 0 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:12:00Z sample 0 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:12:00Z sample 0 rowan-space status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:12:00Z sample 0 stella status=up ready=0 width=2 working=1 2x=4
2026-10-05T18:13:00Z sample 1 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:13:00Z sample 1 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:13:00Z sample 1 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:13:00Z sample 1 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:13:00Z sample 1 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:13:00Z sample 1 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:13:00Z sample 1 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:13:00Z sample 1 rowan-space status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:13:00Z sample 1 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:14:01Z sample 2 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:14:01Z sample 2 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:14:01Z sample 2 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:14:01Z sample 2 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:14:01Z sample 2 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:14:01Z sample 2 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:14:01Z sample 2 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:14:01Z sample 2 rowan-space status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:14:01Z sample 2 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:15:01Z sample 3 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:15:01Z sample 3 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:15:01Z sample 3 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:15:01Z sample 3 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:15:01Z sample 3 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:15:01Z sample 3 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:15:01Z sample 3 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:15:01Z sample 3 rowan-space status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:15:01Z sample 3 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:16:00Z sample 4 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:16:00Z sample 4 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:16:00Z sample 4 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:16:00Z sample 4 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:16:00Z sample 4 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:16:00Z sample 4 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:16:00Z sample 4 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:16:00Z sample 4 rowan-space status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:16:00Z sample 4 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:17:02Z sample 5 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:17:02Z sample 5 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:17:02Z sample 5 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:17:02Z sample 5 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:17:02Z sample 5 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:17:02Z sample 5 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:17:02Z sample 5 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:17:02Z sample 5 rowan-space status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:17:02Z sample 5 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:18:02Z sample 6 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:18:02Z sample 6 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:18:02Z sample 6 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:18:02Z sample 6 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:18:02Z sample 6 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:18:02Z sample 6 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:18:02Z sample 6 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:18:02Z sample 6 rowan-space status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:18:02Z sample 6 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:19:02Z sample 7 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:19:02Z sample 7 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:19:02Z sample 7 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:19:02Z sample 7 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:19:02Z sample 7 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:19:02Z sample 7 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:19:02Z sample 7 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:19:02Z sample 7 rowan-space status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:19:02Z sample 7 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:20:03Z sample 8 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:20:03Z sample 8 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:20:03Z sample 8 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:20:03Z sample 8 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:20:03Z sample 8 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:20:03Z sample 8 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:20:03Z sample 8 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:20:03Z sample 8 rowan-space status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:20:03Z sample 8 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:21:02Z sample 9 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:21:02Z sample 9 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:21:02Z sample 9 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:21:02Z sample 9 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:21:02Z sample 9 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:21:02Z sample 9 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:21:02Z sample 9 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:21:02Z sample 9 rowan-space status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:21:02Z sample 9 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:22:03Z sample 10 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:22:03Z sample 10 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:22:03Z sample 10 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:22:03Z sample 10 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:22:03Z sample 10 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:22:03Z sample 10 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:22:03Z sample 10 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:22:03Z sample 10 rowan-space status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:22:03Z sample 10 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:23:01Z sample 11 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:23:01Z sample 11 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:23:01Z sample 11 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:23:01Z sample 11 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:23:01Z sample 11 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:23:01Z sample 11 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:23:01Z sample 11 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:23:01Z sample 11 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:24:02Z sample 12 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:24:02Z sample 12 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:24:02Z sample 12 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:24:02Z sample 12 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:24:02Z sample 12 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:24:02Z sample 12 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:24:02Z sample 12 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:24:02Z sample 12 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:25:01Z sample 13 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:25:01Z sample 13 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:25:01Z sample 13 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:25:01Z sample 13 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:25:01Z sample 13 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:25:01Z sample 13 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:25:01Z sample 13 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:25:01Z sample 13 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:25:01Z sample 13 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:26:02Z sample 14 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:26:02Z sample 14 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:26:02Z sample 14 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:26:02Z sample 14 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:26:02Z sample 14 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:26:02Z sample 14 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:26:02Z sample 14 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:26:02Z sample 14 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:26:02Z sample 14 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:27:02Z sample 15 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:27:02Z sample 15 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:27:02Z sample 15 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:27:02Z sample 15 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:27:02Z sample 15 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:27:02Z sample 15 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:27:02Z sample 15 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:27:02Z sample 15 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:27:02Z sample 15 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:28:02Z sample 16 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:28:02Z sample 16 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:28:02Z sample 16 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:28:02Z sample 16 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:28:02Z sample 16 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:28:02Z sample 16 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:28:02Z sample 16 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:28:02Z sample 16 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:28:02Z sample 16 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:29:01Z sample 17 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:29:01Z sample 17 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:29:01Z sample 17 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:29:01Z sample 17 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:29:01Z sample 17 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:29:01Z sample 17 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:29:01Z sample 17 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:29:01Z sample 17 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:29:01Z sample 17 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:30:01Z sample 18 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:30:01Z sample 18 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:30:01Z sample 18 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:30:01Z sample 18 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:30:01Z sample 18 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:30:01Z sample 18 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:30:01Z sample 18 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:31:01Z sample 19 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:31:01Z sample 19 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:31:01Z sample 19 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:31:01Z sample 19 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:31:01Z sample 19 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:31:01Z sample 19 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:31:01Z sample 19 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:32:01Z sample 20 emma status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:32:01Z sample 20 freddy status=down ready=0 width=12 working=0 2x=24
2026-10-05T18:32:01Z sample 20 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:32:01Z sample 20 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:32:01Z sample 20 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:32:01Z sample 20 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:32:01Z sample 20 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:32:01Z sample 20 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:32:01Z sample 20 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:33:03Z sample 21 emma status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:33:03Z sample 21 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:33:03Z sample 21 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:33:03Z sample 21 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:33:03Z sample 21 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:33:03Z sample 21 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:33:03Z sample 21 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:33:03Z sample 21 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:33:03Z sample 21 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:34:02Z sample 22 emma status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:34:02Z sample 22 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:34:02Z sample 22 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:34:02Z sample 22 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:34:02Z sample 22 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:34:02Z sample 22 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:34:02Z sample 22 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:34:02Z sample 22 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:34:02Z sample 22 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:35:02Z sample 23 emma status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:35:02Z sample 23 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:35:02Z sample 23 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:35:02Z sample 23 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:35:02Z sample 23 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:35:02Z sample 23 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:35:02Z sample 23 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:35:02Z sample 23 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:35:02Z sample 23 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:36:02Z sample 24 emma status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:36:02Z sample 24 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:36:02Z sample 24 johnny status=up ready=2 width=8 working=8 2x=16
2026-10-05T18:36:02Z sample 24 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:36:02Z sample 24 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:36:02Z sample 24 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:36:02Z sample 24 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:36:02Z sample 24 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:36:02Z sample 24 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:37:02Z sample 25 emma status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:37:02Z sample 25 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:37:02Z sample 25 johnny status=up ready=1 width=8 working=8 2x=16
2026-10-05T18:37:02Z sample 25 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:37:02Z sample 25 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:37:02Z sample 25 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:37:02Z sample 25 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:37:02Z sample 25 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:37:02Z sample 25 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:38:02Z sample 26 emma status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:38:02Z sample 26 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:38:02Z sample 26 johnny status=up ready=0 width=8 working=8 2x=16
2026-10-05T18:38:02Z sample 26 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:38:02Z sample 26 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:38:02Z sample 26 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:38:02Z sample 26 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:38:02Z sample 26 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:38:02Z sample 26 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:39:01Z sample 27 emma status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:39:01Z sample 27 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:39:01Z sample 27 johnny status=up ready=0 width=8 working=8 2x=16
2026-10-05T18:39:01Z sample 27 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:39:01Z sample 27 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:39:01Z sample 27 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:39:01Z sample 27 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:39:01Z sample 27 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:39:01Z sample 27 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:40:00Z sample 28 emma status=up ready=0 width=8 working=4 2x=16
2026-10-05T18:40:00Z sample 28 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:40:00Z sample 28 johnny status=up ready=0 width=8 working=7 2x=16
2026-10-05T18:40:00Z sample 28 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:40:00Z sample 28 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:40:00Z sample 28 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:40:00Z sample 28 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:40:00Z sample 28 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:40:00Z sample 28 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:41:00Z sample 29 emma status=up ready=0 width=8 working=3 2x=16
2026-10-05T18:41:00Z sample 29 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:41:00Z sample 29 johnny status=up ready=0 width=8 working=7 2x=16
2026-10-05T18:41:00Z sample 29 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:41:00Z sample 29 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:41:00Z sample 29 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:41:00Z sample 29 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:41:00Z sample 29 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:41:00Z sample 29 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:42:00Z sample 30 emma status=up ready=0 width=8 working=3 2x=16
2026-10-05T18:42:00Z sample 30 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:42:00Z sample 30 johnny status=up ready=0 width=8 working=7 2x=16
2026-10-05T18:42:00Z sample 30 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:42:00Z sample 30 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:42:00Z sample 30 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:42:00Z sample 30 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:42:00Z sample 30 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:42:00Z sample 30 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:43:04Z sample 31 emma status=up ready=0 width=8 working=2 2x=16
2026-10-05T18:43:04Z sample 31 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:43:04Z sample 31 johnny status=up ready=0 width=8 working=7 2x=16
2026-10-05T18:43:04Z sample 31 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:43:04Z sample 31 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:43:04Z sample 31 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:43:04Z sample 31 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:43:04Z sample 31 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:43:04Z sample 31 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:44:03Z sample 32 emma status=up ready=0 width=8 working=2 2x=16
2026-10-05T18:44:03Z sample 32 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:44:03Z sample 32 johnny status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:44:03Z sample 32 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:44:03Z sample 32 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:44:03Z sample 32 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:44:03Z sample 32 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:44:03Z sample 32 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:44:03Z sample 32 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:45:01Z sample 33 emma status=up ready=0 width=8 working=2 2x=16
2026-10-05T18:45:01Z sample 33 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:45:01Z sample 33 johnny status=up ready=0 width=8 working=6 2x=16
2026-10-05T18:45:01Z sample 33 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:45:01Z sample 33 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:45:01Z sample 33 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:45:01Z sample 33 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:45:01Z sample 33 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:45:01Z sample 33 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:46:06Z sample 34 emma status=up ready=0 width=8 working=2 2x=16
2026-10-05T18:46:06Z sample 34 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:46:06Z sample 34 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:46:06Z sample 34 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:46:06Z sample 34 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:46:06Z sample 34 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:46:06Z sample 34 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:46:06Z sample 34 rowan-space status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:46:06Z sample 34 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:47:02Z sample 35 emma status=up ready=0 width=8 working=2 2x=16
2026-10-05T18:47:02Z sample 35 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:47:02Z sample 35 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:47:02Z sample 35 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:47:02Z sample 35 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:47:02Z sample 35 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:47:02Z sample 35 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:47:02Z sample 35 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:47:02Z sample 35 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:48:00Z sample 36 emma status=up ready=0 width=8 working=1 2x=16
2026-10-05T18:48:00Z sample 36 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:48:00Z sample 36 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:48:00Z sample 36 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:48:00Z sample 36 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:48:00Z sample 36 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:48:00Z sample 36 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:48:00Z sample 36 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:48:00Z sample 36 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:49:02Z sample 37 emma status=up ready=0 width=8 working=1 2x=16
2026-10-05T18:49:02Z sample 37 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:49:02Z sample 37 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:49:02Z sample 37 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:49:02Z sample 37 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:49:02Z sample 37 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:49:02Z sample 37 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:49:02Z sample 37 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:49:02Z sample 37 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:50:03Z sample 38 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:50:03Z sample 38 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:50:03Z sample 38 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:50:03Z sample 38 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:50:03Z sample 38 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:50:03Z sample 38 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:50:03Z sample 38 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:50:03Z sample 38 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:50:03Z sample 38 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:51:04Z sample 39 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:51:04Z sample 39 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:51:04Z sample 39 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:51:04Z sample 39 rowan status=up ready=0 width=6 working=1 2x=12
2026-10-05T18:51:04Z sample 39 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:51:04Z sample 39 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:51:04Z sample 39 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:51:04Z sample 39 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:51:04Z sample 39 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:52:00Z sample 40 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:52:00Z sample 40 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:52:00Z sample 40 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:52:00Z sample 40 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:52:00Z sample 40 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:52:00Z sample 40 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:52:00Z sample 40 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:52:00Z sample 40 rowan-space status=down ready=0 width=16 working=0 2x=32
2026-10-05T18:52:00Z sample 40 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:53:02Z sample 41 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:53:02Z sample 41 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:53:02Z sample 41 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:53:02Z sample 41 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:53:02Z sample 41 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:53:02Z sample 41 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:53:02Z sample 41 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:53:02Z sample 41 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:53:02Z sample 41 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:54:00Z sample 42 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:54:00Z sample 42 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:54:00Z sample 42 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:54:00Z sample 42 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:54:00Z sample 42 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:54:00Z sample 42 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:54:00Z sample 42 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:54:00Z sample 42 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:54:00Z sample 42 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:55:00Z sample 43 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:55:00Z sample 43 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:55:00Z sample 43 johnny status=up ready=0 width=8 working=5 2x=16
2026-10-05T18:55:00Z sample 43 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:55:00Z sample 43 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:55:00Z sample 43 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:55:00Z sample 43 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:55:00Z sample 43 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:55:00Z sample 43 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:56:00Z sample 44 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:56:00Z sample 44 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:56:00Z sample 44 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T18:56:00Z sample 44 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:56:00Z sample 44 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:56:00Z sample 44 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:56:00Z sample 44 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:56:00Z sample 44 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:56:00Z sample 44 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:57:00Z sample 45 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:57:00Z sample 45 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:57:00Z sample 45 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T18:57:00Z sample 45 rowan status=down ready=0 width=6 working=0 2x=12
2026-10-05T18:57:00Z sample 45 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:57:00Z sample 45 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:57:00Z sample 45 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:57:00Z sample 45 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:57:00Z sample 45 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:58:00Z sample 46 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:58:00Z sample 46 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:58:00Z sample 46 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T18:58:00Z sample 46 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:58:00Z sample 46 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:58:00Z sample 46 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:58:00Z sample 46 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:58:00Z sample 46 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T18:59:00Z sample 47 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T18:59:00Z sample 47 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T18:59:00Z sample 47 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T18:59:00Z sample 47 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:59:00Z sample 47 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:59:00Z sample 47 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T18:59:00Z sample 47 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T18:59:00Z sample 47 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T19:00:00Z sample 48 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:00:00Z sample 48 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:00:00Z sample 48 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:00:00Z sample 48 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:00:00Z sample 48 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:00:00Z sample 48 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:00:00Z sample 48 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:00:00Z sample 48 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T19:01:00Z sample 49 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:01:00Z sample 49 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:01:00Z sample 49 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:01:00Z sample 49 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:01:00Z sample 49 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:01:00Z sample 49 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:01:00Z sample 49 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:01:00Z sample 49 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T19:02:00Z sample 50 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:02:00Z sample 50 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:02:00Z sample 50 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:02:00Z sample 50 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:02:00Z sample 50 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:02:00Z sample 50 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:02:00Z sample 50 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:02:00Z sample 50 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T19:03:00Z sample 51 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:03:00Z sample 51 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:03:00Z sample 51 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:03:00Z sample 51 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:03:00Z sample 51 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:03:00Z sample 51 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:03:00Z sample 51 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:03:00Z sample 51 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T19:04:01Z sample 52 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:04:01Z sample 52 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:04:01Z sample 52 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:04:01Z sample 52 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:04:01Z sample 52 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:04:01Z sample 52 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:04:01Z sample 52 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:04:01Z sample 52 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T19:05:00Z sample 53 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:05:00Z sample 53 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:05:00Z sample 53 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:05:00Z sample 53 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:05:00Z sample 53 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:05:00Z sample 53 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:05:00Z sample 53 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:05:00Z sample 53 stella status=up ready=0 width=2 working=0 2x=4
2026-10-05T19:06:01Z sample 54 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:06:01Z sample 54 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:06:01Z sample 54 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:06:01Z sample 54 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:06:01Z sample 54 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:06:01Z sample 54 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:06:01Z sample 54 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:07:00Z sample 55 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:07:00Z sample 55 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:07:00Z sample 55 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:07:00Z sample 55 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:07:00Z sample 55 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:07:00Z sample 55 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:07:00Z sample 55 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:08:00Z sample 56 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:08:00Z sample 56 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:08:00Z sample 56 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:08:00Z sample 56 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:08:00Z sample 56 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:08:00Z sample 56 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:08:00Z sample 56 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:08:00Z sample 56 stella status=up ready=0 width=2 working=1 2x=4
2026-10-05T19:09:00Z sample 57 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:09:00Z sample 57 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:09:00Z sample 57 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:09:00Z sample 57 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:09:00Z sample 57 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:09:00Z sample 57 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:09:00Z sample 57 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:09:00Z sample 57 stella status=up ready=0 width=2 working=1 2x=4
2026-10-05T19:10:00Z sample 58 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:10:00Z sample 58 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:10:00Z sample 58 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:10:00Z sample 58 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:10:00Z sample 58 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:10:00Z sample 58 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:10:00Z sample 58 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:10:00Z sample 58 stella status=up ready=0 width=2 working=1 2x=4
2026-10-05T19:11:00Z sample 59 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:11:00Z sample 59 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:11:00Z sample 59 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:11:00Z sample 59 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:11:00Z sample 59 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:11:00Z sample 59 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:11:00Z sample 59 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:11:00Z sample 59 stella status=up ready=0 width=2 working=1 2x=4
2026-10-05T19:12:00Z sample 60 emma status=up ready=0 width=8 working=0 2x=16
2026-10-05T19:12:00Z sample 60 freddy status=up ready=0 width=12 working=0 2x=24
2026-10-05T19:12:00Z sample 60 johnny status=up ready=0 width=8 working=4 2x=16
2026-10-05T19:12:00Z sample 60 rowan-mas status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:12:00Z sample 60 rowan-next status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:12:00Z sample 60 rowan-personal status=down ready=0 width=8 working=0 2x=16
2026-10-05T19:12:00Z sample 60 rowan-space status=up ready=0 width=16 working=0 2x=32
2026-10-05T19:12:00Z sample 60 stella status=up ready=0 width=2 working=1 2x=4
```

Samples below 2x inside 2 minutes after a deal (listed, not counted as a miss for that friend):

```
2026-10-05T18:23:01Z sample 11 johnny status=up ready=2 width=8 working=8 2x=16 excused=within_2m_of_deal
2026-10-05T18:24:02Z sample 12 johnny status=up ready=2 width=8 working=8 2x=16 excused=within_2m_of_deal
2026-10-05T18:30:01Z sample 18 emma status=up ready=0 width=8 working=6 2x=16 excused=within_2m_of_deal
2026-10-05T18:30:01Z sample 18 rowan status=up ready=0 width=6 working=1 2x=12 excused=within_2m_of_deal
2026-10-05T18:31:01Z sample 19 emma status=down ready=0 width=8 working=0 2x=16 excused=within_2m_of_deal
2026-10-05T18:31:01Z sample 19 rowan status=down ready=0 width=6 working=0 2x=12 excused=within_2m_of_deal
2026-10-05T19:06:01Z sample 54 stella status=up ready=0 width=2 working=2 2x=4 excused=within_2m_of_deal
2026-10-05T19:07:00Z sample 55 stella status=up ready=0 width=2 working=2 2x=4 excused=within_2m_of_deal
```

## What was not measured

- The bus log. `nova-bus log --max 0` exited 2 with the refusal above. No bus line was read. An idle-alarm note that existed only on the bus would have been missed. The sprint log, which is where those notes are written, was read and had none.
- `note.at` for the open judgments themselves. The where object has no since, and those ids were not in the window log. Age is the id's NewID stamp, corroborated by the overdue notes that say judgments have been open since 17:24:00Z.
- A live dashboard alarm row. `/api/sprint` served one frozen snapshot and has no alarm field.
- `nova-sprint stats` was read (exit 0) and was not used to decide the three bars. The bars come from `where` and the sprint log.
- No idle alarm was provoked. No write verb was run. The acceptance-record test was not run.

