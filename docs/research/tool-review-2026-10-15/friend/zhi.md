# nova-friend AI-usability review, 2026-10-15

Reviewer: z-ai/glm-5.3
Build: 4540d7fd37ca
Score: 8/10 (43/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 3 | `nova-friend install --as bob --harness opencode --dir ./bob --model deepseek/deepseek-v4 --dry-run` (the help's own example line, as printed) -> `INSTALL REFUSED: --redis is required; it wants the bus store's Redis address, host:port (or NOVA_BUS_REDIS), written into the agent; refusing to guess; run: nova-friend help` exit 2; with the env set the same line refuses the relative `--dir` too |
| 2 | refusals | 4 | `nova-friend run --dry-run` -> `RUN REFUSED: --as is required; it wants your name, a nova-config friend row; refusing to guess; run: nova-friend help` and two more lines naming `--harness` and `--dir`: every problem at once, each with what it wants; docked for hook's wrong-cause refusal (row 11) |
| 3 | output | 3 | `nova-friend status --as bob --dir ./bob --json` -> `{"result":{"verb":"status","status":"failed","exit":1,"word":"NONE","why":["no daemon has run as bob ..."]},"facts":{}}` — one value, two renderings everywhere both ran; docked: `hook --json` is refused though offered, and resume prints `pause=402\x20insufficient\x20balance` |
| 4 | dry-run | 3 | `nova-friend install --as bob --harness opencode --dir <d> --model deepseek/deepseek-v4 --dry-run` -> `INSTALL OK label=com.nova.friend-bob ... dry_run=true` and nothing was written (verified); docked: `PONG NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 3 | `nova-friend resume --as bob --state-dir ./state` run again with no marker -> `RESUME OK cleared=none`; the local writes are idempotent and say so, but no write takes `--op` |
| 6 | no prompts | 5 | `nova-friend pong --as bob --nonce abc123 --dry-run --state-dir ./state2` -> `PONG REFUSED: --to is required: the sprint server has not named a seat (no status file in ./state2); it wants the coordinator's name; run: nova-friend help` — it refuses, never asks |
| 7 | exit codes | 4 | refusals exit 2 and `nova-friend status --as bob --dir ./bob` exits 1 as the table states; docked: `nova-friend acceptance --as bob --dir <d> --harness opencode --dry-run` -> `ACCEPTANCE NOTE friend=bob exit=126 the runner opencode cannot execute: exec: "opencode": executable file not found in $PATH; run: nova-friend help` — 126, a code its own `-h` exit-codes line does not list |
| 8 | one word | 5 | `nova-friend` -> `FRIEND REFUSED: no verb and no file given; the verbs are hook, run, install, uninstall, check, host, reach, pong, watch, status, lanes, refuse-go, resume, acceptance, version; run: nova-friend help` — one word per thing, no duplicates |
| 9 | push | 5 | `nova-friend reach --as <coordinator> --to bob --dry-run` -> `REACH DRY-RUN friend=bob from=bus step_timeout=1m0s dry_run=true` — a ladder that pushes at the friend (bus, then push, then window), and `watch` blocks on the stream and returns on a wake rather than polling |
| 10 | no leaks | 5 | `nova-friend version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-friend v1.2.26-0.20261015160522-4540d7fd37ca linux/amd64 go1.26.6"}` — no line probed carried a secret; `--secrets` takes names, never values |
| 11 | true help | 3 | `nova-friend hook --json` -> `nova-friend hook REFUSED: --harness wants claude and hook takes no words; run: nova-friend hook -h` — against the banner's `Every verb but run takes --json` and hook `-h`'s own `--json` line; plus the example block's exit-2 lines and `refuse-go --name cargo` accepted |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-friend install --as bob --harness opencode --dir ./bob --model deepseek/deepseek-v4 --dry-run` | the help's first example line exits 2 as printed: `INSTALL REFUSED: --redis is required; it wants the bus store's Redis address, host:port (or NOVA_BUS_REDIS), written into the agent; refusing to guess; run: nova-friend help`; the pong example line refuses the same way; the synopsis marks `--redis` optional while it is required without the env | spell the store in the example lines (a `NOVA_BUS_REDIS=value` prefix or `--redis <addr>`), and state in the flags that it is required when the env is unset | S |
| 2 | `nova-friend install --as bob --harness opencode --dir ./bob --model deepseek/deepseek-v4 --dry-run` | with the env set the same example line exits 2 again: `INSTALL REFUSED: the friend's directory "./bob" is not absolute: a harness's settings name it by its absolute path; run: nova-friend help`, while `host` and `status` accept `--dir ./bob` — one flag, two shapes across verbs | give `--dir` one rule: resolve relative paths in install, or refuse them in every verb — and print the example with a directory spelling every verb takes | S |
| 3 | `nova-friend hook --json` | `nova-friend hook REFUSED: --harness wants claude and hook takes no words; run: nova-friend hook -h` — a wrong cause on both counts (no `--harness` was needed and no word was given; the real cause is `--json`), against the banner's `Every verb but run takes --json` and hook `-h`'s own `--json` flag line | read the `--json` flag in hook and print the result object, or stop advertising it there, and name the real cause in the refusal | S |
| 4 | `nova-friend pong --as bob --nonce abc123 --dry-run --state-dir ./state2` | `PONG NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` — the tool itself warns the dry run is not wired, so no reader can trust `pong --dry-run` to be dry when the store answers | read the dry-run flag in pong: print the line it would send, send and write nothing, and drop the note | S |
| 5 | `nova-friend acceptance --as bob --dir <d> --harness opencode --dry-run` | `ACCEPTANCE NOTE friend=bob exit=126 the runner opencode cannot execute: exec: "opencode": executable file not found in $PATH; run: nova-friend help` — the dry run executes the runner, though its flag help says `print what the verb would write and write nothing` | make acceptance's `--dry-run` print the plan (the wall, the runner, the flags) and execute nothing | S |
| 6 | `nova-friend acceptance --as bob --dir <d> --harness opencode --dry-run` (its `-h`) | the effect text says the verb exits with the wall's real code (125, 126) but its own `exit codes:` line lists only `0 done, 1 ..., 2 could not run`, so 126 reads as an unstated code | list 125 and 126 in acceptance's exit-codes line | S |
| 7 | `nova-friend resume --as bob --state-dir ./state --dry-run` (with a pause marker holding `402 insufficient balance`) | `RESUME OK cleared=would pause=402\x20insufficient\x20balance dry_run=true` — the human line renders the provider's message with literal `\x20` in place of each blank; the JSON rendering prints it plain | render the marker's message plain in the typed line, as the JSON rendering already does | S |
| 8 | `nova-friend refuse-go --name cargo` | the help says the name is go / gofmt but any name runs: `REFUSE-GO REFUSED: cargo is refused: no go command runs on this machine, ... then ssh <bench> 'cd <clone> && cargo ...'` — a bad value is accepted and interpolated into the remedy instead of refused | refuse a `--name` that is neither go nor gofmt, naming the two there are | S |
| 9 | `nova-friend lanes --as bob --clear-faults` (the example block's last line) | the example block's only write line carries no `--dry-run`, so a stranger pasting the block as printed runs a real clear; every other example line is a read or a dry run | print the read form (`nova-friend lanes --as bob`) or the `--dry-run` spelling in the example | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | every write verb | no write takes `--op <id>`, so a retry after a timeout has no operation id to deduplicate on (the local writes are idempotent by construction, the deliveries are not) | add `--op <id>` to install, pong, lanes, resume per the standard | M |
| 2 | `nova-friend install --dry-run` on a host with no launchd | the plan prints `launchctl bootout` and `launchctl bootstrap` steps that cannot run on this host; nothing says the verb needs launchd until the bootstrap fails | refuse at parse on a host without launchd, naming the platform | S |
| 3 | `nova-friend hook --harness claude` with a hand-made event | the deny line `the hook input is not a PreToolUse event; the command was not started` does not name the field it wanted, so a sender with a near-miss event guesses | name the missing field in the deny reason | S |
| 4 | `nova-friend run -h` | about thirty lines of dense prose stand between the usage and the flags; a small model reads the whole banner to reach one flag's meaning | move the narrative into NOTE lines or trim it to the five lines the banner standard names | S |
| 5 | `nova-friend run --bogus` | the unknown-flag refusal truncates the flag list: `the flags of run are ... --load-width and 16 more` — the count is honest but the list is not complete | print every flag, or end at the count with the `-h` door (already named) | S |
| 6 | `nova-friend status --as bob --dir ./bob` | the remedy after `STATUS NONE` holds a placeholder: `run: nova-friend install --as bob --harness <h> --dir ./bob` — the next turn is a choice, not a paste | name one harness in the remedy, keeping the placeholder as the alternative | S |

## Top 3 fixes
1. Make the example block run as printed (the store spelled in the install and pong lines, and a `--dir` shape every verb takes), moving cold and true help the most.
2. Wire pong's `--dry-run` and drop the `may have written` note, so every dry run in the tool is trustworthy again.
3. Accept `--json` in hook (or stop advertising it) and name the real cause in its refusal, restoring the banner's one `--json` claim.

## Good, keep
- The refusal grammar is exemplary: one run names every problem at once, each flag with what it wants and the door to run, and nothing anywhere waited on an interactive answer.
- One result value, two renderings, held everywhere both could run: the `--json` object matched the typed lines on every verb that offers both.
- The verbs each answer one question with one word, the reach ladder and `watch` push rather than poll, and `run --dry-run` opens no store exactly as its help claims.
- The 2026-10-02 ratings hold no nova-friend file, so no old defect could be re-checked; the findings above are all first findings.

## What could not be tried
- `run` (the daemon): never started, per the card; judged from its help and its `--dry-run`, which printed the daemon it would run and started nothing.
- `install` and `uninstall` real: they write and load a launchd agent; only their `--dry-run` plans were run.
- `host` real: it starts a tmux session on this machine; only its `--dry-run` was run.
- `pong` with a live store, `watch`'s live wake, `check`'s health and delivery reads, `reach` beyond `--dry-run`: no bus store may run on this machine, so every store-touching path refused at the dial and was judged from help and dry runs.
- `acceptance`'s real wall: the harness runner is not on this machine's PATH; the `--dry-run` attempt exited 126.
- `hook` inside a live harness session: probed with hand-made stdin events only.
