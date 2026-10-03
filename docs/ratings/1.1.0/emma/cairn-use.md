# nova-cairn USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8.5/10

## Reasons
The tool ran cleanly from the four example commands in its usage banner without requiring external services or network setup. Both nested directory stores and flat markdown bench stores executed end to end, preserving exact prose and reporting durability accurately. Duplicate appends correctly identified existing entries and returned stored timestamps, while conflicting entries exited 1 with clear explanations.

Four refusals were provoked and verified: missing required flags were reported all at once with full descriptions, unknown flags displayed available options with -h hints, unknown verbs listed valid subcommands, and bad parameter values explained valid choices.

The score stopped short of 10 because write verbs lack --dry-run support, re-opening an active session synthesizes a new clock stamp without a duplicate indicator, and the index verb provides no mechanism to list session identifiers independently of entries.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn open --store ./cairns --session s1 --publish manual --dry-run` | Write verbs open and append refuse --dry-run as an unknown flag despite standard requiring dry run planning | Implement DryRun execution on open and append to preview planned file writes | M |
| 2 | `nova-cairn open --store ./cairns --session s1 --publish manual` | Re-opening an existing session outputs a standard OPEN OK line with a newly synthesized clock stamp and no duplicate indicator | Report duplicate=true and retain the session original creation stamp in output facts | S |
| 3 | `nova-cairn index --store ./cairns` | The index verb lists only entries and provides no option to list session identifiers or inspect sessions without entries | Add a flag such as --sessions to list session identifiers and metadata | S |
| 4 | `nova-cairn append --store ./cairns` | Missing flag refusals suggest running general help instead of verb-specific flag documentation | Point the refusal remedy to nova-cairn append -h | S |
| 5 | `nova-cairn append --store ./cairns --session missing --entry e1 --text "words" --publish manual` | Refusal text combines an explicit open first remedy with a redundant trailing run help clause | Suppress the default run help suffix when an explicit whole-verb remedy is provided | S |

## Good, keep
Clean example workflow in help that executes in one sitting without external dependencies.
Faithful duplicate handling that returns original timestamps and distinguishes duplicates from conflicting text.
Complete and structured JSON output across all verbs mirroring the line-oriented protocol.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| index cannot list sessions | STILL THERE | nova-cairn index lists entry items only with no flag to list session names |
| a re-open looks like a first open | STILL THERE | nova-cairn open re-run prints OPEN OK with new stamp and no duplicate indicator |
