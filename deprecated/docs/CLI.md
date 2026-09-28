# Deprecated tools: command reference

## nova-board

```
nova-board list  (--issue <owner/repo>#<n> --gh-timeout <seconds> | --dir <path>) --stale <duration> [--list] [--open] [--owner <name>] [--max <n>]
nova-board add   (--issue ... | --dir ...) --as <name> --text <text> --by <duration-or-stamp> --default <text>
                 [--owner <name>] [--thing <name> --leg <name>] [--evidence <path>] [--id <thirty-two hex>]
nova-board take  (--issue ... | --dir ...) --as <name> --card <id> --stale <duration> [--anyway]
nova-board close (--issue ... | --dir ...) --as <name> --card <id> --stale <duration> (--how <text> | --landed <repo>#<n> | --probed <evidence>) [--anyway]
nova-board check (--issue ... | --dir ...) --words <text> [--max <n>] [--all]     # EXIT 1 WHEN IT MATCHES
nova-board quickstart (--issue ... | --dir ...) --stale <duration>
```

A **board** is the list of things a group of lines owes: one **card** per item, appended
when it is noticed, taken by whoever picks it up, closed with a sentence saying how.
Nothing on it is ever deleted and nothing is ever edited — it is an append-only log of
events, and the list of open cards is *derived* from that log rather than stored anywhere.
The rules, the failures each one closes and what the prototype did wrong are in
[docs/SPEC-BOARD.md](SPEC-BOARD.md), which is the contract.

**The verb that earns the tool is `check`, and it exits 1 when it matches.** The NO a board
owes a filer is *this is already on the board, do not file it*, so the rule every reader and
fixer follows is one line of shell — and the guard tells a NO from a could-not-run:

```sh
nova-board check --dir ./board --words "windows runner skips" || { [ $? -eq 1 ] && exit 0; exit 2; }
nova-board add   --dir ./board --as rowan --text "the Windows runner skips three steps" \
                 --by 4h --default "rowan files it on the schema board as a known gap"
```

**A card matches only when EVERY word appears** in its text (lower-cased, as a substring):
more words is a *narrower* check, never a broader one — `--words "the Windows CI skips
steps"` does not match the card *the windows runner skips three steps*. Two or three rare
words is the query that works, and `matched=0` over three or more words says so in a
`BOARD NOTE`. A check whose every word is in more than half the board still **exits 1** —
a matched check exits 1, always — and says so in a `BOARD NOTE`: the hits are about the
board's prose rather than about your finding, and narrowing `--words` is what sharpens it.

**The default view is counts, not cards**: one line per owner, one per leg, one `BOARD OK`
and exactly one `BOARD NEXT` naming the one thing to do first. At 500 cards across 20 lines
it is 27 lines and under 4 KB, and it does not grow with the number of cards. Cards print
under `--list`, capped at `--max` with one `MORE` line; `--list --owner <name>` is one
line's own batch.

**Every card has a deadline and a default** (`--by`, `--default`): nothing here waits
forever. **Every path and every duration comes from a flag** — there is no default board,
no default `--stale` and no default `--gh-timeout` (required under `--issue`, which is the
backend that runs `gh`), and no environment variable configures anything. A card taken by a
line that then goes silent is `stale=true` past `--stale` and is takeable again without
`--anyway`; a take or a close over somebody's *live* take is refused at exit 1 and names
the holder. Two backends, one format: a directory of card files (`--dir`, which this tool
appends to and never commits — landing it is yours) and issue comments (`--issue` with
`--gh-timeout <seconds>`, durable when the command returns). On the issue backend, the
comment's actual author (from GitHub's `user.login`) is used for card ownership and
closing; the `--as` value remains as a display label on the event. The file backend has
no author, so it uses `--as` as before.

### First run

