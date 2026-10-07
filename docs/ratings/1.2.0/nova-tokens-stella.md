# nova-tokens READ and USE rating, nova-tools 1.2.0

Rater: opencode/gpt-6-luna in OpenCode
Build: adc585d68173
READ: 6/10
USE: 5/10

This rates the release-candidate tree at `sprint/mechanical-2026-10-02`, not a tagged v1.2.0 binary: the checked-out build identifies itself as `v1.0.1-0.20261007015850-adc585d68173` and there is no v1.2.0 tag here. I built that head and used only a throwaway directory; no live store or server was used.

## Reasons

READ. The first line says what the tool does, the nine verbs are listed, and each verb's `-h` gives its synopsis, flags, effect and an example. The spec is unusually explicit about sources, missing token types, provenance, and the difference between a report and a gate. Those are useful facts to have before trusting a ledger. The help is nevertheless a 161-line wall before its first example commands, and repeats the same global exit table even for `version -h`. Some of its guidance contradicts the spec or actual output: the timeout is not the only default, the `session` example's `TOKENS DAY` fields differ from `fold`, and the first-run instructions bury ten setup commands below the rules. The command reference and usage page add stale or contradictory descriptions instead of reducing that uncertainty.

USE. The printed setup and fold/check/sum/sources/report examples ran successfully on the scratch transcript; the ledger dry-run rendered two rows without dialing Redis. The output keeps unreported reasoning as `-`, shows the source label, and the basic local flow is straightforward. But following the separate session example with the same sample transcript and `--out ./out` adds its 812 input tokens a second time. `check` still says `CHECK OK`, and `sum` reports 1624. A structurally valid day file is therefore not evidence that this documented flow has counted each message once. The JSON path also serializes count values as strings beside numeric fields, and some refusals send the caller to the entire global help rather than the verb being built. I would not use this without independently controlling source overlap and parsing every JSON count field.

## Findings

| # | where | finding | fix |
|---|---|---|---|
| 1 | `nova-tokens session --claude-session ./session.jsonl --out ./out` after the help's fold example | Both examples use the same `example-1` transcript. Session writes another row as `unattributed`; `check` stays green and `sum` doubles each token count (input 812 to 1624). | Make the session example use a distinct transcript or output directory, and refuse or clearly surface shared message IDs when session data is already in the day file. |
| 2 | `nova-tokens sum --out ./out --month 2026-09 --json` | Count fields such as `input` and `turns` are JSON strings while fields such as `days` and `pairs` are numbers; a machine caller must coerce fields individually. | Emit counts as JSON numbers and use one stable type per fact across verbs. |
| 3 | `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --json` | The missing-source refusal's remedy is `nova-tokens help`, not the flags for the verb being built. | Point verb refusals at `nova-tokens fold -h`. |
| 4 | `nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --dry-run` | The note body is stdout, but `dry_run=true` is only on the final stderr status line; capturing the artifact alone loses the dry-run confirmation despite the help promising it on the last line. | State explicitly in `report -h` that the dry-run status is on stderr, or include the status in a machine-readable result alongside the artifact. |
| 5 | `nova-tokens version --json` | The refusal says it got one argument but does not identify the rejected `--json` flag. | Name `--json` in the refusal. |
| 6 | `nova-tokens version -h` (and every verb's `-h`) | Every verb help prints the same family-wide exit table, including source and fold failures that `version` cannot encounter. | Print the exit cases that apply to each verb, or link to the global table without presenting it as that verb's contract. |
| 7 | `nova-tokens report -h` | The local-mode synopsis omits `--bus` and `--swarm`, although both appear in the flags list. | Include all accepted local source flags in the synopsis. |
| 8 | `nova-tokens session -h`, `--role` | Help says the role changes the row to `<model>/<role>` but omits that the role is also written as the repo. A run with `--role reviewer` produced `repo=reviewer`. | Say that the role appears in both the model and repo cells. |
| 9 | `nova-tokens session --claude-session ./session.jsonl --out ./role-out --role reviewer` | Session prints `TOKENS DAY day=...`, while fold prints `TOKENS DAY date=...`; a scanner cannot use one field name for the shared event. | Use the spec's `date=` field in both outputs, or give session a distinct event token. |
| 10 | `cmd/nova-tokens/main.go:105`; `nova-tokens fold -h` | The banner calls `--timeout` the only flag with a default, but `--weights` has a default and session `--day` defaults to every stamped day. Fold help also omits timeout's 120-second default. | Describe each default accurately and show 120 in the timeout flag help. |
| 11 | `cmd/nova-tokens/main.go:50` | The first-run directions point to setup and example blocks near the end of the 161-line help, after a long run of policy text. | Put a short runnable first run immediately after the usage synopsis and move detailed rules to verb help or the spec. |
| 12 | `cmd/nova-tokens/main.go:249`; `docs/CLI.md:1842` | The bare-command refusal and command reference call `sources` the only inspection, but `sum`, `check` and `profiles` also write nothing. | Remove the claim or name every read-only verb. |
| 13 | `docs/CLI.md:1829` | It describes the help's ten-line setup block as “one setup command,” understating what a cold user must paste. | Call it a setup block and state its actual number of commands, or provide one executable setup command. |
| 14 | `docs/CLI.md:1825` | The command reference packs the check behavior and descriptions of several other verbs into one very long paragraph, making the reference hard to scan. | Split the paragraph into short sections by verb and rule. |
| 15 | `docs/USAGE.md:185-187` | It says there are no defaults and no environment variables, contrary to the timeout and weights defaults and the Redis/OpenCode environment lookups. | Name the defaults and the two environment-reading paths. |
| 16 | `README.md:55-60` | The repository's try-one section says the tools are 1.0.0 and installs nova-memory at v1.0.0, not the 1.2.0 release candidate being rated. | Update the release and install version when the 1.2.0 release is published. |
| 17 | `docs/SPEC-TOKENS.md:781` | The xAI export paragraph says `usd=0` when no source reported cost, contradicting the earlier rule that absent cost is `-`. | Use `usd=-` for an unreported cost. |

## Good, keep

The setup was self-contained and needed no live data or service. The fold output named its source, the day file kept the five token types separate, and the unreported reasoning count remained `-`. `check`, `sum`, `sources` and local `report` all ran from the shown paths. The ledger dry-run made its no-dial behavior visible, and `version` identifies the exact build.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| JSON count fields use strings | STILL THERE | `sum --json` returns string `input` and `turns` alongside numeric `days` and `pairs`. |
| Missing-flag refusals point to global help | STILL THERE | The fold missing-source refusal returns `remedy: nova-tokens help`. |
| The sample session can double the sample fold | STILL THERE | Running both printed examples gives `CHECK OK` and `SUM TOTAL input=1624` from an 812-token message. |
| Report says OK for an unreadable source; subject is unquoted | FIXED | A report with a missing provider file printed `REPORT FAILED` and exit 1; successful local report quoted its subject. |
| Timeout is described as the only default | STILL THERE | The top help omits the weights and session-day defaults while the spec names them. |
