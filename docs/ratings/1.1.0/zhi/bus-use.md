# nova-bus USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 2c02b2aa2042
Score: 7/10

## Reasons

Everything was run in a scratch directory against a local bare origin and two clones, never a real remote. The first successful run is `nova-bus names --bus ./ada`, which prints the roster and exits 0. The first real job end to end — Ada drafts and sends a note, Bo reads it, Bo replies, Ada reads the reply — works, and a second, different job (Ada sends a status note, Bo marks it heard with `receipt`) works too. The four provoked refusals are the best part of this tool: a missing required flag reports every missing flag at once (`--branch is required; --remote is required`), an unknown flag names the flags there are, an unknown verb names all ten verbs, and a bad value names the value and its remedy, each exit 2 in one line.

The cost is carried by two things. First, a reader on a fresh clone is lied to: the first `--full --advance` walks the local checkout before it fetches the remote, so on a clone made before a note was pushed it prints `changed=0` and `notes=0` and sets the cursor to a stale commit; the note appears only on the next read. A cold AI would conclude "nothing new" when there are notes. Second, there is no `--json` on any verb — every one refuses it — so a harness that wants machine-readable output has to parse the one-line grammar, and the tool breaks the "one output structure, two renderings" rule the rest of the family keeps. `--dry-run` is good where it exists: `send --dry-run` prints the shaped note framed by `SEND DRAFT`/`SEND DRAFT END` and writes nothing, and `close --dry-run` reports the split with `commit=-`.

What a 10 would need: fetch the remote before walking on every read (or refuse a stale checkout by name), add `--json` on every verb, and make `--bodies --advance` actually advance.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus inbox --bus ./bo --as Bo --receipt-max-words 40 --full --advance --remote origin --branch main` | on a fresh clone the first read prints `changed=0` and `notes=0` and sets the cursor to a stale commit, silently missing notes already pushed; they appear only on the next read | fetch the remote before the walk (or refuse a checkout behind origin by name) | M |
| 2 | `nova-bus names --bus ./ada --json` | `--json` is refused on every verb; the family rule that every verb accepts it is broken, so a harness must parse the line grammar | add the one-result-two-renderings path the sibling tools share | M |
| 3 | `nova-bus inbox --bus ./bo --as Bo --receipt-max-words 40 --bodies --advance --remote origin --branch main` | with `--bodies`, `--advance` prints no `INBOX CURSOR` line and leaves the cursor behind even when a note is pending | make `--bodies --advance` advance past the whole commit it prints, as non-bodies does | S |

## Good, keep

The refusal grammar — one run reports every problem, names what the input wants, and prints the next command. The `wait --timeout` rearm line that hands back the exact command to run again. The `send --dry-run` framed draft.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| inbox reads a stale checkout without saying so | STILL THERE | `nova-bus inbox --bus ./bo --as Bo --receipt-max-words 40 --full --advance --remote origin --branch main` printed `INBOX SCOPE mode=full cursor=- changed=0 carrying=0` and `INBOX OK ... notes=0` with two notes already pushed; the next read printed `notes=2` |
| inbox --advance --bodies does not advance | STILL THERE | `nova-bus inbox --bus ./bo --as Bo --receipt-max-words 40 --bodies --advance --remote origin --branch main` printed no `INBOX CURSOR` line and the cursor stayed behind while a note was pending |
| --advance pulls unread notes in unmentioned | STILL THERE | a plain `--advance` pulled the new note commit into the checkout but printed `INBOX OK ... notes=2`, never mentioning the third note that was on the remote |
| the receipt path scope is undocumented and misleading on absolute paths | STILL THERE | `nova-bus receipt --bus ./bo --as Bo --note <absolute path> --remote origin --branch main` printed `RECEIPT FAILED ... is neither an id on this bus nor a note that exists` without saying the path is bus-relative |
