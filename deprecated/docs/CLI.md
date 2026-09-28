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

## nova-swarm

> **Retired 2026-09-24 (verb survey, del-swarm-ci-leftovers).** `add`, `run`, `supervise`, `requeue`, `verdict`, `cost`, `note`, `reclaim`, `bench`, `reap`, `publish`, `pull`, `pull-lanes` and `result-lint` are deleted: nothing called them, and card work runs through `nova-sprint card launch` and `nova-card`. The live surface is `slots`, `native`, `lint` and `batch`. Prose below that describes a deleted verb is historical until this section is rewritten.


```
nova-swarm batch    --pool <dir> --tasks <dir> --files <n> --tokens <n>|unmetered [--max-input <bytes>]           # queue a directory of them under one batch id
nova-swarm status   --pool <dir> [--max <n>]                                                # what is pending, running, done, failed, and how many slots are quarantined
nova-swarm triage   --pool <dir> [--batch <id>] [--max <n>]                                 # one page, and one TRIAGE BATCH line to read a batch down by
nova-swarm result   --pool <dir> --id <job>                                                 # one report, verbatim: the only path a malformed one takes to a person
nova-swarm template --name read-pr|probe-row|fix-card|result|worker|setup|capacity|read|fix|text|replay|drift|tone|models.tsv   # the conditions, the forms, and the pulse card templates, baked in, so they are not retyped and not forgotten; setup is #184's agreement form and capacity is #176's offer-and-routing form, neither is a task template
nova-swarm stop     --pool <dir>                                                            # stop new admissions; drain workers already running — never kill them
nova-swarm lint     --card <file> [--typed] [--trust <file>] [--lineup <file>] [--base-check [--repo <dir>] [--legs <file>] [--p95 <file>]] [--max <n>] | --fleet <script> | --rules          # one card's mechanical shape, before any spend: no model, no probe, one file
```

### The card lint

`lint --card <file>` reads the one file it was handed and names every mechanical
defect by check, line and excerpt **before a token is spent**. No model, no probe,
no network. The checks are `docs/WORKER-CARDS.md`'s rule table and the four typed
header tokens of `docs/SPEC-TOOLWORK.md` §5 rule 1.

