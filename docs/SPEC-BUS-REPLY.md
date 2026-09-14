# The reply transaction — a reply drafted without a hand-built header

Status: **proposed, not implemented.** This is the first bounded slice of
nova-tools issue #246, written to be read before it is built. It is an
extension of the existing `nova-bus draft` verb: no second binary, no second
delivery protocol, no change to any verb or flag that exists today.

`nova-bus` is a released tool that other lines run against their own buses, so
this document obeys one rule above all the others in it: **every verb and every
flag that works today keeps working, byte for byte.** A slice that changed one
of them would be a slice that broke a working loop somewhere nobody in this
repository can see. Everything below is reached only by a flag that does not
exist yet, and the sections say at each point what the absence of that flag
leaves untouched.

## Why — the chore, measured

Answering one note on a bus costs a line more turns than reading one. A survey
of one coordinating line's transcripts found **492 bus-related tool-call
matches** in a single working window, and the replies inside that count took
about **15 coordinator turns each**. The turns are not thinking. They are:

- open the note to find the id, because a `Re:` line is the one thing a reader
  answering a note does not have in front of them;
- pull, because an id resolved against a stale checkout resolves to nothing, or
  worse, to the wrong note of two with the same subject;
- type seven header lines from memory of another bus, get one key wrong, and be
  refused;
- write the draft into the bus checkout, because that is the directory the
  session is already in — and be refused by the clean-tree guard, correctly,
  after the work is done;
- reconcile a receipt that arrived word-split across a shell boundary, and
  decide whether the note went.

None of that is judgment. All of it is mechanical, all of it is already
knowable from state the tool holds, and the cost of doing it by hand is paid
once per reply, forever, by every line on every bus.

**That survey is a wide count; the Measurement section below now carries a
narrow one, and the two do not disagree.** Four replies measured turn by turn
out of one live session cost **2.25 reply-attributable turns each**, sitting
inside raw spans of 11, 11, 16 and 5 assistant turns — one loop counted two
ways. The survey says how often the chore is paid and the baseline says what one
payment costs and **which half of the transaction it goes to**, and it is the
narrow count this slice is held to.

**The fifth cost is removed for this form's own output, and there only.** The
receipt this verb prints is one line, and every value on it is a `key=value`
field through the main specification's one-line field escape, which escapes
every whitespace character: **no field carries an unquoted space**, so a shell
boundary that word-splits the line can neither fuse two fields nor cut one in
half, and a caller reconciles what happened field by field rather than by
guessing where the tokens were. That is free to specify because this form's
output is new — the additive rule constrains what already ships, and nothing
ships here yet. The receipt named in the bullet above is `send`'s, `send` is
released, and this slice does not touch it; giving that line the same property
is later work under #246 and is not claimed here.

**One correction to the shape of the chore, because the read half below
depends on it: today's NEW output is unbounded.** `inbox` and `wait` print
every note that is new to this reader, in full, on every run. The
`--open-max` cap — default 20, with its `and <k> more` line — bounds the
**carried** list and only that; it has never bounded the NEW half and was
never written to. So the half this slice adds bodies to is the half that has
no cap at all today, which is why **The read half, pinned** below carries its
own two limits rather than borrowing one, and why a body-carrying form that
did not would be strictly worse than the second file read it replaces.

**What this slice does not claim.** It does not make the bus faster, does not
change what a note is, does not deliver anything and does not close anything.
It removes one repeated chore, and the section on measurement below says how a
reader would find out whether it did.

## The operation

```
nova-bus draft --bus <dir> --as <name>
               --reply-to <id-or-path-or-subject>
               --body-file <path>
               --draft-dir <dir>
               --remote <name> --branch <name>
               [--to <names>] [--cc <names>] [--subject <text>]
               [--max-body-bytes <n>] [--git-timeout <seconds>]
```

The form is chosen by `--reply-to`. **Without it, `draft` is exactly the verb it
is today**: the same flags, the same skeleton on stdout, the same `DRAFT
REFUSED` lines on stderr, the same exit 2, and no git at all. A caller who never
types `--reply-to` cannot tell this document was written.

`--remote`, `--branch`, `--body-file`, `--draft-dir` and `--git-timeout` are
accepted **only** in the reply form. Given without `--reply-to` they are exit 2
naming the reason — `draft` without `--reply-to` runs no git and writes no
file — which is the same exit code an undefined flag already costs, with a
sentence in place of `flag provided but not defined`.

`--to`, `--cc`, `--subject` and `--as` mean what they already mean and are
resolved by the roster rules in the main specification. `--reply-to` is
mutually exclusive with `--re`: two flags naming one thread say nothing about
which one the writer meant, and that is exit 2.

## What "fresh" means, and why the fetch is inside the verb

The main specification says `nova-bus` reads the checkout and never the remote,
and that a `--fetch` for the reading verbs would be an explicit flag rather than
a hidden fetch. **This form does not weaken that rule; it is the explicit
form.** The refresh is not a flag that can be forgotten — it is part of what
`--reply-to` is, and the verb will not run without `--remote` and `--branch`, on
the same law that refuses to guess a bus or a branch anywhere else here.

The reason it cannot be optional: the whole value of this form is that the id it
writes into your draft is the id of the note you are answering. An id resolved
against a checkout that is an hour behind resolves against a bus that is an hour
behind. The failure is not a refusal — it is a `Re:` line naming the wrong note,
or naming nothing, in a note that then goes out and closes nothing. A reply
written against stale state is exactly the thread-orphaning failure the id
scheme exists to remove, arriving through the reader's own door.

**The refresh is `wait`'s poll, and is one implementation with it** — the same
`internal/bus` code path, not a second one that could drift:

1. take the checkout lock, as every verb that runs git does;
2. `git fetch <remote> <branch>` under `--git-timeout`;
3. **fast-forward the checkout**, never merge and never rebase — which is the
   main specification's own account of `wait`'s poll restated here, and is
   pinned by the existing wait tests rather than asserted fresh: every read in

1. take the checkout lock, as every verb does;
2. `git fetch <remote> <branch>` under `--git-timeout`;
3. **fast-forward the checkout**, never merge and never rebase — which is the
   main specification's own account of `wait`'s poll restated here, and is
   pinned by the existing wait tests rather than asserted fresh: every read in
   this tool reads the working tree, so a fetch that stopped at `FETCH_HEAD`
   would resolve against the same stale tree it was run to replace;
4. a checkout that is **ahead** — holding a commit of its own that has not been
   pushed — is left alone, because there is nothing on the bus it has not got,
   and the receipt says `moved=false`;
5. a checkout that has **diverged** is a refusal naming the recovery, and
   nothing is written;
6. the refresh moves the checkout and **nothing else**. No commit, no push, no
   `CURSOR`, no `OPEN`, no `RECEIPTS`, no `INDEX`.

**A fetch that fails is a refusal and never a fall back to the checkout.** Exit
1, naming the remote, the branch and git's own words under the event line. The
tempting behaviour — fetch, shrug, resolve against what is on disk — is the one
this verb must not have: it produces a draft that looks identical to a correct
one and is wrong exactly when the network was, which is exactly when nobody is
watching. A refusal costs the caller one turn. A wrong `Re:` line costs a
thread.

## Resolving the target

`--reply-to` takes the three shapes `--re` already takes — an **id**, a **path**,
or the **exact subject** of a note on your live listing — resolved by the rules

or the **exact subject** of a note on your open list — resolved by the rules
already written for `--re`, with one addition and one restriction.

**The addition: it is resolved after the refresh**, against the tree the fetch
left. That is the whole point of the form.

**The restriction: the target must be on the caller's live inbox listing.** That
listing is defined here, for this verb, as **what an `inbox` run at this instant
would list**: the `OPEN` entries this reader is already carrying, **plus** every
note new since their `CURSOR` addressed to `--as` on `To:` or `Cc:`. It is
computed **read-only**, after the refresh, against the tree the fetch left, by
the listing implementation the reading verbs already share — and nothing is
written back: no `CURSOR`, no `OPEN`, no `RECEIPTS`, no `INDEX`.

The second half of that set is not decoration; without it the form contradicts
itself. `OPEN` is written only when a read has already shown a note, so a target
that landed on the remote a minute ago is in nobody's `OPEN`. Defining the set
as the carried `OPEN` alone would fetch that note, resolve it, and then refuse it
as *never addressed to you* — the fetch would have done work the rule then threw
away. New-since-the-cursor is exactly what the fetch is for.

**A note already heard is still a legal target.** A receipt records that a note
arrived, not that it was answered: the main specification keeps a receipted note
on the open list for that reason — *heard is not answered* — and this verb keeps
it targetable for the same one.

A note that is on the bus but on neither half of that listing is refused, and the
refusal says which of the reasons it is:

**The restriction: the target must be on the caller's own open list.** A note
that is on the bus but not on your open list is refused, and the refusal says
which of the reasons it is:

- you have already answered it — a note of yours carries its id on a `Re:` line;
- it was never addressed to you, `To:` or `Cc:`;
- it is behind your switch-day line, so this reader has taken it as read;
- you have no cursor yet, so there is no listing for it to be on.

- you have no cursor yet, so you have no open list at all.

The reason for the restriction is what this form is for: it answers what you are
carrying. A target you are not carrying is far more often a stale id copied out
of an older listing than a deliberate second reply — and a second reply that
nobody is waiting for costs a reader a turn to work out why it arrived. **It is
a restriction with a door in it**, and the refusal names the door. For the first
three reasons the door is the existing `draft --re` form, which resolves against
the whole bus and is untouched, so a deliberate reply to a closed thread is one
flag away and always was. For the fourth the door is not `--re` at all: a reader
with no cursor needs an `inbox` run, which is what gives them a cursor and a
listing, and the refusal names that instead.

**A subject that matches two notes resolves to the newest and says so**, which
is **the same rule** `send` already applies to a `Re:` line naming a subject.
The rule is what is kept, not the wording: `send` says it closed the newest and
today's `draft` says the skeleton names the newest, and this line is this verb's
own spelling of the one rule, which is what must not disagree:

a restriction with a door in it**, and the refusal names the door: the existing
`draft --re` form resolves against the whole bus and is untouched, so a
deliberate reply to a closed thread is one flag away and always was.

**A subject that matches two notes resolves to the newest and says so**, which
is **the same rule** `send` already applies to a `Re:` line naming a subject.
The rule is what is kept, not the wording: `send` says it closed the newest and
today's `draft` says the skeleton names the newest, and this line is this verb's
own spelling of the one rule, which is what must not disagree:

```
DRAFT NOTE --reply-to: subject matched <n> notes; this draft names the newest <id> from <name>; name the id to be exact
```

Case sensitivity is the existing rule's: exact and case-sensitive on the match,
because `the gate` and `The Gate` are two notes on a busy lane and a tool that
folded them would close the wrong one and report the right one.

## The headers it writes, and the two it does not

