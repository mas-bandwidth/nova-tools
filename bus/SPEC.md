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

`tla/BusWake.cfg` adds wakes that fail: no message is lost (it is delivered or
stays pending), and a failed one never holds the hook in its pending pass; its
broken cases (a failed wake that acks; a pending read that does not move past
a failed wake) must fail.

Run the models with TLC, with Java on PATH and the jar pinned in
[tests/tla/tla2tools.sha256](../tests/tla/tla2tools.sha256), at an absolute path:

```sh
tests/tla/check.sh /opt/tla/tla2tools.jar bus/tla
```

Each line starts with `ok` when the case came out as `bus/tla/CASES.tsv` says.
