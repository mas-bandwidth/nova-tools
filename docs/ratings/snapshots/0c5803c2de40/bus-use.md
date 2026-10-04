# nova-bus USE rating, current baseline 0c5803c2de40

Rater: opencode, model deepseek-v4.1-flash
Build: 0c5803c2de40
Score: 7/10

## Reasons
Source snapshot 0c5803c2de406c1b0b2b0841f579c9bf73406b1c (staged HEAD, verified with `go version -m`). Every command ran in a scratch directory against a bare origin and two clones, all local, never a real remote; no store, key, model or network service was touched. The advertised first run works: `nova-bus names --bus ./alice` prints the roster, `check --full` and an empty `inbox` both exit 0, and `draft` prints a header skeleton for redirection. Both required local jobs run end to end: Ada drafts and `send`s a note that lands on the bare origin with `pushed=true`; Bo sees it (after the clone is pulled) with `inbox --bodies`, `receipt`s it, `draft --reply-to` and `send`s a reply; Ada sees the reply. A second, different small job (Ada sends a second note; Bo reads it with `inbox --bodies --advance`) also runs. `--dry-run` is real where the help offers it: `send --dry-run` prints the framed note and leaves the tree clean, `reply --dry-run` reports `commit=- pushed=false` and writes nothing, `close --dry-run` reports the split with `commit=-`. The four provoked refusals are the tool's best feature: a missing required flag reports every missing flag at once, an unknown flag names the flags there are, an unknown verb names all eleven, and a bad value names the value and its remedy, each one line and exit 2.

The score is held down by three things. First, a reader on a clone is lied to the same way as the last two ratings: `inbox` and `wait` walk the local checkout without fetching or saying it is behind, so an un-pulled clone prints `changed=0` and `notes=0` while a note is already on the origin; only `inbox --advance` fetches, and then only to the pre-fetch HEAD. Second, `--advance` moves the cursor to the commit read and prints nothing about the fetch, and with `--bodies` `--advance` does not advance at all. Third, the family's one-output-two-renderings rule is broken: no verb accepts `--json` (every verb refuses it by name), and `names --bus X --json` is a hard refusal because no verb accepts a machine-readable shape. Recovering from "read a stale tree" needs a guess (`git pull`), which is the one thing this tool exists to avoid.

A 10 would need: `inbox`/`wait` fetch before the walk or refuse a checkout behind origin by name; `--advance` list what the fetch brought in and point the cursor at the commit actually read; `--bodies --advance` advance; `--json` on every verb from the one result value; and an absolute note path either accepted or answered with "the path is relative to the bus".

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus inbox --bus ./bo --as Bo --receipt-max-words 40 --bodies --advance --remote origin --branch main` | on a clone behind the origin the read prints `mode=full cursor=- changed=0 carrying=0 notes=0` and exits 0 while a note is already pushed; nothing says the checkout is stale, and only a guess (`git pull`) reveals it. The same run with `--advance` fetches but reports zero and reads a pre-fetch HEAD | fetch before the walk, or refuse a checkout behind its remote by name with the pull command; never report `notes=0` for a stale tree | M |
| 2 | `nova-bus names --bus ./alice --json` | every verb refuses `--json` (`unknown flag --json; the flags of names are --bus`); the family's "one output structure, two renderings" is broken, so a harness must parse the line grammar, and `nova-bus --json names` refuses `--json` as an unknown verb | route every verb's one result value through the shared `--json` rendering | M |
| 3 | `nova-bus inbox --bus ./bo --as Bo --receipt-max-words 40 --bodies --advance --remote origin --branch main` | with `--bodies`, `--advance` prints no `INBOX CURSOR` line and leaves the cursor where it was even when a whole note was printed; the non-bodies form advances | advance the cursor to the whole commit printed, as the non-bodies form does | S |
| 4 | `nova-bus receipt --bus ./bo --as Bo --note <absolute path of a note on that bus> --remote origin --branch main` | exit 1, `RECEIPT FAILED ... is neither an id on this bus nor a note that exists`, for a file that is on the bus; the bus-relative path records (`RECEIPT OK recorded=1`) | accept the absolute path, or say in the line that the path is relative to the bus | S |
| 5 | `nova-bus draft --bus ./alice --as Ada --to Bo --subject gate` | stderr says `send --file`, while `nova-bus draft -h` marks `--file` retired and says use `--out`; the first run's breadcrumb points at a retired flag | print `--out` (or the `send` call the actual draft shape wants) | S |
| 6 | `nova-bus reply --bus ./bo --as Bo --re ada-... --file <reply draft> --remote origin --branch main --dry-run` | `--dry-run` is one status line (`subject=Re:\x20hello`, `commit=- pushed=false`), not the reply bytes; a reader cannot check what it is about to write, unlike `send --dry-run` which prints the framed note | print the shaped reply the same way `send --dry-run` prints the shaped note | S |

## Good, keep
The refusal grammar: one run names every missing flag, says what each input wants, and prints the next command; unknown flag and verb name the whole set.
`send --dry-run` prints the exact framed note and writes nothing; `wait --timeout` hands back the exact command to rearm, and `--idle-exit 3` lets a harness branch without parsing.
`check` and `inbox` report the change since the cursor, so a poll's cost is the size of the change; `prepare` is a deterministic, self-contained JSON artifact.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| inbox reads a stale checkout without saying so | STILL THERE | `nova-bus inbox --bus ./bo --as Bo --receipt-max-words 40 --bodies --advance --remote origin --branch main` on a clone one note behind printed `INBOX SCOPE mode=full cursor=- changed=0 carrying=0` and `INBOX OK ... notes=0` (exit 0) while the origin held `ada: third`; the pull then showed it |
| inbox --advance --bodies does not advance | STILL THERE | `nova-bus inbox --bus ./bo --as Bo --receipt-max-words 40 --bodies --advance --remote origin --branch main` printed `INBOX BODIES printed=1 ... complete=true` and no `INBOX CURSOR` line; the cursor stayed at `20c3d8f` |
| --advance pulls unread notes in unmentioned | STILL THERE | the same advance read `changed=2 carrying=1` and printed the older open note, not the freshly fetched `third` note; `INBOX WALK commits=1/1 notes=0` names no fetch |
| the receipt path scope is undocumented and misleading on absolute paths | STILL THERE | `nova-bus receipt --bus ./bo --as Bo --note <absolute path> --remote origin --branch main` printed `RECEIPT FAILED ... is neither an id on this bus nor a note that exists` while the bus-relative path of the same note printed `RECEIPT OK recorded=1` |
