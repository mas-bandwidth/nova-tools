# nova-config AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: 1aa6cffc99e4
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-config friend list --file try.json` -> `FRIEND name=f1 tiers=pro roles=- width=2 mode=one-shot ...`; the `help` banner's `example:` lines all ran as printed with no database |
| 2 | refusals | 4 | `nova-config friend list --bogus --max abc --file try.json` -> `nova-config friend list REFUSED: unknown flag --bogus; this verb takes --file, --json, --max, --pg, --seat; run: nova-config friend list -h` (names the flag and the next command, but not the bad `--max abc` beside it) |
| 3 | output | 4 | `nova-config friend list --json --file try.json` -> `{"result":{"verb":"friend list","status":"ok","exit":0},"facts":{"kind":"friend","rows":1},"items":[{"kind":"friend","fields":{..."width":"2"...}}]}` (parseable and stable, but `items[].fields` carry numbers and booleans as strings) |
| 4 | dry-run | 5 | `nova-config migrate --file dry2.json --dry-run` -> `CONFIG MIGRATE file=dry2.json from=0 to=45 applied=0 dry_run=true pending=45`; `ls dry2.json` -> `No such file or directory` |
| 5 | idempotent | 0 | `nova-config machine add -h` -> flags list ends at `--width`, with no `--op`; every write verb repeats the write on a retry |
| 6 | no prompts | 5 | `nova-config friend list --file try.json` -> returned immediately without asking anything |
| 7 | exit codes | 5 | `nova-config friend list --max nope --file try.json` -> exit 2; `nova-config help` -> exit 0; `nova-config machine self --check` -> exit 3; all match the help table |
| 8 | one word | 5 | `nova-config kinds` -> `CONFIG KINDS count=8`, one kind per line; verbs are single words, no duplicates |
| 9 | push | 5 | `n/a: read verbs wait on no state; the fleet's apply pushes to Redis` |
| 10 | no leaks | 5 | `nova-config status --file try.json` -> `CONFIG STATUS file=try.json ...` prints no secret or DSN password |
| 11 | true help | 4 | `nova-config --help` -> `how it works: each kind (machine, fleet, friend, sprint, loop, route, tier)` lists seven kinds and omits `provider`, which `nova-config kinds` lists |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine add m1 --user nova --seat s1 --slots 8 --actor a1 --file try.json` | write verbs take no `--op`; a retry after a timeout re-applies the write and adds a second row on a new rev, so a resumed run is not safe | add a `--op <id>` flag to add, set, remove and apply, returning the recorded result for a repeated id | M |
| 2 | `nova-config friend -h`, `nova-config route -h`, `nova-config tier -h`, `nova-config provider -h` | the refusal says `run: nova-config friend -h`, but that page prints only `usage: nova-config friend [flags]` and hides the subverbs and fields the AI needs | render each kind's subverb synopses and field list from its `pkg/config.Kind` descriptor | S |
| 3 | `nova-config friend list --json --file try.json` | `items[].fields` serialize numbers and booleans as strings (`"slots":"8"`, `"tla":"false"`, `"width":"2"`) and empty as `""` or `null`, unlike `facts` numbers | type numeric and boolean fields in `--json` `items[].fields` and use one empty representation | M |
| 4 | `nova-config --help` | `how it works:` names only seven kinds; `provider` (in `nova-config kinds`) is missing | add `provider` to the kind list in the banner | S |
| 5 | `nova-config friend list --bogus --max abc --file try.json` | an unknown flag aborts parsing and hides the invalid `--max abc` beside it, so one run does not report every problem | collect unknown flags and bad values in one refusal | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-config version` | the version line does not open with the verb name and status word (`VERSION OK`), unlike other verbs | prefix with `VERSION OK` | S |
| 2 | `nova-config <kind> <verb> -h` footer | every verb help repeats `migrate --dry-run: ready=no` and `machine self: 2 not a row, 3 unreadable` clauses unrelated to that verb | scope the exit-code clauses to the verbs they describe | S |
| 3 | `nova-config friend list --max notanint --file try.json` | `invalid value "notanint" for flag -max: parse error` spells the flag `-max` with a single dash | spell `--max` in parse errors | S |
| 4 | `nova-config friend list` with no store | the refusal prints an absolute home path under the caller's profile directory (e.g. `~/.config/nova-config/login.json`) on the caller's machine | shorten to `nova-config login` without the path | S |
| 5 | `nova-config frobnicate` | the unknown-verb refusal's `want` list omits `help` and `version` | name every verb | S |

## Top 3 fixes
1. Add `--op` idempotency to every write verb (add, set, remove, apply) — this alone moves criterion 5 from 0 to 5.
2. Render each kind's help (`friend`, `route`, `tier`, `provider` -h) with its subverbs and fields, and add `provider` to `how it works:`.
3. Type numeric and boolean fields in `--json` `items[].fields` and collect unknown flags with bad values in one refusal.

## Good, keep
The refusal grammar is excellent: every refusal names the cause and the exact next command in one line (`run: nova-config ...`), and a missing store lists every option at once. The banner is a complete cold start: what it does, how it works, and an `example:` block whose lines ran as printed with no database. `--json` is offered on every verb, the typed line and JSON are the same value, and listings are bounded with `MORE shown=<n> total=<n>`; `migrate --dry-run` writes nothing.

The 2026-10-02 rating's main defect (a refusal under `--json` left stdout empty and printed plain text on stderr) is FIXED: `nova-config friend list --json --max nope --file try.json` exits 2 and prints the refusal as a JSON object on stdout with `result.status=refused`, `exit`, `why` and `remedy`, stderr empty.

## What could not be tried
apply, set and remove (store writes) were not run: this review is read verbs only per the card, so their `--dry-run` was judged from help and from migrate's confirmed dry-run. `add` was run only to seed the scratch `--file` store for the read verbs. The Redis-backed verbs (machine list/set/show with `--redis`, apply, status --redis) need a live Redis and were not run. `login`, `logout` and `loop run` write live state or start a process and were not run.
