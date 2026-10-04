# nova-redis USE rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: bc60d1f260ea
Score: 8/10

## Reasons

Cold: judged from the help alone (`nova-redis help`, `nova-redis help <verb>`, `<verb> -h`), no source read before this score. No store may run on this machine, so serve, the real spill write, recall, fn load, fn check, acl check and acl apply were judged from their help, their --dry-run and their dead-store refusals; the runs that succeeded store-free were spill --dry-run, acl render and version. mem: is not an address form here: --addr takes host:port only, and a mem: guess is refused as a hostname lookup.

Two jobs ran end to end with no store. Planning a spill: `nova-redis help` then the plan command, no wrong guess. Rendering the ACL inventory: `nova-redis help acl` then `nova-redis acl render`, no wrong guess. The wrong guesses all came from outside this tool's help: a mem: address (refused as a hostname), the socket path the sibling table tool's recipe makes (finding 2), a two-word verb passed as one argument (finding 3), and --max on recall (finding 4).

What earns the 8: the help answers before any store exists — the first-run line names the one command that needs no store, every verb has its own help with flags, rules and exit codes, and the exit codes are documented and then honored exactly (0 done, 1 ran and said NO, 2 could not run; a store that does not answer is 2 every time but finding 1). Refusals name the problem, every problem at once (five missing flags in one refusal), quote the rule each one broke, and end with the next command to run. --json mirrors the line output, refusals included, as one object that can be acted on without guessing. spill --dry-run is a bounded store-free plan (key, ttl, expiry, bytes, store, written=0), serve validates bind, port and dir before starting anything and refuses a public bind outright, acl render is a full store-free inventory, and version embeds the exact build sha, so the binary answers for the head it was built from.

What costs it: acl apply --dry-run is not a dry run — it dialled the store and then reported that the verb never read the flag and may have written (finding 1); --addr still refuses the socket form the sibling table tool's first-run recipe makes (finding 2); and three smaller confusions (findings 3 to 5). A 10 would need acl apply to plan without writing, --addr to take the socket path, and the smaller confusions gone.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | --dry-run is not a dry run: the verb dialled the store, failed unreachable, then printed `ACL-APPLY FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` and exited 1, where the same command without --dry-run exits 2; the help promises print what the verb would write and write nothing, so an AI planning an ACL change may apply it for real | read the dry-run flag in the apply path and print the SETUSER plan instead of sending it, keeping the dead-store exit at 2 | M |
| 2 | `nova-redis spill --dry-run --addr /tmp/throwaway/redis.sock --owner worker7 --name note --ttl 10m --value hi` | --addr refuses the socket form the sibling table tool's own first-run recipe makes ($d/redis.sock): `SPILL REFUSED: --addr "/tmp/throwaway/redis.sock" is not <host:port>`, so the store that recipe starts cannot be pointed at from this tool | accept a unix socket path in --addr and say so in its help, matching the sibling tool's --redis | M |
| 3 | `nova-redis "fn load" -h` | a two-word verb passed as one argument is refused with a self-referential remedy: `unknown verb "fn load"; did you mean fn load?` — nothing says the verb is two arguments, while the group's flag refusals point at `run: nova-redis fn load -h`, which works only when unquoted | match the joined form, or have the remedy say the verb is two arguments | S |
| 4 | `nova-redis recall --max 5 --addr 127.0.0.1:6379 --owner w --name n` | the top help promises --max for a verb that lists, but no verb of this tool lists: the probe is refused `unknown flag --max; the flags of recall are --addr, --json, --name, --owner, --password-env, --user`, after the help sent the reader hunting for a flag that exists nowhere here | drop the --max sentence from this tool's help, or name the verbs it means | S |
| 5 | `nova-redis recall --addr 127.0.0.1:6379 --owner worker7 --name note` | recall has no --dry-run, so the read path cannot be planned store-free: with no store the only answer is `RECALL REFUSED key=worker7:note class=unreachable`, and what recall prints for a value stays a guess until a store exists | offer recall --dry-run printing the key, the store and the login it would use | S |

## Good, keep

spill --dry-run is a bounded store-free plan: key, ttl, expiry, bytes, store and written=0 in one line, the same facts as one JSON object.
Refusals name every problem at once, quote the rule each one broke, and end with the next command to run, in lines and in --json alike.
serve validates bind, port and dir before starting anything, and refuses a public bind outright.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| acl apply's remedy names one of four missing users | STILL THERE | cold: the missing-user remedy needs a live store, which this rating never starts; `nova-redis acl apply --addr 127.0.0.1:6379` on a dead store answers `ACL APPLY FAILED store=127.0.0.1:6379 err=redis at 127.0.0.1:6379 as the default user, no password: unreachable: dial tcp 127.0.0.1:6379: connect: connection refused` with a login remedy, and `nova-redis acl render` answers `ACL RENDER OK users=4 functions=41 library=11dc308260975247`, so the partial remedy could not be re-seen |
| recall hex-escapes the value | STILL THERE | cold: recall was not tryable without a store, so the value's shape was judged from the help only; `nova-redis recall --addr 127.0.0.1:6379 --owner worker7 --name note` answers `RECALL REFUSED key=worker7:note class=unreachable: ... connection refused` |
| --addr refuses the socket nova-table's first run makes | STILL THERE | `nova-redis spill --dry-run --addr /tmp/throwaway/redis.sock --owner worker7 --name note --ttl 10m --value hi` answers `SPILL REFUSED: --addr "/tmp/throwaway/redis.sock" is not <host:port>; refusing to guess; run: nova-redis help` and exits 2 |
