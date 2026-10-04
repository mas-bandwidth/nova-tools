# nova-config USE rating, nova-tools 1.1.0

Rater: opencode
Build: bd7949b97aec
Score: 8/10

## Reasons
Rated cold from the binary's help and runs alone, everything against the --file store the help offers, nothing real touched (no PostgreSQL, no Redis, no network). The example block's five lines run as pasted, in order, each exit 0 with a one-line result (CONFIG MIGRATE applied=26, CONFIG ADD rev=1, CONFIG SET changed=width, CONFIG LIST rows=1, CONFIG HISTORY changes=2); that block is the whole onboarding. Two real jobs then run end to end with no database: a fleet's config built and revised (machine add/set, friend add/set, fleet set, route add, tier set, list/show/history, remove), and the deal (route plus tier) read back through list and show. Nine provoked refusals — missing required flags (all four named at once, each with what it wants), unknown flag (the valid flags listed), unknown verb (the valid verbs listed), bad value twice, flag exclusivity, a store that does not answer, a referential remove, self --check — are each one line on stderr with stdout clean, ending in a runnable next command; self --check interpolates the actual --file path into its fix, and the apply refusal at a dead target names the operation that was dialling. --file makes the seven-kind surface rehearsable with no store; inventory --fixture prints a full Ansible inventory with no Redis; --json success output is one object (result/facts/items) whose facts mirror the printed lines; every write leaves a history row with actor and diff. An 8 and not a 10: the --json contract breaks on every refusal (finding 1), the delivery verb cannot be rehearsed offline (finding 2), and three wording and noise gaps (findings 3-5). A 10 needs refusals in the --json shape (or --json's help saying they stay plain on stderr), an apply --dry-run that prints its ADD/SET/REMOVE plan from --file alone, no library dial noise beside the refusal, value refusals that name the unit, and a remove remedy that names the unblocking command.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine remove m2 --as a11 --json --file try.json` | exit 1, stdout 0 bytes, the refusal plain on stderr; --json's help says "print one JSON object (internal/tool's result shape) instead of the lines", but every refusal (exit 1 and exit 2, checked on friend set too) prints no object, so an AI reading stdout as JSON sees an empty success-shaped stream and must switch shape mid-job | print refusals in the result shape under --json (result.status refused, facts carrying the problems), or make --json's help say refusals stay plain on stderr and stdout stays empty | M |
| 2 | `nova-config apply --file try.json --dry-run` | the one verb that delivers to Redis cannot be rehearsed offline: --dry-run still refuses without --redis (its help says "it still reads the store and Redis"), so from --file alone an AI never sees the ADD/SET/REMOVE plan the help promises | let apply --dry-run print the planned lines from the --pg or --file source when --redis is absent, marking the Redis diff as unknown | M |
| 3 | `nova-config apply --dry-run --redis 127.0.0.1:6399 --file try.json` | before the refusal, 3 log lines from the Redis client's dialer (pool.go, "failed to dial after 5 attempts") land on stderr, against the two-line ceiling: noise an AI parses for nothing | silence the library's dial logs and keep the one refusal line | S |
| 4 | `nova-config route add r1 --tier flash --provider z --model m-flash --deadline 45m --as a5 --file try.json` | the banner sells "deadline" and the refusal says "want a non-negative integer", but not the unit; the seconds are one -h away, so the cold guess 45m fails the dry-run and the write | add the unit to the refusal: "want a non-negative integer: the seconds" | S |
| 5 | `nova-config machine remove m1 --as a7 --file try.json` | the remedy "run: nova-config machine list --file try.json" names a read that does not unblock; the actual unblock (fleet set --store to another machine, found by guess and try) is not named | make the remedy the unblocking command: fleet set --store <machine>, then remove | S |

## Good, keep
The example block runs as pasted and is the whole onboarding: five lines, exit 0 each, from migrate to history, against --file alone.
Every refusal is one stderr line naming every problem at once with a runnable next command; help <verb> and -h are identical, on stdout, exit 0, before anything is dialled.
--file plus inventory --fixture means the whole surface, apply's plan excepted, rehearses with no store at all; history rows carry actor and diff.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| every refusal under --json leaves stdout empty (2026-10-02, 7/10 at 1aac13259) | STILL THERE | `nova-config machine remove m2 --as a11 --json --file try.json`: exit 1, 0 bytes on stdout, the line on stderr |
| --json refusals escape as plain stderr (2026-10-02, 8.5/10 at 1aac13259) | STILL THERE | the same run's stderr is the plain line `nova-config machine remove REFUSED: machine m2 is the --store of the fleet; run: nova-config machine list --file try.json`, no JSON object |
| apply --dry-run passes where apply refuses (2026-10-02, 7/10 at 1aac13259) | FIXED | `nova-config apply --file try.json --dry-run` refuses `--redis is required` at exit 2, and `nova-config apply --as a11 --file try.json` refuses the same way, so the rehearsal no longer passes where the delivery refuses |
