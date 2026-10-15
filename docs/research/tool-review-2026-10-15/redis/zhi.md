# nova-redis AI-usability review, 2026-10-15

Reviewer: Claude Sonnet 5.5
Build: c0cf1f631b0e
Score: 7/10 (38/55)

Build is the checkout head, built with `go build -o bin/ ./cmd/...`; every probe ran in a scratch directory against a throwaway store on a Unix socket (`$S`) that was started by hand from the help's own recipe and shut down after. No serve, spill, recall or load touched a live instance.

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 3 | `nova-redis help` -> `first run: --dry-run needs no store; the throwaway recipe below starts a store on a socket.` (the recipe works and the first spill and recall run from it; but the examples use the retired `--addr`, and the claimed store-free dry-run is refused) |
| 2 | refusals | 3 | `nova-redis spill --redis 127.0.0.1:1 --owner o --name n --ttl banana --value hi` -> `SPILL REFUSED: --ttl "banana" is not a duration (try 10m); run: nova-redis help` (cause named, every problem at once on a many-fault call, but the next command is the generic `nova-redis help`, and a missing --redis is reported as `--addr is required`) |
| 3 | output | 3 | `nova-redis recall --json --redis $S --owner o --name n` -> `{"result":{"verb":"recall","status":"ok","exit":0},"facts":{"key":"o:n","bytes":2,"value":"hi"}}` (spill, recall, version have --json and one grammar; fn load, fn check, acl check, acl apply have no --json, and their line heads vary: `OK`, `LOADED`, `MISSING`, `ACL CHECK DRIFT`) |
| 4 | dry-run | 3 | `nova-redis spill --dry-run --redis $S ...` -> `SPILL OK key=o:n ttl=10m0s ... written=0 dry_run=true` (spill, serve, acl apply, install and uninstall have it and write nothing; `fn load` writes functions into the store and has no --dry-run) |
| 5 | idempotent | 2 | `nova-redis spill --redis $S --owner o --name n --ttl 10m --value hi` run twice -> `SPILL OK key=o:n ... expires=...` both times (no --op: the second write silently overwrites and restarts the expiry; fn load, apply and install are naturally repeatable, but none says so) |
| 6 | no prompts | 5 | `nova-redis serve` with nothing -> `SERVE REFUSED: --bind is required: ...` (every verb returned at once; none waited on input) |
| 7 | exit codes | 4 | `nova-redis recall --redis $S --owner o --name missing` -> `RECALL MISSING key=o:missing` exit 1; `fn check` on an empty store `MISSING nova_sprint ...` exit 1; unreachable store exit 2; refusals exit 2 (all as the help states; the one miss is a recall of a key that holds the wrong type, see blocker 5) |
| 8 | one word | 3 | `nova-redis spill -h` -> `--addr <string>  the old spelling of --redis, kept for one release` (two names for one flag live in every verb, and the refusals and the help examples still speak the old one) |
| 9 | push | 5 | n/a: no verb waits on state; every verb is one request and one answer |
| 10 | no leaks | 5 | `NOVA_REDIS_PASSWORD=sekrit nova-redis recall --redis 127.0.0.1:1 --owner o --name b` -> `... as the default user (password from NOVA_REDIS_PASSWORD): unreachable: ...` (the variable name is shown, never its value) |
| 11 | true help | 2 | `nova-redis spill -h` -> `effect: local write: writes files on this machine` (spill writes a key into a store, not a file; the same false line is on fn load; `--dry-run needs no store` and the `--max` line are also untrue, see blockers) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis spill --dry-run --owner o --name n --ttl 10m --value hi` | Help says `first run: --dry-run needs no store`, yet the call exits 2 with `SPILL REFUSED: --addr is required: the store's address as <host:port> ... (no default)`; a new AI cannot take the advertised first step | Let spill --dry-run run with no --redis (print `store=none`), or change the help line to say the dry run needs --redis | S |
| 2 | `nova-redis recall`, `nova-redis spill`, `nova-redis fn check`, `nova-redis acl check` with no store flag | Refusals say `--addr is required` and the unknown-flag line lists `--addr` among the flags, while help names `--redis` and calls --addr `the old spelling`; the retired name is the one an AI is told to type | Name `--redis` in every refusal and drop the retired flag from the flag list in unknown-flag refusals | S |
| 3 | `nova-redis help` (the example block) | The three examples use `--addr 127.0.0.1:6379`; copying them prints `SPILL NOTE --addr is --redis` on every run | Write the examples with `--redis` | S |
| 4 | `nova-redis spill -h`, `nova-redis fn load -h` | The last line reads `effect: local write: writes files on this machine`; both write to a store over the network (and `serve` really does write files), so the effect class an AI plans by is wrong | Give spill and fn load the effect class for a remote store write | S |
| 5 | `redis-cli -s $S set o:k v` then `nova-redis recall --redis $S --owner o --name k` | Help says a recall of an unbounded key is a NO; the run gives `RECALL FAILED key=o:k class=other: ... WRONGTYPE Operation against a key holding the wrong kind of value; next: the store answered, so the connection stands: read the refusal as the command's own`, which names no next command and does not say what spill stores | Map wrong-type and no-expiry keys to `RECALL UNBOUNDED key=...` with the next command, and say in `recall -h` which type spill writes | S |
| 6 | `nova-redis fn load --redis $S` | A write into the store with no --dry-run (the only store write that lacks one); an AI cannot see what it would change before it does it | Add `--dry-run` to fn load, printing the library sha it would load and the one now there | M |
| 7 | `nova-redis install store --dry-run` | Refusal ends `INSTALL-STORE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written`, beside `nothing was written` in the line above: internal jargon and a contradiction | Drop the NOTE when the refusal came before any effect, and never print a field name | S |
| 8 | `nova-redis help` | `A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest` but no verb lists and none takes --max | Delete the sentence | S |
| 9 | `nova-redis fn check --json --redis $S` | `FN-CHECK REFUSED: unknown flag --json`; the same for acl check and acl apply, so the verbs a check script calls are the ones with only a text line, and the line heads differ (`OK`, `MISSING`, `FAILED`, `ACL CHECK DRIFT`) | Give fn load, fn check, acl check and acl apply `--json` in the same envelope as spill | M |
| 10 | `nova-redis spill -h`, `nova-redis fn -h` (any verb) | The 450-character exit-codes paragraph is repeated verbatim under every verb, with spill and serve cases in `fn` help, burying the verb's own flags | Print only the exit codes that verb can return | S |
| 11 | `nova-redis acl apply --dry-run --redis 127.0.0.1:1` | With nothing listening it prints `ACL APPLY OK dry-run=true ... would=4` and exits 0, claiming four users would be set on a store it never reached | Say `store=unchecked` on the line, or dial and report the real difference | S |
| 12 | `nova-redis serve --dry-run --bind 127.0.0.1 --port 6399 --dir /tmp/x` (where /tmp/x is a file) | `SERVE REFUSED: the ACL file "/tmp/x/users.acl": lstat /tmp/x/users.acl: not a directory`; it blames the ACL file and never says --dir is not a directory | Check --dir first and say `--dir "/tmp/x" is not a directory` | S |
| 13 | `nova-redis spill --redis $S --owner o --name n --ttl 10m --value hi` (twice) | The second run overwrites and restarts the expiry with the same `SPILL OK` line; a retrying AI cannot tell a first write from a repeat | Add an `--op <id>` that makes a repeat a no-op and say `repeat=true` | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | every `REFUSED` line | The closing `run: nova-redis help` sends the AI to the whole 40-line help, not to the verb | End with `run: nova-redis <verb> -h` (the unknown-flag refusals already do) | S |
| 2 | `nova-redis acl render` | Prints about 8 KB of ACL commands on four lines, no `--json`, no way to show one user | Add `--user <name>` to render one | M |
| 3 | `nova-redis recall` | A value cannot be read without a text parse of `value=`; an empty value prints `value=-` | Add `--raw` printing only the value | S |
| 4 | `nova-redis version` | Prints `nova-redis devel linux/amd64 go1.26.6` for a source build: no commit | Print the build's commit when built from a checkout | S |
| 5 | `nova-redis acl check` against a bare store | Prints `NOTE ACL DEFAULT on=true nopass=true` without saying that is the unsafe state | Say what to run to close it | S |

