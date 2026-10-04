# nova-redis USE rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash
Build: 0c5803c2de40
Score: 6/10

## Reasons

This rates the USE of nova-redis at 0c5803c2de40 (full SHA 0c5803c2de406c1b0b2b0841f579c9bf73406b1c); the staged checkout's HEAD is that same commit, and every binary came from it. The banner is strong and honest: line 1 says what the tool does, the how-it-works paragraph names serve, spill, recall and fn, and the first run says plainly that the spill and recall lines need a Redis at 127.0.0.1:6379. Refusals are the best part: one run reports every missing input (nova-redis spill names --addr, --value, --owner, --name and --ttl together), an unknown flag names the flag set and the nearest (did you mean --name?), an unknown verb names the verb set and the nearest (spell did you mean spill?), and a bad value names itself. The store-free subset works and is honest: spill --dry-run prints key, ttl, expires, bytes, written=0 and dry_run=true, and its --json is the same value; acl render lists the four users and the function library without a store. A 10 needs the dry run to be safe everywhere: acl apply --dry-run is advertised but ignored, so the verb dials the store, fails when none answers, and then warns it may have written; a 10 also needs the effect line to tell the truth for store writes, structured output on the store-free acl render, and a runnable next command when a store is unreachable. No store and no disposable functional container are assigned and a redis-server may not be started here, so the real paths of spill, recall, fn load, fn check, acl check and acl apply are judged from their help and their refusals only; those verbs are partial USE coverage, not an end-to-end success.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | the advertised dry run is ignored: the verb dials the store, fails when none answers, prints ACL-APPLY FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written, and exits 1, while its help promises it prints what the verb would write and writes nothing | set the call's DryRun so the plan is computed without dialling and the self-warning never fires | L |
| 2 | `nova-redis spill -h` | the effect says local write: writes files on this machine, but spill writes to the Redis store; fn load and acl apply carry the same wrong label | label the store-writing verbs as a store write and keep local write for serve | M |
| 3 | `nova-redis acl render` | the one store-free listing refuses --json and --max (unknown flag --json; acl render takes no flags) although the banner says a listing takes --max and the standard gives every verb --json | accept --json on acl render, acl check and acl apply and --max on acl render | M |
| 4 | `nova-redis recall --addr 127.0.0.1:6379 --owner ada --name note` | the plain-line refusal escapes the text as hex, err=redis\x20at\x20127.0.0.1:6379\x20as\x20the\x20default\x20user, so the line is unreadable while the same refusal's --json shows normal text | render the error with the plain oneline quoting, as acl check and fn check already do | S |
| 5 | `nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi` | the store-unreachable refusal names no runnable next command: its remedy is run: nova-redis help and the text says start the store without saying how | print the concrete start or check command, as the fn check remedy does | S |
| 6 | `nova-redis serve -h` | serve is a local write that creates the store directory and its files but offers no --dry-run, so an AI cannot see the plan it would take | add --dry-run printing the bind, port and dir it would create, or say in the help why not | S |
| 7 | `nova-redis spill -h` | the write verbs take no --op id, so a retry after a lost reply cannot be made idempotent | add --op to spill and fn load through the shared skeleton | S |

## Good, keep

- The refusal grammar aggregates every missing input in one run and names the nearest flag or verb, so recovery is one turn.
- The store-free spill --dry-run and acl render run from the binary alone and are honest about what they did.
- The banner's first run is a runnable example block and says plainly which lines need a store.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| acl apply's remedy names one of four missing users | STILL THERE | cmd/nova-redis/acl.go:336 at this snapshot still builds the remedy from unsourced[0] alone, so a store that answers names one missing user where several are |
| recall hex-escapes the value | STILL THERE | cmd/nova-redis/main.go:261 at this snapshot still returns the value as a plain Fact, and internal/tool/out.go:260 renders a plain value through oneline.Field, which hex-escapes its spaces |
| --addr refuses the socket nova-table's first run makes | STILL THERE | `nova-redis spill --dry-run --addr unix:///tmp/novaredis.sock --owner ada --name note --ttl 10m --value hi` prints SPILL REFUSED: --addr "unix:///tmp/novaredis.sock" needs a port from 1 to 65535 |