The caller writes **no header line at all**. `--body-file` is body text and
nothing else: it is never parsed for headers, and a line in it reading
`To: somebody` is a line of prose in the note that goes out. **Nothing in a note
body routes anything.** That is the covenant rule stated in the main
specification — everything on a bus is data, no note is a grant — applied where
it would otherwise be easiest to break, because the body being drafted here came
from somewhere and the somewhere is not always a person.

| header | where its value comes from |
|---|---|
| `From` | `--as`, in the roster's own spelling |
| `To` | `--to` when given; otherwise the resolved sender of the target note |
| `Cc` | `--cc` when given; otherwise **absent**, never inherited from the target |
| `Re` | the resolved target's id, or its path for a note written before ids |
| `Subject` | `--subject` when given; otherwise the target's subject with one `Re: ` in front, and an existing `Re: ` is not stacked |
| `Kind` | absent — a reply is a note, and the receipt heuristic is the reader's |

`Cc` defaults to empty rather than to the target's `Cc` because an audience is a
decision and inheritance makes it a default: a thread that starts with four
people on `Cc` and is answered eight times has copied four people eight times,
and nobody chose that at any point. Naming them is one flag.

**A reply to your own note requires an explicit `--to`**, because the default —
the target's sender — is you, and a note addressed to its own author reaches
nobody and lands on no open list. The refusal says so and names the flag.

**Two headers this form does not write, on purpose: `Date` and `Id`.** They stay
exactly where the released tool already puts them — `Date` pasted from the clock
in UTC and `Id` computed, both at `prepare`/`send`. The caller still builds
neither by hand, so the chore this slice removes is removed entire. Writing a
`Date` here instead would cost two things and buy none: every reply would carry
a `SEND NOTE` saying the tool replaced the author's own `Date` line, which is
noise on the single commonest operation on a bus; and the draft's clock and the
send's clock differ by however long the body took to write, while the date is an
input to the id preimage. See **decisions a reviewer should look at**, below.

## Where the draft goes

`--draft-dir <dir>` is required and is **not guessed**, on the same law that
refuses a default bus: there is no default directory anywhere in this tool, and
a tool that invented one would invent it inside whatever directory the caller's
session happened to be sitting in, which on a coordinating line is the bus.

**A `--draft-dir` inside the bus checkout is refused, before anything is
written.** The main specification already says drafts go in a scratch directory,
because `send` needs the bus's tree clean but for the note it is about to write —
and today that rule is enforced at `send`, which is after the body is written,
after the headers are assembled and after the caller has spent the turns. The
test is the one `--bus` already makes: resolve both paths, follow symlinks on
both sides, and refuse when the draft directory is the bus root or under it.
Moving that refusal from the end of the job to the start of it is most of what
this section is for.

The file is named `<UTC minute>Z-re-<target id>.md` in `--draft-dir`. It is
named by the tool and not by the caller so that two replies to two notes cannot
land on one path; the minute is there for a person reading the directory.

**The name is built from the target's id and never from a path.** An id is a
lane name and hex — `ada-3f9a1c2b8d40` — so it is one path segment by
construction, while `--reply-to` may resolve a legacy note to a repo-relative
path, and a path holds `/`. A filename built from one would put the draft in a
directory nobody named, or in none at all. **A target with no `Id:` line gets a
derived id instead**, stated here so nobody has to guess it: the literal
`legacy-` followed by the first 12 lowercase hex digits of the SHA-256 of the
target's repo-relative path, exactly as the path appears on the `Re:` line the
draft writes. It is one segment, it is deterministic, and two legacy targets
cannot collide onto one name — which is the whole job of the field. The `Re:`
line itself is unaffected and still carries the path: the filename is the
bench's, the header is the bus's.

**An existing file at that path is a refusal, never an overwrite** — exit 1,
naming the path — because the one thing that can be at that path is a draft of
this same reply that somebody is editing in another window.

**That refusal is made by the publish itself, and never by a check in front of
it.** Two bus checkouts on one bench can be pointed at one `--draft-dir`, and
two readers of one lane compose the same `<UTC minute>Z-re-<target id>.md` by
construction: a check that finds nothing, followed by an ordinary rename,
replaces whatever was created in the gap between the two, and the loser's draft
is gone with no line printed anywhere. **The final name MUST be created
exclusively** — by a call that fails when the destination exists rather than
replacing it. A cheap pre-check is allowed as an early courtesy, but it never
makes the guarantee, and its line and its exit code are the same ones the
publish's own already-exists error produces, so a reader cannot tell which of
the two spoke.

The write is therefore two steps, and the second is the one that matters here.
First the complete draft goes into a unique temporary inside `--draft-dir`: a
name drawn from the OS random source, created exclusively, refusing to follow a
symlink, written, flushed and closed — the same discipline the lane state files
use, and what keeps a kill from leaving half a reply that looks sendable. Then
it is **published no-replace**, by the first of these the platform and the
filesystem support:

1. **hard-link the temporary onto the final name, then unlink the temporary.**
   The link call is atomic and fails with an already-exists error rather than
   replacing, on every POSIX filesystem and on NTFS, so this is the primary path
   on all three platforms the family builds for.
2. **the operating system's own no-replace rename**, where the filesystem
   refuses hard links — some network mounts, some container overlays, FAT:
   `renameat2` with `RENAME_NOREPLACE` on Linux, `renamex_np` with `RENAME_EXCL`
   on macOS, and `MoveFileEx` **without** `MOVEFILE_REPLACE_EXISTING` on
   Windows, which is refusal-by-default and not a flag that has to be added.
3. **where neither is available** — an old kernel, or a filesystem that
   implements neither, seen as the call reporting that it is not supported — the
   tool **refuses to publish**: exit 2, naming the directory, the call it tried
   and what the call said, with one remedy line: name a `--draft-dir` on a
   filesystem that has one of the two. It does not fall back to a replacing
   rename and it does not fall back to check-then-rename, because both are the
   race this rule exists to close. A plain replacing rename MUST NOT appear
   anywhere on this path.

The temporary is removed on every failing path — the already-exists refusal and
the unsupported-filesystem refusal alike — so a refused run leaves `--draft-dir`
holding exactly what it held before, and no stray `.tmp` beside it.

land on one path; the minute is there for a person reading the directory. **An
existing file at that path is a refusal, never an overwrite** — exit 1, naming
the path — because the one thing that can be at that path is a draft of this
same reply that somebody is editing in another window.

The path is printed, once, on the receipt line. Nothing else goes to stdout in
this form.

## Output grammar

```
DRAFT OK path=<path> re=<id> from=<name> to=<names> cc=<names|-> at=<commit> moved=<true|false> bytes=<n>
DRAFT NOTE <one thing this run decided for you>
DRAFT REFUSED: <reason>
```

`DRAFT OK` is **exactly one line, and it is the only thing this form puts on
stdout.** The existing form still prints a skeleton and no `OK` line, because
there its stdout is a file; here the draft is a file the tool wrote and stdout
is a receipt, so the two forms print opposite things for the same reason.

The fields:

- `path=` — the draft, absolute or as given, escaped as a one-line field. It is
  the remedy the rest of the line points at: whatever the receipt does not carry
  is in this file, in full;
- `re=` — the resolved target id, which is the fact a reader most wants to check;
- `from=` — the resolved speaker;
- `to=`, `cc=` — the **resolved** recipients, `;`-joined, each through the
  one-line field escape, **capped at the first 8 names with `+<k>` standing for
  the rest**; `-` when there are none. It is a cap, a count and a remedy on one
  field: `+<k>` says how many were not printed and `path=` is where they all
  are. Eight is enough for every group on a small bus and bounded on a large
  one, which is the property a receipt needs;
- `at=` — the commit the resolution ran against, so a reader can say what state
  produced this id without running a second command;
- `moved=` — whether the refresh moved the checkout;
- `bytes=` — the size of the draft written.

`DRAFT NOTE` lines say what the run decided, one line each, on stderr. **The set
is finite and enumerated here**, which is what makes it bounded — there is no
shape of input that produces an unbounded number of them:

| what happened | the note |
|---|---|
| the subject matched more than one open note | `--reply-to: subject matched <n> notes; this draft names the newest <id> from <name>; name the id to be exact` |
| the target's subject already began `Re: ` | `the subject is the target's own, unstacked: "<subject>"` |
| the refresh moved the checkout | `the bus moved <n> commits before this id was resolved` |
| the refresh found nothing new | `the bus had nothing new; this id was resolved against <commit>` |
| `--to` was given and differs from the target's sender | `--to names <n> recipients rather than the sender of <id>; this reply goes where you said` |

At most five lines, whatever the bus holds. `DRAFT REFUSED` prints **every**
problem in one run, one line each, which is the existing rule for this verb and
is kept for the existing reason: a refusal that names the first of three
mistakes costs the writer three runs to be told what the tool knew on the first.

`OK` and the notes obey the one-line guarantee and the field grammar in the main
specification's Conventions: every value is escaped so that nothing a bus holds
can make one event into two, and the free-text tail of a refusal is capped at
the existing tail budget with the existing cut mark. A subject holding a newline
produces one line.

## The refusals, with their exit codes

The split is the tool's existing one: **exit 1 is the bus or the state saying
NO**, and **exit 2 is an invocation that could not run.** The existing
`draft` form's refusals stay at exit 2 exactly as they are today — there is no
bus state in that form to say anything — and this table is the reply form only.

| what is wrong | what the refusal says | exit |
|---|---|---|
| `--reply-to` with `--re` | name the thread once; the two flags say different things | 2 |
| `--reply-to` without `--body-file`, `--draft-dir`, `--remote` or `--branch` | the missing flag, and `refusing to guess` | 2 |
| `--remote`, `--branch`, `--body-file`, `--draft-dir` or `--git-timeout` without `--reply-to` | this form runs no git and writes no file; the flag belongs to `--reply-to` | 2 |
| `--draft-dir` inside the bus checkout | drafts go outside the bus, because `send` needs its tree clean; the refusal names the bus root it resolved | 2 |
| `--draft-dir` that does not exist, or is not a directory | the path, and that this tool creates no directories | 2 |
| the body file is unreadable, or is not a file | the path and the reason | 2 |
| `--remote` or `--branch` failing the conservative charset check | the existing refusal, unchanged | 2 |
| `--as` naming nobody, or nobody with a lane | the existing refusal, unchanged | 2 |
| `--to`, `--cc` or `--subject` carrying a control character or a line separator | the existing one-line validation's refusal | 2 |
| `--max-body-bytes` zero or negative | a budget of zero is not "unlimited"; the existing law | 2 |
| the fetch failed, timed out, or named a remote or branch that is not there | the remote, the branch, and git's transcript under the event line | 1 |
| the checkout has diverged from the named branch | the recovery command, and that nothing was written | 1 |
| `--reply-to` names no id, no path and no listed subject on the refreshed bus | that threads are named by id, and that a slug is not a thread | 1 |
| `--reply-to` resolves on the bus but is not on this reader's live listing | which of the four reasons it is, and the door: `draft --re` for a closed thread, an `inbox` run for a reader with no cursor | 1 |
| the target's sender is `--as` and no `--to` was given | a reply to your own note needs an explicit `--to` | 1 |
| the body is empty, or over `--max-body-bytes` | the budget and the size, read at budget+1 and no further | 1 |
| a file already exists at the draft path | the path, and that this tool never overwrites a draft | 1 |
| the draft directory's filesystem offers no create-exclusive publish | the directory, the call tried, what it said, and to name a `--draft-dir` on a filesystem that has one | 2 |
| another `nova-bus` holds this checkout | the existing lock refusal, unchanged | 1 |

**No refusal writes a partial draft, and no publish replaces one.** The file is
written once, complete, into a unique temporary and then published onto its
final name by the create-exclusive step set out under the filename above, after
every check in this table has passed. Two reasons, and the second is not the
first: a kill between a truncate and a write leaves a file that is neither the
old one nor the new one, and here that would be half a reply that looks
sendable; and a rename that replaces silently loses a whole draft that another
checkout sharing this directory created in the meantime.

| `--reply-to` names no id, no path and no open subject on the refreshed bus | that threads are named by id, and that a slug is not a thread | 1 |
| `--reply-to` resolves on the bus but is not on this reader's open list | which of the four reasons it is, and that `draft --re` answers a closed thread | 1 |
| the target's sender is `--as` and no `--to` was given | a reply to your own note needs an explicit `--to` | 1 |
| the body is empty, or over `--max-body-bytes` | the budget and the size, read at budget+1 and no further | 1 |
| a file already exists at the draft path | the path, and that this tool never overwrites a draft | 1 |
| another `nova-bus` holds this checkout | the existing lock refusal, unchanged | 1 |

**No refusal writes a partial draft, and no publish replaces one.** The file is
written once, complete, into a unique temporary and then published onto its
final name by the create-exclusive step set out under the filename above, after
every check in this table has passed. Two reasons, and the second is not the
first: a kill between a truncate and a write leaves a file that is neither the
old one nor the new one, and here that would be half a reply that looks
sendable; and a rename that replaces silently loses a whole draft that another
checkout sharing this directory created in the meantime.

## What a draft does not do to the open list

**Nothing.** Drafting a reply does not close the note it answers, does not mark
it heard, does not move the cursor and does not write to the bus. The target
stays on the caller's open list, and comes off it exactly where it comes off
today: on a later run whose change set holds a note of the caller's own carrying
that id on a `Re:` line — which is to say, after `send`, and not before.

That is not a limitation to be fixed later. A drafted reply is a file on a
bench; a delivered reply is a note on the bus; and a bus that treated the first
as the second would tell every other line that a question had been answered by
a file nobody but its author can see. The main specification already keeps a
generated draft, a delivered reply, a read receipt and a completed piece of work
as four distinct states, and this slice adds nothing to that list and merges
none of them.

The path from here is the one that already exists and is already reviewed:

```
nova-bus draft ... --reply-to <id> --body-file reply.txt --draft-dir <scratch dir> ...

