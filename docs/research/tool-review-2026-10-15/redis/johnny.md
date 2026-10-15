# nova-redis AI-usability review, 2026-10-15

Reviewer: Claude Sonnet 5.5
Build: c0cf1f631b0e
Score: 7.5/10 (40/55)

All probes ran on a throwaway store started by hand from the help's recipe on a Unix socket in the job's scratch directory (once with a requirepass for the login probes), shut down after; no live store was touched. `$S` below is that socket.

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-redis spill --redis $S --owner ada --name note --ttl 10m --value hi` then `recall` -> `RECALL OK key=ada:note bytes=2 value=hi`; the recipe works from help alone, but the help's own examples use the deprecated `--addr`, and the dry-run claim and the `--max` claim are false |
| 2 | refusals | 3 | `nova-redis spill --owner ada --name n --ttl 10m --value hi` -> `SPILL REFUSED: --addr is required: the store's address ...; run: nova-redis help` (names a flag the usage line does not show, and the next command is the generic help, not `spill -h`); the unknown flag, unknown verb, bad ttl and unreachable store refusals do name cause and fix |
| 3 | output | 4 | `nova-redis recall --json --redis $S --owner ada --name note` -> `{"result":{"verb":"recall","status":"ok","exit":0},"facts":{"key":"ada:note","bytes":2,"value":"hi"}}`; the line grammar is stable, but `fn check` and `acl check` have no --json and an empty value prints as `value=-`, the same as the one-character value `-` |
| 4 | dry-run | 3 | `nova-redis spill --dry-run --owner ada --name note --ttl 10m --value hi` -> `SPILL REFUSED: --addr is required`; help says `--dry-run needs no store`, yet spill's dry-run refuses without an address; with an address it writes nothing (`written=0 dry_run=true`) and serve, acl apply, install and uninstall all have one |
| 5 | idempotent | 3 | `nova-redis spill ... --value hi` run twice -> `SPILL OK key=ada:note ttl=10m0s expires=...` both times; spill takes no --op, so a repeat rewrites the value and restarts the expiry; `fn load` run twice is idempotent (`UNCHANGED nova_sprint sha=3ff536374b59b81e`) |
| 6 | no prompts | 5 | `nova-redis fn load --redis $S` -> `LOADED nova_sprint ...`; no verb asked a question, every missing input was refused at once |
| 7 | exit codes | 5 | `nova-redis recall --redis $S --owner bob --name gone` -> `RECALL MISSING key=bob:gone` with exit 1; usage errors and an unreachable or refusing store exit 2, a stale or missing library exits 1, all as the help states |
| 8 | one word | 3 | `nova-redis spill --addr $S --owner c --name n --ttl 5m --value x` -> `SPILL NOTE --addr is --redis`; two spellings of the address live side by side, the refusals still say `--addr`, and the help's examples teach the old one |
| 9 | push | 5 | n/a: nova-redis waits on nothing; each verb is one request to the store and then returns |
| 10 | no leaks | 3 | `nova-redis recall --redis $S --password-env zzTOPSECRETzz --owner ada --name n` -> `RECALL REFUSED: --password-env "zzTOPSECRETzz" is not a variable name`; a password pasted into the flag that wants a variable name is echoed back; auth failures themselves print no password |
| 11 | true help | 2 | `nova-redis spill -h` -> `effect: local write: writes files on this machine` (spill writes a key to the store, not files); `Every verb but serve ... takes --json` and `A verb that lists takes --max <n>` -> no verb lists, and `spill --max 3` is `unknown flag --max`; `--dry-run needs no store` is false for spill |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis spill --owner ada --name n --ttl 10m --value hi` (also `recall`, `fn load`, `fn check`, `acl check`, `acl apply` with no address) | `SPILL REFUSED: --addr is required: the store's address as <host:port> ... run: nova-redis help`; the usage line shows only `--redis`, so the refusal names a flag the AI was never told to give, and the next command is the generic help | name `--redis` in the refusal (and its message text), and end it with the verb's own `run: nova-redis spill -h` | S |
| 2 | `nova-redis help` (the `example:` block) and `nova-redis help spill` | the examples are `nova-redis spill --dry-run --addr 127.0.0.1:6379 ...`; the same help calls `--addr` "the old spelling ... kept for one release", and running it prints `SPILL NOTE --addr is --redis` | write the examples with `--redis` | S |
| 3 | `nova-redis spill --dry-run --owner ada --name note --ttl 10m --value hi` | help says `first run: --dry-run needs no store`; the run says `SPILL REFUSED: --addr is required`, so the first command a new AI tries from the help fails | let spill's dry-run run with no address (print `store=-`), or change the help line to say the dry-run still needs `--redis` | S |
| 4 | `nova-redis spill -h` | `effect: local write: writes files on this machine`; spill writes a key with an expiry to the store over the socket, and `help recall` calls itself an inspection | correct the effect line for spill (a write to the named store) | S |
| 5 | `nova-redis help` (the line `Every verb but serve, fn load ... takes --json ... A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest`) | no verb in the tool lists, so `--max` and `MORE` describe nothing; `nova-redis spill --redis $S ... --max 3` -> `unknown flag --max`; the same line also leaves out `version --json`, which works, and `acl render` reads as having --json though it refuses it | delete the `--max`/`MORE` sentence and list the verbs that take --json by name | S |
| 6 | `nova-redis recall --redis $S --password-env zzTOPSECRETzz --owner ada --name n` | `RECALL REFUSED: --password-env "zzTOPSECRETzz" is not a variable name`; a password given by mistake to the flag is printed in the error, and so lands in a log | refuse with `--password-env wants the NAME of a variable, not a value` and do not echo the value | S |
| 7 | `nova-redis recall --redis $S --owner bob --name other` | an empty value prints `value=-`, and a stored `-` also prints `value=-`; a script cannot tell the two apart (`--json` can) | print an empty value as `value=` or add `empty=true`, and say which in the help | S |
| 8 | `nova-redis spill --redis $S --owner ada --name note --ttl 10m --value hi` (run twice) | the second run answers `SPILL OK` with no sign that the key already held a value; help promises no --op and no idempotent repeat, and the repeat resets the expiry | have spill name `replaced=true` when the key existed (and `--json` carry it), and state in the help that a repeat restarts the expiry | S |
| 9 | `nova-redis fn check --redis $S --json` and `nova-redis acl check --redis $S --json` | `FN-CHECK REFUSED: unknown flag --json`; the two read verbs an AI runs to judge a store have no parseable form, only prose lines with `remedy="..."` | give `fn check` and `acl check` the same `--json` object the other read verbs print | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis acl render` | prints several KB of ACL lines at once, with no way to name one user or family | `acl render --user <name>` to print one line | S |
| 2 | `nova-redis spill` | cannot read a value from a file or stdin, so a long or multi-line value must go through the command line | `--value-file` or `--value -` | M |
| 3 | `nova-redis recall` | a plain recall escapes a blank as `\x20` and a newline as `\x0a` (`value=a\x20b\x0ac`) while `--json` prints the real text | state the escape rule in `help recall` | S |
| 4 | `nova-redis` | no verb lists or deletes the keys under one owner, so an AI that spilled a value cannot see what it left | a read-only `list --owner` | M |
| 5 | `nova-redis help`, `nova-redis fn -h` | the exit-code paragraph (a long one) is repeated unchanged in every verb's help, burying the verb's own lines | print only the codes that verb can return | S |

## Top 3 fixes
1. Make every "address is required" refusal name `--redis` and the verb's own `-h`, and write the help's examples with `--redis` (blockers 1 and 2): the first thing a cold AI types fails with a flag it was never shown.
2. Make the help true: the dry-run needs no store, spill's effect line, and the `--max`/`MORE`/`--json` sentence (blockers 3, 4 and 5).
3. Stop echoing a pasted password and give `fn check` and `acl check` a `--json` form (blockers 6 and 9).

## Good, keep
Every refusal exits 2 within one line prefixed `<VERB> REFUSED`, lists every missing flag at once, and an unknown flag lists the valid ones and the verb's own help. The auth and unreachable-store refusals carry `class=`, the cause and a `next:`. The `fn check` line gives `loaded=`, `want=` and a `remedy=` that is the exact `fn load` command, and `fn load` answers `UNCHANGED` on a repeat.

## What could not be tried
- `serve` (live): only `--dry-run` and `help serve`; it starts a store, which this card does not do. The dry-run printed binding, port, directory and ACL file and wrote nothing.
- `acl apply` without `--dry-run`: it rewrites a store's users; judged from `acl apply --dry-run` (`ACL APPLY OK dry-run=true users=4 set=0 would=4`).
- `install store`, `install bus`, `uninstall store`, `uninstall bus`: they write and load service units; judged from `--dry-run` (`INSTALL STORE DRY-RUN ...; nothing was written or loaded`) and, for uninstall, `UNINSTALL STORE DRY-RUN ... present=false`.
- `install ... --dry-run` with a real sops secret: the key and secret files do not exist here, so the secret path was not exercised.
- Any run against the live store; the library check was run against the throwaway store only.
