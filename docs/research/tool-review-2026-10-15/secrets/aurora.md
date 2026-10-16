# nova-secrets AI-usability review, 2026-10-15

Reviewer: Mercury (mercury-3-preview-1002)
Build: cc2f2f8c8148
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-secrets` -> `SECRETS REFUSED: no arguments is not an invocation; run: nova-secrets help`; `nova-secrets help` gives a full setup block and example block that run |
| 2 | refusals | 5 | `nova-secrets check` -> `SECRETS CHECK REFUSED: missing --store <dir>, --as <name>, --key <path>, --sops <path>; run: nova-secrets check -h` |
| 3 | output | 5 | `nova-secrets names --store ./secrets --as ada --json` -> `{"result":{"verb":"names","status":"ok","exit":0},"facts":{"as":"ada","keys":1,"shown":1,"sealed":1,"clear":0},"items":[{"kind":"key","fields":{"key":"GH_TOKEN","clear":false}}]}` |
| 4 | dry-run | 5 | `nova-secrets seal --store ./secrets --as ada --key ... --sops ... --name GH_TOKEN --stdin --no-pr --dry-run < /dev/null` -> `SECRETS SEAL DRY-RUN OK name=GH_TOKEN seat=ada nothing written, no value read, no push, no gh call`, and `git -C ./secrets status --short` stays empty |
| 5 | idempotent | 0 | `nova-secrets help` lists no `--op` flag on any write verb (seal, place, seat inject); a write has no op id, so a retry cannot be deduplicated |
| 6 | no prompts | 4 | `nova-secrets seal --help` -> `reads one value at a hidden prompt (or --stdin)`; the default waits on an interactive answer, though `--stdin` is the documented, non-interactive path |
| 7 | exit codes | 5 | `nova-secrets check ...` (failure) -> exit 1; `nova-secrets exec ...` (refused) -> exit 125; `nova-secrets` (bare) -> exit 2; `nova-secrets names ...` -> exit 0; all as help states |
| 8 | one word | 5 | verbs are version, exec, names, check, gate, keygen, place, placed, seal, seat add, seat inject, help; each names one thing, no duplicate verbs or tools |
| 9 | push | 5 | seal and seat inject push, open a pull request and merge; `SECRETS SEAL PLAN push none (--no-pr)` shows the no-PR path; nothing is polled |
| 10 | no leaks | 5 | `nova-secrets seal ... --dry-run` -> `value=not read (dry run)`; exec/names print no value; every evidence line is free of a secret |
| 11 | true help | 4 | `nova-secrets keygen --as ada --key ... --age-keygen ... --store ./secrets` -> `SECRETS KEYGEN REFUSED: store ./secrets has no .sops.yaml`, while the first-run help creates .sops.yaml by piping keygen's own output; the ordering only works because the shell's redirection opens an empty file first |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets help` -> write verbs | no write verb accepts an `--op` id, so a timed-out retry is not idempotent and can write twice; the tool's own standard says a write verb takes `--op` | add `--op` to seal, place and seat inject, recording and returning a prior result for the same op id | M |
| 2 | `nova-secrets seal --help` | seal by default waits at a hidden terminal prompt; an AI must remember to add `--stdin` or the run hangs | make `--stdin` the default when stdin is not a terminal, or refuse with a reminder to pass `--stdin` | S |
| 3 | `nova-secrets keygen --as ada --key ... --store ./secrets` | refuses `store ./secrets has no .sops.yaml`, but the first-run docs create .sops.yaml from keygen's own output, a chicken-and-egg that fails when run stepwise | have keygen `--store` accept a store whose .sops.yaml is absent (first seat), or print the rule block before the store check so the documented pipe works | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets exec --json` | exec refuses `--json` as an unknown flag, though the repository standard says every verb accepts `--json`; check also offers no `--json` | add `--json` to exec and check for output parity with names | S |
| 2 | `nova-secrets names --help` | the `--json` flag is documented only under names, so a reader must guess which verbs take it | list `--json` availability in each verb's help | S |

## Top 3 fixes
1. Add `--op` idempotency to the write verbs (seal, place, seat inject); it costs the whole idempotent criterion and is the single biggest score loss.
2. Make seal prompt-free for AIs (default `--stdin` when stdin is not a terminal, or a refusal naming `--stdin`).
3. Fix the keygen `--store` chicken-and-egg so the documented first run works stepwise.

## Good, keep
- Refusals are exemplary: every one names its cause and the next command to run, and a single run reports every independent problem at once.
- The banner answers the three questions (what, how, first run) and the first-run/example blocks are real commands, not sketches.
- No secret value is ever printed: dry-runs print `value=not read` and exec/names never echo a value, which is the right default for a secrets tool.

## What could not be tried
- A fully green `exec`/`check` end-to-end run: the environment's age-keygen and sops binaries are present but not readable (exec permission denied), so I used shim replacements whose age1 public keys fail the tool's recipient validation, and the checks correctly refused them.
- A real `seal`/`seat inject` pull-request flow and `place` over ssh (needs a working gh/ssh/sops network); only the documented dry-runs and refusals were exercised.
- seal's interactive terminal prompt path, deliberately not run; the `--stdin` path was used instead.
