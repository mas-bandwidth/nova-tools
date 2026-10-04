# nova-cairn USE rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash
Build: 0c5803c2de40
Score: 8.5/10

## Reasons
nova-cairn is a local, plain-file session store, and every advertised verb was tried end to end in a job-local store under scratch: open, append, index, receipt and version all ran with no network, no remote, no database, no model and no key. The rated source is exactly 0c5803c2de406c1b0b2b0841f579c9bf73406b1c; the `version` line reads `nova-cairn v1.0.1-0.20261003234940-0c5803c2de40 darwin/arm64 go1.27.1`, so the binary is proven to be this snapshot. No verb needs a real service, so nothing was left untried for infrastructure reasons; the store is a directory of plain files (`entries/<session>/<entry>.json`, `log.jsonl`, `sessions/<session>.md`).

The help is the strongest part for an AI. `nova-cairn help` gives the model, the exit codes (0 pass, 1 conflict, 2 bad invocation), an `effect:` line per verb (local write versus inspection) and a four-command first run that works verbatim. Every refusal names the problem, states all problems at once, and ends with the next command to run. `--json` is accepted by every verb and returns exactly one object on stdout with a consistent `result`/`facts`/`items` shape; `--dry-run` is not offered by any verb, and passing it is correctly refused as an unknown flag.

What costs the score: `index` reports a session count but never lists a session id, so a session with no entries cannot be discovered, and `--session`/`receipt` need an id the tool will not surface. Re-opening an existing session prints `OPEN OK` with the requested publish policy while persisting nothing, so the output asserts a policy change that did not happen. Two smaller issues: `index --session <id>` still counts every session in the store, and `help <verb> <extra>` parses the second word as a positional argument to the first verb instead of refusing the extra word.

A 10 would need index to enumerate sessions, re-open to be truthful (report the stored policy and say nothing changed, or actually update it), and the filtered count to match the filter. The score reflects a genuinely excellent local tool whose remaining defects are discoverability and one trust edge case, not missing core function.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn index --store ./st` (store with one empty session) | prints `INDEX OK sessions=1 entries=0` and no session line; session ids are undiscoverable when a session has no entries, yet `--session` and `receipt` require one | emit an `INDEX SESSION session=<id>` line per session and add the session to the JSON items | M |
| 2 | `nova-cairn open --store ./st --session empty --publish manual` after the same session opened with `--publish never` | prints `OPEN OK ... publish=manual` but `sessions/empty.md` still says `Publish: never` and `log.jsonl` gains no line; the report claims a change that did not happen | make re-open report the stored policy with `existing=true`, or actually record the new policy | M |
| 3 | `nova-cairn index --store ./st --session alpha` (store with sessions alpha and beta) | prints `sessions=2 entries=2` while listing only alpha; the session count ignores the filter | count only the filtered session, or label the count as store-wide | S |
| 4 | `nova-cairn open --store ./st --session s --publish never --dry-run` | refused as unknown flag; the two writing verbs offer no way to preview a write | accept `--dry-run` on open and append to print the effect without writing | S |
| 5 | `nova-cairn help open append` | the second word is parsed as a positional argument to `open`, so three missing-flag errors plus a positional error appear instead of one extra-word refusal | refuse `help` with more than one verb, naming the extra word | S |

## Good, keep
- Refusals name the problem, list every problem at once, and end with the exact next command (`run: nova-cairn help` or `run: nova-cairn index -h`).
- The advertised first run works byte for byte as printed, and exact-text retrieval via `receipt --text` and `--json` round-trips the stored bytes.
- Every verb takes `--json` with one consistent object, and `index --json --max` adds a `more` element with a `remedy`, so output can be acted on without guessing.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| index cannot list sessions (USE 2026-10-02 at 1aac13259, rater 9) | STILL THERE | `nova-cairn index --store ./st` on a store whose only session is empty prints `INDEX OK sessions=1 entries=0` with no session line |
| a re-open looks like a first open (USE 2026-10-02 at 1aac13259, rater 9) | STILL THERE | `nova-cairn open --store ./st --session empty --publish manual` prints `OPEN OK ... publish=manual`, while `cat st/sessions/empty.md` still shows `Publish: never` and `log.jsonl` is unchanged |