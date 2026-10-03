# nova-decide: a decision system trained from its own record

`nova-decide` makes typed decisions with probabilities and keeps every one, so
how far each kind of decision can be trusted is read from what it got right.
A backend is the transport that answers; the concept is the decision system
above it: decisions, backends, and the record.

## 1. The two sides over one record

| side | verbs | what it does |
| --- | --- | --- |
| decide | `ask`, `read`, `score` | asks a schema over a state through a backend; prints every answer with its probabilities; appends the decision to the record |
| train | `outcome`, `calibrate`, `findings` | attaches what turned out true to a recorded decision; reads the bar a decision's answer supports from the decisions whose outcome is known; clusters the classes the score decisions find |

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

A label of words joined by `+` (a score's classes, `stranded_fragment+invented_reason`)
is positive, or negative, when one of its words is listed: one outcome per
decision labels every class a review found in it.

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

## 9. The score of a landed diff, and the findings

Rule 7 of the sprint's cost rules: sample the landed work, and every ugly
finding becomes a finder rule or a class test. The score decision is that
sample, made of every landed head: `score --card <file> --diff <file>` asks the
read's five questions (section 6) and one noul per escalation class the cold
reviews of landed work found (the two reviews of 2026-10-02, E1 to E16), over
the same state as the read (the card, then the diff). Each class's statement is
the probability that the diff carries that class of defect:

| class | escalations | yes is |
| --- | --- | --- |
| `stranded_fragment` | E4 | a changed comment or paragraph, read whole, is broken English: a fragment, a line opening with a comma, a deleted sentence's tail, an unmatched backquote, a comment made a preformatted block |
| `cut_citation` | E1, E14 | a live cross-reference deleted: a rule number of a list the document still numbers, a table cell naming a rule, a test name, an identifier, a docs/ or tla/ path, a model citation; or a mechanism's comment collapsed into a summary |
| `renamed_file_assumed` | E3 | a renamed file's bare name rewritten where it named another file of that name in another directory |
| `outside_paths` | E12 | a file changed outside the card's PATHS; not asked: 1 - p(`inside_paths`) |
| `ledger_ceiling` | E6, E15 | a ledger left with a ceiling that is not its row count, or a blank line |
| `comment_contradicts_code` | E7 | a changed comment states what the code beside it contradicts |
| `test_weakened` | E2 | a rewritten assertion that no longer fails exactly when the old one failed |
| `record_made_claim` | E5 | a measured record (a run's number, a dated incident, its provenance) made a present-tense claim, or a replaced behaviour described as the present one |
| `invented_reason` | E10 | a reason put in place of the removed one that nothing supports |
| `fenced_block_edit` | E8 | a line changed inside a fenced block, a transcript or an example of output |
| `asserted_data_cut` | E11, E13 | text a test, a format or a tool's output asserts cut or made a placeholder: a heading, a dated fixture name, a format field, a printed field |
| `load_bearing_word_cut` | E9 | a word that carried the meaning dropped or swapped ("the old", "had to be") |

E16 (one card landed twice) is a property of a batch branch, not of a diff, and
is no class. The line names the top class, the class with the highest p (the
first in table order on a tie), and that p: `SCORE OK id=... top=<class> p=<p>`.
`TestScoreSchemaAsksTheReadAndEveryClass` holds this table to the code.

`nova-sprint land` asks it of every head of a batch it pushed and reported
(docs/SPEC-SPRINT.md section 7, the landed score), as `<card>@landed@<head>`,
over the card's brief and the merge's own diff, recorded in
`decide/score.jsonl` under the land root, and reports each card's top class and
p to the store; a batch whose cards' top class meets the sprint row's
`decide_score_bar` raises one "landed work scored low" judgment listing them. The
bar is empty by default (the scores are recorded and clustered, no judgment is
raised); 0.7 is the starting point once a review round labels cards
independently, the calibration below being in sample.
A review's finding is attached with `outcome`, the label the classes it found
joined by `+` (or `clean`), so each class is calibrated on its own:
`calibrate --decision score --question <class> --positive <class> --negative clean`
(and `outside_paths` as `--question inside_paths --positive clean --negative outside_paths`).
The record is the training data, kept as the read's record is: about the state
plus 1 KB a landed card.

`findings --record <file> [--since <time>] [--bar <p>]` clusters the score
decisions made since `--since` (default: seven days before now) by every class
each gives a p at or above `--bar` (default 0.5): one `FINDING class= count=
cards=` line per class, most cards first, the cards by id. `unnamed` counts the
decisions whose p(defect) meets the bar while no class does: a defect no class
names yet. A class that keeps coming back is the material for a finder rule or
a class test; an unnamed cluster, for a new class.

The calibration of 2026-10-03: the 234 reviewed cards of the two reviews, each
landed diff scored by Jev and labelled per class from the reports (157 clean,
28 ugly with no class named and so in no class's count). Per class, the AUC of
its p against the clean cards (positives in brackets): `cut_citation` 0.945
(18), `stranded_fragment` 0.864 (15), `record_made_claim` 0.967 (10),
`invented_reason` 0.933 (9), `fenced_block_edit` 0.970 (9),
`asserted_data_cut` 0.871 (4), `load_bearing_word_cut` 0.887 (3),
`ledger_ceiling` 0.742 (3), `comment_contradicts_code` 0.959 (2),
`test_weakened` 0.971 (1), `renamed_file_assumed` 0.994 (1), `outside_paths`
0.439 (1). Against the clean cards of the same streams only (docs and diary,
80), the AUCs are lower: `cut_citation` 0.903, `stranded_fragment` 0.739,
`record_made_claim` 0.936, `invented_reason` 0.875, and the top class, the
judgment's score, 0.716 (0.835 against all clean cards). The top class at the bar
0.5 flags 40 of the 49 class-labelled cards and 54 of the 157 clean ones (51 of
the 80 clean docs and diary cards); at 0.7, 26 and 14.
