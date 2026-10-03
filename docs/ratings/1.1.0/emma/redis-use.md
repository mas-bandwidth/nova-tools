# nova-redis USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8.5/10

## Reasons
The tool offers robust store-free dry runs and comprehensive error reporting. The usage banner provides a clear example sequence that executes cleanly without infrastructure. Two small jobs (store-free scratch planning and fleet ACL rendering) executed end to end with consistent status reporting and complete structured JSON envelopes.

Four refusals were provoked and verified: missing required flags were reported all at once with full descriptions and remedies, unknown flags enumerated all valid options, unknown verbs listed all available verbs, and multiple bad parameter values were diagnosed together in one turn. Under --json, refusals aggregated every defect into a structured why array with status refused and exit 2.

Commands requiring a live server (serve, live spill, recall, fn load, fn check, live acl check and apply) were not run, in compliance with the rule prohibiting starting a server on this machine; these verbs were evaluated through their help documentation and dry-run modes.

The score stopped short of 10 because acl apply remedy mentions only the first unsourced user when several are missing, --addr refuses Unix domain socket paths accepted by sibling tools, and recall hex-escapes whitespace in values on line output.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis acl apply --addr 127.0.0.1:6379` | When multiple users lack password environment sources the remedy names only the first user rather than all missing users | Format all unsourced users in the remedy command flags | S |
| 2 | `nova-redis spill --addr /var/run/redis.sock` | The --addr parser requires host:port and refuses Unix domain socket paths used by sibling tools | Allow absolute filesystem paths as socket addresses in validAddr | S |
| 3 | `nova-redis recall --addr 127.0.0.1:6379 --owner trial --name note` | Line output renders string values via oneline.Field which hex-escapes whitespace characters | Format value with oneline.Quote or print raw string in a trailing positional slot | S |
| 4 | `nova-redis fn load --dry-run` | The fn load and fn check verbs offer no dry-run flag to inspect embedded function library metadata without dialing | Add --dry-run support to fn load and fn check to preview library digest and registration plan | M |
| 5 | `nova-redis spill` | Refusal remedies suggest running general help instead of verb-specific flag options | Point refusal remedy to nova-redis help spill or spill -h | S |

## Good, keep
Store-free dry-run mode on spill that calculates expiry timestamps and byte counts without dialing the store.
Deterministic acl render output generating complete pasteable ACL SETUSER rules from embedded function metadata.
Structured JSON refusal reporting that aggregates all independent parameter errors into a why array in one turn.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| acl apply's remedy names one of four missing users | STILL THERE | cmd/nova-redis/acl.go:306 formats unsourced[0] only |
| recall hex-escapes the value | STILL THERE | cmd/nova-redis/main.go:406 formats value with oneline.Field |
| --addr refuses the socket nova-table's first run makes | STILL THERE | nova-redis spill --addr /var/run/redis.sock exits 2 with not host:port |
| bounded store-free plans and aggregated JSON refusals | STILL THERE | `nova-redis spill --dry-run` and `nova-redis spill --json` |
