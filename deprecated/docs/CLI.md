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

## nova-work

`nova-work` is both the thin client of the resident work session ([docs/SPEC-WORK.md](SPEC-WORK.md), "The engine and its client") and the in-process reader of the job graph and bounded `.work` plans ([SPEC-JOBS.md](SPEC-JOBS.md), [SPEC-WORKLANG.md](SPEC-WORKLANG.md)). As a client it sends one request line over the Unix socket `--session` names and prints the session's one answer line, byte for byte; the session is the engine and owns every fact, so the client refuses to guess and second-guesses nothing. The graph and plan verbs read files as data, never as programs.

```
nova-work: the thin client, the job graph and the bounded .work reader (see docs/SPEC-WORK.md, docs/SPEC-JOBS.md, docs/SPEC-WORKLANG.md)

usage:
  nova-work session start  --session <path> --as <name> --file <path-in-repo> --journal <path> --cache <path> --repo <path> --remote <name> --branch <name>
                           --max-bytes <n> --max-depth <n> --max-nodes <n> --every <duration> --skew <duration> --clip-every <duration> --clip-after <n> --retain <duration>
                           --savepoint-every <duration> --savepoint-after <n> --max-frame-bytes <n> --silence-ping <duration>
                           --index-cache <n> --page-bytes <n> --page-records <n> [--closed-window <duration>] [--render-root <root-id>=<owner/name>:<directory> ...]
                           [--resolver <scheme>=<command> ...] --git-timeout <seconds> [--attempts <n>] [--repair] [--foreground] [--max <n>] [--now <stamp>]
  nova-work session export (--session <path> | --journal <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) --into <path>
  nova-work session export (--session <path> | --snapshot <path> --cache <path>) --state --at <revision> --closed-history <none|all|range> [--from <stamp> --to <stamp>] --into <new-directory> --max-bytes <n> --max-depth <n> --max-nodes <n> --max-output-bytes <n>   (a long operation under --session; one finite process under --snapshot)
  nova-work session replay --session <path> --from <path> --as <name> [--max <n>]
  nova-work session status --session <path>
  nova-work session stop   --session <path> --git-timeout <seconds> [--attempts <n>] [--no-clip]
  nova-work session handoff --session <path> --to <name> --git-timeout <seconds> [--attempts <n>]
  nova-work operation status  --session <path> --id <id>
  nova-work operation list    --session <path> [--max <n>]
  nova-work operation wait    --session <path> --id <id> --timeout <duration> [--after <cursor>]
  nova-work operation cancel  --session <path> <write flags> --id <id> --reason <text>
  nova-work savepoint list     --session <path> [--max <n>]
  nova-work savepoint create   --session <path> --as <name> --reason <text>
  nova-work savepoint verify   --session <path> --id <id>
  nova-work savepoint compare  --savepoint <path> --against (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) [--max <n>]
  nova-work undo-plan      --session <path> --request <id> [--max <n>]
  nova-work undo           --session <path> <write flags> --request-of <id> --reason <text>
  nova-work redo-plan      --session <path> --request <id> [--max <n>]
  nova-work redo           --session <path> <write flags> --request-of <id> --reason <text>
  nova-work friend         --session <path> <write flags> (--register <name> | --retire <name> | --role <name>=<role>[:<scope>] | --participation <name>=<yes|no|withdrawn> | --capability <name>=<capability-id> --group <child|swarm|local|one-shot> --limit <n> | --limit <name>=<n>) --reason <text>
  nova-work config         --session <path> (--request <name> --base <hash|-> | --export <name> --into <path> | --intake --from <path> <write flags>) [--max <n>]
  nova-work model          --session <path> <write flags> (--register <id> --provider <name> --route <text> --billing <metered|subscription|local|unknown> | --rate <id>=<pricing-id> --effective <stamp> --source <pointer> | --evidence <id> --task-class <label> --result <pointer> --samples <n>) --reason <text>
  nova-work observe        --session <path> <write flags> --friend <name> (--state <awake|resting|unavailable|unconfirmed> --source <pointer> | --attempt <id> --observed-model <id> --bench <name> --usage <pointer>) --reason <text>
  nova-work goal set       --session <path> <write flags> --expect <rev> [--scope <scope>] (--goal <node-id> | --clear) --reason <text>   (--expect required here; --scope defaults to the caller's --as)
  nova-work goal show      (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) --as <name> [--scope <scope>] --max <n>
  nova-work goal update    --session <path> <write flags> --expect <rev> [--scope <scope>] (--progress <text> [--evidence <pointer> --criterion <id> --against <sha>] | --blocked-by <node-id> --reason <text> | --stop --reason <text>)   (writes on the current goal node of the scope and on no other node)
  nova-work machine        --session <path> <write flags> (--register <id> --name <text> --owner <name> --connect <ref> --role <build|test|profile> ... | --retire <id> | --permit <id>=<kind> | --exclude <id>=<kind> | --limit <id> <key>=<n|n,n,...> | --fact <id> <key>=<value> --declared-by <name>) --reason <text>
  nova-work route          --session <path> <write flags> (--register <id> --provider <name> --endpoint <url> --key-location (:path "<path>"|:env "<name>") --plan <flat|metered|free|local> [--cost-per-mtok <n>] --capabilities <text=yes|no,code=yes|no,tool-calls=yes|no> --owner <name> | --retire <id> | --probe <id> --card <pointer> --pass <true|false|absent> [--wall <duration> --usd <amount>] --source <pointer>) --reason <text>
  nova-work offer          --session <path> <write flags> --node <id> --offer <offer-id> --to <name> --profile <capability-id>@<config-revision> --attempt <attempt-id> --generation <n> --request-ref <opaque-id> --payload <pointer> --payload-sha256 <hex> --reserve <slots> --until <stamp> [--requested-model <model-id>] [--predecessor-offer <offer-id> --predecessor-attempt <attempt-id>] [--reason <text>]
  nova-work profile        --session <path> <write flags> (--write <name> --model <id> --harness <id> --work-type <label> --pointer <path> --policy <revision> [--evidence <pointer>] [--expiry <stamp>] [--owner <name>] | --edit <name> (--pointer <path> | --policy <revision> | --evidence <pointer> | --expiry <stamp> | --owner <name>)) --reason <text>   (an edit re-pins the digest when it changes --pointer; a manager session selects one by name at start and never swaps it mid-session)
  nova-work acknowledge    --session <path> <write flags> --offer <offer-id> --reply <receipt-id> --stage <received|accepted> --provenance <pointer> --provenance-sha256 <hex> [--by <duration|stamp> --default <release|extend-once|escalate:<name>>] [--observed-model <model-id>] [--bench <name>] [--execution <handle>] [--reason <text>]   (--stage accepted: --by and --default, required, create-if-needed; --stage received: both exit 2)
  nova-work decline        --session <path> <write flags> --offer <offer-id> --reply <receipt-id> --provenance <pointer> --provenance-sha256 <hex> [--reason <text>]
  nova-work execution pause     --session <path> <write flags> (--node <id> | --repo <owner/name> | --all) --reason <text>
  nova-work execution stop      --session <path> <write flags> (--node <id> | --repo <owner/name> | --all) --reason <text>
  nova-work execution resume    --session <path> <write flags> --control <id> --action <release-hold|resume-workers> --reason <text>
  nova-work execution correct   --session <path> <write flags> --node <id> --instructions <pointer> --sha256 <hex> --reason <text>
  nova-work execution reconcile --session <path> <write flags> --control <id> --from <manifest-id> --reason <text>   (a content identity, never a local path)
  nova-work execution status    --session <path> --control <id> [--max <n>]
  nova-work check          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) [--max <n>]
  nova-work verify         --session <path> (--offline | --max-fetch <n> --fetch-timeout <seconds>) [--node <id>] [--max <n>]
  nova-work query          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) --ask <kind> --branch <open|closed|root>
                           (--ask is one of: done, remaining, who, percent, size, stream, under, stale, handoffs, roadmap, friends, models, ready, fleet, routes, reports)
                           [--node <id>] [--repo <o/n>] [--owner <name>] [--category <label>] [--axis <member>] [--for <workload-kind>] [--class <card-class>]
                           [--since <revision>] [--at <revision>] [--from <stamp>] [--to <stamp>] [--after <cursor>] [--page-budget <n>] [--max <n>] [--order <discovery|priority>]
                           (who and stale: --window <duration>, required; percent: --axis <member>, required on a matrix and refused on a zero- or one-axis roadmap;
                            ready: --order, optional, discovery by default; --order priority on any other ask is exit 2;
                            --branch closed and --branch root: --from and --to, required, and refused under --branch open;
                            who, stale, handoffs and reports: --branch open only, the other two exit 2; reports: --since <revision>, required (SPEC-AHEAD: #854);
                            fleet: --for optional, --node names a machine id, and --for with --node on a member that excludes the kind is refused;
                             routes: --class optional, the card class whose ordered route list the projection emits, cheapest first; without it the whole registry is listed)
  nova-work render         --session <path> --view <roadmap-id> (--chat [--projection <id> | --row-axis <id> --column-axis <id> --fixed <axis-id>=<member-id> ...] | --projection <id> (--file | --check)) [--at <revision>]
  nova-work node add       --session <path> <write flags> --id <id> --type <work-set|epic|feature|task> (--under <parent-id> | --under-root open --repo <owner/name>) [--title <text>] [--category <label>] [--required <true|false>] [--acceptance <id:kind:subject:predicate> ...] [--link <text> ... | --links-empty | --clear-links] [--private <true|false>] [--version <text>] --reason <text>   (--type roadmap is exit 2 naming 'roadmap create')
  nova-work node edit      --session <path> <write flags> --node <id> (--title <text> | --clear-title | --category <label> | --clear-category | --link <text> ... | --links-empty | --clear-links | --private <true|false> | --clear-private | --version <text> | --clear-version) ... --reason <text>
  nova-work node move      --session <path> <write flags> --node <id> --from <parent-id> --under <parent-id> --reason <text>
  nova-work node remove    --session <path> <write flags> --node <id> --reason <text>
  nova-work node require   --session <path> <write flags> --node <id> --to <true|false> --reason <text>
  nova-work decompose      --session <path> <write flags> --node <id> --into <id,...> --acceptance <child-id:id:kind:subject:predicate> ... --reason <text>
  nova-work accept         --session <path> <write flags> --node <id> (--add <id:kind:subject:predicate> | --remove <id>) --reason <text>
  nova-work source         --session <path> <write flags> --node <id> --to <sha> --reason <text>
  nova-work dep            --session <path> <write flags> --node <id> (--add <id> | --remove <id>) --reason <text>
  nova-work axis           --session <path> <write flags> --roadmap <id> --axis <id> (--add <member> | --remove <member>) --reason <text>
  nova-work roadmap create --session <path> <write flags> --id <id> --under <parent-id> [--title <text>] --row-kind <feature|epic|work-set> --aggregation <required-members|all-members|leaves> --completion-policy all-required-features (--axes-none | --axis-id <id> ...) [--permit-root <root-id> ...] --reason <text>
  nova-work roadmap configure --session <path> <write flags> --roadmap <id> [--row-kind <kind>] [--aggregation <policy>] [--completion-policy all-required-features] [--axes-none | --axis-id <id> ...] [--permit-root <root-id> ... | --roots-empty] --reason <text>
  nova-work roadmap row    --session <path> <write flags> --roadmap <id> (--add <member> | --remove <member>) --reason <text>   (axisless roadmaps only)
  nova-work roadmap projection --session <path> <write flags> --roadmap <id> (--add <id> --root <root-id> --repo <owner/name> --path <relative-path> --start <marker> --end <marker> --policy markdown-table [--row-axis <id> --column-axis <id> --fixed <axis-id>=<member-id> ...] | --remove <id>) --reason <text>
  nova-work prioritise     --session <path> <write flags> --node <id> (--set <rank> | --clear) [--context <self|subtree>] --reason <text>
  nova-work cell           --session <path> <write flags> --roadmap <id> --coord <member,member> (--ref <id|-> | --out-of-scope | --in-scope) --reason <text>   (--ref - clears the mapping)
  nova-work responsible    --session <path> <write flags> --node <id> --to <name> --reason <text>
  nova-work take           --session <path> <write flags> --node <id> --by <duration|stamp> --default <release|extend-once|escalate:<name>>
  nova-work heartbeat      --session <path> <write flags> --node <id> --evidence <pointer>
  nova-work release        --session <path> <write flags> --node <id> [--handed <name> --by <duration|stamp> --default <release|extend-once|escalate:<name>>]
  nova-work heartbeat      --session <path> <write flags> --allocation <id> --generation <n>   (allocation heartbeat: --allocation names the allocation id returned by take, --generation is the machine generation)
  nova-work release        --session <path> <write flags> --allocation <id> --generation <n> [--handed <name>]   (allocation release: --allocation names exactly one allocation, --generation is the machine generation; frees that allocation's slot only)
  nova-work attest         --session <path> <write flags> --node <id> --criterion <id> --result <pointer> --against <sha>
  nova-work attempt        --session <path> <write flags> --node <id> --model <name> --bench <name> --result <pointer> [--usage <pointer>]
  nova-work evidence       --session <path> <write flags> --node <id> --pointer <pointer> --criterion <id> --against <sha> [--attempt <id>]
  nova-work state          --session <path> <write flags> --node <id> --to <state> (--evidence <event-id> ... | --reason <text>) [--blocked-by <id>]
  nova-work correct        --session <path> <write flags> --node <id> --reason <text>
  nova-work event          --session <path> <write flags> --kind <baseline|discovery|defer|cancel|reopen|supersede> --node <id> --reason <text> [--member <id,...>] [--superseded-by <id>] [--evidence <pointer>] (baseline and discovery: --member, required, and --kind discovery on a :roadmap is exit 2 naming 'axis --add'; supersede: --superseded-by, required; cancel: --evidence <pointer>, required, and a note: pointer IS admitted here, because it evidences a stopped worker and never a done; --member on any other kind is exit 2)
  nova-work report         --session <path> <write flags> --act <launched|stopped|other> --subject <node|machine|friend|route|offer|external>:<text> --what <text> --acted-at <stamp> --instead-of <text|-> --reason <text>   (SPEC-AHEAD: #854; records a hand act whose effect lies outside the tree and changes no tree state)
  nova-work version        print this build identity (--version also accepted)
  nova-work help
  nova-work dependencies --graph <file> [--node <id> --needs <id>[,<id>...]]
  nova-work ready --node X --graph <file>
  nova-work clip --worktree <dir> --branch <name> --base <ref> --harvest <dir> [--result <file>] [--message <text>]
  nova-work plan check --file <path.work> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work plan expand --file <path.work> --out <dir> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work set check --file <path.lisp> [--minds <file>] [--lanes <file.tsv>] [--done <id>[,<id>...]] [--ready]
                      [--evaluate] [--base <branch>] [--cache <dir>] [--write-status]
                      [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work attempt record --file <path.lisp> --unit <id> --by <mind> --outcome ok|failed|uncertain [--proof <path|sha|url>]
                      [--rung <name>] [--usage <file.tsv>] [--pr <n>] [--started <stamp>]
  nova-work attempt list   --file <path.lisp> --unit <id>
  nova-work next      --file <path.lisp> --for <mind> --lanes <file.tsv> [--machines <registry>] [--done <id>[,<id>...]]
                      [--kind <kind>] [--floor <0..1>] [--jev | --no-jev] [--usage <file.tsv>] [--log <file>]
                      [--take [--by <mind>] [--started <stamp>]]
  nova-work ask  --owner <friend> --unit <id> --units <file> --bus <dir> --as <name>
                 [--deadline <stamp>] [--kind work|read] [--cc <names>] [--record <file.json>]
                 [--reply-branch <name>] [--remote <name>] [--branch <name>]
                 [--nova-bus <path>] [--attempts <n>] [--timeout <duration>] [--max-bytes <n>] [--now <stamp>]
  nova-work asks (--units <file> | --bus <dir> --as <name>) [--owner <friend>] [--max <n>] [--max-notes <n>]
                 [--max-bytes <n>] [--now <stamp>]
  nova-work events --redis <addr> [--repo <owner>/<name>] [--base <branch>] [--gh-poll 60s] [--bench <name>] [--log <path>] (--once | --deadline <duration>)
  nova-work push --stream <kind> --lane <red|green|small|next> --card <file> (--redis <addr> | --dir <root>) [--priority <n>] [--needs <id>[,<id>...]]
  nova-work verification --sexp <path> --repo <dir> (--check | --write) [--timeout <duration>]

wire:
  one line in, one line out over the Unix socket --session names. The request
  line is the verb and its flags in the order above, each as --name <value>,
  values escaped through internal/oneline's field form (one token per value:
  a space is \x20, an equals is \x3d), bools as --name true, the whole line
  newline-terminated. The reply is the session's own answer line, printed byte
  for byte: OK, ROW, NOTE and MORE to stdout, exit 0; FAIL, RACED and REFUSED
  to stderr, exit 1. What cannot run at all is one WORK REFUSED line on
  stderr, exit 2, ending "run: nova-work help". Values travel as given: the
  session validates every one and refuses with its own naming. An exchange is
  bounded by a 30-second default; a declared wait keeps a bounded 30-second
  transport allowance, an explicit --deadline caps the bound, and a deadline
  already past refuses before anything is dialled.

verbs:
  nova-work dependencies   owns the graph (:deps, refused acyclic at seed by validator rule 3)
  nova-work ready --node X is the ready set
  nova-work clip           commits the card's branch, harvests its result, resets the worktree to base
  nova-work plan check     reads a .work plan as data and closes its needs/blocks graph, never as a program
  nova-work plan expand    writes one card directory per hand-written :node, refusing a cycle or an absent need
  nova-work set check      reads the (work-set ...) form a coordinator writes and validates it whole
  nova-work attempt record files ONE attempt on ONE unit and moves its :state (A3, A4)
  nova-work attempt list   the unit's attempts, in order, with their termination proofs
  nova-work next           the ONE unit this mind does next: ready, owned, admitted, routed
  nova-work ask            delivers ONE unit to the FRIEND who owns it, as a bus note
  nova-work asks           the open asks, oldest first, with their age and their deadline
  nova-work events         bridges the events, not ticks (cards:done stream + gh fallback poll)
  nova-work verification   runs the suite at HEAD, lists STALE and PROPOSE criteria, --write rewrites :verification

THE MACHINERY ROUTES TO FRIENDS (Glenn, 2026-09-18). A bench pulls cards; a friend pulls
asks. A unit whose owner is a friend is therefore never cut as a card: ask renders it as
ONE note in the house shape -- To, Cc, Subject, the unit, its lane, its needs, its
acceptance, the deadline and the branch to reply on -- and sends it through nova-bus's
OWN send path. An ask that could not be sent records nothing.

--units reads EITHER form, read from the file's first byte rather than its name: the
JSON shape this tool writes, or the SPEC-WORKLANG work set a coordinator writes by hand,
through the same bounded reader plan check uses. A JSON work set has the ask written
back onto its unit. A SPEC-WORKLANG one is a person's document and is NEVER written back:
the ask goes to --record when one is named, and otherwise the note on the bus is the
record -- which is what asks --bus --as reads. The bus is the source of truth for what
went out, and where a row appears in both, the bus's wins.

A unit with no :acceptance is asked, not refused: not one unit of the real work set
carries one, so the title stands as the acceptance, the note says "Acceptance: as titled"
and one ASK NOTE line on stderr says the unit carried none. A deadline is still never
guessed: --deadline, or the unit's own :deadline, and a unit with neither is refused.
The sender is on the Cc line of every ask it sends, because a broadcast includes self.

A node is ready only when every need is terminal accepted, and every row that cannot
proceed prints its exact blocker and its resolver. A :deps cycle is refused before
publication, so the ready set is finite and the graph can never deadlock.

A plan is read as data, never as a program: a `#.` dispatch macro anywhere in code
position is refused at exit 2 naming its byte offset, string and comment text is opaque,
and an unknown :kind is refused naming the field. :needs is the reference edge and
:blocks its inverse, so the kernel derives whichever a node did not give; an absent
need is refused naming the field and the id, and a :needs cycle is refused by validator
rule 3, both at load before the graph is published. A hand-written :node owes :kind,
:output and :budget before it can expand: :output must name a :branch, and :budget must
name :minutes, :tokens and :model-floor; a node owing several is refused naming every
field it owes in one run, never one round trip per field.

set check reads the OTHER top form of the same language: not `(:plan ...)`, the
expander's, but `(work-set "id" ... :units ((unit ...)))`, the one a coordinator
writes. It is read by the SAME bounded reader -- three bounds, no eval, a dispatch macro
refused at the byte that owes it -- and a key this reader does not know is KEPT, never
refused: the work set is a person's document and a unit with a :pr or a :budget is still
a unit with an owner. What set check then validates is the CONTENT, and the two exit
codes say different things. Exit 2 is a refusal: this file could not be read at all.
Exit 1 is findings: it was read whole and its content is wrong -- a duplicate id, a
:needs naming a unit nobody defined, a cycle, an :owner no --minds registry names, a
:lane no --lanes file names, a :deadline that is not an instant. Every rule runs over
every unit in ONE pass, one SET line per finding, because a checker that stopped at the
first would cost one round trip per defect. The SET OK line prints either way, and
units = ready + blocked + done closes its arithmetic; one SET DONE done=<n> percent=<p>
line follows it, percent rounded down, so x/y z% comes from the tool and not from awk.

--ready is the mechanical ready set, derived from the language rather than maintained by
hand: a unit is done when it says so (:done, or a :status of closed, done, landed or
merged) or when --done names it, and ready when it is not done and every need is done.
Without --minds and without --lanes those two rules are OFF rather than run against a
guessed file: there is no default registry and no discovery.

attempt and next are the WRITE side of SPEC-WORKLANG's amendment. A3 made an attempt a
RECORD with a termination proof and A4 made uncertain a state that keeps its reservation,
and both landed as readers: the only way a real work set could grow an :attempts list was
a person typing s-expressions into their own document by hand.

attempt record files one attempt on one unit and edits the file IN PLACE by splicing
bytes: every byte outside the edited unit comes back identical, and inside the unit every
byte outside the edited key does too. A work set is a person's document -- its comments,
its blank lines and the column its keys line up at are the document -- so the writer never
re-renders what it is not touching. An outcome that claims to have ended without proving
it is refused NAMING THE WORD to write instead (uncertain), and a unit already closed,
refused or abandoned takes no further attempt: a reopened piece of work is a new id
carrying :was (A2). The state machine is A4's own closed set and gets no second vocabulary
beside it -- green closes the unit, red leaves it OPEN because the ladder is the retry
policy, refused and abandoned are themselves, and uncertain keeps the reservation.

A try that started and then ended is ONE try. Where the unit's last attempt is still open
-- an :outcome :uncertain with no :proof, taken by this same mind -- record CLOSES that
record rather than appending beside it, keeping its :n, its :rung and the instant it
actually began. An open attempt under ANOTHER mind is refused, never appended beside:
its state and its reservation stand until its own owner records the outcome.

next is the what-do-I-do-next verb, and the first place all three halves of the work
language answer one question together: the graph says whose needs are closed, the kernel
(internal/jobs) says whose resources are free, and nova-decide's ladder says which mind
does it. Four gates, each a reading rather than a judgment -- ready, owned, free, routed
-- and ONE line out: NEXT unit= lane= rung= conf= take= reason=, or NEXT NONE naming the
gate that emptied the set. Everything already :live or :uncertain holds its reservation
BEFORE anything is admitted against what is left, which is what A4 means: the clock never
frees capacity, only an outcome does. And a unit the ladder is waiting on is never
dispatched (Stella's lease rule: a rung that may still be running is not a rung to step
off).

