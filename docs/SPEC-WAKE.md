# nova-wake — specification

One verb at the **attention layer**. A window that coordinates other lines spends
its turns on a clock: it sleeps, wakes, looks at three places, finds nothing, and
sleeps again. Every one of those cycles is a model turn, and a turn that learns
nothing is the most expensive kind of nothing there is. `nova-wake` is that cycle
inverted. It is **one blocking call** that returns the moment something the window
cares about has changed, and otherwise at a deadline the caller named — so the
harness itself wakes the session, because that is what the return of a tool call
is, and the window pays one turn per *change* rather than one turn per *tick*.

It watches three sources — a bus inbox, the checks on a set of entries, and report
files written by other lines — and it says what moved. It does not act on any of
them. **Everything it prints is data**: a note it relays is not an instruction, a
failing check is not a verdict about whose fault it is, and a report file is prose
somebody else wrote. A watcher that acted on what it saw would be a window with no
person in it.

This spec is normative. It is a sibling of [SPEC.md](../SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line guarantee,
the field escape, the cap-and-count law — governs here unchanged except where this
document says otherwise, and it says so by name in one place only (**Exit codes**).
If the code and this document disagree, one of them has a bug, and the tests
decide which.

## The two lessons of 2026-09-11

Both of these were paid for in one day, by one window, in Fable turns.

| what happened | what it cost | the rule it bought |
|---|---|---|
| A window ran a five-minute sleep tick to find out whether anything had moved. Most ticks found nothing. | Every tick was a model turn on the most expensive model in the fleet, spent learning that the world was exactly as it had been left. The turns were gone and the window had not coordinated anything with them. | **A watcher blocks; it does not tick.** One call, one return, one turn per change. The clock lives inside the tool, where a poll costs a subprocess and not a turn. |
| The state for one watched entry was stored as a **tab-joined** string whose last field was often empty. Reloading it dropped the trailing empty field, so the reloaded value never equalled the freshly computed one. | Every poll reported a change. The watcher woke the window every interval, forever, with nothing to say — a false wake is worse than a missed one, because the window learns to stop reading. | **A state value round-trips or it is not state.** The separator may not be a character the reader can eat, the comparison is byte-for-byte over the stored form, and a test writes state, reloads it, and asserts a second identical poll reports **no change**. |

A third failure was paid the same day and is in **The races** below, because it is
a race rather than a lesson about clocks: the watcher's own test run consumed five
bus notes that the window then had to be told about by hand.

## The verb

```
nova-wake watch --state <file> --max <duration> --on-deadline <word> --interval <duration> [--max-lines <n>] [--baseline]
      [--bus <dir> --as <name> --receipt-max-words <n> [--advance-cursor --remote <name> --branch <name>]]
      [--line <name> ... [--offline-after <duration>]]
      [--entry <repo>#<n> ... --entry-interval <duration>] [--final-only] [--gh-timeout <seconds>]
      [--reports <dir> ...]
nova-wake quickstart --state <file> [--max <duration>] [--on-deadline <word>]
nova-wake help
```

One verb that watches, one that shows a first run, and `help`. There is no daemon,
no `--detach`, no background mode and no second binary: the whole point is a call
that a harness is already waiting on.

**At least one source, named.** A `watch` with no `--bus`, no `--entry` and no
`--reports` is exit 2 and `refusing to guess`: a watcher with nothing to watch is
a `sleep` with a longer name, and it is the one invocation that would look like it
was working.

**No guessed anything, with two exceptions.** There is no default state file, no
default bus, no default bus name, no default entry, no default report directory,
no default repository, no default poll interval and no default action at the
deadline. Each missing one is exit 2 and `refusing to guess`. The exceptions are
`--max-lines`, which defaults to **40**, and `--gh-timeout`, which defaults to
**45 seconds** — neither a fact about this window's world that only this window
can supply, which is the test the rule is really making. A line cap is this repo's
own law (SPEC.md, Conventions) and is 20 everywhere a listing is a listing, and 40
here because a wake line is not a listing: it is the whole of what the window
learns from this return. `--interval` had a default of 30 seconds in an earlier
draft and lost it on 2026-09-11: the right cadence is a fact about the watched
thing's rate, which only the caller knows (**The rules of the last two days**,
rule 3). `--max` gets **no** default for the same reason `nova-bus wait --timeout`
gets none: a deadline is the one thing the caller must state, because a watcher
with no deadline is a window that is stuck rather than waiting and nobody outside
can tell the two apart. (The prototype defaulted it to 1200 seconds. That default
is the difference between a tool that ends and a tool that has to be killed.)

**The ceiling is the harness's, and 20 minutes is the recommendation.** A watch
runs inside a tool call and every harness kills a call that runs too long, so a
`--max` above the harness's limit does not watch longer — it is killed with
nothing said at all, which loses both the changes seen and the state not yet
written. `--max` above **60m** is refused with that sentence and the advice to ask
your harness what its limit is and sit under it. `--interval` will not go below
**5s**, because a poll is a `git fetch` and an API call against somebody else's
server.

**The only programs it starts are `nova-bus`, `gh` and `git`**, all named here,
all under a timeout, all one at a time. `git` is started only for `--line`,
read-only, against the bus checkout (rule 2 below). It opens no socket of its own, resolves no
host, and has no opinion about what a bus or a forge is beyond what those two
programs tell it. A source whose program is missing from `PATH` is a **change**
on the first poll, not a refusal — see **Sources** — because a window that cannot
see its bus needs to hear so now.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the watch ran: **either** something changed **or** the deadline arrived |
| 2 | could not run: a missing or malformed flag, no source named, a `--max` over the ceiling, an unreadable or unparsable state file, a second watcher on the same state file, or a source that failed three polls in a row (rule 8) |

