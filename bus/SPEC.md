# nova-bus: tests and models

The [guide](README.md) is how to use the tool. This page is for changing it.
Build and test commands for every tool are in [TESTING.md](../docs/TESTING.md).

Unit tests use an in-memory store and a fake clock. Functional tests
start a private Redis. One runs a server whose only user has the ACL rule in
the [bus guide](README.md), with `default` off, to show that rule is enough.
Keep tests for these cases, which the guide does not show:

- A stream with acknowledged entries that were never deleted. A hook trims
  history older than its first delivered, unacknowledged entry; with none, it
  trims nothing and `pending` stays high until the entries are removed with
  `XDEL`, or the inbox with `DEL`.
- A name held for the whole 40-second wait: plant `bus:presence:<name>` with
  `SET ... PX 60000` and a value `<RFC 3339 time><TAB><token>`. The hook must
  print `HOOK WAIT` and exit 1 with `name already hooked`.
- A name taken while a hook runs: overwrite its key. Within 10 seconds the hook
  must exit 1 with `name taken by another hook while this one lapsed`.
- A stdout failure: start a hook with fd 1 opened read-only (`1< file`). A
  closed fd 1 (`>&-`) is unreliable, because the Redis socket can take fd 1.
- A stdin failure: `send ... -` with fd 0 opened write-only gives
  `reading body from stdin`.
- Redis down under a hook: one `reconnecting` line, then at most one a minute.
- A reader that goes: a hook on a pipe whose read end closes after `HOOK OK`
  exits 1 within seconds, `stdout closed: its reader is gone`, with its key
  gone; `hook | true` ends the same way, or with `broken pipe`, the message
  left waiting.
- A send to a name no hook or recv has read: exit 1 and nothing sent;
  `--new` sends; a name with the group takes a send without it.
- recv then ack: recv prints the oldest unhandled and acks nothing (a second
  recv prints it again); ack marks it, and a second ack of the same id fails
  without deleting anything (XDEL only after an XACK of 1, in one script).
  A recv beside a live hook is refused, "name hooked"; a waiting recv claims
  the key again every 10 s, and one that has printed a message holds the
  name naming it for 15 minutes: an ack at 31 s and at 14 minutes passes
  the gate, at 16 minutes the claim has lapsed and the next recv hands the
  message out again.
- The wakes and the service are unit-tested on fake seams (exec, shell,
  devOf, the service manager): no socket, process or device is reached.

## Models

`bus/tla/Presence.tla` models the key with its TTL and hooks that compete for
one name: start, refresh, a stdout that blocks and unblocks, a clean exit
whose DEL may fail, a crash, expiry, and a start that waits for the key. It
checks: a name is present only if
a hook set the key within the TTL; a running hook not stuck on stdout is
present with its own token; a hook never starts while another's token is
present; the key is deleted only by the hook whose token it holds; a hook
that exited or crashed is absent within the TTL; a waiting hook whose rival
crashed sees the name claimed; and every wait ends in a claim or a refusal. A stuck hook is allowed to
drop out, and `PresenceStuckStrict.cfg` shows that it does. The reversed
witnesses remove the TTL, the refresh, the claim's check, the release's check
and the wait, one each. `Presence.cfg` checks two hooks; `PresenceThree.cfg` checks three (about 3.9M states, minutes on many workers, so it is run by hand rather than by `tests/tla/check.sh`). `bus/tla/Bus.tla` models delivery. `bus/tla/CASES.tsv` names
every case with the outcome TLC must reach; `tests/tla/check.sh JAR bus/tla` runs
them.

`tla/BusWake.cfg` adds wakes that fail: a failed wake ends the hook with the
entry unacked, the hook restarts (weakly fair) from the pending list, and so
every message is still printed; its broken cases (a failed wake that acks; a
failed wake that does not end the hook, which reads past the entry and never
delivers it) must fail. `tla/PresenceBrokenSlowBatch.cfg` is the witness for
a delivery that is not bounded under the refresh: time passes the refresh and
the key lapses under a running hook (the wake is bounded by 8 s, with a
refresh before every delivery, so the design passes `Presence.cfg`).

Run the models with TLC, with Java on PATH and the jar pinned in
[tests/tla/tla2tools.sha256](../tests/tla/tla2tools.sha256), at an absolute path:

```sh
tests/tla/check.sh /opt/tla/tla2tools.jar bus/tla
```

Each line starts with `ok` when the case came out as `bus/tla/CASES.tsv` says.
