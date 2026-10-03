# nova-decide: a decision system trained from its own record

`nova-decide` makes typed decisions with probabilities and keeps every one, so
how far each kind of decision can be trusted is read from what it got right.
A backend is the transport that answers; the concept is the decision system
above it: decisions, backends, and the record.

## 1. The two sides over one record

| side | verbs | what it does |
| --- | --- | --- |
| decide | `ask`, `read` | asks a schema over a state through a backend; prints every answer with its probabilities; appends the decision to the record |
| train | `outcome`, `calibrate` | attaches what turned out true to a recorded decision; reads the bar a decision's answer supports from the decisions whose outcome is known |

Both sides read and write one file, the record (`--record`). The truth lives
there and nowhere else: the calibration is computed from it on every call and
never stored beside it. Export of the record as a backend's training data, and
driving a backend's training, are the train side's next verbs; they read the
same record and are not built yet.

Sizes: a decision line is its state plus about 1 KB; a read of a sprint card
is 3 to 55 KB of state, so a thousand reads are about 15 MB and load in one
process. The record is read whole on every call.

## 2. A decision is a named schema over a state

A schema is JSON: `{"name": <decision>, "questions": {<name>: <question>}}`. A
question is one of two types:

- `choice`: `{"type": "choice", "instructions": <text>, "criteria": {<option>: <what it means>}}`,
  at least two options; answered with the chosen option and a probability per option.
- `noul`: `{"type": "noul", "instructions": <one statement>}`; answered with the
  probability that the statement is true (`p=yes:<p>`; the value is `yes` at 0.5 and above).

`ParseSchema` names every problem of a schema in one error (no name, no
questions, a choice with fewer than two options, a noul with options, an empty
statement, an unknown type, an unknown field). The schema's hash is its
identity in the record: decisions are calibrated together only when they asked
the same questions.

A backend's answers are held to the schema (`Schema.Check`): one answer per
question, of its type, a choice's value one of its options and given a
probability, a choice's probabilities only for its options, a noul's
probability of yes and nothing else, every probability in [0, 1], and nothing
that was not asked. An answer that does not fit is a failure, never repaired.

## 3. Backends

A backend is `Name()` and `Ask(ctx, schema, state) (answers, usage, error)`.

- `jev`: TypeSafe's System One model, `jev-latest`. One POST of
  `{"state", "model", "questions"}` to `https://api.typesafe.ai/v1/systemone`
  with the key as a bearer token; the response is
  `{"answers": {<name>: {"type": "choice", "choice", "probabilities", "confidence"} | {"type": "noul", "noul"}},
  "usage": {"input_tokens", "output_tokens"}}`. A choice with no probabilities
  carries its confidence as the chosen option's probability. An answer with no
  probability at all (a noul with no `noul`, a choice with neither
  `probabilities` nor `confidence`) is a failure naming the question: a missing
  number is never read as 0, which would be a confident "no". The HTTP client is
  a `Send` function injected into the backend, so the decision is made apart
  from its transport and every test runs on a fake; `HTTPSend`, the real one, is
  the only code in `internal/decide` that opens a socket, and its test answers
  through an `http.RoundTripper` in process. The key is read from the
  environment variable `JEV_API_KEY`, which `nova-secrets exec --only
  JEV_API_KEY -- nova-decide ...` sets; it is never a flag, a file, or printed.
- `fixed`: answers from a JSON file, `{<question>: {"choice", "p"} | {"noul"}}`,
  whatever the state. It needs no network and no key: a first run, a test, a
  replay of answers recorded elsewhere.

## 4. The record

A JSON-lines file. Each decision is one line,
`{"decision": {id, decision, schema, backend, at, inputs, state, answers, usage}}`;
each outcome is one line, `{"outcome": {id, label, note, at}}`. `inputs` names
each input file and its SHA-256; `state` is the exact text the backend was
asked over, kept because it is the training input. Lines are only appended,
by a writer holding the record file's own exclusive lock (go-internal/lockedfile,
an adopted module); a reader takes the shared lock, so it never meets half a
line. Loading folds each outcome into its decision.

