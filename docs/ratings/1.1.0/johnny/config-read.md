# nova-config READ rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 6.5/10
README: 6/10

## Reasons

README line (the tool's sentence, README.md:28, the same words as the banner at cmd/nova-config/main.go:67): a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis.

First confusion, README.md:25: the bus row says to initialize a local Git bus "as shown below", but the command on that line does not initialize anything. The init is README.md:64. The first confusion about this tool is the next row, README.md:28. One cell names three mechanisms (rows in PostgreSQL, an apply into Redis, and `nova-config migrate --print`, which connects to nothing) and then calls that "the Nova fleet configuration model" without naming a row, a kind, or a file.

First boredom, README.md:47: the table has already given every tool a first command, and the install block starts the same story again, pinned to a release tag. The same skim returns at docs/CLI.md:1945, where each synopsis is one line too long to read, and again at docs/nova-config/README.md, 567 lines that retell the spec.

First doubted claim, README.md:48: "These are the Nova Tools 1.0.0 commands." The tree this rating reads already carries a 1.1.0 notes page and a banner whose first run is not the 1.0.0-shaped cell above it. The claim this tool's own cell makes, "Storing configuration needs PostgreSQL", is the one the rest of the read breaks.

The banner is the essay the code lives up to. In a few lines it names the kinds, the history row, apply as a copy into Redis, and `--file` as a local JSON stand-in with no database (cmd/nova-config/main.go:69-74). The example is five commands that write only `./try.json` (cmd/nova-config/main.go:113-118). `kinds` and each verb's `-h` come from the descriptors (cmd/nova-config/verbs.go:30-71, pkg/config/kind.go). A write collects every problem and refuses once, with a next command (cmd/nova-config/main.go:629-657). That is close to a 10 for the help an AI gets after it has the binary.

The pages that lead to the binary are not that essay. README.md:28 tells an AI to run `nova-config migrate --print` and that storing needs PostgreSQL. docs/USAGE.md:84, the first link the README offers, installs a 1.0.0 binary and never mentions this tool's file store. docs/CLI.md:1974 and the guide's first run (docs/nova-config/README.md:42-47) teach `--file`. An AI that follows the table never meets the trial the banner was written for.

The command reference then invents a field. docs/CLI.md:1970 lists `width` on a loop. docs/SPEC-CONFIG.md:52 does the same in the placement table. The loop descriptor has no width field (pkg/config/kind.go:303-311); the spec's own loop table (docs/SPEC-CONFIG.md:237-245) and the migration note (docs/SPEC-CONFIG.md:254-257) already deleted it. An AI that copies the reference will pass a flag the verb does not have.

Two more gaps sit between the help's promise and the code. `--json` is documented as one object instead of the lines (cmd/nova-config/main.go:428). A refusal never builds that object: it prints one plain line on stderr and leaves stdout empty (cmd/nova-config/main.go:276-282). `apply --dry-run` is documented as the plan. The check path returns before any write (pkg/config/apply.go:174-178), so a ceiling or a role refusal, which only `Write` returns (pkg/config/apply.go:29-30), cannot appear in the plan. A clean dry run is not "apply will accept this".

The command file is still the whole tool: 1645 lines, with a hand-rolled line for each success and a separate `tool.Out` for `--json` (cmd/nova-config/main.go:691-721). The shared skeleton's write flags are `--actor` and `--op` (AGENTS.md:78). This tool records `--as` (cmd/nova-config/main.go:422) and takes no caller operation id on add or set (cmd/nova-config/main.go:606-610). The kind grammar is shared; the result is not.

A 10 is the banner's story in the README cell, one summary list that matches the descriptors, one result value rendered as lines or as JSON including a refusal, and a dry run that either performs the write-time checks or names the ones it skips. The score is 6.5 because the help and the kind verbs are already that tool, and the front door, the reference paragraph, and the two result writers are not. The README score is 6 because the sentence matches the banner and the setup command in the same cell does not.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:28 | The choosing cell's command is `nova-config migrate --print` and its setup says storing needs PostgreSQL. The banner's first run is `nova-config migrate --file try.json` and four more writes to that file (cmd/nova-config/main.go:113-118). An AI that starts at the table never tries the store the help was built around. | Point the cell at the banner example, and say PostgreSQL is the fleet store while `--file` is the trial. | S |
| 2 | docs/CLI.md:1970 | The one-paragraph kind list still gives a loop a `width` field, and the spec's placement table does too (docs/SPEC-CONFIG.md:52). The descriptor's loop fields are machine, argv, seat, keys, every, keepalive, enabled (pkg/config/kind.go:303-311). | Delete `width` from both summaries so they match the loop field table at docs/SPEC-CONFIG.md:237. | S |
| 3 | cmd/nova-config/main.go:276 | A refusal is a plain stderr line and an empty stdout, even when `--json` was set. `apply --dry-run` returns after the plan (pkg/config/apply.go:174) and never calls `Write`, so a ceiling or a role refusal cannot show up in the plan an AI trusts. | Render a refusal through the same result value as success, and either run the write-time checks on a dry run or name the checks the plan cannot see in the effect line (cmd/nova-config/verbs.go:23). | L |
| 4 | cmd/nova-config/main.go:710 | Each success is a hand-written line or, separately, a `tool.Out`. The two can drift, and they are why this file is 1645 lines. Writes take `--as`, not the family's `--actor`, and no caller `--op`. | Build one result and render it twice. Take the shared actor and operation-id flags from the skeleton. | L |
| 5 | docs/nova-config/README.md:165 | The guide says a machine width is set directly and that 0 is not a member. It does not say an unset width is the default, half the cores, resolved later by fleet sync. The spec says that (docs/SPEC-CONFIG.md:155-168) and so does the field help (pkg/config/kind.go:242). | Add the unset-means-default rule beside the "set directly" sentence, in the same words as the field help. | S |

## Good, keep

The banner's `example:` block is a real first run: migrate, add, set, list, history, all against `./try.json`, no database (cmd/nova-config/main.go:113-118). Each verb's `-h` carries one more line from that same file (cmd/nova-config/verbs.go:33-36).

One grammar for every kind, generated from the descriptor, including the required-field line and the worked example (pkg/config/kind.go, cmd/nova-config/main.go:601-614). A write names every problem in one refusal (cmd/nova-config/main.go:629-657).

A route taken out of the deal must carry a note before anything is written (pkg/config/kind.go:328, the `enabled` field help). History records who changed a row and which fields moved.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,655-line main.go that writes every result twice | STILL THERE | cmd/nova-config/main.go is 1645 lines; the line and the JSON object are still two blocks at cmd/nova-config/main.go:691 and cmd/nova-config/main.go:718 |
| flags off the family's shape | STILL THERE | AGENTS.md:78 names `--actor` and `--op`; cmd/nova-config/main.go:422 adds `--as`, and add/set declare no operation id (cmd/nova-config/main.go:606-610) |
| contradictory storage guidance | STILL THERE | README.md:28 says storing needs PostgreSQL and prints `migrate --print`; cmd/nova-config/main.go:72 says `--file` tries every verb with no database, and docs/CLI.md:1974 starts there |
| a large command file | STILL THERE | cmd/nova-config/main.go is 1645 lines and still holds migrate, status, apply, inventory, and both result writers |
| README front door scored 6.5 to 7, and 8.4 | STILL THERE | README.md:28 still teaches `migrate --print` and a required database; this read scores that cell 6/10 |
