# nova-sandbox AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (51/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-sandbox` -> `SANDBOX REFUSED reason=no_command: ... run: nova-sandbox help`; the help example ran clean |
| 2 | refusals | 5 | `nova-sandbox --nonsense --write <dir> -- /bin/true` -> `SANDBOX REFUSED reason=bad_flag: unknown flag --nonsense; ... run: nova-sandbox help` |
| 3 | output | 5 | `nova-sandbox check --json` -> `{"result":{"verb":"check","status":"ok","exit":0},...}` stable key=value lines elsewhere |
| 4 | dry-run | 5 | n/a: the tool's only write is the wrapped command's own; reap --dry-run exists and on linux refuses truthfully (no disposable body) |
| 5 | idempotent | 5 | n/a: it wraps arbitrary commands and owns no state; the same wrap twice produced identical setup lines |
| 6 | no prompts | 5 | `nova-sandbox` -> exit 125 with a REFUSED line, never a question; every path is a flag |
| 7 | exit codes | 3 | help says `126 the command could not be executed`, but `nova-sandbox --write <dir> -- <nonexec.sh>` -> `SANDBOX REFUSED reason=not_executable ...` exits 125; 127 for a command on no PATH matches |
| 8 | one word | 5 | verbs are single words: check, egress, policy, probe, reap, run, version, worktree; each flag names one thing, no duplicates |
| 9 | push | 5 | n/a: no wait/poll surface exists; reap is a cleanup verb for darwin only |
| 10 | no leaks | 5 | `nova-sandbox probe --write <dir> --secret <path>` -> `PROBE STEP name=read_secret expect=deny got=deny`; output lines carry only paths |
| 11 | true help | 3 | help claims exit 126 and lists `--json` for the bare form; both are false when run (`reason=not_executable` exits 125; `--json` on the bare form is refused) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox --write <dir> -- <nonexec.sh>` | `SANDBOX REFUSED reason=not_executable: ... carries no executable bit ...` exits 125, while help states `126 the command could not be executed` | align help to say 125 for not_executable, or make the code return 126 | S |
| 2 | `nova-sandbox --nonsense --write <dir> -- /bin/true` | the refusal lists `--json` among the bare form's flags, then `nova-sandbox --json --write <dir> -- /bin/true` is refused `reason=no_command: --json is not a flag of the bare form` | print a form-specific flag list and refuse `--json` as reason=bad_flag | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox version --json` | refusal `version takes no flags and no arguments, got 1` exits 2; fine but undocumented | add a line to help that version takes no flags | S |
| 2 | `nova-sandbox probe` control step | writes its control file in the parent of the first `--write`, a directory never named | say so in probe -h, or put the control file in a temp directory | S |

## Top 3 fixes
1. Fix the exit-code table: not_executable returns 125, not help's 126.
2. Form-specific flag lists in the unknown-flag refusal (the bare wrap's `--json` mismatch).
3. Document that version takes no flags, so `version --json` is not a surprise.

## Good, keep
- Every refusal names its cause and the next command, and most print the exact corrected invocation.
- SANDBOX OK/DONE lines and PROBE/CHECK step lines share a stable `key=value` grammar with `--json` on check, policy and probe.
- The wrapped command's real exit status is passed through (exit 3 -> DONE exit=3) and told from the tool's own refusal codes; the two old 1.2.0 defects that persist are the exit-code 125/126 mismatch and the `--json` flag-list listing; probe, policy and the net and path claims all hold.

## What could not be tried
- run, reap, and egress apply/drop are darwin- or bench-specific; judged from help and from their truthful linux refusals, not run.
- worktree would mutate a scratch repo; not run (read-only review), judged from help.
- egress plan needs the reviewed allowlist and nftables infrastructure; not run, named from help.