**This is the one deviation from SPEC.md's Conventions table, and it is that there
is no 1.** Nothing here asserts anything, so nothing here can say NO: a watcher is
a report and never a gate. A deadline is not an error — it is the answer *nothing
yet*, exactly as `WAIT TIMEOUT` is — and a change is not a failure even when what
changed is a red check, because *red* is news and news is this tool's whole output.
A caller that needs to know which of the two happened reads the **second token of
the last line**, `WAKE CHANGE` or `WAKE QUIET`, and never the exit code. A `WAKE
BROKEN` last line is the third case, and it is exit 2, because a watch whose
source went away did not run to its deadline (rule 8). Exit 1
is not used and is reserved: if a later version ever gates on something, it will
take 1 and this table will say what it gates on.

**An unparsable state file is exit 2 and never a silent cold start.** The state
file is the only thing standing between this tool and the false-quiet failure: a
run that cannot read it, decides to start cold, and reports nothing has a
*correct-looking* first poll and has silently swallowed everything that moved
while no watcher was running. So it refuses, names the file and the parse error,
and says the two ways out — repair it, or pass a new `--state` path and accept a
cold start on purpose.

## Output grammar

```
WAKE as=<name|-> max=<d> interval=<d> sources=<bus,entries,reports> state=<file> cold=<true|false>
WAKE CHANGE after=<d> polls=<n> bus=<n> entries=<n> reports=<n> lines=<n>
WAKE QUIET after=<d> polls=<n> default=<word>: deadline, default taken
WAKE BROKEN source=<bus|entries|reports> failures=<n> since=<stamp>: <reason>
WAKE BUS id=<id|-> from=<name> addr=<to|cc> at=<stamp|-> commit=<sha|-> path=<path>: <subject>
WAKE BUS LINE <the bus's own line, verbatim>
WAKE BUS STANDING <the bus's own line, verbatim>
WAKE ENTRY <repo>#<n> state=<state> fail=<n> pending=<n> pass=<n> final=<true|false> failing=<names|->
WAKE ENTRY <repo>#<n> unreadable: <reason>
WAKE REPORT path=<path> lines=<n> bytes=<n> <new|modified>
WAKE LINE name=<name> state=<OFFLINE|BACK> last=<stamp|-> silent=<d> commit=<sha|->
WAKE SOURCE <bus|entries|reports> read=<n> suppressed=<n> relayed=<n> standing=<n>
WAKE NOTE <something true about this run that is not a change>
WAKE POLL <source>: <reason one poll failed, which was not fatal>
WAKE MORE kind=<bus|entry|report> shown=<n> total=<t> <remedy>
WAKE REFUSED: <reason>
```

`WAKE CHANGE`, `WAKE QUIET` and `WAKE BROKEN` are the **last** line and the three
possible verdicts; the opening `WAKE` line is the **first**, printed before anything is
waited on, so a transcript shows the call began and what it was told to do — a
tool call that prints nothing for twenty minutes and then prints everything is,
while it runs, indistinguishable from one that has hung. `WAKE` lines and the
informational tokens go to stdout; `WAKE REFUSED` and `WAKE POLL` go to stderr.

Every path, subject, reason, entry name and relayed bus line is rendered through
`internal/oneline`, so a note whose subject carries U+2028 arrives as one escaped
line rather than two, and a `key=value` field is one whitespace-free token as the
field law requires. A relayed line is escaped and **never shortened below its
own tail budget**; see **the unrepeatable line** below for the one thing the cap
may not drop.

The verdict line counts **what changed**, per source, and those three numbers are
about the WORLD and not about the output: the listing above them is capped and the
counts never are. That is SPEC.md's law, stated there once and met here the same
way, through `internal/bounded`.

## Sources

A source is a thing with a **state value** and a rule for what makes two state
values different. That is the whole design: poll, compute a value, compare it to
the stored one byte for byte, and a difference is a change. Nothing here tries to
decide whether a change is *important* — a window asked to be woken on a change
and importance is the window's to judge.

### The bus inbox — a NOTE is always a change, and the status line is never filtered

The bus source runs `nova-bus inbox --bus <dir> --as <name> --receipt-max-words
<n>` under `--gh-timeout`'s sibling budget and reads its stdout and stderr
together, line by line.

**Classification is by first tokens, and the default case prints.** A line
beginning `INBOX NOTE` is a change, always, and is relayed as `WAKE BUS`. The
bookkeeping tokens — `INBOX OPEN`, `INBOX OK`, `INBOX CURSOR`, `INBOX SCOPE`,
`INBOX LEGACY` — are counted and not printed, because they say the same thing
every poll. **Every other line this tool does not recognise is printed verbatim,
under `WAKE BUS LINE`, and wakes the window.** That includes `INBOX REFUSED`,
`INBOX FAIL`, `INBOX UNREADABLE`, `INBOX UNADDRESSED`, `INBOX SWITCH`, a `git`
transcript, a line from a future version of `nova-bus` this tool has never heard
of, and anything on stderr at all.

**Never filter the status line.** A watcher that kept only the tokens it knew
about once dropped an `INBOX REFUSED` line and gave the window thirty minutes of
confident quiet over a bus that was refusing every read. The rule follows from
that and is absolute: an allow-list decides what is *suppressed*, never what is
*shown*, and a line this tool cannot classify is a line this tool prints.

