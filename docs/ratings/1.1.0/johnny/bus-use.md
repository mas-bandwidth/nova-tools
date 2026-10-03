# nova-bus USE rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 7/10

## Reasons

`nova-bus help` exits 0 and says what the tool is in three lines: a git repository, a roster, a lane per sender, notes as markdown. The root text is 271 lines. `nova-bus help <verb>` and `nova-bus <verb> -h` are the usable pages (10 to 49 lines), they match each other, and they exit 0 before touching a bus. There is no `--json` on any verb help. `--dry-run` is offered on send, reply, and close. `prepare` is the JSON path, with no flag for it.

First successful run, from the standalone block in `nova-bus help`, on a fresh local repository: `nova-bus names --bus ./bus` printed two senders and `NAMES OK`; `nova-bus check --bus ./bus --full` printed `BUS OK notes=0`; `nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --open` printed an empty inbox and exited 0; `nova-bus draft --bus ./bus --as Ada --to Bo --subject gate` printed a header and `<the note goes here>` on stdout, and on stderr `DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>`.

The job the tool exists for, on a bare origin and two clones, both local: replace the placeholder, `nova-bus send --bus ./a --file drafts/gate.md --as Ada --remote origin --branch main --dry-run` printed the framed note and left the clone clean, then the same command without `--dry-run` printed `SEND OK ... pushed=true attempts=1`. The other clone did not see it until a pull. After `git pull`, `nova-bus inbox --bus ./b --as Bo --receipt-max-words 40 --full --bodies --open` printed the body `Bo, the gate is green.` between `INBOX BODY` and `INBOX BODY END`.

A second, different job: `nova-bus reply --bus ./b --as Bo --re ada-96029316ab27 --file drafts/reply.md --remote origin --branch main --dry-run` printed one `REPLY OK` line and wrote nothing; the real reply printed `pushed=true`. Bo's next full inbox was `open=0` (the reply closed his copy). After a pull, Ada's inbox printed Bo's body. `nova-bus wait --bus ./b --as Bo --receipt-max-words 40 --timeout 3s --interval 1s --remote origin --branch main` exited 0 with `WAIT TIMEOUT` and `WAIT DONE ... next=` set to that same command.

Refusals, four kinds. Missing flags: `nova-bus inbox` named `--as`, `--bus`, and `--receipt-max-words` together, said the word count wants a number of at least 1, and pointed at `nova-bus help` (exit 2). Unknown flag: `nova-bus names --bus ./a --json` printed `NAMES REFUSED: unknown flag --json; the flags of names are --bus; run: nova-bus help` (exit 2). Unknown verb: `nova-bus wobble` listed every verb and `run: nova-bus help` (exit 2). Bad value: `nova-bus wait ... --timeout 2h` said 2h is longer than 1h, why a longer wait is not a longer wait, and `run: nova-bus wait -h` (exit 2). `nova-bus close ... --before yesterday` and `--idle-exit 1` were the same shape.

What keeps it from a 10 is that a quiet success can be a lie, and two of the required inputs show up only on a second run.

`nova-bus inbox` does not fetch. On the clone that had not pulled, the same full inbox exited 0 with `carrying=0` while the note was already on the bare origin. Nothing in the output said the checkout was behind. `nova-bus help inbox` says `--remote` is required only with `--advance`, and the root help says reading needs no remote, which is true and is also how you read a stale tree and believe it. I had to guess `git pull`.

`--advance` does fetch, and it does not show what it fetched. After Bo sent a second note, Ada's `inbox --advance` listed only the reply she already had, recorded the cursor at that older commit (`fb8ed62`), and pushed. The new note was in the clone (it was on disk, under the new tip) and was not in that output. The next inbox, with no pull, printed it. Help says `--advance` moves the cursor to HEAD. This run moved it to the HEAD from before the fetch.

Missing inputs are not all named on the first run. `nova-bus send --bus ./a` named `--remote` and `--branch` and did not name `--file`. A second run, with those two supplied, named `--file` and `--stdin`. `nova-bus reply` with no flags named five flags and did not name `--re`; the second run did, and that second line is the good one (`name the note being answered; run: nova-bus reply -h`). The first round's remedy is `nova-bus help`, the 271-line page. `nova-bus draft`'s stderr still says `send --file`, and `nova-bus draft -h` says `--file` is retired and to use `--out`.

`nova-bus receipt -h` says `--note` is an id or a path. A bus-relative path recorded (`RECEIPT OK recorded=1`). An absolute path to a note that was on the bus exited 1 with `RECEIPT FAILED` and "neither an id on this bus nor a note that exists". The file was there. The line does not say the path has to be relative to the bus. Receipt has no `--dry-run`; adding one is an unknown-flag refusal that does list the real flags.

