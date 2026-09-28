# SPEC-STATE — the token ledger on Redis

The day TSVs that `nova-tokens fold` writes are the record of token use. The **token
ledger** is their index on the fleet Redis: `nova-tokens ledger` writes one hash per day,
and `nova-tokens report --redis` answers a month from those hashes in one round trip
instead of reading every day file. The ledger is rebuildable from the day files at any
time; nothing in it is the only copy of anything. The code is `internal/record/ledger.go`
(the key layout and the store) and `cmd/nova-tokens/ledger.go` (the two verbs). Related:
[SPEC-REDIS.md](SPEC-REDIS.md) (the instance, its owner prefixes) and
[SPEC-TOKENS.md](SPEC-TOKENS.md) (the fold and the day files).

## The key layout

`tokens:ledger:<YYYY-MM-DD>` is a hash, one per folded day, and `tokens:ledger:` is the
owner prefix of every key the ledger writes.

- **Field:** one row's key, the JSON array `["<card>","<model>","<repo>"]`, so
  `(day, card, model, repo)` is the primary key. The card is the day row's unit, `-` when
  the row has none.
- **Value:** the JSON object `{"provider","tokens","rough","sources"}`. `tokens` carries the
  five types in the order `input`, `output`, `cache_write`, `cache_read`, `reasoning`, with
  `null` for a type no source reported — never `0`, because a dash in the day file is an
  absence. `sources` is the row's sources, sorted and comma-joined; `provider` is their
  kinds (the part before `:`), sorted and comma-joined.
- **Two day rows under one key** are summed into one ledger row, each type summed over the
  rows that reported it and `null` when none did.

A month is the day keys of its calendar days, read in one pipelined round trip: no `SCAN`,
no index key to keep true.

## `nova-tokens ledger`

```text
nova-tokens ledger --out <dir> (--day <YYYY-MM-DD> | --month <YYYY-MM>) --redis <host:port>
                   [--user <name>] [--password-env <NAME>]
```

It reads the day files under `--out` (`<dir>/<day>.tsv`; a month is every valid day file of
that month) and writes each day to `tokens:ledger:<day>`. A day is **replaced whole**: `DEL`
then `HSET` of the day's hash in one `MULTI`/`EXEC`, so indexing a day twice is the same
ledger as indexing it once, and no reader sees half a day. It writes nothing beside the day
files and never changes them.

- `LEDGER day=<day> rows=<n>` for each day written.
- `LEDGER BAD day=<day> why=<reason>` for a day file that does not read or has a finding; a
  missing day file is `why=no day file; fold --day <day> first`, and nothing is written for it.
- `LEDGER OK|NO day=<day>|month=<month> days=<n> rows=<n> bad=<n>` last: `NO` (exit 1) when a
  day was bad or no day was written.
- `LEDGER FAILED store=redis err=<err>` (exit 1) when the store does not answer its `PING`,
  before any day file is read; `LEDGER FAILED day=<day> err=<err>` when a write fails.
- `--out`, `--redis` and exactly one of `--day` or `--month` are required; a missing or
  malformed one is refused (exit 2).

## `nova-tokens report --redis`

```text
nova-tokens report --redis <host:port> --month <YYYY-MM> [--by model|repo|day|tuple] [--max <n>]
                   [--user <name>] [--password-env <NAME>]
```

It reads every `tokens:ledger:<day>` of the month's calendar days in one pipelined round trip
and groups the rows: by `model` (the default), `repo`, `day`, or the `(day, model, repo)`
`tuple`. Each type of a group is the sum over the rows that reported it, and a dash when none
did.

- `REPORT <key columns> rows=<n> input=<n> output=<n> cache_write=<n> cache_read=<n>
  reasoning=<n>` per group, sorted by the key columns, capped at `--max` (default 20, `0` =
  all) with `REPORT MORE shown=<n> of=<n>; raise --max (0 = all)` when capped.
- `REPORT OK month=<month> source=redis groups=<n> rows=<n> indexed=<n> missing=<n>` last:
  `indexed` is how many calendar-day keys existed, `missing` how many did not.
- `REPORT NO month=<month> source=redis indexed=0` (exit 1) when no day of the month is indexed.
- `REPORT FAILED store=redis err=<err>` (exit 1) when the store does not answer, or a field or
  value does not decode; the error names the key, and a row is never quietly skipped.
- A missing or malformed `--month`, a `--by` that is not one of the four, and `--redis` given
  with `--ledger` (two sources for one report) are refused (exit 2).

## The seat

Both verbs dial the fleet Redis as one ACL user. The user is `--user`, else
`NOVA_SPRINT_REDIS_USER`. The password is never a flag: it is the variable `--password-env`
names, else the one `NOVA_SPRINT_REDIS_PASSWORD_ENV` names, else `NOVA_REDIS_BENCH_PASSWORD`.
With no user the connection is the default user's, and a password is read only when
`--password-env` names its variable. A user whose password variable is empty is refused
before any dial, with the remedy (`run under nova-secrets exec --only <NAME>`).

## Tests this spec demands

The tests run against **miniredis**, every path is a `t.TempDir()`, and nothing reaches the
network. The list is numbered from 17: `cmd/nova-tokens` and `internal/record` cite the
monthly-report test as this spec's test 17.

17. `TestTheMonthlyTokenReportFromTheRedisLedgerEqualsTheFoldedTsv` — `nova-tokens report
    --redis` over the `tokens:ledger:<day>` hashes equals the folded day TSVs, every type and
    every `(day, model, repo)`; `ledger` writes exactly one `tokens:ledger:<day>` per folded
    day, re-indexing a day replaces it, and the day files are left byte for byte as the fold
    wrote them.
18. `TestReportRedisRefusesWithoutMonth` — the report refuses a missing `--month`, a `--by`
    outside the four, and `--redis` with `--ledger`.
19. `TestLedgerRefusesWithoutRedisAndNamesAMissingDay` — `ledger` refuses without `--redis`,
    and a day with no file is a `NO` naming the fold, with nothing written.
20. `TestLedgerReadsThePasswordFromTheVariableItIsToldToOnly` — with no user, no password
    variable is consulted unless `--password-env` names it.
21. `TestReportRedisNoIndexedDaysExitsOne` — a month with no indexed day is `REPORT NO`, exit 1.
22. `TestReportRedisPartialMonthNamesIndexedMissing` — a partly indexed month names
    `indexed` and `missing` on the `OK` line.
23. `TestLedgerAndReportDialAsTheAclUser` — both verbs connect as the ACL user from `--user`
    or `NOVA_SPRINT_REDIS_USER`, and a user with an empty password variable is refused.
24. `TestTheRedisLedgerKeepsTheLedgerContract` (`internal/record`) — a day is replaced whole,
    the report sums over the rows that reported a type, and an unreported type stays unknown.
25. `TestTheLedgerIsOneHashPerDayUnderTokensLedger` (`internal/record`) — one hash per day,
    one field per `(card, model, repo)`, and an unreported type stored as `null`.
26. `TestTheLedgerRefusesARepeatedKeyAForeignDayAndABadDay` (`internal/record`) — a batch
    that repeats a key, carries another day's row, or names a day that is not `YYYY-MM-DD` is
    refused.
27. `TestTheLedgerReportRefusesAValueItCannotRead` (`internal/record`) — a field or value that
    does not decode is an error naming the key.
