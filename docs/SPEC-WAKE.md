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

This spec is normative. It is a sibling of [SPEC.md](SPEC.md), whose
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
| A harness `/loop` fired a whole model every minute as a heartbeat. First fire: 53 tool calls and 165 seconds to see a note already answered; second: 58 tool calls on an empty inbox (Johnny, 2026-09-11, on the Grok harness). | Every empty minute was a load: a full context re-read to learn that nothing had arrived. | **An in-session poll is not a wake.** A harness interval that runs a model — a `/loop`, a scheduler prompt, a heartbeat — is the five-minute sleep by another name; `watch` is the blocking call inside a turn, `serve` is the process outside one, and there is no third shape. Empty minutes cost zero tokens (rule 10). |
| The state for one watched entry was stored as a **tab-joined** string whose last field was often empty. Reloading it dropped the trailing empty field, so the reloaded value never equalled the freshly computed one. | Every poll reported a change. The watcher woke the window every interval, forever, with nothing to say — a false wake is worse than a missed one, because the window learns to stop reading. | **A state value round-trips or it is not state.** The separator may not be a character the reader can eat, the comparison is byte-for-byte over the stored form, and a test writes state, reloads it, and asserts a second identical poll reports **no change**. |

A third failure was paid the same day and is in **The races** below, because it is
a race rather than a lesson about clocks: the watcher's own test run consumed five
bus notes that the window then had to be told about by hand.

The four races and the rule that closes each, so the settlement of every
surface is in one place (each is argued in **The races** and pinned by the
test named):

| the race | the rule that closes it | test |
|---|---|---|
| two watchers advancing one bus cursor | one advancing watcher per `(bus, as)`: an exclusive kernel lock, the second is `WAKE REFUSED` exit 2 naming the holder | 12 |
| two runs writing one state file | one writer per `--state`: an exclusive kernel lock on `<state>.lock` for the whole call, the second is `WAKE REFUSED` exit 2 naming the holder and the fix | 4 |
| mail consumed by a probe and not relayed | `--advance-cursor` off by default, only for the window's own `--as`, and only behind an empty bus queue: the advance is a spooled transaction and consumed mail is bounded by this tool's own pending queue, never by anything `nova-bus` re-lists | 5, 12 |
| a harness kill between observing and printing | closed in the safe direction by ordering (rule 11): a kill anywhere leaves the entry pending, a repeated wake and never a lost one; the advance's own kill point is recovered through `inbox --open` | 11 |

## The verb

```
nova-wake watch --state <file> --max <duration> --on-deadline <word> --interval <duration> [--max-lines <n>] [--baseline]
      [--bus <dir> --as <name> --receipt-max-words <n> [--refresh --remote <name> --branch <name>] [--advance-cursor --remote <name> --branch <name>]]
      [--line <name> ... [--offline-after <duration>]]
      [--entry <repo>#<n> ... --entry-interval <duration>] [--final-only] [--gh-timeout <seconds>]
      [--reports <dir> ...]
nova-wake serve --bus <dir> --as <name> --on-note <command> --interval <duration> --state <file> --hours <h> [--receipt --remote <name> --branch <name>] [--on-note-idempotent] [--batch-max <n>] [--git-timeout <seconds>]
nova-wake serve --bus <dir> --as <name> --state <file> --redeliver <id> --on-note <command> [--on-note-idempotent]
nova-wake quickstart --state <file> [--max <duration>] [--on-deadline <word>]
nova-wake help
```

