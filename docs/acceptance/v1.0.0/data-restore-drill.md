# v1.0.0 acceptance: the fleet's configuration and bus store restore onto a fresh host within the budget

- Requirement: docs/DATA.md, Acceptance: restore PostgreSQL and the bus store onto a fresh, isolated host (loopback, no route to the live fleet) in DATA.md's restore order, apply the configuration, and show the retained messages, the receipts and the pending deliveries recovered, within the 30-minute restore time
- Release: v1.0.0
- Measured: 2026-10-06T13:19:03Z
- From: a Linux bench (x86_64, 64 cores), every server a fresh one on 127.0.0.1 under the test's temporary directory; the working tree of sprint/data-lifecycle-names-the-source-of-truth2.w1.g1.e15 on top of the carried design commit (7f922b7cb; the Raw block's 9e2129507 is the same tree before its message was amended), before this commit
- Tool: go test -tags functional -run TestRestoreDrillOntoAFreshHost ./cmd/nova-config/ (go1.26.6), with PostgreSQL 18.6 (pg_dump, pg_restore, initdb) and the bench's own Redis 8 redis-server (not the functional image's reference build, which this run did not use)
- Verdict: MET

The drill is a test, so the record is reproducible: anyone with a PostgreSQL client and server and a
redis-server on PATH runs the same command and gets the same checks. The numbers are as it printed
them.

## Method

The old host: a throwaway PostgreSQL database, migrated, with a machine, two friends (ada, bob) and
the fleet row, applied into a throwaway Redis that is also the bus store. On the bus: five messages
from ada to bob (copied to the machine m1); bob is handed three, acks one and holds two (pending,
delivered and not acked); two are never delivered; bob replies to the first, which is his receipt of
it, so four stay owed to bob and his reply is owed to ada. The facts are read: `bus2:log`'s length
and newest id, each stream's length, each group's last delivered entry, each group's pending list,
what was never delivered, and each `bus2:owed:<friend>` hash.

The backups, as the loops take them: the bus store's snapshot through the same `store.Snapshotter`
`nova-sprint snapshot` uses (checksum, twin, keep), saved with SAVE where the verb uses BGSAVE; then a
configuration change (friend cy added, not applied) and a message sent; then `nova-config backup`
(pg_dump, `pg_restore --list`, the table-data check, the SHA-256). So the dump is newer than the
snapshot's copy of the configuration, and one message falls in the window the snapshot does not
cover.

The loss: the old Redis is shut down unsaved and the old database is dropped, so nothing below can
read the old host.

The witness, before the clock starts: a Redis started with its AOF on, in a directory holding only
the snapshot as `dump.rdb`, loads nothing (DBSIZE 0). DATA.md's restore order steps around it.

The restore, timed from here: a fresh PostgreSQL cluster (initdb), an empty database,
`pg_restore --no-owner --exit-on-error` of the dump, `nova-config migrate` (applied=0),
`nova-config status`
(refuses: the Redis copy is behind until apply). The snapshot's sum checked (`store.RestoreDrill`),
the file placed as `dump.rdb` in an empty directory, a Redis started there with the AOF off,
`CONFIG SET appendonly yes` and the rewrite waited for, that Redis shut down, and the store's own shape (AOF
on, fsync every second) started on the directory; its key count equals the loader's. The function
library loaded. Before apply the roster is the snapshot's (ada, bob, m1). Then `nova-config apply`,
then the facts read again.

## Results

- The bus store's facts after the restore equal the facts at the snapshot, field for field: 6
  messages in `bus2:log` with the same newest id, the same stream lengths and group positions, bob's
  2 pending and 2 never delivered, 4 receipts owed to bob and 1 to ada.
- The configuration after the restore equals the configuration at the dump (`friend list`,
  `fleet show`), and apply replaced the snapshot's older roster with PostgreSQL's (ada, bob, cy, m1).
- The message sent after the snapshot is not in the log: the loss DATA.md accepts, at most
  `nova_data_redis_every`.
- The deliveries resume: bob is handed the two never-delivered entries, in order; the friends'
  undelivered counts are 1 and 4.
- The restored store wrote its AOF (`appendonlydir`) from what it loaded.
- The restore took 8.922 s of a 30-minute budget.

What this does not show: a restore onto a second machine (the fresh servers ran on the same bench as
the old ones, on other ports and directories), the sprint store (restored by the same step), the
secrets store (DATA.md step 2), the ACL users (`fleet/redis.yml`), or the restore time at the fleet's
own data volume: the drill's stores are small, so 8.9 s is the procedure's floor, not the fleet's
time.

## Raw

```
2026-10-06T13:19:03Z
Linux x86_64
64
go version go1.26.6 linux/amd64
9e212950773a3a53ede01dd960960157d840ea06
=== RUN   TestRestoreDrillOntoAFreshHost
=== PAUSE TestRestoreDrillOntoAFreshHost
=== CONT  TestRestoreDrillOntoAFreshHost
redis: 2026/10/06 13:19:21 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:39517: connect: connection refused
redis: 2026/10/06 13:19:21 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:39517: connect: connection refused
redis: 2026/10/06 13:19:21 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:39517: connect: connection refused
redis: 2026/10/06 13:19:29 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:44839: connect: connection refused
redis: 2026/10/06 13:19:30 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:44839: connect: connection refused
redis: 2026/10/06 13:19:30 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:44839: connect: connection refused
    restore_drill_functional_test.go:205: RESTORE DRILL OK elapsed=8.922s budget=30m0s log=6 newest=1791292760031-0 pending_bob=2 undelivered_bob=2 owed_bob=4 owed_ada=1 dump=config-20261006T131920Z.dump snapshot=snapshot-20261006T131920Z.rdb lost_after_snapshot=1
--- PASS: TestRestoreDrillOntoAFreshHost (12.40s)
PASS
ok  	github.com/mas-bandwidth/nova-tools/cmd/nova-config	20.038s
```