**A refused or error line never re-wakes and is never silent.** The first sighting
of an unrecognised line is a change. A subsequent run that sees the *same* line
again — the same bytes, from the same source — prints it under `WAKE BUS
STANDING` and it does **not** count toward `bus=<n>` and does **not** by itself
make the run a `WAKE CHANGE`. A bus that has been refusing for an hour would
otherwise wake the window every interval with one sentence it has already acted
on, which is the false-wake failure arriving by another road; and dropping the
line instead would be the false-quiet failure. So: shown every time, woken on
once. The sighting memory lives in the state file, is bounded (see **State**), and
when an entry is evicted the line is a change again — which is the safe direction
to be wrong in.

**`--advance-cursor` is off, and a watcher may not advance a cursor that is not
its own.** This is the rule the third failure of 2026-09-11 bought: a test run of
the watcher read the window's bus *with* `--advance`, which consumed five notes,
and because the run was a test nobody read its output — so five notes were on no
open list, in no transcript, and the window had to be told about them by a person.
A cursor is a claim about what a reader **has been shown**, and a watcher that
advances a reader's cursor is making that claim on the reader's behalf.

Therefore:

- `nova-wake` passes `--advance` to `nova-bus` **only** when `--advance-cursor` is
  given explicitly, with `--remote` and `--branch`, and never by default.
- `--advance-cursor` without `--as` is exit 2. The `--as` name **is** the claim:
  advancing is permitted only for the line the window itself reads as, and a
  watcher run on somebody else's behalf does not get a flag for it. There is no
  `--as-other`, no `--probe-as`, and no flag that advances one name's cursor from
  another name's watch.
- A run with `--advance-cursor` holds a lock named by `(bus, as)` for the whole
  call — not per poll, unlike `nova-bus wait`, because the thing being protected
  is a cursor this run is moving rather than a checkout it is reading — and a
  second watcher over the same pair is `WAKE REFUSED`, exit 2, naming the holder.
- **Mail consumed is mail printed.** A run that advanced a cursor prints every
  `WAKE BUS` line it consumed, in full, and those lines are the **unrepeatable
  line**: the cap may not drop them. See below.
- A run **without** `--advance-cursor` consumes nothing, so the same note is a
  change on every poll until the window reads it properly. That is correct and it
  is why `--advance-cursor` exists — but the default is the one that cannot lose
  mail.

**The bus is the exception to the cold-start rule.** See **The cold-start rule**.

### Entries and their checks — counts, and a FINAL state when you ask for it

An **entry** is something on a forge with a state and a set of hosted checks:
`--entry mas-bandwidth/schema#942`. It is named `--entry` and not `--pr` because
the repository is part of the name — the prototype took `--prs 942,951` against
one `--repo`, which makes a second repository a second invocation and a typo in
one number a watch on somebody else's work — and because the tool has no model of
what the number means beyond "the thing `gh` will tell me the state of". Entries
are polled in batches, at most **8** outstanding calls, with one `gh` process per
entry per poll and no pagination.

The state value is, in order: the entry's own state (`OPEN`, `MERGED`, `CLOSED`),
the count of **fail**, the count of **pending**, the count of **pass**, and the
**sorted names of the failing checks**. A change in any of the five is a change.

- The bucket rule: a hosted check that has not completed is `pending`; one that
  completed with success, neutral or skipped is `pass`; anything else is `fail`.
  A non-check status context is `pending` while pending or expected, `pass` on
  success, `fail` otherwise. A check with no name is named by its context, and a
  check with neither is `?` — a nameless check is still a count.
- **A hosted-check change is a change.** The failing names are in the value
  precisely so that a red run replaced by a *different* red run with the same
  arithmetic wakes the window: `fail=1` over `windows / build` is not the same
  news as `fail=1` over `race`, and the arithmetic cannot tell them apart.
- `merged` and `closed` are changes, and they are the changes most likely to end
  the window's wait, so the entry's own state is the first field of the value.
- An entry that cannot be read — `gh` missing, a timeout, a rate limit, an
  unparsable answer — has the state value `unreadable:<reason>`, which is a state
  value like any other: the first sighting is a change and prints `WAKE ENTRY
  ... unreadable:`, and the same reason on the next poll does not re-wake. The
  same shown-every-time, woken-once rule as a standing bus line, for the same
  reason.

**`--final-only` exists because check counts churn every poll.** A busy entry
moves `pending=11 pass=1`, `pending=9 pass=3`, `pending=6 pass=6` — every one of
those is a real change in the value and a real wake, and a window woken eleven
times on one entry learns nothing eleven times. Under `--final-only` an entry's
change is reported only when its state is **FINAL**, and FINAL has exactly one
definition:

> An entry is FINAL when it is no longer `OPEN` — `MERGED` or `CLOSED` — **or**
> when `pending=0` and it has at least one check. `pending=0 fail=0` is green;
> `pending=0 fail>0` is red. Both are final; both wake.

Green and red are both final because the window's next action differs between
them and is needed in both cases, and because a watcher that woke only on green
would be a watcher that sleeps through every failure. An entry with **no** checks
at all is never final by the count rule — `pending=0 pass=0 fail=0` is the state
of an entry whose checks have not been created yet, and calling that green is the
one arithmetic mistake here that would merge something red. It becomes final by
changing state, or the window asks without `--final-only`.