## Top 3 fixes
1. Make the advertised first step work: spill --dry-run needs no --redis, or the help stops saying so (blocker 1), and use `--redis`, not `--addr`, in every refusal and example (blockers 2 and 3).
2. Make the help tell the truth about each verb: the effect line, the `--max` sentence, and the per-verb exit codes (blockers 4, 8 and 10).
3. Give the check and load verbs the same `--json` envelope and a `--dry-run` for fn load, so a script reads every result one way (blockers 9 and 6).

## Good, keep
The refusals that dial the store (`class=unreachable ... next: start the store or correct the address`) and the `remedy="nova-redis fn load --redis ... puts this binary's library on the store"` on a MISSING check are the model every refusal should follow.
Spill requires a TTL and an owner prefix, so a key can never be unbounded or unowned, and a many-fault call reports every fault in one run.
`--json` carries the same exit code and a `remedy` in the refusal object; every dry-run printed `written=0` and wrote nothing; no verb ever prompts.

## What could not be tried
`nova-redis serve` (live), `install store` and `install bus` (judged from `--dry-run`, which printed the unit and wrote nothing), `uninstall store` and `uninstall bus` (dry-run only, against a scratch directory), `acl apply` for real (it sets users on the store; judged from its dry-run), and any login with a password or an ACL user (no secret was used; only the refusal for an empty password variable was provoked). A second `fn load` against an already loaded store was not run.
