# accept-installs

Verdict: HOLD

PASS line, verbatim: a server install and a rollback each done by the verbs, gated by the land self-test.

Window start (UTC): not opened
Window end (UTC): not opened
Probe start (UTC): 2026-10-04T23:29:37Z
Probe end (UTC): 2026-10-04T23:32:50Z

The install window stays closed. The release reason for this card is unreadable, so the 30-minute window does not start. A closed window is not stretched into a failed sample run.

Build measured: unknown. The installed sprint verb does not run, so its version line is not read. A host-matched dashboard snapshot fetched at 2026-10-04T23:32:12Z reports build 19c41193. That snapshot is not a verb reading and does not name this card.

## Commands

Each sprint verb is invoked with the card's server and actor prefix. The actor value is omitted in this record. Exit status follows each command.

1. `date -u` at 2026-10-04T23:29:37Z prints `Sun Oct  4 11:29:37 PM UTC 2026`.
2. `nova-sprint --version` exits 126. The shell reports `Permission denied` for the installed binary. Opening that binary for reading also fails, so a copy into the job directory is not made and is not executed.
3. `ss -tln` shows no listener on 127.0.0.1:6390. Port 7390 listens.
4. `curl -sS -m 3 http://127.0.0.1:6390/` exits 7: `Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server`.
5. `curl -sS -m 3 http://127.0.0.1:7390/api/sprint` returns HTTP 200, Content-Length 0, body 0 bytes. The listener matches a different Host than the loopback Host this URL sends.
6. `nova-bus --version` exits 126. The shell reports `Permission denied`.
7. `nova-update --version` exits 126. The shell reports `Permission denied`.
8. `date -u` at 2026-10-04T23:32:50Z prints `Sun Oct  4 11:32:50 PM UTC 2026`.
9. The loopback sprint probe and the card dashboard URL are repeated: connection refused, and HTTP 200 with size 0.

A request to the dashboard listener with the Host the listener matches returns HTTP 200, 81288 bytes, ok true, build 19c41193, fetchedAt 2026-10-04T23:32:12.077243+00:00. That body contains no string naming this card and no install-window release reason. The matched host address is omitted here.

## Criteria

PASS needs an install and a rollback, each done by the verbs, each gated by the land self-test with the self-test pass recorded, and the server back on the original build.

Install by the verb, gated by the land self-test.
Bar: one install of the build named in the release reason, by the verb, with the self-test pass recorded.
Measured: not run.
Samples taken: 0. Samples passing: 0. Worst: the verb does not start (exit 126) at 2026-10-04T23:30:40Z.

Rollback by the verb, gated by the land self-test.
Bar: one rollback to the build that was running before, by the verb, with the self-test pass recorded.
Measured: not run.
Samples taken: 0. Samples passing: 0. Worst: the verb does not start (exit 126) in the same probe, 2026-10-04T23:30:40Z to 2026-10-04T23:32:50Z.

Server back on the original build.
Bar: the server answers on the build recorded before the install.
Measured: not run. The original build is unknown because the version verb does not run and 127.0.0.1:6390 does not answer.
Samples taken: 0. Samples passing: 0. Worst: no answering server on 127.0.0.1:6390 at 2026-10-04T23:32:50Z.

Release reason opens the window.
Bar: the card log says the install window is open.
Measured: unreadable.
Samples taken: 1. Samples passing: 0. Worst: the log verb exits 126 before any line is read, at 2026-10-04T23:30:40Z.

## Raw samples

Failing samples, in full:

```
nova-sprint --version
exit 126
Permission denied
```

```
nova-bus --version
exit 126
Permission denied
```

```
nova-update --version
exit 126
Permission denied
```

```
curl http://127.0.0.1:6390/
exit 7
Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server
```

```
curl http://127.0.0.1:7390/api/sprint
HTTP/1.1 200 OK
Content-Length: 0
body bytes: 0
```

Passing samples: 0. First of the passing ones: none. Last of the passing ones: none.

## What was not measured

The release reason. The install and rollback verb names (help does not run). The land self-test name, its result line, and the proof that a failing self-test refuses. The build before and after an install. The time the server is unanswering during an install. The bus log. The 30-minute sample window, which does not open. No process is started, stopped, or killed. No hand step is named because the verbs never start.

Blocker: the wall refuses execution of the installed sprint, bus, and update binaries (read and execute both denied), and nothing listens on 127.0.0.1:6390, so the release reason cannot be read and the install window stays closed.