`--final-only` **suppresses the wake, not the state**: the value is stored on
every poll as always, so a run that ends at its deadline has an up-to-date state
file and the churn is never re-reported as news later. And it is per entry, not
per run: one final entry wakes the run even while four others churn.

### Report files — new or modified `RESULT.md` under the directories you name

`--reports <dir>` may be given more than once. Under each one the source watches
every `RESULT.md` at any depth, and nothing else: one agreed file name, because a
watcher that woke on every file another line touched would wake on its own
scratch.

**There are no default report directories.** The prototype hardcoded
`$HOME/deepseek-working-*/jobs/*/RESULT.md` and `$HOME/freddy-working-*/jobs/*/`,
which is three guessed paths, a guessed `$HOME`, and two other lines' layouts
frozen into a tool. Every directory is the caller's, per run.

The state value of a report file is `mtime:size`, and the change kinds are `new`
(no stored value) and `modified` (a different one). A file that disappears is
**not** a change and its key is kept: a job directory being rebuilt is not news,
and the window does not want to be woken by a `rm`. The per-file count of lines on
the `WAKE REPORT` line is read at report time and is there so the window can tell
a stub from a finding without opening it.

**The known limit, named rather than discovered:** `mtime:size` cannot see a
rewrite that preserves both, and some tools write identical-length updates within
one second. A content digest would close it and costs a read of every watched file
on every poll; it is a v2 item behind a flag, and until it exists this paragraph
is the answer to *why did it not wake*.

## State

One file, named by `--state`, holding a flat map of key to value: one entry per
watched thing, namespaced by source — `bus:line:<bytes>`, `entry:<repo>#<n>`,
`report:<path>`. It is written **after every poll**, through a temporary file in
the same directory and an atomic rename, so a call killed by the harness mid-poll
leaves either the previous state or the new one and never half of either.

- **It is this tool's own file, in a format this tool chose**, and it is not a
  record of anything: delete it and you get a cold start, which is a correct if
  noisier watch. Nothing else may read it as truth.
- **A value is one line and round-trips.** The separator inside a composed value
  is `|` and not a tab, and a stored value compares equal to a freshly computed
  one byte for byte. The second lesson of 2026-09-11 is this sentence; the test
  that pins it writes, reloads, polls again and asserts quiet.
- **It is bounded.** The `bus:line:` sighting memory is the only part that grows
  with things that happen rather than with things being watched, so it is an LRU
  of **300** entries with the least recently *seen* evicted first. The prototype
  deleted **all** of them once the count passed 300, which turns every standing
  error back into a change at once — a thundering false wake at exactly the moment
  the bus was noisiest.
- **One writer.** A state file written by two concurrent runs is a race with a
  silent outcome; see **The races**.

## The cold-start rule

**A first run is not a change.** With no state file, the first poll of `--entry`
and `--reports` **records** the world and reports nothing: every entry and every
report file is new, so a cold watch would otherwise return instantly with a
listing of everything that exists, which is not what *wake me on a change* means
and is a listing nobody reads. `cold=true` on the opening `WAKE` line says the
run is in this state, and `--baseline` turns it off for the caller who does want
the world listed once — that is what `quickstart` passes.

**Once state exists, the first poll of a later run does report what moved while no
watcher was running.** That is the whole reason state is kept between calls: the
gap between two calls is the gap in which a window is thinking, and news that
arrived in it is the news it most needs.

**The bus is the exception, and it is not a choice.** A bus read with
`--advance-cursor` *consumes* what it reports; a cold first poll that swallowed
five notes to stay quiet would be exactly the failure in **The races** below,
caused deliberately. So a bus `INBOX NOTE` is a change on the first poll of a
cold run as on any other, and `--baseline` does not apply to it. **Never swallow
mail to make a quiet first poll.** A bus read *without* `--advance-cursor`
consumes nothing, and reports the same note on every poll until it is answered —
also not swallowed.

An unrecognised bus line on a cold first poll is a change for the same reason: it
is unrepeatable news about a bus that may be broken right now.

## Bounded output

The cap is `--max-lines`, default **40**, counted over the item lines — `WAKE
BUS`, `WAKE ENTRY`, `WAKE REPORT` — and never over the verdict, the opening line,
a `WAKE NOTE` or a refusal. Past it, one line per kind:

```
WAKE MORE kind=<bus|entry|report> shown=<n> total=<t> <remedy>
```

`0` means all; a negative cap is refused, because `0` already means all and a
negative number is a typo with two readings. The cap is **per kind**, as SPEC.md
requires, because a flat cap over a concatenated stream means the loud kind eats
the quiet one and the quiet one is the finding the window did not already know
about: forty churning entries must not hide one note.

The remedy names the flag that lifts the cap, and where the source has a file that
holds the whole list it names that instead — for the bus, the reader's own `OPEN`
file; for reports, the directory.

**The unrepeatable line is exempt from the cap.** A line whose only record is this
output may not be elided by a count, ever. There is exactly one such line today:
a `WAKE BUS` note relayed by a run with `--advance-cursor`, whose cursor has moved
past it. The prototype capped those lines and printed `state advanced; poll again`
under them, which is a false promise — polling again cannot return a note the
cursor has passed — and is the mechanical shape of the five lost notes. So: if the
consumed notes alone exceed the cap, they are all printed anyway, and one `WAKE
NOTE` says the cap was exceeded by mail that cannot be shown twice. A window
drowning in relayed notes has a real problem and the remedy is `--advance-cursor`
off, which the note names.

## The races

