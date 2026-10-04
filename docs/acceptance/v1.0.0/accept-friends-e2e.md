# accept-friends-e2e

Verdict: HOLD

PASS line (the bar, not met; the window was not run): the E2E probe (bus message -> turn in the open chat -> reply) passes for every friend not held, 10 of 10, for 2 hours; presence matches each friend's own count.

Window start (UTC): 2026-10-04T23:25:19Z (`date -u` of the first sample).
Window end (UTC): 2026-10-04T23:30:36Z (`date -u` when the probe path was confirmed blocked). The 2 hour window was not started and was not stretched. A probe cannot be sent, so a failed 2 hour window was not manufactured.

Build measured: not read from the installed binaries (open and exec return EACCES). Dashboard snapshot `build` field, same on the tailnet pull and the coordinator pull: `19c41193`. Checkout measured from: `bf078ca7d54a07958e51bdb39b9e693fc1010a66` (`sprint/mechanical-2026-10-02`). Reporter: Rowan, harness opencode, model grok-4.7.

## Commands run

Each sprint verb was attempted as `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`. The binary never started, so the env and timeout did not matter. A read that failed is a counted failure, not a skip.

1. `date -u` at first sample: `Sun Oct  4 11:25:19 PM UTC 2026`.
2. `command -v nova-sprint` -> `/home/ubuntu/.local/bin/nova-sprint`. `command -v nova-bus` -> `/home/ubuntu/.local/bin/nova-bus`. `nova-bus2` not on PATH.
3. `stat` of those two files: mode 0755, sizes 19206304 and 4497568, owner ubuntu. `findmnt -T /home/ubuntu/.local/bin`: `/` ext4 `rw,relatime` (not noexec).
4. `nova-sprint --version` and `nova-sprint help`: exit 126, `Permission denied`. `open` and `execv` of the sprint binary: errno 13 EACCES. Repeated at 2026-10-04T23:30:01Z, same rc 126.
5. `nova-bus help`: exit 126, `Permission denied`. `nova-bus2 help`: exit 127, command not found.
6. `curl -sS -m 3 http://127.0.0.1:6390/`: exit 7, connection refused. `ss` shows no listener on 6390.
7. `curl -sS -m 3 -D - http://127.0.0.1:7390/api/sprint`: HTTP 200, `Server: Caddy`, `Content-Length: 0`, body 0 bytes. Same for `/`, `/api/sprint/`, `/friend/rowan`, `/health`, `/api`. `/healthz` on that Host is also an empty 200.
8. Caddy admin `http://127.0.0.1:2019/config/` shows `:7390` serves host `100.115.99.19` from a static site and rewrites `/api/sprint` to `/api/sprint.json`. `curl -H 'Host: 100.115.99.19' http://127.0.0.1:7390/api/sprint` and `curl http://100.115.99.19:7390/api/sprint`: HTTP 200, `Content-Type: application/json`, 81289 bytes, `fetchedAt` 2026-10-04T23:26:58.430163+00:00, `build` 19c41193, `ok` true.
9. `curl http://100.76.29.55:7390/api/sprint`: HTTP 200, 81289 bytes, `build` 19c41193, `fetchedAt` 2026-10-04T23:30:01.250324+00:00, `Server: BaseHTTP/0.6 Python/3.14.7`. This is the pull the local dashboard loop fetches. It is not `nova-sprint where`.
10. `curl http://100.76.29.55:6390/`: HTTP 404, body `the sprint server takes POST /verbs`. `POST /verbs` with `{"verbs":[["where","--actor","rowan","--json"]]}`: HTTP 200, result code 2, stderr `nova-sprint server: where: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed`. Same refusal for `help friend` and `version`. That address is the worker server, not the coordinator loopback the card names.
11. End dashboard sample, 2026-10-04T23:30:36Z, `curl -H 'Host: 100.115.99.19' http://127.0.0.1:7390/api/sprint`: HTTP 200, 81287 bytes, `build` 19c41193, `fetchedAt` 2026-10-04T23:30:35.206193+00:00.