- An id is the caller's `--op`, or `<decision>-<12 hex>` of the schema, the
  stamp and the state. The same `--op` over the same decision, schema (by hash)
  and state returns the recorded decision and asks nothing (`recorded=existing`),
  so a long run resumes where it stopped; over another decision, schema or state
  it is refused.
- An outcome is attached once. The same label again changes nothing
  (`changed=false`) and writes no line; another label is exit 1, naming both.
- Loading refuses, by file and line, a line that does not parse, a decision id
  recorded twice, an outcome for an id with no decision before it, and a second
  outcome line for one id (a hand-edited record never has its last line win).

The record's states are few and plain (a decision recorded, then labelled once)
and the one writer is the lock's holder. Its model is owed: `tla/DecideRecord.tla`,
with actions Ask, Replay and Attach and the invariants above (one decision per
id, at most one outcome per decision, an outcome only after its decision), checked
by TLC on a bench and recorded in the TLC records; it lands with the export verb,
the first reader of the record beside calibrate.

## 5. Calibration

`calibrate --decision <name> --question <q> --positive <labels> --negative <labels>`
scores each decision of that name, under the newest schema the record holds for
it, whose outcome label is positive or negative: `<q>` is a noul's name (scored
by its p of yes) or `<choice>=<option>` (scored by that option's p). It prints:

- the counts (positives, negatives, and the decisions skipped: unlabelled,
  labelled otherwise, or asked under another schema);
- the AUC, the probability that a positive scores above a negative (ties half);
- one `BAR at=<p> caught=<positives at or above p> of=<n> bounced=<negatives at or above p> of_negatives=<n>`
  line per `--bars` value (default 0.5, 0.7, 0.9);
- `CATCH-ALL`: the highest bar that still flags every positive, and the
  negatives it bounces there. That is the bar the record supports when no
  positive may pass.

A record with no positive or no negative outcome is refused: a bar is read from
both. An option no scored decision chose or gave a probability (a misspelling,
`verdict=BOUNCEE`) is refused too, rather than scoring every decision 0.

## 6. The read decision

`read --card <file> --diff <file> [--rule <file>]` is the first read of a
worker's diff against the card that asked for it. The state is the card, the
rule when one is given, and the unified diff, each under its own heading, and
nothing else. Five questions:

| name | type | statement |
| --- | --- | --- |
| `does_task` | noul | the diff does what the card's task states, for the lines it lists, with the meaning kept |
| `lines_changed` | noul | every line the card lists is changed, or the card's words allow it to stay |
| `inside_paths` | noul | every file the diff changes is in the card's PATHS, or one the card names to update |
| `defect` | noul | the diff introduces a defect: an unasked behaviour change, broken text, or a lost reference |
| `verdict` | choice | LAND, BOUNCE or UNSURE, by the rule below |

The verdict rule, as the schema asks it (each option's criterion, word for word;
`TestReadSchemaIsValid` holds this table to the code):

| option | rule |
| --- | --- |
| `LAND` | the diff does the card's task, inside its paths, and introduces no defect |
| `BOUNCE` | the diff misses the task, leaves a listed line, changes a file outside its paths, or introduces a defect |
| `UNSURE` | the card and the diff alone cannot settle it; a stronger reader is needed |

The read schema's hash is pinned to the fixture record
(`cmd/nova-decide/testdata/record.jsonl`, and the docs/TESTS.md transcript that
prints it): a reworded question or rule changes the hash, the test names the
stale fixture, and the fixture is regenerated in the same change. Old records
keep their old hash, and calibrate never pools the two.

The read line carries the verdict and its probability; a gate decides on
`defect` or `verdict` at the bar `calibrate` reads from the read's record.

## 7. Output, refusals and exits

Every verb prints one result through internal/tool: the typed line
(`READ OK id=... verdict=... p=...`, one `READ ANSWER` item per question) or,
with `--json`, the same value as one JSON object. A refusal is one line,
`<VERB> REFUSED: <every problem, each with what it wants>; run: <remedy>`, at
exit 2, and writes nothing. A backend that fails (no answer, an HTTP status, an
answer outside the schema) is `<VERB> FAIL id=... backend=...: <why>; run:
<remedy>` at exit 2, and records nothing. Exit 1 is an outcome that conflicts
with the one recorded. `ask`, `read` and `outcome` take `--dry-run`: the plan,
with no backend call and no write.

## 8. The first read of a flash card

The sprint asks the read decision as the first read of a flash card
(docs/SPEC-SPRINT.md section 6, the decide read) through the library, not the
binary: `FirstRead` asks the read schema over the card and the diff through the
backend it is handed (`Make`: the same ask, check, record and replay `ask` and
`read` run) and routes the decision by p(defect) at two bars (`Bars`, read from
the sprint row's `decide_bounce` and `decide_review` by `ParseBars`: both
probabilities, the review bar at most the bounce bar):

| p(defect) | route | the read |
| --- | --- | --- |
| at or above the bounce bar | `bounce` | broken, its finding `Finding`'s line |
| from the review bar to under the bounce bar | `strings` | a strings read runs |
| below the review bar | `land` | ok, with no model read |

The card the sprint passes is the work card's brief alone, the state the bars
were calibrated on; no rule is passed yet, where the calibration passed the E1
rule for docs and diary cards. The verdict answer and inside_paths are recorded and never routed on (the
calibration of 2026-10-02: p(defect) AUC 0.869, the verdict 0.711, inside_paths
0.612). `Settle` attaches a strings read's verdict as the decision's outcome, ok
as `LAND` and broken as `BOUNCE`, so the record trains on every read that took
the strings route; a review round attaches its own label with `outcome`.

## 9. The judgment decision

`nova-sprint answer --decide` (docs/SPEC-SPRINT.md section 8, answered by
nova-decide) asks the judgment decision for each card of each routine judgment in
the sprint's inbox, through the library (`JudgmentSchema`, `JudgmentState`,
`Choose`), never the binary. A routine judgment is one of these kinds; every other
judgment is left for the coordinator:

