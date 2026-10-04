# nova-redis USE rating, nova-tools 1.1.0

Rater: GLM (glm-5.3-flash via opencode), a cold rater
Build: bf50a8a1e53d
Score: 8/10

## Reasons
Judged from the binary and its help alone, every command run in a scratch directory. The help does nearly everything right: `nova-redis help` states how it works, what the first run needs, one usage line per verb, the `--json` rule (which verbs take it, which do not) and exit codes 0/1/2 spelled out per outcome; `nova-redis help spill` and `nova-redis spill -h` are identical and list every flag with its meaning and default plus an effect line. Refusals are the best part: `nova-redis spill --addr 127.0.0.1:6379` with owner, name, ttl and value all missing prints four refusals at once, each naming its problem and ending with the next command to run; an unknown flag lists the flags the verb does take; an unknown verb lists every verb; a bad value says what a good one looks like (`--ttl "5x" is not a duration (try 10m)`). `--json` gives the same facts as one stable object. A first successful run needs no store (`spill --dry-run`), and a small real job, spill then recall of one bounded key, ran end to end and read back the value it wrote. The score is 8 and not 10 for one trust defect: `acl apply` advertises `--dry-run` in its usage line and flag list, but the verb never reads it, and the tool itself says so and warns it may have written; an AI cannot rely on the plan the help promises. A 10 would also need the remedy for a refused apply to name every user it wants password variables for, and an effect line for spill that says what spill really writes to. Not tried: `serve` (no server may start here), `fn load` and a real `acl apply` (both would write to a store this rater does not own); spill and recall ran for real, with one expiring key, against the loopback store this machine already had listening, and `fn check` and `acl check` ran read-only there.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | The help offers `--dry-run` for acl apply and says `--dry-run` prints what the verb would write and writes nothing, but the verb never reads the flag: the run ends `ACL-APPLY FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` with exit 1, so no plan is printed and an AI is told the store may have been touched | Either read the flag and print the plan the help promises, or drop `--dry-run` from the usage line and flag list for acl apply | M |
| 2 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | The refusal lists four users it lacks (`missing=coordinator,bench,ns-table,ns-friend`) but the remedy names a password variable for one of them only (`--password-env-for coordinator=<VARIABLE>`), so an AI must guess that the other three take the same form | Name the flag for every user in the remedy, or say the flag repeats | S |
| 3 | `nova-redis help spill` | The effect line says `local write: writes files on this machine`, but spill writes one expiring value to the store at `--addr`, which the same help line says is any `<host:port>`; an AI reading the effect line expects local files to change | Make the effect line name the store the verb writes to, as the help's how-it-works already does | S |

## Good, keep
- Per-verb help (`help <verb>`, `<verb> -h`) lists every flag with meaning, default and an effect line, and repeats the exit codes; it answers questions before they are asked.
- Refusals name every problem at once and end with the next command to run; nothing is refused with a bare error.
- The store-free first run (`spill --dry-run`) and a bounded, expiring key model make the tool safe to try.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| acl apply's remedy names one of four missing users (2026-10-02) | STILL THERE | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` ends `run: nova-redis acl apply --addr 127.0.0.1:6379 --password-env-for coordinator=<VARIABLE>` with four users missing |
| recall hex-escapes the value (2026-10-02) | CHANGED | a printable value comes back plain (`RECALL OK key=probe:check bytes=5 value=hello`); a tab comes back as `value=a\x09b` on the line, and `--json` carries it exactly (`"value":"a\tb"`) |
| --addr refuses the socket nova-table's first run makes (2026-10-02) | STILL THERE | `nova-redis recall --addr nt.sock` prints `RECALL REFUSED: --addr "nt.sock" is not <host:port>; refusing to guess; run: nova-redis help` |
| bounded store-free plans and aggregated refusals (2026-10-02) | STILL THERE | `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner probe --name check --ttl 10m --value hi` prints one plan line with `dry_run=true`; `nova-redis spill --json --addr 127.0.0.1:6379` answers one object whose `why` array carries all four missing flags |