Four, all observed or trivially reachable, each with the rule that closes it.

**Two watchers advancing one bus cursor.** Two calls with `--advance-cursor` over
one `(bus, as)` pair interleave, each consuming the notes the other should have
relayed, and each returns a partial listing that looks complete. Closed by the
lock above: one advancing watcher per `(bus, as)`, second is `WAKE REFUSED` exit
2 naming the holder. A non-advancing watcher takes no lock and cannot lose
anything, so any number may run.

**A state file written by two runs.** Two watches sharing one `--state` path each
write the whole map, so the later write erases everything the earlier one learned
— including sighting memory, which silently resurrects standing errors, and
including entry values, which silently re-report churn. Closed by one writer: a
`watch` takes an exclusive lock on `<state>.lock` for the duration of the call and
a second run over the same path is `WAKE REFUSED` exit 2 naming the holder and
saying the fix, which is a state file per watch. Not closed by atomic rename — the
rename makes each write whole, and two whole writes of different truths is the
race.

**Mail consumed by a probe and not relayed.** The one that actually happened: a
test run of the watcher read the window's bus with `--advance`, consumed five
notes, and reported into a transcript nobody read. Closed three ways at once,
because one way was not enough — `--advance-cursor` is off by default; it requires
`--as` and is permitted only for the window's own name; and a run that advances
prints every consumed note uncapped. **A watcher may not advance a cursor that is
not the window's own**, and there is no flag that lets it.

**A harness kill between the poll and the write.** A call killed at its ceiling
after observing a change but before writing state re-reports that change on the
next call. This race is closed in the **safe** direction deliberately: state is
written after every poll and before the verdict is printed, so the duplicate is a
repeated wake and never a lost one. A window told the same news twice reads twice;
a window told it never does not.

## The rules of the last two days

Eight rules, 2026-09-09 to 2026-09-11. Each came from a hurt and each is written so
a test can be built from it. Where a rule changes a sentence above, that sentence
has been changed to match, and this section is the reason. Where a rule names a
prototype behaviour, it is listed by number in **What the prototype does that this
spec forbids**.

1. **Every wait has a written deadline and a default action.** `--max` is the
   deadline and `--on-deadline <word>` is the action. Both are required. A `watch`
   missing either is exit 2, `refusing to guess`, naming the one it lacks. The
   tool never waits past `--max`. A watch that reaches its deadline prints exactly
   one verdict line, `WAKE QUIET after=<d> polls=<n> default=<word>: deadline,
   default taken`, and exits 0. `QUIET` is the status word the caller reads. The
   tool takes no action itself. `<word>` is what the caller said it would do,
   echoed on the opening line and on the verdict, so the transcript records the
   decision. The caller decides. (The hurt: nineteen orphaned shells on 2026-09-09
   were waits with no deadline, and a wait with no default is a window that
   returns and then has to think about what it meant to do.)

2. **Ten minutes silent is offline.** `--line <name>` names a line to watch and
   may be given more than once. It requires `--bus`. A line's **last sign** is the
   stamp of the newest commit on the bus checkout's branch whose author is that
   name: a note, a receipt, or any other commit the line made. A line whose last
   sign is older than `--offline-after` is reported once, as a change, on one
   line: `WAKE LINE name=<name> state=OFFLINE last=<stamp> silent=<d>
   commit=<sha>`. `--offline-after` defaults to **10m**. It is the one duration
   here with a default, because it is the family's rule and not a fact about one
   window: every line must agree about it, and it is the same number as
   `nova-board --stale`. The tool never repeats an OFFLINE line while nothing
   changes: the state file holds `line:<name>` as `<sha>|OFFLINE`, and a later
   poll with the same value prints nothing. A new sign from the line is a change
   once, `state=BACK`. A line with no commit on the branch is `last=- commit=-`
   and is OFFLINE at the first poll after the watch has run for
   `--offline-after`. The sign is read with `git log` against the bus checkout,
   read-only, under `--gh-timeout`, and nothing else is read. (Glenn, 2026-09-10:
   reassign a silent line's items after about ten minutes. Johnny ran out of
   credits at 00:35Z and the board said he held his items for an hour.)

3. **Poll cadence matches the watched thing's rate.** `--interval` has no default
   and is exit 2 when missing. It is the cadence for the bus, for `--line` and for
   report directories. Entries have their own cadence, `--entry-interval`,
   required whenever `--entry` is given, and it is the expected length of the
   hosted run: an entry is polled no more often than that. Both intervals have the
   5s floor. The loop sleeps until the earliest due source, so a 5s bus interval
   beside an 8m entry interval polls the bus every 5s and the entry every 8m.
   (Amdahl, on Glenn's word: an 8-minute CI run deserves one check at 8 minutes,
   not eight checks at one minute. The prototype's 30-second default made sixteen
   `gh` calls per entry per eight-minute run, and every one learned nothing.)

4. **It finds itself by a file, never by `pgrep`.** The tool never lists
   processes, never reads its own command line back from the process table, and
   never uses `/tmp` or `$TMPDIR`. Its only files are `--state`, the fixed-name
   temp file beside it, and `<state>.lock` beside it, which holds the pid of the
   run that took it. A second `watch` over the same `--state` path is `WAKE
   REFUSED`, exit 2, on one line naming the holder's pid and the path. Nothing the
   tool does touches the filesystem outside the directories it was given.
   (2026-09-09: a loop that pgrepped its own command line matched itself and never
   ended.)