| the inbox's type | kind |
| --- | --- |
| a reader found it broken | `broken` |
| work came back failed | `failed` |
| a primary is blocked on something dropped | `blocked` |
| stalled | `stalled` |
| stream stopped: conflict on a card | `conflict` |
| a work card is past its deadline | `deadline` |
| cannot ask | `cannot-ask` |
| ready to accept | `ready` |
| a card reached its bound | `bound` |

The state is the judgment's type and its note's text, the card and how many cards the
judgment holds, the verbs the judgment prints (`ALLOWED VERBS`, read from the decisions
`inbox` prints for it: `VerbOf`), and the card's log (`nova-sprint log --card`, less its
summary, cost and field-change lines: `HistoryOf`), its last 40 lines, each cut at 300
characters. Three choices:

- `verb`, the answer, one of seven options (each option's criterion, word for word;
  `TestJudgmentSchemaIsValidAndPinnedToTheSpec` holds this table to the code):

| option | criterion |
| --- | --- |
| `rework` | send the card back for another attempt, carrying its own finding or report, or the FIX: right when another attempt can succeed |
| `ask-another` | ask another reader: the finding is wrong, or the read ended with no verdict, and the work itself looks done |
| `accept` | accept it for merge: the readers it needs said ok at its head |
| `ack` | acknowledge it: the card can run without the dropped need it names, so nothing is to be done |
| `wait` | leave it open for 30 minutes: the cause is outside the card (a member working through its queue, a reader still reading) and time alone clears it |
| `drop` | take the card off the table: it is done already, cannot be done as written, or cannot run without what was dropped; never for a failure a retry or a fix cures |
| `release` | release the sentinel: the gate for its wave is passed |

- `fix`, the text a rework carries where the printed command takes `--fix '<fix>'`:
  `own` (the card's own finding or report: no text, so a command that wants one is
  listed), `from-tip` (the conflict text: start again from the tip, redo only the listed
  lines), `pro-tier` (run on the pro tier, the task unchanged), `retry` (run it again,
  the task unchanged). Each is a fixed text (`Fixes`): the decision chooses, it does not
  write.
- `reason`, why a drop is chosen: `already-done`, `cannot-be-done`, `need-dropped`,
  `reads-exhausted` (`Reasons`); shown with the listed drop.

