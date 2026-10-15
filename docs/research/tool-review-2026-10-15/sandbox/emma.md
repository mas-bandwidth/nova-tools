# nova-sandbox AI-usability review, 2026-10-15

Reviewer: Mercury-3-preview-1002
Build: 8532dea53788
Score: 7.0/10 (38/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-sandbox help` -> `nova-sandbox: run one command inside an OS-enforced wall around the directories you name`; the banner, per-verb `-h` and exit table are self-contained and got me to working verbs, but the documented first-run wrap opens a `sandbox-exec: sandbox_apply: Operation not permitted` and never runs the command |
| 2 | refusals | 5 | `nova-sandbox bogus` -> `SANDBOX REFUSED reason=unknown_verb: unknown verb "bogus"; available: check, egress, policy, probe, reap, run, version, worktree; run: nova-sandbox help`; every refusal names its cause and a `run:` next command, and `worktree` and `policy` report every problem at once |
| 3 | output | 4 | `nova-sandbox check --json` -> `{"result":{"verb":"check","status":"ok","exit":0},"facts":{"backend":"sandbox-exec",...}}`; lines are a stable `VERB OK/REFUSED/FAILED key=value` grammar and `--json` matches it on check, probe and policy, though a failed wrap leaks the raw `sandbox-exec:` line with no structured cause |
| 4 | dry-run | 1 | `nova-sandbox reap --dry-run` -> `SANDBOX STEP name=list state=done ms=14`; `reap` is the only writable verb with `--dry-run`, and `run`, `worktree`, `egress plan` and `egress apply` offer none |
| 5 | idempotent | 0 | `nova-sandbox run -h` -> no `--op` flag on any verb; no write is idempotent by an op id |
| 6 | no prompts | 5 | `nova-sandbox check` -> `CHECK OK backend=sandbox-exec abi=- net=enforceable hosts=none`; every verb ran to completion without reading stdin |
| 7 | exit codes | 4 | `nova-sandbox check --bogus` -> exit 2; `nova-sandbox run --name j1 --size banana -- /bin/true` -> exit 125; `nova-sandbox probe --write <scratch>` -> exit 1; these match the help's table, but `reap --dry-run` exited 125 here while `reap -h` lists only 0 and 3 |
| 8 | one word | 3 | `nova-sandbox egress -h` -> `usage: nova-sandbox egress <plan|apply|check|drop>`; the `egress check` sub-verb duplicates the top-level `check` verb word |
| 9 | push | 5 | `n/a: nova-sandbox wraps a child process synchronously and never polls remote or shared state` |
| 10 | no leaks | 5 | `HOME=<scratch>/home nova-sandbox probe --write <scratch>` -> `PROBE STEP name=read_secret expect=deny got=deny`; no output line carried a secret, and probe never reads the `--secret` file's contents |
| 11 | true help | 2 | `nova-sandbox --write <scratch> -- /bin/echo hello` -> `sandbox-exec: sandbox_apply: Operation not permitted` (exit 71, command never ran), while `nova-sandbox check` -> `CHECK OK ... net=enforceable`; the enforced/writable claims do not hold on this machine |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox run -h`, `nova-sandbox worktree -h`, `nova-sandbox egress plan -h` | `run`, `worktree`, `egress plan` and `egress apply` all write real state (an APFS volume, worktrees, a ruleset file) yet refuse `--dry-run` (`unknown flag --dry-run`); a caller cannot preview a write before letting it happen, and `reap --dry-run` is the only preview in the tool | Add `--dry-run` to `run`, `worktree`, `egress plan` and `egress apply`, printing the plan from the same code path and writing nothing | M |
| 2 | `nova-sandbox check` then `nova-sandbox --write <scratch> -- /bin/echo hello` | `check` prints `CHECK OK ... net=enforceable` and exits 0, but the very next wrap fails with `sandbox-exec: sandbox_apply: Operation not permitted` and the command never runs; an AI trusts the green and gets a silent no-op | Have `check` (and the wrap) attempt a minimal backend apply and surface a distinct warning or refusal when the wall cannot be applied, instead of a green enforceability claim | M |
| 3 | `nova-sandbox --write <scratch> -- /bin/echo hello` | The wrap prints `SANDBOX OK` (wall up), then the raw `sandbox-exec: sandbox_apply: Operation not permitted` line, then `SANDBOX DONE exit=71 cmd=echo` with no machine-readable failure line; a parser sees OK then DONE(71) with no cause | On a backend apply/exec failure, emit one structured `SANDBOX FAILED reason=...` line with the remedy before `DONE`, and do not print `OK` when the wall did not come up | S |
| 4 | `nova-sandbox probe` | `PROBE REFUSED reason=check: --write is required and has no default` uses `reason=check` — a token naming the verb, not the cause class — so a machine cannot route the refusal | Use distinct reason tokens for the cause (`missing_write`, `home_outside`, `bad_read` ...) on every refusal | S |
| 5 | `nova-sandbox reap --dry-run` | `reap -h` promises `0 clean, 3 something remained`, but here `reap --dry-run` exits 125 with `reason=volume_failed: the volumes on this machine could not be listed: diskutil apfs list: exit status 1: framework being unavailable due to being booted in single-user mode`; the promised end-of-card gate collides with an unlisted exit | Document exit 125 in `reap`'s exit table (and for a machine that cannot answer), so the gate's contract matches what an AI sees | S |
| 6 | `nova-sandbox --bogus -- /bin/true` then `nova-sandbox --json --write <scratch> -- /bin/echo hi` | The bare form's unknown-flag refusal lists `--secret`, `--json` and `--max` among `the flags are ...`, and passing `--json` to the bare form is then refused with `SANDBOX REFUSED reason=no_command: --json is not a flag of the bare form; it is check, policy and probe's` (carried from the 2026-10-02 ratings) | List only the bare form's valid flags in its unknown-flag refusal, and use `reason=bad_flag` instead of `reason=no_command` when a verb-only flag is given to the bare wrap | S |
| 7 | `nova-sandbox egress plan -h` | The example `nova-sandbox egress plan --run r1 --policy infra/image/egress.txt --model-host api.example.com --uid 1001 --out /tmp/r1.nft` omits the required `--resolver <ip>`, so a pasted first run is refused (carried from the 2026-10-02 ratings) | Add `--resolver` to the example line in `egress -h` and `egress plan -h`, and run every example line in a test | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox egress -h` | the `egress check` sub-verb repeats the top-level `check` verb word, a one-word-rule collision | Rename the sub-verb (for example `egress verify`, or fold it into `plan --check`) | S |
| 2 | `nova-sandbox policy` with a bad `--read` | the `home_outside` remedy builds one long `mkdir -p ... && HOME=... nova-sandbox policy ...` command repeating every flag; effective but very long | Keep the remedy but print it on one wrapped line and do not repeat unchanged flags | S |

## Top 3 fixes
1. Add `--dry-run` to `run`, `worktree`, `egress plan` and `egress apply` — the single change that lifts the dry-run criterion most.
2. Make `check` (and the wrap) detect when the sandbox backend cannot be applied and refuse or warn instead of printing a green `net=enforceable` — restores true-help trust and unblocks cold first runs.
3. Unify refusal reason tokens (`bad_flag`, `missing_write`, `home_outside`, `volume_failed` ...) and emit a structured `SANDBOX FAILED` line on backend failure, so every refusal and failure is machine-routable.

## Good, keep
The refusal grammar is excellent: every refusal names its cause and a `run:` next command, and `worktree`/`policy`/`egress` report every problem at once. The `key=value` line grammar is stable and `--json` matches it on check, probe and policy. `probe` is honest: when the wall cannot be applied it exits 1 and says so, and it never reads the `--secret` file's contents. Two earlier-rated defects are still present on this build and are logged as blockers 6 and 7: the bare form's unknown-flag list advertises verb-only flags, and the `egress plan` example omits the required `--resolver`.

## What could not be tried
- `run`: creating the disposable APFS volume and deleting it is a boot-container side-effect outside the job, so it was judged from `run -h` and its refusals only.
- `reap` beyond its refusal: `diskutil apfs list` is unavailable on this machine (booted single-user), so no volume list could be produced.
- `egress apply` and `egress drop`: they are linux-only (nftables) and refused here on darwin with a clear remedy.
- `egress plan` to completion: it resolves names over DNS and this host's resolver at 127.0.0.1:53 answers no such host, so the `resolve` step fails before `--out` is written; the plan path was exercised through its (high-quality) refusals.
- End-to-end wall enforcement: `sandbox-exec` cannot apply in this nested environment (`Operation not permitted`), so the contained command's behavior could not be observed; the wrap's refusals and `probe` were exercised.