5. **Wake output is bounded and never carries a body.** One line per event. A
   relayed note is its id, sender, stamp, commit, path and subject, never its body.
   A report is its path and size, never its contents. An entry is its counts and
   failing names, never a log. Past `--max-lines` events of one kind in one
   interval, one `WAKE MORE` line counts the rest. `--max-lines` is the N. The
   bound holds at the largest plausible state: 200 notes, 50 entries, 100 report
   files and 20 lines changing in one interval print at most `4 * --max-lines +
   10` lines. (Glenn, 2026-09-09: tool output costs tokens; test at the largest
   plausible state.)

6. **What woke you is named.** Every change line carries the identity of the thing
   that changed: a note's id and commit sha, an entry's repository, number and new
   state, a report's path, a line's name and last commit. The words `something
   changed` never appear in this tool's output, and no change line is printed
   without its identity field. (A wake that says only that the world moved sends
   the window back to look at all three places, which is the tick this tool
   replaces.)

7. **Never filter the status line.** Every line the bus source reads is classified
   as suppressed, relayed or standing, and every line is counted. Nothing is
   dropped silently. Once per run, before the verdict, `WAKE SOURCE bus read=<n>
   suppressed=<n> relayed=<n> standing=<n>` prints the four counts, and they add
   up: `read` equals the sum of the other three. The suppress list decides what is
   hidden and never what is shown; a line the tool cannot classify is relayed.
   (2026-09-10: a grep that kept only NOTE lines dropped the `INBOX REFUSED` line
   and gave the window thirty minutes of false quiet.)

8. **The watcher's own failure is loud.** A poll of one source that fails is one
   `WAKE POLL` line on stderr and the watch goes on. The **third consecutive**
   failure of the same source ends the watch: the verdict is `WAKE BROKEN
   source=<s> failures=3 since=<stamp>: <reason>`, exit 2, because a watcher that
   cannot see its source is not watching, and a `WAKE QUIET` from it would be a
   lie. A success resets the count. A source fails when the bus's `nova-bus` exits
   other than 0 or times out, when every entry is unreadable in one poll, or when
   every `--reports` directory is unreadable. Three is fixed and not a flag: it is
   a fact about the tool, not about the window.

## Tests this spec demands

One test per rule above, named for the rule, beside the tests the work list names.
Each is proven able to fail by a mutation before it is trusted.

1. `TestAWatchNamesItsDeadlineAndItsDefault`: no `--max` is exit 2 naming
   `--max`; no `--on-deadline` is exit 2 naming `--on-deadline`; both missing is
   one run naming both; a watch whose sources never change ends at `--max` with
   exactly one `WAKE QUIET` line carrying `default=<word>` and the tail `deadline,
   default taken`, exit 0. The clock is injected.
2. `TestTenMinutesSilentIsOfflineOnce`: a bus checkout whose last commit by one
   line is eleven minutes old prints one `WAKE LINE ... state=OFFLINE` with that
   commit's stamp and sha; a second poll with nothing changed prints nothing for
   that line and is not a change; a new commit by the line prints `state=BACK`
   once; nine minutes is not offline.
3. `TestTheEntryIntervalIsTheRunLength`: with `--interval 5s --entry-interval 8m`
   over an injected sixteen-minute clock the bus is polled 192 times and the entry
   twice; `--interval` missing is exit 2; `--entry` without `--entry-interval` is
   exit 2.
4. `TestASecondWatcherOnOneStateFileRefusesOnOneLine`: two watches over one
   `--state`; the second prints one `WAKE REFUSED` line naming the pid and the
   path, exit 2; the source tripwire finds no `pgrep`, no `ps`, no `/proc`, no
   `os.TempDir` and no literal `/tmp` in the package.
5. `TestWakeOutputIsBoundedAtTheLargestPlausibleState`: 200 notes, 50 entries,
   100 reports and 20 lines changing in one poll print at most `4 * --max-lines +
   10` lines, measured in lines and bytes on stdout plus stderr, and no printed
   line contains a note's body or a report's contents.
6. `TestWhatWokeYouIsNamed`: every `WAKE BUS`, `WAKE ENTRY`, `WAKE REPORT` and
   `WAKE LINE` line in a mixed run carries its identity field, and `something
   changed` appears nowhere on stdout or stderr.
7. `TestEveryBusLineIsClassifiedAndCounted`: a bus transcript of twelve lines, one
   of them `INBOX REFUSED`, yields `WAKE SOURCE bus read=12` with the three counts
   summing to twelve and the `REFUSED` line relayed verbatim; a mutation that
   drops one line turns the test red.
8. `TestThreeFailedPollsEndTheWatchLoudly`: a bus that exits 1 three times in a
   row ends the watch with `WAKE BROKEN source=bus failures=3`, exit 2; two
   failures then a success is two `WAKE POLL` lines and the watch goes on to its
   deadline.

## Known limits

- **It cannot make the window act.** It returns, the harness wakes the session,
  and what the session does next is the session's. The lost-note failure this tool
  closes was never that a note went missing; it was that nobody came back to look.
- **It watches three sources and no others.** No filesystem watch of arbitrary
  trees, no log tailing, no process liveness, no schedule. `--line` is not a
  fourth source: it is a view over the bus checkout's commits (rule 2). A fourth source is a
  spec change, and a flag that ran an arbitrary command each poll would make this
  a `cron` with a blocking call, which is the thing it replaces.
- **Its view of a forge is `gh`'s.** Rate limits, authentication and a forge's own
  eventual consistency are `gh`'s behaviour, reported verbatim and never retried
  around. A check that the forge has not created yet is invisible, which is why
  the no-checks case is not final.
