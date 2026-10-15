# nova-bus AI-usability review, 2026-10-15

Reviewer: gemini-3.8-flash
Build: de16887455af
Score: 8/10 (44/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 2 | `nova-bus wait --as bob --timeout 1s` -> `WAIT REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help` |
| 2 | refusals | 5 | `nova-bus send` -> `SEND REFUSED: --to is required; it wants the recipients, comma-separated names; refusing to guess; run: nova-bus help` |
| 3 | output | 5 | `nova-bus version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-bus v1.2.26-0.20261015154341-de16887455af darwin/amd64 go1.26.6"}` |
| 4 | dry-run | 3 | `nova-bus overdue --dry-run` -> `OVERDUE REFUSED: unknown flag --dry-run; the flags of overdue are --as, --json, --max, --older, --redis, --timeout; run: nova-bus overdue -h` |
| 5 | idempotent | 2 | `nova-bus send --op 123` -> `SEND REFUSED: unknown flag --op; the flags of send are --as, --body, --cc, --deadline, --dry-run, --json, --kind, --re, --redis, --stdin, --subject, --timeout, --to, --token, --token-cleanup, --token-life; run: nova-bus send -h` |
| 6 | no prompts | 5 | `nova-bus help` -> exits 0 with usage, runs to completion with no interactive prompt |
| 7 | exit codes | 5 | `nova-bus frobnicate` -> `BUS REFUSED: unknown verb "frobnicate"; the verbs are wait, send, peek, recv, ack, receipts, overdue, asks, log, names, version; run: nova-bus help` |
| 8 | one word | 4 | `nova-bus send -h` -> `--token <string>  your word for this one send, the same on every retry of it` (second word for the standard `--op` concept) |
| 9 | push | 5 | `nova-bus wait -h` -> `reads your stream past the cursor with XREAD, never the consumer group` (blocking push on stream, plus `--wake-file`) |
| 10 | no leaks | 5 | `nova-bus names --redis 127.0.0.1:6379` -> `NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: unreachable: dial tcp 127.0.0.1:6379: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-bus help` |
| 11 | true help | 3 | `nova-bus recv --dry-run` -> `RECV NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus wait --as bob --timeout 1s` | `WAIT REFUSED: --redis is required: NOVA_BUS_REDIS is unset...` — every example line in help and all dry-run write invocations fail cold with exit 2 when no store is configured; a new AI cannot execute a single demonstration command without an active Redis store | allow dry-run commands to evaluate against a mock or empty store, or supply a store-free fixture mode so a cold first run succeeds | M |
| 2 | `nova-bus overdue --dry-run` | `OVERDUE REFUSED: unknown flag --dry-run; the flags of overdue are --as, --json, --max, --older, --redis, --timeout; run: nova-bus overdue -h` — overdue delivers timeout messages to expired ask senders but provides no `--dry-run` flag to preview delivery | add `--dry-run` to overdue to inspect and print pending timeouts without dispatching messages | S |
| 3 | `nova-bus send --op 123` | `SEND REFUSED: unknown flag --op; ...` — the standard across tools specifies `--op <id>` for idempotent write retry, but send names its parameter `--token`, and recv, ack, and overdue accept no `--op` flag | accept `--op` on send as an alias for `--token` and support `--op` on remaining write verbs | S |
| 4 | `nova-bus recv --dry-run` | `RECV NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` — when pre-flight validation fails before dialing Redis, an alarming note warns it may have written when nothing was written and dry-run was requested | suppress the misleading note when the verb exits during initial connection validation | S |
| 5 | `nova-bus --json` | `BUS REFUSED: unknown verb "--json"; the verbs are wait, send, peek, recv, ack, receipts, overdue, asks, log, names, version; run: nova-bus help` — help promises that every verb takes `--json`, but passing `--json` at the root fails as an unknown verb | recognize `--json` as a top-level flag or output the banner as JSON | S |
| 6 | `nova-bus send` | required-flag refusals end with `run: nova-bus help` instead of `run: nova-bus send -h`, pointing to the broad root banner rather than the verb's specific flag reference | end per-verb required flag refusals with `run: nova-bus <verb> -h` | S |
| 7 | `nova-bus version -h` | the exit code summary in `-h` is shared verbatim across all verbs, citing verb-specific conditions (`recv: nothing waiting; recv --exec: the command failed; wait: nothing came`) even for inspection verbs and version | tailor the exit code description to each verb | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus names` vs `nova-hook friends` | both tools inspect known identities and their push or hook state | clarify the distinction and cross-reference between the two tools in help | S |
| 2 | `nova-bus wait -h` | flag description for `--wake-after` includes internal implementation jargon ("No replay is an LLM receipt") | replace internal terminology with concise operational instructions | S |

## Top 3 fixes
1. Add `--dry-run` to `overdue` and enable dry-run inspections across write verbs without requiring an active Redis store.
2. Support standard `--op <id>` on `send` and write verbs to align with the repository-wide idempotency convention.
3. Suppress the alarming `it may have written` warning when `--dry-run` fails on pre-flight connection checks.

## Good, keep
The refusal grammar is clear, structured, and informative: every refusal states what was wrong, what the input wants, lists all missing inputs in one run, and provides a runnable next command.
Push semantics over Redis streams with blocking XREAD and `--wake-file` file integration provide true wake notification without wasteful polling loops.
Structured `--json` output is available across every verb, matching the stable line grammar, with ULIDs for immutable message tracking and sha256 digests for payload verification.

## What could not be tried
Live operations against Redis: verbs `wait`, `send`, `peek`, `recv`, `ack`, `receipts`, `overdue`, `asks`, `log`, and `names` could not be executed against an active store because no Redis store is configured in the environment (`NOVA_BUS_REDIS` is unset, `NOVA_SPRINT_REDIS` is unset), and starting a live store or modifying shared peer streams is forbidden by the card rules. They were evaluated through help, flag validation, refusals, and `--dry-run`.