One verb that watches, one that serves, one that shows a first run, and `help`.
The two shapes are exactly two: `watch` is a **blocking tool call** inside a
turn the session is already spending, and `serve` is a **process outside any
session** that starts a turn only when a note has landed (rule 10). There is no
third: `watch` has no `--detach` and no background mode, and an in-session
poll — a harness `/loop`, a scheduler prompt, a heartbeat that runs a model on
an interval — is not a wake and is not `serve`; it is the five-minute sleep of
the first lesson, a load per tick, and this tool offers no verb for it (Johnny,
2026-09-11: 53 tool calls to learn nothing).

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
all under a timeout, all one at a time. `git` is started only against the bus
checkout, read-only: for `--line` (rule 2 below) and for the checkout's head,
which is the freshness the `WAKE SOURCE bus` line shows (**How the checkout
receives mail**). `nova-bus` is started once before the opening line as
`nova-bus version`; once per bus poll as `inbox` without `--advance`, or as
`wait` without `--advance` under `--refresh`; a second
time on a poll that advances, as `inbox --advance`; and once, as `inbox
--open`, at the start of a call that finds an advance interrupted (**The bus
inbox**). It opens no socket
of its own, resolves no host, and has no opinion about what a bus or a forge is
beyond what those two programs tell it. A source whose program is missing from `PATH` is a **change**
on the first poll, not a refusal — see **Sources** — because a window that cannot
see its bus needs to hear so now.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the watch ran: **either** something changed **or** the deadline arrived |
| 2 | could not run: a missing or malformed flag, no source named, a `--max` over the ceiling, an unreadable or unparsable state file, a second watcher on the same state file, a `nova-bus` whose version is not this build's own (the pin, below), or a source whose failure streak reached three (rule 8; the streak is in the state file and spans calls) |

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
WAKE at=<stamp> as=<name|-> max=<d> interval=<d> on-deadline=<word> sources=<bus,entries,reports> state=<file> cold=<true|false> nova-bus=<version|-> pending=<n>
WAKE CHANGE after=<d> polls=<n> bus=<n> entries=<n> reports=<n> lines=<n> pending=<n>
WAKE QUIET after=<d> polls=<n> default=<word>: deadline, default taken
WAKE BROKEN source=<bus|entries|reports> failures=<n> since=<stamp>: <reason>
WAKE BUS id=<id|-> from=<name> addr=<to|cc> at=<stamp|-> commit=<sha|-> path=<path>: <subject>
WAKE BUS LINE <the bus's own line, verbatim>
WAKE BUS STANDING <the bus's own line, verbatim>
WAKE ENTRY <repo>#<n> state=<state> fail=<n> pending=<n> pass=<n> final=<true|false> failing=<names|->
WAKE ENTRY <repo>#<n> unreadable: <reason>
WAKE REPORT path=<path> lines=<n> bytes=<n> <new|modified>
WAKE LINE name=<name> state=<OFFLINE|BACK> last=<stamp|-> silent=<d> commit=<sha|->
WAKE SOURCE <bus|entries|reports> read=<n> suppressed=<n> relayed=<n> standing=<n> head=<sha|-> head-at=<stamp|->
WAKE NOTE <something true about this run that is not a change>
WAKE POLL <source>: <reason one poll failed, which was not fatal>
WAKE MORE kind=<bus|entry|report> shown=<n> total=<t> n=<k> <remedy>
WAKE REFUSED: <reason>
WAKE FIRED ids=<n> first=<id> rc=<n> redelivered=<0|1>
WAKE UNCERTAIN id=<id> attempt=<n>: dispatch interrupted; nova-wake serve --bus <dir> --as <name> --state <file> --redeliver <id> --on-note <command> runs it again
WAKE UNCERTAIN id=<id> attempt=<n> rc=<n>: retry not terminal; nova-wake serve --bus <dir> --as <name> --state <file> --redeliver <id> --on-note <command> runs it again
WAKE BLOCKED as=<name> uncertain=<id> queued=<n>: a dispatch may still own this receiver; end it, then nova-wake serve --bus <dir> --as <name> --state <file> --redeliver <id> --on-note <command>
WAKE SERVE fired=<n> notes=<n> redelivered=<n> uncertain=<n> queued=<n> cc=<n> max_wait=<d> idle=<duration>
```

The last three are `serve`'s (rule 10); everything above them is `watch`'s.

`WAKE CHANGE`, `WAKE QUIET` and `WAKE BROKEN` are the **last** line and the three
possible verdicts, and `pending=<n>` on each of the first two is the length of
the delivery queue — every observation this run or an earlier one made and
has **not** printed, which the next call prints first (**Delivery
is the printed line**, rule 11); the opening `WAKE` line is the **first**, printed before anything is
waited on, so a transcript shows the call began and what it was told to do — a
tool call that prints nothing for twenty minutes and then prints everything is,
while it runs, indistinguishable from one that has hung. `WAKE` lines and the
informational tokens go to stdout; `WAKE REFUSED` and `WAKE POLL` go to stderr.

Every path, subject, reason, entry name and relayed bus line is rendered through
`internal/oneline`, so a note whose subject carries U+2028 arrives as one escaped
line rather than two, and a `key=value` field is one whitespace-free token as the
field law requires. A relayed line is escaped and **never shortened below its
own tail budget**; and no line is exempt from the cap, because a line the cap
drops is pending and the next call prints it (rule 11).

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

### The bus inbox — a NOTE not yet printed is always a change, and the status line is never filtered

The bus source runs `nova-bus inbox --bus <dir> --as <name> --receipt-max-words
<n>` under `--gh-timeout`'s sibling budget and reads its stdout and stderr
together, line by line.

**Classification is by first tokens, and the default case prints.** A line
beginning `INBOX NOTE` whose id carries no `printed=` mark in the state file is a
change, always, and is relayed as `WAKE BUS`; a note this tool has already
printed is **suppressed** and counted, because the window has been shown it,
and that mark is written only after the line was printed (rule 11). A plain
`inbox` lists a note **once**: on every poll while the cursor stands behind it,
and never again once `--advance` has moved the cursor past it — measured, below
— so the suppression does its work on a run without `--advance-cursor`, where
every poll re-lists the same new notes, and on a recovery read under `--open`,
and nowhere else. **This spec does not depend on `nova-bus` re-listing
anything.** The bookkeeping tokens — `INBOX OPEN`, `INBOX OK`, `INBOX CURSOR`,
`INBOX SCOPE`, `INBOX LEGACY` — are counted and not printed, because they say
the same thing every poll. **Every other line this tool does not recognise is printed verbatim,
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
- **The cursor never moves past a note this tool has not spooled, and never
  moves while a note is unprinted.** Measured on 2026-09-11 against `nova-bus
  v0.10.3` on a synthetic bare bus with two clones (**How the checkout receives
  mail**): a plain `inbox` lists a note while the cursor stands behind it, and
  once `--advance` has moved the cursor past it the note is on the reader's
  `OPEN` list and a plain `inbox` prints `INBOX OPEN carrying=<n>` and **no**
  `NOTE` line for it; only `--open --open-max <n>` lists it, `--open-max` must
  be at least 1, and past `--open-max` it prints `INBOX OPEN listed=<k> and
  <m> more`. So the cursor is not a thing this tool may move and then read
  behind. **Consumed mail is bounded by this tool's own pending queue**
  (**State**) and by nothing `nova-bus` does, and a poll with `--advance-cursor`
  is a transaction in four steps:
  1. `inbox` **without** `--advance`. Every `INBOX NOTE` not marked printed is
     appended to the queue and the state is written (rule 11, step 1) before
     anything is printed.
  2. Print up to the cap; delete each printed record and mark it. If any bus
     record remains in the queue after this — the cap elided some, or the poll
     found more than it could print — **this poll does not advance**, and the
     advance waits for a poll whose queue holds no bus record after its print.
  3. When the queue holds no bus record: write `bus:advance` =
     `inflight|<stamp>|<checkout head>` to the state, **then** run `inbox
     --advance --remote --branch`, classify its output exactly as in step 1 —
     normally it lists nothing new, because nothing fetched between step 1 and
     now; when it does, those notes are appended to the queue — write the state,
     and only then clear `bus:advance`. The marker is written before the advance
     and cleared after its output is durable, so a kill between the two leaves
     the marker.
  4. A call that finds `bus:advance=inflight` at its start runs `inbox` plainly,
     reads `carrying=<n>` from its `INBOX OPEN` line, and if `n>0` runs `inbox
     --open --open-max <n>` once — `n`, never a fixed number, because `nova-bus`
     caps the listed `OPEN` at `--open-max` (default 20) and a fixed number
     would recover a fixed number — appends every listed note not marked
     printed to the queue, writes the state, clears the marker and says `WAKE
     NOTE bus advance was interrupted; recovered <k> notes from OPEN`. When
     the `--open` read lists fewer than `carrying=` — the checkout moved
     between the two reads — it is run once more with the new count; still
     short, the marker stays, nothing advances, and the call says `WAKE NOTE
     bus recovery incomplete: listed=<k> carrying=<n>; retried next call`. The
     recovery is complete and not paginated: `--open-max` takes any positive
     number, measured to 100000, and `n` is the whole carried list.

  `nova-bus` moves a cursor to `HEAD` and nowhere else, so this is the only way
  to keep the cursor behind the print. Mail consumed is mail spooled; mail
  spooled is mail printed, **under the cap** like everything else (**Bounded
  output**); and a cap that elides a note defers the fetch rather than losing
  the note.
- A run **without** `--advance-cursor` consumes nothing and fetches nothing
  (**How the checkout receives mail**). A note it prints is marked printed and
  wakes once; the default is the one that cannot lose mail.
- **`--refresh` fetches without moving anything.** With `--refresh --remote
  <name> --branch <name>`, each bus poll is `nova-bus wait --bus <dir> --as
  <name> --receipt-max-words <n> --timeout <t> --remote <name> --branch
  <name>` with **no** `--advance`, where `<t>` is the time to the earliest due
  source, at most `--interval`: `wait` takes the checkout lock, fetches,
  fast-forwards, and returns the moment the inbox would list something new or
  at `<t>` (SPEC.md, **wait**), and its lines are classified exactly as
  `inbox`'s, because the two verbs share one listing. So new mail reaches a
  non-advancing watcher within one poll, no cursor moves, nothing is consumed,
  and the carried list is then read whole with `inbox --open --open-max
  <carrying>` once per run on the first poll, so a cold watcher lists what it
  is owed before what is new. `--refresh` and `--advance-cursor` together are
  exit 2: one fetch per poll, never two.
- **`--advance-cursor` is specified here and is not in the first build.** Work
  list item 3a ships it after item 3 is read and test 11 is green against the
  pinned binary; until then the flag is `WAKE REFUSED: --advance-cursor is not
  in this build; use --refresh`, exit 2. Advancement is an acknowledgement
  optimisation and not a prerequisite for delivery (Stella and Johnny,
  2026-09-11), and a v1 that cannot move a cursor cannot lose a note.

**The bus is the exception to the cold-start rule.** See **The cold-start rule**.

#### How the checkout receives mail, and the version this depends on

`nova-bus inbox` reads the checkout and never the remote (SPEC.md, *what it
deliberately does not do*: pull first is the caller's). The **one** write-side
call this tool makes against a bus is `nova-bus inbox --advance --remote <name>
--branch <name>`, and it is also the one call that fetches: the push inside it
fetches before it writes and fetches-and-rebases when it is rejected (SPEC.md,
**send** and **receipt** protocol, steps 2 and 4), so mail that landed on the
remote reaches the checkout through that push and is listed by the **next**
`inbox`. Measured on 2026-09-11 against `nova-bus v0.10.3`: a note pushed to a
bare remote by another clone was listed on the second `inbox --advance` after it
landed, never the first. Measured the same day, on the same fixture, the other
half of the dependency: after `--advance` moved the cursor past that note, a
plain `inbox` printed `INBOX OPEN carrying=1` and no `NOTE` line; `--open
--open-max 10` listed it; `--open-max 0` was refused, exit 2; with 45 notes
carried, `--open` alone listed 20 and said `and 25 more`, and `--open-max
100000` listed all 45. (Stella measured it first, `bus-probe.json` beside her
second read; the author repeated it on a fresh synthetic remote before this
sentence was written.) So, with `--advance-cursor`, new mail is relayed within
**two advancing polls**, and a poll advances only when this tool's bus queue is
empty after its print (**The bus inbox**). A call holding unprinted mail prints
up to the cap, returns, and does not advance; the next call advances if the
queue drained. The promise, stated whole: **mail is relayed within two polls
of the window having been shown everything it was already owed**, and while it
has not been, every verdict carries `pending=<n>` and the call prints, once,
`WAKE NOTE bus advance deferred: pending=<n> bus notes unprinted; nothing
fetches until they print`. The freshness promise is not made over a backlog,
and the tool says so rather than pretending. The two-poll half is a promise
about `nova-bus`'s push and not about its read — which is why the version is
pinned:

- **The pinned version is this build's own version: the `nova-bus` from this
  tool's own release.** Before the opening line the tool runs `nova-bus version`
  and reads the second token of its first line. A version other than this
  build's is `WAKE REFUSED: nova-bus <found>; this tool is written against
  <this build> and its fetch is a property of the push` — exit 2, because a
  `nova-bus` that stopped fetching inside `inbox --advance` would leave a
  watcher that looks perfectly healthy and is blind. The opening line carries
  `nova-bus=<version>`.

  The pin **was** the literal `v0.10.3`, written here and in
  `internal/wake/bus.go`, and on 2026-09-12 Emma's dogfood pass of v0.12.0
  found what a literal costs (nova-tools #104): every binary in `cmd/` ships
  from one tag, so the release moved `nova-bus` to `v0.12.0`, the literal stayed
  where it was, and `nova-wake v0.12.0` refused the `nova-bus` of its own
  release — `WAKE REFUSED: nova-bus v0.12.0; this tool is written against
  v0.10.3`. A pin no release can update is a pin that expires, and a check that
  expires is not a check. So the pin is **derived** instead: the check is
  `AcceptBus(tool, found)`, which is **string equality** between this build's
  own version and the second token of `nova-bus version` — nothing is parsed,
  compared as a range, or ordered. One tag stamps both programs with the same
  `-ldflags -X main.version=<tag>`, so a released pair is equal by the stamp; in
  an unstamped `go build` both halves take `Main.Version`, the toolchain's
  module pseudo-version for the tree (`v0.12.1-0.<stamp>-<12hex>`, `+dirty`
  included), so they are equal there too. So *the `nova-bus` this tool is
  written against* is exactly *the `nova-bus` built from the same tree as me*,
  and the two versions match as a fact about the build. (Under `go run`, where
  neither half has a stamp or a module version, both report `devel` and
  `AcceptBus` accepts the pair: a developer's own risk, never a release — the
  release workflow stamps every binary in `cmd/`.)
  The guard is not weakened: an older release's `nova-bus`, a foreign one, or
  one from another tree is still refused by name before the opening line, and
  the tool still names both versions. The measurement above is what argues the
  pin, and it is repeated per release against the pair that ships — which is
  what `cmd/nova-wake`'s advancing tests do, building `nova-bus` from this tree,
  stamping it with this tool's own version and reading the version back out of
  the binary. Widening the rule further — a major.minor line, or "newer than" —
  would be a rule change under CONTRIBUTING; matching a release to itself is
  not.
- **Without `--advance-cursor` nothing here fetches**, and the tool says so
  rather than letting a quiet checkout look like a quiet bus: once per run,
  `WAKE NOTE bus checkout is read as it stands; nothing fetches without
  --advance-cursor; freshness is head-at=`; and every `WAKE SOURCE bus` line
  carries `head=<sha> head-at=<stamp>`, the checkout's newest commit and its
  commit stamp, read with `git log -1` under the timeout, so a reader of the
  transcript can see the checkout stand still. External synchronisation — a
  `nova-bus wait`, a `git pull`, a `serve` — is a prerequisite and is named as
  one, never assumed.

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
  reason. An unreadable value is an **error and never a count**: it wakes under
  `--final-only` exactly as without it, because a flag that asks for fewer
  wakes about arithmetic is not a flag that asks to sleep through a source that
  cannot be read. It also counts one poll toward the source's failure streak
  only when **every** entry is unreadable (rule 8).

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
per run: one final entry wakes the run even while four others churn. It never
suppresses `unreadable:` — one unreadable entry beside four readable ones wakes
the run under `--final-only` and prints its `WAKE ENTRY ... unreadable:` line.

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
watched thing, namespaced by source — `bus:line:<bytes>`, `bus:note:<id>`,
`entry:<repo>#<n>`, `report:<path>`, `line:<name>` — plus one `fail:<source>`
per source, the **delivery queue** `queue:<n>` with its counter `queue:next`,
the advance marker `bus:advance`, and for `serve` one `serve:<id>` per note
holding exactly one of `queued|<stamp>`, `dispatching|<stamp>|attempt=<n>`,
`delivered|<stamp>|rc=<n>|redelivered=<0|1>`, `uncertain|<stamp>|attempt=<n>`,
`uncertain|<stamp>|attempt=<n>|rc=<n>` (an idempotent retry that returned
non-zero) or `cc|<stamp>` (rule 10) — a `delivered … rc=0` is the one record
that says the receiver finished with that id, and under `--on-note-idempotent`
it is the completion boundary the queue waits behind. It is written **after every poll**, through a temporary file in
the same directory and an atomic rename, so a call killed by the harness mid-poll
leaves either the previous state or the new one and never half of either.

