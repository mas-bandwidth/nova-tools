# nova-cairn USE rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 2c02b2aa2042
Score: 8.5/10

## Reasons

Cold, from the binary alone. `nova-cairn help` answers in one sitting: what it
does, how it works in five lines, the four verbs, the exit table, and an
example block that runs as printed. The first run is the example verbatim —
open, append, index, receipt — and it works first try, exit 0, against a store
made under scratch, no network, no key. Two real jobs succeed end to end: a
decision log (open with a source pointer, three appends, a duplicate retry that
reports duplicate=true, a conflict that exits 1 and names the remedy "pick a
new id", index, receipt --text), and a hand-written markdown record (open is a
no-op, append lands a dated section with no sidecar, receipt reports
publish=unknown). The refusals are the best part: a missing flag names what it
wants, one run reports every missing flag at once (three for a bare open), an
unknown flag lists the valid flags, an unknown verb lists the verbs, a bad value
lists the valid values, and an append into a session neither shape holds prints
the whole remedy verb to paste. --json gives one object with result, facts and
items and, on a refusal, a why array naming every problem, so a program can act
without parsing lines. --max prints MORE with its remedy and the first line
keeps the uncapped total. No verb needs a real service: every verb runs against
a local store, so nothing was left untried; the only remote gesture is
--publish, which this slice records but never acts on (observed published=false).

Where I had to guess. A re-run of open prints the same OPEN OK as the first
run, with a fresh stamp and no word that says it was a no-op, so I could not
tell whether a record was created or already stood. index reports a session
count but no session ids, so a session with no entries is invisible — I could
not discover my own sessions from the tool. --dry-run is offered nowhere, so I
could not preview an append before letting it write. And receipt --text renders
the stored words with \x20 for each blank between words and \x0a for each
newline in the typed line, so the words read plainly only through --json.

A 10 would need: a way to list session ids, a no-op signal on re-open, a
--dry-run on the write verbs, and a plain rendering of receipt --text.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn index --store ./cairns` | index reports a session count but no session ids, so an empty session is invisible and a reader cannot enumerate its own records | add a session row or a --sessions listing so every session id is printable | M |
| 2 | `nova-cairn open --store ./cairns --session full --publish never` | a re-open prints the same OPEN OK as a first open with a fresh stamp and no no-op word, so a retry cannot tell whether a record was created or already stood | add a noop or already-open fact on re-open so the retry is distinguishable | S |
| 3 | `nova-cairn open --store ./cairns --session s1 --publish never --dry-run` | the write verbs open and append offer no --dry-run, so a reader cannot preview a write before letting it land | set DryRun on open and append and return the plan from the same code path | S |

## Good, keep

Keep the refusal grammar: one run names every problem, and a use-before-open prints the whole remedy verb to paste.
Keep --json as the same value as the lines: on a refusal the why array names every problem, so a program can act without guessing.
Keep the duplicate/conflict split: a retry is duplicate=true with the stored stamp; different prose under one id is a conflict, never an overwrite.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| index cannot list sessions | STILL THERE | `nova-cairn index --store ./cairns` prints `INDEX OK sessions=2 entries=1` then only the non-empty session's entry; no line names the empty session |
| a re-open looks like a first open | STILL THERE | running `nova-cairn open --store ./cairns --session full --publish never` twice prints `OPEN OK session=full ... stamp=2026-10-03T16:38:24.685359Z` then `OPEN OK session=full ... stamp=2026-10-03T16:38:55.633886Z`, identical shape, no no-op word |
