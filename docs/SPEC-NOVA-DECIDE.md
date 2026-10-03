# nova-decide: a decision system trained from its own record

`nova-decide` makes typed decisions with probabilities and keeps every one, so
how far each kind of decision can be trusted is read from what it got right.
A backend is the transport that answers; the concept is the decision system
above it: decisions, backends, and the record.

## 1. The two sides over one record

| side | verbs | what it does |
| --- | --- | --- |
| decide | `ask`, `read`, `attempt`, `grade` | asks a schema over a state through a backend; prints every answer with its probabilities; appends the decision to the record |
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
(`READ OK id=... verdict=... p=...`, `ATTEMPT OK ... class=... p=...`,
`GRADE OK ... grade=... p=...`, one `<VERB> ANSWER` item per question) or,
with `--json`, the same value as one JSON object. A refusal is one line,
`<VERB> REFUSED: <every problem, each with what it wants>; run: <remedy>`, at
exit 2, and writes nothing. A backend that fails (no answer, an HTTP status, an
answer outside the schema) is `<VERB> FAIL id=... backend=...: <why>; run:
<remedy>` at exit 2, and records nothing. Exit 1 is an outcome that conflicts
with the one recorded. `ask`, `read`, `attempt`, `grade` and `outcome` take
`--dry-run`: the plan, with no backend call and no write.

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

## 9. The attempt decision

`attempt --brief <file> [--result <file>] --reason <line>` is how a work take ended,
asked after it ends (the owner, 2026-10-02, the agreed plan's layer 2: "result
classification after each attempt (done / nothing to do / wrong scope / no result /
needs pro)"). The state is the card's brief, the child's RESULT.md (cut to 16 KB;
`(none: the child wrote no RESULT.md)` when there is none) and the member's reason
line for the take's end, each under its own heading. One question, `class`, a choice;
each option's criterion, word for word (`TestAttemptAndGradeSchemasAreThePinnedOnes`
holds this table to the code):

| option | criterion |
| --- | --- |
| `done` | the take did the card's task: the work is committed and the gate the card names ran green |
| `nothing-to-do` | the card's task was already done at its base, or asks for nothing that can change: there was nothing to do |
| `wrong-scope` | the card cannot be done as written: the change it asks for lies outside its PATHS, or the card asks for the wrong thing; the fix is to the card, not another try |
| `no-result` | the child ended without leaving a result to judge (no RESULT.md, or a budget or deadline reached before any finding): another try may converge |
| `needs-pro` | the work was attempted and came back wrong in a way a stronger model would get right: tests red, the change incomplete or broken |
| `provider-failure` | the provider or its harness failed the run (an HTTP error, a rate limit, a balance, a crash), not the work: the same card on a working route would run |

An attempt decision's op id is `<card>@<attempt>.<12 hex of its state>` (`AttemptOp`):
a finish reported again replays its decision, and two takes of one attempt are two
decisions. The sprint asks it through the library, not the binary
(docs/SPEC-SPRINT.md section 2, the attempt decision): the member asks
`AttemptDecision` through the backend it is handed and the finish carries the
decision, one JSON record line, which the sprint's server holds to the schema
(`ParseAttempt`: the name, the schema's hash, the answers and the op id over the
state) and records. Its outcome labels are the card's fate after the decided attempt
(`AttemptLabel`): `landed` (it landed at that attempt), `later-<tier>` (it landed at a
later attempt, on that tier: `later-flash`, `later-pro`, `later-script`) or `dropped`.

The calibration of 2026-10-03, 100 ended takes of the store of 2026-10-02 (70 failed,
30 ok; their RESULT.md rebuilt from the reports the store keeps; 16 `landed`, 26
`later-flash`, 4 `later-pro`, 54 `dropped`), schema `3156747c139f228f`:

| question | positive | negative | AUC | at 0.7 |
| --- | --- | --- | --- | --- |
| `class=done` | landed | later-flash, later-pro, dropped | 0.937 | 11 of 16 caught, 7 of 84 bounced |
| `class=no-result` | later-flash, later-pro | landed | 0.883 | 14 of 30 caught, 0 of 16 bounced |
| `class=nothing-to-do` | dropped | landed, later-flash, later-pro | 0.618 | 14 of 54 caught, 2 of 46 bounced |
| `class=needs-pro` | later-pro | landed, later-flash | 0.494 | 1 of 4 caught, 3 of 42 bounced |

Against the reason line's prefix, at 0.7: 23 of the 24 takes a budget or a deadline
ended with no RESULT.md are `no-result`, 16 of the 20 `nothing to do` takes are
`nothing-to-do`, 18 of the 30 ok takes are `done`, and 11 of the 20 `push refused`
takes are `needs-pro`. `needs-pro` has four positives: no bar is read from it yet.

## 10. The grade decision

`grade --brief <file>` is a card's convergence before its first deal (the agreed plan's
layer 2: "convergence grade and route choice before the deal"; the owner's grade is
"confidence of convergence, work we are confident is going to converge"). The state
is the brief alone. One question, `grade`, a choice:

| option | criterion |
| --- | --- |
| `script` | the card's answer is known exactly: a mechanical replacement a program (a regex, a short Go or Lisp program) makes with no model |
| `flash` | a fast, cheap model converges: a bounded edit with the lines or files named, little reasoning, and a gate that says when it is done |
| `pro` | a strong model is needed to converge: reasoning across files, judgement the card cannot spell out, or work a cheap model gets wrong |

A grade's op id is `<card>@grade.<12 hex of its state>` (`GradeOp`): a brief graded
before is answered from the record, and a card id that comes back with another brief
is graded again. The sprint's server grades every card before its first deal
(docs/SPEC-SPRINT.md section 5, the grade); its outcome label is the tier that landed
the card (`GradeLabel`: `script`, `flash` or `pro`) or `dropped`.

The calibration of 2026-10-03, schema `ad287c9232ad5008`, `grade=pro` with positive
`pro` and negative `flash`:

| set | pro | flash | AUC | at 0.7 |
| --- | --- | --- | --- | --- |
| the 234 reviewed cards of 2026-10-02 | 2 | 232 | 0.547 | 0 of 2 caught, 0 of 232 bounced |
| every card of the store that landed | 34 | 298 | 0.930 | 16 of 34 caught, 0 of 298 bounced |
| the same, line 1's `tier:` word cut | 34 | 298 | 0.824 | 16 of 34 caught, 10 of 298 bounced |

The reviewed set is the mechanical flash sprint, two cards of it landed on pro: it
holds no measure of `pro`. Over the store's landed cards the brief's own `tier:`
word carries part of the separation (the third row); the sprint asks over the brief
as it is, and a grade raises no card above its ceiling.
