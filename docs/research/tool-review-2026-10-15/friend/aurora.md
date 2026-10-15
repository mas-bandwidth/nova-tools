# nova-friend AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-friend status --as aurora --dir ./bob` -> `STATUS NONE: no daemon has run as aurora (...); run: nova-friend install --as aurora --harness <h> --dir ./bob` |
| 2 | refusals | 5 | `nova-friend status` -> `STATUS REFUSED: --as is required; it wants your name; refusing to guess; run: nova-friend help` |
| 3 | output | 5 | `nova-friend version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-friend v1.2.26-0.20261015145010-cc2f2f8c8148 linux/amd64 go1.26.6"}` |
| 4 | dry-run | 2 | `nova-friend resume --as aurora --dry-run` -> `RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 4 | `n/a: no --op flag; writes are single-purpose verbs (resume, uninstall, clear-faults) meant idempotent, but their --dry-run is broken (see #4), so the identical-op check could not be shown clean` |
| 6 | no prompts | 5 | `nova-friend lanes --as aurora` -> `LANES OK lane_fault=none` (returned immediately, no interactive wait) |
| 7 | exit codes | 5 | `nova-friend version` -> `EXIT=0`, `nova-friend status` -> `EXIT=1`, `nova-friend version --bogus` -> `EXIT=2` (match documented 0/1/2) |
| 8 | one word | 5 | `nova-friend help` -> verbs `hook, run, install, uninstall, check, host, reach, pong, watch, status, lanes, refuse-go, resume, acceptance, version` (all one word, no duplicates) |
| 9 | push | 5 | `nova-friend help` -> `beats to the sprint server, whose answer carries the friend's row` (state pushed by beat, not polled) |
| 10 | no leaks | 5 | `nova-friend status --as aurora --dir ./bob --json` -> no token/secret appears; refusal and status lines name only paths and commands |
| 11 | true help | 3 | help says resume `--dry-run` prints what it would do and writes nothing, but `nova-friend resume --as aurora --dry-run` -> `RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-friend resume --as <me> --dry-run` | `RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`, exit 1, instead of the promised `RESUME OK cleared=` dry output | make `resume` honor `Call.DryRun` (branch on it before removing the PAUSED marker) | S |
| 2 | `nova-friend lanes --as <me> --dry-run` | `LANES FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`, exit 1, instead of `--dry-run prints what a clear would do, and writes nothing` | make `lanes` honor `Call.DryRun` (skip clearing lane-fault under dry-run) | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-friend status` (missing flags) | prints two separate `STATUS REFUSED` lines (one for `--as`, one for `--dir`), then exit 2 | merge the missing-flag refusals into one line naming every missing flag and one `run:` | S |

## Top 3 fixes
1. Make `nova-friend resume --dry-run` actually run dry (honor Call.DryRun) — fixes blocker 1 and a false help claim.
2. Make `nova-friend lanes --dry-run` actually run dry (honor Call.DryRun) — fixes blocker 2 and a false help claim.
3. Combine missing-flag refusals into a single line naming all missing flags and the one next command.

## Good, keep
- Every refusal names its cause and the next command (`run: nova-friend help` / `nova-friend <verb> -h`) — ideal for an AI caller.
- `--json` is available on every read verb with a stable `{"result":{...},"facts":{...}}` shape.
- Exit codes are stable 0/1/2 and match the documentation; `status` exit 1 on no-daemon matches its help exactly.
- No 2026-10-02 nova-friend rating exists in docs/ratings, so there is no old defect to compare against.

## What could not be tried
- `install` / `run` / `uninstall`: would start or stop the launchd daemon (forbidden).
- `host`: spawns a tmux session and a terminal harness (starts a service).
- `reach` / `pong` / `watch`: send beats or wake the coordinator (forbidden to beat/wake).
- `acceptance`: runs a full acceptance harness against the daemon.
- `check`: contacts a session or forge; not run.
- `refuse-go`: writes a name to config (a write).
- `run`/`reach`/`pong` real delivery paths (not `--dry-run`): require the daemon or bus store.
- `--dry-run` on `install`/`run`/`host`/`reach`/`pong` judged from their `-h` only, not executed.