nova-bus draft ... --reply-to <id> --body-file reply.txt --draft-dir /tmp/drafts ...
nova-bus prepare --bus <dir> --as <name> --file <the path it printed>
nova-bus send    --bus <dir> --as <name> --prepared <artifact> --remote <r> --branch <b>
```

`prepare` and `send --prepared` are unchanged, and a draft this form writes is an
ordinary draft: it must pass the existing `prepare` validation with no
tolerance applied, and one of the tests below asserts exactly that.

## Bounded by design

Three places where this verb's cost could grow with the size of a bus, and what
holds each one:

- **stdout is one line, at every state.** Not one line per open note, not one
  per recipient, not one per commit the refresh brought in. A reader carrying
  six hundred open notes gets the same receipt as a reader carrying two, and a
  test asserts it at six hundred rather than at two;
- **stderr is at most five `DRAFT NOTE` lines plus one refusal line per problem
  in the invocation**, and the problems in an invocation are bounded by the
  number of flags;
- **the resolution parses what it must and no more.** The target is found
  through the live listing and the catalogue the reading verbs already use — the
  carried `OPEN` plus the change set since the cursor, which is the O(new) read
  and not a walk of the bus — so a reply on a bus of ten thousand notes costs
  what a reply on a bus of ten costs.

  through the open list and the catalogue the reading verbs already use, so a
  reply on a bus of ten thousand notes costs what a reply on a bus of ten costs.
  Where the existing resolution does read more than that, this slice **preserves
  correctness and measures it** rather than introducing a second index whose
  freshness nobody can vouch for; any indexing change is a separate, separately
  measured piece of work.

## Decisions a reviewer should look at

Four places where this document departs from the proposal it came from, each
with the alternative and what it would cost.

1. **`Date` is not written at draft time.** The proposal has the reply draft
   carry every header including a `Date` pasted from the clock. Writing it here
   means every send of a generated reply prints a `SEND NOTE` saying the tool
   replaced the author's own date — noise on the commonest operation there is —
   and it puts two clocks into a value the id is computed from. The chore is
   removed either way, because the caller types neither. If a reviewer wants the
   draft to be literally complete, the honest version is a `send` that
   recognises its own `Date` line and stays quiet about it, and that is a change
   to a released verb, which this slice will not make.
2. **The target must be on the caller's live inbox listing** — the carried
   `OPEN` plus everything new since the cursor, computed read-only after the
   fetch, with a receipted note still a legal target. The proposal resolves an
   exact id against the whole bus. The restriction catches a stale id copied out
   of an older listing, which is the failure this form is most likely to
   produce; it refuses a deliberate second reply to a closed thread, which
   `draft --re` still does. Note that the narrower reading — the carried `OPEN`
   alone — is not on the table: it would refuse the note the fetch was run to
   find. If reviewers would rather have the looser rule, the change is to the
   refusal table and to two tests, and nothing else.

2. **The target must be on the caller's open list.** The proposal resolves an
   exact id against the bus. The restriction catches a stale id copied out of an
   older listing, which is the failure this form is most likely to produce; it
   refuses a deliberate second reply to a closed thread, which `draft --re`
   still does. If reviewers would rather have the looser rule, the change is to
   the refusal table and to two tests, and nothing else.
3. **The draft is a file, not stdout.** The proposal prints the draft on stdout.
   A file that the tool names, refuses to overwrite and refuses to put inside
   the bus is what closes the in-checkout refusal at the start of the job rather
   than at the end; and a one-line receipt is what makes the outcome scannable.
   The cost is one required flag, `--draft-dir`.
4. **`--body-file` only; no `--body-stdin` in this slice.** A body is something
   its author will open again, and a pipe cannot be reopened. `prepare --stdin`
   already exists for a pipeline that has no file, so nothing is closed off.

## What this slice deliberately does not do

- **No send, no receipt, no cursor.** It writes one file outside the bus and
  nothing else.
- **No exactly-once claim.** Recovery from an uncertain send is the existing
  prepared artifact, retried; it is never a freshly generated draft, because a
  second draft is a second identity.
- **No quoting of the source note, and no summary of it.** Both are judgment,
  both would put text nobody wrote into a note somebody signs, and a body is the
  part of a bus no tool here has an opinion about.
- **No group or thread expansion.** The audience is the target's sender or what
  the caller named. Expanding a thread's history into a `To:` line grows an
  audience silently, once per reply.
- **No second read path.** The refresh, the listing and the resolution are the
  implementations the reading verbs already use, because two spellings of one
  rule drift and a reply resolved by the drifted one is a reply to the wrong
  note.
- **No `--reply-to` on `prepare` or `send`.** Those verbs are released and this
  slice does not touch them.

## Measurement

The claim under test is narrow: **this form removes operational tokens from
answering a note, and its own output does not cost back what it saved.** Nothing
here claims a percentage, and shorter output on its own proves nothing about
total cost.

### The before side, measured

**The before side is no longer a method to be run later. It has been run.** Four
real coordination replies were measured out of one coordinating line's live
session, 2026-09-13 22:55Z to 2026-09-14 01:10Z, against a live bus checkout —
every one of them an actual reply to an actual note, each with a clear `Re:`
target, and not a synthetic exchange. This table is the baseline the after side
is compared against.

| reply id | target id | asst turns | tool calls | tool-result chars (proxy) | discovery calls (chars) | wall clock |

| reply id | target id | asst turns | tool calls | tool-result bytes | discovery calls (bytes) | wall clock |
|---|---|---|---|---|---|---|
| rowan-f61f36cf7f3f | stella-c1b4e36711c5 | 2 | 2 | 3,787 | 2 (10,492) | 4m 29s |
| rowan-b55e2424667d | stella-69a04c2f42f3 | 3 | 3 | 7,134 | 2 (1,502) | 3m 12s |
| rowan-ddfbbd2a2cf9 | stella-0c1bdfd61805 | 2 | 2 | 3,239 | 1 (410) | 6m 21s |
| rowan-b2b8550843de | stella-d7257ff57d84 | 2 | 2 | 2,196 | 1 (410) | 1m 27s |
| **total / mean** | | **9 / 2.25** | **9 / 2.25** | **16,356 / 4,089** | **6 (12,814)** | mean 3m 52s |

**The method, in one sentence:** turns and calls are read out of the harness
transcript rather than estimated, and the size columns are the **character
length** of the `tool_result` text returned to the model.

**What those size columns are, said exactly, because an earlier draft said it
loosely.** A character length is **not a count of UTF-8 bytes** — one character
of a subject outside ASCII is two, three or four of them — and a character
length divided by four is **not native token usage**, which is what a harness's
own counters report and what a tokenizer would count. So the size half of this
table is a **text-volume proxy** and is labelled one wherever it is used: it is
evidence about how much text moved, it is reproducible from the transcript, and
it is the best thing available before harness counters are. It is **not an
observed operational-token baseline**, and this document does not call it one.
On the proxy, and stated in proxy units rather than in tokens, the reply path is
about 16,400 characters over the four replies (~4,100 each) and discovery about
12,800 (~3,200 each): about 29,200 characters in all, **about 7,300 characters
per answered note**. A reader who wants a token figure divides by roughly four
and carries the word *proxy* with the result.

**The baseline becomes an observed operational-token baseline when two things
exist, and not before**: harness counters that report input, output and
cache-read tokens per turn for the harness the measurement was taken on, and a
statement of what share of each side's total those counters cover. Until then
the after side is compared against this proxy, in proxy units, on both sides —
comparing a proxy on one side against counters on the other is the one thing
that would make the number worse than no number.

transcript rather than estimated, bytes are the character length of the
`tool_result` text returned to the model, and tokens are those bytes at about
four bytes to a token — **an estimate, labelled as one, and never a tokenizer
count**. On that estimate the reply path is about 4,100 tok over the four
replies (~1,020 each) and discovery about 3,200 tok (~800 each): about 7,300 tok
in all, **about 1,800 tok per answered note**.

Three caveats, in the section and not in a footnote, because each one narrows
what the numbers may be used to claim:

- **a turn is an assistant record carrying a tool use.** Not a message, not a
  thought, and not a turn that called nothing;
- **only reply-attributable turns are counted** — the read of the target note
  and the draft/body/send call — and not the unrelated work interleaved between
  them. The raw assistant-turn span from note-read to `SEND OK` was 11, 11, 16
  and 5 for the four rows, so the columns above are the cost of the reply and
  not the span it sat inside. Where one call served both discovery and reading
  the note it was counted once, on the reply side, which makes the discovery
  column conservative;
- **wall clock is elapsed time in a busy window** — target-note bus commit to
  reply bus commit — and is **not** reply latency. It is in the table because a
  reply that takes six minutes to arrive is a fact about the loop, not because
  it is a number this slice promises to move.

The measurement was read-only. Nothing was put on the bus to produce it.

### What the baseline moves the target to

The shape of today's transaction, per reply: one backgrounded `nova-bus wait`,
whose own return is 410 characters — the *running in background* line and
nothing else; one later read of that wait's output file, to learn that a note
landed, at 410 characters in the cheap case and 10,082 in the expensive one; one
read of the target note's file, often batched with a sibling note; and then
**one** call that runs `draft` into a file, appends the body by heredoc and runs
`send`, returning about 250 characters. Every size in this paragraph is the same
text-volume proxy as the table's, and carries the word with it.

whose own return is 410 b — the *running in background* line and nothing else;
one later read of that wait's output file, to learn that a note landed, at 410 b
in the cheap case and 10,082 b in the expensive one; one read of the target
note's file, often batched with a sibling note; and then **one** call that runs
`draft` into a file, appends the body by heredoc and runs `send`, returning
about 250 b.

So the composing step this document specifies is **already one turn and one
call**, and the cheapest two of the four replies are two turns end to end. The
target follows from that and is stated here as a constraint on the after side
rather than as an aspiration:

**The after side must reduce the discovery-and-read volume and turns — the 6
calls and 12,814 characters of the discovery column, and the note-body reads
inside the 16,356-character reply column.** That is where the cost is, and that
is the half the slice is held to.

**A saving on the draft step counts if it is measured, and ranks below the
other half.** An earlier draft excluded it by definition; that was wrong, and
this is the repair. A draft-step saving that is *proved* — measured turn by turn
on the after side, by the same rules, on comparable real replies — is a real
saving and is reported as one. It is simply small: there is one turn and one
call there and about 250 characters of output, so the ceiling on that half is
low, and a report leads with the discovery-and-read half and puts the draft-step
figure after it. What is still refused is a saving **assumed** there, or a total
that hides an unmoved discovery column behind a moved composing step: a report
that shows the composing step got cheaper and the read-and-discover side did not
says that, in place of a number.

**Only operational tokens are compared: the same work before and after
adoption, at equivalent accepted quality.** What it cost to build this form and
to review it is sunk and is **excluded entirely** — not folded into the
per-reply figure, not reported beside it, and above all not turned into a count
of replies at which it pays for itself. A build cost is spent whatever happens
next, so a payback threshold measures nothing anybody can act on. The question
is only whether a line that has the form spends fewer tokens than a line that
does not, on the same work, for a reply of the same accepted quality —
"accepted" because a draft that needed a second pass before it could be sent did
not do the same work as one that did not, and must carry that pass in its own
column rather than in neither.

**The two forms are compared without the same live reply being delivered
twice.** The before side above is real coordination replies for exactly this
reason — a synthetic reply has no stale checkout and no ambiguous subject, which
is where the cost actually goes — and the after side is gathered the same way,
over comparable real replies, by the same rules. Then compare at draft time: the other side is
composed as a draft and stopped there, or both sides are replayed as a fixture
exchange against a disposable local bare remote. **A duplicate note is never put
on the real bus to produce a number.** Where a real reply does go out it goes
out once, by whichever form is in use that day, and the other form is the dry
run beside it.

The claim under test is narrow: **this form removes turns from answering a note,
and its own output does not cost back what it saved.** Nothing here claims a
percentage, and shorter output on its own proves nothing about total cost.

**The after side must reduce the discovery-and-read bytes and turns — the 6
calls and 12,814 b of the discovery column, and the note-body reads inside the
16,356 b reply column — and it may not count as a saving anything it takes off
the draft step.** There is one turn and one call there and about 250 b of
output; a form that halved that would have halved nothing a line can feel. A
report that shows the composing step got cheaper and the read-and-discover side
did not has measured the wrong half, and says so in place of a number.

**Only operational tokens are compared: the same work before and after
adoption, at equivalent accepted quality.** What it cost to build this form and
to review it is sunk and is **excluded entirely** — not folded into the
per-reply figure, not reported beside it, and above all not turned into a count
of replies at which it pays for itself. A build cost is spent whatever happens
next, so a payback threshold measures nothing anybody can act on. The question
is only whether a line that has the form spends fewer tokens than a line that
does not, on the same work, for a reply of the same accepted quality —
"accepted" because a draft that needed a second pass before it could be sent did
not do the same work as one that did not, and must carry that pass in its own
column rather than in neither.

**The two forms are compared without the same live reply being delivered
twice.** The before side above is real coordination replies for exactly this
reason — a synthetic reply has no stale checkout and no ambiguous subject, which
is where the cost actually goes — and the after side is gathered the same way,
over comparable real replies, by the same rules. Then compare at draft time: the other side is
composed as a draft and stopped there, or both sides are replayed as a fixture
exchange against a disposable local bare remote. **A duplicate note is never put
on the real bus to produce a number.** Where a real reply does go out it goes
out once, by whichever form is in use that day, and the other form is the dry
run beside it.

What is counted, per reply:

- **coordinator turns**, before and after, read out of the harness transcript
  rather than estimated. This is the number the slice exists to move;
- **the discovery-and-read side, as its own column** — the calls and the
  tool-result bytes spent finding out that a note landed and getting its text,
  kept separate from the composing call. That is the half the baseline says the
  cost is in, and a total that folds the two together cannot show whether it
  moved;
- **the coordinator's own input, output and cache-read tokens per reply**, where
  the harness reports them, because a turn is mostly a cache read and a count of
  turns alone would hide the category the saving actually lands in;
- **the tokens of the tool's own output**, before and after: stdout plus stderr,
  measured **at the largest plausible state** — a reader carrying several
  hundred open notes — because a receipt that is bounded at ten notes and
  unbounded at six hundred is unbounded. The tool's output is an operational
  cost like any other, counted on the after side and never netted out;
- **operational review and retry overhead**: the turns and tokens spent reading
  a generated draft before it is sent, every refusal met on the way, and every
  re-run. A form that halves the composing turns and adds a review pass has
  moved the cost rather than removed it;
- **errors and wall time**, for that same reason;


- **the coordinator's own input, output and cache-read tokens per reply**, where
  the harness reports them, because a turn is mostly a cache read and a count of
  turns alone would hide the category the saving actually lands in;
- **the tokens of the tool's own output**, before and after: stdout plus stderr,
  measured **at the largest plausible state** — a reader carrying several
  hundred open notes — because a receipt that is bounded at ten notes and
  unbounded at six hundred is unbounded. The tool's output is an operational
  cost like any other, counted on the after side and never netted out;
- **operational review and retry overhead**: the turns and tokens spent reading
  a generated draft before it is sent, every refusal met on the way, and every
  re-run. A form that halves the composing turns and adds a review pass has
  moved the cost rather than removed it;
- **errors and wall time**, for that same reason;
- **source and recipient correctness**: did the reply name the note it meant and
  reach the people it meant. A turn saved by a reply that went to the wrong
  audience is not a turn saved.

**Any category the harness does not report is labelled `unknown`, never zero,
and the report states what share of each side's total is unmeasured.** A harness
that reports turns but not cache reads yields a turn comparison and an unknown
token comparison, and the summary says so in the same sentence as the number,
not in a footnote. A saving claimed over a total that is largely unknown is not
a saving claimed.

**Implementation and review cost is excluded from the per-reply figure and
reported beside it**, as its own number, with the count of replies at which it
pays for itself. Folding it into the per-reply figure would hide the steady
state; leaving it out entirely would hide the bill. Both numbers, separately, is
the only honest shape.

Raw transcripts and the exact revision each side was measured at are retained,
and the report names the harnesses and models involved without treating any of
them as the standard: a measurement taken on one harness is evidence about that
harness. Adoption is voluntary in either case, and the existing `draft --re`
loop stays supported for lines that prefer it.

## The read half, pinned

The Order of build below puts the READ half first. This section pins that half
as a contract before anything is built, so that what goes first is a thing with
a shape rather than a direction. It is deliberately small: **one opt-in flag,
two limits, one frame, one receipt line, one continuation input and one cursor
rule.** No new binary, no
new verb, and nothing here changes a byte of what `inbox` or `wait` print today.

**Draft 7 proposal (Stella):** draft 6 cleared R3 (item/summary bounds) and
R4 (exact frame separators). R1/R2 still failed across real multi-page drains:
a cursor advanced by page1 invalidated page2; a later page forgot an earlier
gap; legacy IDs were not unique; a lone oversized note had no honest terminal
state. The snapshot continuation contract below replaces the earlier commit:id
scheme. It is awaiting independent friend review; reply-side approval remains
scoped to the unchanged reply contract.


a shape rather than a direction. It is deliberately small: **one additive flag,
two limits, one frame, one receipt line and one cursor rule.** No new binary, no
new verb, and nothing here changes a byte of what `inbox` or `wait` print today.

**Four repairs at draft 6.** The read half was held on four read-side holds.
Each is fixed by id, in the section that states the rule, and each names the
test that fails if the rule is broken:

| id | the hold at draft 5 | fixed in | test |
|---|---|---|---|
| **R1** | continuation named a commit, so a commit holding two new notes lost the second, and display order was assumed to be commit order | *The limits* (the scan order), *The receipt* (`next=<commit>:<id>`), *The cursor* (`--after`) | `TestTwoNotesInOneCommitWithMaxNotesOneLosesNeither` |
| **R2** | a first body larger than any allowed `--max-bytes` printed nothing, said `next=-`, and the same command looped forever | *The limits* (the named gap), *The receipt* (`oversize=`), *The cursor* | `TestASingleOversizeBodyIsANamedGapAndNeverALoop` |
| **R3** | "bounded listing" was claimed over a NEW half that only bodies were bounded on | *The flag* (the guarantee), *The limits* (the cap) | `TestBodiesModeCapsTheNewSummaryLinesToo` |
| **R4** | no separator was stated between a body with no final newline and the line after it | *The frame* (the exact bytes) | `TestTheFrameSeparatorIsExactBytesIncludingAnEmptyBody` |

### The flag, and why it is a flag

**`--bodies`**, on `inbox` and on `wait`. With it, each NEW note that fits the
budget prints its existing `INBOX NOTE` line and then its body, framed. Without
it, both verbs are exactly what they are today.

It is a flag on the existing listing path and not a verb for three reasons, and
the third is the one that decides it:

1. **`--since` is spoken for.** `check --since <commit>` is this binary's gate
   (**SPEC.md:2397**, specified at **SPEC.md:3959**). A `read --since` verb would
   give one flag two meanings in one binary.
2. **`wait` is `inbox` on a clock.** They share one listing implementation, so a
   flag lands on both at once; a verb would be a third caller of that listing
   and would have to be kept in step with two.
3. **No second read path.** That is this document's own law, stated under *What
   this slice deliberately does not do*, for the reason that two spellings of one
   resolution drift, and a reply resolved by the drifted one is a reply to the
   wrong note. A verb would be the second spelling. A flag cannot be.

**R3 — the guarantee, in one sentence.** *With `--bodies` the whole NEW half of
the return is bounded — at most `--max-notes` NEW items print at all, summary
line and frame together, and at most `--max-bytes` body bytes — and without the
flag nothing is bounded and the return is byte-identical to today's.*

Draft 5 had that both ways: it said the flag removed no `INBOX NOTE` line and
limited only bodies, and then called the result a bounded listing. Both cannot
stand, because today's NEW half is unbounded, and a baseline that is unbounded
does not become bounded by having a bounded thing added to it. The sentence
above picks the side that is worth having and *The limits* makes the bound real:
in bodies mode the summary lines are capped with the frames, so an item past the
cap prints no line of any kind and is left for the next call whole. The flag is
still additive in the only sense that was ever promised to the released tool —
it reorders nothing, renames nothing and changes no run that does not pass it —
and `TestBodiesModeCapsTheNewSummaryLinesToo` asserts the counts on both sides.

`--bodies` is only ever additive: it adds frames after `INBOX NOTE` lines that
were going to be printed anyway, and adds one receipt line at the end. It
removes nothing, reorders nothing and renames nothing.

### The limits

Bodies are the first thing on a bus whose size is the *sender's* choice rather
than the reader's, so the flag carries its own budget and both halves of it are
required to have a default:

| flag | default | ceiling | what it bounds |
|---|---|---|---|
| `--max-notes <n>` | 20 | 1000 | how many NEW items print at all in one return — summary line and frame together (**R3**) |

| `--max-notes <n>` | 20 | 1000 | how many NEW notes get a body in one return |
| `--max-bytes <n>` | 65536 | 1048576 | the total body bytes printed in one return |

**Zero is not "unlimited" and over-ceiling is not "as much as you can".** Either
one, on either flag, is `INBOX REFUSED` at **exit 2**, naming the flag, the
value given and the ceiling — the same law the existing `--max-body-bytes`
refusal obeys, for the same reason.

Both limits are checked **before** a frame is opened, never inside one: a note
whose body would cross `--max-bytes` is not printed half. It is left for the
next call, whole, and counted in the receipt as not printed. `--max-notes` is
the cheap bound and `--max-bytes` the honest one; a return stops at whichever it
reaches first. A run without `--bodies` reads no body and is bounded by neither
limit, which is the released behaviour, unchanged.

**R3 — the cap is the NEW half, not the bodies alone.** With `--bodies`,
`--max-notes` counts **NEW items printed at all**. An item's `INBOX NOTE` line
and its frame print together or not at all, and an item past the cap prints no
line of any kind: it is left for the next call, whole, and the return says
`complete=false`. This is the one place the flag withholds something the same
run without it would have printed, and it is what makes *The flag*'s guarantee
true rather than a phrase. `TestBodiesModeCapsTheNewSummaryLinesToo` asserts
both sides: the capped counts with the flag, and every line still printed
without it.

The snapshot continuation section below defines page selection, gaps and
resume. Its item accounting includes gap lines in the note cap. Body framing
remains exactly the R4 contract that follows.

reaches first.

**R3 — the cap is the NEW half, not the bodies alone.** With `--bodies`,
`--max-notes` counts **NEW items printed at all**. An item's `INBOX NOTE` line
and its frame print together or not at all, and an item past the cap prints no
line of any kind: it is left for the next call, whole, and the return says
`complete=false`. This is the one place the flag withholds something the same
run without it would have printed, and it is what makes *The flag*'s guarantee
true rather than a phrase. `TestBodiesModeCapsTheNewSummaryLinesToo` asserts
both sides: the capped counts with the flag, and every line still printed
without it.

**R1 — the next call resumes at an item, not at a commit.** The NEW half is
taken in one **fixed scan order** and the printed set is a **prefix** of it:
commits in the lane's first-parent order forward from the cursor, and within one
commit **the note files that commit added under `from-<line>/`, by path,
compared bytewise**, then **the lines that commit appended to
`from-<line>/RECEIPTS`, in file order**. That is #239's K3b order taken as it
stands rather than spelled a second way, so one bookmark names the same item in
both tools. Two consequences, and the second is the hold:

- **the display is not reordered, and nothing depends on its order.** The three
  groups `inbox` prints today — `INBOX NOTE`, `INBOX HEARD`, `INBOX RECEIPT` —
  print exactly as they print today. The scan order decides **which** items are
  in the return; the display decides only where each appears in it. Display
  order and scan order may therefore differ, and no rule here assumes they
  agree;
- **a commit holding several items is cut between them, never at its edge.** A
  commit that adds notes A and B, read with `--max-notes 1`, prints A and leaves
  B, and the continuation names **A**, not the commit. Draft 5 named the commit,
  so advancing to it stepped over B and B was never NEW again — the hold, and
  the reason continuation is snapshot-qualified from here on.
  `TestTwoNotesInOneCommitWithMaxNotesOneLosesNeither` is the test, and it
  carries the display-order mismatch as its second fixture.

**R2 — a body no allowed budget can hold is a named gap, never a loop.** Where
the **first** body of a return is larger than this run's `--max-bytes`, no frame
is opened and the item prints once, outside any frame, as its own line. It is
only ever the first: a later body that does not fit is simply the point the
return stops at, left whole for the next call by the rule above, and it becomes
a gap only on the call where it is first and still does not fit — so the two
rules meet and neither covers the other's case.

```
INBOX BODY OVERSIZE id=<id|-> bytes=<n> max-bytes=<m> path=<path>
```

That line **is** the remedy and it names the raised bound: `<m>` is what this run
allowed, `<n>` is what the body actually is, and raising `--max-bytes` to at
least `<n>` carries it — up to the ceiling of 1048576, above which no value
does, and `path=` is then the file to open. The item is a **gap**, and a gap is
never a consumed item:

- it is counted in the receipt's `oversize=`, and in **neither** `printed=` nor
  `bytes=`;
- the return is `complete=false`;
- the continuation steps **past** it, so a caller draining the backlog makes
  progress on every pass and no command repeats itself;
- the **cursor does not** step past it, so the note keeps its turn at being NEW
  and no run ever reports it as read. *The cursor* states that split and why the
  two differ.

`TestASingleOversizeBodyIsANamedGapAndNeverALoop` asserts every one of those,
and the no-frame rule with them, including
the case Stella named: a first body over the hard ceiling, where draft 5 printed
`printed=0 next=-` and the same command ran forever.

### The frame

For each NEW note within budget, in the order `inbox` already prints:

```
INBOX NOTE id=<id|-> from=<name> addr=<to|cc> at=<stamp|-> path=<path>: <subject>
INBOX BODY id=<id|-> bytes=<n>
<the body, exactly n bytes, verbatim>