`Choose` applies the verb chosen when the judgment prints it, it is neither `drop` nor
`release`, and its probability is at or above the bar (`decide_judgment_bar` in
nova-config's sprint row; `--bar` overrides it). Anything else is listed for the
coordinator with why. The row ships the bar empty: with none, answer --decide applies
nothing, records every decision and lists what a bar would apply, until the coordinator
sets one. 0.8 is the bar the calibration below documents: at it the decision applies 40
of the 100 and 39 of those are the coordinator's own verb. An applied `ack` gives the reason `nova-decide (p=<p>):
the card can run without the dropped need; a conflict is handled at merge`.

A card whose judgment text or last ten log lines carry a provider's refusal for want of
payment (HTTP 402, out of credit, insufficient funds or quota: `PaymentRefusal`) is not
asked at all: it is listed, "a payment is the owner's".

Each decision is recorded under its judgment's id: the note's id (`<note id>:<card>` for
a note of several cards), with `inputs` naming the judgment, the note, the kind, the
card, the verb chosen and what was done (`act`: applied, listed, refused). A card of a
judgment is decided once: the same id again is answered from the record and asks
nothing, and one applied before whose judgment is still open is listed, never applied
twice; a card the decision reworked within the last hour is listed, not reworked again. Its outcome is attached once the card's state says it (`JudgmentOutcome`):
`landed`, `dropped` (off the table), or `came-back` (another judgment open on it).

The calibration fixture (`internal/decide/testdata/judgment-calibration.jsonl`) is 100
judgments the coordinator answered on 2026-10-02 and 03, read from the sprint's log,
each with the coordinator's verb and the card's outcome; the record of Jev's answers to
them is `internal/decide/testdata/judgment-record.jsonl`, and
`TestJudgmentCalibrationAgreement` recomputes the agreement it states. Each state is
built by `JudgmentState` from the card's log up to the second the coordinator answered,
as answer --decide builds it live (one card a judgment), with the fleet's machine,
person and home-directory names replaced by placeholders (m1, coordinator, owner,
friend-a, /home/user) before Jev was asked, so the record holds the very state each
answer was given. The kinds are as the log held them, at most two judgments of one
card a kind (the bound's 752 answers were the night's runaway of a few cards); no
ready-to-accept judgment was answered by the coordinator that day. Jev's 100 answers
cost 189,920 input tokens, under a cent.

| kind | judgments | the coordinator's verb | applied at 0.8 | of those, the coordinator's verb |
| --- | --- | --- | --- | --- |
| blocked | 21 | 3 | 0 | 0 |
| bound | 13 | 3 | 1 | 1 |
| broken | 19 | 18 | 16 | 15 |
| cannot-ask | 6 | 1 | 0 | 0 |
| conflict | 6 | 6 | 5 | 5 |
| deadline | 10 | 6 | 0 | 0 |
| failed | 22 | 20 | 18 | 18 |
| stalled | 3 | 2 | 0 | 0 |
| all | 100 | 59 | 40 | 39 |

| the card's outcome | judgments | the coordinator's verb |
| --- | --- | --- |
| landed | 15 | 10 |
| dropped | 7 | 1 |
| came-back | 74 | 45 |
| open | 4 | 3 |

What it says. At 0.8 the decision applies 40 of the 100, and 39 of those are the
coordinator's own verb; the one that is not is a rework where the coordinator asked
another reader. Of the 40, 8 cards landed and 32 came back: the routine judgments of
that day mostly came back whoever answered them, so landing is not yet a label the
bar can be read from, and the coordinator's verb is the better label for now. Where
it disagrees it mostly chooses drop: 18 of the 21 blocked cards the coordinator acked
under the owner's ruling of that afternoon ("safe to run, not dependent", which the
state does not carry), 10 of the 13 bound cards the coordinator reworked (nine of
which came back and one was still open: the night's runaway), 4 deadline cards the
coordinator waited on, and 2 failed cards. A drop is never applied, so each of those
is listed for the coordinator, which is what the bar is for. Deadline, stalled,
blocked and cannot-ask judgments all stay under the bar.