**Every watched entry holds two things — the newest observation and what was
printed — and every unprinted observation is a queue record.** The stored form
of a watched key is `<value>|printed=<id|->`, where `<value>` is the
newest observed state value and `<id>` is the **delivery id** of the value the
window was last shown: a note's own id for a `bus:note:` entry, and for every
other key the first twelve hex characters of SHA-256 over `<key>\x00<value>`.
A poll whose observed value differs from the stored newest value **appends** a
record `queue:<n>` = `<delivery id>|<key>|<value>`, `<n>` taken from
`queue:next` and never reused, and then replaces the newest; it never replaces
an unprinted value, because the unprinted value is in the queue and a queue
record leaves only by being printed. An entry is **pending** while any queue
record names its key; `pending=<n>` on the opening line and the verdict is the
queue's length. Printing takes records in `<n>` order — oldest first, within
the per-kind cap — and a record is deleted, and `printed=<its id>` written,
only after its line reached stdout (rule 11, step 3). **Nothing coalesces.** A
second red observed while the first is unprinted is a second record and a
second line; red then green with the red unprinted prints red, then green, in
that order; sixty entry transitions elided over three calls print as sixty
lines. The one thing that keeps an observation out of the queue is
`--final-only`, which keeps a non-final entry value out at observation time,
because that is what the flag asked for. A key or value holding `%` or `|` is
stored with `%` escaped first as `%25` and then `|` as `%7C`, and unescaped in
the reverse order on load, so a value holding a literal `%7C` reloads as those
three characters and never as a pipe; the round-trip test covers a report path
that carries a `|`, a value holding a literal `%7C`, and one holding `%25`
(Stella, 2026-09-11).
`fail:<source>` is `<n>|<since stamp>|<reason>`: the source's consecutive-failure
streak, written on every failed poll, cleared on the first success, and read at
the start of the next call — so the streak spans calls (rule 8).