- **`mtime:size`** is the report identity; see the limit named above.
- **Quiet is only as true as the sources.** `WAKE QUIET` means *these sources said
  nothing in this window*, not *nothing happened*. It is a report and not a
  guarantee, as `WAIT TIMEOUT` is.

## What it deliberately does not do

- **No default source, no default bus, no default state file, no default
  repository.** Three of the prototype's paths were one line's home directory and
  one team's issue tracker, and a tool with those in it is a tool only that line
  can run.
- **No reading a coordination file to invent its own arguments.** The prototype
  read a merge lane's `lane.json` to decide which entries to watch when `--prs`
  was absent. That is a guessed path and a guessed scope at once, and it means a
  watch's subject can change under it because a file somebody else owns changed.
  Entries come from flags.
- **No `--advance` by default, and no cursor moved for another name.** Stated
  three times above, which is the right number for the failure it closes.
- **No writing anywhere but its own state file.** It sends nothing, receipts
  nothing, comments on nothing, and its only write outside `--state` is the one
  `nova-bus` makes when the caller asked for `--advance-cursor`.
- **No acting on a report.** `RESULT.md` is prose another line wrote, relayed as a
  path and a size. Nothing parses it, and nothing in it is an instruction.
- **No exit code for *what* changed.** One bit of news in an exit status is a
  grammar that cannot grow; the second token of the last line is the answer and is
  readable by a person as well as a scanner.
- **No retry loop around a forge.** A failed poll is one `WAKE POLL` line on
  stderr and the watch goes on, still bounded by the deadline. The third failed
  poll in a row of one source ends the watch as `WAKE BROKEN` (rule 8). A failed poll on
  the *first* call is not special here, unlike `nova-bus wait`'s first fetch,
  because the unreadable state value is itself the news and the window gets it as
  a change.

## Work list — building it in Go under `cmd/`, like `nova-bus`

Standard library only, no third-party imports, no hardcoded paths, and the repo's
shared packages used rather than re-spelled.

1. **`internal/wake/state.go`** — the state map: load, save through a temp file and
   rename, the `|` composition, the 300-entry LRU over `bus:line:` keys, and an
   exclusive lock on `<state>.lock` reusing `internal/bus`'s lock (`lock.go`,
   `lock_unix.go`, `lock_other.go`) rather than a second lock implementation.
   Tests: round-trip quiet on a second poll (the 2026-09-11 lesson), eviction
   order, an unparsable file is an error and not a cold start, a killed write
   leaves the old file intact.
2. **`internal/wake/source.go`** — the `Source` interface: `Poll(ctx) ([]Item,
   error)` returning items that each carry a state key, a state value and a
   display line, plus whether the item is *unrepeatable*. Every change decision is
   one comparison in one place, so a fourth source cannot invent its own.
3. **`internal/wake/bus.go`** — run `nova-bus inbox` under a timeout; classify by
   first tokens with a **suppress** list and a printing default case; the standing
   vs first-sighting split; the `--advance-cursor` guard (`--as` required, lock
   held for the call, consumed notes marked unrepeatable). Tests: an `INBOX
   REFUSED` wakes and is relayed verbatim; a repeat is `STANDING` and does not
   wake; an unknown future token prints; `--advance-cursor` without `--as` is exit
   2; a consumed note is never elided by the cap.
4. **`internal/wake/entry.go`** — the `gh` call per entry, the bucket rule, the
   five-field value, batching at 8, `unreadable:<reason>` as a value, and
   `--final-only`'s FINAL predicate as a pure function over the value. Tests:
   churn wakes without the flag and does not with it; green and red both wake
   under it; an entry with no checks is not final; a different failing name with
   the same counts wakes; a `gh` timeout is a change once and not twice.
5. **`internal/wake/report.go`** — walk each `--reports` directory for `RESULT.md`
   at any depth, `mtime:size` values, `new` vs `modified`, a vanished file is not
   a change. Tests: a touched file with the same size and mtime does not wake (the
   named limit, pinned as behaviour rather than left to be discovered).
6. **`cmd/nova-wake/main.go`** — flags, refusals that name what the flag wants and
   report **every** independent problem in one go, the opening `WAKE` line, the
   poll loop with the deadline and the interval floor, the `60m` ceiling refusal,
   the verdict line, and `internal/bounded` for the per-kind cap. One `oneline`
   call on every value that reaches a line.
7. **`cmd/nova-wake/quickstart`** — the natural first run: `--baseline`, a short
   `--max`, one source if one can be inferred from the flags given, and a
   statement of what it chose, in the shape `nova-memory quickstart` and
   `nova-check quickstart` already use.
8. **Onboarding, which `internal/ci/onboarding_test.go` will require the moment
   the directory exists** — a usage banner ending in an `example:` block whose
   lines run, a `### First run` in `README.md`, `nova-wake help` on stdout at exit
   0, and a one-line refusal for a bad invocation rather than the banner.
9. **Tests for the two lessons, named as such** — `TestNoFalseWakeOnReload` and
   `TestBlocksRatherThanTicks` (a watch whose sources never change returns once, at
   its deadline, having printed one verdict line). A spec whose lessons are not
   pinned by a test is a spec that will buy them again.
10. **`SPEC.md` and `README.md` wiring** — the binary count in SPEC.md's opening
    paragraph, a `## nova-wake` section or a pointer to this file, and the README
    `### First run`. CONTRIBUTING says a wording change to a rule here is a rule
    change; this file is that rule.
