Verdict: HOLD

The check was not run. This is not a measured failure of a window, and it is not a pass. The PASS line is unmet because no sample exists. This is attempt 4 of the card; attempt 3 recorded the same hold at Sun Oct 4 11:36:50 PM UTC 2026 (branch sprint/accept-server-latency.w3.g3.e15, commit 56c8c20a81b6f2f29ed119e974a53b74d43d5f5b), and both probe rounds of this attempt, one minute apart, saw the same state, so the blocker persisted across attempts. This attempt's wall fixes its landlock rights when it starts, so the exec denial below cannot lift during the session, and waiting could not produce samples.

PASS line, verbatim, unmet: every verb answered under 1 s at p99 during lands for 1 hour; no false STOPPED.

Window start: not started
Window end: not started
Probe stamps (UTC, `date -u`): round 1 Sun Oct  4 11:40:26 PM UTC 2026, round 2 Sun Oct  4 11:41:55 PM UTC 2026

Build measured: unknown. The installed client did not start, so it printed no version line; neither `nova-sprint --version` nor `nova-sprint help | head -1` could run. The only build stamp available is this record's own base: branch sprint/accept-server-latency.w4.g3.e15 at 70587807db83810ecb356be2cdcdefbc7e497382, the tip of sprint/mechanical-2026-10-02. That is the record's base, not a measurement of a server build; no server was reachable to be measured.

Commands run (read-only probes in two rounds; the one-hour sample loop was not started). Every sprint verb was attempted in the card's exact form, `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`, one at a time:

- `date -u` at both round stamps above.
- `which nova-sprint nova-bus nova-bus2 curl jq` — nova-sprint at /home/ubuntu/.local/bin/nova-sprint, nova-bus at /home/ubuntu/.local/bin/nova-bus, curl and jq present; `nova-bus2` is not on PATH (`which` exit 1), so the un-renamed bus log form `nova-bus log --max 0` is the only one available, and it too is denied.
- `stat -c '%A %U %s %n'` on both installed binaries: `-rwxr-xr-x ubuntu 19206304 /home/ubuntu/.local/bin/nova-sprint` and `-rwxr-xr-x ubuntu 4497568 /home/ubuntu/.local/bin/nova-bus`. The unix mode and owner grant read and exec, so the denial below comes from the wall, not from the filesystem. This attempt's own wall line names the shape of the restriction: `SANDBOX OK backend=landlock abi=8 used=6 read=3 read-noexec=2 write=4 net=nopromise`; the installed clients fall in the denied set (read and exec both refused), as in the prior attempt, whose wall named the same path with landlock operation unverified, denial read.
- `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint --version` — exit 126, `timeout: failed to execute process: Permission denied (os error 13)`; the same run without `timeout` gives `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-sprint: Permission denied`.
- `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json` — exit 126, same denial (round 2).
- `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json` — exit 126, same denial (round 2).
- `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint log --json --since 1m` — exit 126, same denial (round 2). `nova-sprint log --json --since <window start>`, `where --json --cards`, and `card <a card in flight>` were not attempted further: no verb of this binary can execute, and no card in flight can be named without `where`.
- `head -c 4 /home/ubuntu/.local/bin/nova-sprint` — exit 1, `head: cannot open ... for reading: Permission denied`. The binary cannot even be read, so it cannot be copied around the exec denial either.
- `timeout 300 nova-bus log --max 0` — exit 126, same denial; `head -c 4 /home/ubuntu/.local/bin/nova-bus` — exit 1, same read denial. The bus log is unreadable by every available route.
- `timeout 10 curl -sS -m 5 http://127.0.0.1:6390/` — exit 7, `curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server`, in both rounds. A bare `/dev/tcp/127.0.0.1/6390` connect also fails: `connect: Connection refused`. Nothing listens on the sprint server's port; there is no server to answer any verb even if the client could run.
- `timeout 10 curl -sS -m 5 -D - -o /dev/null -w 'dash_code=%{http_code} dash_size=%{size_download}\n' http://127.0.0.1:7390/api/sprint` — `HTTP/1.1 200 OK`, `Server: Caddy`, `Content-Length: 0`, `dash_code=200 dash_size=0`, in both rounds; `http://127.0.0.1:7390/` answers the same empty 200. The dashboard endpoint is up but serves no sprint document, so it reports no machine state — not RUNNING, not STOPPED, nothing.

