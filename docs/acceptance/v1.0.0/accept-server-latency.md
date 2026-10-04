Verdict: HOLD

The check was not run. This is not a measured failure of a window, and it is not a pass. The PASS line is unmet because no sample exists.

PASS line, verbatim, unmet: every verb answered under 1 s at p99 during lands for 1 hour; no false STOPPED.

Window start: not started
Window end: not started
Probe stamp (UTC, `date -u`): Sun Oct  4 11:36:50 PM UTC 2026

Build measured: unknown. The installed client did not start, so it printed no version line.

Commands run (read-only probes; the one-hour sample loop was not started):

- `date -u` at Sun Oct  4 11:36:50 PM UTC 2026
- `/home/ubuntu/.local/bin/nova-sprint --version` with `NOVA_SPRINT_SERVER=127.0.0.1:6390` and `NOVA_SPRINT_ACTOR` set as the card requires. Exit 126. Shell line: `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-sprint: Permission denied`. A copy of that path failed open for reading with the same denial. `file` on that path reported no read permission. The prior attempt's wall, re-read here, names the same path: landlock denied, operation unverified.
- `/home/ubuntu/.local/bin/nova-bus --version`. Exit 126. Shell line: `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied`. Open for reading denied the same way. `nova-bus2` is not on `PATH`.
- `curl -sS -m 3 http://127.0.0.1:6390/` — connection refused in 0 ms. Nothing listens on 6390. Exit of curl is 7.
- `curl -sS -m 3 -D - http://127.0.0.1:7390/api/sprint` — HTTP 200, `Content-Length: 0`, body size 0, server name Caddy. No sprint document.

The sampling loop (every 10 seconds for 1 hour: `where --json`, `stats --json`, `log --json --since 1m`, `card <a card in flight>`, timed with the shell clock) was not started. A verb that cannot be executed cannot be timed, and stretching or substituting a self-built client is outside this card.

Criteria:

1. p99 wall under 1 s for every read verb, over samples taken during lands.
   Bar: under 1 s at p99, per verb, during lands, for 1 hour.
   Measured: not measured. Samples taken: 0. Samples passing: 0. Worst value: none. When: no sample.
   p50, p99, maximum: not computed (no rows for sort or awk).

2. At least 20 lands in the window.
   Bar: at least 20, else HOLD with the count, do not stretch the window.
   Measured: lands counted: 0. The window did not start, so this is a hold with count 0, not a stretched window.

3. No false STOPPED.
   Bar: every STOPPED from `where` or the dashboard in the window has a real stop in `nova-sprint log`.
   Measured: not measured. STOPPED reports listed: 0. Samples of `where`: 0. Dashboard bodies: 1 empty body, which reports no machine state.

Raw lines that decide the hold (there are no passing sample lines):

```
Sun Oct  4 11:36:50 PM UTC 2026
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-sprint: Permission denied
sprint_exec_rc=126
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied
bus_exec_rc=126
curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server
dash=200 size=0
```

Passing samples: count 0. No first line. No last line.

What was not measured: verb wall times, exit codes of a running client, samples during lands, p50, p99, maximum, land steps within 30 seconds, the bus log, a card in flight, any STOPPED stamp, and the installed build's version string. `TestAcceptanceRecordsAreWellFormed` was not run; this card does not run it. No server was started. The window was not stretched.
