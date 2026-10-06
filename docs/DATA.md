# DATA: where each class of data lives, and how it comes back

*Normative for the fleet's data. Each class of data has one source of truth, and
this page names it. [SPEC-BUS.md](SPEC-BUS.md) and [SPEC-REDIS.md](SPEC-REDIS.md)
state the same split for their own tools, and [SPEC-CONFIG.md](SPEC-CONFIG.md) states
it for configuration. If one of them disagrees with this page, one of them has a bug, and
`TestTheSpecsAgreeOnTheSourceOfTruth` (internal/ci/data_lifecycle_test.go)
decides which.*

## The source of truth

| Class | Source of truth | What it is | Copies | Writer |
| --- | --- | --- | --- | --- |
| configuration | PostgreSQL | schema `config`: every kind's rows and `config.history` | the keys `nova-config apply` writes into Redis, rebuilt by the next apply | nova-config |
| messages | Redis | the bus store: `bus2:to:<name>` and `bus2:log` | its backups only | nova-bus |
| receipts | Redis | the bus store: each stream's consumer group and pending list, `bus2:owed:<friend>`, `bus2:push` | its backups only | nova-bus, the friend daemon |
| runtime state | Redis | the sprint store: cards, leases, the seat, judgments, beats, ledgers | its backups only | nova-sprint, nova-swarm, nova-friend |
| scratch | none | `<owner>:<name>` keys with a TTL | none | nova-redis spill |
| code, playbooks and records | Git | the repositories: the code, the fleet plays, the docs, the TLA+ models, and the records a repository holds on purpose (acceptance records, TLC records, allowlists, ledgers checked in) | the forge and every clone | whoever commits |

Redis is the only store of messages, receipts and runtime state. For those
classes it is the record, so it is backed up as a record. The configuration
in Redis is only a copy. When the copy and PostgreSQL disagree, PostgreSQL is
right, and `nova-config apply` fixes the copy. Scratch is nobody's record, and
reading it is allowed to miss. Git holds no message and no runtime state. A
record written by a tool lives in Git only when a repository holds that record
on purpose.

## Backups

| Class | How | Every | Kept | Verified by |
| --- | --- | --- | --- | --- |
| configuration | `pg_dump --format=custom` of the `nova_pg_dsn` database | `nova_data_pg_every` | `nova_data_pg_keep` | `pg_restore --list` reads the dump whole |
| messages, receipts | `nova-sprint snapshot --redis <the bus store> --dir <backup dir>/redis-bus`, run by the loop record `data-backup-bus` | `nova_data_redis_every` | `nova_data_redis_keep` | the snapshot's SHA-256, then a load into a twin |
| runtime state | `nova-sprint snapshot --redis <the sprint store> --dir <backup dir>/redis-sprint`, run by the loop record `data-backup-sprint` | `nova_data_redis_every` | `nova_data_redis_keep` | as above |
| code, playbooks and records | a push to the forge; every clone is a copy | each push | all of it | git's own hashes |

Each Redis store also keeps its AOF (fsync every second) and an RDB every 60 s
in its own `--dir` (SPEC-REDIS.md). Those survive a crash. They do not survive
the loss of the host's disk. The backups above do, but only if they are written
somewhere other than the store's disk. For that reason `nova_data_backup_dir`
has no default: the fleet names a volume that outlives the store host. The
backup loops log in to each store as `nova_data_redis_user`, with the password
held in the secret `nova_data_redis_password_key` in the store machine's seat.
A snapshot asks the store for `BGSAVE` and `CONFIG GET`, and copies the RDB from
the store's own `--dir`, so it runs on the store machine.

The backup play, `fleet/backup.yml`, sets up the backup configuration on the
store machine. It creates the backup directories (mode 0700) and writes every
setting on this page to `nova_data_settings_file` (JSON, mode 0600). It holds
the two Redis backup loops against what those settings say. A loop that is
missing, or whose argv differs, gets a `BACKUP LOOP` line carrying the
`nova-config loop add` line that fixes it, and outside `--check` the play then
fails. A loop record goes into PostgreSQL and is rendered by `fleet/loops.yml`,
and the play never writes PostgreSQL itself.

**Owed.** No verb takes, rotates and verifies a PostgreSQL backup yet, the way
`nova-sprint snapshot` does for Redis. Until one exists, the play prints
`BACKUP config OWED` and a dump is taken by hand before every `nova-config
migrate`. The configuration class's loss budget below is not met until that
verb exists.

## Acceptable loss, restore time and ownership

