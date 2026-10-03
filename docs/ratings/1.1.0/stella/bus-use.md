# nova-bus USE rating, nova-tools 1.1.0

Rater: gpt-5.6-sol
Build: f3136a624c87
Score: 7/10

## Reasons

The local exchange worked end to end from help alone. `names` confirmed the roster, `draft --out` produced an inspectable file, `send --dry-run` showed the exact note before the real send, the real send pushed to the local bare origin, and `inbox --bodies` in the other clone returned the note and body with stable fields. A second job, recording a receipt, also pushed cleanly and the next full read rendered the note as `INBOX HEARD`. These results are concrete enough for an automated caller to parse and act on.

Refusals are generally excellent. The missing-input run reported both independent omissions in one invocation, the unknown flag suggested the likely flag, the unknown verb listed every verb, and the bad numeric value stated the lower bound and three ways to supply it. Each exited 2. The weak point is the remedy: these paths usually say `nova-bus help`, whose output is several hundred lines, even though focused `help <verb>` exists. That turns an otherwise precise refusal into a search task.

The largest cost is the interface's accumulated surface. The main help is too long for a first turn, a retired draft flag remains visible, and the unknown-flag response truncates the valid flags as `and 6 more`. The offered dry runs are safe, but `reply --dry-run` and `close --dry-run` report ordinary `OK` with `commit=-`; they do not label the result as a dry run, so a caller must infer the lack of mutation from sentinel fields. Help offers no `--json`, so there was no JSON form to try. The verbs not tried were `prepare`, `wait`, and `check`; they were unnecessary for the two local jobs, and no verb required a live service.

A focused comparison probe also found that `inbox --advance --bodies` can report a drained page without advancing, despite focused help saying `--advance` moves the cursor to HEAD: it walked two commits, printed no cursor result, and left the cursor behind the checkout head. That combination is particularly costly because every individual line sounds successful.

A 10 would make the first help a short map, point every refusal at focused verb help, remove retired flags from the visible interface, print every valid flag in an unknown-flag response, add an explicit dry-run fact to plan output, and offer the same structured JSON result for each verb.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus help` | The first help is several hundred lines and combines the quick path with paging, migration, polling, roster, and recovery detail, so the advertised recovery door is costly to search. | Keep the top-level help to purpose, verbs, first setup, and one exchange; move detailed protocols behind focused help topics. | M |
| 2 | `nova-bus inbox --bus ./neutral/writer` | Both missing inputs are named, but each remedy says `nova-bus help` instead of the already available `nova-bus help inbox`, sending the caller back to the very long general page. | Make verb refusals end with the focused help command. | S |
| 3 | `nova-bus inbox --bus ./neutral/reader --as Reader --receipt-max-words 20 --bodies --advance --remote origin --branch main` | Focused help says `--advance` moves the cursor to HEAD, but the run reports `changed=2`, `drained=true`, and `INBOX OK` with no cursor result; before and after, HEAD is `50f48c255a42` while the cursor remains `4dd2d5a25b51`. | Advance to the last fully processed commit when the body page drains, and always print the cursor outcome requested by `--advance`. | M |

## Good, keep

Keep the local Git-only workflow, strict roster, inspectable note files, exact paths and identifiers, and one-line result fields. Keep the all-at-once missing-input validation and the safe send preview that prints the complete shaped note before a real push.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| Inbox reads a stale checkout without saying so | STILL THERE | `nova-bus inbox --bus ./neutral/reader --as Reader --receipt-max-words 20 --full --bodies --open` reports scope and cursor facts but no fetch or freshness fact. |
| `inbox --advance --bodies` does not advance | STILL THERE | `nova-bus inbox --bus ./neutral/reader --as Reader --receipt-max-words 20 --bodies --advance --remote origin --branch main` reports `changed=2` and `drained=true`; HEAD is `50f48c255a42` before and after while the cursor remains `4dd2d5a25b51`. |
| `--advance` pulls unread notes in unmentioned | CHANGED | `nova-bus inbox --bus ./neutral/reader --as Reader --receipt-max-words 20 --full --advance --remote origin --branch main` prints `INBOX HEARD id=writer-62cbb3bfd054` before its cursor result. |
| Receipt path scope is undocumented and misleading on absolute paths | STILL THERE | `nova-bus receipt --bus ./neutral/reader --as Reader --note "$PWD/neutral/reader/from-writer/2026-10-03T1650Z-hello-62cbb3bfd054.md" --remote origin --branch main` says only that the existing file is not a note, without saying paths are repository-relative. |
