# nova-redis USE rating, nova-tools 1.1.0

Rater: Grok
Build: eb80e19c25ae
Score: 8/10

## Reasons

`nova-redis help` answers what it does in the first line, then how serve, spill, recall and the function verbs work, and it says the dry-run needs no store. `nova-redis version` exits 0 with one line. `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner trial --name note --ttl 10m --value hi` exits 0 and prints `SPILL OK dry-run=true key=trial:note ttl=10m0s expires=2026-10-03T16:52:31Z bytes=2 store=127.0.0.1:6379 written=0`. That is the scratch plan, and it returns at once. The same plan with `--json` is one object, status ok, written 0, and the fields are named. `nova-redis acl render` is a second job, also with no store: it exits 0 and ends `ACL RENDER OK users=4 functions=41 library=3e675645b96ea537`.

The four refusals do what an AI needs, with one gap. `nova-redis spill` exits 2 and names every missing flag, what each one wants, and `nova-redis help spill`. `nova-redis spill --addr not-an-addr --owner a:b --name x y --ttl banana --value hi` exits 2 with four lines, one per problem. With `--json` those four reasons are one why array, remedy `nova-redis help spill`, and stderr is empty. `nova-redis nosuch` exits 2 and lists the verbs. `nova-redis spill --not-a-flag` exits 2, names the flag, and lists the flags that exist, but it stops there and does not also name the missing required flags.

A 10 would run spill and recall on a store the binary provides, with the same bytes coming back, and would print a serve plan and an acl apply plan that do not dial. This use does not. Help offers no memory address. `nova-redis help serve` says serve starts the server binary in the foreground, reads NOVA_REDIS_PASSWORD, and takes no `--json`. Those verbs are judged from help only, and the Reasons name them: serve, spill without `--dry-run`, recall of a stored value, fn load, fn check, acl check, and acl apply aimed at an address. `nova-redis help acl apply` says `--dry-run` writes nothing. It does not say that it dials nothing, which spill's help does say, so apply's dry-run was not pointed at an address. `nova-redis fn load` with no address exits 2 and names `--addr` before any dial. `nova-redis recall --addr 127.0.0.1:6379` exits 2 for a missing owner and name, and prints no value.

Two guesses remain. Whether apply's dry-run dials is the first. Whether a recalled value is escaped is the second: neither `nova-redis help` nor `nova-redis help recall` says so, and no value was read. The dry-run also hides the text. `--value hello there` prints `bytes=11` and not the words, so the plan shows the length and not the bytes. The render succeeds and then spends four lines on a grant list too long to check by eye. The example sits at the bottom of an 86-line help, and its live spill and recall lines are the ones help says need a store, so they were not pasted.

The score is 8 because the store-free plan and the aggregated refusals are already something an AI can act on, and it is not 9 because the write and the read never happen, the sibling tool's socket form is refused, and the render's success is a wall.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis help` | The example is the last block of 86 lines, and its live spill and recall lines need a store. Help offers no memory address. serve, a real spill, a recall of a value, fn load, fn check, acl check and acl apply against an address were not run. | Put a store-free spill and recall in the example, above the essay, and say they need no server binary. | L |
| 2 | `nova-redis acl render` | The command exits 0 and the last line is clear (users=4 functions=41). Each user line is one long grant list. The JSON is 7126 bytes of the same list. An AI cannot check or safely repeat a line that size. | Default to one short line per user. Put the pasteable rules behind a flag. | M |
| 3 | `nova-redis spill --dry-run --addr /tmp/redis.sock --owner trial --name note --ttl 10m --value hi` | Exit 2: --addr "/tmp/redis.sock" is not host:port. `nova-table help` says a store address is host:port or an absolute socket path. The same dry-run with 127.0.0.1:6379 exits 0. A socket both tools are pointed at does not work here. | Accept the absolute socket path the other tool's help already describes. | M |
| 4 | `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner trial --name note --ttl 10m --value hello there` | Exit 0 and the line is SPILL OK with bytes=11 and no value. The JSON fields are the same. The plan shows the length, not the text that would be stored. | Print the value on the dry-run line and in the JSON, in the same quoting the line already uses. | S |
| 5 | `nova-redis spill --not-a-flag` | Exit 2 names the unknown flag and lists the real flags, then stops. The missing required flags are not in that run. The other three refusals do name every problem. | After an unknown flag, still list each required flag that is absent. | S |
| 6 | `nova-redis help acl apply` | --dry-run says it writes nothing and does not say it dials nothing. spill's help does say that. The flag text says a missing user is created only with one variable. The refusal that names one of several missing users was not reached, because the dry-run was not aimed at an address. | Say whether the dry-run dials. When it refuses, put every missing user in the one next command. | S |

## Good, keep

A spill line with several bad fields exits 2 and names each one, and `--json` puts that same list in one why array with the help command as the remedy. `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner trial --name note --ttl 10m --value hi` dials nothing, exits 0, and prints written=0 with the key, the ttl, the expiry and the byte count. `nova-redis nosuch` exits 2 and names every verb.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| acl apply remedy names one of four missing users | STILL THERE | `nova-redis help acl apply` still says a user the store lacks is created only with one --password-env-for, and the banner says apply refuses at exit 1 without it. `nova-redis acl apply --dry-run` exits 2 with `--addr is required` and never reaches that refusal. It was not aimed at an address, because the help does not say the dry-run dials nothing. |
| recall hex-escapes the value | STILL THERE | `nova-redis help` and `nova-redis help recall` never say how a value is printed. `nova-redis recall --addr 127.0.0.1:6379` exits 2 and names the missing owner and name, and prints no value. The escape was not shown again, because no store is used on this run. |
| --addr refuses the socket the other tool's first run makes | CHANGED | `nova-table help` now says the first run is NOVA_REDIS_ADDR=127.0.0.1:6379, and the dry-run with that address exits 0 and prints SPILL OK written=0. The same help still allows an absolute socket path. `nova-redis spill --dry-run --addr /tmp/redis.sock --owner trial --name note --ttl 10m --value hi` exits 2: not host:port. |
| bounded store-free plans and aggregated JSON refusals | STILL THERE | `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner trial --name note --ttl 10m --value hi` exits 0 with written=0. `nova-redis spill --json --addr not-an-addr --owner a:b --name x y --ttl banana --value hi` exits 2 and the why array holds all four reasons. |