| flag | what it does |
| --- | --- |
| `--card <file>` | the card to read. Required unless `--rules` is given |
| `--rules` | print `LINT RULE <check> remedy=<what it wants>` for every check and exit 0. It takes no card, because the question is asked before there is one, and it is the one listing a bench with a clone months behind its binary can still read (#1464) |
| `--typed` | apply the typed-header tokens to a card that declares **no** typed line at all. Without it a card with no header is left to the older rules, which is what every card written before §5 is. Under `--typed` the card also carries `DEPENDS-ON: <card-id>[, ...]` or `DEPENDS-ON: -` (#2636) |
| `--trust <file>` | a file of `TRUST kind=<kind> … state=<trial\|trusted\|paused>` lines in the shape `nova-pulse trust` prints; it is what the `paused` token reads. With no file there is no paused kind and the lint says nothing rather than guessing |
| `--lineup <file>` | the lineup `depends-on` checks ids against, and only together with `--typed`. One card id per line, or a TSV whose id column is named `id`, `card`, `card-id` or `label` — otherwise the first column — and a header row that names `depends-on` is not a card. With no file an id is not called unknown |
| `--max <n>` | bound the printed drifts, default 20, `0` for all. Over the bound it adds one `LINT MORE` line naming the remedy; it never changes the verdict |

Output, and what a caller does with it:

```
LINT OK    card=<name> checks=<n> bytes=<n> cap=<n>                      # exit 0: admitted to the wall
LINT DRIFT card=<name> <check>: <line>: <excerpt> remedy=<what it wants> # exit 2: a caller refuses to admit it
LINT NOTE  card=<name> <check>: <line>: <excerpt> remedy=<…>             # advice; it changes NO verdict
LINT MORE  card=<name> findings=<n> remedy=<…>                           # more drifts than --max printed
LINT SIZE  card=<name> bytes=<n> cap=<n> advisory=true                   # every drifting card's size, and that the cap is advice
LINT NOT-A-CARD card=<name> template=<name> remedy=<…>                   # exit 1: a shipped template piped in, answered by name
```

**`DRIFT` is a defect and `NOTE` is advice.** The 12000-byte ceiling is the only
advisory check today: it is a reading budget, not an input limit, so a card over
it is never refused and never truncated, draws a `LINT NOTE`, and exits 0
(#1494, #1527). A card whose only findings are advisory is a clean card.

**Every drift names its remedy on the same line** (#1464), and the `DRIFT` line and
the `--rules` listing read one table, so a remedy cannot drift from the rule it
explains. The rule tokens and what each wants are in `docs/WORKER-CARDS.md`; the
`../` rule and the ceiling are written out in `docs/SPEC-SWARM.md`'s lint section.

### native and batch

routes: see docs/MODELS.md

**Routing is the launcher's default: the ladder chooses the model, not the TSV.** `batch --cards` reads `label<TAB>slot<TAB>model<TAB>card-path`, and that `model` column used to be the last word — a string a fill script wrote by hand. The batch now asks the ladder one typed decision per card, in process, **before the card is assigned a model**: which mind does this unit of work. The answer's rung names the model id, from the registry; the TSV's model becomes the **fallback**, which is exactly today's behaviour (SPEC-DECIDE rule 5). Routing is a launcher step and not a line in a brief (#1625).

```
nova-swarm batch --cards <tsv> [--route-log <path>] [--route-usage <path>]
                 [--route-registry <path>] [--route-floor 0.9]
                 [--route-key-env JEV_API_KEY] [--route-base-url <url>]
                 [--no-route --reason <text>]
```

A launcher that names no accounting home does not skip the route: the rules answer, no call is made, and the receipt says `why=no-accounting`. `--route-log` and `--route-usage` are where the decision's records go when they are named; accounting is not optional, and a call nobody can account for is not made, but the card is still routed. **The one way out is `--no-route --reason <text>`**, which writes a `"source":"skipped"` row carrying the reason to the route log, so a skipped route is a fact in the log and not an absence. Every routed card carries one receipt line, said on stderr in TSV order and written into the card's job directory as `route.txt`:

```
ROUTE card-742 ROUTE jev=flash conf=0.94 rung=flash model=opencode/deepseek-v4-flash why=-
ROUTE card-743 ROUTE jev=fallback conf=0.61 rung=pro model=opencode/deepseek-v4-flash why=below-floor
ROUTE card-744 ROUTE jev=fallback conf=0.90 rung=opus model=opencode/deepseek-v4-flash why=rung-is-asked-not-run
```

`jev=` is the rung where the answer chose the model and the literal `fallback` where today's model stands; `rung=` always names what the ladder answered, so a fallback never hides the rung; `why=` is one enumerated token — `no-key`, `no-accounting`, `below-floor`, `refused`, `rung-is-asked-not-run`, `card-names-no-kind`, `no-ladder`. The third line above is the one worth reading in the log: the ladder says that card is judgment work owed to a child or a friend, and the batch ran it on a mechanical model because dispatching a card is the only thing a batch can do. That is evidence for the escalation log, not a silent success.

**The card's own evidence.** The ladder wants a kind, a size, a lane, a platform need and what security is touched, and it reads them from the card's own text — never from a model, and never from anything outside the card. A card may state them outright, one `FIELD: value` line anywhere in its text, and a fill script should write them from now on:

```
KIND: fix-with-red-test
FILES: 4
PACKAGES: 1
LANES: 1
LANE: code
PLATFORM: windows
TOUCHES: sandbox
```

Where the card names no `KIND:`, the kind is read from its contract line by a deterministic table of phrases — security phrases first, so security never falls through to a cheaper reading of the same line. A card whose kind cannot be read is **not routed at all**: no evidence is no decision, its line says `why=card-names-no-kind`, and today's model stands. What reaches the provider is never the card: it is the bucketed public projection of the unit and nothing else (SPEC-DECIDE rule 4).

**Two routes, two questions — do not point both at the same column.** `internal/swarm/route.go` (`nova-swarm route`, `nova-pulse launch --routes`) asks what KIND of work a card's text is and picks the **worker description** for it from a routes table, writing that path into the cards TSV's model column. `--route` here asks which MIND does the unit — over the registry ladder, with the escalation policy on it — and names that mind's **model id**. One chooses the harness a card runs under; the other chooses who does the work, and neither answer is the other's. They write the same column, so a pulse that already rewrote it with `--routes` should run `nova-swarm batch` with `--no-route --reason <text>` (the skip is logged), and a batch that routes by the ladder should be handed a TSV carrying model ids.

**No key is no call.** An absent key is said once, by the name of the variable and never by its value, and the batch runs on today's models — so the loop runs on a bench with no API at all.

**A card that fails its gate re-enters one rung up.** The ladder is the retry policy: a confirmed failure is appended to the unit as evidence, and the rung that failed — and its lineage at that height — is out of the eligible set, so the answer is another lineage on the same rung where there is one (sideways before up) and the rung above where there is not. It is never a retry on the rung that just failed.

**The free tier that queues forever: `--max-inflight` and `--stall-after` (#917).** Both default to **0, which is off**, and a batch that names neither behaves exactly as it does today.

```
nova-swarm batch --cards <tsv> ... [--max-inflight <n>] [--stall-after <seconds>]
```

`--max-inflight <n>` caps how many of the batch's cards run against **one route** at a time, where a route is the **provider, the model and the key** — two models on one key share that key's queue and the same model on two keys do not, so neither alone is the unit. The key is named by its **auth profile** (the `--auth` file), never by its value: this string is printed. Cards past the cap **wait** — they hold no process, no bench slot lease and no spend, and their deadlines have not begun. When the batch's own deadline passes, every card still waiting is released unlaunched and scored `deadline`.

With a cap set, one line per route follows the BATCH line:

```
BATCH ROUTE <model>@<auth-profile> cap=<n> peak=<n> held-back=<n>
```

`peak` is the most ever in flight on that route and `held-back` is how many launches had to wait for a slot; a route whose `peak` is under the cap and whose `held-back` is `0` never was the constraint. With no cap no such line is printed.

`--stall-after <seconds>` is the **first-token** deadline and is **not** `--idle`. Every signal the idle window has needs a first sample to compare against, so a card that never speaks once is invisible to it and burns its whole deadline. A card that has produced **nothing at all** since it launched is ended at `--stall-after` and scored `ABSTAIN reason=stalled`; a card that spoke once and went quiet is `--idle`'s business and this never fires for it, and a card burning CPU in silence has moved and is not stalled (#593).

Measured 2026-09-17: above roughly 30–40 concurrent requests on one Muse contributor-free key the tail latency goes to infinity — hulk and vision returned zero results in thirteen minutes at load 0.5–2.0 — while `deepseek-flash` on the same bench in the same second answered in 11 s. A launcher with no cap turns a free tier's queue into spend.

**`native` takes no bench slot lease (#3877).** A bench's capacity is one number,
`bench:<b>:desired` in Redis, and the one place a card is admitted or refused against it
is the dealer: a card beyond it stays queued and nothing is written on the bench. `native`
reads no slot store and writes none, so a bench with no `~/nova-bench/slots` runs a dealt
card. The file ledger it used to lease from (#1546, #2033) was a second answer to the same
question: on 2026-09-25 it refused seven dealt cards on batman with
`SLOTS REFUSED owner=swarm-batman want=4 held=16 share=16` while Redis said the bench had
room. `--slots-store` and `--owner` are still accepted, so a caller built before #3877 is
not refused on an unknown flag, and they are read by nothing.

`batch` without a `--runner` of its own still asks for the two flags and refuses the
whole batch without them; the store is no longer leased from by the `native` it launches.

**The release is by identity.** `native` and `run` keep the lease ids `TakeSlotLeases`
granted them and give back exactly those, pid-fenced. Releasing by owner and label would
mean that two runs sharing a bench and a card name — or two dispatchers sharing an owner
and a task id — each give away the other's live seat, and that a run refusing before it
started — a missing harness, say — deletes a lease it never took (Stella, on #1562;
dispatcher, #1582). `nova-swarm slots release --owner … --label …` keeps the
by-owner-and-label behaviour, because that is what a person at a prompt means by it.

**A release never frees a seat whose holder is still running (issue #1902).** Deleting a
lease does not stop the process holding it: the holder keeps running and the seat it is
sitting in is handed to the next taker, so two cards end up on a one-seat bench. A lease
whose pid is alive and is not this process is kept, counted in the `live=` field of the
`SLOTS RELEASED` line, and named on stderr as `SLOTS KEPT`, and the verb exits 2. Giving
back your OWN seat is always allowed — that is how `run` and `native` end. `--force` is
the loud override for a person who knows something the store cannot.

`nova-swarm slots release --store <dir> --owner <o> (--label <text> | --all) [--force]`

`--force` frees a lease whose holder is still running, which **oversubscribes the bench**:
the holder keeps its seat in fact while the store hands the same seat to the next taker.
It is an operator's act, typed at a prompt by someone who knows what the store cannot see
— a holder on another host, a pid the kernel has since handed to somebody else. No card
carries it, and no manager, launcher or cleanup path passes it by default.


**`native` carries a token budget, and the word is required (rule 13d, #1545).**
`--tokens <n>` or `--tokens unmetered` on **every** launch. Without it the verb is exit 2
naming the flag and makes no directory; `--tokens 0` is refused. `unmetered` is the
caller's statement that this provider has no live accounting and the deadline is the only
stop, and it is printed on the line:

```
NATIVE OK label=card-a job=… harness=ok budget=unmetered
NATIVE OK label=card-a job=… harness=ok budget=-/200000
```

`budget=` always follows `harness=`. It is `unmetered`, or the number with what was
observed against it: `<spent>/<n>`, `<spent>+/<n>` when some token column was a dash, and
`-/<n>` when nothing was observed at all — so a card that ran under an unobservable budget
is visible as such and is never reported as under budget.

**There is no test for a provider that costs money.** A provider's name is whatever a
config file says it is, a `baseURL` can point a local-looking name at a metered endpoint,
and a reported `0` is a measurement, not a licence. So the caller says a number or says
`unmetered`, every time, and the tool infers neither.

**A budget needs a source the tool can read, and that is checked before anything is made.**
A native card's usage source is its worker description's `usage`, and `opencode` when there
is no `--worker`. A numeric `--tokens` beside `usage: none`, or on a bench with no
`sqlite3` on `PATH`, is `NATIVE REFUSED` at exit 2 **before any directory is made** — for
rule 13's own reason: a budget nothing can observe is a promise the tool cannot keep. The
same refusal meets a description that sets `max_cache_read` or `max_turns` under either
condition, **whatever `--tokens` says**, because the card's own budget is read from the
same source; so `--tokens unmetered` beside a `max_turns` on a `usage: none` bench is
refused too. `--tokens unmetered` with no such description runs under both, as it does
today.

**`--usage-interval <s>`** is how often a live sample reads that source, default 5, the
same flag `run` takes. On `native` an interval **under one second**, or one **not shorter
than `--deadline`**, is exit 2: under the first, three quick failed reads would end an
honest card `budget-unverifiable`; under the second no sample would ever run.

**`native` samples in its own process.** No supervisor is spawned. While a launch runs,
`native` reads the harness's own database under the job's data home — at both spellings,
`$XDG_DATA_HOME/opencode/opencode.db` and the `$HOME/.local/share/opencode/opencode.db` a
Linux harness derives from `HOME` — read-only, every `--usage-interval`. It **never** reads
`usage.tsv`: that row is written after a launch's process group is dead, so it is the
record of a stop and cannot be the cause of one.

A sample gets **5 seconds whatever the interval**, **waits for no checkpoint** (rule 13's
five-second wait for a write-ahead log belongs to the *final* read, made when the harness is
gone), never overlaps another, and is never in the deadline's way: a read still unanswered
at its limit is abandoned and counted as a failed read, and the deadline and a TERM from
outside end the card at their own instants whatever a read is doing.

**The observed sum is `tokens_in + tokens_out + reasoning`.** `cache_write` and `cache_read`
stand in the usage row and are never in the sum, because a card re-reads about thirty times
what it sends and a budget that counted them would measure the harness's re-reading.

**The figure on the line is the job's, at the final read.** It is the sum over every launch
of this one invocation of `native`, taken from each launch's own final read once its group
is dead — never the sum at a sample. Where no final read could be made at all, the last sum
a sample saw stands in **with the plus**, because a sample's figure is never allowed to pass
for a final one.

**The budget is the job's, across every launch, and a stop is the end.** A native job is one
invocation of `native`: up to three launches when a provider 5xx inside the launch grace
retries it, one data home, one usage row per launch. The sum is over the whole job, every
launch counted from the first launch's start, and the stop is `spent >= n` — tested at every
sample and **once more before any relaunch**, so a first launch that reached the budget
alone is never launched again.

When it fires, `native` ends the card the way it ends one on a TERM from outside: a
terminate to the whole process group, a wait, then a kill, after which no process of that
group is alive — grandchildren and a harness that ignores the terminate included. What the
card published stays where it is, byte for byte, and on this route the tool writes nothing
into a report. Nothing is launched again, by `native` or by a batch. `native` exits **1**: it
ran, and the answer is no.

**The line says what stopped the card**, in a key of its own:

```
NATIVE OK label=card-a … rc=-1 … harness=ok budget=110/100 stopped=tokens
```

`stopped=<tokens|max_turns|max_cache_read|unverifiable>`. `reason=terminated` stays what a
TERM from outside prints, and the `reason=` inside the `usage=none` group stays the usage
read's.

**Two numbers, kept apart: the row is the launch's and the line is the job's.** Each usage
row carries what THAT LAUNCH was finally reported to have used, from its own start to its
own end, and never the job's running sum — a job's rows are disjoint, so adding them counts
each launch once. Two launches finally reported at 40 and 70 under `--tokens 100` print
`budget=110/100` on the line, and their rows hold **40 and 70**, never 40 and 110. The
stopping launch's row carries `end=budget`, and its `rc` is a dash there while the line
prints `rc=-1`.

**When the source cannot be read**, the three cases stay apart. *Nothing observed*: the
budget cannot fire, the deadline ends the card, the line prints `budget=-/<n>`. *A partial
observation* counts the columns it has; it can reach the budget and stop the card, and it
can never show that the card stayed under it, which is what the plus says. *A read that
fails* — the database unreadable, the query erroring, the read abandoned at its limit — on
**three consecutive** samples ends the card with `end=budget-unverifiable`,
`stopped=unverifiable` and exit 1; two failures and then an answer end nothing.

**The card's own budget comes along.** `--worker` naming a description with `max_cache_read`
or `max_turns` has them enforced by the same samples, over the whole job, with
`<job>/harness-output.log` as the turn log (`native` never writes `harness.log`). The stop
is the one above, with `end=budget` in the row and `stopped=max_turns` or
`stopped=max_cache_read` on the line. The `PROMPT-DEFECT` line is printed on `native`'s own
**stdout, after `NATIVE OK`**, and is written into **no file**: the card's `RESULT.md` is the
card's.

**Every caller passes the word along.** `batch --cards` takes `--tokens <n>|unmetered`,
required, and refuses the whole batch before any card starts:

```
BATCH REFUSED reason=no_tokens: --tokens is required; it wants a token budget for EACH card in this batch, or the word `unmetered` when this provider has no live accounting and the deadline is the only stop; it is never divided among the cards and never a total for the batch; refusing to guess
```

It is **each card's own budget** — the same word for every card, never divided among them
and never a total for the batch — and the batch puts it verbatim into every `native` argv
it builds, the local one under `--harness` and the remote one over ssh. A `--runner` is
handed it as a **sixth** argument after the five it already gets (label, slot, model, card
path, root), and a runner that reaches `native` without passing it on meets `native`'s own
refusal.

`native --config` copies the named `opencode.json` into the job's data home. Only the
provider `--model` names is checked against `--auth`; a provider whose options carry
`baseURL` and no `apiKey` (ollama on localhost) needs no key and is admitted without one.

**The deadline ends the whole tree, and a TERM is the same cleanup (issue #779).** The
harness runs as the leader of its own process group, so at `--deadline` the run kills the
entire tree the card started — grandchildren included, never just the leader — and writes
`usage.tsv` from what it had up to the kill, so the spend is known. A `SIGTERM` from outside
(the manager) is handled the same way: the tree is reaped, `usage.tsv` is written, and the
`NATIVE OK` line carries `reason=terminated` instead of a silent exit.

**A launch that started always prints a verdict, and `native` never exits 255 (#2058).**
The three words are `NATIVE OK`, `NATIVE INCOMPLETE` and `NATIVE REFUSED`. A darwin
OpenCode that logged `Error starting FSEvents stream`, wrote `RESULT.md` and exited 255
prints exactly one `NATIVE INCOMPLETE` with `rc=255` and `why=rc`, never OK or REFUSED.
Passing 255 through made a fill loop retry a finished card. Local ssh(1) exits 255 for
any error; that is not proof the remote command never started, so the outcome is
potentially UNKNOWN and a retry waits on reconciliation. The process exits 1.

### First run

`quickstart` needs nothing but a directory: it makes the pool's structure and names the
three commands that follow. `./pool` is a directory of yours; the tests run every line below
against one they make in `t.TempDir()`.

```
$ nova-swarm quickstart --pool ./pool
QUICKSTART OK pool=./pool pending=0 next=add,run,triage
QUICKSTART NOTE a task is a file: nova-swarm add --pool ./pool --task <file> --files <n> --tokens <n>
QUICKSTART NOTE a worker description says whose model runs: nova-swarm run --pool ./pool --workers <n> --hours <h> --worker <file>
QUICKSTART NOTE the conditions are worth more than the model: nova-swarm template --name read-pr

$ nova-swarm status --pool ./pool --max 20
STATUS OK pending=0 running=0 done=0 failed=0 slots=0/0 quarantined=0
```

**`stop` holds the pool still without killing anyone.** It writes a `stop` file that
stops new admissions while workers already running finish under their own deadline. A
`run` that ends over a stopped pool names the stop in its `RUN NOTE` remedy rather than
guessing a second run would help — `the pool is stopped; <n> task(s) stay pending until the
stop file is removed` when work remains, or `the pool is stopped and drained; remove the
stop file to resume` when it does not. Remove `stop` to admit again; the stop survives a
dispatcher's death and the next `run`'s recovery (issue #180).

**`run` needs `nova-sandbox` before it needs anything else.** Every job runs inside it
(docs/SPEC-SANDBOX.md): the job directory and its data home are the only writable paths, the
worker home and whatever `read_roots` names are readable, and the key file, `~/.ssh` and the
`gh` configuration are outside both lists and unreadable to the worker. `run` proves the
wall ONCE, before the first worker, and refuses the pass if it cannot:

```
$ go build -o ~/bin/nova-sandbox ./cmd/nova-sandbox      # or name it with --sandbox <path>
$ nova-swarm run --pool ./pool --workers 4 --hours 2 --worker ./worker.json
RUN POOL workers=4 hours=2 worker=deepseek-1 model=deepseek/deepseek-chat auto_retry=true pool=./pool
RUN START id=20260912T0141Z-task-1a2b3c slot=1 pid=41321 pgid=41321 started=2026-09-12T01:41:07Z deadline=20m tokens=100000 job=/home/you/worker-1/jobs/20260912T0141Z-task-1a2b3c
```

A machine with no backend, or a wall that fails a check, starts no worker at all:

```
$ nova-swarm run --pool ./pool --workers 4 --hours 2 --worker ./worker.json
RUN POOL workers=4 hours=2 worker=deepseek-1 model=deepseek/deepseek-chat auto_retry=true pool=./pool
RUN REFUSED reason=no_sandbox: this machine has no sandbox backend, and a job this tool cannot contain does not run: CHECK OK backend=none abi=- net=unenforceable note=the linux body of docs/SPEC-SANDBOX.md is not built yet. The one workaround is `--no-sandbox`, which runs every job with no OS containment and says so once per job
```

`--no-sandbox` is that workaround and nothing else is: no environment variable and no file
turns the wall off, and a pass that takes it says so once per job, on stderr, before the job
starts — `RUN UNSANDBOXED id=<id> slot=<n>: no OS containment; every read and write this job
makes is yours`.

**The one input `run` cannot proceed without** is the worker description, and every field
below is required. This one ran two real DeepSeek workers end to end on 2026-09-11:

```json
{
  "name": "deepseek-1",
  "provider": "deepseek",
  "model": "deepseek/deepseek-chat",
  "env_var": "DEEPSEEK_API_KEY",
  "key_file": "/home/you/.keys/deepseek",
  "usage": "opencode",
  "harness": "opencode",
  "harness_args": ["run", "--model", "{model}", "--title", "nova-swarm", "--", "{prompt}"],
  "worker_dir": "/home/you/worker",
  "deadline": "20m",
  "read_roots": ["/home/you/toolchains"]
}
```

`read_roots` and `input_limit_phrases` are the optional fields. `read_roots` is the wall's: a toolchain installed under a
user directory — Go under `~/go`, node under `~/.nvm`, the Studio's `/Users/<you>/toolchains`
— is under no system root, so a harness that needs one runs outside the wall and dies inside
it. Name those directories here and they are READ-ONLY for every job of this worker, named
once so that N workers read one copy. A worker that needs nothing beyond the system roots
names nothing, and an absolute directory that does not exist is refused when the description
is read, not at every launch.

`input_limit_phrases` teaches this provider's own way of saying *your request did not fit*:
a job that dies on an input limit is its own failure class, `input-limit`, never a 429 to
retry, and the phrases the tool already knows (OpenCode's `input token limit exceeded`,
Anthropic's `prompt is too long`, OpenAI's `maximum context length`) are a table this field
ADDS to. A phrase counts only on the provider's own error line — a line whose own LABEL is an
`error`, `fatal` or `exception` mark (the mark begins a word, at most two tokens before it and
at most one of those a bare word, no list marker at the head of the line, no quote character
before it), or the line directly under one, so a report that merely quotes the sentence beside
the word is not one, and a line these tools wrote themselves — `RUN REFUSED …`, `SANDBOX OK …`
— is skipped whole, since a job that runs them logs them, unless its second word is itself a
mark, which no line of theirs has (a test over the sources keeps that true) and a shouting
proxy does (`HTTP ERROR: 400 …`) — and a phrase you name must be a sentence, twelve characters
with a space or a digit in it, refused when the description is read: a job classed this way is
never retried, so a bare word here would take the retry away from every failed job whose log
happens to carry it. A task may name the other half, `--max-input <bytes>`, and `run` refuses
a prompt over it before the launch.

`key_file` lives **outside `worker_dir` and outside every `read_roots` entry**, which is why
the example keeps it in `~/.keys`. `worker_dir` is copied into the slot directory before
every job and the slot directory is the job's one readable path, so a key file inside it
would be copied INSIDE the wall and read by the worker under a green line; a key inside a
read root is readable without even the copy. The key reaches the harness by `env_var` and
its FILE is in neither list (docs/SPEC-SANDBOX.md rule 6). A key file inside a SLOT
directory — `<worker_dir>-1/.key`, or anything under it such as its `jobs/` — is the same
hole from the other side, since the slot IS the job's readable path, and it is refused too.
All three placements are refused when the description is read.

`harness_args` is the invocation the harness needs, and `{model}` is where the model goes:
a harness handed nothing but a path reads that path as a project directory and does
nothing, so a description that never places `{model}` is refused before any worker starts.
`{prompt}` is the prompt FILE, appended last where `harness_args` does not name it — the
task text is never an argument. `usage` names the token source for what it IS: `opencode`
is OpenCode's own `opencode/opencode.db`, in the job's own data home, read through
`sqlite3 -readonly` at every sample and once more when the job ends — so `sqlite3` is on
PATH or the source is one that cannot be read — and `none` is a harness that reports
nothing, under which only `--tokens unmetered` tasks may run. The name was not always true:
on 2026-09-11 `opencode` read a tab-separated file no OpenCode writes, and two real jobs
burned 61,875 and 85,308 tokens against `--tokens 20000` while both reported
`budget=-/20000`. A source that cannot be read is never a source reporting nothing: three
failed samples end the job `RUN BUDGET-UNVERIFIABLE`.

`nova-swarm template --name worker` prints this description with every field in it, so the
one file a first run cannot start without is the one file you do not have to invent.

`nova-swarm template --name setup` prints the per-friend safety-setup agreement form
(issue #184): the proposal half and the friend's own agreement half — a friend may agree,
propose an alternative, decline, or stay silent, and missing feedback is pending, never
assent — a guarantee table whose rows say who enforces each guarantee (the OS wall, a
cooperating harness, or the launcher outside the wall), and generic wall, fence, seat and
launcher examples with placeholder values only. It is a form, not a task's conditions:
`add --template setup` is refused the way `add --template result` is, no secret, key,
token or private path is ever printed by it, and an agreed form supplies no account
access — implementation, credential migration and deployment are separate staged work
with their own authorization.

`nova-swarm template --name capacity` prints the per-friend offered-capacity and routing-log
form (issue #176): the offer half with every field the issue names (expiry, friend, instance,
bench, model identity and basis, harness, supported task types, demonstrated strengths and
limits, permitted scope, current availability, concurrency, expected queue/latency and
shared-limit pools) and the coordinator's routing-log half (ready work, compatible offers,
incompatible offers, shared pool share, stale offers excluded, the pool-specific utilisation
denominator) plus the four rows acceptance evidence demands (an idle compatible pool receiving
ready work, an incompatible offer being skipped, shared capacity counted once, and a stale
offer excluded). Capacity kinds are kept apart — coordinator, direct worker, one-shot,
swarm and local — because model slots are not interchangeable throughput units and two
offers sharing a quota must be counted once. missing contact is unknown; stale capacity is
not proof of failure and not proof of consent, so an offer nobody answered since the
silent-ping window is excluded. It is a form, not a task's conditions: `add --template
capacity` is refused the way `add --template result` and `add --template setup` are, no
key, no token, and no private host detail is ever printed by it, and a filled form
supplies no account access — an automatic scheduler is separate staged work with its own
authorization.

`nova-swarm template --name read` (and `fix`, `text`, `replay`, `drift`, `tone` and
`models.tsv`) prints the six typed card templates SPEC-PULSE rule 4 names and the cost
table rule 7 reads, so the templates directory the deleted `nova-pulse cut --templates <dir>`
needed was built from the tool rather than copied out of the old command's testdata. `read`, `text` and
`tone` are text-only cards and carry rule 6's no-build line; `fix`, `replay` and `drift`
carry the red-then-green row. They are cards, not task templates: `add --template read`
is refused the way `add --template result` is.

### The harness contract

A harness is any program on `PATH` that can be handed a prompt file and left to work. This
is everything `nova-swarm` promises it, and everything it asks back:

- **Its working directory is the JOB directory**, `<worker_dir>-<n>/jobs/<id>`, and the
  SLOT directory above it — the one-way copy of your `worker_dir`, refreshed before every
  job — is readable from there. The cwd is the job directory because the wall is: a cwd
  outside every named path denies `getcwd(3)` and kills every `git` command before it reads
  anything, and a harness that evaluates its own `external_directory` permission relative to
  its cwd would call the job directory "external" to itself. Relative paths in a worker
  description are made absolute at load, so the child always gets paths it can open from
  where it stands.
- **It is contained by the operating system.** The job directory and its data home are
  writable; the slot directory and `read_roots` are readable; everything else on disk,
  including the key file it was given the VALUE of, is denied by the kernel. A refused read
  or write is not an error and does not end the run — the prompt says so — and a command
  that runs outside the wall and dies inside it is missing a `read_roots` entry. **Unless it
  lives directly in your home directory**: the directory of the resolved command is itself a
  read root, so the wall refuses `SANDBOX REFUSED reason=bad_read` at every launch rather
  than make the whole of `$HOME` — `.ssh`, `.config/gh`, the keychain — readable inside it.
  The remedy there is to move the harness into a directory of its own, `~/.local/bin/` being
  the usual one, and `read_roots` is no remedy at all if what you name is the bare home.
- **`HOME` is the job's own data home**, inside the write set, so the harness's own config
  and cache land in the job and not in yours.
- **Its arguments are `harness_args`**, with `{model}` replaced by the description's model,
  `{prompt}` by the path of the prompt file, and `{base_url}` by `base_url`. Where
  `harness_args` names no `{prompt}`, the prompt file is appended LAST. The task text is
  never an argument.
- **`NOVA_SWARM_JOB` is the job directory** — the only place the worker writes — and
  `XDG_DATA_HOME` is that job's own data home, so one job is one harness database.
  `PATH` is passed through; nothing else is inherited, and the key is in the child's
  environment under the name `env_var` gives and nowhere else.
- **It publishes `RESULT.md` in the job directory**, whole, by writing `RESULT.md.tmp` and
  renaming it: a report is a revision, and a half-written one is never read. `note` is a
  file in the same directory the worker may read between steps.
- **Its stdout and stderr are `<job>/harness.log`**, and what it said last is on the
  `RUN DONE` line of a job that published nothing or exited non-zero.

`cmd/nova-swarm/testdata/fakeharness` is a harness that does exactly this in about two
hundred lines of Go, and the whole test suite runs against it with no provider, no network
and no key worth anything. It is the shortest way to see the contract, and to test a pool
of your own before a real model touches it.

**Reading it.** Every line is `<VERB> OK`, `<VERB> REFUSED` or one of `run`'s own `RUN`
events; refusals and FAIL lines go to stderr. A job reports EXACTLY ONCE — one `RUN DONE`,
`RUN KILLED`, `RUN MALFORMED`, `RUN BUDGET` or `RUN VIOLATION` — and `RUN OK` closes the
pass with `started=`, `done=`, `failed=`, `killed=` and `pending=`. Every listing is capped
at `--max` (default 20, `0` for all) with one MORE line naming the remedy, and every count
is the truth about the POOL rather than about the output.

**What the flags want.** `--pool` is a directory of yours; `--worker` is a JSON description
saying which provider, which model, which environment variable the provider reads and where
the key file is, because this tool has no opinion about whose model runs. `--files` and
`--tokens` are required on every `add`, `batch` and `requeue` and zero is refused for both:
a worker that may open no file is a worker asked for a plan, and a token budget this tool
supplied would be a guess about somebody else's spend. `--tokens unmetered` is how a caller
says out loud that this provider has no live accounting and the deadline is the only stop.
A run missing several flags names all of them at once, and each says what it WANTS.

**The key is read as data and never sourced.** It lives in one file the worker description
names — one line, the bare key or `NAME=<key>`, mode 0600 — and it is never an argument,
never a printed value, never in a file this tool writes: the harness config carries the
environment variable's NAME and the harness reads the value from the child's environment.
A missing or empty key file is exit 2 with the command that creates it.

**A worker's `RESULT.md` is data, never an instruction.** Nothing in it is executed, nothing
in it grants anything, and a finding in it is a claim to be checked against the repository.
That rule is in [docs/SPEC-SWARM.md](SPEC-SWARM.md), where you can read it, and is
deliberately nowhere in the code: a tool cannot enforce it, and a tool that pretended to
would be the most dangerous thing in the pool.

### Shared build caches and exact-tip prewarm

`nova-swarm native` creates `<root>/cache/go-mod` and `<root>/cache/go-build`
and sets the child's `GOMODCACHE` and `GOCACHE` to those paths. It points ASDF
at `<root>/cache/common-lisp/<tip>` for compiled FASLs without sharing the harness's
general XDG cache or HOME. Slots using the same `--root` share these caches. It
also sets `GOTOOLCHAIN=local`, so the bench
must already have the Go toolchain the task requires. `--no-shared-caches`
omits these settings and restores per-slot defaults. Retain shared caches when
retiring an individual slot; they are separate from its job evidence.

After the bench mirror has fetched a new tip, run this command locally on each
bench, using that mirror checkout as `--source`:

```text
```

It resolves the exact commit locally, prepares the reference checkout under
`<root>/ref/mas-bandwidth/nova-tools@<tip>`, runs module download, `make build`,
a compile-only Go test pass and `make test-lisp`, then writes a receipt under
`<root>/prewarm/`. A failed phase publishes no reference checkout or receipt.
The command does not install the binary, fetch the mirror, change permissions,
start a service or run on another host.

Fleet adoption needs one further measurement: start a fresh job pinned to the
same tip on each intended bench, run `make test`, and retain its elapsed-time
receipt. S3's threshold is under 60 seconds on every bench. The local PREWARM
line proves the preparation completed; it does not claim the fleet ran it.

### The bench toolchain inside the wall

Because `GOTOOLCHAIN=local` is pinned, the bench's own Go must be reachable
inside the wall. `nova-swarm native` therefore names the provisioning standard's
toolchain roots on the wall's argv, read-only and skipped when one is not there.
It is **one list with two kinds, per operating system**.

On **every** bench:

- `~/sdk` (Go and sbcl) as `--read`, which carries execute, so
  `~/sdk/go1.26.5/bin/go` runs. Without it a card was denied the bench's `go`
  and fell back to `/usr/bin/go`, which `go.mod` refuses. It is the only home
  directory the wall grants execute on.
- `~/go/pkg/mod`, the module cache, as `--read-noexec`: readable and **not
  executable**. A card reads a dependency's sources out of it and never runs
  them, and the bench user can write to that tree, so execute there would put a
  dependency's own files one exec away from running inside the wall.

On a **Mac** bench the toolchains are installed and on `PATH` rather than
unpacked into a home, and each one finds its own runtime beside the launcher
that ran it — so inside the wall, without its tree, `go` says `cannot find GOROOT
directory: 'go' binary is trimmed`, `java` says `Unable to locate a Java Runtime`
and `dotnet` says `Failed to resolve full path of the current executable []`
(measured on the M2 Air, 2026-09-18). Darwin therefore also names, all as
`--read` because all of them are runtimes a card runs:

- `/opt/homebrew/Cellar/go` and `/opt/homebrew/Cellar/sbcl`, each narrowed to
  the one version directory this bench runs — read off the launcher, the way
  `readlink -f "$(command -v go)"` does, so a `brew upgrade` needs no edit here.
- `/opt/homebrew/opt/openjdk` and `/Library/Java/JavaVirtualMachines` for
  `java`, and `/usr/local/share/dotnet` for `dotnet`.

Each is skipped when it is not installed, and each reaches the argv resolved
through its symlinks, because the wall checks the resolved target — on the Air
`/opt/homebrew/opt/openjdk` resolves to `/opt/homebrew/Cellar/openjdk/27`.

One narrowing, measured: with the JDK tree granted, that JDK runs inside the
wall, but the `/usr/bin/java` **stub** still says `Unable to locate a Java
Runtime`, because it asks `/usr/libexec/java_home`, which needs a system service
the wall denies rather than a path anyone can grant. A Java card sets
`JAVA_HOME`, and then the stub works too.

No launcher directory is ever a toolchain root. `~/go/bin` is granted under
NEITHER kind — it is GOPATH/bin, a card that could exec it could run bench-user
tools, and read-without-execute buys nothing in a directory of binaries.
`~/go/bin/go` still works, because it is a symlink into `~/sdk` and the kernel
checks the resolved target; `/opt/homebrew/bin` is out for the same reason, and
the Cellar tree behind it is what is granted. No other path under your home is
granted: not `~/.config/nova-secrets`, not `~/.ssh`. The list and each root's
kind live in `internal/swarm/toolchain.go` and are checked, per OS and in both
directions, against `tools/bench-standard.sh` and `nova-pulse fleet standard`'s
own `toolchain-*` checks by a test, so provisioning and the wall cannot drift
apart.

**A denial in the capture that nobody read is refused, never `NATIVE OK`** — and
the refusal says what it measured and what it did not:

```
NATIVE REFUSED: go-card a denial the card's shell reported went unread, so this
run's disposition is refused rather than OK: step=3 rc=0 wall=landlock
denied_path=/opt/sdk tool/bin/go operation=unverified job=<job>
line="/usr/bin/bash: line 1: /opt/sdk tool/bin/go: Permission denied". The shell
names a path and a refusal and NOT an operation: a denied exec, a redirection to
a path the card may not write, and a cd into a directory it may not read all
print these words, and a card can carry on from any of them, so what failed here
is not established by this line. Remedy: re-run the card's own gate against the
commit under <job> and read its stderr -- that is the measurement this refusal is
standing in for. One candidate among the others, if that path was one the child
had to read or execute: it is under no root this wall was handed, and
"/opt/sdk tool/bin" would be the read_roots entries for it. That is a candidate
and not the diagnosis
```

The exit is 2 and the job directory is named, so the result and the usage row the
child did write are still there to harvest. A run typed `--no-wall` had no
sandbox, is told so, and is offered no read set.

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
