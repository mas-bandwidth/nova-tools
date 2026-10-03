# nova-decide: a decision system trained from its own record

`nova-decide` makes typed decisions with probabilities and keeps every one, so
how far each kind of decision can be trusted is read from what it got right.
A backend is the transport that answers; the concept is the decision system
above it: decisions, backends, and the record.

## 1. The two sides over one record

| side | verbs | what it does |
| --- | --- | --- |
| decide | `ask`, `read`, `brief` | asks a schema over a state through a backend; prints every answer with its probabilities; appends the decision to the record |
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
with the one recorded. `ask`, `read`, `brief` and `outcome` take `--dry-run`: the plan,
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

## 9. The brief decision: card quality before add

**Uncalibrated.** p(converges) is a rank, not a probability, and on the only labels
there are (234 reviewed cards) its AUC is 0.600, about two standard errors above chance.
No bar may be set (nova-config's sprint row `decide_brief_bar` stays empty, which
reports only) until `calibrate` over the brief record's own `landed`, `reworked` and
`dropped` outcomes supports one. Every line that prints the reading says
`uncalibrated=true`.

`brief --card <file|dir>` reads a card as a flash child with no memory reads it,
before the card is added (the owner, 2026-09-30: "give it all the details it
needs, like a real child agent"). The state is the card, under its heading, and
the frame the sprint hands every child beside it (JOB.md names the staged
checkout, its start and how the commit leaves; the rules file the card names is
appended), so a card is not asked to say what the frame says. Nine questions:

| name | type | asks |
| --- | --- | --- |
| `repo_branch` | noul | the card names the repository and the branch or base it starts from |
| `files_named` | noul | the card names where the work is: the files, or a PATHS glob of them, and in them the functions, tests, sections or lines to change |
| `gate_stated` | noul | the card states the gate, the exact commands, written to run as given |
| `commit_stated` | noul | the card states the commit message |
| `report_stated` | noul | the card states what the child reports at its end |
| `one_thing` | noul | the task is one thing: one change toward one end (a tree's steps toward one end are one thing) |
| `ambiguous_step` | choice | the first step a child could read two ways or not know when it is done: `none`, `step-1` to `step-12`, or `unnumbered` |
| `minutes` | choice | how long a capable flash child takes, the gate included: `under-10`, `10-20`, `20-45`, `45-90`, `90-180`, `over-180` |
| `converges` | noul | the child ends with a commit that does the task and passes the gate on its first attempt, asking nothing |

A card fails a need (the six nouls before `ambiguous_step`) when its p of yes is
under 0.5, and fails `ambiguous_step` when it names a step; the reading is one
line, `p_converges=<p> minutes=<option> failed=<need(p),...,ambiguous_step:step-<n>(p)> uncalibrated=true`
(`failed=-` for none), the same wherever it prints. A card's decision id is
`<card>@brief-<8 hex>`, the hex of the schema and the state: a brief rewritten
after a refusal, or asked under a reworded schema, is a decision of its own, and
the same brief asked again is answered from the record. A directory's cards are
its `*.md` files that are not directories, none below it, as `nova-sprint add
--brief-dir` reads them (`CardPaths`), each card's id its file's name without `.md`.

**A batch.** Every card of one call is one batch (`MakeAll`): the record is read
once, a card it holds is answered from it, the rest are asked through the backend
at most `BriefWidth` (8, add's width and `--width`'s default) at a time, and every
new decision is appended in one write under the record's lock. Once the batch's
context is done no further card is asked; each card unanswered is its own error,
nothing is recorded for it, and the rest are. With no record a batch keeps
nothing. `nova-decide brief` fails at exit 2 when a card went unanswered, so a
script runs it again (a recorded card costs nothing).

**Where it is asked.** `nova-sprint add`, under `JEV_API_KEY`, asks it of every
card it names with a brief (a card per brief file, or the one brief of each id;
a `--count` add names no id before the store numbers it, and a sentinel carries
no brief) after its own checks (the arguments, the files, the card lint), so a
card add refuses costs no call, and before it writes. The whole batch has one
deadline, a minute: past it each unanswered card is a `NOTE brief:` line and the
add goes on. With a server named, add runs its checks and asks where it is typed
(the caller's key and files; the server's one line of control holds no backend
call) and sends the server the add with each card's op (`--brief-op <id>=<op>`)
and the record; in that half the card lint runs only under `--rules`, since the
sprint's recorded rules file is the server's to read, so a brief the server's
lint refuses may have cost a call. One `BRIEF card=<id> op=<op> <reading>
recorded=<new|existing>` line per card (on stderr under `--json`). The record is
the coordinator's, `<root>/decide/brief.jsonl` with the root `~/nova-sprint`
(never a cache directory), or the file `add --decide-record` names. A card stores
its op and its record (`brief_op`, `brief_record`); a brief replaced by `nova-sprint
brief` drops both. With `decide_brief_bar` set, a card under it refuses the whole
add, exit 2, nothing written, naming each such card, its p and the questions it
failed, and saying the decision is uncalibrated. A decision that cannot be made (no
key, the bar or the record unreadable, the backend failing a card) is one `NOTE
brief:` line and the add goes on. `nova-swarm lint --card <file> --decide` prints
the same reading on one `LINT DECIDE` line after the lint's own and never changes
its verdict (`--decide-answers` answers from a file, `--decide-record` records).

**The outcome** is the card's end in the sprint, attached by the exact op the card
stores, in the record it names: `landed` when land lands it at attempt 1,
`reworked` at a later attempt (the note names it), `dropped` when the coordinator
drops it (the note is the reason). A card that stores no op attaches nothing; an
op its record does not hold (a record on another machine, a record removed) is a
`NOTE` line of land or drop, never another decision's label. `calibrate --decision
brief --question converges --positive landed --negative reworked,dropped` reads
the bar from the sprint's own record.

**The calibration of 2026-10-03.** Jev over 847 cards: the 234 cards of the two
review rounds of 2026-10-02, scored against their review labels (ok as positive;
wrong, ugly and outside as negative), and the 613 tree cards held for the next
waves. The shipped schema and state, asked in 45 seconds: `converges` AUC 0.600,
`one_thing` 0.680, `gate_stated` 0.610, `report_stated` 0.561, `commit_stated`
0.551, `files_named` 0.547, `repo_branch` 0.447, `ambiguous_step=none` 0.509. Two
earlier asks measured the questions before they were settled: over the card alone,
`files_named` asking for "the exact files" was inverted (0.252) and failed 511 tree
cards whose PATHS are globs; with the frame added it was still inverted (0.289);
reworded to accept a glob it fails none and scores 0.547. `ambiguous_step` named
`step-1` on 612 of 613 tree cards over the card alone, and on 611 with the frame:
it does not discriminate (0.509) and is to be reframed or dropped before any bar.
A card written to every question scores 0.72; the reviewed cards 0.33 to 0.48; the
schema wave's tree cards 0.08 to 0.21. The review label is a diff's quality, not a
card's convergence: the brief record's own outcomes are the measure that counts.