11. **`internal/wake/line.go`** — the `--line` view: `git log` on the bus checkout
    under the timeout, the last sign per name, the `<sha>|OFFLINE` state value,
    OFFLINE once and BACK once. Plus, in `main.go`: `--on-deadline` echoed on the
    opening line and the verdict, `--entry-interval` with a per-source due time,
    the pid in `<state>.lock`, the `WAKE SOURCE` counts, and the three-in-a-row
    `WAKE BROKEN`. Tests: the eight in **Tests this spec demands**.

## What the prototype does that this spec forbids

`bin/wake-on-change.sh` (zsh, 290 lines) proved every idea above and is a thing to
be grateful to. These are the places it is **not** a model, each with the reason.

1. **Hardcoded paths.** `$HOME/rowan-working/rowan-stella` as the bus,
   `$HOME/rowan-working/wake/state.json` as the state, `$HOME/deepseek-working-*`
   and `$HOME/freddy-working-*` as the report roots. Forbidden: no guessed paths,
   and two other lines' directory layouts are not a tool's business.
2. **A hardcoded default repository**, `mas-bandwidth/schema`, twice — once as a
   default and once as a fallback when the lane file had no `.repo`. Forbidden.
3. **Reading `merge-lane/lane.json` to invent its own watch list.** A guessed path
   whose content decides the scope of the run. Entries come from flags.
4. **Third-party programs in the hot path.** `jq` parses every poll's JSON,
   `python3` writes the state file, and `perl -e 'alarm shift; exec @ARGV'` is the
   timeout because this Mac has no `timeout`. Standard library only: `encoding/json`,
   `os.Rename` and `context.WithTimeout` are all three of those, and none of them
   is a dependency a line has to install before the watcher works.
5. **A default deadline.** `MAX=1200` means an invocation with no `--max` waits
   twenty minutes, which is the right number and the wrong rule: the one thing a
   caller must state is when to give up.
6. **Unconditional `--advance`.** Every bus poll advances the window's cursor,
   from any invocation, including a test run. This is the five-note failure, and
   the spec closes it three ways.
7. **A cap that lies.** `... +N more change lines (state advanced; poll again)`
   over consumed bus notes: polling again cannot return them. Consumed mail is
   uncapped.
8. **A tab-joined state value** — forbidden by name, with a test.
9. **Unbounded sighting memory swept to zero.** `if (( ${#seenlines} > 300 ))` then
   `unset` **all** of them, which re-wakes every standing error at once. An LRU.
10. **No lock anywhere.** Two runs share one state file and one cursor; both races
    above are open in the prototype.
11. **`exit 0` on everything, including a state file it could not parse** — the
    `jq` reload is `2>/dev/null` and a failure reads as a cold start, which is the
    false-quiet failure with a silencer fitted. Exit 2, naming the file.
12. **Usage printed by `sed`-ing its own source**, which makes the banner a
    property of the comment block's line numbers. `help`, on stdout, exit 0.
13. **Ungrammatical output.** `no change in 1200s` has no token, no fields and
    nothing a scanner can anchor on; `CHANGE after=…` has a token but its item
    lines (`bus: …`, `PR 942: …`, `report: …`) are three ad-hoc shapes. One
    grammar, first token `WAKE`.
14. **Silent truncation.** `cut160` cuts a relayed line to 160 bytes with no mark,
    so a reader cannot tell a cut from an author's own ellipsis.
    `oneline.TailBytes` and the `...+<dropped>B` mark exist for this.
15. **`--prs` as bare numbers against one `--repo`.** An entry's name includes its
    repository.
16. **One cadence for every source.** `INTERVAL=30` polls a bus that answers in
    seconds and a CI run that answers in eight minutes at one rate: sixteen `gh`
    calls per entry per run, learning nothing. Rule 3: `--interval` and
    `--entry-interval`, no defaults.
17. **A deadline with no default action.** `no change in 1200s` and the window has
    to remember what it meant to do. Rule 1: `--on-deadline` is required and is
    echoed on the verdict.
18. **Scratch under `/tmp`.** `TMPD="${TMPDIR:-/tmp}/wake-on-change.$$"` holds
    every poll's `gh` output. Rule 4: the only files are beside `--state`.
19. **No way to tell a silent line from a quiet one.** The prototype watches
    notes, checks and files, and a line that stopped writing all three looks
    exactly like a line that is busy. Rule 2: `--line` and OFFLINE once.
20. **A failing source that ends the run as quiet.** After the first `nova-bus
    exit=N` line, every later failure is `(still failing)` under STANDING, and a
    run in which the bus never answered ends with `no change in 1200s`. Rule 8:
    three in a row is `WAKE BROKEN`, exit 2.
21. **Lines read and never counted.** An unknown line is relayed, which is right,
    but nothing says how many lines were read, so a bus that printed nothing and
    a bus that printed twelve bookkeeping lines look the same. Rule 7: `WAKE
    SOURCE bus read=<n>` and the counts add up.
22. **A second state file under the same guessed directory.** `child-report.sh`,
    the report-side prototype, keeps its triage watermark at
    `$HOME/rowan-working/wake/triage.json` beside the watcher's `state.json`, both
    reachable through `ROWAN_WORKING` and `TRIAGE_STATE` from the environment.
    Its `--max 40` and `--since` are good ideas in the wrong place. Rule 4 and the
    no-environment law: a report source's state is `--state` and nothing else.