`send --dry-run` is what the help says: the note, framed, and a clean tree. `reply --dry-run` is one status line and not the body (`subject=Re:\x20gate`, `commit=-`). `close --dry-run` printed `CLOSE OK closed=2 kept=0 commit=-` and not the ids, so the count is not something you can check before you write. `prepare` printed one JSON object and left the tree clean; the `Date` inside the note is `Sat Oct  3 16:34:39 UTC 2026`, while the send summary's `date=` field is RFC3339. `nova-bus wait -h` still lists `--beat` and `--beat-lease` as retired, and lists `--on-note` in the flags but not in the synopsis. The `--host` metavar is printed as `<host=>`.

A 10 fetches on inbox, or refuses a checkout that is behind its remote and prints the pull. `--advance` lists every note the fetch brought in, or it does not move that history under a cursor aimed at the old head. The first refusal names every missing flag, including `--file` and `--re`, and the remedy is that verb's `-h`. Receipt either accepts an absolute path or says "relative to the bus". Draft stops recommending the flag it has retired. `close --dry-run` and `reply --dry-run` show the same bytes send already shows.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus inbox --bus ./b --as Bo --receipt-max-words 40 --full --open` | exit 0 and `carrying=0` on a clone that had not pulled, while the note was already on the bare origin; the output never says the checkout is behind | fetch, or refuse with the pull command, and do not report an empty inbox for a stale tree | M |
| 2 | `nova-bus inbox --bus ./a --as Ada --receipt-max-words 40 --advance --remote origin --branch main` | the fetch brought in a newer note and the listing did not name it; the cursor was recorded at the pre-fetch commit | list what the fetch added, and point the cursor at the commit you actually read | M |
| 3 | `nova-bus send --bus ./a` | the first refusal names `--remote` and `--branch` only; `--file` appears on the next run, and the remedy is the 271-line root help | name every missing input on the first run, and point at `nova-bus send -h` | S |
| 4 | `nova-bus receipt --bus ./a --as Ada --note <absolute path of a note on that bus> --remote origin --branch main --no-push` | exit 1, `RECEIPT FAILED`, "neither an id on this bus nor a note that exists", for a file that is on the bus; the bus-relative path of another note had just recorded | say the path is relative to the bus, or accept the absolute path | S |
| 5 | `nova-bus draft --bus ./bus --as Ada --to Bo --subject gate` | stderr says `send --file`, and `nova-bus draft -h` says `--file` is retired and to use `--out` | print the flag the verb accepts now | S |
| 6 | `nova-bus close --bus ./a --as Ada --before 2026-10-04T00:00:00Z --dry-run` | `CLOSE OK closed=2 kept=0 commit=-` names no id, so the plan cannot be checked before a real close | print each id the dry run would close | S |

## Good, keep

`nova-bus wait ... --timeout 3s` ends with `WAIT DONE` and `next=` set to the command just run, on stdout, exit 0. That is the line an AI can paste.

`nova-bus send ... --dry-run` prints the note it would commit, framed by `SEND DRAFT` and `SEND DRAFT END`, and leaves the clone clean. The real send then uses that same id.

`nova-bus wobble` and `nova-bus names --bus ./a --json` each name what does exist (the verbs, or the one flag) in one line.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| inbox reads a stale checkout without saying so | STILL THERE | `nova-bus inbox --bus ./b --as Bo --receipt-max-words 40 --full --open` before any pull printed `INBOX OK as=Bo carrying=0 open=0 notes=0` and exited 0; origin already had the note |
| inbox --advance --bodies does not advance | FIXED | `nova-bus inbox --bus ./b --as Bo --receipt-max-words 40 --bodies --advance --remote origin --branch main` printed `INBOX CURSOR commit=2506bfdc3044cd17a03d6771571cb02b33dee948 carrying=1 pushed=true attempts=1` and wrote `from-bo/CURSOR` |
| --advance pulls unread notes in unmentioned | STILL THERE | after a newer note was on origin, Ada's `--advance` listed only `bo-989e40709ef6` and recorded cursor `fb8ed62`; the next inbox printed `bo-52445c678782` with no pull |
| the receipt path's scope is undocumented and misleading on absolute paths | STILL THERE | a bus-relative `--note` printed `RECEIPT OK recorded=1`; an absolute path to a note on that bus printed `RECEIPT FAILED` and "neither an id on this bus nor a note that exists" |
