# nova-update AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-update example --out versions.tsv` -> `EXAMPLE OK wrote=versions.tsv entries=1 unchanged=false` |
| 2 | refusals | 5 | `nova-update check` -> `CHECK REFUSED: missing --file; refusing to guess; run: nova-update check -h` |
| 3 | output | 5 | `nova-update report --file versions.tsv --json` -> `{"result":{"verb":"report","status":"ok","exit":0},...}` |
| 4 | dry-run | 3 | `nova-update report --file versions.tsv --dry-run` -> `REPORT REFUSED: unknown flag --dry-run; ...; run: nova-update report -h` (`--snapshot` is a local write with no preview; `release build`/`install` list no `--dry-run` either), while `apply --file versions.tsv go --dry-run` -> `APPLY NOTE dry run: nothing installed, nothing written` |
| 5 | idempotent | 2 | `nova-update apply -h` -> flags list has no `--op`; write verbs cannot be retried idempotently by op id |
| 6 | no prompts | 5 | `nova-update report --file versions.tsv` -> `REPORT OK checked=1 ...` (returns without prompting) |
| 7 | exit codes | 5 | `nova-update status --file unk.tsv` -> `STATUS FAILED ... unknown=1` exit=1; refusal exit=2; ok exit=0 |
| 8 | one word | 5 | `nova-update help` -> `the verbs are example, check, status, apply, report, watch, adoption, release, version` |
| 9 | push | 5 | n/a: no polling surface; watch/report deliver by push (send) |
| 10 | no leaks | 5 | `nova-update report --file versions.tsv` -> no line carries a secret; help states provider keys are never printed |
| 11 | true help | 4 | `nova-update version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"payload":"nova-update v1.2.26-...",}`; service-bound claims (watch, release, report --send, report --store) not exercised |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-update apply -h` | write verbs (apply, release cut/build/adopt/pull/cycle, report --snapshot) take no `--op <id>`, so a retried write cannot be made idempotent by op id; a rerun reinstalls instead of returning the recorded result | add `--op <id>` to every write verb and return the recorded result for a repeated id | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-update report --file versions.tsv` | value fields containing spaces print with `\x20` escapes (`raw=go\x20version`), surprising to a reader parsing k=v lines | print literal spaces in value fields, or document the escaping | S |
| 2 | `nova-update report -h` | `report --snapshot` is a write but report takes no `--dry-run`, so its local write cannot be previewed | accept `--dry-run` on report to print the snapshot plan and write nothing | S |

## Top 3 fixes
1. Add `--op <id>` to the write verbs so a retried apply/release/report-snapshot is idempotent by op id (moves criterion 5, 2 to 5).
2. Give `report` a `--dry-run` so its `--snapshot` write can be previewed (moves criterion 4, 3 to 5).
3. Print literal spaces instead of `\x20` escapes in line-rendered value fields (moves criterion 3 readability; already scored 5).

## Good, keep
- Every refusal names its cause and the next command (`run: nova-update <verb> -h`), and a malformed manifest reports every bad line at once, not only the first.
- The output has a single stable shape: a first line `VERB OK|REFUSED|FAILED` with run counts, and `--json` renders the same value, so the two cannot drift.
- The `example` verb writes a real one-tool manifest and the help's first-run path works end to end offline; exit codes 0/1/2 match the help table exactly.

## What could not be tried
- `apply` for real: never installs a binary per this review's brief; only `--dry-run` was run.
- `watch`, `adoption`, `report --send`/`--draft`, `report --store`, and `release` (cut/build/adopt/pull/cycle): need a bus, a store/fleet, or a build checkout this review may not touch; judged from help only.
