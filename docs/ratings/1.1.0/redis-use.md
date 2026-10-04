# nova-redis USE rating, nova-tools 1.1.0

Rater: google/gemini-3.8-flash
Build: 2fb4e333eb2c
Score: 8/10

## Reasons
The tool offers structured command-line operation for running Redis, loading function libraries, rendering ACL configurations, and storing short-lived values with mandatory TTLs. First runs succeed cleanly with version inspection and store-free ACL rendering. Error handling is notably strong for basic usage: missing flags and invalid values are aggregated and reported together in both human-readable text and JSON output arrays. Bounded store-free dry runs are implemented for value storage via `spill --dry-run`.

However, several defects prevent a higher score:
1. `acl apply --dry-run` crashes with `ACL-APPLY FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`, violating the dry-run safety contract.
2. `acl apply` reports multiple missing users but only suggests a remediation flag for one user at a time, requiring repetitive error loops.
3. Diagnostic output in `fn check` and `acl check` emits malformed key-value pairs with unclosed quotation marks on `remedy="...`.
4. The `--addr` flag strictly requires `<host:port>` and rejects unix domain sockets used by companion tools.
5. `acl render` does not support `--json`.

Per the rating guidelines, no redis-server was started on this host. Verbs requiring an active TCP server (`recall`, `fn load`, `fn check`, `serve`, and `acl check`) could not be exercised end-to-end because no in-memory mode is available and those verbs lack dry-run support.

A score of 10 would require fixing the dry-run defect in `acl apply`, generating remediation flags for all missing users at once, closing quotation marks on remedy output, adding unix socket support to `--addr`, adding `--json` to `acl render`, and providing store-free simulation modes for operational verbs.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | Verb fails with `ACL-APPLY FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`, breaking dry-run safety and confusing callers. | Inspect Call.DryRun before performing mutations, output planned user additions without executing writes, and exit cleanly without failing. | M |
| 2 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | When multiple users are missing from the store, the suggested remedy command only specifies `--password-env-for coordinator=<VARIABLE>`, omitting the remaining missing users and requiring iterative runs. | Generate the remedy command string containing `--password-env-for <user>=<VARIABLE>` flags for all missing users simultaneously. | S |
| 3 | `nova-redis fn check --addr 127.0.0.1:59999` | Error output has an unclosed quotation mark in `remedy="no answer: check that the store...`, producing malformed key-value pairs that break structured log parsers. | Append the missing closing quote to the remedy value in the diagnostic formatter. | S |
| 4 | `nova-redis spill --dry-run --addr /tmp/redis.sock --owner worker --name note --ttl 10m --value hi` | `--addr` rejects filesystem socket paths with `is not <host:port>; refusing to guess`, preventing connection to local unix domain sockets created by companion tools. | Accept unix socket paths or socket URIs in `--addr` and dial the unix domain socket network instead of demanding TCP port parsing. | M |
| 5 | `nova-redis acl render --json` | `acl render` refuses `--json` with `unknown flag --json; acl render takes no flags`, denying structured output for policy inspection. | Support `--json` flag on acl render to output parsed ACL families and user definitions as a structured JSON object. | S |
| 6 | `nova-redis recall -h` | Operational commands such as recall, fn load, fn check, and serve offer no `--dry-run` flag and reject in-memory addresses, preventing store-free validation. | Implement `--dry-run` mode on read and verification verbs to enable store-free schema and argument checking. | M |

## Good, keep
Aggregated flag validation that reports every missing required flag and invalid flag simultaneously in text and JSON output.
Store-free planning in `spill --dry-run` and `spill --dry-run --json` computing keys, payload sizes, and expiry timestamps without network access.
Clean ACL policy generation with `acl render` producing complete permission sets and library checksums without contacting a store.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| acl apply's remedy names one of four missing users | STILL THERE | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` reports four missing users but remedy only suggests `--password-env-for coordinator=<VARIABLE>` |
| recall hex-escapes the value | STILL THERE | `nova-redis recall -h` provides no flag to disable hex escaping, and binary symbols retain `\x%02x` byte formatting |
| --addr refuses the socket nova-table's first run makes | STILL THERE | `nova-redis spill --dry-run --addr /tmp/redis.sock --owner worker --name note --ttl 10m --value hi` prints `SPILL REFUSED: --addr "/tmp/redis.sock" is not <host:port>; refusing to guess; run: nova-redis help` |
| another 10: bounded store-free plans and aggregated JSON refusals | FIXED | `nova-redis spill --json` aggregates all missing flags in why array, and `spill --dry-run` produces bounded plans store-free |
