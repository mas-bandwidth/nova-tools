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

1. take the checkout lock, as every verb does;
2. `git fetch <remote> <branch>` under `--git-timeout`;
3. **fast-forward the checkout**, never merge and never rebase. Every read in
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
or the **exact subject** of a note on your open list — resolved by the rules
already written for `--re`, with one addition and one restriction.

**The addition: it is resolved after the refresh**, against the tree the fetch
left. That is the whole point of the form.

**The restriction: the target must be on the caller's own open list.** A note
that is on the bus but not on your open list is refused, and the refusal says
which of the reasons it is:

- you have already answered it — a note of yours carries its id on a `Re:` line;
- it was never addressed to you, `To:` or `Cc:`;
- it is behind your switch-day line, so this reader has taken it as read;
- you have no cursor yet, so you have no open list at all.

The reason for the restriction is what this form is for: it answers what you are
carrying. A target you are not carrying is far more often a stale id copied out
of an older listing than a deliberate second reply — and a second reply that
nobody is waiting for costs a reader a turn to work out why it arrived. **It is
a restriction with a door in it**, and the refusal names the door: the existing
`draft --re` form resolves against the whole bus and is untouched, so a
deliberate reply to a closed thread is one flag away and always was.

**A subject that matches two notes resolves to the newest and says so**, which
is the rule `send` already applies to a `Re:` line naming a subject, kept
verbatim so the two cannot disagree:

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
| `--as` naming nobody, or nobody with a lane | the existing refusal, unchanged | 2 |
| `--to`, `--cc` or `--subject` carrying a control character or a line separator | the existing one-line validation's refusal | 2 |
| `--max-body-bytes` zero or negative | a budget of zero is not "unlimited"; the existing law | 2 |
| the fetch failed, timed out, or named a remote or branch that is not there | the remote, the branch, and git's transcript under the event line | 1 |
| the checkout has diverged from the named branch | the recovery command, and that nothing was written | 1 |
| `--reply-to` names no id, no path and no open subject on the refreshed bus | that threads are named by id, and that a slug is not a thread | 1 |
| `--reply-to` resolves on the bus but is not on this reader's open list | which of the four reasons it is, and that `draft --re` answers a closed thread | 1 |
| the target's sender is `--as` and no `--to` was given | a reply to your own note needs an explicit `--to` | 1 |
| the body file is unreadable | the path and the reason | 1 |
| the body is empty, or over `--max-body-bytes` | the budget and the size, read at budget+1 and no further | 1 |
| a file already exists at the draft path | the path, and that this tool never overwrites a draft | 1 |
| another `nova-bus` holds this checkout | the existing lock refusal, unchanged | 1 |

**No refusal writes a partial draft.** The file is written once, complete, by
rename into `--draft-dir`, after every check above has passed — the same rename
discipline the lane state files use, for the same reason: a kill between the
truncate and the write leaves a file that is neither the old one nor the new
one, and here it would leave half a reply that looks sendable.

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

The claim under test is narrow: **this form removes turns from answering a note,
and its own output does not cost back what it saved.** Nothing here claims a
percentage, and shorter output on its own proves nothing about total cost.

**The comparison is the same real exchange, answered both ways.** Pick a handful
of actual coordination replies — not synthetic ones, because a synthetic reply
has no stale checkout and no ambiguous subject, which is where the turns
actually go. Answer each one by hand as today, and once through this form, from
the same checkout state.

What is counted, per reply:

- **coordinator turns**, before and after, read out of the harness transcript
  rather than estimated. This is the number the slice exists to move;
- **the tokens of the tool's own output**, before and after: stdout plus stderr,
  measured **at the largest plausible state** — a reader carrying several
  hundred open notes — because a receipt that is bounded at ten notes and
  unbounded at six hundred is unbounded;
- **errors, retries and wall time**, because a form that halves the turns and
  doubles the refusals has moved the cost rather than removed it;
- **source and recipient correctness**: did the reply name the note it meant and
  reach the people it meant. A turn saved by a reply that went to the wrong
  audience is not a turn saved.

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

## The tests, by name

Every MUST above has a test, and the name says which one. They are ordinary
package tests against disposable local bare git remotes, inside the existing
fast tier's budget — one minute ideally, two at most — with anything heavier
declared in the certification tier rather than deleted.

**That the released tool is untouched**

- `TestDraftWithoutReplyToIsByteIdenticalToTodays` — the existing form's stdout,
  stderr and exit code, over the existing fixtures, unchanged.
- `TestTheReplyFlagsAreRefusedWithoutReplyTo` — `--remote`, `--branch`,
  `--body-file`, `--draft-dir` and `--git-timeout` each exit 2 with a sentence.
- `TestPrepareAndSendAreUnchangedByThisSlice` — the prepared-artifact tests pass
  unmodified, and a draft this form wrote is an ordinary input to them.

**That it is fresh**

- `TestReplyRefreshesBeforeItResolves` — **the fixture is a target note that
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
  `OPEN`, `RECEIPTS` and `INDEX` unchanged on every lane.

**That it resolves the right note**

- `TestUnknownReplyTargetIsRefusedAndWritesNoDraft` — an id nobody has, a path
  that does not exist and a subject that matches nothing: exit 1, and
  `--draft-dir` is still empty afterwards.
- `TestReplySubjectMatchingTwoNotesTakesTheNewestAndSaysSo` — two open notes
  with one subject; the draft names the newer id and one `DRAFT NOTE` says so
  and says how to be exact.
- `TestTargetNotOnTheOpenListIsItsOwnRefusal` — four fixtures, one per reason:
  already answered, never addressed to this reader, behind the switch-day line,
  and no cursor at all. Each refusal names its own reason and the `--re` door.
- `TestReplyResolvesAPathForANoteWrittenBeforeIds` — a legacy target is answered
  by path, and the path is what lands on the `Re:` line.

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