The sampling loop (every 10 seconds for 1 hour: `where --json`, `stats --json`, `log --json --since 1m`, `card <a card in flight>`, each timed with the shell's own clock via `/usr/bin/time -p`, recording verb, stamp, wall time and exit code) was not started. A verb that cannot be executed cannot be timed; a sample that cannot be read would be a counted failure, but here there is no window in which to count it. This card builds no code, so a self-built substitute client is outside the card and was not made; a curl-only substitute cannot time the verbs the PASS line names. No server was started on this machine (the rules forbid it, and the card measures the live system, not one this card brings up). The window was not stretched and not restarted: with 0 lands the card says report HOLD with the count, which this record does.

Criteria:

1. p99 wall under 1 s for every read verb, over samples taken during lands, for 1 hour.
   Bar: under 1 s at p99, per verb, during lands.
   Measured: not measured. Samples taken: 0. Samples passing: 0. Samples over 1 s or exiting 2 or timing out: 0 taken, so none counted. Worst value: none; when: no sample. p50, p99, maximum per verb: not computed — there are no rows for sort and awk.

2. At least 20 lands in the window (a land step in `nova-sprint log` within 30 seconds of a sample).
   Bar: at least 20, else HOLD with the count rather than stretch the window.
   Measured: lands counted: 0. `nova-sprint log` itself cannot run (exit 126) and the server that would hold the log is not listening (connection refused), so no land step exists to match any sample. This is the HOLD-with-count case, count 0.

3. No false STOPPED.
   Bar: every STOPPED reported by `where` or the dashboard in the window is listed with its stamp and checked against `nova-sprint log` for a real stop by the coordinator; any STOPPED with no real stop fails.
   Measured: not measured. STOPPED reports listed: 0. `where` runs: 0 (exit 126). Dashboard bodies read: 2 (one per round), both `Content-Length: 0`, which report no machine state at all. The cross-check source, `nova-sprint log`, cannot run either.

Raw lines that decide the hold (probe rounds 1 and 2; there are no sample lines, passing or failing, because the loop never started):

```
stamp: Sun Oct  4 11:40:26 PM UTC 2026
-rwxr-xr-x ubuntu 19206304 /home/ubuntu/.local/bin/nova-sprint
-rwxr-xr-x ubuntu 4497568 /home/ubuntu/.local/bin/nova-bus
timeout: failed to execute process: Permission denied (os error 13)
nova_sprint_version_rc=126
timeout: failed to execute process: Permission denied (os error 13)
nova_sprint_where_rc=126
head: cannot open '/home/ubuntu/.local/bin/nova-sprint' for reading: Permission denied
nova_sprint_read_rc=1
timeout: failed to execute process: Permission denied (os error 13)
nova_bus_rc=126
head: cannot open '/home/ubuntu/.local/bin/nova-bus' for reading: Permission denied
nova_bus_read_rc=1
curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server
curl_6390_rc=7
HTTP/1.1 200 OK
Server: Caddy
Content-Length: 0
dash_code=200 dash_size=0
stamp: Sun Oct  4 11:41:55 PM UTC 2026
timeout: failed to execute process: Permission denied (os error 13)
sprint_where_rc=126
timeout: failed to execute process: Permission denied (os error 13)
sprint_stats_rc=126
timeout: failed to execute process: Permission denied (os error 13)
sprint_log_rc=126
timeout: failed to execute process: Permission denied (os error 13)
bus_rc=126
bus2_which_rc=1
curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server
curl_6390_rc=7
dash_code=200 dash_size=0
```

Passing samples: count 0. No first line. No last line. Failing samples: count 0 taken; the failures above are probe failures that prevented the window, not samples within one.

What was not measured: verb wall times, verb exit codes from a running client, the installed build's version string, samples during lands, land steps within 30 seconds of samples, p50, p99 and maximum per verb (no rows for sort and awk), the count of lands (no log source), cards in flight, the bus log, every STOPPED stamp and its cross-check against `nova-sprint log`, and the dashboard's machine state (its body is empty). The full probe output is kept beside the job at `scratch/probe-1.log` and `scratch/probe-2.log` under the job directory, not in this repository. `TestAcceptanceRecordsAreWellFormed` was not run: the card says it is a placeholder name and is not run by this card, and no test of that name exists in `./internal/ci` at this base. No server was started; no process was killed; the window was not stretched; nothing outside the job directory was written.