No bus message was sent. No probe verb was named, because neither `nova-bus help` nor `nova-sprint help friend` could be run.

## Friends not held

Read from the dashboard snapshot (tables.friends), not from `nova-sprint where --json`, which could not be run. Status not `held` at the first dashboard sample (2026-10-04T23:26:58Z) and again at the end sample (2026-10-04T23:30:35Z); the two lists match.

Not held: emma (up, working 0), freddy (up, working 0), johnny (up, working 2), rowan-next (down, working 0), rowan-personal (down, working 0), rowan-space (down, working 0), stella (up, working 0), zhi (up, working 5).

Held, not probed: alex (held, working 2), rowan (held, working 0), rowan-mas (held, working 0).

## Criteria

E2E probe, bar 10 of 10 replies within 10 minutes for every friend not held, 10 probes each, one every 12 minutes across 2 hours.

Measured: samples taken 0, samples passing 0, for each of emma, freddy, johnny, rowan-next, rowan-personal, rowan-space, stella, zhi. Worst value: 0 of 10, at the window start 2026-10-04T23:25:19Z, because send could not be executed. No send time, reply time, or round trip exists.

Presence, bar the friends table and the dashboard status and working count match the count the friend states in its reply, at every probe.

Measured: probes 0, matches 0. Dashboard status and working count were read twice (above) and did not change. The friend's own count was not asked and not received. Worst: no comparison, at 2026-10-04T23:25:19Z.

## Raw lines that decide it

Failing reads, in full:

```
Sun Oct  4 11:25:19 PM UTC 2026
/home/ubuntu/.local/bin/nova-sprint: writable, executable, regular file, no read permission
/home/ubuntu/.local/bin/nova-bus:    writable, executable, regular file, no read permission
timeout: failed to execute process: Permission denied (os error 13)
sprint_rc:126
LISTEN 0      4096                             *:7390             *:*
curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server
HTTP/1.1 200 OK
Server: Caddy
Date: Sun, 04 Oct 2026 23:25:19 GMT
Content-Length: 0
```

```
Sun Oct  4 11:30:01 PM UTC 2026
/usr/bin/bash: line 22: /home/ubuntu/.local/bin/nova-sprint: Permission denied
sprint_ver_rc:126
sprint_help_rc:126
/usr/bin/bash: line 24: /home/ubuntu/.local/bin/nova-bus: Permission denied
bus_help_rc:126
/usr/bin/bash: line 25: nova-bus2: command not found
bus2_rc:127
curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server
6390_curl_rc:000
HTTP/1.1 200 OK
Server: Caddy
Date: Sun, 04 Oct 2026 23:30:01 GMT
Content-Length: 0
7390_code:200 bytes:0
```

```
open_read 13 Permission denied
execv 13 Permission denied
{"results":[{"code":2,"stdout":"","stderr":"nova-sprint server: where: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed\n"}]}
```

Passing probe lines: none. Count 0. No first or last passing sample.

Dashboard friend rows that were readable (not a substitute for `where --json`, and not a probe). First sample 2026-10-04T23:26:58Z and last sample 2026-10-04T23:30:35Z, same status and working count:

```
alex	held	working=2
emma	up	working=0
freddy	up	working=0
johnny	up	working=2
rowan	held	working=0
rowan-mas	held	working=0
rowan-next	down	working=0
rowan-personal	down	working=0
rowan-space	down	working=0
stella	up	working=0
zhi	up	working=5
```

## What was not measured

The 2 hour probe loop was not run. No bus message was sent, no open-chat turn was observed, no reply was waited for, no round trip was timed. `nova-sprint where --json`, `nova-sprint where --json --cards`, `nova-sprint stats --json`, and `nova-sprint log --json` were not run. `nova-bus log` and `nova-bus2 log` were not run. Presence was not compared to a friend's own working count. `TestAcceptanceRecordsAreWellFormed` was not run; this card does not run it. No server was started. The only live sprint view obtained was the dashboard JSON the pull already publishes, which does not carry a probe reply.