`quickstart` needs a board and a stale window. It prints the board's counts and then the
check-then-add pair with this board's own values in it, quoted so it can be pasted.
`cmd/nova-board/testdata/example-board` is a board the size of a first run, and the
transcript the tests execute against it is in [TESTS.md](TESTS.md#nova-board).

`quickstart`, and the first `add`, make the directory if it is not there — `created=` on
`quickstart`'s first line says whether that run made it — while `list`, `check`, `take` and
`close` refuse one that is missing rather than making it, so a wrong path is a refusal and
not an empty board:

```
$ nova-board quickstart --dir ./board --stale 10m
QUICKSTART OK backend=dir source=./board stale=10m0s created=false: the board, then the rule every filer runs in front of add
BOARD LINE name=emma open=1 overdue=1 stale=1
BOARD LINE name=bo open=1 overdue=0 stale=1
BOARD LINE name=rowan open=1 overdue=0 stale=1
BOARD LINE name=freddy open=1 overdue=0 stale=1
BOARD LEG leg=cpp owed=1 probed=0
BOARD LEG leg=go owed=0 probed=1
BOARD NEXT the oldest OVERDUE card 283e2dd1e5c5424d7637d28488365e98, owed by emma, due 2026-09-11T09:00:00Z -- the token ledger has no September rows yet
BOARD OK cards=5 open=4 closed=1 stale=4 overdue=1 owed=1 lines=4 conflicts=0 quarantined=0 shown=8 backend=dir source=./board
QUICKSTART LINE n=1 what=check: "nova-board check --dir ./board --words 'the token ledger' || { [ $? -eq 1 ] && exit 0; exit 2; }"
QUICKSTART LINE n=2 what=add: "nova-board add --dir ./board --as <your-name> --text 'the token ledger has no September rows yet' --by 4h --default 'the filer files it as a known gap'"
QUICKSTART NOTE check EXITS 1 WHEN IT MATCHES, so the guard reads "if it is already there, stop"; the exit-2 arm tells a NO from a board that could not be read
QUICKSTART NOTE --stale 10m0s is this family's number and this run passed it in words: there is no default duration here, and --by and --default are required on every card
```

**What a first run gets wrong.** `--dir` naming a directory that is not there: `quickstart`
and the first `add` make it, because a first run has nowhere to write yet and a board is an
append-only log, so an empty directory is a valid empty ledger; `list`, `check`, `take` and
`close` refuse — a read or a take against a directory that is not there is a path typed
wrong, and making it would answer the typo with an empty board. That refusal names the
`mkdir -p` that fixes it, quoted so a `--dir` with a space in it pastes. `--stale` missing: it wants how long a card may go without
an event before it lists as takeable again, and the family's number is 10m — the tool will
not guess one. No backend, or both: name exactly one, because a board written to two places
is two boards with one name. `--by` or `--default` missing on `add`: a card with no deadline
cannot be filed. And reading `check`'s exit backwards: 1 means *found it, do not file*, so
the natural `&&` chain would file exactly the duplicates.

## nova-play

Shared reading annotations at the **margin layer**. Participants anchor notes to exact passages in a source text, reply to each other's notes, and resume across sessions. A changed source produces an explicit anchor conflict rather than silently moving notes. The contract is [docs/SPEC-PLAY.md](SPEC-PLAY.md).

### First run

Three lines: annotate a passage, read the notes back, reply to a friend. Every path is a flag — there is no default source, no default author, and no default annotation file.

```
$ printf 'The keeper climbed the last stair before dawn.\nThe lantern room held a brass fitting.\nBelow, the harbour was still asleep.\n' > story.txt

$ nova-play annotate --source story.txt --author Emma --passage "The lantern room held a brass fitting." --note "I wonder what alloy this is."
ANNOTATE OK id=f24beb35f0df author=Emma created=2026-09-16T08:22:37Z

$ nova-play read --source story.txt
READ OK source=story.txt notes=1
NOTE id=f24beb35f0df author=Emma created=2026-09-16T08:22:37Z
  PASSAGE The lantern room held a brass fitting.
  BODY I wonder what alloy this is.

$ nova-play reply --source story.txt --id f24beb35f0df --author Stella --body "Ship's brass, probably 70/30."
REPLY OK id=03ad5e57d795 author=Stella created=2026-09-16T08:22:38Z
```

**What the flags want.** `--source` is the text being annotated; `--author` is who is speaking; `--passage` is the exact passage text to anchor to (must appear verbatim in the source); `--note` is the annotation text; `--id` is the note to reply to; `--body` is the reply text.

**When the source changes.** Edit the source file between sessions and the next `read` says `ANCHOR STALE`, naming both the stored hash and the current hash. A new annotation is refused until the operator decides whether to migrate notes, discard them, or revert the source.

### Companion view

`view` renders an explicitly selected sample of Markdown records into a static timeline, one card per record linked back to its source. It is read-only: viewing never edits, seals, rolls up or deletes a record, and an excluded record is never even opened.

```sh
nova-play view --max 20 moment-one.md moment-two.md
nova-play view --exclude draft* --max 0 ./moments/*.md
```

Every record is named on the command line — there is no default file and no directory walk. `--exclude` takes a glob matched against each path as given and its base name, repeatable; `--max` caps the cards printed (default 20, `0` prints every card), and the summary line always carries the totals. A card shows the record's date, author, kind (human, ai, summary, or whatever word the record carries — echoed, never inferred) and source; a missing or unparseable date or author prints as `unknown` rather than a guess. Two layouts are read: a leading `---` fence holding lowercase `author:`, `date:`, `kind:` and `supersedes:` lines, and `Author:`, `Date:`, `Kind:` and `Supersedes:` lines anywhere else in the file. Cards sort chronologically with undated records last; a `supersedes:` value stays on the card so corrections remain discoverable, and a summary is listed beside the record, never in place of it.

### The sidecar file, and older ones

Notes for `story.txt` live in `story.txt.notes` beside it. It is a plain text file you can read, and it is **versioned**: this build writes version 2, which puts `VERSION 2` on the second line, stores each `PASSAGE`, `BODY` and `REPLY_BODY` as one physical line escaped with `\\`, `\n` and `\r`, and frames an author that is empty or contains a space, a quote, a backslash or an unprintable rune as a Go-quoted string (`author="Ada \"The Reader\" Lovelace"`). That is what lets a note keep a trailing space, a `"`, a `\`, or a line of prose beginning with `NOTE` without the reader mistaking it for the next record.

A sidecar written before version 2 has no `VERSION` line. It is still read, under the older rules: no escaping (a backslash is literal), an unprefixed line continues the value above it, and an unquoted multi-word author runs on to the next `key=value` token. `read` leaves such a file exactly as it found it. **The first `annotate` or `reply` that succeeds on that source rewrites the whole sidecar as version 2** — in place, one way, no backup — carrying the `ANCHOR` line over unchanged and storing every value it just read without reinterpreting it. A refused operation (stale anchor, missing source, unknown note ID) writes nothing and leaves the old file alone. If you want the old bytes, copy the file before the next write. The format is specified in [docs/SPEC-PLAY.md](SPEC-PLAY.md#the-sidecar-file).

**What this deliberately is not.** Not a reader or viewer — the source stays where it is, opened in whatever reader the participants choose. Not a publishing platform — notes are local to the machine that creates them. Not a notification system — participants check for new notes by running `read`.

## nova-friend

```
nova-friend here --as <you> [--harness <h>] [--host <h>] [--login <alias>]... [--pid <harness-pid>] [--session <id>] [--sprint <S>] [--once]   # the one presence process: register, then once a second the beat, the leases, the take; --once ticks once and returns
nova-friend bye --as <you>                                               # DEL your beat: down at once; registration and dealt work stay
nova-friend pull --as <you> [--n <k>] [--dir <d>] [--model <m>] [--harness <h>] [--child <id>]   # ready -> working for your copies, one brief per copy under --dir
nova-friend done --as <you> --id <copy> --ok --pr <repo>#<n> --head <sha> --repo <checkout> [--test <t>] [--branch <b>]   # the spec gate in your checkout, the PR recorded, the copy ended ok
nova-friend done --as <you> --id <copy> --fail <why>                     # the copy ended failed, the why on the record
nova-friend done --as <you> --id <copy> --score <N>/10 [--gates <g>] [--finding <text>]   # a read copy's score
nova-friend list                                                         # one line per registered friend: state, slots, tiers, roles, host, working copies, the applied revision
nova-friend show <name>                                                  # one friend, with the session's facts: session, harness, load, models, the beat's age, away
nova-friend away <name> --reason <r> --as <actor>                        # set friend:<name>:down: no push, take or copy until back
nova-friend back <name> --as <actor>                                     # clear it
nova-friend <verb> -h                                                    # the verb's usage line and every flag it takes
```

`nova-friend` is the one tool for what a friend, or a coordinator, does about a friend: the person, who exists whether or not a sprint is running. The roster (who exists, with slots, tiers and roles) is configuration and lives in [nova-config](#nova-config); `nova-friend` reads it and never writes it. What it owns is the runtime a friend reports herself: the beat, the away flag, and her own copies. The guide is [nova-friend/README.md](nova-friend/README.md).

### First run

```sh
nova-friend here --as rowan --once --host studio --session s1
nova-friend list
nova-friend bye --as rowan
```

The three lines need a store that holds the roster (`nova-config apply` writes it): `here --once` registers the session and ticks once, `list` reads every friend with the revision the configuration was applied at, and `bye` deletes the beat. The executable transcript, on a throwaway store, is in [TESTS.md](TESTS.md#nova-friend). The line each harness runs when its session opens is `nova-friend here --as <you> --harness <h> --pid <harness-pid>`, left running.

There is no `quickstart`: a verb that registered a friend nobody asked for would write configuration, which is nova-config's, and `here --once` is the first run.

**What the flags want.** `--redis` is `host:port` (env `NOVA_SPRINT_REDIS`, then `NOVA_REDIS_ADDR`, then the seat's address). `--as` is you, a lower-case friend name (env `NOVA_FRIEND`, which it must equal when set). `here` takes `--harness` and `--host` for the beat (default `nova-friend` and the hostname), `--login <alias>` repeatable for your forge accounts, `--pid` for the harness process your copies are bound to (their leases renew only while it is seen alive, nova-tools#4415), `--session` for the identity (default a fresh one) and `--sprint` for the take. `done` wants exactly one of `--ok`, `--score <N>/10` and `--fail <why>`; `--pr` wants `--head` and `--repo`, the checkout the spec gate runs in. `away` wants `--reason`.

**Reading it.** Every success is one typed line: `FRIEND HERE as= host= session= slots= taken=`, `FRIEND BYE as= was=`, `FRIEND PULLED id= leg= token= card=` then `FRIEND PULL as= n= free= dir= ms=`, `FRIEND ENDED id= primary= from= to= next=` then `FRIEND DONE as= n= ms=`, `FRIEND name= state= slots= tiers= roles= host= working= rev=` per friend, `FRIEND AWAY name= reason= changed=`, `FRIEND BACK name= changed=`. `state` is `up` (a beat at most a minute old and no away flag), `away` (the flag) or `down`; `rev` is the friend revision `nova-config apply` last stamped in `config:decl`, `-` when none.

**Refusals.** Exit 1 is the store saying no, one stderr line naming the next step: `UNREGISTERED emma: not in the roster; run: nova-config friend add emma --slots <n> --as <you>, then nova-config apply`, `BUSY rowan: a live session is here already on studio (session s1, beat 3s ago); stop it, or wait a minute and it is taken over`, `LOGIN-TAKEN rowan: --login x is stella's login already`, `NOTMINE task:q2~1 is friend:stella's copy, not friend:rowan's`. A stale `--token` on `done` is `FENCED`, exit 3, as `nova-sprint card end` spells it. Exit 2 is an invocation that could not run: a missing flag says what it wants (`--reason wants why stella is away`), a store that does not answer is one line. `here` exits 3 when five beats in a row fail (after bye) or another session took its beat (no bye).
