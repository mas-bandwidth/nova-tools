# nova-version AI-usability review, 2026-10-15

Reviewer: grok-build-0.1 (openrouter/x-ai/grok-build-0.1)
Build: 8486acc19206
Score: 9/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-version help` -> `first run: the binary alone; the example lines write a one-tool manifest and read it.`; the example block ran as printed |
| 2 | refusals | 4 | `nova-version snapshot` -> `SNAPSHOT REFUSED: missing --bin, --out; refusing to guess (--bin wants the directory holding the binaries...); run: nova-version help`; but `nova-version report --file versions.tsv --kind bogus --max -2` -> `REPORT REFUSED: invalid value for --kind (...); run: nova-version report -h` names only the first |
| 3 | output | 4 | `nova-version report --file versions.tsv` -> `REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=8ms ...`; stable line grammar and --json where offered; but `nova-version diff --from b --to a` after rename -> `DIFF OK ... tools=23 changed=2` (identical files give tools=22) |
| 4 | dry-run | 3 | `nova-version snapshot --bin ../bin --dry-run` -> `SNAPSHOT REFUSED: missing --out; refusing to guess`; `nova-version example --out x --dry-run` -> `EXAMPLE REFUSED: unknown flag --dry-run`; moved --dry-run works and writes nothing |
| 5 | idempotent | 5 | `n/a: writes are local file outputs; no store update and no --op surface` |
| 6 | no prompts | 5 | `nova-version example --out v; nova-version moved --dry-run ...` and all others: none waited for interactive answer |
| 7 | exit codes | 5 | `nova-version report --file unk.tsv` -> `REPORT FAILED ...` exit 1; `nova-version snapshot` -> REFUSED exit 2; successes exit 0; matches banner table |
| 8 | one word | 5 | `nova-version help` -> `the verbs are example, moved, snapshot, diff, report, send, version` |
| 9 | push | 5 | `n/a: nothing waits on state; --snapshot is caller-supplied local file for dedup; no polling` |
| 10 | no leaks | 5 | no error or output line observed carrying a secret (PATHs shown on unknown but no keys) |
| 11 | true help | 4 | `nova-version version` -> `nova-version v1.2.26-0.20261015162509-8486acc19206 linux/amd64 go1.26.6`; example lines and refusals ran as help; but `snapshot --file bad` did not name every problem (e.g. bad kind not reported), and every verb -h repeats full exit table |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-version snapshot --bin <dir> --dry-run` | `SNAPSHOT REFUSED: missing --out; refusing to guess (...)`; help says `--dry-run ... write no --out` | make --out optional under --dry-run (or accept any path for dry) | S |
| 2 | `nova-version example --out x --dry-run` | `EXAMPLE REFUSED: unknown flag --dry-run; the flags of example are --json, --out` | add --dry-run support to example (print, do not write) | S |
| 3 | `nova-version report --file versions.tsv --json` | `REPORT REFUSED: unknown flag --json; the flags of report are ...; run: nova-version report -h` | support --json on report (and send) like other verbs | S |
| 4 | `nova-version diff --from before.tsv --to after.tsv` | after one rename: `DIFF OK ... tools=23 changed=2`; no-rename same files: `tools=22` | make tools= count stable (e.g. from left or union) | S |
| 5 | `nova-version report --file versions.tsv --kind bogus --max -2` | only names `--kind` error; `--max` ignored (report -h says "a run prints EVERY problem of the file at once" but flag errors too) | collect and name every bad flag value in one refusal | S |
| 6 | `nova-version <verb> -h` (e.g. version, example) | prints the full 5-verb exit table including report:0/1 etc | print only the current verb's exit codes in -h; full table only in top help | S |
| 7 | `nova-version bogus` | `VERSION REFUSED: unknown verb "bogus"; ...; run: nova-version help` | use consistent tool-level prefix e.g. `NOVA-VERSION REFUSED` (same for `help bogus`) | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-version moved --dry-run ...` | `MOVED OK ... verbs=220` (and in json); no explanation of what "verbs" counts | document the metric (verbs checked? changed?) or add NOTE | S |
| 2 | `nova-version report --file versions.tsv` | `REPORT TOOL ... raw=go\x20version\x20go1.26.6...` | print raw as plain (unescaped) text on the line | S |
| 3 | `nova-version snapshot --bin <dir>` | no NOTE when non-nova-* or unreadable files are present (silently skipped) | add NOTE for skipped entries and why | S |
| 4 | `nova-version help` | the example block uses snapshot and report, but bare first run NOTE after example points only to report | align example block and first NOTE | S |

## Top 3 fixes
1. Let snapshot --bin --dry-run (and example --dry-run) work without forcing a dummy --out path.
2. Give report (and send) --json like the other verbs.
3. Make diff's tools= count stable, and collect every bad flag value in refusals.

## Good, keep
- Refusals for missing required name every one at once with what each wants and a runnable next command; unknown verb/flag list the options.
- The help banner + verb -h give runnable first-run example, the manifest rules inline, effect lines, exit tables; everything needed is in the tool's own output.
- Line output and --json come from one value (parity holds where offered); exit 1 for partial/unknown is clear and as documented.
- Old defects from 1.2.0 and snapshot ratings are still present (dry-run still needs --out, report still refuses --json, diff tools= unstable, bad-flag lists cut early, raw escaped, verbs= unexplained, silent skips, repeated exit tables in -h, VERSION prefix for refusals); the refusal grammar, cold first-run, and line/JSON parity remain strong and should be kept.

## What could not be tried
- `send` (and `report --send`): needs a bus and live redis (NOVA_BUS_REDIS etc); used only --draft (which refused for send) and judged from help.
- network lookups inside report for latest= (github, npm, brew, ollama): env has NOVA_TEST_NO_HOST=1, no net started; judged from help and local cases.
- `moved` over revisions that change many verbs/flags (our pair had zero); the verbs=N meaning not fully exercised.
- actual writes of snapshot --out or moved --out (used only --dry-run to obey read-only card).