- **It is this tool's own file, in a format this tool chose**, and it is not a
  record of anything: delete it and you get a cold start, which is a correct if
  noisier watch. Nothing else may read it as truth.
- **A value is one line and round-trips.** The separator inside a composed value
  is `|` and not a tab, and a stored value compares equal to a freshly computed
  one byte for byte. The second lesson of 2026-09-11 is this sentence; the test
  that pins it writes, reloads, polls again and asserts quiet.
- **It is bounded where it grows with events, and unbounded where a bound
  would lose a delivery.** The `bus:line:` sighting memory and the **delivered**
  `bus:note:` marks — entries whose `printed=` equals their value's id and
  which no queue record names — are the two parts that grow with things that
  happen rather than with things being watched, so each is an LRU of **300**
  entries with the least recently *seen* evicted first; an evicted note is a
  change again, which is the safe direction. **The queue is never evicted and
  never truncated**, and a `bus:note:` entry a queue record names is not a
  candidate for eviction whatever its age: a pending record leaves the state
  only by being printed, so a backlog of 3,000 notes is 3,000 records in the
  file and `--max-lines` lines per call until drained, and the file's size is
  the honest cost of a window that has not read its mail. Pending is unbounded
  in the state and bounded in the output (`WAKE MORE`), never the other way
  round. The prototype
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
consumes nothing, and lists the same note on every poll while the cursor stands
behind it — also not swallowed, and suppressed once printed.

An unrecognised bus line on a cold first poll is a change for the same reason: it
is unrepeatable news about a bus that may be broken right now.

## Bounded output

The cap is `--max-lines`, default **40**, counted over the item lines — `WAKE
BUS`, `WAKE ENTRY`, `WAKE REPORT` — and never over the verdict, the opening line,
a `WAKE NOTE` or a refusal. Past it, one line per kind:

```
WAKE MORE kind=<bus|entry|report> shown=<n> total=<t> n=<k> <remedy>
```

`<k>` is `total - shown`, the lines this poll did not print, and every one of
them is pending in the state file and printed by the next call (rule 11). `0`
means all; a negative cap is refused, because `0` already means all and a
negative number is a typo with two readings. The cap is **per kind**, as SPEC.md
requires, because a flat cap over a concatenated stream means the loud kind eats
the quiet one and the quiet one is the finding the window did not already know
about: forty churning entries must not hide one note.

The remedy names the flag that lifts the cap, and where the source has a file that
holds the whole list it names that instead — for the bus, the reader's own `OPEN`
file; for reports, the directory.

**There is no unrepeatable line, and the bound holds over consumed mail.** An
earlier draft exempted notes consumed by `--advance-cursor` from the cap, on the
argument that polling again cannot return a note the cursor has passed. Two facts
make that exemption unnecessary and its cost — 200 mail lines under a promise of
`4 * --max-lines + 10` — dishonest. First, a note the cursor passed is in this
tool's own queue before the cursor moves, because the advance is a spooled
transaction (**The bus inbox**) and the queue is never evicted (**State**). It
is **not** on any list `nova-bus` prints by default — measured: a plain `inbox`
does not re-list a note the cursor has passed — and nothing here depends on it
being. Second, the cursor is not advanced on a poll
while any note is pending, and a note is pending until this tool has printed it
(**The bus inbox**, rule 11). So past the cap the line is `WAKE MORE kind=bus
shown=<n> total=<t> n=<k> pending; the next call prints them, the cursor waits`,
`<k>` is `total - shown`, the `<k>` notes stay pending in the state file, and the
next call — or a wider `--max-lines` — prints them before anything newer. The
prototype's `state advanced; poll again` was the false promise; this is the same
words made true by the ordering. A `WAKE BUS STANDING` line prints on every poll
it stands, and is counted against the bus cap on each of them, so a run of many
polls over a refusing bus still prints at most `--max-lines` bus lines per poll.

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
does so only behind its own print, so a consumed note is a printed note. **A watcher may not advance a cursor that is
not the window's own**, and there is no flag that lets it.

**A harness kill between observing and printing.** A call killed at its ceiling
after observing a change — before or after the state write, before or after the
item line reached stdout — re-reports that change on the next call. This race is
closed in the **safe** direction deliberately, by rule 11: observed state is
written first, item lines are printed second, `printed=` marks are written
third, and a kill at any boundary leaves the entry pending, so the duplicate is a
repeated wake and never a lost one. A window told the same news twice reads twice;
a window told it never does not. The one residual is named and closed: with
`--advance-cursor`, a kill between `nova-bus inbox --advance` returning and the
write of its output would lose this tool's record of any note that call listed
first, and — measured, **How the checkout receives mail** — a plain `inbox`
does not re-list a note the cursor has passed, so nothing would bring it back
on its own. So the advance is wrapped: `bus:advance=inflight` is written before
it and cleared after its output is durable, and a call that finds the marker
runs `inbox --open --open-max <carrying>` and spools every unprinted note
before anything else (**The bus inbox**, step 4). That is not left to the
prose: test 11 kills at exactly that point, with a backlog above `nova-bus`'s
default `OPEN` display cap of 20, and asserts every note the advance consumed
is printed by the following calls. **This race is closed by
ordering, not by refusal.** A refusal before the state write (lesson 63's
shape) would be the wrong tool here: there is nothing to refuse, because the
kill is the harness's and arrives at any instruction; the only choice the tool
has is which side a kill lands on, and it chooses the repeated wake every
time. The cost — a window can read one line twice — is in **Known limits**.

## The rules of the last two days

Twelve rules, 2026-09-09 to 2026-09-11. Each came from a hurt and each is written so
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
   changes: the state file holds `line:<name>` as `<sha>|<state>|<stamp>` — the
   commit the judgement was made on, the word it produced, and that commit's
   stamp, which is what `silent=` is computed from at print time rather than a
   duration stored in the file — and a later poll with the same value prints
   nothing. A new sign from the line is a change once, `state=BACK`. A line
   with no commit on the branch is `last=- commit=-` and is OFFLINE at the
   first poll after the watch has run for `--offline-after`. The sign is read with `git log` against the bus checkout,
   read-only, under `--gh-timeout`, and nothing else is read. (Glenn, 2026-09-10:
   reassign a silent line's items after about ten minutes. Johnny ran out of
   credits at 00:35Z and the board said he held his items for an hour.)

3. **Poll cadence matches the watched thing's rate.** `--interval` has no default
   and is exit 2 when missing. It is the cadence for the bus, for `--line` and for
   report directories. Entries have their own cadence, `--entry-interval`,
   required whenever `--entry` is given, and it is the expected length of the
   hosted run: an entry is polled no more often than that. Both intervals have the
   5s floor and neither has a default, which is the opposite of `--offline-after`
   in rule 2 on purpose: an interval is a fact about one window's round trip
   and only that window knows it, while ten minutes silent is the family's
   rule and every window must agree about it. A duration gets a default here
   only when a different number per window would be a bug. The loop sleeps
   until the earliest due source, so a 5s bus interval
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
   bound holds at the largest plausible state **with no exception for consumed
   mail**: 200 notes, 50 entries, 100 report files and 20 lines changing in one
   interval print at most `4 * --max-lines + 10` lines, with or without
   `--advance-cursor`, on every poll of the call and not only the first. (Glenn, 2026-09-09: tool output costs tokens; test at the largest
   plausible state.)

