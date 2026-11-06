# nova-bus: tests and models

The [guide](README.md) is how to use the tool. This page is for changing it.
Build and test commands for every tool are in [TESTING.md](../docs/TESTING.md).

Unit tests use an in-memory store and a fake clock. Functional tests
start a private Redis. One runs a server whose only user has the ACL rule in
the [bus guide](README.md), with `default` off, to show that rule is enough.
Two models check the design: [Bus.tla](tla/Bus.tla) for print before
ack and replay, and [Presence.tla](tla/Presence.tla) for claims,
refresh, expiry and release. `PresenceThree.cfg` checks three hooks and is run
by hand. Keep tests for these cases, which the guide does not show:

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

Run the models with TLC, with Java on PATH and the jar pinned in
[tests/tla/tla2tools.sha256](../tests/tla/tla2tools.sha256), at an absolute path:

```sh
tests/tla/check.sh /opt/tla/tla2tools.jar bus/tla
```

Each line starts with `ok` when the case came out as `bus/tla/CASES.tsv` says.