<the body, n bytes, verbatim>
INBOX BODY END id=<id|->
```

`INBOX BODY` is the fixed opening line and carries the note's id and the exact
byte count of what follows. The `<n>` bytes after it are the body as the sender
wrote it: **no escaping, no re-wrapping, no trailing-newline normalisation and
no substitution of any kind.** `INBOX BODY END` is the fixed closing line and
carries the same id.

**R4 — the exact bytes, and the separator is outside the count.** A body the
sender did not end in a newline would otherwise run into the closing line and
make it unfindable, so the framing supplies the newline itself and says so. One
frame is, in order and with nothing between the parts:

1. the opening line `INBOX BODY id=<id|-> bytes=<n>`, ended by one `\n`;
2. exactly `<n>` bytes of body, whatever those bytes are;
3. **the separator**: one `\n`, emitted **if and only if** `n` is `0` or the
   body's last byte is not `\n`. It is framing and never body — it is **not**
   counted in this frame's `bytes=`, **not** added to the receipt's `bytes=`,
   and a reader that keeps it has corrupted the body by one byte;
4. the closing line `INBOX BODY END id=<id|->`, ended by one `\n`.

So a reader does exactly this: read the opening line; consume `n` bytes; then,
**if `n == 0` or the last byte consumed was not `\n`**, consume exactly one more
byte and assert it is `\n`; then read the closing line and assert its id. Both
halves of that condition are load-bearing and **a zero-byte body is the case
that needs both**: `bytes=0`, no body bytes at all, one separator `\n`, then the
closing line. `TestTheFrameSeparatorIsExactBytesIncludingAnEmptyBody` fixes all
three shapes — a body ending in `\n`, a body not ending in `\n`, and the empty
body — as byte-for-byte expected strings, and asserts the consume-and-assert
sequence above rather than searching for the closing line.

**Why a body line can never be mistaken for a status line.** The count is the
frame, and the closing line is for the person reading, never for the parser:

- **status lines are parsed only outside frames.** A reader is in exactly one of
  two states. Outside a frame it reads event lines. On `INBOX BODY` it reads
  `bytes=<n>`, consumes **exactly `n` bytes** and returns to the outside state;
- **inside a frame nothing is parsed.** A body holding a line that reads
  `INBOX NOTE id=... : anything` is `n` bytes of body like any other, because
  the reader is counting and not matching;
- **the closing line does not end the frame — the count does.** A body holding a
  line that reads `INBOX BODY END id=whatever` ends nothing. The frame ends at
  byte `n`, and the reader then asserts that the next line is the real closing
  line for the same id. If it is not, that is a **defect in the writer**, and
  the reader says so and stops rather than resynchronising by guesswork;
- **the id on both lines ties them together**, so a frame cannot be attributed
  to the wrong note by a reader that lost its place.

This is the delimited framing the Order of build asks for, stated exactly:
nothing a body holds can be read as an event line, and that property comes from
the byte count, which a sender cannot forge, and not from a delimiter, which a
sender can type.

### Snapshot continuation and the read cursor (draft 7 proposal)

A continuation is a position in one immutable listing snapshot. It is not a
receipt, a reply, permission to read another lane, or proof that a person read a
body. `--advance` remains the explicit request to update this reader's bus state.

**Snapshot.** The first call captures the reader's base cursor C0 and refreshed
bus tip H, plus the canonical reader identity and listing selector. The existing
inbox listing implementation defines eligible NEW items in that range. Pagination
uses a stable internal ordering of those same items: first-parent commit order,
then bytewise repository-relative note path, then appended receipt-record offset
within its path. It does not borrow nova-wake's narrower lane selection. Within
each page, the existing display groups keep their order. Legacy notes without an
Id and two items in one commit remain distinct by path/record offset; `id=-` is
never a continuation identity.

**Token.** `next=` is a bounded, versioned, opaque URL-safe token, passed back
unchanged as `--after <token>`. Version1 carries C0, H, reader/selector identity,
the last accounted item identity, the earliest unresolved gap (or none), cumulative
gap count, the whole-commit safe frontier, and the expected persisted cursor after
this call. No body, subject, secret, unbounded item list, or absolute path belongs
in it. Encoding and decoding use a single schema; malformed, oversized (over8KiB),
unknown-version, mismatched reader/selector, unavailable snapshot, non-ancestor
range, invalid item position, or inconsistent frontier/gap fields refuse at exit2.
A token is client-supplied query state, not authentication: its checksum, if any,
only detects accidental corruption. Never use it to bypass roster/path checks or
claim a read receipt. Cursor update authority comes from `--advance` alone.

**Resume.** Validate the token against its original C0..H snapshot, not the new
CURSOR..HEAD range. A normal prior page may have advanced CURSOR to the commit
containing the token's item; that does not invalidate the token. The current
persisted cursor must equal the token's expected cursor. A different cursor means
another read changed this reader's state: refuse with an explicit fresh-read
remedy rather than moving it backward or silently changing the snapshot. New
commits after H wait for a fresh chain. A missing or rewritten snapshot likewise
refuses without mutation. No continuation token requires a hidden server ledger
or writes from a read-only invocation.

**Page accounting.** `--max-notes` caps all NEW items emitted, including gap
lines and summaries of receipt/heard items. `--max-bytes` caps the sum of emitted
body bytes. A summary and its body frame are emitted together. Stop before an
item whose body would exceed the remaining body budget. A body exactly equal to
the remaining budget is emitted; the next token points to the
last item already accounted for, so the omitted item is first on the next page.
If the first candidate's body exceeds the full per-call budget, emit one bounded
`INBOX BODY OVERSIZE` line with its identity, size and repository-relative path,
count one gap/item, and account for that position. It consumes no body bytes.
The following candidate may fit on this page if item and byte budgets remain.
The token carries the earliest gap across every later page, even when later
pages print ordinary bodies. A later page cannot forget a skipped first body.

**Completion.** The receipt is:

```
INBOX BODIES printed=<n> bytes=<b> oversize=<k> gaps=<g> drained=<true|false> complete=<true|false> next=<token|->
```

`printed` counts body frames and `bytes` their body bytes only; `oversize` counts
new gap lines on this page, `gaps` all unresolved gaps in this chain. `drained`
means no eligible items remain after this page in C0..H. `complete` means drained
AND gaps=0. `next` is present exactly when another page remains; terminal pages
have `next=-`. Thus empty snapshots are drained/complete, while a lone oversized
body is drained but incomplete. There is no command to repeat indefinitely.
At a gapped terminal page print one bounded remedy naming the first unresolved
gap: restart without `--after` with a sufficient allowed byte budget, or inspect
the named file explicitly if it exceeds the hard ceiling. A fresh chain starts
from the persisted cursor and may re-show bodies; it never silently marks a gap
consumed. Do not loop on `complete=false`: drain only while `next` is present.

**Cursor and OPEN.** Without `--advance`, no CURSOR, OPEN, RECEIPTS, INDEX,
commit or push changes. With it, the safe frontier is the greatest whole commit
whose entire eligible prefix in this snapshot has been emitted in full across
this chain, strictly before the earliest unresolved gap or partially emitted
commit. Do not infer this frontier from display order or from the last printed
body. The token carries the prefix accounting needed to resume inside a commit;
validate its internal ordering and snapshot identities before accepting it.
Advance only monotonically from the expected current cursor to that frontier.
An entirely empty return or one containing only gaps does not advance it. OPEN
updates use the same emitted set and existing bookkeeping rules; excluded bodies
do not lose their NEW turn. Failure writing stdout must not advance past the last
fully emitted safe prefix. Network/publish recovery follows the existing bus
rules; retry may re-show a body and is not an exactly-once human-delivery promise.

### The receipt

After the frames, exactly one line:

```
INBOX BODIES printed=<n> bytes=<b> oversize=<k> complete=<true|false> next=<commit>:<id>|-
```

- `printed=` — how many NEW notes got a body in this return. A gap is not one;
- `bytes=` — the total body bytes printed, which is the sum of the frames'
  `bytes=` and is the number a caller compares against `--max-bytes`. It counts
  no separator (**R4**) and no gap (**R2**);
- `oversize=` — how many items this return named as gaps (**R2**), each of which
  printed one `INBOX BODY OVERSIZE` line naming its id **once**. It is `0` on
  every ordinary return, and it is the only place a gap is counted;
- `complete=` — `true` when every NEW item this run reached printed in full,
  `false` when a limit stopped the printing short **or** any gap was named.
  `false` is the whole overflow signal; there is no second one;
- `next=` — the **item** a following call resumes after, as `<commit>:<id>`
  (**R1**): the commit, and within it the id of the last item this return
  accounted for in the scan order — the last item printed, or the gap, whichever
  came last. It is a value to pass back as `--after`, never a status: it says
  where to continue, and says nothing about what was consumed. It is `-` only
  when the return accounted for no item at all, which is a return that printed
  nothing and named no gap, and from which rerunning the same command is
  therefore correct.

This is a cap, a count and a remedy on one line, which is the law the carried
list's `--open-max` line obeys and the law this document's own `DRAFT OK` line
obeys. It is one line at every state: a reader carrying six hundred open notes
gets one, and a test asserts it there rather than at two.

### The cursor

**`--advance` moves the cursor only to the last commit every one of whose items
this run printed in full.** Never past it, in particular never to `HEAD` on a
partial return, never into a commit the return was cut inside (**R1**), and
never past a gap (**R2**). Without `--advance`, nothing moves: no `CURSOR`, no
`OPEN`, no commit, no push — the read-only property `inbox` already has,
unchanged.

The cursor is a commit and cannot name a position inside one, so where a return
stopped part-way through a commit the cursor stops **before** that commit. Its
already-printed items are then NEW again on a run that passes no `--after`,
which is a **re-show and never a loss** — the direction SPEC.md's open-list
section already fixes for this bus — and `--after` is what makes the next call
exact instead of merely safe. A gap is the same rule from the other side: the
cursor stopping before it is precisely what keeps the note NEW, so a caller who
raises `--max-bytes` later still finds it in the half `--bodies` prints.

That rule is what makes a partial return safe, and the reason is worth stating
because it is not the obvious one. An unprinted note is not lost from the
**open list**: `OPEN` is the memory that lets the cursor move past unanswered
notes, and the note would still be carried there. What a cursor moved to `HEAD`
would lose is the note's turn at being **NEW** — it would never again appear in
the half `--bodies` prints, and its body would be reachable only by the second
file read this flag exists to remove. So:

- a partial return advances to the last printed note, and the next call resumes
  at `next=` with the first unprinted note as its first NEW note, in full;
- a complete return advances as `inbox --advance` does today;
- a run that printed nothing advances nothing, whatever flags it was given.

**R1/R2 — continuation is an explicit input, `--after <commit>:<id>`.** Draft 5
said the second call was the first call again. It is not, and could not be: a
read-only run moves no cursor, so rerunning the same command returns the same
items forever, and a run that stopped on a gap reruns into the same gap. So:

- **`--after <commit>:<id>`** takes a `next=` value back and starts the scan at
  the item **after** the one it names, in the fixed scan order. The id is the
  last printed note's id — or the gap's — and never a commit alone;
- the flag is **only** ever a value the tool printed. `--after` naming a commit
  outside the range this run reads, or an id that commit does not hold, is
  `INBOX REFUSED` at **exit 2**, naming the value given and, as its remedy, the
  same command **without** `--after`. It is never silently ignored and never
  silently restarted, because either would re-read items the caller was told it
  had passed;
- a caller draining the backlog loops `--bodies --after <the last next=>` until
  `complete=true`, each pass bounded by both limits. With `--advance`, the
  cursor follows behind at whole commits; without it, nothing moves at all and
  `--after` alone carries the position. Both are exact, and neither can spin: a
  pass that prints nothing and names no gap returns `next=-`, which is the one
  state where rerunning unchanged is the right thing.

`TestTwoNotesInOneCommitWithMaxNotesOneLosesNeither` and
`TestASingleOversizeBodyIsANamedGapAndNeverALoop` both drain to
`complete=true` through `--after` and assert every item arrives exactly once.

### One reader, not two

`nova-wake` (#239) needs to correlate an answer to a ping, and its **draft 7**
took that correlation **off `inbox` entirely** — see its *An answer is a note or
a receipt from the line to the caller* section, finding **K3**, which found that
draft 6 had called it two bounded `inbox` reads when a plain `inbox` prints
every NEW note in full, so its size was never that tool's to bound. Draft 7
replaced it with `probe`'s own read-only `git` walk of the pinged line's lane,
headers and `RECEIPTS` only, never a body, under `--correlate-max` and
`--correlate-bytes`.

That finding is the same fact this section opens with, met from the other side,
and it is the reason this flag exists in the shape it does. Two consequences,
and the second is a standing constraint rather than a claim:

- **nothing here changes #239.** Its correlation stays exactly where draft 7 put
  it. This document does not ask it to move, and a reader should not take this
  section as a second answer to #239;
- **if a bounded listing call is ever wanted there — for a body, which the lane
  walk deliberately never reads — it is this flag that is called, and no second
  bounded reader is written beside it.** The reason draft 7 could not use
  `inbox` was that the NEW half had no bound; `--bodies` is that bound, with its
  own budget, its own `drained=`/`complete=`/`next=`/`--after` continuation and its
  own read-only default. One listing path, one set of limits, one continuation grammar.

  own budget, its own `complete=`/`next=` continuation and its own read-only
  default. One listing path, one set of limits, one continuation grammar.

  own budget, its own `complete=`/`next=`/`--after` continuation (**R1**) and its
  own read-only default. One listing path, one set of limits, one continuation grammar.

### What this half does not do

- **it changes no existing output.** Without `--bodies`, `inbox` and `wait` are
  byte-identical to today — stdout, stderr and exit code — and a test asserts
  it;
- **it caps nobody's mail.** A note is never truncated and a body is never cut:
  the budget decides **how many whole items** print, never how much of one, and
  an item too large for any budget is named as a gap and left whole where it is
  (**R2**) rather than clipped to fit;
- **it consumes nothing it did not print.** `printed=`, the cursor and
  `oversize=` are three separate statements and none of them stands in for
  another: a gap is counted, is not printed, and is not passed by the cursor;

  the budget decides **how many whole notes** print, never how much of one;
- **it is no new binary and no new verb**, and it adds no state: no index, no
  cache, no file of its own;
- **it does not touch the carried list.** `--open`, `--open-max` and
  `--open-warn` behave exactly as they do today, and `--bodies` prints no body
  for a carried note — the carried list is a reminder of what is owed, not a
  second delivery.

## Order of build

The baseline changes which half of #246 is built first, and this section says so
rather than leaving the order to whoever picks the work up. **The reply verb
specified in this document is the second build target, not the first.**

**First: the READ half — the new notes addressed to `--as`, in full, in one
call, with no output file to read afterwards. That half is `--bodies` on
`inbox` and `wait`, and it is pinned as a contract in *The read half, pinned*
above: one opt-in flag, `--max-notes` and `--max-bytes`, the counted
`INBOX BODY` frame with its stated separator, the `INBOX BODIES` receipt, the
`--after <token>` snapshot continuation and the cursor rule that never advances
past what was printed in full.** The paragraphs below are why that half goes
first; the section above is what is built.

The released listing supplies eligibility and bookkeeping; this opt-in flag adds
the NEW-output bounds, so it extends `inbox` rather than creating a second reader.

call, with no output file to read afterwards.**

above: one additive flag, `--max-notes` and `--max-bytes`, the counted
`INBOX BODY` frame, the `INBOX BODIES` receipt and the cursor rule that never
advances past what was printed.** The paragraphs below are why that half goes

`--after <commit>:<id>` continuation and the cursor rule that never advances
past what was printed in full.** The paragraphs below are why that half goes
first; the section above is what is built.

The main specification already provides the bounded new-notes half of that on a
released verb, so this is `inbox` extended and not a new verb beside it.
SPEC.md's nova-bus section states the shape at **SPEC.md:2539**:

> Every `inbox` and `wait` return has the same three parts, in this order: what
> is NEW, in full; one `INBOX OPEN carrying=<n> heard=<m>` line; and the carried
> list only if you asked for it.

and caps the carried half at **SPEC.md:2559–2561** — `--open-max`, default 20,
with one `INBOX OPEN listed=<n> and <k> more (--open-max to widen)` line where
the listing stopped. That cap is the shape the body-carrying form takes too: a
cap, a count and a remedy on one line, which is the law this document already
obeys on its own receipt.

Two things that reading leaves open, and between them they are the whole of the
first build target:

1. **"In full" there is the display line, not the body.** What a new note prints
   as is `INBOX NOTE id=<id|-> from=<name> addr=<to|cc> at=<stamp|-> path=<path>: <subject>`
   — **SPEC.md:2479** — which is id, sender, address, date, path and subject,
   and no body. That is exactly why the measured transaction pays a second read:
   the listing says a note exists and the file says what it says. The first
   build target is **the body in that same return**, under an `--open-max` style
   cap with the same count-and-remedy line, each note's text delimited so that
   nothing a body holds can be read as an event line. A reader answering a note
   then has its text from the call that told them it arrived.
2. **`--since` is already spoken for, so the form is a flag and not a verb.** In
   this tool `--since` belongs to `check` —
   `nova-bus check --bus <dir> (--full | --as <name> | --since <commit>)`,
   **SPEC.md:2397**, specified at **SPEC.md:3959** — and `check` is the gate. A
   `read --since <cursor-or-instant>` verb would give one flag two meanings
   across two verbs of one binary, and would be a second read path besides. The
   form to build is an additive flag on `inbox`, and on `wait` with it, because
   `wait` is `inbox` on a clock and shares one listing implementation. That is
   the same law this document obeys under **no second read path**.

**So the order is two items and no more: the read half first, and it is that
flag; the reply verb second.**

**Second: the reply verb specified above.** Nothing in this document is weakened
by going second and none of it is withdrawn. It goes second because its own step
is already one turn and one call, and because a reply drafted against a note the
reader needed three calls to read has removed the last and smallest part of the
chore rather than the chore.

**The larger lever is neither of these, and this slice does not duplicate it.**
The backgrounded wait whose output file has to be read is a wake problem rather
than a read problem: one turn per *change* instead of one per tick is
`nova-wake`, which is issue #239 and is specified in this repository's
SPEC-WAKE.md. That is where it stays. This document does not restate it, does
not extend it, and nothing here should be read as a second answer to it. Where
#239 lands, the discovery column of the table above goes to the wake rather than
to a poll's output file, and what the read half is then responsible for is the
other piece: the note's text, in the call that reported it.

## The tests, by name

Every MUST above has a test, and the name says which one. **The tests
are named below**, and each one names its fixture and its observable. They are
ordinary package tests against disposable local bare git remotes, inside the
existing fast tier's budget — one minute ideally, two at most — with anything heavier
declared in the certification tier rather than deleted.

**That the read half is bounded, framed and loses no note** — each fixture
uses the actual shared inbox selector. Tokens are asserted by decoding the one
schema, never by inventing abbreviated commit strings.

- `TestBodiesWithinBudgetPrintsEveryNewNoteAndSaysComplete`: three bodies,
  one per commit; byte-identical frames; final printed=3,bytes=612,oversize=0,
  gaps=0,drained=true,complete=true,next=-.
- `TestBodiesOverBudgetStopPrintingWholeNotesAndSayCompleteFalse`: exercise
  both limits and invalid zero/over-ceiling values; no partial frame; page1
  drained=false,complete=false and usable token; remaining item delivered whole.
- `TestABodyHoldingFakeStatusLinesIsDeliveredVerbatimAndParsedCorrectly`:
  fake INBOX NOTE/BODIES/END lines inside body bytes remain body data; only exact
  byte count plus separator and closing-line validation establish a frame.
- `TestRetryAfterAPartialResumesAtNext`: drain a fixed snapshot with/without
  --advance. Each accounted item appears once in that chain; new commits after H
  wait for a fresh chain. Malformed or mismatched tokens refuse without writes.
- `TestBodiesWithoutAdvanceMovesNoCursor`: complete, partial, empty and gapped
  returns leave every lane's bookkeeping and Git state unchanged; tokens work
  without a hidden writable ledger.
- `TestInboxAndWaitWithoutBodiesAreByteIdenticalToTodays`: existing golden
  bytes/statuses over both verbs and --open/--open-max/--full, including600carried
  items; no new receipt without the opt-in flag.
- `TestTwoNotesInOneCommitWithMaxNotesOneLosesNeither`: page1 prints A and
  cursor stays before c1; page2 prints B and may advance to c1; continuation uses
  pinned C0..H. Repeat with legacy id=- notes and display-group order differing
  from scan order, asserting unique path/record identities and no lost item.
- `TestContinuationSurvivesOrdinaryCursorAdvance`: c1:A,c2:B,max-notes1,
  --advance: page1 moves CURSOR to c1; its token remains valid for B. A distinct
  external cursor change instead refuses and never rewinds state.
- `TestASingleOversizeBodyIsANamedGapAndNeverALoop`: only item exceeds1MiB;
  no frame,one gap line,printed=0,oversize=1,gaps=1,drained=true,complete=false,
  next=-; first-gap remedy explicit, no cursor advance, no repeated drain call.
- `TestEarlierGapSurvivesLaterPages`: oversized c1:A then c2:B,c3:C with
  max-notes1. Each later token retains the c1gap; terminal drained=true remains
  incomplete,gaps=1. CURSOR never crosses c1. Fresh raised-budget run re-shows A
  if it fits; a hard-ceiling gap remains explicit. Repeat multiple gaps and
  assert constant-size earliest-gap state rather than an unbounded list.
- `TestSnapshotTokenValidationAndBound`: unknown versions,over8KiB,wrong reader
  or selector,non-ancestor/unavailable snapshot,invalid path/offset,frontier past
  gap or unaccounted partial commit all refuse. No token confers new read or
  write authority. The token is not asserted to authenticate a prior human read.
- `TestBodiesModeCapsTheNewSummaryLinesToo`: fifty eligible items,cap5;
  at most5 NEW summaries/gap lines with associated frames. Without --bodies,
  preserve today's fifty-line output. Include heard/receipt-only and mixed pages.
- `TestTheFrameSeparatorIsExactBytesIncludingAnEmptyBody`: bodies `ok\n`,
  `ok`, and empty produce separators only for the latter two. Assert exact bytes,
  byte-count parsing and printed=3,bytes=5; separators do not add to bytes.
- `TestBrokenOutputCannotAcknowledgeUnprintedBodies`: inject stdout failure
  before and inside frames; no cursor advancement crosses unprinted data; retry
  may re-show a body but cannot skip it. Preserve existing publish-retry rules.


## The tests, by name

Every MUST above has a test, and the name says which one. **Thirty tests

Every MUST above has a test, and the name says which one. **Thirty-six tests

Every MUST above has a test, and the name says which one. **Forty tests
are named below**, and each one names its fixture and its observable. They are
ordinary package tests against disposable local bare git remotes, inside the
existing fast tier's budget — one minute ideally, two at most — with anything heavier
declared in the certification tier rather than deleted.

**That the read half is bounded, framed and loses no note** — the first build
target, from *The read half, pinned*

- `TestBodiesWithinBudgetPrintsEveryNewNoteAndSaysComplete` — three new notes,
  one per commit, inside both limits: each `INBOX NOTE` line is followed by its
  `INBOX BODY` frame with the true byte count and a byte-equal body, and the run
  ends with one receipt line,
  `expected=INBOX BODIES printed=3 bytes=612 oversize=0 complete=true next=c3:n3`.
- `TestBodiesOverBudgetStopPrintingWholeNotesAndSayCompleteFalse` — one fixture
  over `--max-notes` and one over `--max-bytes`, plus `--max-notes 0` and a
  value over the ceiling: the return stops on a frame boundary and never inside
  one, and the continuation names the item and not `HEAD`,
  `expected=INBOX BODIES printed=2 bytes=408 oversize=0 complete=false next=c2:n2`;
  the two bad values are refusals,
  `expected=INBOX REFUSED: --max-notes 0 is not unlimited; give 1 to 1000` and
  `expected=INBOX REFUSED: --max-bytes 4194304 is over the ceiling 1048576`.
- `TestABodyHoldingFakeStatusLinesIsDeliveredVerbatimAndParsedCorrectly` — a
  body whose lines include `INBOX NOTE id=...`, `INBOX BODIES printed=9` and
  `INBOX BODY END id=<the real id>`: the frame's byte count carries the reader
  past every one of them, the body arrives byte-identical, and the note printed
  after it is parsed as the next note and not as a continuation,
  `expected=INBOX BODIES printed=2 bytes=290 oversize=0 complete=true next=c2:n2`.
- `TestRetryAfterAPartialResumesAtNext` — the run after a `complete=false`
  return, given `--after` with that return's `next=` value verbatim, prints the
  first unaccounted item as its first NEW item in full: no item is printed
  twice, none is skipped, and looping to `complete=true` yields every item
  exactly once. A run given `--after c9:nobody` refuses,
  `expected=INBOX REFUSED: --after c9:nobody names no item in this range; rerun without --after`.
- `TestBodiesWithoutAdvanceMovesNoCursor` — `--bodies` without `--advance`, on
  complete, partial and gapped returns alike: `CURSOR`, `OPEN`, `RECEIPTS` and
  `INDEX` unchanged on every lane, no commit and no push, and the return still
  carries a usable continuation, `expected=next=c2:n2`.
- `TestInboxAndWaitWithoutBodiesAreByteIdenticalToTodays` — the existing
  fixtures over both verbs with the flag absent: stdout, stderr and exit code
  unchanged, including at six hundred carried notes and with `--open`,
  `--open-max` and `--full`, `expected=` the recorded golden output of today's
  binary, byte for byte, with no `INBOX BODIES` line anywhere in it.
- `TestTwoNotesInOneCommitWithMaxNotesOneLosesNeither` (**R1**) — **one commit
  adds two notes**, `a-note.md` then `b-note.md`, whose bytewise path order is
  the scan order and whose ids are `nA` and `nB`. Read with `--max-notes 1`:
  `expected=INBOX BODIES printed=1 bytes=140 oversize=0 complete=false next=c1:nA`,
  and exactly one `INBOX NOTE` line on stdout. The next call, `--after c1:nA`,
  reads the same range again and
  prints B whole,
  `expected=INBOX BODIES printed=1 bytes=155 oversize=0 complete=true next=c1:nB`.
  With `--advance` the first run's `CURSOR` is `c1`'s parent — the fixture gives
  `c1` one — and never `c1` itself, because `c1` was cut inside; a third run with
  no `--after` is then asked about A again, which is the re-show the cursor rule
  allows and not the loss draft 5 had. A second fixture makes display order differ
  from scan order — one commit adding a note and appending to `RECEIPTS`, which
  print in different groups — and asserts the same two returns cover both items
  exactly once, in the scan order's prefix and the display's own order.
- `TestASingleOversizeBodyIsANamedGapAndNeverALoop` (**R2**) — the first NEW
  note's body is 2,097,152 bytes, over the 1048576 ceiling, so no `--max-bytes`
  can carry it. The default run opens no frame and names the gap once,
  `expected=INBOX BODY OVERSIZE id=nBig bytes=2097152 max-bytes=65536 path=from-x/2026-09-13-big.md`,
  then
  `expected=INBOX BODIES printed=0 bytes=0 oversize=1 complete=false next=c1:nBig`.
  The next call, `--after c1:nBig`, prints the following note and reaches
  `complete=true`, so the drain terminates; rerunning the **first** command
  unchanged returns the identical gap, which is why `next=` is not `-`. With
  `--advance`, `CURSOR` does not reach `c1`, and a later run at
  `--max-bytes 1048576` still finds the note NEW and still names it a gap.
- `TestBodiesModeCapsTheNewSummaryLinesToo` (**R3**) — fifty NEW notes,
  `--bodies --max-notes 5`: exactly five `INBOX NOTE` lines and five frames on
  stdout and no sixth line of either kind,
  `expected=INBOX BODIES printed=5 bytes=1020 oversize=0 complete=false next=c5:n5`.
  The same fixture **without** `--bodies` prints all fifty `INBOX NOTE` lines and
  no `INBOX BODIES` line at all, which is the released behaviour the guarantee
  leaves alone.
- `TestTheFrameSeparatorIsExactBytesIncludingAnEmptyBody` (**R4**) — three
  bodies in one return: `ok\n` (ends in a newline), `ok` (does not), and the
  empty body. Stdout is asserted byte for byte, and the separator is present for
  the last two and absent for the first:
  `expected=INBOX BODY id=n1 bytes=3\nok\nINBOX BODY END id=n1\nINBOX BODY id=n2 bytes=2\nok\nINBOX BODY END id=n2\nINBOX BODY id=n3 bytes=0\n\nINBOX BODY END id=n3\n`.
  The reader's consume-and-assert sequence is exercised rather than a search for
  the closing line, and the receipt counts body bytes only,
  `expected=INBOX BODIES printed=3 bytes=5 oversize=0 complete=true next=c1:n3`.

**That the released tool is untouched**

- `TestDraftWithoutReplyToIsByteIdenticalToTodays` — the existing form's stdout,
  stderr and exit code, over the existing fixtures, unchanged.
- `TestTheReplyFlagsAreRefusedWithoutReplyTo` — `--remote`, `--branch`,
  `--body-file`, `--draft-dir` and `--git-timeout` each exit 2 with a sentence.
- `TestPrepareAndSendAreUnchangedByThisSlice` — **the fixture is the draft
  `TestGeneratedReplyHeaderIsByteEqualToTheHandBuiltOne` produces**, fed to
  `prepare` with no tolerance applied: it validates as an ordinary draft, and
  the prepared-artifact tests pass unmodified beside it.

- `TestPrepareAndSendAreUnchangedByThisSlice` — the prepared-artifact tests pass
  unmodified, and a draft this form wrote is an ordinary input to them.

**That it is fresh**

- `TestReplyRefreshesBeforeItResolves` — **the fixture is a target note that
  exists only on the remote**, addressed to `--as` and newer than this reader's
  `CURSOR`, so it is on no `OPEN` file anywhere and the refresh is what puts it
  on the live listing. The checkout is stale, the id is unknown locally, and the
  run resolves it and writes the draft. The same run with the fetch disabled at
  the seam refuses, which is what proves the fetch is load-bearing rather than
  incidental. A second fixture asserts the other half of the set: a note the
  reader is carrying on `OPEN` and has already receipted is still a legal
  target, because heard is not answered.

  exists only on the remote.** The checkout is stale, the id is unknown locally,
  and the run resolves it and writes the draft. The same run with the fetch
  disabled at the seam refuses, which is what proves the fetch is load-bearing
  rather than incidental.
- `TestRefreshFailureIsARefusalAndNeverAStaleAnswer` — an unreachable remote, a
  branch nobody has, and a fetch that times out: exit 1 each, no draft written,
  and git's transcript under the event line.
- `TestADivergedCheckoutIsRefusedAndLosesNothing` — local commits and unrelated
  dirty files survive the refusal byte for byte.
- `TestTheRefreshWritesNothingToTheBus` — no commit, no push, and `CURSOR`,
  `OPEN`, `RECEIPTS` and `INDEX` unchanged on every lane, including after a run
  whose target was found only in the new-since-the-cursor half of the listing:
  computing that listing is a read and leaves no trace.

  `OPEN`, `RECEIPTS` and `INDEX` unchanged on every lane.

**That it resolves the right note**

- `TestUnknownReplyTargetIsRefusedAndWritesNoDraft` — an id nobody has, a path
  that does not exist and a subject that matches nothing: exit 1, and
  `--draft-dir` is still empty afterwards.
- `TestReplySubjectMatchingTwoNotesTakesTheNewestAndSaysSo` — two open notes
  with one subject; the draft names the newer id and one `DRAFT NOTE` says so
  and says how to be exact.
- `TestTargetNotOnTheOpenListIsItsOwnRefusal` — the refused target is a note
  that is on the refreshed bus and on **neither** half of the live listing:
  not carried on `OPEN`, and not new since the cursor. Four fixtures, one per
  reason: already answered, never addressed to this reader, behind the
  switch-day line, and no cursor at all. Each refusal names its own reason and
  its door — `draft --re` for the first three, an `inbox` run for the fourth.
- `TestReplyResolvesAPathForANoteWrittenBeforeIds` — a legacy target is answered
  by path, and the path is what lands on the `Re:` line, while the draft's
  filename is the derived `legacy-<12 hex>` id and holds no `/`; two legacy
  targets in one directory produce two names.

- `TestTargetNotOnTheOpenListIsItsOwnRefusal` — four fixtures, one per reason:
  already answered, never addressed to this reader, behind the switch-day line,
  and no cursor at all. Each refusal names its own reason and the `--re` door.
- `TestReplyResolvesAPathForANoteWrittenBeforeIds` — a legacy target is answered
  by path, and the path is what lands on the `Re:` line, while the draft's
  filename is the derived `legacy-<12 hex>` id and holds no `/`; two legacy
  targets in one directory produce two names.

**That the headers are the tool's and the body is the author's**

- `TestGeneratedReplyHeaderIsByteEqualToTheHandBuiltOne` — one fixture exchange,
  one hand-built reply committed as testdata, and the generated draft compared
  byte for byte. This is the test the slice is really making a claim about.
- `TestReplyDefaultsToTheSendersCanonicalNameAndNoCc` — `To` is the target's
  resolved sender; `Cc` is absent and is never inherited.
- `TestReplySubjectIsPrefixedOnceAndNeverStacked` — `Re: x` and `x` both produce
  `Re: x`.
- `TestReplyToYourOwnNoteNeedsAnExplicitTo` — refused without `--to`, written
  with it.
- `TestBodyFileIsPreservedAndItsFakeHeadersDoNotRoute` — a body whose first line
  reads `To: somebody-else` produces a note addressed as the flags said, with
  that line in the body, and the body bytes otherwise unchanged but for the
  documented note newline convention.
- `TestControlCharactersInSubjectAndToAreRefused` — including U+2028 and a bidi
  override, in each of the three caller-supplied text flags.

**That it writes outside the checkout, or not at all**

- `TestDraftInsideTheProtectedCheckoutIsRefused` — `--draft-dir` at the bus
  root, under it, and reaching it through a symlink: exit 2 each, nothing
  written, and the refusal names the resolved bus root.
- `TestReplyNeverOverwritesAnExistingDraft` — a file at the composed path is a
  refusal, and the existing file is unchanged.
- `TestTwoProcessesRacingOneDraftPathLeaveOneWinner` — **two `nova-bus`
  processes**, not two goroutines, started together against **two separate bus
  checkouts sharing one `--draft-dir`**, each composing the same filename for
  the same target, run enough times to hit the interleaving. Exactly one exits
  0; the other exits 1 with the existing-file line naming the path. The file at
  that path is byte-equal to the winner's draft, no `.tmp` is left in the
  directory, and no run produces two files. The same assertions hold with the
  advisory pre-check disabled at the seam, which is what proves the publish
  itself refuses rather than the check in front of it.
- `TestNoPartialDraftOnAnyRefusal` — every row of the refusal table, asserted
  against an empty `--draft-dir`.
- `TestReplyBodyAtTheBudgetAndOneByteOver` — at `--max-body-bytes`, at budget+1,
  and one body that is a single very long line; the over-budget run reads
  budget+1 bytes and no more.

**That the output is bounded and scannable**

- `TestReplyReceiptIsExactlyOneLineOnStdout` — over every success fixture.
- `TestReplyReceiptStaysOneLineAtSixHundredOpenNotes` — the state where an
  unbounded receipt would show.
- `TestReplyRecipientFieldCapsAtEightNamesAndCounts` — a group of twenty: eight
  names, `+12`, and the whole list present in the file `path=` names.
- `TestAReplyRefusalNamesEveryProblemInOneRun` — three mistakes, three lines,
  one run.
- `TestReplyExitCodesSeparateTheBusFromTheInvocation` — every row of the refusal
  table asserted against its stated code.

**That it is a draft and nothing more**

- `TestDraftingAReplyClosesNothing` — the target is still on the open list, with
  the same `carrying=` and `open=` counts, after a successful draft.
- `TestGeneratedReplySendsAndClosesItsTarget` — the end-to-end path against a
  disposable local bare remote: draft, `prepare`, `send --prepared`, and the
  target leaves the open list on the next read. Retrying the same prepared
  artifact retains exactly one note, which is the existing delivery evidence
  reused rather than a new claim.
- `TestASecondReplyOnOneCheckoutWaitsAndThenRefuses` — the existing checkout
  lock, met from this verb.
