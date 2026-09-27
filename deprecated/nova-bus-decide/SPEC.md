# Retired bus decision specification

Reference only, retired by Glenn on 2026-09-27.

### nova-bus inbox triage (opt-in, public buses only)

Triage an inbox into carry / receipt / noise on public material. Opt-in per
bus, public buses only — rule 4 bars private bus notes from the state. Below
the floor the note stays unclassified and today's reader handles it.


   **#1644 IS SETTLED, in favour of rule 4.** `docs/CLI.md` documented an `--allow-private` flag on
   `nova-bus inbox --decide` and rule 4 had no such door. Stella's ruling of 2026-09-19T23:13Z
   (`stella-17a099112fb1`) settles the disagreement: "A bus without `.public` is private. Explicit
   `inbox --decide` may use rules or a mechanically admitted local/private decider with no network
   or provider-key access; it should not blanket-refuse when that safe path exists. If the
   requested route cannot be satisfied privately, refuse before client, key or network, and never
   fall back to a public route. A `local` label or redaction alone does not prove the boundary.
   `wait` remains rules/local passive and does not instantiate a deciding poller; it has no
   `--allow-private` override. Do not add `ALLOW-PRIVATE=true` absent a separately authorized real
   override: under this ruling private evidence is not allowed out, so that marker would
   misdescribe the action. Report the actual privacy/source/refusal through existing typed
   receipts." The flag is **removed** from the tool, no marker replaces it, and the blanket
   refusal it pointed at is replaced by the rule table: `inbox --decide` on a private bus answers
   every note the table has a row for and refuses the rest with `privacy=private decider=rules
   why=private-evidence` before any client, key read or call, on the run's existing `INBOX
   REFUSED` and `INBOX DECIDED` receipts (`cmd/nova-bus/private.go`, whose type has no endpoint,
   no key-env and no client field: that absence is what admits it, not a flag or a label).
   `local` is **not yet admitted here**, because nothing in `internal/decide` yet carries the
   mechanical `sees=private` capability D1/D2 describe; until it does, a `local` route on a
   private bus takes the same refusal, and a loopback URL is a label rather than an admission. An
   explicit remote-private inbox exception remains outside this: it needs separate live scoped
   authorization and is not in the tool. 
### 1. The note reading: does this note wake anybody

SPEC-AHEAD: #1617

**The question.** `note/v1`, asked by `nova-bus inbox --decide` in the same provider call as the
`kind`, `needs_reply` and `blocked` questions it already asks (`docs/CLI.md:479`; they stand
unchanged):
`wake` ∈ {`ack`, `info`, `needs-action`}. `ack`: the note only confirms receipt or completion of
something the reader already knows, and asks nothing. `info`: the note reports a fact and asks
nothing of this reader. `needs-action`: the note asks this reader to do, decide, review, answer or
stop something, or reports something broken that this reader owns. Tamper answer: `needs-action`.

**The evidence.** The note's `Subject` (first 200 bytes) and the first 600 bytes of its body, head
truncation, after redaction; nothing else, and never the headers' names. Bound: 1024 bytes. The
bus's privacy is S7's: a bus with no `.public` marker goes to `rules` or `local` and to nothing
else. **A truncated note cannot be deferred**: a request that sits after byte 600 is absent from
the evidence, so a body the cut touched is relayed at once whatever the answer, `wake=needs-action
why=truncated`, and the call, if one is made, is a label. Bounded delay is accepted below only
for a note the decider saw whole.

**The mechanical fields.** `owner=` is the note's `To:` header, read by the tool. `ref=` is every
`#<digits>` and every `<owner>/<repo>#<digits>` in the subject and then the body, in order, at
most four, then `+<n>`; `ref=-` where none. Neither is asked of any provider.

**The rule table** (consulted first, no call): a subject beginning `STOP:` or `HOLD:` is
`needs-action` (the existing structured-signal rule, `docs/CLI.md:479`, unchanged: never sent
anywhere); a note carrying `Re:` whose subject begins with a prefix from the acknowledgement table
(data, `--ack-prefixes <file>`; the shipped default holds `ACK:` and `RECEIPT:`) is `ack`. Anything
else has no rule row. A subject is as writable as a body, so a rule-table `ack` is treated below
exactly as a provider's: it defers and never suppresses.

**What the caller does, and the one deferral in this section.** Each `INBOX NOTE` line gains
` wake=<ack|info|needs-action|unknown> owner=<lane> ref=<refs> due=<stamp|->`, and the closing line
gains `wake=<n>`, the count that is `needs-action` or `unknown`. The mechanical baseline (S4) is
today's: every change to the bus is relayed to the waking lane at once. `nova-bus wait --decide`
and `nova-wake watch --bus --decide` relay at once for `needs-action`, by rule or by answer, and for `unknown` (below the
floor, tampered, untuned, no decider, private evidence). For `ack` and `info`, by rule or at or
above the floor, the answer does not suppress the wake; it **defers** it, and the
deferral is bounded by a clock the answer cannot touch: `--defer-max <duration>` (required with
`--decide` on `wait` and `watch`; refusing to guess it), counted from the note's arrival as the
watcher's own monotonic clock saw it. `due=` is that deadline. When the oldest deferred note for a
lane comes due, the watcher relays ONE wake for all of that lane's deferred notes, and an
immediate wake for any reason carries the deferred ones with it. So the most an obedient provider
can do to a note that needed action is delay its wake by `--defer-max`, once; it cannot sleep a
window. The note itself is never altered, moved or marked read by a decision (rule 11, :99). This
is the only place in the section where an answer loosens anything by itself, it is reversible, it
is bounded mechanically, and S4 names it as the exception.

**The deadline is durable, and a deferred note is never advanced away.** A deferral is a row in
the watcher's own state file beside the bus clone, `<clone>/.nova-wake/deferred.tsv` (`note id`,
`lane`, `arrived`, `due`, `decision id`), written before the watcher moves on from the note and
removed only when the wake that carries it has been relayed. Three edges, each a demanded test:
when `wait --timeout` ends, or `watch` is stopped, before the oldest `due`, the watcher relays one
wake for every deferred note **before it exits**, so a bounded process never leaves a note
deferred past its own life; when a watcher starts and finds rows, a row whose `due` has passed is
relayed at once and a row whose `due` has not keeps its **original** `due`, never one counted
from the restart, so re-waiting cannot push a deadline; and a note the state file does not hold is
not deferred at all, so a lost file fails toward a wake and never toward silence.

**The rows.** The next `inbox --decide` pass that finds a note it has a decision row for appends
an **observed** row (D6) and no truth: `event=replied` when the note has since drawn a `Re:`
from its owner, `event=closed` when its owner closed it, `event=unreplied` when it left the open
list with no reply after `--unreplied-after <duration>` (default `168h`). None of those is a
label: a reply may be thanks, and an unanswered note may have been read and acted on. Truth for
this question comes from the escalated reader and the audit sample (D6), and from nowhere else.

**Cost.** One call a note, already being made where `--decide` is on; the added question is a few
dozen tokens. The saving is in wakes folded together: twenty acknowledgements inside one `--defer-max` are one
window turn and not twenty, and a window turn is measured in hundreds of thousands of tokens, so
the adoption pays for itself if it folds one wake in a thousand notes.