| Class | Acceptable loss | Restore time | Owner |
| --- | --- | --- | --- |
| configuration | the changes since the last dump, at most `nova_data_pg_every` | 30 minutes | the fleet's owner restores; nova-config is the one writer |
| messages | 1 s on the same host (the AOF); at most `nova_data_redis_every` when the host is lost | 30 minutes | the fleet's owner restores; nova-bus is the writer |
| receipts | as messages: a receipt lost with its hour reads as owed again, so the loss shows up as a duplicate, never as a message that silently vanishes | 30 minutes | as messages |
| runtime state | as messages | 30 minutes | the coordinator seat |
| scratch | all of it | none | its owner prefix |
| code, playbooks and records | nothing that was pushed | one clone | the repository's maintainers |

The bus delivers at least once. After a restore, a message delivered and acked
in the lost window is pending again and gets delivered a second time. A message
sent in that window is gone. Its sender's `SEND OK` is the only evidence it
existed.

## Restore order onto a replacement host

1. **Git.** Clone nova-tools at the fleet's release and install the tools
   (`fleet/tools.yml`). The plays and the code that restores everything else
   come from Git.
2. **Secrets.** Restore the nova-secrets store from its own backup
   (SPEC-SECRETS.md). Every later step logs in through it.
3. **PostgreSQL.** Create the database, `pg_restore` the newest dump, then run
   `nova-config migrate` and `nova-config status`.
4. **Redis.** For each store, run `nova-sprint snapshot --restore-drill <file>`
   on the newest snapshot. Place it as `dump.rdb` in an empty `--dir` (one with
   no `appendonlydir`), then start `nova-redis serve` on that dir. Redis loads
   the RDB and writes its AOF from it (Redis 7 and later; the acceptance drill
   below is what proves it on the fleet's version). Then run `fleet/redis.yml`. The users
   come back through `acl apply`, because `users.acl` lives beside the store and
   is not in the snapshot. Then run `nova-redis fn load`.
5. **Apply config.** Run `nova-config apply` (PostgreSQL over the copy the
   snapshot held), then `fleet/loops.yml` and `fleet/backup.yml`.
6. **Check.** Compare `nova-bus log --max`, `nova-bus peek --as <name>` and the
   friends table's undelivered counts with the drill's counts. The time from
   step 3 to here is the restore time measured against the budget.

PostgreSQL comes before Redis's apply because apply reads it. Redis is
restored before apply so that apply overwrites the snapshot's older copy of
the configuration.

## Thresholds

Each threshold is a setting with a default in `fleet/group_vars/all.yml`. The
backup play writes it to `nova_data_settings_file`, and a measure that reaches
it is an alarm.

| Setting | Default | Measure |
| --- | --- | --- |
| `nova_data_bus_messages_per_hour` | 2000 | entries added to `bus2:log` in the last hour |
| `nova_data_oldest_pending_seconds` | 3600 | the idle time of the oldest entry in any group's pending list |
| `nova_data_unreceipted_age_seconds` | 1800 | the age of the oldest `bus2:owed:<friend>` mark |
| `nova_data_disk_free_percent` | 20 | free space on the file system of a store's `--dir` or of the backup directory |
| `nova_data_redis_memory_percent` | 75 | a store's used memory as a share of `maxmemory`, or of the host's memory when `maxmemory` is 0 |

The friends table already shows the undelivered count and the oldest age.
**Owed:** a watcher that reads the settings file and raises the other
thresholds.

## What retention must keep

These come before retention, and no retention rule may remove them:

- **The audit history.** `bus2:log` holds every message once. It is the answer
  to "who sent what to whom, when", and it is kept for at least
  `nova_data_bus_log_keep_days`.
- **The delivery state.** On a recipient's stream, keep every entry the group
  has not delivered yet (after its last-delivered id), every entry in its
  pending list (delivered and not acked), and every entry whose id is a field of
  `bus2:owed:<friend>` (not receipted). Keep the groups themselves, and
  `bus2:push`, which is runtime state that gets overwritten.

## Retention

An entry of `bus2:to:<name>` may be removed only when all of these hold: its
group has delivered it, it is in no pending list, no `bus2:owed` field names
it, it is older than `nova_data_bus_stream_keep_days`, and it is in `bus2:log`.
An entry of `bus2:log` may be removed only when it is older than
`nova_data_bus_log_keep_days` and it is in a verified backup that is kept
longer than the log entry was. **Owed:** the trim that applies this rule. Until
it exists, nothing is deleted (SPEC-BUS.md), and the memory and disk thresholds
are what bound the history.

## Acceptance

Restore PostgreSQL and both Redis stores onto a fresh, isolated host (bound to
loopback, with no route to the live fleet), following the restore order above,
then apply the configuration. Then show three things are back: the retained
messages (`bus2:log`'s count and newest id), the receipts (each `bus2:owed`
hash), and the pending deliveries (each group's pending list). The time must
fall within the restore time above. **Owed:** the drill has not been run, and
its record will go beside the other acceptance records.
