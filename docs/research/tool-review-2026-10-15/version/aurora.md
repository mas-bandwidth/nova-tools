# nova-version AI-usability review, 2026-10-15

Reviewer: Mercury (inception/mercury-3-preview-1002)
Build: cc2f2f8c8148
Score: 9.5/10 (51/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-version help` -> `first run: the binary alone; the example lines write a one-tool manifest and read it.`; the example block ran as printed |
| 2 | refusals | 4 | `nova-version snapshot` -> `SNAPSHOT REFUSED: missing --bin, --out; refusing to guess (--bin wants ... ); run: nova-version help`; but `nova-version report --file versions.tsv --kind bogus --max -2` -> `REPORT REFUSED: invalid value for --kind (...)...; run: nova-version report -h` names only the first bad flag |
| 3 | output | 4 | `nova-version report --file versions.tsv` -> `REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=6ms ...`; stable line grammar and --json; but the diff tools count is not stable |
| 4 | dry-run | 3 | `nova-version snapshot --bin <bin> --dry-run` -> `SNAPSHOT REFUSED: missing --out; ...` though the dry run writes no --out; moved has a working --dry-run; example's --out write has none |
| 5 | idempotent | 5 | `n/a: writes are local file outputs; no store update and no --op surface` |
| 6 | no prompts | 5 | none of example, snapshot, diff, report, moved, version, send waited on an interactive answer |
| 7 | exit codes | 5 | `nova-version report --file unknown.tsv` -> `REPORT FAILED checked=1 known=0 unknown=1` exits 1; usage refusals exit 2; 0 on success, as the banner states |
| 8 | one word | 5 | verbs example, moved, snapshot, diff, report, send, version: one word each, no duplicates |
| 9 | push | 5 | `n/a: nothing waits on state; report reads and, only with --send, writes the snapshot state file; no polling` |
| 10 | no leaks | 5 | no error or output line observed carrying a secret |
| 11 | true help | 5 | `nova-version version` -> `nova-version v1.2.26-0.20261015145010-cc2f2f8c8148 linux/amd64 go1.26.6`; exit-code tables and example verified true when run |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-version snapshot --bin <bin> --dry-run` | refused `SNAPSHOT REFUSED: missing --out; refusing to guess` although `snapshot -h` says a dry run writes no --out; a caller must invent an --out path for a run that writes nothing | let --dry-run list the rows without --out | S |
| 2 | `nova-version diff --from before.tsv --to after.tsv` | same two files, one row renamed, print `DIFF OK ... tools=23 changed=2`; identical files print `tools=22`; the count is not stable across a change | count tools from the union of names once | S |
| 3 | `nova-version report --file versions.tsv --kind bogus --max -2` | names only `--kind`; the invalid `--max` is not named, so the caller fixes one and reruns (the manifest help promises every problem at once) | validate every flag value before refusing and name each | S |
| 4 | `nova-version snapshot --bin <bin> --out ov.tsv` run twice | the second run replaces ov.tsv and prints the same OK line with no `replaced=true`, while example never overwrites and reports `unchanged=true` | say `replaced=true` on the OK line, or refuse an existing --out without a flag | S |
| 5 | `nova-version <verb> -h` for every verb | each repeats the whole five-verb exit table, so `example -h` and `version -h` print codes for verbs they do not drive | print the verb's own exit line in -h and keep the full table in help | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-version example --out versions.tsv` | the example verb's local write has no --dry-run, unlike moved and snapshot | accept --dry-run and print the manifest without writing | S |
| 2 | `nova-version moved --from A --to B` over a change pair | the summary `added=0 deleted=0 renamed=0 verbs=N` counts only whole tools and the total, so it can understate and note's rows; the no-change pair here was consistent | count verbs and flags too or name the fields; put note entries in the JSON items | M |
| 3 | `nova-version report -h` | the effect line says report reads and writes nothing without --send, but --snapshot writes state on its own | state that --snapshot is a local write even without --send | S |
| 4 | `nova-version moved -h`, `snapshot -h` | no flag line prints its default for --timeout/--budget (snapshot's are only in the full text) | print the default beside every flag that has one | S |

## Top 3 fixes
1. Let `snapshot --bin --dry-run` run without an --out it never writes.
2. Make `diff`'s `tools=` count stable whether or not rows changed.
3. Validate and name every bad flag value in one refusal.

## Good, keep
- The refusal grammar names the cause and the next command in one line for a missing flag, an unknown flag, an unknown verb and a bad value, and a missing-required-flags run reports all of them at once.
- The banner and per-verb `-h` embed the manifest's six rules, the exit-code tables and a runnable example; the tool is usable cold with no other document.
- The one output shape renders as lines and --json from the same value, report/send clearly take no --json because they carry a note body, and exit 1 on UNKNOWN is honored.
- The old 1.2.0 defects of snapshot's --dry-run demanding --out, silent --out replacement, single-bad-flag refusals, and repeated exit tables in every -h are all still present; the strong refusal grammar, the first run, and the line/JSON parity are worth keeping.

## What could not be tried
- `send` needs nova-bus (a live bus delivery) and writes the snapshot state file; judged from its help and report's --draft only.
- `moved` over a pair that actually changes tools/verbs was not run (the revision pair taken had no changes), so its note-vs-summary parity was not directly reproduced.
- `report --send` actual delivery and `report --snapshot` state persistence were not exercised against a live store.