6. **What woke you is named.** Every change line carries the identity of the thing
   that changed: a note's id and commit sha, an entry's repository, number and new
   state, a report's path, a line's name and last commit. The words `something
   changed` never appear in this tool's output, and no change line is printed
   without its identity field. (A wake that says only that the world moved sends
   the window back to look at all three places, which is the tick this tool
   replaces.)

7. **Never filter the status line.** Every line the bus source reads is classified
   as suppressed, relayed or standing, and every line is counted — a note
   already marked printed is suppressed, and is the one suppression the tool's
   own state decides. Nothing is dropped silently. Once per run, before the verdict, `WAKE SOURCE bus read=<n>
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
   lie. **The streak lives in the state file as `fail:<source>` and spans
   calls**: the first unreadable reason is a change and returns the call, and a
   counter that started at zero on every call would never reach three over a
   source whose error text changes, so the count, its start stamp and its last
   reason are written on every failed poll and read at the next call's start.
   A success clears it. `since=` is the streak's first failure, which may be a
   call or more ago. A source fails when the bus's `nova-bus` exits
   other than 0 or times out, when every entry is unreadable in one poll, or when
   every `--reports` directory is unreadable. Three is fixed and not a flag: it is
   a fact about the tool, not about the window.

9. **The tool stamps; a typed time is never trusted.** Every stamp `nova-wake`
   prints or stores is the tool's own clock, read at the moment of writing, RFC
   3339 in UTC: the `at=` on the opening line, `since=` on `WAKE BROKEN`, and
   every stamp in the state file. A time that reaches this tool inside text, in
   a note's body, a report's contents or a caller's flag, is data and is never
   used to order, age or deduplicate anything. Bus notes are ordered by the
   bus's own commit stamp, which is a tool's stamp; a line's last sign is its
   newest commit's stamp and never a date in that commit's subject. There is no
   flag that sets a stamp and none will be added. (2026-09-11: a person stamped
   notes two hours ahead of the clock, and every reader that ordered by the
   typed time put them in the future.)

10. **`serve` wakes a harness from outside it, once per note, and spends
    nothing while idle.** `watch` runs inside a tool call and returns to a
    session that is already awake; `serve` is the other shape (Glenn,
    2026-09-11: a named friend should wake efficiently when a note for them
    lands, and be efficient while there is NO work, never polling once a
    second inside a turn that costs tokens). `serve --bus <dir> --as <name>
    --on-note <command> --interval <duration> --state <file> --hours <h>`
    runs as its own process outside any session — `<h>` is a **decimal**
    number of hours and not a whole one, as `nova-swarm run --hours` is, so a
    first run can ask for `0.02` of one and a working day is `8`, and a
    `--hours` naming a deadline **under one second** is refused by name (a
    float can name one no run reaches: `1e-12` rounds to `0s`, and a process
    that exits 0 having polled nothing is a green that did nothing; a deadline
    shorter than one `--interval` is not refused and polls once) — fetches the
    bus every `--interval` (a git fetch costs no tokens; the interval matches the
    latency a person will accept, never the second), and for each new note
    whose `To:` names `<name>` runs `<command> <id> [<id>…]`: **once, and a
    second time only on a person's word or under a declared idempotent
    receiver**. `serve` starts `<command>` for a note and for nothing else —
    never on an interval, never to receipt, never to look — so an empty
    minute costs one fetch and zero tokens. The command receives note ids
    and nothing else: never `inbox`'s output, never the `INBOX OPEN
    carrying=<n>` line or the carried list, never a body; the line's model
    opens the note itself (Johnny, 2026-09-11: the carrying dump is not a
    wake payload). **`To:` wakes; `Cc:` does not.** A note carrying the name
    on `Cc:` only (`INBOX NOTE … addr=cc`) is never dispatched, never
    receipted and never a turn: it is recorded `cc|<stamp>` and counted
    `cc=<n>` on the exit line, and the line reads it at its next natural
    turn — to means must act, cc means should know, and a broadcast to five
    is five turns (nova-tools #53). **Dispatch is coalesced.** Every note
    `queued` at the moment the command is not running is handed to one
    invocation, in bus order, at most `--batch-max` ids (default 20, the
    listing law); one turn reads k notes rather than k turns reading one.
    A note's delivery is durable states under `serve:<id>` in the state
    file, each written through the same temp-file-and-rename as everything
    else: `queued|<stamp>` when the note is first seen; `dispatching|<stamp>|
    attempt=<n>` written **before** the spawn, for every id in the batch;
    `delivered|<stamp>|rc=<n>|redelivered=<0|1>` written **after** the
    command exits, with its exit code, for every id in the batch. Exit 0 is
    the one acceptance boundary an arbitrary command offers, so `delivered
    rc=0` is *accepted* and there is no separate *completed*. **A restart
    first establishes that the receiver is idle, and only then runs what it
    finds `queued`.** Killing `serve` does not prove that the command it
    started died: the child may be alive and mid-turn, and the harness is
    one. So a `dispatching` entry found on restart is an **interrupted
    dispatch** that becomes `uncertain|<stamp>|attempt=<n>`, is **not** run,
    and prints `WAKE UNCERTAIN id=<id> attempt=<n>: dispatch interrupted;
    nova-wake serve … --redeliver <id> --on-note <command> runs it again` —
    because a command with no idempotency protocol may have acted, and a tool
    that ran it again would be choosing a duplicate action on the mind's
    behalf — and **an unresolved `uncertain` entry blocks this receiver's
    queue**: no `queued` note is dispatched and nothing is redelivered while
    one exists, `WAKE BLOCKED as=<name> uncertain=<id> queued=<n>: …` is
    printed once per call with the remedy, and the exit line's `queued=`
    carries what waited. `--redeliver <id> --on-note <command>` is a person's
    act — the person has ended the earlier command or watched it return, and
    the state stores no command, so the redelivery names its handler —
    refused unless the state is `uncertain`, and runs it once more as
    `attempt=<n+1>` with `redelivered=1`; the queue drains after it returns.
    With `--on-note-idempotent` — the caller's declaration of a **stronger
    receiver contract**, never an assumption about an arbitrary command:
    the command de-duplicates by id, durably; it tolerates a second
    invocation for the same id beside a live first one; **and its exit 0 for
    an id is a terminal acknowledgement that the work for that id has
    completed**, so a second invocation that finds the first still running
    either waits for it and returns its outcome, or exits non-zero — it
    never exits 0 for merely having seen the id, because "already accepted"
    says nothing about idleness, and idleness is what the queue behind it
    needs — an
    interrupted `attempt=1` is run once more as `attempt=2 redelivered=1`
    before anything queued, and **the completion boundary is that retry's
    `delivered|<stamp>|rc=0|redelivered=1` written to `serve:<id>` after it
    exits 0**: until that record exists no different queued id is
    dispatched (the entry sits `dispatching|<stamp>|attempt=2` while the
    retry runs, and a running command holds the queue as always); a retry
    that exits non-zero has not acknowledged the original dispatch and its
    entry becomes `uncertain|<stamp>|attempt=2|rc=<n>` — `WAKE UNCERTAIN
    id=<id> attempt=2 rc=<n>: retry not terminal; …` — which blocks the
    queue like any `uncertain` until a person's `--redeliver`, never a
    third automatic run; and an interrupted `attempt=2` is `uncertain` and
    blocks all the same, so nothing fires forever and nothing goes quiet. A
    receiver that cannot promise the terminal meaning of exit 0 must not be
    declared idempotent; it gets the default, a person's `--redeliver`.
    Continued automatic progress past a kill would need durable
    process-level receiver ownership and a handoff that proves no overlap,
    which this version does not claim. (Stella, 2026-09-11: with A running
    and B queued, kill `serve` alone and restart: A uncertain, B dispatched
    beside a live A, two turns on one harness.) An earlier draft
    wrote the id once before the spawn and called that exactly-once; Stella's
    second read showed it was at-most-once with a lost launch in the gap,
    her third that an automatic second run of an arbitrary command is a
    duplicate nobody chose, and her final read that a same-id concurrency
    tolerance does not establish idleness for a different id, so the
    idempotent retry needs a completion boundary and the flag names it.
    The handoff is therefore **at-most-once by
    default, with the uncertain case surfaced and never silent**. A command
    still running holds the next note as `queued`, because the harness is
    one and cannot take two turns at once. The command's exit code is
    recorded per batch as `WAKE FIRED ids=<n> first=<id> rc=<n>
    redelivered=<0|1>`; the tool never reads the command's output and never
    retries a non-zero exit on its own. It ends at `--hours` or a `stop` file
    and prints `WAKE SERVE fired=<n> notes=<n> redelivered=<n> uncertain=<n>
    queued=<n> cc=<n> max_wait=<d> idle=<duration>` on exit, where
    `max_wait` is the longest a note sat `queued` before its dispatch — the
    latency Stella's sixth idea asks to measure. What the command is (a
    `claude -p`, an `opencode run`, a `grok` invocation) is the line's
    business, never the tool's, and whether it runs at all is the line's
    person's: starting a `serve` under a name is that name's decision and
    nobody else's. With `--receipt`, `serve` sends the bus receipt for every
    id in a batch **after** `delivered` is written with `rc=0`, never at fire
    time and never for a non-zero exit: a receipt is the machinery's claim
    that the note was handed to the mind and the mind returned, and a receipt
    sent before the command returned would claim a handoff a kill could still
    lose. A note whose command failed stays on the open list unreceipted,
    which is where a note nobody has dealt with belongs. So the model never
    spends a turn on "Heard": a receipt is the machinery's, a reply is the
    mind's. (2026-09-11: one line sent 26 "Heard" receipts by hand, a turn
    each, and the coordinator read every one.) While a line has no work, its
    cost is one fetch per interval and zero tokens; while it has work, one
    wake per batch of notes and no poll inside the turn.

11. **Delivery is the printed line, not the state write.** A change is
    *delivered* when its line has been written to stdout, and nothing else —
    not the poll that observed it, not the state write that recorded it, not
    a cursor that moved past it. The order on every poll is fixed: (1)
    append every observed value to the queue and write the state — an
    observation never overwrites an unprinted one, (2) print the item lines
    from the head of the queue, up to the cap, (3) delete each printed record
    and write `printed=<id>` for each line that reached stdout, (4) print the
    verdict. An entry with a queue record is **pending**, stays pending
    across calls, is counted as `pending=<n>` on the opening line and the
    verdict, is never evicted (**State**), and is printed by the next call
    before anything newer — so a line elided by the cap is shown by the next
    call or by a wider `--max-lines`, and a call killed at any of the three
    boundaries replays rather than loses. A duplicate line is the cost and it
    is paid on purpose. (Stella, 2026-09-11, second read: a cursor is a claim about what
    a reader has been shown, and an entry already in state that the cap
    elided was never shown.)

12. **New mail reaches the checkout through `inbox --advance`, and through
    nothing else this tool does.** Stated in full under **How the checkout
    receives mail**: the fetch is inside `nova-bus`'s push, the version is
    pinned to this build's own and checked before the opening line, mail is relayed
    within two **advancing** polls under `--advance-cursor` — a poll advances
    only behind an empty bus queue, and a call that defers the advance says
    so — and without it the tool fetches nothing and prints `head-at=` so the
    standing checkout is visible. (Stella, 2026-09-11, second read: a perfectly functioning
    watcher can remain quiet while remote mail arrives.)

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
   line contains a note's body or a report's contents; the same with
   `--advance-cursor` on, where the bus prints `--max-lines` notes and one `WAKE
   MORE kind=bus ... n=160`, the state holds 160 queue records, the cursor has
   not moved, four further calls drain them in order and the cursor moves on
   the fifth, after its print, with `nova-bus` asserted to have received
   `--advance` exactly once; each of the first four calls prints `WAKE NOTE
   bus advance deferred` once; a backlog of 3,000 notes leaves 3,000 queue
   records after the first call with none evicted and `bus:note:` marks of
   300 older delivered notes evicted around them; a mutation that lets the
   LRU take a pending entry turns the test red; a bus refusing for twelve
   polls prints at most `--max-lines` bus lines on each poll.
6. `TestWhatWokeYouIsNamed`: every `WAKE BUS`, `WAKE ENTRY`, `WAKE REPORT` and
   `WAKE LINE` line in a mixed run carries its identity field, and `something
   changed` appears nowhere on stdout or stderr.
7. `TestEveryBusLineIsClassifiedAndCounted`: a bus transcript of twelve lines, one
   of them `INBOX REFUSED`, yields `WAKE SOURCE bus read=12` with the three counts
   summing to twelve and the `REFUSED` line relayed verbatim; a mutation that
   drops one line turns the test red.
8. `TestThreeFailedPollsEndTheWatchLoudly`: a bus that exits 1 three times in a
   row ends the watch with `WAKE BROKEN source=bus failures=3`, exit 2; two
   failures then a success is two `WAKE POLL` lines, `fail:bus` gone from the
   state, and the watch goes on to its deadline; three failed polls spread over
   **three separate watch calls** with three different reasons — each call
   returning on the changed reason — end the third call `WAKE BROKEN
   failures=3` with `since=` the first call's stamp; one unreadable entry
   beside a readable one under `--final-only` wakes the call and prints the
   unreadable line.
9. `TestTheToolStampsAndATypedTimeIsData`: with an injected clock, the opening
   `WAKE` line's `at=` and `WAKE BROKEN`'s `since=` equal the clock and not the
   wall; a bus note whose subject and body carry a time two hours ahead is
   ordered by its commit stamp; a `--line` whose newest commit's subject carries
   a future date is judged by the commit stamp; the source tripwire finds no
   flag named `--at`, `--stamp` or `--now`, and no time parse over a note's body
   or a report's text.
10. `TestServeSurfacesTheUncertainAndWakesOnlyTo`: a fixture bus with two
    notes `To:` the name, one `Cc:` the name only, and one for another name;
    `serve` with a fake `--on-note` that records its arguments runs once with
    the two `To:` ids in bus order, never the `Cc:` id and never the third,
    the `Cc:` note is recorded `cc` and the exit line says `cc=1`; killed
    with an injected kill point **after** `dispatching` is written and
    **before** the spawn, the restart runs nothing, prints `WAKE UNCERTAIN`
    naming the id and the `--redeliver` command, and the fake records zero
    further calls; `--redeliver <id>` runs it once as `attempt=2
    redelivered=1` and is `WAKE REFUSED` for an id that is not `uncertain`;
    killed **after** the spawn and **before** `delivered` the same; with
    `--on-note-idempotent` the same two kills each run it once more,
    `redelivered=1`, and a second interruption is `uncertain`; killed after
    `delivered`, the restart fires zero more; three notes landing while the
    command runs are `queued` and fired in **one** invocation carrying three
    ids after it returns, never beside it, and `--batch-max 2` fires two then
    one; with `--receipt` a receipt is sent for every id in a batch only
    after `delivered rc=0`, and a command exiting 3 gets `WAKE FIRED rc=3`
    and no receipt for any id; the fake `--on-note` is asserted to receive
    ids only — no argument or stdin holds an `INBOX` line, `carrying=`, or a
    note body; **the surviving child**: the fake `--on-note` for A blocks
    until a release file appears, B lands while A runs and is `queued`,
    `serve` is SIGKILLed with A alive, and the restart prints `WAKE
    UNCERTAIN` for A and `WAKE BLOCKED as=<name> uncertain=A queued=1`, the
    fake records zero further calls while A lives, the exit line says
    `uncertain=1 queued=1`, and a second restart prints `WAKE BLOCKED` again
    and still fires nothing; after A is released and returns, `--redeliver A
    --on-note <fake>` runs A once as `attempt=2 redelivered=1` and the next
    call fires B in its own invocation, never beside A; with
    `--on-note-idempotent` the restart runs A's retry once and still fires B
    only after that retry returns 0; **the completion boundary** (Stella,
    2026-09-11, final read): with `--on-note-idempotent`, the fake for A
    blocks until a release file appears, B lands and is `queued`, `serve`
    is SIGKILLed with A alive, and the restart runs A's retry, which the
    fake answers **immediately with "already accepted" and exit 75** while
    the original A still runs: the entry becomes `uncertain attempt=2
    rc=75`, `WAKE UNCERTAIN id=A attempt=2 rc=75: retry not terminal` and
    `WAKE BLOCKED … uncertain=A queued=1` are printed, B stays queued, the
    fake records no call for B while A lives, and a further restart runs no
    third attempt; after A is released and returns, `--redeliver A --on-note
    <fake>` exits 0, `serve:A` reads `delivered rc=0 redelivered=1`, and only
    then does the next call fire B in its own invocation; the same schedule
    with a fake whose duplicate **waits for the original and then exits 0**
    fires B in the call after the retry returns, never before, and the
    state file shows `dispatching attempt=2` for A for as long as the retry
    ran; a mutation that dispatches B on the retry's non-zero return, one
    that treats the retry's spawn rather than its `delivered rc=0` as the
    boundary, and one that runs `attempt=3` on its own, each turn the test
    red; `--redeliver` without `--on-note` is
    refused naming the flag; a mutation that dispatches B on a restart with
    A `uncertain` turns the test red, and the state codec round-trips a
    value holding a literal `%7C` and one holding `%25` byte-identically;
    an hour with no note fires nothing, the fake was never
    started, and the exit line says `fired=0`; `max_wait=` equals the
    injected clock's longest queued interval; a mutation that writes
    `delivered` before the spawn, one that runs a `dispatching` entry on
    restart without `--on-note-idempotent`, and one that dispatches an
    `addr=cc` note, each turn the test red.
11. `TestDeliveryIsThePrintedLine`: 60 entry changes under `--max-lines 40`
    print 40 and `WAKE MORE kind=entry ... n=20`, the verdict says
    `pending=20`, and the next call with `--max-lines 0` prints exactly those
    20 first; the loop is killed, with an injected kill point, after the
    observed write, after the item lines, and after the `printed=` marks, and
    in each case the next call prints every line the killed call had not
    marked and nothing it had; with an injected stdout that fails mid-write,
    no `printed=` mark is written for the failed line; a mutation that writes
    `printed=` before the print turns the test red; one entry observed red,
    then green, across two polls with the red still unprinted, prints two
    `WAKE ENTRY` lines for it, red first, and a mutation that overwrites the
    unprinted value turns the test red; with `--advance-cursor` and a fixture
    bus of 25 notes — above `nova-bus`'s default `OPEN` display cap of 20 —
    against the real installed `nova-bus`, the loop is killed with an
    injected kill point between `nova-bus inbox --advance` returning and the
    write of its output; the state then holds `bus:advance=inflight`; the
    next call is asserted to run `inbox --open --open-max 25`, `25` being
    the `carrying=` it read and not a constant, prints the `WAKE NOTE ...
    recovered 25 notes from OPEN` line, and the following calls print all 25
    `WAKE BUS` lines — the residual named in **The races** is a repeated
    wake, never a lost one; a mutation that passes a fixed `--open-max 20`,
    and one that clears the marker before the output is written, each turn
    the test red.
12. `TestNewMailReachesTheCheckoutThroughTheAdvance`: a bare remote and two
    clones; a note is pushed from the other clone while one watcher runs alone
    with `--advance-cursor` and is relayed within two polls; a second note
    pushed while the watcher holds unprinted notes is not relayed until they
    have printed, the deferring call prints `WAKE NOTE bus advance deferred`
    once, and the note is relayed within two polls of the queue draining; the
    same without `--advance-cursor` is not relayed, `head=` and `head-at=` on `WAKE SOURCE
    bus` do not move, and the `WAKE NOTE` about fetching is printed once;
    with `--refresh` the note is relayed within one poll, `nova-bus` is
    asserted to have been run as `wait` with `--timeout` and without
    `--advance`, the cursor never moves, the first poll runs `inbox --open
    --open-max <carrying>` once and lists the carried notes before the new
    one, and `--refresh --advance-cursor` together are exit 2; a `wait` that
    exits non-zero on one poll (the remote unreachable) is one `WAKE POLL`
    line, every queue record is intact afterwards, and the next successful
    poll relays the note; in a build without item 3a, `--advance-cursor` is
    `WAKE REFUSED … not in this build; use --refresh`, exit 2; a
    fake `nova-bus` answering `nova-bus v0.10.4` is `WAKE REFUSED` naming both
    versions, exit 2, before the opening line.

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
- **A line can be printed twice, never zero times.** A harness kill between
  an item line reaching stdout and its `printed=` mark leaves the entry
  pending, and the next call prints it again (**The races**, rule 11). The
  duplicate is the price of never losing a wake, and it is deliberately not
  refused away: there is no moment before the write at which a refusal would
  mean anything, because the kill is not the tool's to see coming.
- **Quiet is only as true as the sources.** `WAKE QUIET` means *these sources said
  nothing in this window*, not *nothing happened*. It is a report and not a
  guarantee, as `WAIT TIMEOUT` is — and without `--advance-cursor` it means
  *the checkout as it stands said nothing*, which `head-at=` makes visible.
- **Two advancing polls of latency on mail, none while mail is unread, and a
  pinned `nova-bus`.** New mail arrives through the push inside `inbox
  --advance` and is listed by the following `inbox`; a poll advances only
  behind an empty bus queue, so a window that has been shown more than it has
  read is not fetched for until it reads; a `nova-bus` other than `v0.10.3`
  is refused until this document is re-measured against it.
- **A `serve` dispatch can be uncertain, and a person clears it.** After a
  kill between the `dispatching` write and the `delivered` write the note is
  `uncertain` and waits for `--redeliver`; only under `--on-note-idempotent`
  does the restart run it once more, marked `redelivered=1`, and the queue
  behind it waits for that retry's `delivered rc=0`, the receiver's terminal
  acknowledgement — a retry that returns non-zero is `uncertain` again and
  a person's (rule 10). Never
  a silent duplicate, and never a silent loss.

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
  `nova-bus inbox --advance` makes when the caller asked for `--advance-cursor`
  — the one write-side call, named as such, and the one fetch.
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
   rename, the `|` composition with `%25` then `%7C` escaping and the reverse on load, the `printed=<id>` half of every value, the
   `queue:<n>` records and `queue:next`, the pending predicate as *a record
   names this key*, `bus:advance`, `fail:<source>` streaks, the 300-entry
   LRUs over `bus:line:` and **delivered** `bus:note:` keys with pending keys
   exempt, and an
   exclusive lock on `<state>.lock` reusing `internal/bus`'s lock (`lock.go`,
   `lock_unix.go`, `lock_other.go`) rather than a second lock implementation.
   Tests: round-trip quiet on a second poll (the 2026-09-11 lesson), eviction
   order with a pending record surviving 300 newer delivered ones, an
   observation appended behind an unprinted one and never over it, an
   unparsable file is an error and not a cold start, a killed write
   leaves the old file intact.
2. **`internal/wake/source.go`** — the `Source` interface: `Poll(ctx) ([]Item,
   error)` returning items that each carry a state key, a state value and a
   display line and a delivery id. Every change decision is one comparison in one
   place — observed value against `printed=` — so a fourth source cannot invent
   its own.
3. **`internal/wake/bus.go`** — run `nova-bus inbox`, or `nova-bus wait`
   without `--advance` under `--refresh`, under a timeout; classify by
   first tokens with a **suppress** list and a printing default case; the standing
   vs first-sighting split; the first-poll `inbox --open --open-max <carrying>`
   under `--refresh`; the `nova-bus version` pin; `head=`/`head-at=` from
   `git log -1`; and `--advance-cursor` refused as not in this build. Tests: an
   `INBOX REFUSED` wakes and is relayed verbatim; a repeat is `STANDING` and does
   not wake; an unknown future token prints; `--refresh` fetches and moves no
   cursor; a note past the cap is pending, not lost.
3a. **`internal/wake/advance.go`** — after item 3 is read and against the
   pinned binary: the `--advance-cursor` guard (`--as` required, lock held for
   the call, the four-step transaction — plain `inbox`, spool, print,
   `--advance` only behind an empty bus queue under the `bus:advance` marker —
   and the `inbox --open --open-max <carrying>` recovery with its re-read).
   Tests: `--advance-cursor` without `--as` is exit 2; the cursor waits behind
   the print; the recovery's `--open-max` equals `carrying=` and is never a
   constant; tests 5, 11 and 12's advancing halves, green against the real
   `nova-bus v0.10.3` before the flag is admitted.
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
    under the timeout, the last sign per name, the `<sha>|<state>|<stamp>`
    state value, OFFLINE once and BACK once. Plus, in `main.go`:
    `--on-deadline` echoed on the opening line and the verdict, `--entry-interval` with a per-source due time,
    the pid in `<state>.lock`, the `WAKE SOURCE` counts, and the three-in-a-row
    `WAKE BROKEN`. Tests: the eight in **Tests this spec demands**.
12. **`internal/wake/serve.go`** — the `serve` loop: the fetch per interval,
    `To:` dispatched and `Cc:` recorded, the coalesced batch under
    `--batch-max`, the `serve:<id>` states `queued`, `dispatching
    attempt=<n>`, `delivered rc=<n> redelivered=<0|1>`, `uncertain` and `cc`,
    each written before or after the step it names and never during it, the
    `uncertain` surfacing with `--redeliver --on-note`, the queue blocked
    behind an unresolved `uncertain` (`WAKE BLOCKED`) and the one-more-run
    only under `--on-note-idempotent` with the queue held until that retry's
    `delivered rc=0` and `uncertain … rc=<n>` on a non-zero return, ids and nothing else on the command line, the
    receipt per id after `delivered rc=0` and never before, `max_wait`, the
    `stop` file and `--hours`. Tests: test 10, with the kill points injected.

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
   over consumed bus notes, with the cursor already at `HEAD`: the words were a
   promise nothing kept. Rule 11: the cursor waits behind the print, the
   elided notes are pending in the state, and the next call prints them.
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

## Ideas folded on 2026-09-11

| source | the idea, in six words | disposition |
|---|---|---|
| Stella, spec repairs | pending retained until delivery; LRU never pending | already, rule 11 and **State** |
| Stella, spec repairs | plain inbox does not list OPEN | already, measured (**How the checkout receives mail**) |
| Stella, spec repairs | grow `--open-max` to the count, retry | step 4: re-read on a short list, marker kept |
| Stella, spec repairs | leave advancement out of v1; use `wait` | `--refresh` (wait without advance); `--advance-cursor` refused until item 3a |
| Stella, spec repairs | serve: uncertain, not silent retry | rule 10: `uncertain`, `--redeliver`, `--on-note-idempotent` |
| Stella, spec repairs | receipt only after durable acceptance | already, rule 10 (`delivered rc=0`); per id in a batch |
| Johnny, addition 1 | an in-session poll is not serve | the verb paragraph and the lessons table |
| Johnny, addition 2 | empty minutes cost zero tokens | rule 10: the command starts for a note and nothing else; test 10 |
| Johnny, addition 3 | quiet poll is counts, not carrying | rule 10: ids only, never `inbox` output; test 10 |
| Johnny, ideas 1–3 | serve is the wake; machinery sends Heard | already, rule 10 |
| Johnny, idea 4; Emma, C2 | `Cc:` is not `To:` | rule 10: to wakes, cc does not (nova-tools #53) |
| Stella, ideas 1–2 | machinery observes; coalesce per item; batch decisions | rule 10: coalesced dispatch, `--batch-max`; `--final-only` already (entries) |
| Stella, idea 6 | measure latency and duplicate wakes | rule 10: `max_wait=`, `redelivered=`, `uncertain=` on the exit line |
| Emma, A1–A2, C1 | event-driven wake, silence on idle | already, rule 10 |
| Emma, B1–B3 | nova-bus counts by default, batch advance | not folded: `nova-bus`'s (Emma's tool, #53); `serve` never feeds the carrying list to a model regardless |
| Emma, D | rolling coordinator session boundary | not folded: a window's practice, not a tool rule |
| Rowan, ideas 1, 9 | serve for every line; no status polls | rule 10, narrowed: `serve` is an opt-in adapter per consenting line — starting one under a name is that name's person's decision (the receipt paragraph), never a fleet setting; no status polls is the first lesson (Stella's closing read) |
| Stella, closing read | recovery proves idleness before any dispatch | rule 10: an unresolved `uncertain` blocks the receiver's queue, `WAKE BLOCKED` with the remedy; `--redeliver` names `--on-note`; `--on-note-idempotent` declares concurrency tolerance; `%` escaped as `%25` in the state codec (test 10) |
| Stella, final read | the idempotent exception needs a completion boundary | rule 10: `--on-note-idempotent` names the stronger contract — exit 0 for an id is terminal completion, never "already accepted"; the boundary is the retry's `delivered rc=0 redelivered=1` in `serve:<id>`, before which no different id is dispatched; a non-zero retry is `uncertain … rc=<n>` and a person's (state, grammar, test 10) |
| Rowan, idea 4 | pointers in the window, detail in children | not folded: a window's practice |
| Freddy, idea 2 | webhook wakeups over polling | already, rule 10 (a fetch outside the session); a webhook is a v2 source |
| Freddy, idea 5 | bundle acknowledgements | rule 10: one batch, receipts per id by machinery |
| DeepSeek, idea 3 | filtered event wakeups per interest | already, rule 10 (`--as`, `To:`) |
| DeepSeek, idea 1 | state digest plus a cursor | already, **State** (the queue and `printed=`) |
| nova-tools #53 | `to` wakes, `cc` does not | rule 10; the send side (`--to all`, `wakes=`) stays nova-bus's |
| ideas #273 | evidence arriving after the belief | already, rule 11 (observed before printed, nothing coalesces) and `head-at=` |
| ideas #357 | relay a note, never execute it | already, the data paragraph at the top |
| nova-tools #35 | the table races | already: `nova-bus`'s ids and push-retry are what `serve` and `--refresh` rely on; the version is pinned |
