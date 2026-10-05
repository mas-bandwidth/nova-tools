# accept-dashboard

Verdict: FAIL

PASS line, verbatim: every number on the dashboard traced to the store.

Report disposition: HOLD. The 30-minute window was not started. The named reads could not be run, so this is not a measured miss of a traced number. The file uses FAIL because its verdict vocabulary is PASS or FAIL, and the check did not pass.

Window start (UTC): not started
Window end (UTC): not started
Probe time (UTC): 2026-10-05 00:15:46

Build measured: unread. `nova-sprint --version` and `nova-sprint help` both exited 126 before printing a version. The installed binary is `/home/ubuntu/.local/bin/nova-sprint` (mode 0755, size 19206304, mtime 2026-10-04 17:52 UTC). This process cannot read or execute it (Permission denied). Checkout measured against: `b4d64dd710e49ee57fc8fc0878b45a41e7d6dc7b` on `sprint/accept-dashboard.w4.g2.e15`. Harness: opencode. Model: x-ai/grok-4.7. By: Rowan.

`timeout` could not wrap the verbs: `/usr/bin/timeout` failed with `failed to execute process: Permission denied (os error 13)`. Each verb was therefore run directly. Each failed in well under 300 seconds.

## Commands run

All of these are read-only. Each sprint verb was invoked as `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan nova-sprint <verb>` (no `timeout` prefix; see above).

1. `date -u` at 2026-10-05 00:13:39 UTC, again at 2026-10-05 00:15:39 UTC, and at the probe stamp 2026-10-05 00:15:46 UTC.
2. `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan nova-sprint --version` — exit 126.
3. `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan nova-sprint help` — exit 126.
4. `nova-bus log --max 0` — exit 126. `nova-bus2` is not installed (`No such file or directory`).
5. `curl -sS -m 3 -D - http://127.0.0.1:6390/` — exit 7, connection refused. Nothing is listening on 127.0.0.1:6390.
6. `curl -sS -m 5 -D - http://127.0.0.1:7390/api/sprint` — exit 0, HTTP 200, `Content-Length: 0`, `Server: Caddy`, body 0 bytes. Not a sprint snapshot.
7. `curl -sS -m 3 -D - http://127.0.0.1:7395/api/sprint` — exit 7, connection refused. The dashboard pull default is not listening.

A copy of the installed `nova-sprint` into the job directory failed the same way: `cannot open '/home/ubuntu/.local/bin/nova-sprint' for reading: Permission denied`. The wall is landlock (abi used 6): the binary is outside the readable, executable set. No server was started.

## Criterion

Bar: every number the dashboard serves (each field of `/api/sprint` that is a number, a count, or a percentage, by JSON path) equals a store field, or a computation from store fields, at three snapshots 10 minutes apart inside a 30-minute window. A difference must be accounted for by log lines between the two reads. A sample that fails to read is a counted failure.

Measured value: no number was served, and the store was not read. There is no path-by-path table, because `/api/sprint` returned an empty body and `nova-sprint where`, `stats`, and `log` did not run.

Raw counts: samples taken 0 of 3; samples passing 0; worst value: empty dashboard body and store unread, at 2026-10-05 00:15:46 UTC.

## Raw sample lines

No window sample was taken. The probes that decide the criterion, in full:

```
Mon Oct  5 12:15:46 AM UTC 2026
--- nova-sprint --version ---
/usr/bin/bash: line 3: /home/ubuntu/.local/bin/nova-sprint: Permission denied
exit:126
--- nova-sprint help ---
/usr/bin/bash: line 6: /home/ubuntu/.local/bin/nova-sprint: Permission denied
exit:126
--- nova-bus log ---
/usr/bin/bash: line 9: /home/ubuntu/.local/bin/nova-bus: Permission denied
exit:126
--- curl 6390 ---
curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server
exit:7
--- curl 7390 /api/sprint ---
HTTP/1.1 200 OK
Server: Caddy
Date: Mon, 05 Oct 2026 00:15:46 GMT
Content-Length: 0

exit:0
--- dash body bytes ---
0 scratch/dash-body.txt
--- curl 7395 ---
curl: (7) Failed to connect to 127.0.0.1 port 7395 after 0 ms: Could not connect to server
exit:7
```

Passing samples: 0. First and last passing sample: none.

## What was not measured

The 30-minute window, three snapshots 10 minutes apart, and the within-5-seconds pairing of `/api/sprint` with `nova-sprint where --json --cards`, `nova-sprint stats --json`, and `nova-sprint log --json --since <window start>`. The bus log. Every numeric JSON path, including `throughput`, `throughputMinutes`, and the counts inside `data` (`landed`, `all`, per-state counts, `ready`, `width`, costs, and the page's derived percentage). No number was traced to the store. The window was not stretched or restarted.
