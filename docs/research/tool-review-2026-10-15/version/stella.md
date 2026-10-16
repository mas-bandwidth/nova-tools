# nova-version AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: fb84d5d9fec
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-version example --out versions.tsv` -> `EXAMPLE OK wrote=versions.tsv entries=1 unchanged=false` |
| 2 | refusals | 4 | `nova-version diff` -> `DIFF REFUSED: --from is required; ... run: nova-version help` reports all missing flags, but `report --kind bogus --max -2` names only the invalid --kind and not --max |
| 3 | output | 5 | `nova-version diff --from a.tsv --to b.tsv` -> `DIFF OK from=a.tsv to=b.tsv tools=22 changed=1`; `--json` works on example, version, snapshot and diff |
| 4 | dry-run | 3 | `nova-version snapshot --bin <bin> --dry-run --out dry.tsv` -> `SNAPSHOT NOTE dry run: dry.tsv not written`; example and send have no dry-run |
| 5 | idempotent | 1 | no write verb takes `--op`; report dedups via `--snapshot` state file but moved and snapshot overwrite on repeat |
| 6 | no prompts | 5 | every command ran non-interactively to completion, exit 0 |
| 7 | exit codes | 5 | `nova-version report --file bad.tsv` -> `REPORT FAILED checked=1 known=0 unknown=1` exit 1; usage refusals exit 2 |
| 8 | one word | 5 | verbs example, moved, snapshot, diff, report, send, version are distinct single words |
| 9 | push | 5 | n/a: no shared state is awaited; report reads installed versions one-shot by executing them |
| 10 | no leaks | 5 | no output line leaks a secret; the UNKNOWN remedy prints PATH, which is environment, not a secret |
| 11 | true help | 4 | the example block ran as printed and the exit table matched, but report's effect claims no write without --send while `report --snapshot state.json` writes state.json and state.json.lock |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-version report --file versions.tsv --snapshot state.json` | help says `without --send, report reads and writes nothing`, but the run writes `state.json` and `state.json.lock` anyway, an unexpected write plus a false help claim | make `--snapshot` write only under `--send`, or correct the help effect line to name `--snapshot` as a local write | S |
| 2 | `nova-version snapshot --bin <bin> --out a.tsv`, `nova-version moved --from ... --to ... --repo ... --out note.txt`, `nova-version send --file ... --as ... --to ...` | no verb takes `--op <id>`, so a retry after a timeout re-runs the write (snapshot rewrites, moved rebuilds, send re-delivers); only report dedups via its `--snapshot` state file | add `--op <id>` to every write verb, record the result under the id, and replay it unchanged for the same id and arguments | M |
| 3 | `nova-version report --file versions.tsv --kind bogus --max -2` | the refusal names only the invalid `--kind` and says nothing about the invalid `--max`, though the manifest help promises every problem at once | report all invalid flag values in one run | S |
| 4 | `nova-version example --out x.tsv` | example is a local write with no `--dry-run`; a cold caller cannot preview the file it will write | add `--dry-run` to example that prints the manifest and writes nothing | S |
| 5 | `nova-version send --file <manifest> --as <friend> --to <who,who>` | send delivers beyond the machine and has no `--dry-run`; the caller cannot see the note before it is sent | add `--dry-run` to send that prints the note body and does not deliver | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-version <verb> -h` | every verb repeats the full five-verb exit table, so the relevant exit meaning is buried in unrelated verbs | print the called verb's exit contract, or a compact shared table once from top-level help | S |
| 2 | `nova-version report --snapshot` | one word, `snapshot`, means both the four-column binary inventory and the report delivery-state JSON file | rename the report state flag to `--state` while retaining a documented compatibility alias | M |
| 3 | `nova-version snapshot --bin <bin> --dry-run` | dry-run still demands `--out`, so the preview cannot be requested without naming an output file | allow `--dry-run` without `--out` | S |
| 4 | `nova-version report --file bad.tsv` | the UNKNOWN remedy prints the whole PATH, which is long and noisy for a cold reader | omit PATH from the remedy line | S |
| 5 | `nova-version moved --dry-run` | moved builds both revisions in full before printing the note, a slow first probe for a new user | state the expected cost in moved's help | S |

## Top 3 fixes
1. Fix report's `--snapshot` write-vs-help contradiction (unexpected write and false help claim).
2. Add `--op` to the write verbs (moved, snapshot, send/report --send) and replay recorded results.
3. Report all invalid flag values in one refusal (--kind and --max together).

## Good, keep
The bare invocation and every refusal name the cause and the next door (`run: nova-version help` / `run: nova-version <verb> -h`), and missing flags are reported all at once. The one line grammar (`VERB OK|FAILED|REFUSED key=value`) is stable and `--json` is offered on every read verb and matches the line output. The build identity is now fully populated (version stamp and snapshot rows carry the VCS revision), the old `devel`/`revision=-` defect is gone. Exit codes are meaningful (0 done, 1 failed/UNKNOWN, 2 usage) and match the help table.

## What could not be tried
`send` and `report --send` deliver beyond this machine through nova-bus and were not run; they are judged from their help and refusals (`SEND REFUSED: missing --file, --as, --to; run: nova-version send -h`). `moved` was run only with `--dry-run`, which prints the note and writes it.
