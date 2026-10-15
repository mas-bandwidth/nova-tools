# nova-hook AI-usability review, 2026-10-15

Reviewer: Mercury 3 preview (inception/mercury-3-preview-1002)
Build: 17b017339e72
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-hook status --as nova --redis 127.0.0.1:6379` -> `STATUS FAILED name=nova state=none role=- epoch=0 reason=unhooked: nova is not hooked in: no hook record (never hooked); ... run: nova-hook status --as nova` |
| 2 | refusals | 5 | `nova-hook hook --as nova --redis 127.0.0.1:6379` -> `HOOK REFUSED: --role is required; ... refusing to guess; run: nova-hook help` |
| 3 | output | 5 | `nova-hook status --as nova --redis 127.0.0.1:6379 --json` -> `{"result":{"verb":"status","status":"failed","exit":1,"remedy":"nova-hook status --as nova",...},"facts":{...},"notes":[...]}` |
| 4 | dry-run | 3 | `nova-hook serve --as nova --once --dry-run --redis 127.0.0.1:6379` -> `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 3 | `nova-hook hook --as nova --role comms --dry-run --redis 127.0.0.1:6379` -> `HOOK DRY-RUN as=nova role=comms epoch=1 joined=false hold=bus dry_run=true` (no --op surface; real writes not run to verify) |
| 6 | no prompts | 5 | `nova-hook status --as nova --redis 127.0.0.1:6379` -> runs to completion with no interactive wait |
| 7 | exit codes | 5 | `nova-hook version` -> exit 0; `nova-hook bogus` -> exit 2; `nova-hook status --as nova` -> exit 1 (matches documented 0/1/2) |
| 8 | one word | 5 | `nova-hook help` -> `the verbs are status, hook, prove, unhook, friends, serve, version` (distinct single words) |
| 9 | push | 5 | `nova-hook serve -h` -> `every change of a friend's state ... is pushed to the coordinator at once, in that pass, as its own bus message` |
| 10 | no leaks | 5 | `nova-hook status --as nova --redis 127.0.0.1:6379` -> output carries no secret material |
| 11 | true help | 3 | `nova-hook serve --as nova --once --dry-run --redis 127.0.0.1:6379` -> `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` (help says --dry-run "writes nothing") |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-hook serve --as nova --once --dry-run --redis 127.0.0.1:6379` | `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` — help claims --dry-run "plans one pass now and writes nothing", but the serve handler never reads the dry-run flag and warns it may have written | pass Call.DryRun into the serve handler so --dry-run returns the plan and writes nothing | M |
| 2 | `nova-hook status --as nova` | `STATUS REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row ... cannot be read either; refusing to guess; run: nova-hook help` — a cold run finds no redis address in help (only env-var names) and is told to guess nothing | when the env is unset, attempt and report the default bus address in the refusal and in help | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-hook hook -h` | write verbs hook/prove/unhook/serve take no --op flag, so re-run idempotency cannot be verified cold | document re-run semantics (join vs new epoch) explicitly in help | S |
| 2 | `nova-hook help` | the resolved default redis address is not stated, only the env vars that feed it | print the resolved default when it differs from empty | S |

## Top 3 fixes
1. Wire `serve --dry-run` through the handler: it currently never reads Call.DryRun and warns it may have written, breaking both the dry-run and true-help guarantees (criterion 4 and 11).
2. Make `status` (and friends) resolve and report the actual bus address when the env is unset, instead of "refusing to guess", so a cold first run can proceed (criterion 1).
3. Name the re-run/idempotency semantics of hook/prove/unhook in help so an AI can predict running the same op twice (criterion 5).

## Good, keep
- Every refusal names its cause and the next command to run, and exit codes match the documented 0/1/2 meanings.
- Every verb takes --json with a stable shape ({result: {verb,status,exit,remedy,why}, facts, notes}) and a stable line grammar in text form.
- hook/prove/unhook --dry-run print a true plan and write nothing (the store state was unchanged after running them).

## What could not be tried
- Real `hook`, `prove`, `unhook` and `serve` (they write live hook/bus state, which this card is forbidden to touch): judged from help and --dry-run only.
- The `serve` loop (requires an identity hooked in as coordinator or comms, which would itself be a live write).
- `--max`/MORE paging (no names present in the store to list past the summary).