--evaluate derives done from each unit's :acceptance instead of its :status, through gh
(nova-tools #2664). Two criteria are evaluable: (:kind :landed :subject "pr:<o/r>#<n>"
:predicate :merged-or-closed-in-base) holds when the PR is merged, OR closed with its
content in the base by the lander's rule -- git merge-tree --write-tree <base> <head>
yields the base's own tree, so merging it changes nothing, which holds for a PR the lander
combined with another -- and :subject "commit:<sha>" holds when the commit is reachable
from the base; (:kind :merged ... :predicate :merged-at) holds only when the PR is merged.
A subject pinned as pr:<o/r>#<n>@<sha> asks about that head only: a pin the PR's last
head does not match was superseded and is not landed. The merge runs in one blobless
bare repository per repo under --cache, fetched --depth 50 and deepened (the depth
doubled) only while the PR's window start or merge base lies past it; a window the
clone could not reach is unknown, never no (#3404). The base is
--base, else the set's :base, and one is required. Each evaluable criterion prints one
SET EVAL line with holds=yes|no|unknown and a why=; a criterion gh or git could not
answer is unknown and counts as not done. A unit is decided by its criteria when every
one is evaluable or one fails; a unit naming only :test, :job or :attested criteria keeps
its :status. Each question is asked once per run, and every PR the criteria name is read
before any is evaluated, in one gh GraphQL call per repo (100 PRs to a call; #3460); a PR
that call did not answer is read alone by REST.
--write-status (implies --evaluate) then rewrites :status "open" to "landed" for each unit
whose criteria all hold, one SET WROTE line per unit, and changes no other byte.

events publishes the family's three event channels from two sources: a card's end on
the cards:done stream (consumer group events) becomes card-done, and a poll of gh every
--gh-poll becomes pr-checks-done on a changed check-suite conclusion and dev-moved on a
changed base head. Only an ok or a fail entry is a card's end (or an entry with no event
field, written before the field existed); every other transition on the stream -- queued,
a turn, a decide event -- is acked and not re-announced. The poll is the fallback
heartbeat until the forge pushes a webhook; a quiet poll publishes nothing. Without
--repo only the stream is bridged.

--once reads the stream and polls the forge once, then exits. The loop form requires
--deadline and returns when it is reached.

Every event events publishes is also written as one structured JSON line (SPEC-LOGS.md
Part 2): the same five labels on every line -- source=nova-work, verb=events, bench, the
event kind (start, card-done, pr-checks-done, dev-moved, done) and level -- plus the
fixed fields ts, guid, card, pr, msg, dur_ms and err. The line goes to stderr, which
under systemd is the unit's journal and so a source Alloy already reads, or to the file
--log names, which Alloy tails on every bench. A secret value never reaches the line:
the emitter redacts anything credential-shaped before it leaves the process. The stdout
EVENTS OK line is unchanged; the JSON line is written beside it, never instead of it.

flags:
  --graph <file>  the node graph, as JSON: {"nodes":[{"id":"a","needs":["b"]}, ...]}
                  Required on both graph verbs; there is no default and no discovery.
  --node <id>     dependencies: the node to write a needs edge to, creating it when the
                  graph does not hold it yet. ready: the one node to evaluate; without
                  it, ready prints one row per node in seed order.
  --needs <ids>   a comma-separated list of needs for --node. --needs needs --node;
                  --node alone creates a node needing nothing.
  --file <path>   plan check and plan expand: the plan to read. set check: the work set.
                  Required, always: there is no default file and no discovery from the
                  working directory.
  --minds <file>  set check: the registry an :owner must name, as the decide lane's
                  ladder ({"minds":[{"name":"emma"}...]}), the bus roster
                  ({"participants":[{"name":"Emma"}...]}) or a plain list, one name per
                  line. The shape is READ, not guessed at from the name, and the match
                  folds case. Without it no owner is checked.
  --lanes <file>  set check: the lanes file a :lane must name, <name>\t<path prefixes>
                  per line. Without it no lane is checked.
  --done <ids>    set check: comma-separated unit ids that are done, beside what the
                  file's own :done and :status say.
  --ready         set check: also print one SET READY line per unit of the ready set,
                  each carrying its admission verdict (admit=go, or admit=held with the
                  dimension or path that held it and the unit holding it).
  --unit <id>     attempt record and attempt list: the unit, by the stable id A2 mints.
  --by <mind>     attempt record: the mind the attempt is filed under. next --take: the
                  mind the opened attempt is filed under; --for when absent.
  --outcome <o>   attempt record: ok | failed | uncertain, and the grammar's own green,
                  red, refused and abandoned. Every outcome but uncertain owes --proof.
  --proof <p>     attempt record: the termination proof (A3). A url, a sha or a path,
                  and which of the three is READ off the value rather than asked for.
  --pr <n>        attempt record: the PR this attempt produced, written onto the record.
  --for <mind>    next: the mind asking. It is the OWNER the unit must belong to, not
                  the rung: rung= on the NEXT line is the ladder's answer, a different
                  axis, and the line carries both.
  --machines <f>  next: the registry of minds the ladder routes over; the embedded one
                  when absent, exactly as nova-decide route reads it.
  --kind <kind>   next: the decide kind a unit carrying no :kind of its own is routed
                  as. The default is new-verb: a unit of a pit-stop set is a verb to
                  build unless its author says otherwise.
  --jev/--no-jev  next: ask Jev among the eligible rungs, or answer by the rules alone
                  with no key and no network. Asking requires --usage and --log, because
                  a call nobody can account for is refused rather than made: both are
                  probed before the first call, every decision is appended to --log
                  and every provider call's spend to --usage.
  --take          next: open the attempt on the unit chosen -- :state :live, the lane
                  and the writes charged to it from that instant -- under the set's own
                  lock, so the unit a mind is told to do and the unit it is recorded as
                  doing are one decision.
  --evaluate      set check: derive done from :acceptance through gh (:landed, :merged).
  --base <branch> set check: the branch :landed means; default the set's :base.
  --cache <dir>   set check: where --evaluate keeps one blobless bare repository per
                  repo for the merge; default <user cache dir>/nova-work/landed.
  --write-status  set check: rewrite :status "open" to "landed" where every criterion
                  holds, in place, one line per unit; implies --evaluate.
  --out <dir>     plan expand: the directory to write one card per node into. Required;
                  a card already there is left byte-identical, so a re-expansion appends
                  only the new card and mints no id.
  --max-bytes <n> plan check: the byte ceiling (default 65536). A file past it is
                  refused before a byte is parsed, never truncated.
  --max-depth <n> plan check: the nesting ceiling (default 64). A form past it is
                  refused at its opening byte.
  --max-nodes <n> plan check: the atom ceiling (default 4096). A plan past it is refused
                  at the atom's byte.
  --units <file>  ask and asks: the work set, in either form and read as data. JSON:
                  {"units":[{"id":"u1","title":"...","owner":"Emma","lane":"work",
                  "needs":[...],"acceptance":[...],"deadline":"...","branch":"..."}]}.
                  SPEC-WORKLANG: (work-set "id" ... :units ((unit "id" :owner "Stella"
                  :lane "work" :needs (...) :deadline "2026-09-18T18:00Z" :title "..."))).
                  Required on ask; there is no default and no discovery.
  --record <file> ask: where the ask is recorded when --units is SPEC-WORKLANG, which is
                  never rewritten. Without it the bus note is the only record, and one
                  ASK NOTE line says so.
  --owner <name>  ask: the friend the unit belongs to, spelled the way the bus's roster
                  spells it. asks: show only that friend's asks.
  --deadline <t>  ask: when the answer is owed, as 2026-09-18T18:00:00Z or the shorter
                  2026-09-18T18:00Z a person writes. The unit's own :deadline stands when
                  this is absent; a unit with neither is refused, and a deadline that is
                  not after --now is refused before anything is sent.
  --bus <dir>     ask: the bus checkout the note is sent on. asks: the bus to READ the
                  sent notes from, which needs --as and is the source of truth.
  --now <stamp>   ask and asks: the instant deadlines and ages are measured against;
                  the default is this run's clock and an unparsable one is a refusal
                  rather than a silent fall back to it.
  --bench <name>  events: the fleet name of this machine, the bench label on every
                  structured line. Without it, $NOVA_BENCH, else the short hostname.
  --log <path>    events: append the structured JSON lines to this file instead of
                  stderr. The file is the one Alloy tails; a path that cannot be opened
                  is refused naming --log, never a silent run with no log.

exit codes: 0 ran and passed; 1 set check read the file whole and found something wrong
with its content, one SET line per finding; 2 could not run (bad invocation, an
unreadable graph, plan or work set, a :deps cycle, an unknown node, a refusal).

example:
  nova-work dependencies --graph ./deps.json --node b
  nova-work dependencies --graph ./deps.json --node a --needs b
  nova-work ready --node a --graph ./deps.json
  nova-work plan check --file ./work.work --max-bytes 65536
  nova-work set check --file ./work-set.lisp --ready
  nova-work next --file ./units.lisp --for rowan-child --lanes ./lanes.tsv --no-jev --take
  nova-work attempt record --file ./units.lisp --unit certify:verb --by rowan-child --outcome ok --proof 8a132e77 --pr 1369
  nova-work events --redis 127.0.0.1:6379 --once
```

The session verbs `session start`, `session status` and `session stop` speak the socket protocol; `SESSION OK` is one shape printed by all three alike. A missing `--session` (the socket has no default path) or a socket nothing answers is one `WORK REFUSED` line on stderr, exit 2, ending `run: nova-work help`. The session's own refusals — `FAIL`, `RACED`, `REFUSED` — reach stderr and exit 1. The graph and plan verbs read the JSON dependency graph and the bounded `.work` plan as data: `plan check` and `plan expand` require a plan path and default to 65,536 bytes, 64 levels of nesting and 4,096 atoms (`--max-bytes`, `--max-depth`, `--max-nodes`); unknown kinds, absent dependencies and dependency cycles refuse. `plan expand` writes card directories for explicit `:node` entries and does not launch them; existing cards are left unchanged when expanding again. `dependencies --graph <file>` reads the graph and `--node <id> --needs <id,id>` writes dependency edges; `ready` prints whether each requested node's dependencies are terminal and accepted without acquiring a lease or reserving a slot. `set check` reads the other top form of the same language — `(work-set "id" … :units ((unit …)))`, the one a coordinator writes by hand — through that same bounded reader, and validates its content: a duplicate id, a `:needs` naming a unit nobody defined, a cycle, an `:owner` no `--minds` registry names, a `:lane` no `--lanes` file names, a `:deadline` that is not an instant. Every rule runs over every unit in one pass and each finding is one `SET` line, so a defective set costs one run rather than one run per defect. The two exit codes stay apart: exit 2 is a file that could not be read at all, exit 1 is a file read whole whose content is wrong, and the `SET OK units=… ready=… blocked=… owned=…` summary prints either way. `--ready` adds the mechanical ready set — a unit is done when it says so (`:done`, or a `:status` of closed, done, landed or merged) or when `--done` names it, and ready when it is not done and every need is done — so what can be pulled is derived from the language rather than maintained by hand. `nova-work help` also describes `clip`, which commits and harvests a worker's result before resetting its worktree; use that mutating workflow only with the intended worktree, branch, base and harvest destination.

Readiness is only half the question, so each SET READY line carries the other half: the
ADMISSION verdict from internal/jobs (SPEC-JOBS section 9, SPEC-WORKLANG A5 to A9).
`ready` is whether a unit's needs are closed, which the language answers; `admit` is
whether its resource vector is free, which the kernel answers. The ready units are run
through one admission in written order, so the ones marked `admit=go` are a set that may
run TOGETHER -- one live unit per lane (A6), no two intersecting :writes (A7) -- rather
than a list each of which could run if the others did not. A held unit prints what held
it and who holds it:

  SET READY unit=certify:verb owner=rowan-child lane=pulse deadline=- admit=go on=- by=-
  SET READY unit=harvest:bench owner=rowan-child lane=pulse deadline=- admit=held on=lane:pulse by=certify:verb

A held unit never holds the ones after it: A9 says a unit goes when its OWN needs are
closed and its OWN vector is free, so the pass neither stops nor waits at a refusal. The
authority here counts lanes and writes only -- set check reads a file and knows no bench
-- so a unit naming cpu or memory is reported as held on that dimension rather than
silently granted against a capacity nobody counted.

## nova-decide

```
nova-decide --questions <json file> [--state <file>|stdin] [--floor 0.9]
            [--base-url <url>] [--key-env JEV_API_KEY] [--prefix DECIDE]
```

One typed decision per call through TypeSafe Jev (`internal/decide`). The questions file maps each name to one question — `{"type": "choice"|"score"|"noul", "instructions": <text>, "criteria": {<option>: <description>} for choice, [<level texts>] for score, absent for noul}` (`{"questions": {...}}` also accepted). The state is a file, or stdin when `--state` is absent. The key comes only from the environment (`--key-env`, default `JEV_API_KEY`, `TYPESAFE_API_KEY` also accepted) and is never printed.

```
$ nova-decide --questions ./questions.json --state ./state.md --floor 0.9
DECIDE gate=go conf=0.93 risk=2.50 conf=0.81 floor=0.90 below=-
```

**Reading it.** Exactly one line, on stdout for a decision and on stderr for a refusal. Exit 0 when every answer is at or above the floor, 3 when any answer is below it — a suggestion, never an authorization: the caller keeps today's behaviour as the fallback. Exit 2 on refusal (no key, bad questions, provider error): `DECIDE REFUSED reason=<one word> <detail>`.

### route — the ladder of minds

```
nova-decide route --unit <json file|inline json> --usage <path> --log <path>
                  [--down-store <host:port>] [--card <path> --allowed-routes <path>]
                  [--jev] [--store-user <user>] [--store-password-env <NAME>]
                  [--store <host:port> [--user <acl user>] [--password-env NOVA_REDIS_BENCH_PASSWORD]]
                  [--registry <path>] [--floor 0.65] [--base-url <url>] [--key-env JEV_API_KEY]
                  (--usage and --log are REQUIRED whenever jev is asked)
nova-decide route --unit <json file|inline json> --no-jev [--registry <path>]
                  [--usage <path>] [--log <path>] [--store <host:port>] [--floor 0.65]
nova-decide route --unit-id <id> --kind <kind> [--files n] [--packages n] [--lanes n]
                  [--lane-owner <lane>] [--attempt rung:outcome:reason] [--platform <name>]
                  [--guard] [--secrets] [--touches guard|secrets|sandbox|sudo|deploy-keys|network]
                  [--fresh-take] [--deadline 45m] [--no-jev]
nova-decide route ... [--step-up] [--max-steps 3]
                  (below the floor, re-ask with that rung excluded from the criteria;
                   every step is a logged decision, and --max-steps caps how many)
nova-decide route ... [--paste]
                  (one more line, for a coordinator to act on:
                   ROUTE <unit> -> <mind> (<model id>) conf=<x>)
```

Who does this unit of work. The rungs come from a registry — a data file of minds (`name`, `lineage`, `height`, the `kinds` it is designated for, the `lanes` it owns, `availability`, how it is `ask`ed, and — for a rung that is a model rather than a person — the `model` id a unit dispatched to it runs with) — and the embedded default is the ladder Glenn named: Flash and Pro on the DeepSeek lineage at the bottom, the child rungs Opus (Rowan's) and Sol (Stella's) at **one** height in two lineages, the friends above them each owning a lane, Astra and Fable as the top pair, then all friends at once, then Glenn.

The answer is the **lowest rung the evidence supports** with confidence that the first attempt is right. Below the floor it steps **up** a rung, never down. A failed attempt re-enters the decision carrying its evidence — `--attempt "opus:failed:missed the cause"`, quoted, because the reason may hold spaces and an unquoted one arrives as three arguments — and the answer is the next rung automatically: **sideways first**, where the same height holds another lineage, then up. The ladder is the retry policy.

Two rungs are chosen by **kind** and not by height, and by machinery rather than by the provider, so no provider call is made for either: security — a guard, secrets, the sandbox, sudo, deploy keys, the network — reaches Johnny always, and so does a fresh take (the rungs below failed in two lineages, or a design with one author). Friends first: the DeepSeek rungs take mechanical kinds only (`rebase`, `stack`, `fixture-retarget`, `row-test`, `dogfood`, `fleet-chore`).

**`row-test` is card work by kind, because its size lies.** One row of a table-driven suite, on a leg whose card shape is already proven, starts at `pro` — not at the bottom. It has a kind of its own because the two names it used to wear both answered wrong: as `fixture-retarget` the ladder read one file and one package and answered `flash`, one rung under the rung that landed it; as `fix-with-red-test` it answered `opus`, two rungs over. Measured 2026-09-18, the schema campaign's row cards ran on `pro` and came back green at usd 0.03-0.04 and about 150 s each. The size term raises a row test's rung and never lowers it: one file is the shape of a trivial rebase **and** of a subtle codegen fix, and a count cannot tell them apart.

**`dogfood` is a kind, because a transcript diff is not a guard.** Reading a documented transcript against what the tool actually prints starts at the bottom rung. The kind exists because that work was being named `guard`, and `guard` is a **security** kind — it resolves to the designated mind on every path, at any height, at any floor, which is the rule working correctly on a unit that was described wrongly. Measured 2026-09-18: 21 Flash cards over dogfood transcripts found four real drifts for about 20 cents. Name it `dogfood` and it is priced at the rung that does it.

**A mechanical kind that failed on a card rung was not mechanical.** A confirmed failure on `flash` or `pro` takes **both** card rungs out for that unit and the answer is the child rung: a mechanical kind is one whose answer is a procedure, and a failure is the evidence that the procedure was not given after all, so the other card rung is the same mistake one height up. Sideways-before-up cannot reach this case, because `flash` and `pro` are the only two minds of one lineage standing at two different heights. An attempt that merely timed out is **not** a confirmed failure and takes nothing out.

**A designation on a reserved mind is a READ, never the work.** Johnny is `reserved`: a read, and the STOP a read can call, not the work itself. The rule used to answer `rung=johnny`, and routing the twenty real units of 2026-09-18 showed what that meant — **seven** units, six of them owned in the work set by `rowan-child`, dispatched to a mind that does not take work. The answer is now `rung=<the rung the evidence supports> read=johnny`: the work goes where the evidence puts it, and the security read rides beside it, on the line and in the log row. Every line carries the field, as `read=-` where there is none.

**Security never falls through.** `--guard`, `--secrets`, `--kind guard` and each `--touches` value attach the designated reader on every path — Jev on or off, at any floor, after any attempt. It is a kind and not a height, so no floor and no step-up touches the read. If no mind is designated, or every designated one is asleep, the decision is **refused**: security work is not dispatched with nobody reading it. Each touch is named **once** in the reason, in the order the enumeration puts it — `--secrets` beside `--touches secrets` is one fact, not `"secrets, secrets"`.

**The floor is per kind, and measured.** One floor for every kind is one number standing in for ten different questions. On 2026-09-18 it was 0.90 for everything, and all 13 provider-answered units came back between 0.70 and 0.78 and stepped up — a 100% escalation rate, against `tune`'s own 0.7 cap, and the end of "the lowest rung the evidence supports". The registry now carries a `floors` table: one row per kind, each the **p25 of the provider answers that stood**, each with the measurement in `from`. `--floor` on the command line still wins; a kind with no row keeps the built-in 0.9. Every line says which it was, as `floor_from=flag|kind|built-in`.

```
$ nova-decide route --unit-id sec --kind fleet-chore --files 1 --secrets --touches secrets --touches network --no-jev
ROUTE unit=sec rung=flash confidence=0.90 floor=0.70 floor_from=kind read=johnny wait=- next=- steps=1 reason="security is a kind and not a height: secrets, network, so a security READ by johnny is attached to this unit at any height, at any floor and after any attempt -- johnny is reserved for reads and for the STOP a read can call, and the WORK goes to the rung the evidence supports; kind fleet-chore starts at rung flash" ask=card
```

**A timeout is not a death, and a wait is not permission.** `--attempt opus:timeout` says the attempt fell silent; its expiry is UNKNOWN until something proves it dead, so the answer is the **same** rung with `wait=awaiting_termination` on the line, the floor does not move it and the provider is not asked. The same rung is an answer about who owns the work, **not permission to retry**: establish what happened to the attempt first. The exit code says it too — **1 is the verb saying NOT YET**, and only exit 0 is permission to dispatch. A security unit whose rung timed out carries both facts: `read=johnny` *and* the wait, and the rung named is the one the attempt left occupied. `--attempt opus:timeout-terminated:killed at 10m` is the proof of death, and only then does the ladder move on; `failed` and `abandoned` are confirmed failures and move it as before.

`--no-jev` answers by the rules alone — no key, no network, the same answer every time — so the loop runs on a bench with no API. With Jev, the provider is offered only the eligible rungs at the supported height and the one above it, so it can advise sideways or up but never down; a mechanical kind with no confirmed failure is offered its supported rung alone, so no decision is asked for it at all, and a confirmed failure restores that step-up offer (#1513); an answer below the floor steps up, and a provider error, or a rung nobody offered, leaves the rules' answer standing.

**What Jev is told is typed and enumerated**, and it is less than the evidence: one `field: value` line each for the kind, size buckets, whether the lane is one a mind on the ladder **owns** (`none`, `owned`, `other` — never which lane), an attempt-count bucket, a platform flag (`ordinary` or `named`), a security flag and a deadline bucket — every value checked against the closed set its field allows before anything is sent, so the boundary fails closed. **No registry string crosses it either**: a mind's name, its lineage and its lanes are local configuration, not public data, so the rungs Jev chooses between are **opaque ids** (`rung-1`, `rung-2`) described only by the step above the lowest rung offered, a per-call lineage label, whether that mind owns the lane, and how it is asked. The answer is mapped back to a mind here. The unit's id, its lane's spelling, its platform's name, its deadline and every attempt reason stay in the process. `--floor` refuses NaN, an infinity, a negative and anything above one, with one remedy line.

**The floor is a number with rows behind it.** The default is **0.65**, and it was 0.9 until the rows existed. Measured 2026-09-18: 39 real route calls over 13 units came back between 0.61 and 0.91, and the same unit with the same evidence came back 0.78, 0.80, 0.81 and 0.82 on four separate calls. A floor of 0.9 therefore stepped up on 13 units of 13 — the provider's answer never survived, and the route was the rules plus exactly one rung, bought with a call. A floor of 0.8 sits inside that noise, so the same unit routes to one mind on one call and another on the next. A floor of 0.95 sent an eight-file pull request a child had landed green all the way to `all-friends`. 0.65 sits below the whole band, so a step-up means the confidence actually collapsed; 0.7 was still inside it, and one rebase unit came back 0.68, 0.69 and 0.71 on three calls and routed two ways. Re-tune it from the log, never from a feeling about the model.

**Accounting is not optional.** Token spend reporting is an obligation and every decision is logged, so **`--usage` and `--log` are required whenever jev is asked**. A route that would call the provider without them is refused *before* the call, in one line naming the missing flag and the line to paste — a call nobody can account for is refused rather than made and then forgotten. `--no-jev` makes no call, so there is nothing to account for and both stay optional there.

```
$ nova-decide route --unit-id u --kind rebase --files 2 --packages 1
ROUTE REFUSED reason=no-accounting a jev call must be accounted for: --usage and --log missing; pass --usage ./usage.tsv --log ./decide.jsonl, or --no-jev to answer by the rules alone with no call to account for
```

**What a call spent is kept.** `--usage <path>` appends one row of the fleet's usage TSV — the same columns, through the same appender, that a swarm card's usage is written with, so `nova-tokens` reads a decision's spend the way it reads everything else. A call that **failed** is a row too, with a non-zero `rc`: its cost is real and unmeasured. A decision that made no call writes no row. The `--log` row carries the same numbers as `calls`, `tokens_in` and `tokens_out`.

Presence is tracked **per counter**: a 200 with a valid answer and no `usage` object has said nothing about what it cost, so `tokens_in` and `tokens_out` are written as `-` and are absent from the log row — a successful answer is not evidence of reported usage. An explicitly reported `0` is a measurement and is written as `0`.

**A refusal cannot unspend a call.** Where the route refuses *after* a call — the provider picks the top rung below the floor and there is nothing above it to step up to — the usage row and the log row (carrying `refusal`) are written **before** the verb exits, and the exit code stays the refusal's own (2). A refusal with no call behind it writes the log row and no usage row.

```
$ nova-decide route --unit-id card-41 --kind rebase --files 2 --packages 1 --no-jev
ROUTE unit=card-41 rung=flash confidence=0.90 floor=0.90 floor_from=built-in read=- wait=- next=- steps=1 reason="kind rebase starts at rung flash" ask=card

$ nova-decide route --unit-id card-41 --kind fix-with-red-test --files 3 --packages 1 --attempt "opus:failed:missed the cause" --no-jev
ROUTE unit=card-41 rung=sol confidence=0.95 floor=0.73 floor_from=kind read=- wait=- next=- steps=1 reason="kind fix-with-red-test starts at rung opus/sol; 1 prior attempt(s) burned rung opus/sol: sideways before up" ask=child
```

```
$ nova-decide route --unit-id t-1 --kind fix-with-red-test --files 3 --packages 1 --attempt opus:timeout --no-jev ; echo "exit=$?"
ROUTE unit=t-1 rung=opus confidence=1.00 floor=0.73 floor_from=kind read=- wait=awaiting_termination next=- steps=1 reason="the attempt on opus timed out (timeout) and is not known to have terminated: its expiry is UNKNOWN, so this is a WAIT on the same rung and NOT permission to retry -- establish termination first" ask=child
exit=1
```

**The step-up signal names the rung above it, and can take it.** Exit 3 used to say only *which question* fell below the floor, which left the reader to work out where the work goes next from a ladder they cannot see. Every line now carries `next=<rung>` — the rung **above** the one answered, from the same registry — and `-` where the answer is at or above the floor, where the work is waiting, or where there is nothing above. `--step-up` turns the signal into the step: below the floor it re-asks the **same question** with that rung excluded from the criteria, up to `--max-steps` (default 3). Every step is a decision in its own right — asked, answered, paid for — so each one is a row in `--log` and `--usage`, carrying its own reason, and the final line says how many it took in `steps=<n>`. `--max-steps` without `--step-up`, and a `--max-steps` below 1, are refusals naming the flag. A step-up that never gets above the floor is still exit 3: a suggestion, never an authorization.

```
$ nova-decide route --unit-id thin --kind new-verb --no-jev ; echo "exit=$?"
ROUTE unit=thin rung=emma confidence=0.60 floor=0.75 floor_from=kind read=- wait=- next=astra steps=1 reason="kind new-verb starts at rung opus/sol; below the floor on opus, so the answer steps UP a rung to emma, never down" ask=bus
exit=3

$ nova-decide route --unit-id thin --kind new-verb --no-jev --step-up --log ./decide.jsonl ; echo "exit=$?"
ROUTE unit=thin rung=astra confidence=0.60 floor=0.75 floor_from=kind read=- wait=- next=all-friends steps=3 reason="step 3: 2 rungs answered below the floor and emma, freddy excluded from the criteria; kind new-verb starts at rung opus/sol; below the floor on opus, so the answer steps UP a rung to astra, never down" ask=bus
exit=3
```

**An answer is checked against the question that asked it.** A choice answer must name one of the options the question offered: an answer the criteria never held (`deepseek-flash` to a question offering `continue|ask-all-friends|ask-glenn`), an answer naming **nothing at all** (`{"type":"choice","confidence":0.99}`), and an answer of the wrong type are all **provider errors** at exit 2, naming the answer and the offered set — not decisions with a confidence on them. A decision that was never made cannot be authorized by the number attached to it, and the floor never sees one.

**Reading it.** One line: the unit (the evidence pointer rule 10 owes), the rung, the confidence the floor was applied to, the floor, the typed `wait` (`-` or `awaiting_termination`), the `next` rung, the step count, the reason, and how that rung is asked — `bus`, `card` or `child`. Exit 0 the answer may be acted on, **1 the verb ran and said NOT YET** (a wait: the rung named owns the work and an attempt on it is not known dead), 3 below the floor (the line already carries the rung it stepped up to and the one above that), 2 on refusal. Only exit 0 is permission to dispatch.

**Before every Agent spawn: ask, then take the rung.** The route verb is the mechanism by which a model is chosen, not a report about one. A coordinator about to hand work to a child, a card or a friend runs this first, and `--paste` prints the one line it acts on:

```
$ nova-decide route --unit-id <id> --kind <kind> --files <n> --packages <n> \
      --usage ~/rowan-working/usage/decide.tsv --log ~/rowan-working/queue/decide.jsonl --paste
ROUTE unit=<id> rung=flash confidence=0.94 floor=0.90 wait=- next=pro steps=1 reason="..." ask=card
ROUTE <id> -> flash (opencode/deepseek-v4-flash) conf=0.94
```

Take the rung on the second line and nothing else: `ask=card` means cut the card on that **model id**, `ask=child` means spawn a child of that lineage, `ask=bus` means put the ask on the bus — the line prints `(ask-bus)` in place of a model id where the mind is asked and not run. Exit **0** is permission to dispatch; **1** is the verb saying NOT YET (a wait — establish what happened to the prior attempt before anything is started); **3** is below the floor, where the line already names the rung it stepped up to; **2** is a refusal. `--usage` and `--log` are required whenever jev is asked, so the spend and the decision are both on the record before the child exists.

The same decision runs by machinery on the swarm's own fill/launch path — `nova-swarm batch --route` below — so a card's model is the ladder's answer rather than a string somebody wrote in a TSV. A coordinator dispatching by hand asks the same verb over the same registry, and the two answers are the same decision.

### help — continue, ask all friends, ask Glenn

```
nova-decide help --state <json file|inline json>
nova-decide help [--hours 2] [--retries-on-rung n] [--failures-last-hour n]
                 [--self-inflicted n] [--class-recurring] [--landing-moved]
                 [--uncertainty 0..1] [--asked-all-friends]
```

The second decision, over what a line can count about itself: hours on the same problem, retries on one rung, failures in the last hour and how many were self-inflicted, whether a class is recurring, whether landing moved, and the uncertainty it states out loud. `ask-glenn` only ever comes after `ask-all-friends`.

```
$ nova-decide help --hours 3 --retries-on-rung 2 --landing-moved
HELP answer=ask-all-friends reason="3.0 h on the same problem; the friends have not been asked, and Glenn is only asked after they are"
```

**A sub-verb refuses an unknown flag by name.** `nova-decide help` with no arguments is the door the onboarding standard names and prints the banner at exit 0; `nova-decide help` with a flag it does not hold — or one given no value — is `HELP REFUSED reason=bad-flags` at exit 2, naming the flag. A flag that is silently swallowed is a caller who thinks they asked something and did not.

```
$ nova-decide help --state ; echo "exit=$?"
HELP REFUSED reason=bad-flags flag needs an argument: -state
exit=2
```

### The coordinator's line — route with the key sealed

The key arrives from the environment and nowhere else (SPEC-DECIDE rule 3), so a real route runs under `nova-secrets exec`. This is the whole line, as one paste:

```
nova-secrets exec --store ~/rowan-working/secrets --as studio \
  --key ~/.config/nova-secrets/studio.key --sops /opt/homebrew/bin/sops \
  --only JEV_API_KEY --require JEV_API_KEY -- \
  nova-decide route --unit-id <id> --kind <kind> --files <n> --packages <n> \
    --usage ~/rowan-working/queue/decide/usage.tsv \
    --log ~/rowan-working/queue/decide/route.jsonl
```

`--only JEV_API_KEY --require JEV_API_KEY` is the pair that matters: `--only` hands the child that one variable and nothing else, and `--require` refuses *before* the command runs if the store does not hold it, so a route never fails halfway with a key-shaped hole. The key is never an argument, never a file the tool reads and never a line it prints. `--usage` and `--log` are the accounting, required whenever jev is asked, and pointing every caller at **one** pair of paths is what makes the log a calibration record rather than a pile of them. Run several decisions under **one** `exec` — `... -- sh -c '<several nova-decide route lines>'` — rather than one decrypt per call.

Friend presence is read from `--down-store`, default `NOVA_REDIS_ADDR`; `--store` also supplies that address and additionally writes the decision event. Presence keys name seats (`friend:stella:down` excludes Astra and `friend:rowan:down` excludes Fable). A configured presence store that cannot be read refuses the route with exit 2 and `reason=presence-unavailable`: a route does not select a friend while their seat's status is unknown. With no presence store the route prints `ROUTE NOTE down friends not checked (no store)` and its JSON row records `down_checked:false`.

### outcome — the other half of the row

```
nova-decide outcome --log <path> --unit-id <id> --result green|red|blocked|skipped [--of-time <RFC3339>]
```

What **happened** to a unit a decision routed. Rule 8 asks for the decision to be logged beside the outcome it predicted, and this is the half nobody was writing: on 2026-09-18 the shared log held 78 rows, 73 escalations and **zero** successes, so `log --summary` had nothing to regenerate a starting rung from.

Run it the moment a routed unit lands or comes back failing:

```
$ nova-decide outcome --log ~/rowan-working/queue/decide/route.jsonl --unit-id row-card-9 --result green
OUTCOME unit=row-card-9 kind=row-test rung=pro result=green outcome=ok
```

`green` is `ok` and names the rung that succeeded, `red` is `failed`, `blocked` is `abandoned`, and `skipped` is `skipped` — a unit a precondition stopped before it ran, which is no rung's success and no rung's failure, so it moves no floor in either direction while still being a row `coverage` can see. The kind and the rung are read from that unit's last **decision** row rather than retyped, because a caller who has to retype them will eventually retype them wrong; an outcome for a unit no decision routed is a refusal, not a row. It appends a row of its own — the log is append-only and a row written is never rewritten — marked `source: outcome`, which the summary folds into the rung it names without counting a second decision.

### review — the Jev first pass over a pull request

```
nova-decide review --repo <owner/name> --pr <n> [--card <file>]
                   [--task <id> --store <host:port>]
                   [--post|--dry-run] [--ledger file|redis|file,redis]
                   [--store <host:port> [--user <acl user>] [--password-env NOVA_REDIS_BENCH_PASSWORD]]
                   [--ledger-path <jsonl>] [--pass-above <n>] [--bounce-below <n>] [--checks <list>]
                   [--usd-per-mtok-in <x>] [--usd-per-mtok-out <x>] [--skip-heads <file>]
                   [--base-url <url>] [--key-env <name>] [--gh <path>] [--stream <name>]
                   [--store-user <user>] [--store-password-env <NAME>]
                   [--no-jev] [--table] [--record <dir>] [--replay <dir>]
                   [--prompt <file|sha8>] [--conf <jev.conf>|none] [--pr-dir <dir>]
nova-decide review --repo <owner/name> --batch <file of pull request numbers>
```

Every harvested pull request, before any friend sees it (#2565). It fetches the diff, the card and the check rollup at the exact head, runs **five mechanical checks in Go with no model**, asks Jev **one** typed question for a 1-10 score, prints one typed line and appends the verdict to a ledger.

**The line starts `JEV`, never `DISPOSITION`, and carries neither APPROVE nor HOLD.** Its verdict is `PASS`, `BOUNCE` or `UNSURE`. Both landers read a verdict by its *shape* from any scanned account — the bash lander's `verdict_of`/`scan_body` and the Go gate's `ParseComment` (`internal/merge/verdict.go`) take a `DISPOSITION ... verdict=HOLD` line from `rowan-claude` as a hold, and `bin/land-loop-schema` counts any `verdict=APPROVE ... score=N/10` line from a FRIENDS login — so the earlier `DISPOSITION who=jev ... verdict=HOLD` shape, posted from `rowan-claude`, would have been read as a HOLD on every pull request it held. Every body line is defanged the same way: upper-case verdict words are lowered, and no line starts with `DISPOSITION`, `HOLD` or `#`, or carries bold.

```
JEV head=<sha40> verdict=PASS|BOUNCE|UNSURE score=N conf=<x> rubric=<sha8> base=ok|behind|conflict checks=donewhen:ok,selfcheck:ok,paths:ok,claims:ok,ci:ok,score:N model=<model> cost=$x explain=<one line>
```

The body under it is one line per check with its evidence, the token counts, and a sentence saying it lands nothing.

The five checks, and what each is for:

| check | ok when | the case it exists for |
|---|---|---|
| `donewhen` | line 2 of the RESULT is the bare word `DONE` (a blank line under RESULT is skipped); with no RESULT line, every Go test the body's `DONE-WHEN:` names is added by the diff (a named test the diff does not add is `missing`: it may be on the base) | a RESULT that has to qualify DONE has not finished |
| `selfcheck` | the added test exercises generated code **and** carries none of the self-check tells; `missing` on a pull request that is not a conformance cell, and fixtures under `testdata/` and pages under `docs/` are not read: they quote the tells because they are the specimens (#2621) | schema#1507 landed on a 10/10 with `check(true, ...)` as its only assertion |
| `paths` | every changed file is inside the card's `PATHS` globs; with no card, the body's `PATHS:` line is the bound (`dir/` means `dir/**`, a bare file name matches anywhere), and there only product code outside it fails -- a test-only file outside is the reader's one-point deduction, not a gate (#2536) | schema#1569 added a whole stub crate beside its one test file |
| `claims` | every file the RESULT's `files:` line names is in the diff | a RESULT written from intent rather than from the diff |
| `base` | off unless `--checks` names it: the read rubric's base gate. The pull request targets a trunk (`dev`, `main`, `fixed-table-form`) and is mergeable; a stacked base or a conflict is `base:fail`, an unread one `missing` (#2536) | 17 of the 397 friend-read heads of 2026-09-24 conflicted with trunk at the time of the read |
| `ci` | off unless `--checks` names it. When it is on, `ci-ok` on the pull request's exact head is success. A red or missing `ci-ok` is `ci:fail` and the explain names the failing jobs. A run whose `head_sha` is not this head does not count (#2704) | nova-tools #2519 at `907546af` scored PASS 8 while `ci-ok` and shards 1/4 and 2/4 on space and studio were red; #2522 at `8359db4f` is the pass |

**Tool-PR mode: the score is the lowest file group's (#2621).** One question over a whole tool pull request scored its size (Spearman -0.64 against diff bytes over the eight of the 2026-09-22 dry pass, confidence 0.00 on every one over 33 KB). So the question is asked once per changed-file group -- the files of one directory, `testdata/` left out, coarsened to the first two path segments past eight groups, and a group past the 64 KiB diff cap split into parts of whole files -- and the pull request's score is the lowest answer; the score evidence line names how many groups were asked and which was lowest, and the cost is all of the calls. A pull request with one group (every conformance cell) is asked the one question over the same state as before. `--record` writes one fixture per group (`<repo>-<n>-g<k>.json`) and `--replay` reads them back.

**Gates cap the score (#2536).** When `ci` or `base` is enabled and answers fail, the score on the line is capped at 7, the read rubric's rule that a gate failure is never an 8; the ledger keeps the raw answer.

**The prompt (#2536).** The one score question is asked with a prompt file: `--prompt <file|sha8>`, else the `prompt=` key of `--conf` (default `$NOVA_JEV_CONF`, for example `~/rowan-working/etc/jev.conf` on the Studio; unset or `none` reads no conf), else the embedded default, the seed (`fd94795e`), which stays the default until the tuning rule adopts a candidate over it (the 2026-09-24 run's best prompt `6b7343c3` is refused, so it ships only by name). A prompt and its pass threshold are tuned together: with `6b7343c3` and no `--pass-above`, the threshold is 8 (`jevcalib.TunedPassAbove`; at 7 this prompt passed 10.2% of the heads friends held), otherwise 7. Shipped prompts live in `internal/jevcalib/prompts/<sha8>.txt`; a prompt's `LEVEL` lines (none or ten) replace the ten score levels, and its `EXEMPLAR` lines are metadata, never sent. A named prompt that does not resolve refuses (`reason=bad-prompt`); it never falls back. The ledger row carries `prompt8`, and `rubric=` is the sha8 of the levels asked.

**`--pr-dir <dir>` (#2536)** reads each pull request from `<dir>/<n>/` instead of gh: `view.json` in the `gh pr view --json` shape (`baseRefName` and `mergeable` optional), `diff.txt`, and `check-runs.json` (the commit check-runs document; without it `ci` answers missing). It makes no GitHub call and refuses `--post`: it is how the calibration set is scored through the real review path.

A check with nothing to decide on answers **`missing`, never `fail`** — an absent card is not a failed card, and a missing check is neutral. **`ci`, when it is enabled, is the exception to that neutrality:** a rollup with no `ci-ok` at the head is a fail, because a missing answer would let a score above `--pass-above` PASS. The verdict, under the tuning: an enabled check that **failed** BOUNCEs; a score below `--bounce-below` (default 4) BOUNCEs; an unscored pull request is UNSURE; a score above `--pass-above` (default 7 with the default prompt, else 8 with `6b7343c3`) PASSes; anything between is UNSURE. `--checks` is `checks_enabled`, the checks that may decide (default `donewhen,selfcheck,paths,claims,score`). `ci` is off in that list: schema has no `ci-ok` job, and a default run that required one bounced every schema pull request. The loop turns `ci` on by naming it. A disabled check still runs and prints as `off-<answer>` so the scorecard can say what it would have done. `cost=` is the call's tokens at `--usd-per-mtok-in/out`, and `$-` when no rate is given — TypeSafe has published none to us, and a guessed price is worse than an honest dash. `--skip-heads <file>` skips, before any call, a pull request whose head is in the file (the loop's record of heads it has posted on).

```
$ nova-decide review --repo mas-bandwidth/schema --pr 1488 --dry-run
JEV head=8d2213c7a6ea7ac0359e1020edaaa7914b8f8df3 verdict=BOUNCE score=6 conf=0.21 rubric=816c4381 base=ok checks=donewhen:ok,selfcheck:ok,paths:ok,claims:fail,ci:off-fail,score:6 model=jev-latest cost=$- explain=claims: 1 of 2 files the RESULT claims are not in the diff: test/conformance/go/go.mod
```

**The symbol check is two-sided, and one side alone gets it wrong.** "Does the file mention a generated symbol" says *yes* to schema#1459, which calls the real `tableFixedSelect` for half its assertions and writes `// Simulate exactly what FixedLoad does` for the other half. So the check asks for a generated symbol **and** the absence of a self-check tell, and every tell cites the pull request a friend read it out of. That is also its honest limit: it is calibrated on 122 cells of one repository's conformance legs, and a tell is a string a future card can avoid writing while doing the same thing. It bounces a card to a recut; it lands nothing.

**With no card,** `PATHS` is inferred from the pull request's own `cell: <lang>/<row>` line and the line says so (`paths_from=pr-body-cell` in the ledger, and the posted comment says it in words). The inference comes from the cell's *identity*, never from the list of files the diff happens to touch — a bound read off the diff would pass by construction, and the row would then say a check ran that decided nothing.

**With `--task <id>`,** the card is read from the Redis task hash `task:<id>` (`HGET task:<id> title`). The title is parsed for `PATHS:`, `DONE-WHEN:`, and `RESULT:` keys. The line reports `paths_from=task` and `card=task:<id>`. `--task` and `--card` are mutually exclusive. `--task` requires `--store <host:port>`. `--task` cannot be used with `--batch`.

**One question, one call, one price.** `ScoreLevels` is ten levels in a fixed order: Jev is order-sensitive, so the same ten shuffled are a different question and a score from one ordering cannot be compared with one from another. A test pins the ordering by hash. The raw provider answer travels into the ledger beside the 1-10 that was printed, so that if the provider ever answers in level *indexes* rather than in the numbering the levels carry, both numbers are on the record and somebody can tell.

**Confidence, rubric version, and base gate on every line.** `conf=` is the provider's reported confidence (or `-` when unscored). `rubric=` is the 8-character sha256 prefix of `ScoreLevels` (`816c4381`), pinning which question levels produced the score. `base=ok|behind|conflict` is the base gate, derived from GitHub PR mergeability (`mergeable`, `mergeStateStatus`) or `git merge-tree` / `git merge-base`.

`--ledger file` appends one JSON object per verdict (the calibration record: a friend read at the same head is later a pair with it, and the weekly false-pass rate is counted off those pairs). The ledger row carries `who=jev`, `conf`, `rubric`, `base`, and both raw and mapped scores. `--ledger redis` (or `--ledger file,redis`) writes one `kind=jev` entry on `cards:done` (the fleet Redis `--store`, default `NOVA_REDIS_ADDR`), with `--user` (alias `--store-user`) and `--password-env` (alias `--store-password-env`, default `NOVA_REDIS_BENCH_PASSWORD`).

`--no-jev` runs the mechanical checks alone: no key is read, nothing is dialled, and the line prints `score=-` rather than a zero nobody gave, with `model=none cost=$0.0000`. `--record` writes each provider answer as a fixture and `--replay` reads them back, which is how the tests run: a Jev call costs money, so the 122-cell pass ran **once** (`internal/prereview/testdata/jev-2026-09-22/RUN.md` is that run's receipt) and everything since replays it.

Exit **0** when every pull request PASSed, **3** when any did not — a BOUNCE is a suggestion to recut, never an authorization — and **2** on refusal.

### classify — ask one typed question over one item

```
nova-decide classify --question <q> --evidence <file|-> --pointer <id>
                     [--version 1] [--decider rules[,jev|local]] [--floor 0.65] [--rules <tsv>]
                     [--tamper <file>] [--escalate-to <name>] [--log <path>] [--private]
                     [--key-env <name>] [--base-url <url>] [--usage <tsv>]
                     [--record <dir>] [--replay <dir>]
```

The generic door onto the question table (`docs/SPEC-DECIDE.md` D3). It exists **beside** the `--decide` flags on the tools that own the acts, and the reason runs both ways: a verb alone can be skipped, and a flag alone hides the question inside one tool where nobody else can ask or test it. So a shell script, a fixture, or a person with a text file and a question can ask anything nova-tools asks.

One line out, and one of three exits: **0** an answer at or above the floor (or a stopping member, which the caller acts on), **3** `unknown`, **2** a refusal. `unknown` is a member of no answer set — it is the absence of an answer — and what each caller does with it is always today's behaviour.

```
$ nova-decide classify --question harvest --evidence ./result.txt --pointer card-9
CLASSIFY question=harvest/v1 answer=unknown conf=- floor=0.65 decider=none stop=no below=- tamper=no why=no-decider skipped=- escalate=- pointer=card-9 bytes=412
```

**The chain.** `rules` is always consulted first whether or not you name it, because a question a table can answer is a call not worth making. A **stopping** member ends the walk before the floor is looked at, at any confidence: a first decider's stop is not undone by a later one's permission. Below the floor the answer is kept as `below=` and the walk goes on; when the chain is exhausted the answer is `unknown`, and `why=` says which nothing it was — `no-decider`, `below-floor`, `tamper`. `skipped=` names every decider the walk could not get an answer out of, as bounded `<decider>=<reason>` tokens and never a provider's own error text: without it a chain with no provider and a chain whose provider failed print the same words, and a failure behind a later success disappears from both the line and the row. `--floor` is a confidence: -1, 1.1, NaN and +Inf are refused as `bad-floor` at exit 2 before anything is asked.

**What never happens.** The evidence is redacted, bounded and framed between two markers carrying a nonce drawn fresh per call, every evidence line behind a `| ` so it cannot forge a marker; the instructions are constants and no byte of evidence is interpolated into them. A text addressed to a classifier is screened *before* any call and answers with the question's tamper answer at `tamper=yes`. `--private` evidence never reaches a decider that leaves the machine — the question falls through to the next one instead. An answer outside the question's closed set is a provider error at exit 2 and never a decision. `--log` writes a row carrying a **hash and a size** of the evidence, never its text.

**Asking Jev.** `--decider rules,jev` (or `rules,local`) asks the provider after the table, through the client the verb opens from `--key-env` (default `JEV_API_KEY`; the key is read from that variable, never from argv or a file) and `--base-url`, the same way `route` and `review` open theirs. A provider call is accounted for or not made: without both `--log` and `--usage` the verb refuses `no-accounting` at exit 2 before any key is read, and with no key in the variable it refuses `no-key`, naming the variable. Every call made writes one row of the fleet's usage TSV to `--usage` (provider `typesafe`, the call's tokens), before any refusal; a classification that made no call — `--decider rules`, or `--private` evidence that skipped `jev` as `jev=private-evidence` — writes none. `--record <dir>` writes the provider's answer to `<dir>/<question>-v<n>-<pointer>.json`, and `--replay <dir>` answers from that file with no key and no call, so a test of a classify caller runs offline; a missing fixture is skipped as a provider error and the answer is `unknown`.

### log — the escalation log

```
nova-decide log --log <path> --summary [--registry <path>]
```

`route --log <path>` appends one JSON object per decision: the evidence, the rung tried, its confidence and floor, **where that floor came from**, **who reads the work**, whether it stepped up, the source, the outcome and the rung that succeeded when they are known — and, beside all of it, `rowan_pick`, what the rules alone would have chosen. `log --summary` reads the rows back: the escalations per kind, the starting rung **regenerated** from the rows — the lowest rung carrying its own weight, with at least as many successes as failures — and, per kind, the **shape of the provider's answers** against the floor they were gated on. A kind with no success keeps the rung the table started from. The closing line carries `coverage=<outcomes>/<decisions>`, rows against rows: the two halves of rule 8's row, so the share of decisions with an outcome beside them is visible rather than guessed — it was 141 of 412 on 2026-09-19, and a floor tuned on a third of the rows is tuned on the rows somebody remembered.

The histogram counts provider rows only: the rules' own confidences are the machinery's numbers, and mixing them in hides the thing it exists to show. `below_floor` is that kind's escalation, counted rather than felt, and `defeated=true` says the floor is above **every** answer the provider has ever given for that kind — the step-up is then not a policy, it is the only outcome. A kind the provider has never answered prints dashes, never zeroes nobody measured.

```
$ nova-decide log --log ./decide.jsonl --summary
LOG kind=fix-with-red-test decisions=6 escalations=5 successes=0 failures=0 start_rung=opus/sol start_height=2 default_rung=opus/sol regenerated=false floor=0.73 floor_from=kind provider_rows=5 conf_min=0.72 conf_max=0.78 conf_p25=0.73 below_floor=1 defeated=false hist=0.0-0.5:0,0.5-0.6:0,0.6-0.7:0,0.7-0.8:5,0.8-0.9:0,0.9-1.0:0
LOG kind=guard decisions=1 escalations=0 successes=0 failures=0 start_rung=emma/freddy/johnny start_height=3 default_rung=emma/freddy/johnny regenerated=false floor=0.65 floor_from=built-in provider_rows=0 conf_min=- conf_max=- conf_p25=- below_floor=0 defeated=false hist=0.0-0.5:0,0.5-0.6:0,0.6-0.7:0,0.7-0.8:0,0.8-0.9:0,0.9-1.0:0
LOG OK rows=20 kinds=6 escalations=13 defeated=0 coverage=0/20
```

### tune --propose-floors — a floor per kind, measured

```
nova-decide tune --propose-floors --log <jsonl> [--registry <path>]
                 [--write <path>] [--floor-for <kind>=<floor>]
```

The floor is re-tuned from rows, never from a feeling about the model. For each kind this reads the **provider** answers the log holds, keeps the ones no failure was recorded against, and proposes their **p25** — a floor three answers in four would have cleared. A kind with fewer than two such answers is not proposed a floor at all and keeps the built-in default; the row says so rather than leaving a reader to infer it from a missing line.

A floor **above the provider's observed maximum** for its kind is refused with the remedy, and nothing is written. Such a floor does not gate a decision, it deletes it — which is exactly what 0.90 against a measured 0.78 was doing on 2026-09-18 — and it does not get written back into the file it came from.

`--write <path>` merges the proposal into a registry, leaving every other field of the file as it was, comments included; with no `--registry` it starts from the embedded ladder, so a bench that has never had a registry file gets one.

```
$ nova-decide tune --propose-floors --log ./decide.jsonl --write ./registry.json
TUNE FLOOR kind=fix-with-red-test rows=5 stood=5 failed=0 max=0.78 p25=0.73 current=0.73 current_from=kind floor=0.73 proposed=true defeated=false note=""
TUNE FLOOR kind=guard rows=0 stood=0 failed=0 max=- p25=- current=0.65 current_from=built-in floor=- proposed=false note="no provider answer for guard stood; a floor with no rows behind it is untuned"
TUNE FLOORS OK rows=20 kinds=6 proposed=5 defeated=0
TUNE FLOORS WRITTEN floors=5 path=./registry.json
```

`route --store <host:port>` also writes each decision as one `decide` event on the
`cards:done` stream of the fleet Redis, through the same writer every card transition uses
(`internal/events`), and `nova-pulse fold` keeps it in its `decisions` table. The event carries
`decide_log`'s fields under `decide_log`'s names — `unit_id`, `kind`, `files`, `packages`, `lanes`,
`lane`, `rung_tried`, `height`, `confidence`, `floor`, `stepped_up`, `escalated`, `designated`,
`source`, `rowan_pick`, `reason`, `wait`, `awaiting_termination`, `refusal`, `outcome`,
`rung_succeeded`, `calls`, `tokens_in`, `tokens_out`, `usage_failed` — with the row's stamp as the
entry's `at`. The evidence document stays in the JSON lines log: the stream carries ids and
counts, so a reason over 200 bytes is cut with a `...+<n>B` mark and the uncut text is in `--log`.
A counter the provider did not report is absent from the entry and NULL in the fold, never a
zero. The password is never a flag: it arrives in the variable `--password-env` names. The
`decide_log` table this replaces is retired (nova-tools #2623), and with it the table form of
`--log`, `--dsn-env` and `log migrate`.

```
$ nova-secrets exec --store ~/nova-bench/secrets --as swarm-hulk --only NOVA_REDIS_BENCH_PASSWORD -- \
    nova-decide route --unit-id card-41 --kind rebase --files 2 --packages 1 \
    --usage ./usage.tsv --log ./decide.jsonl --store space:6379 --user swarm-hulk
ROUTE unit=card-41 kind=rebase rung=flash ...

$ nova-pulse fold --db ./ev.sqlite --report
...
# decisions_by_kind
kind	decisions	units	stepped_up	escalated	refused	calls	tokens_in	tokens_out
rebase	1	1	0	0	0	1	937	12
```

`nova-decide tune` is the other half of rule 8: it reads a decisions log back and reports, per
confidence floor, what that floor decided, what it agreed with, and what it escalated.

```
nova-decide tune --decisions <jsonl> [--floors 0.5,0.7,0.8,0.9,0.95]
                 [--label label] [--choice decision] [--conf confidence]
                 [--max-escalation 0.7] [--default <answer>]
nova-decide tune --kind <kind> [--dsn <dsn>] [--decisions <tsv>]
```

**`--default` names what a below-floor row actually gets.** Escalation is not one thing. Where the
escalation is a step UP a rung — the `route` question — the row gets a different, more careful
answer, and a cap on the escalation rate is the right shape: escalating costs money, and the verb
should not spend it on more rows than the cap allows. Where the escalation is a fallback to ONE
configured default, the interesting number is how often that default **disagreed** with the row's
label, and no field held it. With a default named each floor also reports `defaulted=`,
`default_agree=` and `missed=`, and the best floor is the one that misses fewest; a tie is broken
by the agree rate and then by the higher floor. A default no labeled row ever answered is a
refusal, not a zero.

**It is a calculation, and what it says about a log is a statement about that log.** A floor for a
reading is set from that reading's own adjudicated evidence — truth labelled separately from the
observation, bound to the question version, the decider and the model — and this flag does not
manufacture any of that. The example below runs over
`internal/decide/testdata/reader-observations-2026-09-19.jsonl`, whose own README says it tunes
nothing: 47 answers from one day, labelled by later HOLDs, asked with a previous question version,
sparse in every stopping class but one.

**That boundary is executable, not advice.** Every labeled row is read for an `adjudicated`
marker, and there are three answers, not two:

| the row says | what it is | what `tune` does |
| --- | --- | --- |
| nothing at all | a log from before the field existed | reads it exactly as it always did |
| `"adjudicated": true` | adjudicated truth | tunes, and prints a floor |
| `"adjudicated": false` | an observation, joined afterwards | `TUNE REFUSED reason=not-adjudicated` unless `--observations` |
| anything else — `null`, `"maybe"`, `1`, `{}` | a status nobody can read | `TUNE REFUSED reason=adjudicated-malformed`, naming the line; **no flag admits it** |

An absent marker and an unreadable one are different faults: the first is a log that never
claimed a status, the second is a row that claims one illegibly, and reading the second as the
first is how an observation log tunes by accident. `--observations` admits a log that SAYS it is
observations; it does not admit one that says nothing legible.

```
$ nova-decide tune --decisions internal/decide/testdata/reader-observations-2026-09-19.jsonl \
    --floors 0.5,0.65,0.8,0.9 --max-escalation 0.7 --default opus-child
TUNE REFUSED reason=not-adjudicated … 47 of 47 labeled rows carry adjudicated:false … Re-run with --observations …

$ nova-decide tune --decisions internal/decide/testdata/reader-observations-2026-09-19.jsonl \
    --floors 0.5,0.65,0.8,0.9 --max-escalation 0.7 --default opus-child --observations
TUNE floor=0.5 decided=40 agree=27 agree_rate=0.68 escalated=7 escalation_rate=0.15 defaulted=7 default_agree=7 missed=0
TUNE floor=0.65 decided=35 agree=24 agree_rate=0.69 escalated=12 escalation_rate=0.26 defaulted=12 default_agree=11 missed=1
TUNE floor=0.8 decided=29 agree=23 agree_rate=0.79 escalated=18 escalation_rate=0.38 defaulted=18 default_agree=15 missed=3
TUNE floor=0.9 decided=22 agree=19 agree_rate=0.86 escalated=25 escalation_rate=0.53 defaulted=25 default_agree=21 missed=4
TUNE OBSERVATIONS lines=47 labeled=47 observations=47 best_floor=none reason="…"
```

The closing line is `TUNE OBSERVATIONS … best_floor=none`, never the `TUNE OK` line and never a
number. **Neither reading is a tuned floor for the who-reads question** — that reading is untuned
until its own adjudicated evidence exists. What the arithmetic shows is that the miss count is
expressible at all, and that escalation alone is silent about the cost the fallback carries.

### the question and its criteria are one pair

`nova-decide --questions <file>` loads the question file AND the criteria file it names. A
question file may carry `criteria_version`, `criteria_file`, typed `state_fields` and `machinery`
beside its `questions`; any other key beside them is a refusal that names it.

```json
{
  "criteria_version": "2026-09-19.3",
  "criteria_file": "criteria-reader.md",
  "machinery": "who-reads",
  "state_fields": [
    {"name": "security_shaped_package", "type": "bool"},
    {"name": "design_defaults_taken", "type": "int"},
    {"name": "notes", "type": "string", "optional": true}
  ],
  "questions": { "reader": { "type": "choice", "instructions": "…", "criteria": { "…": "…" } } }
}
```

The criteria file sits **beside** its question file — the name carries no path — and its own
`version:` line must match. What goes to the provider is the criteria first and the state second.
Before the call, the state's `key: value` lines are checked against the declared fields: a
required field the state does not carry, or carries with the wrong type, is
`DECIDE REFUSED reason=bad-state` at exit 2, naming the field, with **no request made**. An
absolute `criteria_file`, a parent escape, a symlink resolving out of the question's directory, or
a file over 64 KiB is `bad-questions`: a question file is not a way to read an unrelated local
file and post it to a provider.

The pairs this repository ships are in `docs/decide/`. They name configured ROLES and no roster;
one house's role binding and its trial rows are in `docs/decide/examples/`.

`"machinery": "who-reads"` says the question is answered UNDER the rules in
`internal/decide/readers.go` rather than by the provider alone, and the verb enforces them at the
call boundary. A settled security designation is taken **before a client is built** — no key is
wanted, **no call is made**, at any confidence — and every other rule constrains the answer before
it is recorded or printed. The line carries the whole decision:

```
DECIDE reader=security-designate conf=- floor=0.65 below=- source=machinery \
       required=design-authority,security-designate holder=design-authority hold=open \
       lifts_hold=false receipt=recorded reason="…"
```

`conf=-` is not a missing number: a decision the machinery settled had no provider answer, so
there is no confidence to print and none is invented — a display constant here becomes, one copy
later, calibration evidence about a call nobody made. `source=` says which of the two decided, and
`receipt=` says where the durable row went: `recorded` when the configured `--dsn` took it,
`not-configured` when no table was configured. A configured table that REFUSES the row is
`DECIDE REFUSED reason=decisions-write-failed` at exit 2, on the settled path and the answered one
alike: a caller told the decision succeeded while nothing was written has no receipt at all.

The decisions table carries both facts. Its TSV fallback gained a seventh column, `source`, after
the other six; a file written before it exists is six columns wide and is still read, with its
source unknown rather than guessed. `provider_confidence` is a dash for a row no provider
answered.

`tune --kind <kind>` reads the decisions TABLE rather than a JSONL log and prints the rows behind
one kind; a kind with no rows is a refusal, because a floor with no rows behind it is untuned. It
lists rows and reports no floor — the floors come from `--decisions`.

## nova-merge

`nova-merge` keeps the **evidence a stream lands on**: a typed read at a head, a
local gate's verdict for a merge, a typed classification of a failed merge-group
run, a batch gate that merges N heads onto a base and runs the tests, and a fold of
branches onto a base into one out-branch. Its contract is
[docs/SPEC-MERGE.md](SPEC-MERGE.md), which is normative; this section is the door.

The per-PR lander role is **retired** (stream is the unit, 2026-09-24): `init`,
`quickstart`, `add`, `add-branch`, `run`, `status`, `dry-run`, `packet`, `stop`,
`queue` (and `queue audit`), `wait`, `sweep`, `simulate`, `rebase`, `react`,
`land`, `integrate`, `stack` and `receipt` are gone, and each is now an unknown
subcommand at exit 2. A stream lands onto dev by hand until the stream-lander spec
names the verb that does it.

```
nova-merge version
nova-merge read     --lane <dir> (--pr <n>|--branch <name>) --who <name> --head <sha> --verdict approve|hold [--note <text>] [--redis <addr>]
nova-merge gate     --lane <dir> (--pr <n>|--branch <name>) --head <sha> --base-sha <sha> --merge <sha> --verdict green|red --summary <path>
nova-merge fold     --branches <file> --onto <base> --out <branch> --lane <dir>
nova-merge fold     --close-folded --pr <n>
nova-merge classify --lane <dir> --run <id> [--base-url <url>] [--key-env <name>]
```

`batch` has its own section below, with its full flag list.

### read and gate

A read and a gate are each **one immutable file in the lane's branch**, written to a
durable outbox first and pushed in a compare-and-swap loop, so a reader on another
machine records a verdict where every lane on that branch folds it
([SPEC-MERGE.md](SPEC-MERGE.md), *The read condition* and *The local gate*). The lane
directory is an existing one; the verbs that made lanes left with the lander role,
and the stream-lander spec says where these records live next (Redis, per the
"GitHub is a git remote only" ruling).

- **`gate --base <sha>`** — exit 2, naming `--base-sha`. The lane's branch and the
  base **sha** a gate was taken against are different words on purpose.
- **`read` with no `--head`** — exit 2. A verdict binds to the sha the reader had
  open, never to whatever the entry's head is when the verb runs: an approve
  recorded a minute after the author pushed is an approve for code nobody read.
- **a verb on a directory that is not a lane** — exit 2, and nothing written on the
  way past.

### fold

`fold --branches <file> --onto <base> --out <branch> --lane <dir>` merges the
listed branches in the file's order onto `--onto` in a scratch clone of the lane's
repository, drops a branch whose conflict it may not resolve or that stays red
after three tries, squashes the rest to one commit on `--out` and opens it as one
pull request — card PRs into a stream branch. `fold --close-folded --pr <n>`
closes the member pull requests a merged fold carried. See
[SPEC-MERGE.md](SPEC-MERGE.md), *The fold (#1142)*.

### classify

`classify` asks one typed decision about **one failed merge-group run**: was the
failure flaky under the queue's load, the environment, or the pull request's own
change. It is advisory and it merges nothing. The floor is 0.90 and is not a flag —
above it the kind drives the action, and below it the kind is `unknown`, neither
action is taken, and the line names the raw answer it did not trust:

```
CLASSIFY run=<n> pr=<n> kind=<flaky-under-load|own-change|environment|unknown> conf=<x.xx> floor=0.90 rerun=<yes|no> park=<yes|no> [below=<the raw answer>]
```

`flaky-under-load` and `environment` are `rerun=yes park=no`; `own-change` is
`rerun=no park=yes`. The evidence put to the route is bounded and public — the run,
its failing jobs, their packages, whether the pull request changed those packages,
and the runner — and never a secret and never a body. `--base-url` and `--key-env`
are the decide route's, as everywhere else; a run the host cannot read, a route that
will not answer, or a `--run` that is not a positive number is `CLASSIFY REFUSED`,
exit 2. See [SPEC-DECIDE.md](SPEC-DECIDE.md), *Git and GitHub — classify, order,
risk; never a merge*.

### batch

```
nova-merge batch --name <name> --pr <list> --repo <owner>/<name> --root <dir> (--reviewers <file> --lane <dir> | --no-require-holds --reason <text>) [--untyped-comments ignore] [--base <branch>] [--reference <mirror>] [--timeout <duration>] [--gomaxprocs <n>] [--require-lisp] [--no-require-checks] [--check-name <name>] [--receipt-file <path>] [--accept-control <dir>] [--sibling <name>=<url>@<ref>]
```

`batch` is the landing gate and **it pushes nothing**. It clones `--repo` under
`--root`, merges each `--pr` head onto `--base` in the order given on a branch
`rowan/<name>`, drops a head that will not merge and says so, then runs the suite —
`build`, `vet`, `vet-windows`, `test`, `lisp` — over what is left.

**Exactly one of `--reviewers <file>` and `--no-require-holds --reason <text>` is
required**, and neither or both is exit 2: a gate that cannot say whose reads it
honoured is not a gate. `--reviewers` names the reviewers file, and under it
`--lane <dir>` is required too -- the lane directory the typed read records are
read from, which may not be the literal `none`. `--no-require-holds` lands over
an unlifted hold and says so on the verdict line, which is why it demands a
`--reason`. `--untyped-comments ignore` sets aside untyped comments on the same
terms and demands the same `--reason` (SPEC-DECIDE reading 3, *No flag ignores a
hold*; nova-tools #1748).

```
BATCH OK   name=<name> base=<sha> head=<sha> members=<list> dropped=<list> skipped=<list> checks=<required|waived> [check=<name>]
BATCH FAIL <the same fields> step=<name> packages=<list> tests=<list> reason="<condensed failure capped at oneline.TailBytes (500); a red build quotes the compiler lines; a red test step starts stream=<root>/test-<round>.jsonl>"
BATCH DROP #<n> reason="the merge conflicts with the members ahead"
BATCH DROP #<n> reason="head <sha> has no green <check> (state=<pending|failure|none>)" check=<name>
BATCH SKIP <step> reason="<why it could not run>"
BATCH STEP <step> command="<what it runs>"
BATCH NOTE checks=waived reason="<what the caller took on>"
BATCH NOTE #<n> checks=<batch-branch|receipt> reason="<the gate's own evidence for this member>"
BATCH SIBLING name=<name> ref=<ref>
BATCH REFUSED: <reason>
```

`skipped=<list>` **names every step that did not run**, so a green line never claims a
suite it only ran part of: `BATCH SKIP lisp` went to stderr and `BATCH OK` said nothing
about it. **`--require-lisp`** turns a skipped lisp step into `BATCH FAIL` for a caller
who needs it run. A program that is not on `PATH` is also looked for under
`~/sdk/<toolchain>/bin` — this fleet's toolchains live there — before its step is
skipped.

**The toolchain is checked against the tree's `go.mod` before the first merge.** With
`go1.22` on `PATH` and a `go.mod` asking for 1.26 the whole gate ran and the failure
surfaced as `step=build reason="go: downloading go1.26 (linux/amd64)"` — a progress
notice naming nothing to fix. It is now one `BATCH REFUSED` with the remedy, and a
`go: downloading …` line is never what a `reason=` quotes.

**A red test step keeps the stream it condensed.** The gate used to reduce
`go test -json` to that one `reason=` and discard the rest, so a `--- FAIL` block and
its assertion text were nowhere on disk (#2626). The test step now writes the complete
stream to `<root>/test-<round>.jsonl`, whether the step passed or failed. Round is `1`
the first time that root keeps a stream and the next free integer after that, so a later
run in the same root does not replace the file. The working directory `<root>/<name>`
is removed at the start of the next run; the stream is not inside it. On `step=test` the `reason=`
begins with `stream=<path>`.

**A red build keeps the compiler lines.** `go build` prints `# package` then the
diagnostics; `reason=` used to quote only that header, so a failure in
`bench/tools/realpacket-gen` named the package and not `undefined: Foo`
(nova-tools #2499 item 3 / #2508). The reason is now the captured stderr with
those notices stripped, capped at `oneline.TailBytes` (500 bytes); the mark
`...+<n>B` says when more was dropped.

**`checks=required` is the default (edge 25).** A member whose own head has no green
required check is **dropped before the merge**, by name and with the state it was in.
The check's name is `ci-ok` in this repository — CI's one rollup — and a repo whose
rollup is named something else (schema's `tests`) passes **`--check-name <name>`** or
writes `required-check=<name>` in **`.nova-merge`** at the clone's root (#2499). The
flag wins over the file; a missing file is the default, not a refusal. The name is
printed as `check=<name>` on `BATCH OK` and on the check `BATCH DROP` so a lane script
can parse it (#2508). `checks=waived` omits `check=`. The gate
runs on one operating system and CI runs on three: three members went green under the
gate on linux and red on CI's windows legs, and the batch pull request went red after
the gate had said OK. A member that has not been green on its own is a member nobody
has judged on every platform, and putting it in a batch asks this gate a question it
cannot answer. A member whose head is **a batch's own branch** (`rowan/integration-*`) or is named by a
`BATCH OK` line in **`--receipt-file`** is admitted on the gate's own evidence instead of
the forge's rollup, read by the same
parser — so a batch pull request whose own CI is still running is never refused as a
member of the next one. `--no-require-checks` waives the whole check and says so on
`BATCH NOTE` and on the verdict line. The `vet-windows` step (`GOOS=windows go vet ./...`) catches the
build-level half of the same class on the bench, in seconds, with no second machine; it
does not catch a windows-only **test** failure, which is what the forge's own windows
leg is for.

**`--sibling <name>=<url>@<ref>` (repeatable) stages a checkout beside `repo/`.**
`--root/<name>` is rebuilt on every run, so a neighbour the tree's tests resolve
as `../serialize.go` is gone unless this flag clones it again (nova-tools #2499
item 2: schema's serialize runtimes). `name` is one path element — dots allowed,
so `serialize.go` is a legal dest — and `repo` and `tmp` are reserved for the
batch's own checkout and temp dir. `url` is a git URL; `ref` is a branch or tag.
The last `@` splits url from ref, so an ssh URL is
`git@host:path.git@v1.16.2`. A sibling that cannot be cloned is `BATCH REFUSED`,
not a red test. Tests stage a `file://` fixture; they do not clone the real
serialize runtimes.

## nova-review

One bounded, exact-revision **review packet** at the review layer, specified in
[docs/SPEC-REVIEW.md](SPEC-REVIEW.md). It builds the file a reader needs to
read one entry at one head — the range since that reader's last recorded head,
the rules the diff touches, the prior verdicts and the open findings — and it
never forms an opinion about code and never merges anything.

```
nova-review packet --lane <dir> (--pr <n>|--branch <name>) --who <name> --out <file> [--head <sha>] [--spec <path>]... [--rule <spec>:<n>]... [--max <n>] [--max-bytes <n>] [--diff-only] [--files <glob>] [--reuse <file>] [--timeout <seconds>]
nova-review mutate --repo <dir> --base <ref> --head <ref> [--test <name>] [--timeout <seconds>] [--max <n>]
nova-review mutate --repo <dir> --head <ref> --seed <patch file> --tests <package>[,<package>...] [--timeout <seconds>]
nova-review guard --repo <dir> --head <ref> [--tests <package>[,<package>...]] [--timeout <seconds>] [--max <n>]
nova-review version
nova-review help
```

`mutate` is the mechanical half of a read, taken off the reader, and it has two
forms. The **range** form reverts every non-test hunk in a throwaway worktree at
`--head` and runs the tests the change touched: they must fail, or the change has
no red test of its own. Its verdict line says how much it put back —

```
MUTATE <head8> reverted=<n> red=<n> green=<n> <PASS|FAIL>
```

— so "every non-test hunk" is a number a caller can gate on, and a `PASS` with
`reverted=0` is visibly a control that never ran. The **seed** form is the other
half, and the one every negative control in the accept gate is built from: one
deliberate defect goes INTO the head and the named suites must kill it.

```
MUTATE <head8> seed=<hex8> edits=<n> red=<n> green=<n> <PASS|FAIL>
```

`seed=` is the first 8 hex of the patch's SHA-256, so a report names which control
ran. The edit count is asserted, not reported: exactly one, counted from the
worktree after `git apply` and never from the patch's own `@@` header, else
`MUTATE REFUSED` and exit 2 before the seeded run. Five more things refuse rather
than answer, because each of them kills every seed and would print a `PASS` that
is not about the seed: a patch that does not apply, a `--tests` package `go list`
does not resolve at that head, a named suite already red at the unseeded head, a
seeded tree that does not BUILD (a control that did not compile is the `broken`
seed of SPEC-TOOLWORK §1 rule 6, whose want is the token `build` and not a kill,
and it kills every suite it is pointed at: `MUTATE REFUSED: seed does not build:
<the compiler's own line>`), and a `--timeout` deadline that killed the run
mid-flight. Neither form writes anything
into the repo it is pointed at, on any path. Full grammar in
[docs/SPEC-REVIEW.md](SPEC-REVIEW.md).

`--test <name>` is optional and range-only, and asks SPEC-TOOLWORK §1 rule 4(d)'s
own question: does THAT test detect the reverted change. The default verdict is
per FILE, which is stricter and a different question — a card that also touched a
second test file whose tests are correctly insensitive to the change came back
`FAIL` with its named `TEST:` red (#1849). Given, the verdict is that unit's, and
the line carries `test=<resolved name>` beside the usual counts:

```
MUTATE <head8> reverted=<n> red=<n> green=<n> test=<name> <PASS|FAIL>
```

The name is resolved among the test units of the files THIS RANGE CHANGED and
nowhere else, and may be qualified `<file>:<name>` where two of them declare it.
One that resolves to none of them, to more than one, or to a unit whose suite
could not be run is `MUTATE REFUSED`, exit 2 — never a vacuous `PASS`, never an
inferred `FAIL`, and never a guess at which test was meant. The co-touched units
are still counted and still listed; another test's result no longer answers the
question that was asked. Every `MORE` remedy carries `--test`, so rerunning it
asks the same question. The singular `--test` is the range form's unit and the
seed form's plural `--tests` is its package selector: together they are malformed.

A range that changes ONLY test files does not refuse — it ABSTAINS, on stdout,
still exit 2 (#1850).

```
MUTATE <head8> ABSTAIN reason=no-change-to-revert: every changed file is a test file, so there is no production hunk to revert and this control cannot be proved either way; choose the seed form's control or hold
```

`guard` is the post-landing negative control (#2042). It reverts the commit's
non-test files, keeps the tests, and runs the named packages. The verdict is
computed from exit codes and test names, never judged: `GUARDED` when tests go
red, `UNGUARDED` when they stay green, `COMPILER-HELD` when the revert does not
compile, `NOT-APPLICABLE` when the file is excluded on this OS. `--tests` is the
only judgement (which packages to run); omitted, the packages are the commit's
changed `.go` files. Both test tails and `platform=<goos>/<goarch>` are recorded.
The verdict is `status=`, never the last token. It writes nothing into the repo
it is pointed at.

```
GUARD <head8> platform=<goos>/<goarch> reverted=<n> red=<n> green=<n> status=<GUARDED|UNGUARDED|COMPILER-HELD>
GUARD <head8> platform=<goos>/<goarch> status=NOT-APPLICABLE reason=build-tags
```

Reverting nothing runs the head's own suite, so the control cannot be PROVED,
which is not the same as a run that broke and is not acceptance either: it is
never a `PASS`, never a `REJECT` and never permission to push. Every
`internal/docs` and `internal/ci` doc-rule repair has this shape, and each one
used to be sent to the seed form by hand. A range that changes NO test file is a
different condition and still refuses, `MUTATE <head8> no-tests-changed`: it can
be an ordinary production fix missing the red test it was required to have, and
calling that harmless is the inference this verb must not make.

The other verbs are `packet`, `version` and `help`. `guard` is above. `packet` is the one that works:
it reads one entry on a lane at one head and writes one bounded file, capped at
`--max-bytes` (default 131072), past which the packet holds the hunk list and
the command that prints the rest; `--max` (default 20) caps the prior-verdicts
table, the open-findings table and the rules section inside it. `--reuse
<file>` hands a second reader with the same range the same bytes and reads no
tree at all. `--rule <spec>:<n>` (with the matching `--spec`) writes a
**scoped** packet — the first line and only that rule's section, its text
quoted at the head, both sides when the rule changed since the base — and reads
no diff, no verdicts and no findings, so a rule question costs one rule, not a
SPEC-WORK-sized whole-packet build. `--diff-only` drops the context lines around a change, so a
multi-file PR contributes only its changed lines and none of the unchanged
whole-file context; `--files <glob>` narrows the diff to the paths
matching the glob, and both refuse to combine with `--reuse`.

**Reading it.** Success writes the packet to `--out` exclusively — packets are
immutable, and an `--out` that already exists is a refusal — and prints one
receipt line: `PACKET OK entry=… id=… head=… base=… range=… files=… hunks=…
rules=… prior=… open=… bytes=… cut=… reused=… out=…`. A `--head` that is no
longer the entry's head prints `PACKET STALE entry=… asked=… current=…`, exit 1,
naming the head it moved to. Every refusal is one `PACKET REFUSED: …` line,
exit 2, and a `--reuse` candidate built for another (entry, head, range) is a
`PACKET REUSE` line naming what it was built for.

## nova-wake

One blocking call at the **attention layer**, specified in
[docs/SPEC-WAKE.md](SPEC-WAKE.md). A window that coordinates other lines
spends its turns on a clock: it sleeps, wakes, looks at three places, finds
nothing, and sleeps again. Every one of those cycles is a model turn, and a turn
that learns nothing is the most expensive kind of nothing there is. `nova-wake`
is that cycle inverted — one call that returns the moment something moved, and
otherwise at a deadline you named, so the window pays one turn per **change**
rather than one turn per **tick**.

It watches **seven** sources — a bus inbox, the checks on a set of entries,
`RESULT.md` files written by other lines and, since the amendment of
2026-09-13, the comments and reviews on named or owned pull requests (`--pr`,
`--owned-prs`), the check runs on a named head (`--run`), a branch moving
(`--ref`) and an advisory lock released (`--lock`) — and says what moved. It
acts on none of them: **everything it prints is data.** A note it relays is not
an instruction, a failing check is not a verdict about whose fault it is, and a
report file is prose somebody else wrote.

There is one verb beside them that asks a question rather than reporting one.
`nova-wake probe --line <name>` reads a line's last sign from the bus checkout,
sends one caller-written ping if it is silent past `--silent-after`, and is
`UNAVAILABLE` after `--answer-within` with the reason **unknown** — the tool
never writes *out of credits* or *asleep*, because it has measured a silence and
nothing else. `nova-wake probe --here` reads this bench's load averages, CPU
count and process count from the operating system at that instant, so a
readiness receipt carries the numbers it was decided on. The word READY appears
nowhere in this tool's output: the receipt is yours, and it is a promise about
the next ten minutes.

There is another that asks who is awake rather than watching who changes.
`nova-wake awake --bus <dir>` reads presence over the bus cursors: for every
`from-<name>/CURSOR` lane, the newest commit touching the cursor is that
friend's last beat, `awake` inside `--window` (default 300s), `asleep` past it,
`unknown` where no cursor was ever written, one `FRIEND` line each capped by
`--max` (default 50) and one `AWAKE OK` verdict (docs/SPEC-WORK.md, **Presence**,
source `bus-cursor`). A `from-<name>/BEAT` file is also read, from its own
content, and a beat whose stamp is newer than the cursor reads `source=bus-beat`.
A BEAT carries a `until=<stamp>` lease; a beat whose lease is still in the
future reads `awake` `source=bus-beat` even when its stamp and cursor are both
past `--window`. Since #3144 `wait` writes no BEAT (the bus carries notes, never
beats; `--beat` and `--beat-lease` are accepted and ignored with one
`WAIT NOTE`), so this source reads only a BEAT an older wait left, and live
presence is `awake --store`, read from the store:

```
$ nova-wake awake --bus ./bus
FRIEND alice awake age=10 source=bus-cursor
FRIEND bob asleep age=600 source=bus-cursor
FRIEND carol unknown age=- source=bus-cursor
FRIEND rowan awake age=500 source=bus-beat
AWAKE OK friends=4 awake=2 asleep=1 unknown=1 window=300
```

**`beat` and `presence` are retired** (2026-09-27). The friend heartbeat
(#2610: the hash `friend:<name>` with a TTL, `friend:<name>:last` beside it)
and the one-line presence read are a friend's own runtime now, `nova-friend`
(`here` beats, `list` and `show` read); the two verbs answer with one refusal
naming it. What stays here is `awake --store`, which reads the same hash the
way it always did. Presence is Redis only: the bus carries notes, never beats
(#3144).

### First run

Point it at a directory holding `RESULT.md` files and give it a state file of
its own. `quickstart` passes `--baseline`, so the first run lists the world once
instead of recording it quietly:

  cp -R cmd/nova-wake/testdata/example-reports ./reports

```
$ nova-wake quickstart --state ./wake.state --reports ./reports
WAKE NOTE quickstart chose --baseline, --interval 5s and --max 5s, so a first run returns with the world listed once rather than blocking; --on-deadline report is the word it echoes back
WAKE at=2026-09-11T18:56:43Z as=- max=5s interval=5s on-deadline=report sources=reports state=./wake.state cold=false nova-bus=- pending=0
WAKE REPORT path=reports/first-job/RESULT.md lines=8 bytes=220 new
WAKE REPORT path=reports/second-job/RESULT.md lines=7 bytes=199 new
WAKE CHANGE after=0s polls=1 bus=0 entries=0 reports=2 lines=0 prs=0 runs=0 branches=0 locks=0 pending=0

$ nova-wake watch --state ./wake.state --max 5s --on-deadline report --interval 5s --reports ./reports
WAKE at=2026-09-11T18:56:43Z as=- max=5s interval=5s on-deadline=report sources=reports state=./wake.state cold=false nova-bus=- pending=0
WAKE QUIET after=5s polls=1 default=report sources-failing=0: deadline, default taken
```

The natural FIRST probe is `--here`, because it needs no bus, no state file and
no lock at all:

```
$ nova-wake probe --here
WAKE HERE at=2026-09-13T10:12:04Z load=2.41,2.10,1.98 cpus=10 procs=1344
```

How to read it. The **first** line is the opening `WAKE`, printed before
anything is waited on, so a transcript shows the call began and what it was told
to do — a tool call that prints nothing for twenty minutes and then prints
everything is, while it runs, indistinguishable from one that has hung. The
**last** line is the verdict, and its **second token** is the answer: `CHANGE`,
`QUIET` or `BROKEN`. Read that and never the exit code, which is 0 for both of
the first two — a deadline is not an error, it is the answer *nothing yet*, and
a change is not a failure even when what changed is a red check.

What a first run gets wrong, and what each one wants:

- **No `--max`, or no `--on-deadline`.** Both are required. The deadline is the
  one thing only you can state, because a watcher with no deadline is a window
  that is stuck rather than waiting and nobody outside can tell the two apart;
  the default is what *you* will do if nothing moves, echoed back on the verdict
  so the transcript records the decision. This tool takes no action itself.
- **No `--interval`.** The right cadence is a fact about the watched thing's
  rate, which only you know. Entries have their own, `--entry-interval`, and it
  is the expected length of the hosted run: an 8-minute CI run deserves one
  check at 8 minutes, not eight checks at one minute.
- **No source.** A watch with nothing to watch is a `sleep` with a longer name,
  and it is the one invocation that would look like it was working.
- **A `--max` over 60m.** A watch runs inside a tool call and every harness kills
  a call that runs too long. Ask your harness what its limit is and sit under
  it; 20m is the recommendation.
- **A second watch on one `--state`.** Two runs each write the whole map, so the
  later write erases what the earlier one learned. One state file per watch.

The flags a line retypes every call — `--bus`, `--as`, `--state`,
`--on-deadline` and `--receipt-max-words` — may live in a config file instead:
this tool reads `<cwd>/.nova-wake/config`, or the file named by
`NOVA_WAKE_CONFIG`, as `key=value` lines, and a flag given on the command line
wins. A required value named by neither the file nor a flag is still a refusal,
now naming the config file as a second remedy.

By default nothing here fetches: the bus checkout is read as it stands, and
every `WAKE SOURCE bus` line carries `head=` and `head-at=` so you can see it
stand still. Two flags change that, and never both at once — one fetch per poll,
never two:

- `--refresh --remote <name> --branch <name>` fetches through `nova-bus wait`
  and **moves no cursor**. Nothing is consumed, so any number of watchers may
  run. This is the one to reach for.
- `--advance-cursor --as <name> --remote <name> --branch <name>` fetches through
  the push inside `nova-bus inbox --advance` and **moves your own cursor**, so
  new mail is relayed within two advancing polls. It advances only behind a
  print and only as part of a bus poll before the deadline: a call holding
  unprinted notes prints up to the cap, says `WAKE NOTE bus advance deferred`,
  and does not fetch until they have printed. A cursor is a claim about what a
  reader has been shown, so it needs `--as`, it is one advancing watcher per bus
  and name, and there is no flag that advances somebody else's.

### serve: the process outside a session

`watch` runs **inside** a tool call and returns to a session that is already
awake. `serve` is the other shape — a process **outside** any session that
fetches the bus on an interval, and for each new note whose `To:` names you runs
one command with the note ids and nothing else. An empty minute costs one git
fetch and **zero tokens**; a harness interval that runs a model is not a wake
and is not this.

```
$ nova-wake serve --bus ./bus --as rowan --on-note ./wake-me --interval 60s \
    --state ./serve.state --hours 8 --remote origin --branch main \
    --receipt-max-words 40
```

Every flag above is required, and `serve` names **all** of the missing ones in
one refusal rather than one per run:

- `--bus <dir>` the bus checkout, `--as <name>` the name whose `To:` wakes you.
  **`To:` wakes; `Cc:` does not** — a note that names you on `Cc:` only is
  recorded and counted, never dispatched.
- `--on-note <command>` what to run. It is started for a note and for nothing
  else — never on the interval, never to receipt, never to look — and it
  receives note ids as arguments and no body: the line's own model opens the
  note. Dispatch is **coalesced**: every note queued when the command is not
  running is handed to one invocation, in bus order, at most `--batch-max`
  (default 20).
- `--interval <duration>` how often it fetches. The interval is the latency a
  person will accept, never the second.
- `--state <file>` its own state file; one writer per state file, and the
  delivery record of every note lives here, so a restart knows what was
  delivered, what was queued and what is `uncertain`.
- `--hours <h>` **this process's own deadline**, after which it starts nothing
  new — every ask, child or read has a written deadline (Glenn, 2026-09-09). A
  `<state>.stop` file — the state file's own path with `.stop` after it, beside
  it in the same directory — does the same thing on demand.
  It is a **decimal number of hours**, not an integer: `--hours 8` is a working
  day, `--hours 0.5` is thirty minutes and `--hours 0.02` is about a minute,
  which is how the tests and a first run try it. It must name a deadline of at
  least a second: a float can name one no run reaches (`--hours 1e-12` rounds to
  `0s`), and a process that exits 0 having polled nothing is a green that did
  nothing, so it is refused by name instead. A deadline shorter than one
  `--interval` is **not** refused — it polls once and ends.
- `--remote <name> --branch <name>` what it fetches. A `serve` that cannot fetch
  is a `serve` that cannot see its mail, so these are required with the rest.
- `--receipt-max-words <n>` how much of a receipt is printed. Add `--receipt` to
  send the bus receipt for every dispatched note; `--on-note-idempotent` if your
  receiver is safe to run twice, which buys one retry of an interrupted first
  attempt and never a third run.

A dispatch interrupted by a kill is `uncertain`, not lost, and it blocks that
receiver's queue rather than guessing: the exit line names the id and the
`nova-wake serve … --redeliver <id> --on-note <command>` that runs it again on a
person's word.

`serve` checks `nova-bus version` before its first line and refuses a `nova-bus`
that is not the one from its own release, because the freshness it promises is a
property of that program's push (docs/SPEC-WAKE.md, *How the checkout receives
mail*).

## nova-sprint

**Help** (#3254). `-h`, `-help` or `--help` on any verb or subverb prints
`usage: nova-sprint <verb> [<subverb>] [flags]`, every flag that verb takes
(one `--name <type>` per line, no defaults, since a default can come from the
environment) and the exit codes on standard output, and exits 2 without
dialling Redis. A mistyped flag stays the verb's one-line refusal on standard
error. `file -h` prints its own usage text (exit 2); the batch `task` verbs
(`cancel`, `move`, `front`, `block`, `unblock`, `sweep`) print theirs and exit 0.

**Capacity and the three model types.** `capacity bench [--tiers
<t>,...] [--kinds work|read|fix,...] <name> <slots>` sets a bench's slot
share (the most it runs at once; live, its slots are the machine ceiling
less the slots of the friends awake on that machine, so a sleeping friend's
slots are the swarm's and hers again when she is back) and what it advertises it can run (a
friend's slots, kinds and tiers are `nova-config friend set`, applied by
`nova-config apply` through the same Redis Function; the retired `capacity
friend` refuses naming it). A card's
`ROUTE:` names one of three model types, `frontier` (the most recent Astra or
Fable model only), `pro` or `flash`; `--tiers` takes those three words and
refuses any other, an empty value clears, omitted keeps the stored list. The
dealer never hands a card to a worker that did not advertise its type
(`TIER <consumer> advertises <list>, not <type>`); a worker with no tiers
stored is treated as `flash,pro`, so a frontier card only reaches a worker
that said `frontier`. A read is always pro. Nothing in code names a worker;
see [nova-sprint/copies.md](nova-sprint/copies.md).

**Pausing a worker (#4308).** `worker pause <bench:<b>|friend:<f>> [--as
<actor>] [--idem <k>] [--redis <addr>]` sets the `paused` flag on the
worker's desired hash (`<kind>:<name>:desired`) in one call and prints
`PAUSED <worker>`; `worker resume <worker>` clears it and prints `RESUMED
<worker>`; a flag already at the value prints the same word and writes
nothing. It is the one verb for benches and friends (it replaces `capacity
bench <b> 0` and the retired `capacity friend --paused 1` as the way to
pause, though `--paused 0|1` still works on a bench). The deal pass deals a paused
worker nothing; it keeps working what it already holds, so a pause is
never a cancel. The sprint table prints `paused` in the worker's status
column while it is up (down wins). A worker neither registry holds prints
`WORKER PAUSE REFUSED <worker> why="UNKNOWN ..."`, exit 1; a name that is
not `bench:<b>` or `friend:<f>` is a usage refusal, exit 2. `worker show
[<worker>]` prints one `WORKER <kind>:<name> slots=<n|-> paused=<0|1>
tiers=<list|-> kinds=<list|-> machine=<m|->` line per worker, the
`friends` and `benches` registries plus the `consumers` SET each once,
sorted by id, from the desired record alone (one read-only call), or the
one line for the worker named.

Renders the sprint table from Redis. `table --redis <addr>` makes one
`FCALL_RO ns_snapshot` per render over the `s:<S>:*`, `bench:*` and
`friend:*` keys and prints the table to standard output; `--once` renders one,
`--loop` one per second, and `--sprint <name>` shows a control sprint.
`--out <file>` publishes instead of printing (#3343): each tick writes
`<file>.tmp.<pid>` beside `<file>`, fsyncs it, and renames it onto `<file>`,
so a reader sees one whole table and never a partial or empty one, and the
temp file is gone after each tick. There is still no `--fixture` and no
`--refresh pending`: a reader runs the verb and reads stdout or the published
file, and a restarted unit re-renders from Redis on its next tick. `table --check --redis <addr>` renders the
fixture keyspace on a throwaway server and compares it byte for byte.
`refresh -- <command>` runs that command in its own session (POSIX setsid) and
returns without waiting, so `launchctl kickstart -k` of the loop unit does not
kill it. The unit plist `fleet/templates/nova-loop.plist.j2`, which
`fleet/loops.yml` renders for every loop, sets `AbandonProcessGroup` so launchd
itself signals only the unit's pid.

`table --layout live [--redis <addr>] [--sprint <name>] [--friends <a,b,...>]
[--once | --loop [<seconds>]] [--out <file>]` is the whole sprint table Glenn
watches (#3530): the headline (`SPRINT TABLE *** PIT STOP ***` while
`s:<name>:pitstop` or `sprint:<name>:pitstop` exists), the
`<landed>/<total> done <z>%, left <l>, eta <HH:MM> ET` line, the streams block and the
worker table, one blank line between them. The streams block is the
nova-table `streams` ([nova-table](#nova-table), `internal/ntable`): its
cells are bound to the sets named here, the tick reads them through it in
its one pipeline, renders through it, and the loop binds the table in the
store whenever its shape moves, so `nova-table render streams
--hide-zero-rows` prints the same block. The streams are the
rows of `ws:order` (the ws index, #3662) with
the ZCARDs of their `waiting`, `ready`, `working`, `review`, `reading`,
`merging` and `landed` sets (`ws:<stream>:<where>`, `ws:<e>:<stream>:<where>`
after a `sprint clear`), one column each in that
order (nothing folded: `review` is its own column between `working` and
`reading`, and `reading` between `review` and `merging`); rows
with all zeros are hidden. The headline, the rows and the total row are one
read of the one count, `ws.Counts` (internal/nsprint/ws/progress.go), the
numbers `sprint status` and `ws counts` print too: total is every card in
the six sets of the streams of `ws:order` (each stream's sentinel is its
stop, not work, and counted nowhere; parked and done cards are in none of
them; after `sprint clear` every count is 0, parked included: a clear is not
a cancel, a parked card stays parked in the epoch the clear left, and the
receipt says `parked_kept=<n>`), done is landed, left
is total minus done (a card in `review` or `merging` is left, not done), and
the ETA is now plus left over the cards (sentinels aside) moved to `landed` in
the last hour of `ws:log`, in Eastern (`+<n>d` when days out; `?` with no
landing in the hour; `-` with no card). Below the total, one
`LAND` line per open landing and one `REVIEW stream=<s> over=<n>
oldest=<id> age=<d> max=<d>` line per stream holding cards in review longer
than `cfg:review max_age` (seconds; one hour when unset; a card with no
`review_at` counts as over). The worker table (#4071) is ONE table for
friends and benches: `consumer | ready | working | done | ok | fail | ok% |
status | load`, one row per worker named `<kind>:<name>` (`friend:emma`,
`bench:hetzner`): the friends (the `--friends` roster, else the `friends`
SET sorted), then the `benches` SET, then any other member of the
`consumers` SET, each once, and a total row. Every cell is one ZCARD of
`<kind>:<name>:cards:<set>` (`<kind>:<name>:<e>:cards:<set>` at epoch e,
`sprint clear`) for set = `ready`, `working`, `ok`, `fail`; done
is ok + fail and ok% is ok over done, derived, with no sprint window and no
base from a `table clear`; a set that does not read prints `?`, never 0.
status is `up` when the worker's own beat (`<kind>:<name>:beat` at, ms)
is under a minute old and `<kind>:<name>:down` does not exist, else `down`
(the row still shows its cards); an up consumer whose desired hash has
`paused` 1 (`worker pause`, #4308) prints `paused` instead; load is the
beat's load1 (`-` for a beat with none, a friend's). The old `friend:<f>` row hash and `bench:<b>` hash
are not read. Every tick is ONE
pipeline (a second one only on the tick a set's membership changed), never
KEYS or SCAN, zero GitHub. `--redis` defaults to `NOVA_SPRINT_REDIS`, then
`NOVA_REDIS_ADDR`; `--sprint` to `NOVA_SPRINT`, which names the pit stop
read and must be the open sprint: another name is refused, as `sprint
status` refuses it, one line on stdout, exit 1 (the one-shot, the loop on
its tick, and `--compare`), `REFUSED table --layout live --sprint <S>: not
the open sprint; open=<open|-> remedy="nova-sprint table --layout live"`
(`NOVA_SPRINT=<S>` and `remedy="unset NOVA_SPRINT"` when the name came
from the environment) (#4411). `--loop 1` renders once a
second until SIGTERM; with `--out <file>` it is the table's one writer: it
takes the Redis lock `lock:nova-sprint-table` (`--lock <key>` names another),
refuses with exit 3 when another writer holds it, and publishes each tick by
writing a temp file beside `<file>` and renaming it; stdout gets one `TABLE
loop` line. A tick whose read fails publishes the last good rows and a
`stale:` line.

`table clear --checkpoint <file> [--redis <addr>] [--friends <a,b,...>]
[--by <name>]` (#3637) zeroes the landed column in under a second:
it writes the checkpoint (every landed task with its fields, every friend's
done count) and prints `CHECKPOINT`, then in one MULTI/EXEC moves every
`ws:<s>:landed` member to closed (`task:<id>` state, one `ws:log` entry
each), stores the done counts in `ws:done0` (which the table no longer
reads) and the receipt in `ws:checkpoint`, and prints `CLEARED ... ms=<n>`.
Waiting, ready, working, review, reading and merging and the worker table
are untouched.

`sprint clear --why <text> [--force] [--checkpoint <file>] [--by <name>]
[--redis <addr>]` (#4238) is the sprint table to zeros as ONE call,
`ns_sprint_clear`: one `HINCRBY sprint:epoch n 1`. Nothing is moved or
deleted. Every set behind a table is named by the sprint epoch it was
written under, so the next tick reads the new epoch's empty sets and the old
epoch's members are invisible for good:

| set | epoch 0 (the name before the first clear) | epoch e |
| --- | --- | --- |
| a stream's tasks | `ws:<stream>:<where>` | `ws:<e>:<stream>:<where>` |
| a consumer's copies | `<kind>:<name>:cards:<col>` | `<kind>:<name>:<e>:cards:<col>` |
| a sprint's dealer lists | `s:<S>:pool`, `s:<S>:waiting` | `s:<S>:<e>:pool`, `s:<S>:<e>:waiting` |

A store never cleared reads as it did (epoch 0 is the old names, no
migration). A record carries the epoch it was created under (its `epoch`
field; absent is 0) and lives in that epoch's sets for good: a move, a beat
or an end of an older epoch's card stays in its own epoch (an old copy's
beat or end refuses `NOTWORKING`), so a writer holding the old epoch never
makes a current cell non-zero. Every reader keys by the current epoch: the
tick reads `sprint:epoch n` after its cells in the same pipeline and reads
again when it moved, and `ReadLive` does the same. The names are spelled by
one rule (`ws.KeyAt`, `ws.ConsumerKeyAt`, `ws.SprintListAt`; `NS.card.ckey`,
`wskey`, `skey` in the library); `TestEveryTableSetIsNamedByTheEpochRule`
refuses any other spelling. A stream name may not start with `<digits>:`
(`STREAM bad name <s>: a leading <digits>: is the sprint epoch segment of
the set names`). Cards working or merging are in flight and refuse the
clear (`REFUSED sprint clear: INFLIGHT <n> cards working or merging; sprint
clear --force leaves them to epoch <e>`) unless `--force`. The pit stop is
kept, never lifted. `--checkpoint` writes every ws set's members (the
current epoch's) before the increment. The receipt:

```
CLEARED streams=10 cards=598 copies=38 consumers=11 epoch=1 parked_kept=0 by=rowan ms=0
PITSTOP kept sprint=fix
STREAM swarm: cards cards=164
```

`PITSTOP none` when no open sprint has a stop; one `STREAM` line per stream
with what the clear made invisible; `ms` is the one call's own time. The
same receipt rides the `sprint:epoch` hash (`n`, `at`, `by`, `why`, `from`,
`streams`, `cards`, `copies`, `consumers`, `pitstop`, `pitstop_sprint`) with
one `ws:log` entry (`epoch/<e>` to `epoch/<e+1>`). The old epochs' sets
stay until a reaper takes them (not built). Exit 0 cleared, 1 refused, 2
could not run.

`review post --id <primary> --verdict recut|redeal|reassign:<consumer>|drop
--why <text> [--to <consumer>] [--redis <addr>] [--actor <a>]` (#4072) is
the one way out of review. A card whose consumer copy fails (`card end
--fail`, with `--exit <rc>` and the result flags as evidence; a lapsed
lease; a read copy's fail; a second read under 8, which also moves the
author's copy from its ok set to its fail set) moves to `ws:<stream>:review`
in the same call as the copy's move to `<kind>:<name>:cards:fail`, with the
evidence on its record (`review_consumer`, `review_model`, `review_exit`,
`review_line` the last typed line, `review_wall`, `review_pr`,
`review_read`, `review_why`, `review_at`) and the mechanical first pass: the
failure shape (`review_shape`: `exit-<rc>`, `lease-lapsed`, `read-fail`,
`read-under-8-twice`, else the why's first word), its count for this card
(`same_shape`) and for this consumer (`same_shape_consumer`), and a
`REVIEW-JEV id=<id> consumer=<c> shape=<s> same_card=<n> same_consumer=<m>
suggest=<verdict>` line (`review_jev`), a suggestion, never a verdict. A copy
given back (`card cancel`, `card assign --revoke`, a down consumer's ready
copies) is not a fail: its card returns. Nothing else moves a card out of
review (a deal, a task move and a cancel are refused). `card cancel --ids
a,b,c --why <why>` is all or nothing: one bad id refuses the batch and
writes nothing. With `--each` (#4309) every id is cancelled on its own in
the same one call, so a refusal names its id and the rest still move:
`CANCELLED <id> to=<where>` or `REFUSED <id> why=<why>` per id in order,
then the count line `CARD CANCEL n=<ok> refused=<n> ms=<n>` last, exit 1
when any id was refused. The verdict is typed
(anything else is a usage refusal, exit 2) and applied through the one move
(`ns_cm_review`): `recut` returns the card to waiting, `redeal` to ready,
`reassign:<consumer>` (or `--to`) cuts its copy on that consumer, `drop`
moves it to landed with `outcome=dropped` and closes its origin issue with
the `REVIEW verdict=<v> by=<actor>: <why>` line. That line is on the record
(`review`) and is carried to the card's next copy, whose card file says why
it is back. One receipt line: `REVIEW POST id=<id> verdict=<v> to=<where>
copy=<copy|-> [issue=<repo#n|-> closed=yes|no [err=<why>]] ms=<n>` (a close
the forge refused says why, exit 1), or `REVIEW POST REFUSED id=<id>
why=<why>` with exit 1 (a card not in review, a consumer with no slots).

`table --compare <file> --redis <addr> --sprint <name> --friends <a,b,...>`
renders the #2674 port of rowan-tools `bin/sprint-table-redis` (the keys
that script reads, the bytes it prints, but for its progress line: the one
count's `<landed>/<total> done <z>%, left <l>, eta <HH:MM> ET`, never
sprint-xy's `sprint:<S>:xy`, masked with the bash's xy stale lines on both
sides, #4411; `--xy-file` is refused as retired), waits for the file's next
publish, and prints `MATCH` or a unified diff and exits 1.

### Unused verbs and the fold's verbs step (#3160)

```
nova-sprint verbs unused --store <host:port> --tools <dir of nova-* binaries built at dev> --repo <nova-tools clone at dev> --receipts <dogfood receipts dir> [--days 14]
nova-sprint verbs unused --check --store <host:port> --repo <nova-tools clone at dev>
```

`verbs unused` lists every verb the dev build ships (each binary's own
`help`) that has no use and no counting dogfood receipt in the window
(`--days`, default 14). A use is an entry with `verb` and `actor` on
`cap:log` or on `s:<S>:log` for every sprint in `sprints` or in
`sprint:order` up to the window's end (no KEYS or SCAN; its key is `tool`
plus `verb`, `tool` defaulting to nova-sprint). A dogfood receipt counts when
it names an inventory key, is ok with no edge, is by someone other than the
verb's git author, falls in the window and names `#<n>` or `card-<id>`;
every other line prints `VERBS SKIP receipt=<file:line> why=<field|parse>`.
It prints `VERBS STREAMS n=<k> sprints=<k-1>`, then appends one entry to
`verbs:unused:log` (fields `at, days, since, until, dev_sha, count, verbs,
resolved, prev`; `MAXLEN ~ 10000`) by WATCH/MULTI/XADD/EXEC, so `resolved`
(each key that left the list: `deleted`, `use` or `dogfood:<file:line>`)
always describes the entry `prev` names, and prints `VERBS UNUSED count=<n>
dev_sha=<sha8> id=<id>`. Exit 0 appended, 2 refused with nothing appended
(`inventory`, `authors`, `receipts`, `repo`, `store`, or a flag), 3 the tip
moved under three tries (`prev-moved tries=3`). `--check` reads the newest
two entries: `FALLING` (exit 0) when the count fell at a newer dev sha, or at
the same sha with every dropped key explained in `resolved`; `MISSING`,
`BROKEN-CHAIN`, `NOT FALLING`, `STALE` or `UNEXPLAINED` exit 1. Nothing
reaches GitHub.

`nova-sprint fold <S> ... --tools <dir> --repo <clone> --receipts <dir>` runs
both after the fold is recorded and prints each line prefixed `FOLD VERBS
sprint=<S>`. Exit 4 is folded with the check failed; 5 is folded with the step
not run (no verbs flags prints `FOLD VERBS sprint=<S> MISSING
flags=--tools,--repo,--receipts`); some but not all three flags is refused
(exit 2) before anything is read. A sprint already folded runs no verbs step.

### Merged-tree guards, dev-red and read carry (#3629, #3630)

`internal/nsprint/land/guard` is the merged-tree guard suite: a library any
lander calls with a repository path before the batch test of a stream
branch, one PASS/FAIL row per guard with the offending file (`lua-locals`,
`lua-crossfile`, `one-parser`, `catalog`, `named-paths`, `tracked-files`).
Each row is an interaction that was green per PR and red on the merged
tree; the next one is a row in the registry, not a hunt.

`dev-red status|check|watch|unwatch --repo <r> --base <b> --redis <addr>`:
the reconciler's dev-red duty walks `devred:bases` every pass; while the
base tip's CI record (`ci:<repo>:<sha>`, the GitHub leg `ci:<repo>:<sha>:gh`,
or the gated receipt; Redis only, never GitHub) is red it writes
`land:<repo>:<base>:red` (the key a lander reads through `land.RedBlocked`
before merging a stream into that base) and pushes ONE fix task to the
coordinator's queue naming the failing check; green clears it. `status`
prints `RED <check> <sha> task=<id>` or `GREEN <repo>/<base>`.

`ci github --redis <addr> [--consumer <seat>] [--once]` (#3597) is the
GitHub leg of CI in Redis: the `ci-github` consumer group of `ev:github`
turns each `check_run` and `workflow_run` delivery the webhook receiver
appended into one field of `ci:<repo>:<sha>:gh` (`check:<name>` or
`wf:<name>` = `<word> <id> <at>`, newest attempt wins) and refolds `gh`
(red if any is red, pending if any is pending, else green) and `gh_fail`,
writing and acking in one `ns_ci_github` call. `ci status --repo --sha`
prints the leg under our own record. Nothing in nova-sprint reads a check
state from GitHub or asks it to rerun one; a rerun is `ci request --again`.

`ci github --from-runner --redis <addr> --repo owner/name --sha <head> --run-id <n> --event <ev> --workflow <name> --conclusion <job.status> [--head-branch <b>] [--base-branch <b>] [--pr <n>] [--at <rfc3339>] --job <name>=<result>...` (card gh-ci-receipts) was the runner as the event source until the ci-ok job of `.github/workflows/ci.yml` moved to `nova-ci github receipt --from-runner` (see nova-ci), which writes only the `ev:github` row; this verb writes what the receiver path would have: one `ev:github` workflow_run row with sender `runner`, and `ci:<repo>:<sha>:gh` through `ns_ci_github` with `wf:<workflow>`, one `check:<job>` per `--job` (the run id as the id, so an older run's receipt is KEPT), `source=runner`, and the `--pr` number on the record's `pr` field. For a pull_request run that was not cancelled it also claims the PR's head on `pr:<repo>:<n>` (`land.RecordPRHead`: creates the record with the branch's card's stream or `-`, moves head and the head index `pr:<repo>:head:<sha>`, ordered by run id; an existing record's stream is never touched) and folds a final word onto every open record at that head (`ci`, `ci_sha`, `ci_at`, `ci_why`), printing `PR HEAD <key> outcome=created|moved|same|kept ...` and `CIGH FOLD <key> ci=<word> prs=<n,...>` (card pr-record-follows-github). `read brief --pr` and `land pr` refuse `STALE <key> head=<h> github=<g> src=<s> ev=<id>` when the record's head is not the head GitHub last named (or, for `land pr`, the REST head); `land pr` after MERGED writes the record from the REST reply when it has none (`land.RecordPRHead` source `rest`: head.sha, head.ref, the card `land.BranchCardID(head.ref)` spells; `PR <n> RECORD <key> outcome=created ...`), marks it merged and lands the card the record names (`PR <n> CARD <id> <from>->landed`), under a pit stop too (`PR <n> PITSTOP kept sprint=<S> scope=<scope> card=<id>`). Measured 2026-09-26 12:38 PM ET before it: `ev:github` XLEN 0 and no `ci:*:gh` key, because the signed receiver sits behind a tailscale funnel kept off by design, so `land pr` could only print WAITING. One `CIGH RUNNER <key> gh=<word> fail=<f> runs=<n> applied=<n> ev=<id>` line; exit 0 written, 1 the store refused the write (which reddens ci-ok), 2 usage. `webhook.Source(record)` says runner, hook or none for a record, `webhook.SourceOf(sender)` the same for an `ev:github` row, which `doctor`'s ingest line prints as `source=`.

`read digest --repo <r> --n <n>` records the diff identity of the head a
typed line is taken at (`diff_sha256` on the unit record; the reader runs
it at read time). `read carry --repo <r> --n <n>` compares it with the
unit's head now from the bench mirror and, when `git diff base...head` is
byte-identical after the base merge is normalised, copies every typed line
to the new head as a record with a `carried_from` receipt (`CARRIED`, exit
0); a changed diff is `REFUSED changed=<files>` (exit 1) and a re-read is
the one remedy. The lander counts a carried read as a read.

### First run

Run the three lines in an empty directory. They are the three file-shaped
first tries, and each is refused with the whole verb; the directory stays
empty. With a server, `nova-sprint table --redis 127.0.0.1:6379 --once` prints
the table, and `nova-sprint table --redis 127.0.0.1:6379 --loop --out
sprint-table.txt` publishes it by atomic rename.

```text
$ nova-sprint table --once --fixture table.txt
! nova-sprint table: flag provided but not defined: -fixture; the wide table is read from Redis and written nowhere: --redis <addr> [--sprint <name>] (--once | --loop), or --check --redis <addr>; the whole sprint table is --layout live [--loop 1] [--out <file>]; run: nova-sprint help

$ nova-sprint table --once
! nova-sprint table: --redis <addr> is required; the wide table is read from Redis and written nowhere: --redis <addr> [--sprint <name>] (--once | --loop), or --check --redis <addr>; the whole sprint table is --layout live [--loop 1] [--out <file>]; run: nova-sprint help

$ nova-sprint table --check
! nova-sprint table: --check needs --redis <addr>, a throwaway server for the fixture keyspace; run: nova-sprint help
```

What a first run gets wrong, and what each one wants:

- **`--fixture` or `--refresh pending`.** These were the file cut, deleted by #3326; both are unknown flags. `--out <file>` is the one file the wide table writes (#3343), by writing a temp file beside it and renaming it; `--layout live [--out <file>]` remains the one published whole-sprint table (#3530).
- **`nova-sprint table` without `--redis`.** It wants the server address. There is no default address and no default loop.
- **`--check` without `--redis`.** It wants a throwaway server; it seeds the fixture keyspace when the store is empty, and refuses a store whose `sprints` set holds a non-control sprint (the live fleet). `--check --live --redis <addr> --out <file>` checks that the published file is younger than 2 s and every rendered cell matches a direct Redis read in the same second.

There is **no `quickstart` verb**. A one-word first run would have to invent a fixture path or publish a table nobody named. The three lines above are the first run, in an empty directory that already holds `table.txt`.

**The fleet Redis has its default user off**, so every `--redis` verb against it needs the ACL user *and* its password in one pair: `NOVA_SPRINT_REDIS_USER=bench` and `NOVA_REDIS_BENCH_PASSWORD` (the password reaches the process through `nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD`, never a flag). A password with no user is refused with a line naming the missing variable and the pair.

**The Redis verbs need the `nova_sprint` function library on the server** (#3196). `nova-sprint fn load --redis <addr>` installs the library embedded in the binary with `FUNCTION LOAD REPLACE` and prints `LOADED nova_sprint sha=<sha>`; when the server already holds that exact source it loads nothing and prints `UNCHANGED nova_sprint sha=<sha>`, so a converge runs it every pass. `nova-sprint fn check --redis <addr>` changes nothing and prints `OK nova_sprint sha=<sha> ping=PONG` (exit 0), or `MISSING`, `STALE loaded=<sha> want=<sha>` or `NOPING` (exit 1): that exit is the bench-conform line for the fleet Redis. On `MISSING` or `STALE` it does not call `ns_ping` (`ping=skipped`), since the server's `ns_ping` is then not the embedded one and may write. The address authenticates the way every other `--redis` verb does: set the pair `NOVA_SPRINT_REDIS_USER=bench` and `NOVA_REDIS_BENCH_PASSWORD`, and a password with no user is refused naming the missing variable.

**The deploy loads the library with one verb** (#2937). `nova-sprint fn deploy --redis <addr> [--want <sha>]` is what the rowan-tools fn-load play (the last play of `make -C fleet tools`) runs as the coordinator seat: `fn load`, then a read-back of the library the store holds and `FCALL ns_ping 0`, and one receipt line, `FN RECEIPT at=<utc> store=<addr> load=LOADED|UNCHANGED sha=<sha> version=<build> ping=PONG` (exit 0; the rerun is `UNCHANGED`). It refuses with one `FN REFUSED store=<addr> reason=digest-mismatch ... remedy=...` line and exit 1 when `--want` is not the digest this binary embeds (nothing is loaded: the coordinator runs another build than the declared one) or when the store holds another library after the load. `--dry-run` changes nothing: `FN OK` when the store is current, `FN WOULD-LOAD store=<addr> got=MISSING|STALE ...` when a deploy would load. `nova-sprint fn sum` prints `SUM nova_sprint sha=<sha>`, the digest this binary embeds, so a deploy reads its `--want` from the declared build's own binary.

`nova-sprint backpressure check --sprint <name> [--redis <addr>]` (#3276) refuses a second backpressure source of truth beside `s:<S>:backpressure`. It reads the named keys only (the sprint's hash, the `proc:backpressure` beat and the legacy global `backpressure` hash) with one EXISTS pipeline, never SCAN or KEYS, and prints one receipt: `BACKPRESSURE CHECK OK sprint=<S> own=<0|1> beat=<0|1> legacy=0 round_trips=1` (exit 0), or `BACKPRESSURE CHECK REFUSED ... legacy=<keys> round_trips=1 remedy=...` (exit 1); usage or an unreachable store exits 2.
### read

A friend read makes zero GitHub calls (#3599, umbrella #3594: GitHub is a
git remote only). `read brief --repo <r> --n <n> --out <dir> [--mirror <dir>]
[--redis <addr>]` reads the PR record `pr:<repo>:<n>` (head, base, base_sha,
paths, done_when, stream, depends_on, branch, who) and the typed lines already
posted (the list `pr:<repo>:<n>:lines`) in one pipeline, the CI hash at head
(`ci:<repo>:<head>`, #3597) in one HGETALL, and takes the diff from the bench
mirror with `git -C ~/nova-bench/mirror/<repo>.git diff <base_sha>..<head>`
(the rowan-tools `mirror-refresh` loop keeps `refs/pull/*/head` there; the
verb never fetches). It writes `<dir>/brief.md`, the read brief rendered
from `internal/nsprint/read/tmpl/read.tmpl` with the record, the CI lines, the
posted lines, the file list and the files outside PATHS, and `<dir>/diff.patch`,
then prints one receipt:

```
READ BRIEF repo=nova-tools n=7 head=b7628a80 base_sha=1a1ad594 files=1 outside_paths=0 lines=1 ci=2 out=<dir>/brief.md diff=<dir>/diff.patch github_calls=0
```

A record with no head, a record with no base_sha, a mirror without the head
yet, and a missing mirror are each one `READ BRIEF REFUSED repo= n= why=`
line naming the remedy (exit 1); a head the mirror's `refs/pull/<n>/head`
has moved past is reported as `mirror_head=` and as a `HEAD MOVED` line in the
brief, and the record head is what is read.

`read brief --id <task> [--sprint <S>] --out <dir> [--mirror <dir>] [--redis
<addr>]` is the same brief for a read task, so a friend holding one needs
nothing but its id. It reads the task hash (`task:<id>`, and
`s:<S>:task:<id>` with `--sprint`, in one pipeline), which names the PR by
its `repo` and `pr` fields or by `ref` (the PR URL the first read pushes, or
`<repo>#<n>`), and the head the task was queued at; the brief reads that head
(its diff and its CI) even when the record has moved on, and the receipt adds
`record_head=` then and `task=<id>` always. A task that is not a read (kind
`read` or `review`), names no PR or has no head is one `READ BRIEF REFUSED
task=<id> why=` line (exit 1).

`read post --repo <r> --n <n> --line "<typed line>" [--no-github] [--owner
<o>] [--redis <addr>]` stores the line: the first word is one of SCORE, HOLD,
REPAIR, SPEC, SPEC-WRITTEN, CLOSE or JEV-DIFF and the first line carries
`who=<name>` and `head=<sha>` (a SPEC line: `who=`, `rev=<k>` and
`score=<0..10>`, no head, see `spec` below), or the line is refused. It RPUSHes the line
onto `pr:<repo>:<n>:lines` and stamps `last_line` and `last_line_at` on the
record in one MULTI, then mirrors it as one
REST comment (`POST /repos/<owner>/<repo>/issues/<n>/comments`, the token the
seat's seats.tsv row names, else `GH_TOKEN` or `GITHUB_TOKEN`, the base URL from
`GITHUB_API_URL`). `--no-github`
is Redis only (the tests count HTTP calls: 0 with it, exactly 1 without). A
comment that fails after the Redis write is `READ POST REFUSED ... redis=ok
github=<why>` (exit 1): the line is in Redis, which is the record.

```
READ POST repo=nova-tools n=7 kind=SCORE lines=2 github_calls=1 comment=4242
```

`read brief --pr <n> [--repo <r>] [--issue <ref>] [--mirror <dir>]
[--no-github] [--redis <addr>]` (#4335, #4315) is the whole read in one
command, printed to stdout in one screen, so a cold reader scores from its
output alone. `--repo` defaults to `nova-tools`. Sections, each from the copy
Redis holds: the ISSUE (title and body); the CARD, the imported card record
`task:<task>` the PR record names (harvest writes `task=<primary>`), with its
PATHS, DEPENDS-ON, base, DONE-WHEN and the spec lines of its body (DO,
EVIDENCE, SEAMS, RULES, RECEIPTS, KEEP); DONE-WHEN from each source that has
one (issue, card, record); the PR title and body (`pr_title` and `pr_body` on
the record, which harvest writes with the text it opened the PR with); the
FILES with `+added -deleted` (`git diff --numstat` in the bench mirror,
`base_sha..head`, else the merge base of the base branch and head) and the
files outside PATHS (the record's, else the card's, else the issue's); the CI
at head, our checks (`ci:<name>:<head>` and one receipt per check, with the
first FAIL line) and the GitHub leg the webhook ingest writes
(`ci:<name>:<head>:gh`), with a `FAILING:` line naming every red check; the
lines already posted; and the rubric with the post command. Nothing polls
GitHub. Redis keeps an issue's text only on a card pushed from it (`source=issue`:
the card body is the issue text); an issue the card only names (its SOURCE,
ORIGIN or ref, or `--issue`) is one REST read, `GET /repos/<o>/<r>/issues/<n>`,
and a PR with no `pr_body` (a hand PR) is one `GET /repos/<o>/<r>/pulls/<n>`.
The `SOURCES` line names where every section came from (`redis:<key>`,
`mirror:<dir>`, `rest:GET <path> (Redis has no copy)`). `--no-github` makes
zero HTTP calls and prints each section it could not fill as a `READ BRIEF
GAP` line. The last line is the receipt; exit 0 complete, 1 with gaps (or a
record with no head: `READ BRIEF REFUSED`), 2 could not run:

```
READ BRIEF DONE mas-bandwidth/nova-tools#7 head=b7628a80 files=2 outside_paths=0 failing=1 lines=1 gaps=0 github_calls=1
```

`read post --file <scores.tsv> [--mirror <dir>] [--no-github] [--redis
<addr>]` posts many typed lines in one call. A row is `<repo>\t<n>\t<typed
line>` (a literal `\n` in the line is a newline, so a SCORE's numbered items
fit on one row; blank rows and `#` rows are skipped). Each row is exactly one
`read post --line` (gates measured, the comment mirror unless `--no-github`),
its receipt or refusal printed under `ROW <i>`; a malformed row and a post
that printed no receipt are refused, never skipped. The tally is last; exit 0
every row posted, 1 a row refused, 2 a row could not run:

```
READ POST FILE file=scores.tsv rows=4 posted=3 refused=1 github_calls=0
```

Every brief this repository ships (the nova-sprint brief templates, the read
template, the `nova-swarm template` cards and the swarm's card fixtures) is
scanned by `internal/ci` (TestNoGhInAnyBrief, #3600): a `gh ` invocation, a
GraphQL mention, or a GitHub clone without the bench mirror as `--reference`
is a red run.

### jev

Jev runs the mechanical passes first, on every PR, before any friend read
(#3631). `jev mech --repo <r> --n <n> --body-file <f> [--mirror <dir>]
[--redis <addr>]` reads the PR record `pr:<repo>:<n>` (head, base, base_sha,
paths, reads) in one HMGET, the changed files `<base_sha>..<head>` from the
bench mirror (default `~/nova-bench/mirror/<repo>.git`; the verb never
fetches) and the PR body from the file, runs three passes (`internal/jev`)
and appends ONE typed line to the record's `reads`:

- lint: every typed body line present, once, in its one form: `BASE:` (one
  branch), `base-sha:` (7-40 hex), `PATHS:` (parses), `DEPENDS-ON:` (`none`
  or `owner/name#n[, ...]`, an optional `(WHY: ...)` after), `DONE-WHEN:`,
  `STREAM:`, and `Closes #<n>` (or `ORIGIN:`). The refusal names the line.
- scope: every changed file inside the body's PATHS (else the record's).
- base: the PR targets dev or main, the base the body names, cut from the
  record's base_sha.

```
JEV who=jev pass=mech head=<sha> gate=ok|fail lint=ok scope=ok base=ok why=-
```

A pass with nothing to decide on (no mirror, the head not in the mirror yet,
no base) is `missing`, which is not `fail`. The line is never a read:
`stream.ReadAt` skips every `who=jev*` line and it carries no SCORE,
DISPOSITION or HOLD word. The stream lander reads it as a gate: `cfg:land jev`
is `gate` (the default: a gating pass that failed at head skips the PR as
`jev:<passes>`), `require` (a PR with no JEV line at head also skips, as
`no-jev-at-head`) or `off`; `cfg:land jev_passes` names the passes that gate
(empty: all), so a pass whose precision falls is turned off by config. The
same line already last at head is not appended again (`JEV SAME`).

```
JEV RECORDED pr:nova-tools:7 head=b7628a80 gate=ok lint=ok scope=ok base=ok
```

Exit 0 recorded with gate=ok; 1 recorded with gate=fail (`why=` and
`remedy=` on the receipt), or refused (no record, no Redis: `JEV REFUSED`
on stderr); 2 usage, before Redis is touched. A new line (`JEV RECORDED`) is
also a `gate` row in the decision ledger below, at
`jev:row:gate:<repo>#<n>@<head12>` with the gate word as its rules answer;
`land merge` joins `outcome=ok` to the row of every member head it lands
(by `landed`), and prints `JEV REFUSED land-gate pr=#<n> why=... remedy=...`
on stderr when the join fails (the land stands).

#### The decision ledger: jev sync | ask | report | outcome

Every decision the card model makes is one row (nova-tools #4316,
`internal/nsprint/jev`): `jev:row:<type>:<subject>` holds `state` (the exact
text Jev reads), `input_sha`, `rules` (the answer the structure acts on
today), Jev's shadow answer (`jev`, `jev_conf`, `prompt_version`, `tokens`,
`cost`, `ms`) and the `outcome` (`outcome_by`, `outcome_why`,
`outcome_at`). Rows are indexed by `jev:rows:<type>` (scored by the
decision's ms) and every decision, answer and outcome is appended to the
stream `jev:decisions`, the training set. The built-in prompts are
`docs/jev/<type>.<version>.txt`.

| type | subject | decided at | rules | outcome |
|---|---|---|---|---|
| `tier` | primary | push | the declared tier, else `flash` | at merging: the tier of the work or fix copy whose PR read 8+ (that copy's record at its ok) |
| `worktype` | primary | push | - | the card's TYPE line, when it is one of Jev's types |
| `review` | `<primary>@<at>` | a failed copy's move into review (`at` is that move's, the same ms as `review_at`) | REVIEW-JEV `suggest=` | the posted verdict (a read reassign too) |
| `readsane` | read copy | the read's end with a score | a pass with a failing gate, or an under-10 naming no work, is `suspect` | at land or close from merging: `trust` when the read's pass or fail matched the head's fate |
| `gate` | `<repo>#<n>@<head12>` | `jev mech` | `ok` or `fail` | `ok` when the head lands |

`jev sync [--n <moves>] [--redis <addr>]` reads up to `--n` (default 1000)
entries of `ws:log` after `jev:cursor` and writes the rows and outcomes those
moves are, and the new cursor, in one MULTI. Nothing on the copy model's live
path writes a jev key. A review move whose record has already moved on to a
later review makes no row (its fields are not that move's); it is counted and
printed. A cursor older than the log's first entry is a possible gap:

```
JEV SYNC GAP between=<cursor>..<first id> why=ws:log was trimmed past jev:cursor remedy=run jev sync more often than ws:log turns over
JEV SYNC MOVED review <primary>@<at> why=the record moved on to a later review before sync read it remedy=...
JEV SYNC moves=<n> decisions=<d> outcomes=<o> moved=<m> from=<id|-> cursor=<id|->
```

`jev ask [--n <rows, 1-256>] [--key-env <VAR>] [--base-url <url>] [--redis
<addr>]` claims up to `--n` (default 16) rows off `jev:pending` with SMOVE to
`jev:asking`, asks TypeSafe Jev each (one typed call per row, the key from
`--key-env`, default `JEV_API_KEY`), and in one MULTI writes the answers,
returns the rows the provider failed on to `jev:pending` and removes the rest
from `jev:asking`. Those writes run without the caller's cancel, so a
cancelled ask still writes what it paid for and puts back what it claimed;
when even that fails, the refusal names the rows left in `jev:asking`.

```
JEV ASKED <type> <subject> answer=<a> conf=<0.00> version=<v> ms=<ms> cost=<$|->
JEV REFUSED ask <type> <subject> why=<error> remedy=back on jev:pending; the next jev ask asks it again
JEV ASK asked=<n> answered=<a> failed=<f> pending=<p> asking=<k>
```

`jev report [--type <t>] [--version <v>] [--redis <addr>]` prints one line
per type, source (`rules`, `jev`) and prompt version: `answers`,
`outcomes`, `agree`, `agreement` (of outcomes), `open` (no outcome yet),
`overrides` (outcomes a person recorded, not `card`, `merging`, `landed` or
`closed`) and `override_agreement`; a `jev` line adds `cost` (sum) and `ms`
(mean). A ratio with nothing under it prints `-`. `--version` keeps that
prompt version's lines and the same types' rules lines beside them.

```
JEV REPORT rows=<n> pending=<p> lines=<l>
JEV type=tier source=jev version=tier-v1 answers=38 outcomes=30 agree=26 agreement=87% open=8 overrides=0 override_agreement=- cost=$0.001200 ms=640
```

`jev outcome --type <t> --subject <s> --outcome <o> --why <text> [--by
<who>] [--redis <addr>]` joins an outcome by hand (the coordinator's confirm
or override; `--by` defaults to `NOVA_FRIEND`); an outcome with no decision
row is refused.

```
JEV OUTCOME <type> <subject> outcome=<o> by=<who>
JEV REFUSED <verb> why=<why> remedy=<remedy>
```

Exit 0 done, 1 refused (Redis, Jev, no row, a failed ask), 2 usage. A
decision point another stream builds records its row with `jev.Record` and
its outcome with `jev.Join`, one call each.

### spec

The specs table in Redis (#3370, part of #3364, #4400). A SPEC line's facts go
onto the record `pr:<repo>:<n>` in the same call that stores the line
(`spec_rev`, `spec_state`, `spec_stream`, `spec_score:<who>` = `<rev>
<score>`), and the spec is in exactly one of `specs:<stream>:working` or
`specs:<stream>:done` (ZSETs of `<repo>#<n>`, score = the spec's first mark
in ms, so every list reads oldest first). Done is at least quorum distinct `who=`
scoring `>= pass` at the current rev (`cfg:spec pass=9 quorum=2`; quorum falls to
1 when the count of live readers with active heartbeats within 60s, excluding the
spec owner, is <= 1); a newer rev resets the count and a line at an older rev is
refused `STALE_REV` with nothing written. On done the same call releases every task
waiting on `spec:<repo>#<n>` (the DEPENDS-ON release). Subverbs are FCALL of functions
in `internal/nsprint/fn/lua/unblock_spec.lua`.

- `nova-sprint spec mark <repo>#<n> --rev <k> --who <friend> --score <s>
  [--stream <name>] [--sprint <S>] [--redis <addr>]` stores `SPEC
  who=<friend> rev=<k> score=<s>` and its facts. `read post` of a SPEC line
  is the same call, and its comment mirror follows the Redis write.
- `nova-sprint spec list [--stream <name>] [--redis <addr>]` prints the
  specs block from Redis alone; `--stream` adds that stream's ids.
- `nova-sprint spec revise <repo>#<n> --from-pr <m> --note "<what changed>"
  [--no-github] [--redis <addr>]` appends a `REVISION` comment naming the PR and
  the change, bumps the spec's `rev`, and resets the spec to `working` at the new rev.
  If the note is empty, "none", or "unchanged", prints `SPEC unchanged`. (Also
  executed automatically during `task land`, `land stream`, and `land pr` for cards
  whose issue is a spec).

```
SPEC MARK nova-tools#3370 who=emma rev=3 score=10 answer=DONE state=done tens=2 released=1 stream=nova-sprint
specs | working | done
nova-sprint | 4 | 9
SPEC LIST streams=1 working=4 done=9
```

Exit 0 written or already so (RECORDED, NEW_REV, DONE, SAME), 1 refused
(STALE_REV, INVALID; nothing written), 2 usage or could not run.

### ws, scope, stream

The ws index (#3662) is the sprint's work-stream data structure: `ws:names`, `ws:order` (rank), and per stream one ZSET per state, `ws:<stream>:waiting|ready|working|merging|landed|parked`, with `task:<id>` fields `stream` and `state` naming the one set a task is in (`closed` is in none) and every move receipted in the `ws:log` stream. Each verb is one FCALL of an `ns_ws_*` function (library file `internal/nsprint/fn/lua/ws.lua`, Go wrappers in `internal/nsprint/ws`), prints one receipt line ending `ms=<n>` (the list verbs print their rows first), and exits 0 done, 1 refused (`REFUSED <why>`, nothing written), 2 could not run. Every verb takes `--redis <addr>` (default `$NOVA_SPRINT_REDIS`) and `--as <actor>` (default `$USER`); the writing verbs take `--why <text>` for the log.

- `nova-sprint ws counts` prints the one count (the numbers `sprint status` and the table print): `COUNTS sprint=<S> streams=<n> waiting= ready= working= review= merging= landed= parked= total= done=<landed>/<total> pct= left= eta=`; `nova-sprint ws checkpoint --out <path>` writes every stream's six sets with the task fields as TSV and records the receipt in `ws:checkpoint`.
- `nova-sprint sprint status [--sprint <S>]` prints the one count as `<S> <status> <landed>/<total> done <z>%, left <l>, eta <HH:MM> ET` (`eta -` with no card) for the open sprint only: the ws index holds one sprint's streams, so `--sprint` naming another is refused, exit 1, `REFUSED sprint status --sprint <S>: not the open sprint; open=<open|-> remedy="nova-sprint sprint status"`. `ws show --order` prints each stream's `cards= live= landed=` from the same count. The wide `table --once` prints every open sprint's pipeline row as `pipeline <S> REFUSED pipeline reads a retired key family; remedy="nova-sprint ws counts"`, and `census --sprint <S>` prints `REFUSED census reads a retired key family; remedy="nova-sprint ws counts"` (exit 1, no Redis read), whatever that family (`s:<S>:idx:card:*`, `s:<S>:pool`, `s:<S>:waiting`, `sprint:<S>:cards`) holds: it is not a count of the sprint's work, and card push still writes it, so a number there would disagree with the one count (#4411). `card fsck` labels its counts of that family `family=retired`.
- `nova-sprint ws show --order [--stream <s>]` (also `stream order --show`; #4318) prints every stream's cards in order, one line each, `<where> <id> <- <edges>` (the card's DEPENDS-ON entries; one not landed carries its set in parentheses, one with no record `(no record)`), the stream's sentinel last, then `SHOW streams=<n> cards=<n> edges=<n>`.
- The stream sentinel (#4318): every stream has one sentinel card, `<slug>:sentinel` (the stream name lower-cased, runs of other characters one `-`; two names with one slug are refused, `SLUG ...`), created in the stream's waiting set when the stream is registered (its first push, `stream order`, a rename, a migrate; `task fsck` names a registered stream without one, or whose stop sits in another stream, as `NOSENTINEL`, and `task fsck --repair` creates it or moves it home). It is the stream's stop, and its graph is structure in the one move: waiting -> landed when the stream's last live card lands (at that sha, in the same call) or by `task land --id <slug>:sentinel --sha <merge sha>` once no other card is live (refused by name until then; the waiting resolver prints `SENTINEL ... ready-to-land` with the remedy when the last card was cancelled instead); waiting <-> parked with its stream (`scope park` and `unpark` count cards, the stop moves uncounted); done only by a rename (a rename to the same slug keeps the stop; a renamed landed stream's old stop ends done/ok and the new name's lands at the same sha); never dealt, never ready, working, review or merging, never done by task done, cancel or sprint clear, never owned, never moved to another stream or to none, and a move that stays in place carries no fields. It is counted nowhere: the one count (`sprint status`, the table, `ws counts`, `ws show`, `stream ls`, `scope ls`, the progress duty's `PROGRESS left=`) counts cards without it, so a stream holding only its sentinel counts 0 (#4411). A stream that must wait for another whole stream puts `DEPENDS-ON <slug>:sentinel` on its first card; the resolver treats it like any card edge, met by the sentinel's landing alone, and there is no second kind of dependency (`stream/<slug>`, the older spelling, is read as `<slug>:sentinel`).
- `nova-sprint scope keep --streams "<a>|<b>"` parks every stream not named (waiting and ready move to parked; every set is scored by the task's `created_at` ms, so a list reads oldest first and a move never changes the score); `scope park --stream <s> [--ids @file]` parks one stream, or only the listed ids of it; `scope unpark --stream <s>` returns each parked task to the set it came from; `scope ls` prints each stream as kept, parked or partial. `scope keep` and `scope park` write a checkpoint first, to `--checkpoint <path>` or a new file in `$NOVA_SPRINT_CHECKPOINT_DIR` (default `nova-sprint/ws` under the user cache directory, newest 32 kept).
- `nova-sprint stream ls --tree` (nova-tools#4317) adds every plan of a stream under its line, collapsed: `plan <id> <derived state> children=<n> waiting=.. ready=.. working=.. review=.. merging=.. landed=.. done=.. parked=.. stitch=<id>:<where>`, the children's counts folded in and no new column; `--tree --expand` lists each child (`child <id> <where> pr=<repo#n> score=<n>`) and the stitch under the parent; the receipt is `STREAMS n=<n> plans=<k>`.
- Stream paths (nova-tools#4322): no path belongs to two open streams. `ws:paths` (HASH, field `<stream>`) holds the union of the stream's live cards' PATHS (each record's `stream_paths`, the PATHS line as `ws.SplitPaths` reads it), `""` when they name none; a registered stream holding a live card with no field is unbuilt. The gate is Lua (SP.gate in 02_card_move.lua), in the same FCALL as the write and before it: ns_card_push (card push, card cut), ns_tcard_push (task push, quack cut, card cut --from and --parent), task move into another stream (`task move --to-stream`, `ws move`) and `scope unpark` (refused whole). It refuses a card whose PATHS overlap another open stream's (equal, or one a prefix of the other at a `/`): `REFUSED PATHS overlap stream=<s> paths=<a,b> remedy="--join <s>"` (a move's remedy is `nova-sprint scope park --stream <s>`; an unpark's line adds `unpark=<stream>`); a card overlapping two or more streams names every one: `REFUSED PATHS overlap stream=<s1> also=<s2> paths=<...> remedy="nova-sprint scope park --stream <s2>"`; any gated write while a stream is unbuilt: `REFUSED PATHS unbuilt stream=<s> remedy="nova-sprint ws check --repair"`; and `--join <s>` (card push, card cut, task push) naming a stream with no live card: `REFUSED PATHS notopen stream=<s> remedy="nova-sprint stream ls"`. `--join <s>` pushes a card that overlaps open stream `<s>` onto it instead. Cards of one stream may share paths. A move never carries `stream_paths` (only the push and the repair write it) nor `paths` (fixed at the push; `card end --paths` records the paths the work touched as `result_paths`, which a copy's brief carries when the primary has no PATHS, and never changes the card's PATHS or its stream's): `REFUSED FIELD stream_paths ...`, `REFUSED FIELD paths ...`. An adoption of a record that predates the where field (task and card), the reap's relink of a stray's views and `card fsck --repair`'s relink of a stream view are gated the same way. `card cut --from` checks every row the same way before it files any issue, and its `--dry-run` with `--redis` reports the refusals; without a store it prints `CARD CUT DRY PATHS unchecked rows=<n> why=... remedy="pass --redis <addr>"`. A rerun whose row's PATHS differ from its pushed card's is refused: `why="CONFLICT task:<id> paths=<old> new=<new>: ..."`. `stream ls` prints each stream's `paths=<n>`. `nova-sprint ws check [--repair]` recomputes every stream's paths from its live records (each record's `stream_paths`, else its PATHS), prints `PATHS OVERLAP stream=<a> other=<b> paths=<...>` for two open streams sharing a path and `PATHS STALE stream=<s> record=<n> live=<m>` for a record that differs or is unbuilt; `--repair` is one FCALL (`ns_ws_paths_repair`): each live record with PATHS and no `stream_paths` is backfilled, even when its paths overlap another stream's (both streams then hold the path, a push into it is refused naming both, and the pair's `PATHS OVERLAP` line is printed, exit 1, until one side is parked, cancelled or lands), and every stream's field is written from the live sets as they are in that call; only a record whose PATHS changed since the read keeps none and leaves its stream unbuilt: `REPAIR REFUSED PATHS unread id=<id> in=<s> remedy="nova-sprint ws check --repair"`. With `--repair` the lines report the store as the repair left it. Then `CHECK streams=<n> cards=<n> overlaps=<n> stale=<n> repaired=<n> records=<n> unbuilt=<n> refused=<n>`; exit 1 on an overlap, a repair refusal or a stale record. Deploy: the store fails closed. After `fn deploy` loads the library that carries the gate (the `fn` step of `fleet release`), every gated write is refused as unbuilt until `nova-sprint ws check --repair` has run once on that store, so run it right after the `fn` step.
- `nova-sprint stream ls` prints each stream's rank and six counts; `stream order <a> <b> ...` ranks the named streams first; `stream rename <old> <new>` renames the sets and every member's `stream` field.
- The stream branch lifecycle (#3358), each step one path through the land verbs: `stream open --repo <owner/repo> --stream <s>` is `land stream` refused when the landing is already open (it cuts `stream/<slug>` off the base tip and records `base` and `base_sha` on `land:<repo>:<slug>`); `stream rebase` is the same run refused unless the landing is open (rebuilds on the base head, re-runs the batch test, reuses the PR, records the new `base_sha`); `stream pr` is `land stream` with no guard; `stream status --repo` is `land status --repo`; `stream close` is `land merge`, the one event that moves every member merging -> landed and closes the members.
- Merging -> landed as a duty (#4324), the alarms in the structure: `land stream --dry-run` prints the plan first (`PLAN repo= stream= base= branch= pr=one members= left_out= order=ws-score`, then one `ORDER` line per member in the work order the `ws:<stream>:merging` score gives, #4342); a run whose PR would carry fewer members than the stream has in merging is refused `LAND-SERIAL stream=<s> carrying=<n> merging=<m> left_out=#<pr>:<why>,...` (never one at a time), before the build (unread, held, no PR) and after it (a conflict parked, a red bisected out, a moved head: the parks still happen, the landing record is `state=serial`, nothing is pushed); `--partial` on `land stream` and `land` (and `cfg:land partial 1` for the land duty) lands without them with the same line printed `allowed=partial` and kept on the landing record (`land status` prints `serial= partial_by= partial_at=`); every landing step prints one line with its wall (`REBASED #<n> at <head> onto <branch> ms=`, `PUSHED`, `PR #<n> opened`, `BUILT`, `CI <head> <green|red|pending>`, `MERGED <sha>`, `LANDED n=<members> total_ms=`). The reconciler's land watch stamps each merging member's first sight on `land:merging:<stream>` (the task's own `merging_at` wins when the move writes it), prints `LAND-SLOW <stream> oldest=<id> age=<d> max=<d>` past `cfg:land slow` (seconds, default 600) when the word or the oldest member changes, with one wake note per episode to `friend:outbox` (the coordinator's bus channel and `cfg:land notify`, default `bus:To:glenn`), `LAND-WALL` with `land:slow:<stream> stalled=1` and a second note past `cfg:land wall` (default 1800; the progress duty counts the stream as stalled; the table's line is #4387), cuts one merge card per stream with merging members (`MERGE-CARD <stream> card=merge-<slug>-<n> to=<frontier friend|coordinator>`, kind merge, its brief the members in order plus the MERGE-NOTEs, its child running `land --card <id>`) and, when that card ends `BLOCKED cross-stream paths=<files>`, one escalation to the coordinator with the #4318 sentinel edge (`LAND-CROSS <stream> card= escalation=cross-<slug>-<n> to= after=<slug>:sentinel,...`: the other streams whose live cards' PATHS hold a named file; until each sentinel lands only the escalation card may land the stream), or, when it closes any other way with the same members still in merging, one escalation instead of a new card (`LAND-STUCK`). One writer per stream: `land:merge:<stream> owner` is one atomic claim taken before any build, push or merge, by the watch for a card (`card:<id>`, live while the card is open), by the land duty for its pass (`duty:<repo>:<token>`, live while its lease holds the token; `LAND-DUTY ... state=merge-card card=<id>` or `state=owned owner=<o>` when another holds it), and by `land`, `land stream` and `land merge` (`--card <id>` as that card's child, else a hand claim renewed while the run lasts); another writer's live claim refuses the run `REFUSED LAND-OWNER stream=<s> owner=<o>`. A build refused `LAND-SERIAL` after the build keeps the open stream PR's number on the `state=serial` record and names the parked members (`serial_left`); the next run is refused before any build while one of them is live outside merging (`left_out=#<n>:not-back:<where>`), unless `--partial`. `nova-sprint note post --stream <s>|--sprint <S> --by <who> <text>` writes a `MERGE-NOTE` line that every copy's card carries from then on (`MERGE-NOTES` block; `note ls`, `note drop`); a stream's notes expire when its landing merges (`LAND MERGE ... notes_dropped=<n> ...`).

### lesson

Every rendered build, fix, and read brief tells the card to read the repository's
`docs/LESSONS.md` when present. It is reviewed data subordinate to the live
brief and repository rules. The file is capped at 40 physical lines so a card
can consume the whole active view. A read proposes the concrete failure and
the action that would have prevented it; after the repository owner reviews
the evidence, append the structured one-line row:

```sh
nova-sprint lesson append \
  --repo ./nova-tools \
  --id s9-001 \
  --component brief \
  --kind read \
  --failure "card skipped repository lessons" \
  --prevention "read the capped lessons file before review" \
  --evidence "mas-bandwidth/nova-tools#2498" \
  --status active \
  --reviewed-by stella
```

The append verb never guesses a checkout or creates the active lessons file. Every field is
required and must fit on one line without a Markdown table pipe. Lesson IDs
are stable: an identical retry prints `LESSON UNCHANGED`; different content
under an existing ID refuses. The append is published by atomic rename and
refuses the 41st line. A holder-lifetime kernel lock (flock on Unix) on
`nova-lessons.lock` in the checkout's git directory serializes the whole
read/check/rename transaction, so concurrent successful appends cannot lose
one another. The lock is never broken on age: a waiter queues behind a live
holder for up to 30 seconds and then refuses as busy, and the kernel alone
releases a holder that died. Append accepts `--status active`; retire a row with:

```sh
nova-sprint lesson supersede --repo ./nova-tools --id s9-001
```

Supersede first publishes the same row with status `superseded` to
`docs/LESSONS-ARCHIVE.md`, then removes it from the capped active view. It
creates the archive when needed; cards never load it. If interrupted between
those writes, retry recognizes the archived row and finishes the removal.
Archived IDs remain reserved, and an identical supersede retry is unchanged.

### pitstop

`nova-sprint pitstop set|clear|status --sprint <S> [--scope all|<stream>]... [--why <text>] [--by <who>] [--force] [--redis <addr>]`

The sprint's pit stop is one Redis hash, `s:<S>:pitstop` {by, why, at}, never a bus note (#3371). While it exists the deal pass plans nothing from the sprint and `ns_card_deal` refuses its cards; any other reader (the feed, the table) reads the same key through `pitstop.Read`. `set` refuses to overwrite a stop without `--force` and refuses a sprint with no `s:<S>` status; `clear` refuses when none is set; `status` prints one line. `--by` defaults to `NOVA_FRIEND`. Set and clear are one FCALL each (`ns_pitstop_set`, `ns_pitstop_clear`) and write one receipt to `s:<S>:log`. Exit 0 done, 1 refused with the remedy named, 2 usage.

`--scope` (repeatable) names streams. `set` with none (or `--scope all`) stops every stream; `set --scope <stream>...` stops only those. `clear --scope <stream>...` narrows the stop by exactly those streams: an all-scope stop lifts them (`lifted:<stream>` fields) and keeps every other stream stopped, a named-scope stop drops them and lifts itself whole when the last one goes; a stream the stop does not hold refuses the clear with nothing written. `pitstop.Stop.InScope(stream)` in Go and `NS.pitstop.in_scope(S, stream)` in the function library answer whether a stream is stopped. The deal pass still stops the whole sprint while any stop exists.

```text
nova-sprint pitstop set --sprint nova-sprint-0924 --by rowan --why "Glenn 8:00 PM: rest tonight"
# prints
PITSTOP SET sprint=nova-sprint-0924 by=rowan at=1790000000000 scope=all why="Glenn 8:00 PM: rest tonight"
nova-sprint pitstop clear --sprint nova-sprint-0924 --by rowan --scope nova-work
# prints
PITSTOP NARROW sprint=nova-sprint-0924 by=rowan at=1790000060000 lifted="nova-work" was_by=rowan was_at=1790000000000 was_why="Glenn 8:00 PM: rest tonight"
```

### reconcile: the progress duty

**The pass's duties (2026-09-27).** `nova-sprint reconcile` runs the copy
model only: `fleet` (bench UP/PROBING/DOWN), `dev-red`, `fleet-deploy`,
`land`, `land-watch`, `progress`, `route`, `card-deal`, `task-lease` and
`waiting-resolve`, in that order, each one round trip or two (the DUTY line
prints `trips=`; a functional test pins each duty's budget). The
sprint-store model's duties, the refill's deal pass over `s:<S>:pool` with
its ssh launches, `ok-to-friend`, `pr-to-read` with hold-to-fix,
`done-already`, the expire sweep and the old card fsck, are retired from the
pass (Glenn: "Go for retiring", after the round-trip measurement); their
verbs stay (`consume`, `card fsck`) for a sprint that still uses that model.

`nova-sprint reconcile` runs the progress duty (nova-tools #4319; internal/nsprint/reconcile/progress.go) with its other duties: it measures whether each stream of `ws:order` is converging, and when one is not it stops that stream and asks for help. It reads `cfg:progress` (a hash; a missing or non-positive field keeps its default): `window_s` (1800, the stall window), `every_s` (10, the cadence), `refusals` (20, passes a duty error may repeat unchanged) and `ask` (`glenn,rowan`, who the wake note goes to). The `every_s` gate comes before any read: a gated pass costs no round trip and a run three (the index, the measurement, the record); a changed `every_s` applies from the next run.

Per stream, `left` is waiting + ready + working + review + merging; the stream is **in play** when no pit stop holds it and a card is working or a ready card has a consumer with room (live beat, not down, not paused, a free slot). It is `converging` while in play inside the window since it last fell, `idle` when not in play, and `stalled` when in play for the whole window with no fall. A card that churns working -> ready -> working never falls, so it stalls the stream. Each run prints one line per stream whose numbers changed:

```text
PROGRESS autonomy left=1 delta=0 ready=1 landed_h=0 retries_h=0 oldest=1h0m0s blocked=30m0s window=30m0s status=stalled
```

`landed_h` and `retries_h` are the last hour of `ws:log` (moves to landed; working back to ready or waiting), `oldest` the age of the oldest card not landed, `blocked` how long the stream has been in play with no fall.

The one ask path: a stalled stream, a duty error repeating unchanged past `refusals` passes, or a release probe failing twice. The duty sets the open sprint's pit stop `by=progress` with the diagnosis as its why (scoped to the stream for a stall, `scope=all` otherwise), prints one `EVENT` line, and writes one `friend:outbox` note (`kind=notice`, `actor=progress`) per `ask` name. An episode asks once: a stall again only after the stream fell or was lifted and stalled anew, a refusal once per text.

```text
EVENT PROGRESS STALLED autonomy stall blocked=30m0s window=30m0s left=1 ready=1 landed_h=0 retries_h=0 sprint=sprint-4319 at=1790000000000
PROGRESS REFUSED pitstop set sprint=sprint-4319 why="already stopped by=rowan why=\"looking\""
```

Every refusal of its own (no open sprint, a stop already set, a wake that did not write) is one `PROGRESS REFUSED` line and counts as `refused=` on the pass's `DUTY` line, as every duty's refusals do (the waiting-resolve duty counts the moves `ns_ws_move_many` refused). State: `proc:progress:<stream>` {left, ref_left, ref_at, fell_at, blocked_since, asked_at, status, at}, so a restarted reconciler continues the window; `proc:progress` {events, event, event_at, refused, asked:<duty>, at}, where `refused` is the other duties' refusals of the last pass plus this run's own, each counted once. `nova-sprint table` prints one line from `proc:progress`, only when a count is non-zero:

```text
EVENTS n=1 refused=0 last=EVENT PROGRESS STALLED autonomy stall blocked=30m0s window=30m0s left=1 ready=1 landed_h=0 retries_h=0 sprint=sprint-4319 at=1790000000000
```

The stop is cleared like any other: `nova-sprint pitstop clear --sprint <S> --scope <stream> --by <who>` once the cause is fixed (docs/PIT-STOP.md, **The system's stop**).

### quack cut, quack run

`nova-sprint quack cut --n <N> --repo <owner/name> --stream <s> --sprint <S> [--tiers flash,pro] [--base dev] [--base-sha <sha40>] [--ref <owner/name#n>] [--actor <a>] [--redis <addr>]`
`nova-sprint quack run --sprint <S> [--slots <bench>=<n>,...] [--actor <a>] [--redis <addr>]`

A quack run is N one-file probe cards in one stream, each a primary the copy model fans out to the benches (#4307; the morning of 2026-09-26 pushed a hundred of them by hand from a template, with the pit stop set and lifted by hand and the base sha read by hand). `quack cut` does that as one verb: it sets the sprint's pit stop (why: `quack cut: cutting N quack cards into <s>`), pushes `quack-001`..`quack-NNN` into `ws:<s>:waiting` through the one task push (`ns_tcard_push`, one call per card, never a child process), and prints one `CUT` line. Each card is the template rendered for its id: `REPO` is `--repo` (the quack repository, `mas-bandwidth/quack`, whose card creates `docs/fixtures/quack-<S>-<id>.txt` holding the one line `quack <S> <id>`), `ROUTE` round-robins over `--tiers` (card 1 the first tier, card 2 the second, ...), `BASE` is `--base` at `--base-sha`, else the tip of that branch in this host's mirror (`~/nova-bench/mirror/<name>.git`); with neither the cut is refused naming the remedy before anything is written. An id that already exists is `SKIPPED id=<id> why=exists` and the cut goes on; the `CUT` line counts `pushed`, `skipped` and `refused`. The stop stays set (`pitstop=set`, or `held` when one was already there) and the line names what lifts it. `--actor` defaults to `NOVA_FRIEND`. Every card is rendered before Redis is touched, and a sprint nobody opened is refused (`CUT REFUSED ... why=sprint-unknown`). Exit 0 cut, 1 refused or a card refused, 2 usage.

`quack run` starts the run: each `--slots <bench>=<n>` goes through the capacity path (`capacity.SetBenchWith` on the bench's recorded machine; a bench with no machine is `SLOTS REFUSED ... why=no-machine` naming the capacity verb), then the pit stop is lifted (`PITSTOP CLEAR`, or `PITSTOP NONE` when none was set), one receipt line each and one `QUACK RUN` line. Exit 0, 1 when a bench was refused, 2 usage.

```text
nova-sprint quack cut --n 100 --repo mas-bandwidth/quack --stream quack --sprint quack-0926 --tiers flash,pro --ref mas-bandwidth/nova-tools#4232 --actor rowan
# prints
CUT n=100 stream=quack sprint=quack-0926 repo=mas-bandwidth/quack tiers=flash,pro pushed=100 skipped=0 refused=0 base-sha=5f2e1c9a7b3d pitstop=set lift="nova-sprint quack run --sprint quack-0926" ms=412
nova-sprint quack run --sprint quack-0926 --slots hetzner=8,hulk=16 --actor rowan
# prints
SLOTS SET bench=hetzner machine=hetzner slots=8 desired=8/64
SLOTS SET bench=hulk machine=hulk slots=16 desired=16/64
PITSTOP CLEAR sprint=quack-0926 by=rowan at=1790000060000 was_by=rowan was_why="quack cut: cutting 100 quack cards into quack"
QUACK RUN sprint=quack-0926 benches=2 refused=0 pitstop=lifted ms=9
```

### land pr

`nova-sprint land pr <n> [--repo owner/name] [--redis <addr>] [--api <url>]`

One pull request to its merge commit in one pass (#4311; the scratch script that ran about twenty times on 2026-09-26, as a verb). It reads the PR by REST (one call), then reads its head's check state from Redis, `ci:<repo>:<head>:gh`, which the webhook ingest writes from GitHub's `check_run` and `workflow_run` deliveries (internal/nsprint/webhook); it never reads the check-runs or workflow-runs endpoints and never calls GraphQL (nova-sprint is REST only, and GitHub is events only). It prints `PR <n> CHECKS <word> <pass>/<total> head=<sha8>`. Green: it merges the PR by REST at exactly that head (GitHub refuses when the head moved), skipping the merge queue whose run re-proves the same tree (Glenn 2026-09-26), and prints `MERGED <sha>`. Red: `FAILED <first red run>` (`kind:name`). Pending or nothing recorded yet: `WAITING` and it returns at once; there is no loop and no sleep, so run it again once the webhook has written green. A merged PR is `MERGED <sha>`, a closed one `FAILED closed without a merge`, and `mergeable_state=dirty` is `FAILED conflict`. A final `LAND PR` line carries the state, head, check word, merge sha and REST calls made (at most two; the budget is three). The token is the seat's when its seats.tsv row names one (#4330), else the environment's (`GH_TOKEN`, then `GITHUB_TOKEN`, as the lander reads it). No token is the typed refusal `REFUSED no GitHub token remedy=...`. `--repo` defaults to `mas-bandwidth/nova-tools`; `--redis` defaults to `NOVA_REDIS_ADDR`. Exit 0 merged, 1 failed, closed or in conflict, 2 usage or refused, 3 waiting, 6 no Redis.

```text
nova-sprint land pr 4304
# prints
PR 4304 CHECKS green 6/6 head=9c41d7e2
PR 4304 MERGED 635eaca1c7b0e4f2a9d8c6b5a4e3f2d1c0b9a8f7
PR 4304 RECORD pr:nova-tools:4304 outcome=same head=9c41d7e2 prev=- state=merged stream=github task=gh-client
PR 4304 CARD gh-client merging->landed
LAND PR repo=mas-bandwidth/nova-tools pr=#4304 state=merged head=9c41d7e2 ci=green merge=635eaca1 failed=- record=merged card=gh-client card_move=merging->landed rest_calls=2
```

### adopt

`nova-sprint adopt receipt --verb <verb> --pov <coordinator|bench|reader|friend> --state <state> [--gap <repo>#<n>] [--hand <text>] [--note <text>] [--as <who>] [--redis <addr>]`
`nova-sprint adopt matrix [--md | --tsv] [--redis <addr>]`
`nova-sprint adopt status [--redis <addr>]`

Adoption receipts per verb per point of view live in Redis, not in a hand-kept table (#3186, the matrix slice). A verb's record is one hash, `adopt:<verb>` {who, at, pov, state, gap, hand, note, receipts, and one `pov:<pov>` field per POV that wrote}, indexed by age in the ZSET `adopt:verbs`; every accepted receipt is also appended to `adopt:<verb>:receipts`. `--state` is one of adopted, adopted-gaps, in-flight, unexercised, blocked, hack; adopted-gaps, blocked and hack must name the issue holding the gap (`--gap`, or a gap already on the record), else `ADOPT REFUSED reason=no-gap` and nothing is written. The same seat, POV and body again prints `ADOPT UNCHANGED` and writes nothing. `--as` defaults to `NOVA_FRIEND`. `matrix` prints one `ADOPT ROW` per verb, oldest first, and `ADOPT MATRIX verbs=<n> adopted <x>/<y> <z>%`; `--md` prints the hacks-to-verbs table (hand step, verb, state, who, when in ET, pov with the POVs that hold a receipt, gap). `status` prints the x/y line: x the verbs whose latest receipt is adopted or adopted-gaps, y every verb with a receipt, `adopted 0/0 -` when there are none. receipt is one FCALL (`ns_adopt_receipt`), matrix and status one FCALL_RO (`ns_adopt_matrix`); each first loads the library when the store has none. Exit 0 done, 1 refused with the remedy named, 2 usage.

```text
nova-sprint adopt receipt --as rowan --verb "nova-sprint land stream" --pov coordinator --state in-flight --gap nova-tools#3975 --hand "stream branch built by hand"
# prints
ADOPT RECEIPT verb="nova-sprint land stream" who=rowan pov=coordinator state=in-flight gap=nova-tools#3975 at=1790000000000 receipts=1
```

### cost import

`nova-sprint cost import --provider <anthropic|openrouter|oc> --file <export.csv> --redis <addr>`
imports one provider usage export (#3159). It runs from any seat: no home path,
no `--as`, no friend name; the file is the only input, with zero REST calls
and zero model tokens. Redis auth comes from the environment, as for every
nova-sprint verb.

The CSV's header is matched case-insensitively by alias: day (`date`, `day`,
`usage_date`, `created_at`; the first ten characters, a UTC `YYYY-MM-DD`),
cost (`cost_usd`, `usd`, `cost`, `total_cost`; dollars, >= 0), model
(`model`, `model_name`, `model_permaslug`) and the optional project
(`workspace`, `workspace_name`, `api_key_name`, `key_name`, `project`; `-`
when absent). A row whose day cell is `total` is the export's own total,
allowed only in a single-day file. Each row goes to the field
`<project>|<route>`: the routes.yaml route with `via: openrouter` (for
`openrouter`) or `via: opencode` (for `oc`) and the same model, else
`model:<model>` (every `anthropic` row), counted in `unrouted_rows`.

Each day is reconciled in integer micro-dollars before anything is written:
the fields sum to the day's rows and a `total` row equals them, within $0.01.
The day is written whole as the hash `cost:<provider>:<day>` (the fields,
`total`, `rows`, `unrouted_rows`, `source_sha256`, `source_name`, `writer`,
`at` in epoch ms UTC) with member `<provider>:<day>` in the zset `cost:idx`,
score `YYYYMMDD`; neither key has a TTL. A clean import is two round trips: one
pipeline reads what is stored, one MULTI/EXEC writes each changed day (DEL,
HSET, ZADD). A day is `same` (not written, `at` kept) only when its stored hash
without `at` and its index score both match; the same file with either half
missing is `repaired`; a different file is `replaced` whole.

```text
COST IMPORT provider=<p> day=<d> rows=<n> total=<usd> fields=<n> unrouted_rows=<n> source=<sha8> state=new|same|replaced|repaired[ recovered=1]
COST IMPORT DONE provider=<p> days=<n> written=<n> same=<n> repaired=<n> recovered=<n>
```

EXEC is not a rollback, so after a command error or a lost EXEC reply the verb
reads each day back, retries the days not written once, and reads back again
(at most five round trips). A Redis error reply in a read-back is a reply:
the day is `partial`. Only a read-back with no reply makes a day `unknown`.

| exit | meaning |
|---|---|
| 0 | imported (every day `same` included); a day recovered by the read-back adds `recovered=1` |
| 2 | could not run: a flag, an unknown provider, no `--file` or `--redis` |
| 3 | bad export: unreadable, no header, a required column missing (named), a day or cost that does not parse, a negative cost, a `\|` in a project or model, a `total` row in a multi-day file |
| 4 | does not reconcile; nothing written |
| 6 | Redis failed before the write (dial, AUTH, the read pipeline, `cost:idx` not a zset, EXECABORT); `nothing written`, proven |
| 7 | written in part: every reply received, and after one retry a day is not written; days print `state=written\|unchanged\|partial` and stderr names each failing command and its reply |
| 8 | outcome unknown: a read-back got no reply; each such day prints `state=unknown`; re-run the same import (it is idempotent) |

Exit codes are scoped per verb: `task push`'s DOWN 7 (#2929) does not alter this
verb's 7. Exits 7 and 8 never print `nothing written`.

### Where a card's results live

`nova-sprint card end --results <dir>` writes `<dir>` once into the card hash
`s:<S>:card:<label>` field `results` (single writer `ns_card_end`, stamped
`ended_at` from Redis TIME). The dir is always a Unix absolute path on the
bench: a leading `/`, not `//` (a network share), no backslash, no `..`
segment; a drive root (`C:\x`, `C:/x`) or a scheme is refused. `card end`
exits 1 (USAGE) on anything else before it opens Redis, `ns_card_end` applies
the same rule (so nothing is written).

### `--seat` and `nova-sprint redis-cli`

`nova-sprint`, `nova-card`, `nova-swarm` and `nova-wake` take `--seat <name>`
anywhere before a `--` (or `NOVA_SEAT=<name>` when no flag names one) and log
in to Redis as that seat with no wrapper around them (nova-tools #4052). The
seat's file is read in the tool's own process through `internal/seatcred`, on
the library `nova-secrets exec` runs on, with every check exec makes: the store
is `~/nova-bench/secrets` (`NOVA_SECRETS_STORE` overrides it), the key
`~/.config/nova-secrets/<seat>.key` (`NOVA_SECRETS_KEY`), and `sops` the one on
`PATH` (`NOVA_SECRETS_SOPS`). The Redis user is the first of `coordinator` and
`bench` whose password (`NOVA_REDIS_COORDINATOR_PASSWORD`,
`NOVA_REDIS_BENCH_PASSWORD`) the seat's file holds, or `NOVA_SPRINT_REDIS_USER`
when set. The password goes to the Redis client in memory: it is never printed,
logged, put on an argument list or set in the tool's own environment, so no
child the tool starts inherits it. Without a seat each tool authenticates as
before, from its environment.

`nova-sprint redis-cli [--seat <name>] [--redis <host:port>] -- <cmd...>` runs
one `redis-cli` command under the seat's login for the rare hand read:
`redis-cli -h <host> -p <port> --user <user> --no-auth-warning <cmd...>`, with
the password as `REDISCLI_AUTH` in that child's environment only. `--redis`
defaults to `NOVA_REDIS_ADDR`, else the seat row's address. stdout is redis-cli's; the receipt is one line on
stderr, `REDIS-CLI seat=<s> user=<u> key=<k> addr=<a> cmd=<c> exit=<n>`. Exit 0
the command ran, 1 redis-cli failed, 2 refused (no seat, no address, no command
after `--`, or a seat that cannot be read, named with its remedy).

```
nova-sprint table --seat studio --redis 100.115.99.19:6380 --once
nova-sprint redis-cli --seat studio --redis 100.115.99.19:6380 -- ZCARD sprint:S:cards
```

### Seat profiles: `nova-sprint --seat coordinator <verb>` and `nova-sprint redis`

The seat is a fact of the machine, not of the shell (nova-tools #4330). The
fleet play writes `$XDG_CONFIG_HOME/nova-sprint/seats.tsv` (else
`~/.config/nova-sprint/seats.tsv`), one tab-separated row per seat: name, redis
addr, redis user, secret env, store, key, and an optional seventh column, the
GitHub token env. Blank lines and `#` lines are skipped; a leading `~/` in store
or key is `$HOME`. The key's file name names the seat's file in the store
(`studio.key` opens `<store>/studio.yaml`).

```
coordinator	100.115.99.19:6380	coordinator	NOVA_REDIS_COORDINATOR_PASSWORD	~/nova-bench/secrets	~/.config/nova-secrets/studio.key	GH_GATE_TOKEN
```

`nova-sprint --seat coordinator <verb>` (or `NOVA_SPRINT_SEAT=coordinator`,
which wins over `NOVA_SEAT`) reads the row: the verb logs in as the row's user
with the password the seat's file holds under the row's secret env, read in
process through `internal/seatcred` as above (never printed, never in the
environment). The row's address is every verb's `--redis` default: a verb
given no `--redis` dials it (`nova-sprint --seat coordinator table --once`,
`census`, `digest`, `fn load` included), after the verb's own environment
default (`NOVA_SPRINT_REDIS`, `NOVA_REDIS_ADDR`, `NOVA_REDIS`, which the row
also sets); a `--redis` on the line still wins. The class test
`internal/ci/seatredis_class_test.go` holds every `--redis` flag in
cmd/nova-sprint and internal/nsprint to that default. A seat with no row is the
#4052 seat above; when that does not open either, the refusal names `seats.tsv`
and the row it wants. A malformed row is refused before any verb runs, as
`seats.tsv:<line>`, exit 2.

The seventh column names the key of the seat's file that holds its GitHub
token. The GitHub verbs (`land`, `ci compare`, `read post`, `file`, `pr reap`,
`card cut-from`) read the token from the seat's file in process, so no session
exports `GH_TOKEN`; a file without that key is refused naming the file and the
`nova-secrets seal` remedy. A six-column row (or a seat with no row) keeps the
old behaviour, `GH_TOKEN` then `GITHUB_TOKEN` from the environment, and says so
once per process on stderr:
`nova-sprint: seat <s>: its seats.tsv row names no GitHub token env (the seventh column), so GitHub verbs read GH_TOKEN from the session as before`.

`nova-sprint [--seat <name>] redis [--redis <host:port>] [--] <cmd...>` sends
one raw command over the same dial (no redis-cli, the password never leaves the
process) and prints the reply as redis-cli does to a pipe: one line per value,
arrays and maps flattened (map keys sorted), nil an empty line. The receipt is
one line on stderr, `REDIS seat=<s> user=<u> key=<k> addr=<a> cmd=<c> exit=0`;
a Redis refusal (NOPERM, WRONGTYPE, NOAUTH, unreachable) prints `REDIS REFUSED
... why=<error>` there and exits 1; 2 is could not run (no command, no
address, a seat that cannot be read). The address is the row's, else
`NOVA_SPRINT_REDIS`, then `NOVA_REDIS_ADDR`, then `NOVA_REDIS`.

```
nova-sprint --seat coordinator redis ZCARD sprint:S:cards
NOVA_SPRINT_SEAT=coordinator nova-sprint sprint status
```

### `nova-sprint doctor`

`nova-sprint doctor [--seat <name>] [--redis <addr>] [--bench <name>]` (nova-tools #4352 item L) is the five hand checks of 2026-09-26 (`fn check`, `version`, `fn deploy`, `pitstop status`, a `ps`) as one verb. It prints one line per check, in this order, each `OK` or `FIX` with `why="<prose>"` and `remedy="<one command to paste>"` (never prose, alternatives or a second step; an ansible fix is `make -C ~/rowan-working/rowan-tools/fleet <target>`), or `SKIP needs=<check>` when the check it needs is not OK (a SKIP is not a fix):

- `seat`: `--seat`, else `NOVA_SPRINT_SEAT`, then `NOVA_SEAT`, resolved in this process (its `seats.tsv` row when it has one) (the key, the store file, the Redis password); with no seat, the environment's login, or the default user when the store lets it in. A seat that does not resolve names its `nova-secrets seal` or `nova-secrets check` line; no seat on a store that wants one names `export NOVA_SPRINT_SEAT=<seat>` for a seat whose key is in `~/.config/nova-secrets`.
- `redis`: `PING` as that login (`--redis`, else the seat row's address, `NOVA_SPRINT_REDIS`, then `NOVA_REDIS_ADDR`); one dial bounded by a second and no retries.
- `fn`: the loaded `nova_sprint` library against the one this binary embeds, fn check's own verdict (`fn.Judge`), then `ns_ping` only when the code is ours. The remedy is `fn deploy` as the admin user, or, when this binary is not the dev tip, the version remedy (deploying an older binary's library would roll the store back).
- `version`: this binary's build identity against `fleet:release commit`, the dev tip every landing into dev writes; the remedy is `self update --sha <sha>` on the coordinator's machine (`fleet:release self`), `fleet build --bench <b>` on a bench.
- `runners`: this machine (`--bench`, else the short hostname) in the `benches` registry (`fleet:release self`, the coordinator's machine, beats as a bench outside it by design and counts, printed `self=yes`), its role, CI legs and hold (`bench:<b>:desired`), and its beat's age against the preflight's 2 s; the remedy restarts the beat unit.
- `ingest`: `ev:github`: its last entry's age and sender, printed as information (a quiet stream is not a dead receiver: the stream also carries runner receipts, and evenings are quiet); a fix only when the `ci-github` group lags or holds pending entries (`ci github --once` drains it).
- `pitstop`: every sprint not closed, its `s:<S>:pitstop` (or the legacy key); a held stop names who set it, when and its `reason=`, and the `pitstop clear` line, whose `--by` is `NOVA_FRIEND`, else the Redis user, else the seat, else the machine.
- `sprint`: the one open sprint (control sprints aside) and `sprint:epoch`; none or more than one is a fix.

The last line is `DOCTOR OK checks=8 trips=<n> ms=<n>` (exit 0) or `DOCTOR FIX fixes=<n> skipped=<n> checks=8 trips=<n> ms=<n>` (exit 1); 2 is usage. No ssh, no GitHub, no model: everything past the seat is one pipeline, and a second only for the sprints and `ns_ping`, so `trips=` is at most 2.

```text
nova-sprint doctor --seat studio --redis 100.115.99.19:6380
DOCTOR seat OK seat=studio user=coordinator key=NOVA_REDIS_COORDINATOR_PASSWORD
DOCTOR redis OK addr=100.115.99.19:6380 user=coordinator
DOCTOR fn OK sha=0123456789abcdef ping=PONG
DOCTOR version OK have=v0.16.0-dev.c839379e tip=c839379e4eab
DOCTOR runners OK bench=studio role=friends legs=- beat=1s
DOCTOR ingest OK stream=ev:github last=40s sender=glenn group=ci-github lag=0 pending=0
DOCTOR pitstop FIX sprint=s1 by=glenn age=10m reason="rest" held=1 why="a pit stop idles every automatic duty; lift it when the stop is done" remedy="nova-sprint pitstop clear --sprint s1 --by rowan --redis 100.115.99.19:6380"
DOCTOR sprint OK sprint=s1 epoch=7
DOCTOR FIX fixes=1 skipped=0 checks=8 trips=2 ms=61
```

### `nova-sprint fleet build`

`nova-sprint fleet build [--redis <addr>] [--bench <b>[,<b>...]] [--build-cmd <path>] [--machines <file>] [--dry-run]` is the fleet deploy (nova-tools #3310), with its whole plan in Redis: the `fleet:release` hash holds `version` (`v<x>.<y>.<z>-dev.<sha8>`), `commit` (the full sha of that `<sha8>`), `builder` (the bench that builds), `self` (this machine's bench name), an optional `tools` list (default `nova-sprint,nova-swarm,nova-card,nova-wake`) and `platform:<bench>` (`<goos>-<goarch>`) for every bench and for `self`; the `benches` set names where to install. One pipeline reads both, and a gap is refused (`FLEET BUILD REFUSED: <why> (<remedy>)`, exit 1) before any child starts. The run: the builder builds the release once for the distinct platforms, in one ssh session running the nova-sprint the last fleet build installed there (`ssh -n <builder> .local/bin/nova-sprint fleet build compile --version <v> --commit <sha> --platform <list>`, which skips a platform already built; `--build-cmd <path>` runs that command locally with rowan-tools' space-build argv `--host <builder> --version <v> --commit <sha> --platform <list>` instead), and the `BUILD OK` line carries its last line; every bench, in one ssh session each and all at once, rsyncs its platform's tools from `<builder>:nova-bench/release/<v>/<platform>/` (the builder from its own disk) into `~/.local/bin.new`, renames each into `~/.local/bin` and prints its `nova-sprint version` line; this machine does the same locally. Each target prints `OK|MISMATCH|FAIL <bench> platform=<p>: <detail>`; a target whose version line names the release gets its receipt in one pipeline, `bench:<b>` fields `build`, `build_sha`, `build_at`, and a failed or mismatched one keeps its old receipt. Every target's probe result goes on its beat, never on a consumer's cards (nova-tools #4237): `bench:<b>:beat probe` is `<OK|MISMATCH|FAIL> <v> <utc>` when the beat exists (`PROBE <bench> beat=bench:<b>:beat probe="..."`), and a bench with no beat gets `PROBE NOBEAT <bench> probe="..."` and no beat written; the bench's own beat leaves the field as it is. Last, the new nova-sprint here runs `fn deploy --redis <addr>`, so the store's function library is this release's. The final line is `FLEET BUILD OK version=<v> commit=<sha12> benches=<n> fn=ok` (exit 0) or `FLEET BUILD FAIL ... at=build|install|fn ok=<n> failed=<list>` (exit 1). `--dry-run` prints `WOULD BUILD`/`WOULD INSTALL` lines and starts nothing. `nova-sprint fleet build set [--redis <addr>] <key>=<value>...` validates and writes `fleet:release` fields in one HSET. `nova-sprint fleet build compile --version <v> --commit <sha40> [--platform <p>[,<p>...]] [--repo-url <url>] [--dry-run]` is the builder half (nova-tools #4080; internal/nsprint/fleetbuild/compile.go), space-build's steps in Go: a platform already published under `~/nova-bench/release/<v>/<p>/` whose SHA256SUMS verifies prints `OK <p>` and is not rebuilt; the commit and the v* tags are fetched from GitHub into `~/nova-bench/space-build/src/nova-tools` (the mirror only an object alternate); a missing linux-amd64 reference at `~/nova-bench/build/<v>` is built first (`REFERENCE BUILT ...`); every cmd/nova-* is built per platform with `CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=<v>"` under the reference's toolchain, headers checked, the linux-amd64 build held file for file to the reference's sha256 (`IDENTICAL`), and each platform renamed into the release root (`PUBLISHED`). Every go child runs with the build's OWN Go state, `GOMODCACHE=~/nova-bench/space-build/go/mod`, `GOCACHE=~/nova-bench/space-build/go/build` and `GOTOOLCHAIN=<the reference's toolchain>` (a downloaded toolchain lands in that module cache), on top of the sanitized environment, so it never shares a cache or a toolchain extraction with a CI runner on the same machine; `BUILT <v> platforms=<list> tools=<n> toolchain=<go> gomodcache=<dir> gocache=<dir> log=<file>` is the receipt, and the last line is `FLEET COMPILE OK <v> commit=<sha> platforms=<list> built=<list>|none [go=<dir>]` (exit 0) or `FLEET COMPILE REFUSED: <why> (<remedy>)` (exit 1); 2 is usage. The loops that run the old binaries are not restarted by this verb.

Since nova-tools #4050 nothing in the plan is typed. `land merge` of nova-tools into dev writes `fleet:release` `version` (`v<x>.<y>.<z>-dev.<merge sha8>`, the train of the version already stored, else `v0.16.0`), `commit` (the merge sha) and `landed` in the same Lua call that marks the landing merged, and its receipt ends `release=<version>`. Before each plan, `builder`, `self` and `platform:<bench>` converge (one HSET of what changed, `CONVERGED k=v ...`; `WOULD CONVERGE` under `--dry-run`): `builder` is the one machine of the machines registry (`--machines <file>`, else `$NOVA_FLEET_MACHINES`) whose roles carry `services`, `self` the one carrying `coordination`, and each bench's platform is `bench:<b>:desired platform`, else the registry's os/arch, else the platform of the version line its beat names. After the build the release manifest (`SHA256SUMS` in the builder's `<v>/<platform>/`) names the tools: every `nova-*` the build produced is installed and recorded as `fleet:release tools` (`MANIFEST tools=<n> <list>`; a manifest without nova-sprint is `MANIFEST FAIL`). `--build-cmd` defaults to `$NOVA_FLEET_BUILD_CMD`, else none, which is the compile verb on the builder. `--redis` goes anywhere on the line, `set`'s pairs included.

`nova-sprint fleet build duty [--redis <addr>] [--machines <file>] [--dry-run]` is one pass of the reconciler's `fleet-deploy` duty, which `nova-sprint reconcile` runs every pass: it converges `fleet:release`, reads every registered bench's beat (`bench:<b>:beat build`, a bench with no live beat is quiet, never drift) and, when a beating bench names another version than `fleet:release version`, claims the deploy of that version (`SET fleet:release:deploy <version> NX`, held for the build's bound, so a running deploy is never started twice and a failed one is retried after it) and starts `nova-sprint fleet build --bench <drifting benches>` in its own session, its output in `~/nova-bench/logs/fleet-build-<version>.log` (`FLEET DEPLOY START version=<v> commit=<sha12> benches=<list>`). `--dry-run` prints `FLEET DEPLOY WOULD INSTALL <bench> beat=<version> want=<version>` per drifting bench and `FLEET DEPLOY DRY-RUN ...`, and starts nothing; a pass with nothing to do prints `FLEET DEPLOY IDLE version=<v> why=current|noplan ...`.

### `nova-sprint fleet release <sha>|dev`

`nova-sprint fleet release <sha>|dev [--redis <addr>] [--machines <file>] [--benches <a,b,...>] [--play-dir <dir>] [--play tools.yml] [--wait 60s] [--admin-password-env NAME]` is the whole roll of a landed dev commit as one command (nova-tools #4306, #4356 item A; internal/nsprint/fleetbuild/release.go): what was nine hand commands across three tools (an ssh for the admin password, two variables, `fn deploy` and `fn check`; `go build`, `mv` and `version` for the Studio; the bench play and an ssh verify). The one command a release is, run as the coordinator seat: `nova-sprint --seat studio fleet release dev` (or a sha). `dev` is dev's tip (`git ls-remote git@github.com:mas-bandwidth/nova-tools.git refs/heads/dev`, `DEV TIP <sha40>`); a sha is 8 to 40 hex digits; the version is `v0.16.0-dev.<sha8>`. The steps, in order, each ending in one `RELEASE <step> OK <detail>`, `RELEASE <step> REFUSED: <why> (<remedy>)` or `RELEASE <step> SKIPPED: <why>` line:

- **build**: `SOURCE <dir> <sha40>` (the clone under `~/nova-bench/release-src/nova-tools`, shallow and dev only, fetches dev and checks the commit out detached; a 40-digit sha outside dev's last 50 is fetched on its own); `BUILT <v> ~/nova-bench/release-src/bin/nova-sprint-<v>` (the release's own nova-sprint for this machine, with go.mod's pinned Go as `GOTOOLCHAIN`, `KEPT` when it already answers `<v>`; it carries the release's Lua); `FLEET RELEASE SET version=<v> commit=<sha40>` and `CONVERGED ...` (fleet:release as `fleet build set` writes it); then the builder's compile of every bench platform with its `BUILD OK` and `MANIFEST` lines, which publishes `nova-bench/release/<v>/<platform>/` for the play. `RELEASE build OK version=<v> commit=<sha12> builder=<b> platforms=<list> tools=<n> bin=<path>`.
- **fn**: `fn deploy --redis <addr>` by the release binary as the Redis `admin` user (`NOVA_SPRINT_REDIS_USER=admin`, `NOVA_SPRINT_REDIS_PASSWORD_ENV=NS_ADMIN`, `NOVA_SEAT=` so no seat login wins). The password is the variable `--admin-password-env` names (default `NS_ADMIN`), else the seat's sealed `NOVA_REDIS_ADMIN_PASSWORD`, read in this process through nova-secrets' library (`--seat <name>` or `NOVA_SEAT`; seal it once with `make -C ~/rowan-working/rowan-tools/fleet store-seal ROLE=admin SEAT=<seat>`); it is put in the fn children's environment only and never printed, and never read by ssh. With neither, the step alone is refused with that remedy.
- **fn-check**: `fn check --redis <addr>` by the same binary (as admin when the password is known, else as the seat): the store holds this release's library and `ns_ping` answers.
- **play**: the bench play through ansible (`ansible-playbook -i inventory.py <play> --forks 16 --diff -e nova_build=<v> --limit <benches>` in the play directory, `--play-dir`, else `$NOVA_FLEET_PLAY_DIR`, else `~/rowan-working/rowan-tools/fleet`, with `ANSIBLE_NOCOWS=1 ANSIBLE_HOST_KEY_CHECKING=True FLEET_REGISTRY=<registry>`), one `RECAP <host> ...` line per host. The `--limit` is always given: the play's last play is the coordinator's fn-load, which would reload the coordinator's not-yet-updated library over the one the fn step deployed. Then every bench whose beat is not on `<v>` has its beat restarted through ansible over the same inventory (`ansible <benches> -i inventory.py -m ansible.builtin.shell -a <the darwin kickstart or the linux systemctl restart>`), one `BEAT <bench> restarted|failed <status>` line each. `RELEASE play OK <play> version=<v> hosts=<n> beats-restarted=<n>`.
- **self**: `self update` of this machine at the commit (its `SOURCE`, `TOOLCHAIN`, `BUILT` and `MOVED` lines; install by rename only), then `KICKSTARTED <unit>` for `nova-sprint-reconciler`, `sprint-table-live` and `nova-sprint-bench-beat`, unless the binary was current and this machine's beat already names `<v>`. `RELEASE self OK <old> -> <new> bin=<path>`.
- **verify**: every bench's beat build (`bench:<b>:beat build`, one pipelined read, no ssh) re-read every 5 s until each names `<v>` or `--wait` is spent, one `VERIFY <bench> want=<v> have=<v>|none ok|behind` line per bench.

A step's refusal never stops the steps after it that can still run: fn and fn-check need the release binary, the play needs the builder's published build, self needs the commit, and the verify always runs. The benches are `--benches`, else every machine of the registry (`--machines <file>`, else `$NOVA_FLEET_MACHINES`, required) with the `bench` role. It is idempotent: a rerun keeps the release binary, restarts only the beats still behind, and skips self update when this machine is current. The last line is `FLEET RELEASE OK|BEHIND|FAIL version=<v> benches=<n> behind=<list>|-`: exit 0 OK (every step answered, every bench beat on `<v>`), 1 FAIL (a step refused or was skipped; FAIL wins over BEHIND and the benches behind are still named) or BEHIND, or `FLEET RELEASE REFUSED: <why>` on standard error before any step (a bad sha, no registry, no benches, dev's tip unreadable); 2 is usage and 5 the store unreachable. `fleet roll` is retired into this verb (`nova-sprint fleet roll: is retired into fleet release ...`, exit 2), and so are `--studio-only` (`self update` does this machine alone) and `--benches-only`. `nova-sprint fleet release --bench <b>` with no sha is the older verb: it releases a held bench (the fleet state machine), and the two forms refuse each other's flags.

### `nova-sprint self update`

`nova-sprint self update [--sha <sha>] [--from <checkout>] [--allow-branch]` rebuilds this machine's own nova-sprint and installs it by rename (nova-tools #4337; internal/nsprint/fleetbuild/selfupdate.go): the coordinator's hand rebuild after every landing, as one verb. The fleet play does the benches; this verb does the machine it runs on. With no `--from` it builds in the release clone `fleet release` uses (`~/nova-bench/release-src/nova-tools`, fetched from dev) at `--sha`, or at dev's tip when none is given; with `--from <checkout>` it builds that checkout as it stands (it runs `git fetch origin dev` there and never checks anything out), and a `--sha` that is not the checkout's HEAD is refused. The receipt lines: `SOURCE <dir> <sha40>`; `TOOLCHAIN <go> from <go.mod>` (the module's pinned Go: its `toolchain` line, else its `go` line, handed to the build as `GOTOOLCHAIN`); `BUILT <v> <temp>` (`go build -trimpath -ldflags "-X main.version=<v>" ./cmd/nova-sprint` into a temp file beside the live binary, which must answer `<v>` before anything is installed); `MOVED ~/.local/bin/nova-sprint <v>` (the temp file renamed over the live binary, then the binary must answer `<v>`). The install is a rename and nothing else, so a process running the old binary keeps its file; the verb holds no code that writes into the live binary, and a class test keeps it that way. `<v>` is `v0.16.0-dev.<sha8>` for a commit on origin/dev (the version `fleet release` stamps, so it sees this machine already current); a commit off dev, or a `--from` tree with uncommitted edits, is refused unless `--allow-branch`, which stamps `-branch.<sha8>` and `-dirty`. A live binary already answering `<v>` is not rebuilt: `SELF UPDATE SKIPPED <bin> already answers <v> commit=<sha12>`. The last line is `SELF UPDATE OK <old> -> <new> commit=<sha12> toolchain=<go> bin=<bin>` (exit 0; `<old>` is `none` when no binary answered), or `SELF UPDATE REFUSED: <why> (<remedy>)` on standard error (exit 1, nothing installed); 2 is usage. The loops on this machine keep running the old binary until kickstarted; `fleet release <sha> --studio-only` builds, moves and kickstarts.

### `nova-sprint fleet play`

`nova-sprint fleet play <tag> [--limit <a,b,...>] [--dry-run] [--redis <addr>] [--machines <file>] [--play-dir <dir>]` runs one rowan-tools fleet play through the verb, never by hand (nova-tools #4356 item C; internal/nsprint/fleetbuild/play.go). `<tag>` names the play: `tools` runs `tools.yml` in the play directory (`--play-dir`, else `$NOVA_FLEET_PLAY_DIR`, else `~/rowan-working/rowan-tools/fleet`); the machines registry is `--machines <file>` else `$NOVA_FLEET_MACHINES`, required, and `--limit` names registry machines only. `--redis <addr>` names the store the receipts go to; without it the address is the seat's Redis under `--seat` (the seats.tsv row, #4382), else the environment default, as `fleet release` and `fleet ps` do. Before the play the rowan-tools clone holding the play directory is checked, each refusal naming the git command that fixes it: dirty (`git status --porcelain` not empty, untracked files included: `commit and push it, or git -C <clone> stash -u`), behind its upstream (`git fetch -q`, then `git rev-list --count HEAD..@{u}` above 0: `git -C <clone> pull --ff-only`), or no upstream (`git -C <clone> switch main`). The play is the runner and argv `fleet release` plays the bench play with, `ansible-playbook -i inventory.py <tag>.yml --forks 16 --diff [--limit <a,b>]` in the play directory with `ANSIBLE_NOCOWS=1 ANSIBLE_HOST_KEY_CHECKING=True FLEET_REGISTRY=<registry> ANSIBLE_CALLBACKS_ENABLED=ansible.posix.profile_roles` (the callback prints each role's time); `--dry-run` adds `--check` (ansible moves nothing) and writes no receipt. From ansible's own output it prints one `FLEET PLAY <bench> <role> ok|changed|failed ms=<n>` line per bench and per role, benches in the PLAY RECAP's order and roles in run order: a role's state is failed when a task in it failed on that bench and the recap counts the bench failed or unreachable (an `...ignoring` failure and a rescued one are not), else changed when a task changed, else ok; `facts` is Gathering Facts (where an unreachable bench stops), `tasks` a play's own tasks outside any role; `ms` is the role's time from the ROLES RECAP (`-` without one). Each bench's receipt is one HSET of `bench:<b>:play` {`at` (unix ms), `tag`, `sha` (the clone's HEAD), `role` (the last role it ran), `result` (`ok` or `failed:<role>`, the role it stopped in)}, read by `fleet doctor` and the table: a bench whose result is `failed:<role>` shows `behind: <role>` in the wide table's why and in the live table's status (an up bench; down wins). A play that dies before any bench (no PLAY RECAP) prints `PLAY ABORTED <tag>.yml err=<exit> last=<line>` and writes nothing (ABORTED, not ERROR: `error` is a provider error mark, and this family's own second word must never be one; internal/swarm TestNoEventLineOfThisFamilyHasAMarkForItsSecondWord). The last line is `FLEET PLAY OK|FAIL tag=<tag> sha=<sha12> benches=<n> failed=<bench:role,...>|-` (` check=yes` on a dry run): exit 0 every bench ran every role, 1 a bench stopped (the failed list), no bench ran, or `FLEET PLAY REFUSED: <why>` on standard error before the play ran; 2 usage; 5 the store unreachable.

### `nova-sprint fleet churn`

`nova-sprint fleet churn [--seconds 12] [--machines <file>] [--only <a,b,...>]` is the process-age sample as a verb (nova-tools #4310; internal/nsprint/fleet/churn.go): finding the relaunch churn (a bench-row every second, a ci-run dead and relaunched, a grok heartbeat) took a hand `ps` per machine. It runs one sample on every machine of the registry (`--machines <file>`, else `$NOVA_FLEET_MACHINES`; `--only` narrows to the names given and refuses one the registry lacks), all at once, over the registry's ssh column (`ssh -n -o BatchMode=yes -o ConnectTimeout=6 <host> <ps>`; a machine whose ssh column is `localhost` is sampled without ssh): `ps -eo pid,etimes,ppid,comm` on linux, `ps -Ao pid,etime,ppid,comm` on darwin with `[[dd-]hh:]mm:ss` parsed to seconds, a path reduced to its base name and a login shell's leading `-` dropped. Per machine, in registry order, it prints one `<machine> young <command> <n>` line per command with a process younger than `--seconds` (most first), one `<machine> old pid=<pid> age=<n>d comm=<command>` line per process older than a day whose parent is 1, and last `CHURN <machine> young=<n> old=<n>` (young counts processes, not commands). The sample's own pipeline is left out: `ps`, `awk`, `sshd`, and the shell `ps` runs under. A machine whose sample fails prints `CHURN <machine> FAIL <why>` (an ssh error, a bad ps line, or an os/arch that is neither linux nor darwin) and the verb goes on to the rest. Exit 0 every machine answered, 1 a machine failed, 2 usage or no registry.

### `nova-sprint acl check`

`nova-sprint acl check [--redis <addr>] [--rows <file>] [--admin-password-env NAME]` is the store's live Redis ACL against the declared users (nova-tools #4333; internal/nsprint/acl): one `ACL LIST`, read as the `admin` user with the password from the variable `--admin-password-env` names (default `NS_ADMIN`; the password lives on the store, `/var/lib/nova-redis/admin.pass`, and is never a flag), diffed against the rows file. It replaces the hand `ACL SETUSER` of apply-acl.sh and fix-acl.py: drift is one command away and is never fixed by hand.

The rows file (`--rows`, default `/var/lib/nova-redis/acl-rows.tsv`, which the play installs beside the server) is one declared user per line, `<user>` TAB `<rules>`, where `<rules>` is the ACL SETUSER rule list after the password: `rowan-tools/fleet/redis.yml` `redis_users` rules, each line of `rowan-tools/fleet/templates/redis-acl.rules` (its first space a tab), and `default` with the `off ~* &* +@all` the play writes first. A leading `on` or `off` is the user's state (default `on`); `#` lines and blank lines are skipped; a password token (`>`, `<`, `#`, `!`, `nopass`, `resetpass`) is refused, as are a duplicate user and an unknown rule, each naming its line. `internal/nsprint/acl/testdata/acl-rows.tsv` is the mirror of `rowan-tools/fleet/redis.yml` at 2026-09-26.

The comparison is by effective grant, not text, because servers print the rules they hold differently: redis-server 8.x prints them as written (`+@all -@dangerous +info +config|get`), 7.0 prints a compaction of its command bitmap (`+@all -@admin -flushall +config|get -keys ...`). So the verb reads `ACL CAT` for every category in the same pass as `ACL LIST` and `INFO server`, applies each side's command rules in order over the server's own categories into the commands and subcommands the user may run, and compares those sets; a first-argument grant (`+fcall|ns_ping`) stays a token unless the whole command is granted. Key and channel patterns compare as tokens (`%RW~k` reads as `~k`, `allkeys` as `~*`, `allchannels` as `&*`; `resetkeys`, `resetchannels`, `reset`, `sanitize-payload` and password hashes are dropped); a selector `( ... )` is one token of its own grants. The class test holds the rows to zero drift against the captured renderings of redis-server 8.10.2, 8.0.5 and 7.0.15 (`internal/nsprint/acl/testdata/redis-<version>.acl`). Each drifted user prints one line, declared users in rows order, then users the server holds that no row declares:

```
ACL DRIFT user=bench missing="~cfg:deal" extra="+sort"
ACL DRIFT user=viewer state=off want=on
ACL DRIFT user=ns-deploy absent=live
ACL DRIFT user=ghost absent=declared
ACL CHECK DRIFT store=127.0.0.1:6380 redis=8.0.5 drifted=4 users=12 rows=/var/lib/nova-redis/acl-rows.tsv converge="make -C rowan-tools/fleet store"
```

`missing` is what the row grants and the live user lacks, `extra` what the live user holds beyond the row; a run of commands that is a whole category of the server prints as `+@<category>`. No drift is `ACL CHECK OK store=<addr> redis=<version> users=<n> rows=<file>`. `acl check --fix` is refused, `REFUSED acl check --fix: the play is the only writer of the store's ACL, never a hand ACL SETUSER; run: make -C rowan-tools/fleet store`: the play writes `users.acl` from the declared users and applies it with `ACL LOAD`. A rows file, password or store that cannot be read is `ACL CHECK REFUSED ... reason=... remedy=...` on standard error. Exit 0 no drift, 1 drift, 2 could not check (or `--fix`).

### `nova-sprint fleet ps`

`nova-sprint fleet ps [--redis <addr>] [--bench <b>] [--stray] [--since <RFC 3339 | duration>]` is what runs on every bench, read from the beats with no ssh (nova-tools #4338; internal/nsprint/fleet/ps.go): the scan rowan-tools probe-fleet.py did over ssh with ps and top. The bench beat (`nova-sprint bench beat`) reads ps at most once per 10 s (`ps -eww -o pid=,ppid=,etimes=,pcpu=,uid=,args=` on linux, `ps -Aww -o pid=,ppid=,etime=,pcpu=,uid=,args=` on darwin; pcpu is ps's own: recent on darwin, lifetime on linux) and its nova unit files (`com.nova.*.plist` in ~/Library/LaunchAgents and /Library/LaunchDaemons, `nova-*.service` in ~/.config/systemd/user and /etc/systemd/system), and writes one bounded JSON sample as the beat's `ps` field: the top 5 processes by CPU, the nova units each `declared` (the file names the fleet play that wrote it, `fleet/<play>.yml`) or `undeclared`, and the 12 oldest of the bench user's processes outside every declared unit (a process runs a declared unit's command, or the command after its `--`, maybe behind an interpreter, or descends from one that does; system daemons, apps, login shells and ssh-agent are left out), each command capped at 120 bytes, with the totals before the cut. A ps that fails is a sample carrying its error. Per registered bench (`SMEMBERS benches`, sorted; `--bench` one), ps prints `<bench> load1=<l> ncpu=<n> cpu=<pct> sample=<age>`, one `<bench> top pid=<pid> cpu=<pct> user=<u> age=<age> cmd=<cmd>` per top process, one `<bench> unit <name> undeclared` per undeclared unit, and last `PS <bench> top=<n> units=<n> undeclared=<n>`. `--stray` prints only the undeclared units and one `<bench> old pid=... cmd=...` per process outside every declared unit that started before the last play, then `STRAY <bench> units=<n> old=<n> play=<t>`; the last play is the bench's last deploy (`bench:<b> build_at`), or `--since` (an RFC 3339 time, or a duration back from now) for every bench. A bench with no beat (`NOBEAT`), a beat with no sample (`NOSAMPLE`, a build from before #4338), a failed sample (`FAIL <why>`) or no last play (`old=? no last play`) prints its line and is never read as clean. Exit 0 every bench read (with `--stray`, and nothing stray); 1 a bench could not be read, or `--stray` found a stray; 2 usage; 5 store unreachable.

### Cutting a card from an issue: `nova-sprint card cut`

`nova-sprint card cut --sprint <S> --repo <owner/name> --issue <n> [--spec <n>] [--index <dir>] [--stream <name>] [--base <branch>] [--redis <addr>]` (nova-tools#3623) is the cut `nova-pulse cut` did, as one Redis write: it reads the issue over REST (`gh api`, the caller's `GH_CONFIG_DIR`), renders the card in the card-push shape (`KIND`, `TASK`, `REPO`, `BASE`, `base-sha`, `PATHS`, `TEST`, `DEPENDS-ON`, `WHY`, `DONE-WHEN`, `WHO`, `STREAM`, `EST`, `ORIGIN`, then the issue quoted line by line), stores the exact bytes at `s:<S>:body:sha256:<sha>` and pushes the card as `card push` does, so the record `s:<S>:card:<name>-<n>` lands in waiting (an unmet dependency) or ready. The first line of each key anywhere in the issue body is read; `--stream` and `--base` override, `WHO` defaults to `any`, and an issue with no `base-sha` gets the branch tip. `DEPENDS-ON` is rewritten into the card vocabulary (`#n` and `name#n` become `owner/name#n`; a `(WHY: ...)` becomes the `WHY:` line). `--index` names a context index directory (`internal/ctxindex`) and inlines the CONTEXT block for the spec IDs the issue (and the `--spec` issue) names. No file and no queue directory is written. A recut of the same issue is `place=exists`.

The card is a spec (nova-tools#4313): its `TEST` line names the one test the change is proved by, `<package> <TestName>`, read from the issue's `TEST:` line or from a `go test <pkg> -run <TestName>` in its `DONE-WHEN`; a card with no test says why on that line, `TEST: none <why>`, and the why reaches the copy's card, its `RESULT.md` and its PR body so the reader sees it. An issue whose `DONE-WHEN` cannot be turned into a test (no `TEST:` line and no `go test` in the sentence, or a bare `TEST: none`) is refused before any write, with the remedy on the line: name `go test <package> -run <TestName>` in `DONE-WHEN`, or add `TEST: <package> <TestName>`, or `TEST: none <why the change has no test>`. `task push` holds a swarm card (`ROUTE: frontier|pro|flash`) to the same line (`INCOMPLETE task:<id> route=<r> lacks TEST (...)`).

One receipt: `CARD CUT <S>/<repo>#<n> label=... place=pool|waiting|exists stream=... contexts=<k> origin=<url>`. Exit 0 cut; 1 refused with `REFUSED card cut ... why=...` (no `STREAM` and no `--stream`, a `DEPENDS-ON` that is not a card id, owner/repo#n, stream/<slug> or task:<id>, no `PATHS` or `DONE-WHEN`, a `DONE-WHEN` no test can fail, or a missing index, each before any write; or the push's own refusal); 2 usage.

The wrapper (internal/nsprint/card/wrapper_spec.go) holds every code card's commit to the card before its end pushes or harvests anything, a copy's (`nova-card copy`) and a sprint card's (`nova-card <S>/<label>/<n>`) alike: the diff adds or changes at least one `_test.go`; `TEST` passes at the head and fails at `base-sha` with the diff's test files checked out over it (a `TEST: none <why>` card is excused from that, not from CI). `TEST` runs under `go test -json`, and green is the named test's own `pass` event: `[no test files]`, `[no tests to run]` and a subtest's pass are not green, and a red at base is the named test's own `fail` event, or its package failing to build only when the diff's test files add or change the named test (never `[setup failed]`, and a `pass` of the named test at base is never red); `TEST` names one package, never a `...` pattern. `TEST: -tags <tags> <package> <TestName>` runs with those tags, and `go test -p 2 -tags functional ./pkg -run TestX` in `DONE-WHEN` derives it. A fix copy's commit sits on the PR head, where the primary's `TEST` is green already, so a fix is held to the finding test it names on `RESULT.md` line 3 (`TEST: <package> <TestName>`, never `none`), not to the primary's. A friend's copy has no wrapper: `nova-friend done --ok --pr` (the retired `nova-sprint friend done`) runs the same gate in `--repo <checkout at --head>` (a fix names its finding test with `--test`) and refuses the end on a red; then `nova-ci local --base <base-sha>` in the checkout (#4360: the unit tier CI runs for the diff), or, where the verb cannot run, `go test -json -p 2 -count=1` of the touched packages. A red ends the copy `FAILED` with the typed reason (`no-test`, `test-not-green`, `test-not-red`, `ci-red`; a sprint card's end is `FAILED tests-red`, the reason `ns_card_end` takes, with `gate=<reason>:` leading its why) and one line naming the red and the remedy; no PR is opened; the rows, every `RED package=<p> test=<t>` line included, go under `## Gates` in `RESULT.md`; `wrapper.line` carries `gate=<pass|reason>` and the copy's record `evidence` = `gate=<pass|reason> red=<names|->`.

### Many cards from one file: `nova-sprint card cut --from`

`nova-sprint card cut --from <cards.tsv|-> --repo <owner/name> [--stream <s>] [--sprint <S>] [--base dev] [--base-sha <sha40>] [--actor <a>] [--redis <addr>] [--dry-run] [--no-github]` (nova-tools#4340; cmd/nova-sprint/card_cut_from.go) files one issue per row through the one GitHub writer (`nova-sprint file`'s REST create and read-back) and pushes each row as a task card (`task:<id>`, the record `task push` and `quack cut` write) onto its stream's waiting set, every push in one pipeline. `-` reads the file from standard input.

The file is tab separated, one card per line; blank lines and lines starting with `#` are skipped. The columns, in this order unless the first line is a header row naming them (`done_when` and `DONE-WHEN` spell `done-when`):

| column | what | default |
|---|---|---|
| `title` | the issue title and the card's task, one line | required |
| `stream` | the card's stream | `--stream` |
| `who` | `any`, `only <names>` or `except <names>` | `any` |
| `paths` | the card's PATHS | required |
| `done-when` | the card's DONE-WHEN | required |
| `body` | the issue text below the card lines | empty |
| `depends-on` | `#<n>` (an issue of `--repo`), `owner/name#<n>`, or a task id (`task:<id>` or `<id>`), comma separated; `none` or `-` is none | none |
| `route` | `frontier`, `pro`, `flash` (a swarm card, complete at push, with `base-sha`) or `friend` | `friend` |
| `est` | minutes: `30`, `45 min`, `2 h` | `30` |
| `id` | the task id; only a header row can name this column | `<repo name>-<issue n>`, or with `--no-github` the title's slug |
| `test` | the card's TEST line: `<package> <TestName>` or `none <why>`; only a header row can name this column (#4313) | the `go test <pkg> -run <TestName>` in `done-when`; a swarm row with neither is refused |

A cell writes a newline as `\n`, a tab as `\t` and a backslash as `\\`. A `depends-on` entry that is another row's `id` cell is that row: its issue is filed first, the issue's `DEPENDS-ON` carries that row's issue ref and the card's `blocked_on` its task id. A row that is depended on needs an `id` cell: there is one DEPENDS-ON form (#3409), so `row:<n>` is refused naming the id column, and with `--no-github` naming a row by its title slug is refused the same way.

Every row is checked before anything is written: a bad row is named with its row, line and why, and then nothing is filed or pushed. Then each issue is filed in dependency order and written to the cut ledger, `cut:<sha256 of the file>` (`repo` and `<row n> -> <issue n>`, never expired), before the next is filed; the ledger is read before the first filing. A rerun of the same file after a partial or a full filing therefore files nothing twice: a row the ledger holds takes its issue from there, and its card, when an earlier run pushed it, is `to=already`. The first forge failure stops the filing and names every row behind it; the rows filed before it are still pushed. `--dry-run` prints the rows in push order and touches neither GitHub nor Redis (nor the ledger); `--no-github` files no issue and writes no ledger.

The four receipt lines, all on standard output:

```
CARD CUT row=<n> id=<id> ref=<owner/name#n|-> stream=<s> to=waiting|already depends=<ids|none>
CARD CUT REFUSED row=<n> line=<l> id=<id|-> why=<why> [remedy=<what to run>]
CARD CUT DRY row=<n> id=<id|-> stream=<s> who=<w> route=<r> est=<e> depends=<d> title=<t>
CARD CUT FROM file=<f> rows=<n> cut=<k> already=<a> refused=<r> filed=<f> reused=<u> github=on|off ms=<ms>
```

`CARD CUT` is one row cut (or already cut by an earlier run of this file); `CARD CUT REFUSED` names a row and why (a refusal of the whole file, such as an unreadable ledger or a ledger filed on another `--repo`, prints `file=<f>` in place of the row and carries `remedy=`); `CARD CUT DRY` is one row of a `--dry-run`; `CARD CUT FROM` is the summary, last: `filed` counts issues filed by this run, `reused` the rows whose issue came from the ledger. Exit 0 every row cut or already; 1 a row refused (named); 2 usage.

### Work as a hierarchy: `nova-sprint card cut --parent`, `card stitch` (nova-tools#4317)

Glenn 2026-09-26: "You are fable, you deploy to children, you review their work at the end, and stitch it together. Can you do all this within the worker system?" A PLAN is a parent card the coordinator cuts into child cards on the same stream, with a STITCH card behind them; the parent's state is derived and it lands when the stitch lands. Everything is the one edge form (`blocked_on`, a comma list of task ids, released by the waiting resolver): the stitch DEPENDS-ON every child, the parent DEPENDS-ON the stitch.

`nova-sprint card cut --parent <id> --from <children.tsv|-> [--stitch-route frontier] [--stitch-est 60] [--repo <owner/name>] [--sprint <S>] [--base-sha <sha40>] [--actor <a>] [--dry-run] [--no-github]` cuts the rows (the `card cut --from` file, same columns; a row another row depends on has an id cell, as above) as the parent's children and the stitch in one call. Every child rides the parent's stream (a row naming another stream is refused; `--stream` is refused) and carries `parent=<id> phase=child`; the parent's repo, base and base-sha are the rows' defaults. The stitch, `<id>-stitch`, DEPENDS-ON every child row (and every child the parent already has), carries the parent's DONE-WHEN as its own, the union of the children's PATHS, `KIND: stitch`, `ROUTE --stitch-route` (frontier by default: a model type advertised by the worker, never a name; `friend` for the coordinator's own session) and a body whose generated section (`## Children`) is every child's PR, RESULT.md summary (line 2 and finding) and read score. Then the parent is bound (`taskcard.BindPlan`): `kind=plan children=<ids> stitch=<id> blocked_on=<stitch>`, moved to waiting (from ready) through the one move. A parent that is working, review, merging, landed or done is refused by name (a plan is cut while its parent waits); a parent whose stitch is still waiting takes more children (the stitch's edges grow); one whose stitch ended done re-cuts it (below); one whose stitch is in flight is refused. The cut ledger applies as for `--from`: a rerun reuses the issues, the cards are `to=already`, and the bind runs again with every child. Receipts: one `CARD CUT row=<n>|stitch ...` line per card, then `CARD CUT PLAN parent=<id> children=<n> stitch=<id> parent_to=waiting depends=<stitch>`; `--dry-run` prints `CARD CUT DRY ...` and `CARD CUT DRY PLAN ...` and writes nothing (it reads the parent, so it needs the store).

The rules in the one writer (`02_card_move.lua`): a plan is never dealt (`card deal` refuses it: `PLAN task:<id> is a plan: its children are dealt and its stitch lands it`); it lands from waiting or ready at the stitch's merge sha only once the stitch is landed (`PLAN task:<id> lands when its stitch <s> lands (now <where>)` until then), and `ns_tcard_land_stream` (the stream landing) lands the parent in the same call as its stitch, naming it in the reply so the lander closes its issue. The parent's derived state (`taskcard.Plan.State`): working while any child is working, review or merging; ready when one is ready; review, merging or working when the stitch is; landed when the stitch lands.

The stitch's brief is regenerated when the waiting resolver releases the stitch to ready (after every child landed), so the coordinator's stitch child starts with the whole picture. `nova-sprint card stitch --id <parent|stitch> [--write]` prints the plan's line, one line per child and the brief as the records hold it now; `--write` stores it onto the stitch's body (a refresh after a late read). Receipt: `PLAN id=<parent> state=<s> children=<n> stitch=<id>:<where> written=<0|1> ms=<ms>`.

A plan lands with its stitch by every door (`TK.land_parent` in the one move: the stream landing, `task land --id <stitch>`, a verdict's drop), from waiting or ready; a plan is never moved to ready (refused by name; the resolver skips it). A cancelled child leaves the plan `stuck` (its edge is never met); the brief and `card stitch` name the remedy, `nova-sprint card stitch --drop <child> [--as <by>]`, which drops a done child from the parent's children and the stitch's edges through the one move and rewrites the brief (`STITCH DROP parent=<p> child=<c> children=<n> state=<s>`); a live, landed or plan-less child is refused by name. `task cancel --id <plan>` cascades: the live children and the stitch are cancelled first (why `plan <id> cancelled: <why>`), then the plan; a child in flight (working, review, merging) refuses the whole cancel by name before any write, and a hand move of a plan to done while children are live is refused by the one writer.

A plan never sits in a state with no way on (the fix of the #4317 cold read). A stitch that ends done on its own (`task cancel --id <stitch>`, `task done` with no PR, a hand move) or loses its record leaves the plan `stuck`, never `waiting`: `Plan.State` derives it from the stitch and `Plan.Remedy` names `nova-sprint card cut --parent <plan>`, which, with no `--from`, re-cuts the stitch as `<plan>-stitch-2` (then `-3` ...) DEPENDS-ON every child the plan has, points the parent's `stitch` and `blocked_on` at it through the one move and leaves the old record done; with `--from` it re-cuts the stitch behind the new children too. `task cancel` of a child or a stitch, and `task done` of one with no PR, that leaves its plan stuck prints `PLAN id=<plan> state=stuck remedy="<the way on>"` after its receipt. The re-cut stitch's issue is filed through its own cut ledger, `taskcard.RecutLedgerKey(<plan>, <stitch>)`, never the file's (a bare re-cut has none), so no two re-cuts share an issue and a plan on any repo re-cuts. A bare `card cut --parent <plan>` whose stitch still waits is refused (`no children rows`), and one whose stitch is in flight is refused naming it (the plan lands when it lands). A stitch landed by `task land --id <stitch>` names the plan it landed on its receipt, `TASK land id=<stitch> from=merging to=landed parent=<plan> ref=<repo#n> origin=<url> ms=<ms>` (the `ns_tcard_move` reply's `parent= ref= origin=`), so the lander closes the plan's issue as it does from a stream landing's `LANDED` line. `land merge` names it too, one `LAND MERGE PLAN parent=<plan> ref=<repo#n> origin=<url>` per plan a member's stitch landed (the `ns_land_member` note `PLAN ...`), and so do `read post` and `line post` of a CLOSE line (`READ POST PLAN` and `LINE POST PLAN`). A CLOSE line finds the stitch by its PR number whichever door wrote the stitch's `pr`: every writer of a task's `pr` field (`task done --pr`, `task move` or `task push` with a pr, a copy's `card end --ok --pr`) indexes it under `ref:<repo>#<n>:tasks` in the same call, and a CLOSE lands a stitch in `review` (a copy's `card end --ok --pr` put it there) as well as one in `merging`. A filing refusal of a row (the forge refused, the ledger write failed, or the filing stopped at an earlier row) carries `remedy=`: rerun the same cut, since its ledger (a re-cut stitch's own) holds every issue filed.

### The card model: `nova-sprint card fsck`, `card ls --unplaced`, `bench reindex`

ONE PLACE (nova-tools#3692). Glenn: "cards are not allowed to disappear." A card is its record `s:<S>:card:<label>` (the card id), never deleted, listed forever in `sprint:<S>:cards`. Its `where` names its one place (`waiting`, `ready`, `working`, `done`, `parked`, or empty: null, in no table set), `where_ok` is `ok` or `fail` once done. The places are ZSETs of card ids, every score the card's `created_at`: `bench:<b>:cards:<where>` (plus `:ok` and `:fail`; `_pool` while the card has no bench), `ws:<stream>:<where>` for a card with a `STREAM:` line, `friend:<owner>:cards:<where>` for a card a friend holds, and the dealer's lists: `s:<S>:pool` (ready) and `s:<S>:waiting`, each named at epoch 0 as here and with the sprint epoch after a clear (`ws:<e>:...`, `<kind>:<name>:<e>:cards:...`, `s:<S>:<e>:...`; see `sprint clear`). Every ZSET, the pool included, is scored by `created_at`, uniformly, so every list reads oldest first; the deal priority is the record's `priority` field (lower deals first), and the dealer reads the pool by age and deals by that field, age breaking ties. A count the host table could not read prints `?`, never 0. One Lua primitive (`internal/nsprint/fn/lua/02_card_move.lua`) is the only writer of the pointer and the sets, in one call; the host table's ready, working, done, ok and fail are those ZCARDs.

- `nova-sprint card fsck --sprint <S> --redis <addr> [--repair]` walks both directions (every card in exactly one place per dimension at its created_at score, every set member pointing back, the places summing to the roster) and prints one `CARD FSCK sprint=<S> family=retired cards=<n> ...` line (its counts are the s:<S>:card records of `sprint:<S>:cards`, labelled so they never read as the sprint's progress, #4411); exit 1 names `--repair`.
- `nova-sprint bench reindex --sprint <S> --redis <addr>` is the one-time rebuild for a sprint whose cards predate the model: it adopts them from the sprint's state indexes and fills every view.
- `nova-sprint card ls --unplaced --sprint <S> --redis <addr>` lists the null cards.

### `nova-sprint digest`

`nova-sprint digest --redis <host:port> --since <RFC3339 UTC> [--until <RFC3339 UTC>] [--repo <owner/name>]...` prints what landed, which holds were routed and which reads were scored in the window `[since, until)` (nova-tools#3158). It reads three Redis sources and nothing else: `ws:log` (every move receipt), `land:<repo>:events` (the unit lander's `LANDED` events) and the `pr:<name>:<n>` records (the stream PR's `merge_sha`, the typed lines in `reads`). No GitHub, no model, no SCAN: `ws:log` is read by id range with `COUNT 1000` pages, the event streams are each `--repo` plus every repo a landing in the window names, and the PR records are the ones the window's receipts name, so a digest is two round trips plus one per further page. `--until` defaults to now; an entry at `since` is in, one at `until` is out.

- `landed` lines: a stream landing (the `land:<repo>:<slug>` receipt to `landed`) with its PR, merge sha, members and the tasks landed with it; a unit-lander batch (`LANDED` event) with its base, batch and train head; the tasks a person's `CLOSE` line landed.
- `hold` lines: a hold routed to its answerer (a `route pr fix|close|recut` task created), the holder from the record's `HOLD` line at that head, `state=answered` once the holder has a later `SCORE` on the record, else `open` (`?` with no record or no HOLD line).
- `read` lines: a read scored (`read: SCORE by <who> at <head8>`), the score from the reader's `SCORE` line at that head (`?` when the record has none).
- A stream that lost entries of the window prints `<key> TRIMMED source <stream> max-deleted=<id>` (an XDEL at or after since) or `first=<id>` (trimmed off the front past since) right after the section header, and that section never prints `none`. A section with no facts prints `<key> none`.

First run, on the fixture of `internal/nsprint/digest/testdata/digest`:

```text
nova-sprint digest --redis 127.0.0.1:6379 --since 2026-09-23T00:00:00Z --until 2026-09-23T04:20:00Z
digest since=2026-09-23T00:00:00.000Z until=2026-09-23T04:20:00.000Z repos=mas-bandwidth/nova-tools
landed:
landed at=2026-09-23T00:00:00.000Z repo=nova-tools pr=3901 merge_sha=b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0 members=2 tasks=2 by=rowan
landed at=2026-09-23T00:00:05.000Z close=rowan-tools#410 by=glenn tasks=1
landed at=2026-09-23T00:00:07.000Z repo=nova-tools base=dev batch=b7 train_head=7777777777777777777777777777777777777777
holds:
hold at=2026-09-23T00:00:03.000Z repo=nova-tools pr=3850 head=cccccccc holder=emma route=fix state=answered
hold at=2026-09-23T00:00:06.000Z repo=rowan-tools pr=411 head=eeeeeeee holder=johnny route=close state=open
reads:
read at=2026-09-23T00:00:04.000Z repo=nova-tools pr=3850 head=dddddddd who=stella score=9
```

The first stumble is a missing `--redis`; every refusal is one line on stderr, exit 2, before any read:

```text
nova-sprint digest --since 2026-09-23T00:00:00Z
nova-sprint digest: wants --redis <host:port>; run: nova-sprint help
```

The other refusals name their remedy the same way: `wants --since <RFC3339 UTC>`, `wants --until <RFC3339 UTC>`, `since must be before until`, `takes flags, not positional arguments`, a `--repo` that is not `<owner>/<name>` (the parser's `invalid value` line), and `redis <addr>: <error>` when the store cannot be reached. A read that fails after that exits 1.

## nova-post

Prepares outward messages for Ghost, Bluesky, email or Discord. `draft` saves the
payload, `show` displays those saved bytes, and `send` checks the approval receipt
before contacting the provider. See [SPEC-OUTBOUND.md](SPEC-OUTBOUND.md).

```sh
mkdir -p ./drafts && printf 'email\tteam\n' > ./targets.tsv && printf 'a first post for the first run.\n' > ./message.md
nova-post draft --channel email --target team --file ./message.md --drafts ./drafts --allowlist ./targets.tsv
nova-post show --draft <hash-from-draft> --drafts ./drafts
```

Create the draft directory first. The allowlist contains one `channel<TAB>target`
per line; `team` above must be an explicitly allowed target. Optional `--title`
sets the title or subject. The draft's hash identifies the exact content.

`send` requires `--draft`, `--drafts`, `--allowlist`, `--bus` and `--approval`.
The shipped approval gate requires a bus note from Glenn carrying
`APPROVE nova-post sha256=<hash>`, received less than 24 hours ago. It does not
expose a flag for choosing another approver. Provider credentials are supplied
through the child environment. Drafting and showing do not authorize a send.
