# nova-version AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (51/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-version help` -> `nova-version: which version of each tool is installed, recorded and compared`; bare `nova-version` -> `VERSION REFUSED: no verb given; the verbs are example, moved, snapshot, diff, report, send, version; run: nova-version help` |
| 2 | refusals | 5 | `nova-version diff` -> `DIFF REFUSED: --from is required; it wants the snapshot to compare from; refusing to guess; run: nova-version help`; `nova-version report --file badmanifest.tsv` named all four bad lines at once and ended `run: nova-version report -h` |
| 3 | output | 5 | `nova-version diff --from snap.tsv --to snapB.tsv` -> `DIFF OK from=snap.tsv to=snapB.tsv tools=22 changed=1` and `DIFF CHANGED name=nova-bus from=... to=...`; `--json` returns one object with the same facts |
| 4 | dry-run | 3 | `nova-version snapshot --bin <dir> --out snap-dry.tsv --dry-run` -> `SNAPSHOT NOTE dry run: snap-dry.tsv not written` and no file created; but `nova-version snapshot --bin <dir> --dry-run` -> `SNAPSHOT REFUSED: missing --out`, so the dry-run demands an invented `--out` it never writes, and `example` (a write) has no `--dry-run` flag |
| 5 | idempotent | 3 | no write verb accepts `--op`; `nova-version snapshot --bin <dir> --out snap.tsv` run twice returns identical rows and `example --out` on an existing file reports `unchanged=true` without overwriting, but the documented op-id mechanism is absent |
| 6 | no prompts | 5 | every probe ran to completion with flags only; nothing waited on an interactive answer |
| 7 | exit codes | 5 | `nova-version report --file unknown.tsv` (UNKNOWN) -> `EXIT=1`; usage refusals -> `EXIT=2`; all OK runs -> `EXIT=0`, matching the exit table help states |
| 8 | one word | 5 | verbs are example, moved, snapshot, diff, report, send, version, help, no duplicates; last and installing live in nova-update, so report is not double-counted |
| 9 | push | 5 | `n/a: writes land as files; nothing is polled` |
| 10 | no leaks | 5 | `REPORT UNKNOWN` leaks PATH directory names but no key-shaped or secret material in any output |
| 11 | true help | 5 | help's exit table, `--json` coverage (every verb but report and send), and first-line format all held on the runs above |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-version snapshot --bin <dir> --out snap.tsv` (and moved, example, report --send) | no write verb takes `--op`, so a retry after a timeout has no documented idempotency contract; the repo standard requires it | add `--op <id>` to each write verb so the same op again returns the recorded result and changes nothing | M |
| 2 | `nova-version snapshot --bin <dir> --dry-run` | the dry-run is refused `SNAPSHOT REFUSED: missing --out`, so an AI must invent an `--out` path that is never written; the same holds for `moved --dry-run`; this defect was reported in 2026-10-02 ratings and is still here | let `--dry-run` list the plan without any `--out` | S |
| 3 | `nova-version moved --from <sha> --to <sha> --repo <dir> --out note.md --dry-run` | prints `MOVED OK ... added=0 deleted=0 renamed=0 verbs=220` but neither help, note nor JSON defines what the counts count; this defect was reported in 2026-10-02 ratings and is still here | define each count in `moved -h` and write the counted rows into the note and JSON items | M |
| 4 | `nova-version report -h` | the manifest-rules stanza repeats a command as a label: `nova-version report --file versions.tsv     the six lines that say what versions.tsv holds:` reads as a paste, not a rule heading | reword the label as prose naming the file, not echoing a command | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-version report --file <manifest>` | `REPORT TOOL` prints `raw=go\x20version\x20go1.26.6...` with escaped hex that is hard to read while still parseable | render the raw argv space-separated, keeping JSON as the machine form | S |
| 2 | `nova-version report --file unknown.tsv` | the UNKNOWN line dumps the full PATH, which is noisy on a long PATH | keep the searched-path note but cap it or move it to a NOTE line | S |
| 3 | `nova-version help` | there is no `quickstart` verb; `example` covers the first run but the name is less discoverable | note in the banner that example is the quick start | S |

## Top 3 fixes
1. Add `--op <id>` to every write verb so retries are safe and the repo standard is met.
2. Let `--dry-run` run without an invented `--out` on snapshot and moved, and give example a dry-run form.
3. Define moved's counts in help, note and JSON so a reader can tell what changed.

## Good, keep
- The refusal grammar is excellent: every refusal names the cause, what the flag wants, and the next command (`run: nova-version help`), and a malformed manifest is reported all at once with line numbers; earlier ratings' strongest findings are preserved.
- Output is one stable grammar (`VERB OK/REFUSED/FAILED` with `key=value` facts) and `--json` mirrors the same result value, so a machine never has to parse prose.
- The cold first run works exactly as the example block prints: example, snapshot, then report all run and exit as documented, `example` never overwrites (`unchanged=true`), and dry-runs write nothing when given a path.

## What could not be tried
- `send` (delivery) was not run: it writes to the nova-bus, which is live state this card may not touch; judged from its help and `report --draft`, which prints the same note body without sending.
- `report --send` was not run for the same reason; `report` alone was verified to read and write nothing.
- `moved` was run only with `--dry-run` (it builds both revisions, which is heavy); the note write itself was not created.
