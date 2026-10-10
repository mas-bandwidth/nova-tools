# nova-decide: a decision system trained from its own record

`nova-decide` makes typed decisions with probabilities and keeps every one, so
how far each kind of decision can be trusted is read from what it got right.
A backend is the transport that answers; the concept is the decision system
above it: decisions, backends, and the record.

## 1. The two sides over one record

| side | verbs | what it does |
| --- | --- | --- |
| decide | `ask`, `read`, `score`, `attempt`, `grade`, `gate`, `brief`, `hold` | asks a schema over a state through a backend; prints every answer with its probabilities; appends the decision to the record |
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
probability or a confidence kept apart from one, a choice's probabilities only
for its options, a noul's probability of yes and nothing else, every
probability in [0, 1], and nothing that was not asked. An answer that does not
fit is a failure, never repaired.

## 3. Backends

A backend is `Name()` and `Ask(ctx, schema, state) (answers, usage, error)`.

- `jev`: TypeSafe's System One model, `jev-latest`. One POST of
  `{"state", "model", "questions"}` to `https://api.typesafe.ai/v1/systemone`
  with the key as a bearer token; the response is
  `{"answers": {<name>: {"type": "choice", "choice", "probabilities", "confidence"} | {"type": "noul", "noul"}},
  "usage": {"input_tokens", "output_tokens"}}`. A choice with probabilities is
  unchanged. A choice with only a confidence records the choice, that
  confidence, and method wire, and leaves the probabilities empty; neither is
  promoted into a probability of correctness. An answer with no
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
each outcome is one line, `{"outcome": {id, label, note, at}}`; a caller that acts
on its decisions (nova-sprint answer, section 13) writes a line per step of applying
one, `{"act": {id, act, op, at}}` (`applying` with the op id its verbs carry, before
them; `applied` or `refused` after). `inputs` names
each input file and its SHA-256; `state` is the exact text the backend was
asked over, kept because it is the training input. Lines are only appended,
by a writer holding the record file's own exclusive lock (go-internal/lockedfile,
an adopted module); a reader takes the shared lock, so it never meets half a
line. Loading folds each outcome into its decision, and each act, in order, into
its decision's `acts`.

- An id is the caller's `--op`, or `<decision>-<12 hex>` of the schema, the
  stamp and the state. The same `--op` over the same decision, schema (by hash)
  and state returns the recorded decision and asks nothing (`recorded=existing`),
  so a long run resumes where it stopped; over another decision, schema or state
  it is refused.
- An outcome is attached once. The same label again changes nothing
  (`changed=false`) and writes no line; another label is exit 1, naming both.
- Loading refuses, by file and line, a line that does not parse, a line holding
  more than one of decision, outcome and act, a decision id recorded twice, an
  outcome or an act for an id with no decision before it, and a second outcome
  line for one id (a hand-edited record never has its last line win).
- Loading refuses, by file, line and question, an answer whose probability is
  below 0, above 1, or not a number. A hand-edited record does not calibrate
  until that line is fixed.
- Replaying a recorded decision (`Make`, `MakeAll`, `Gate`) holds its answers to
  the schema like a fresh ask (`Schema.Check`); answers that do not fit the schema
  are refused naming the op id and the failing rule, and do not route.

The record's states are few and plain (a decision recorded, then labelled once; its
acts appended) and the one writer is the lock's holder. Its model is owed: `tla/DecideRecord.tla`,
with actions Ask, Replay, Attach and Act and the invariants above (one decision per
id, at most one outcome per decision, an outcome only after its decision), checked
by TLC on a bench and recorded in the TLC records; it lands with the export verb,
the first reader of the record beside calibrate.

### backfill-2026-10-04.w1: importing finished decisions as labelled records

`nova-decide import --record <file>` loads decisions already made as labelled records, so they can be read, calibrated and trained on. It never asks a backend and duplicates none of the verbs: a record it writes is a decision (named `import-<kind>`, backend `import`, empty answers, the source text as the state) with its label attached as the outcome, the same lines `outcome` writes. The sources:

- `--verdicts <glob>`: heavy-read `VERDICT.md` files; the first word of the first line (ACCEPT, REWORK, DROP-OR-RECUT) is the label, kind `verdict`.
- `--judgments <dir>` with `--log <file>`: the judgment files (`<judgment id>.md`), each labelled by the verb of the last line of a `nova-sprint log --json` export whose `answers` names the judgment, kind `judgment`. A judgment nothing answers is counted as `unanswered` and not recorded.
- `--reports <glob>`: `REPORT.md` files whose first line is `Verdict: HOLD`, label `HOLD`, kind `report`.

An item's id is its kind and the hash of its absolute source path and content, so a second import adds nothing and an edited source is a new item. The import is one write under the record's lock. It prints `IMPORT OK` with `<kind>_new` and `<kind>_existing` per kind, and `unanswered`. The model is `internal/decide/import.go`; the pin is `TestImportIsIdempotentAndCountsPerKind`.

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

The read line carries the verdict and its probability, or `p=-` when the
choice has none; a gate decides on
`defect` or `verdict` at the bar `calibrate` reads from the read's record.

## 7. Output, refusals and exits

Every verb prints one result through internal/tool: the typed line
(`READ OK id=... verdict=... p=...`, `ATTEMPT OK ... class=... p=...`,
`GRADE OK ... grade=... p=...`, one `<VERB> ANSWER` item per question) or,
with `--json`, the same value as one JSON object. A choice with an empty
probability map prints `p=-`, and a wire confidence adds `confidence=<x>
method=wire` on the headline and the answer row; neither is a probability.
A refusal is one line,
`<VERB> REFUSED: <every problem, each with what it wants>; run: <remedy>`, at
exit 2, and writes nothing. A backend that fails (no answer, an HTTP status, an
answer outside the schema) is `<VERB> FAIL id=... backend=...: <why>; run:
<remedy>` at exit 2, and records nothing. Exit 1 is an outcome that conflicts
with the one recorded. `ask`, `read`, `attempt`, `grade`, `gate`, `brief` and
`outcome` take `--dry-run`: the plan, with no backend call and no write; an op
the record holds already is reported as `recorded=existing` (for `gate`, per
failure).

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

## 10. The attempt decision

`attempt --brief <file> [--result <file>] --reason <line>` is how a work take ended,
asked after it ends (the agreed plan of 2026-10-02, layer 2: result classification
after each attempt, done, nothing to do, wrong scope, no result or needs pro; the owner
agreed to the plan: "OK let's do this too", 2026-10-02 ~9:20 PM ET). The state is the card's brief, the child's RESULT.md (cut to 16 KB;
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
it is per attempt and state, not per take. A finish reported again replays its
decision; two takes of one attempt are two decisions when their states differ, and
two takes that ended with identical states (the same reason line and RESULT.md) share
one decision, recorded once (the sprint's `decided:<op>` is set once). The sprint's
server routes a finish only by a decision whose op names that finish's own card and
attempt (`AttemptOf`), and refuses any other. The sprint asks it through the library, not the binary
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

Two classes may route a failed finish, each from its own bar on the sprint row:
`no-result` from `decide_attempt_no_result` (an ended take, redealt) and
`nothing-to-do` from `decide_attempt_nothing_to_do` (failed work of that class). No
other class has a bar in this layer: `needs-pro`, `wrong-scope`, `provider-failure` and
`done` are recorded and shown only, and a report native ended as a provider failure is
never overridden by any decision (docs/SPEC-SPRINT.md section 2, the attempt decision).
Both bars are empty by default, so no decision routes a finish until a review round
labels cards independently; 0.7 is the starting point the numbers above support for
`no-result`, and `nothing-to-do`'s AUC (0.618) is near chance until that round. (Measured
under the first rule, one bar on which `no-result` and `provider-failure` both ended the
take: at 0.7, 26 of the 70 failed takes would have gone from failed work to an ended
take, redealt with no judgment; 14 of them landed later, 12 were dropped. The split per
class was not measured.)

## 11. The grade decision

`grade --brief <file>` is a card's convergence before its first deal (the agreed plan
of 2026-10-02, layer 2: a convergence grade and route choice before the deal; and the
owner the same evening, of what the grade measures: "work we are confident is going to
converge"). The state
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
as it is, and a grade raises no card above its ceiling. The sprint row's `decide_grade`
is empty by default, the grade a hint; 0.7 is the starting point.

### 11.1 Few-shot calibration

`grade --examples <jsonl> [--held-out <ids>] [--seed <s>]` puts ten landed cards per class
ahead of the card under grade (`GradeStateWith`, `PickExamples`, `internal/decide/gradefewshot.go`).
Each example is one line: the brief's heading, its `PATHS`, its `KIND` and the label of its
outcome, never the brief's text. The labels are `flash` (landed on flash within 2 attempts),
`pro` (needed pro) and `heavy` (needed heavy). The library reads no sprint store: the caller
builds the examples file from the record, one JSON object per line
(`{"card","heading","paths","kind","label"}`), and `ParseExamples` names every bad line.

The pick is deterministic: cards in `--held-out` leave the pool, a card's rank is the SHA-256
of `--seed` and its id, and the ten lowest ranks of each class are taken, in class order
(flash, pro, heavy) then rank; the pool's order moves nothing. A class with fewer than ten
cards outside the held-out set is refused, naming it. `TestTheGradePromptCarriesTenExamplesPerClassFromTheRecord`
fakes the backend and pins exactly ten per class, the order, and no held-out card.

Score on a held-out set, before and after, with the jev-score program
(the coordinator's scratch program, `go run . --grades grade.jsonl --cards <dir>`):

| grades | held-out cards | result |
| --- | --- | --- |
| before (no examples) | not measured | not measured by this change |
| after (`--examples`) | not measured | not measured: needs the Jev key and a held-out re-grade |

Neither number is measured here: a bud holds no Jev key and the jev-score input is the
shadow grade record, which has to be re-run with `--examples` over the held-out cards.
The all-cards figure of 2026-10-04 (first-attempt tier flash, Jev grade flash: 111 cards,
87% landed within 2 attempts; Jev grade pro: 161 cards, 58%) is the baseline to compare with.

## 12. The gate decision

`gate --output <file> --card <file> [--diff <file>] [--base-red <test,...>]` reads a red
gate: go test's output (plain or `-v`), the card that asked for the work, and its diff.
`ParseGateOutput` reads each failing test: a top-level test with a `--- FAIL:` line (a
subtest's failure is its test's, the subtest's line among its lines), its package from
the `FAIL <pkg>` line after it, and its first ten lines (the indented lines after it,
else, under `-v`, what it printed after its `=== RUN`), each cut to 300 bytes; a package
that did not build, or that failed with no test named (a timeout names its test under
`running tests:`), is one failure with no test. Output with no failure is refused.

Each failing test is one decision, `<op>/<pkg>.<Test>` (`GateOp`), asked over its own
state (`GateState`): the failure and its lines, whether the same test is red at the
card's base (`--base-red` names those, `<Test>` or `<pkg>.<Test>`; without it the base
is `not run`), the gate's other failures by name, the card's PATHS, and a summary of the
diff (`DiffSummary`: each file with the lines it adds and removes, a rename as
`old -> new`), each under its own heading, and nothing else. One question, a choice
with a probability per class:

| option | criterion |
| --- | --- |
| `flaky` | it fails by chance and not by the change: a timing bound, a race, a port or file in use, the machine's load; the same test at the same commit passes when run again |
| `caused` | the card's change broke it: the test asserts what the diff altered, or reads a file the diff changed |
| `pre-existing` | it fails without the card's change: it is red at the base, or fails on this machine (an operation denied, a tool or service absent) whatever the diff |

(`TestGateSchemaIsValid` holds this table to the code, word for word.) A build failure,
and a failure past the eighth (`MaxGateFailures`), is `caused` and not asked.

Two bars (`GateBars`, the sprint row's `decide_gate_flaky` and `decide_gate_preexisting`;
`gate --bars <flaky>,<pre-existing>`) route each failure: p(flaky) at or above the flaky
bar is `flaky`, rerun once; p(pre-existing) at or above the pre-existing bar is
`pre-existing`; every other is `caused`. `ParseGateBars` reads each as a probability or
empty; an empty bar is `Unset`, reached by no probability, so its route is never taken;
two set bars sum above 1, so no failure meets both. **Both are empty by default** (the
sprint row's, and `--bars`'): every gate decision is recorded and shown, and nothing is
rerun or reclassified until the owner sets a bar. 0.8 each is the starting point the
calibration below reads; it is not routed yet because at 0.8, with the base run, 24 of the
39 flaky failures would have been reported pre-existing. The gate's route (`Gate`) is `caused` when one
failure is, else `flaky` when one is (the flaky ones are rerun), else `pre-existing`; a
gate with no failure decides nothing and has no route (never `pre-existing`).

The rerun's result is each flaky decision's outcome (`SettleGate`): green is `flaky`;
red again is `pre-existing` when the test is red at the base, `caused` when it is green
there, and `red-again` when the base was not run. The gate after the rerun is `caused`
when a rerun failure is red again, else `pre-existing` when a failure was routed so, else
`green`. A review that learns a failure's class later attaches it with `outcome`, in the
same three words, and `calibrate --decision gate --question class=<class>` reads the bars
the record supports.

The calibration of 2026-10-03: 60 failing tests of the coordinator bench's CI runs of
this repository (real go test output; account and machine names replaced by generic ones
before they were asked), each labelled by git and the other runs: flaky (39) when the same
code of its package passed in another run, pre-existing (8) when it was red at the nearest
ancestor run, caused (13) when it was green there and the change touches its package.
With the base run (the ancestor run's result, as the member runs the base), p(caused) AUC
0.914, p(pre-existing) 0.907, p(flaky) 0.716; at the starting bars, 0.8 each, every caused
failure stays caused and every pre-existing one is reported so, and of the 39 flaky ones 4
would be rerun, 24 reported pre-existing and 11 left caused. Those 24 are failures of the
machine's environment (a store absent for that run), not the card's under either label,
but a label the record cannot yet tell from pre-existing is the reason the bars ship
empty. With the base not run,
p(caused) 0.8, p(pre-existing) 0.522 (no separation), p(flaky) 0.72. Both records are
`internal/decide/testdata/gate-calibration-base-run.jsonl` and `...-base-not-run.jsonl`,
pinned by `TestTheGateCalibrationRecordsSupportTheBars`.

The sprint asks it through the library (docs/SPEC-SPRINT.md section 5, the gate verdict,
and section 7, the lander's gate): a worker's native, before the member reports a take
whose child ended not-done on a red gate, with the base run in the child's own wall;
the lander on a red batch check, with the base not run. The member's base run is one run of
the failing tests, in the child's wall, bounded (`gateRunWait`, three minutes, and the
rerun the same), so a red gate's report is delayed by no more than those and the asks.

## 13. The judgment decision

`nova-sprint answer` (docs/SPEC-SPRINT.md section 8, answered by
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
coordinator with why. The row ships the bar empty: with none, answer applies
nothing, records every decision and lists what a bar would apply, until the coordinator
sets one. 0.8 is a starting point measured on 100 of the coordinator's own judgments, below, not an
independent calibration: at it the decision applies 40 of the 100 and 39 of those are the
coordinator's own verb. An applied `ack` gives the reason `nova-decide (p=<p>):
the card can run without the dropped need; a conflict is handled at merge`.

A card whose judgment text or last ten log lines carry a provider's refusal for want of
payment (HTTP 402, out of credit, insufficient funds or quota: `PaymentRefusal`) is not
asked at all: it is listed, "a payment is the owner's".

Each decision is recorded under its judgment's id: the note's id (`<note id>:<card>` for
a note of several cards), with `inputs` naming the judgment, the note, the kind, the
card, the verb chosen and what was done (`act`: listed, or applying with the `op` its
verbs carry), and act lines after it: `applying` (with the op) before the verbs of a
decision recorded earlier run, `applied` or `refused` after any decision's verbs ran.
The decision's last act says where it stands. A card of a judgment is decided once:
the same id again is answered from the record and asks nothing, and one applied
before whose judgment is still open is listed, never applied twice; one left
`applying` (a pass stopped between its lines) is finished through the same op, the
bar and the hour guard skipped (a drop, a release, or a verb not printed is refused, never
applied); a
card the decision reworked, or began to, within the last hour is listed, not
reworked again. One ask may take `--timeout` (60s by default, `JevTimeout`); an ask
past it is that card's `failed` row and records nothing. Its outcome is attached once the card's state says it (`JudgmentOutcome`):
`landed`, `dropped` (off the table), or `came-back` (another judgment open on it).

The in-sample fixture (`internal/decide/testdata/judgment-insample.jsonl`) is 100
judgments the coordinator answered on 2026-10-02 and 03, read from the sprint's log,
each with the coordinator's verb and the card's outcome; the record of Jev's answers to
them is `internal/decide/testdata/judgment-record.jsonl`, and
`TestJudgmentInSampleAgreement` recomputes the agreement it states. Each state is
built by `JudgmentState` from the card's log up to the second the coordinator answered,
as answer builds it live (one card a judgment), with the fleet's machine,
person and home-directory names replaced by placeholders (m1, coordinator, owner,
friend-a, and `<home>`, written `/Users/user` or `/home/user` as the machine spelled
it; an Ubuntu box's `/home/ubuntu` names no one and is kept) before Jev was asked, so the record holds the very state each
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
blocked and cannot-ask judgments all stay under the bar. The measure is in-sample: 0.8
was chosen on these same 100 judgments, against the coordinator's own verbs (the night's
runaway reworks among them), so it is a starting point and not an independent
calibration; the bar ships empty until independent labels calibrate it.

### decision-record.w1: the coordinator's own answers

Every judgment answered with a verb that takes `--answers` (accept, rework, return, drop,
ask, and an accept over a reader's verdict), or with `ack` or `wait`, appends one record of
decision kind `judgment-answer` to `<decide dir>/judgment-answer.jsonl`, the file the
server's `run --decide` dir holds, by the verb that gave it, one record per judgment and
card (a group answered at once is one record each). The record is the existing record
format (section 4): `state` is the judgment's state as the judgment decision is asked it
(`JudgmentState`), the answer `verb` is the verb given with probability 1, the backend is
`coordinator:<actor>`, and the inputs are `card`, `note`, `kind` (the judgment's type),
`verb`, `reason` (the `--reason` text, or a wait's time), `fix` (the `--fix` text), `actor`,
`evidence` (the paths the judgment text names, one a line) and `broken_reads` and `failed`,
the card's counters when it was answered. The id is `<card>@answer.<note>.<12 hex of the
state, verb, reason and fix>`, so the same answer given again is the same record.

The outcome is attached by the server's decide lane, once, as `nova-decide outcome` does:
`landed` when the card lands, `dropped` when it leaves the table, `bounced` when more reads
have found it broken or more finishes have failed than its counters at the answer (the next
read broken, the next finish failed). A card still standing with nothing new has none yet,
and the lane watches its card, placed or not, until it does. A record write that fails
never fails the verb: it is a NOTE line. Nothing is recorded when the verb moved nothing,
for a card the step refused, or when no decide lane keeps a record (a verb run with no
server). The record is the label set `nova-decide` evaluates, shadows and trains the
judgment decision on; it duplicates no verb of `nova-decide`.

### jev-shadow-judgments.w1: Jev answers every judgment in shadow

The server's decide lane (`run --decide <dir>`) asks the judgment decision (the one `answer
--decide` uses, `JudgmentSchema`) of every open routine judgment as it is raised, through the
lane's backend, and appends the answer to `<dir>/judgment-shadow.jsonl`. It is the existing
record format (section 4) under decision name `judgment-shadow`: `state` is the judgment's state
as the judgment decision is asked it, every answer carries `method: "shadow"` beside its
probabilities, and the inputs are `card`, `note`, `kind` (the judgment's type), `verb` (the
shadow verb) and `shadow` (`true`). The id is `<card>@shadow.<note>.<12 hex of the state>`, so a
judgment is shadowed once. A shadow record has no act: nothing is applied, written on the work
table or listed for the coordinator, and a judgment that names a provider's refusal for want of
payment is not asked, as the real decision does not ask it. A failed ask is a DECIDE FAILED line
and is asked again on the next round; with no backend nothing is asked. The state carries no log
lines, since the lane holds the work table and not the card's log.

When the real answer arrives (the `judgment-answer` record above) the pair is joined by `note`
and `card`. `nova-decide findings --record <file> --shadow <judgment-shadow.jsonl> --real
<judgment-answer.jsonl>` prints, besides its findings, one `shadow` item per judgment kind
(count of joined pairs, agreement percent, how many of the pairs had the shadow verb at p 0.95
and above and the agreement percent among those) and `shadow_pending`, the shadow records with
no real answer yet. Verbs are compared as recorded: the shadow verb is one of the judgment
decision's, the real one any of the answer verbs, so a `recut` or `hold` never agrees. It
duplicates no verb of `nova-decide`.

### jev-shadow-heavy-read.w1: Jev reads every card in shadow

The server's decide lane asks the read decision (`ReadSchema`, the first read's) of each card in review
at its head, over its brief and diff, through the lane's backend, and appends the answer to
`<dir>/read-shadow.jsonl`: decision name `read-shadow`, every answer marked `method: "shadow"`, inputs
`card`, `verdict`, `shadow` (`true`), `broken_reads` (the card's count when it was read) and the brief's
and diff's hashes. The id is `<card>@shadow-read.<12 hex of the state>`, so a card at a diff is read once.
A shadow read is never a read: it is not in the read record, has no act, writes nothing on the work
table and no reader sees it. The lane takes the diff from a source it is given; with none, or with no
backend, nothing is asked. A failed ask is a DECIDE FAILED line and is asked again on the next round.

The readers' outcome is attached to the shadow read once, by the lane: `BOUNCE` when more reads have
found the card broken than its `broken_reads`, `LAND` when the card lands, `dropped` when it leaves the
table. The gold of the heavy reads is the import of heavy-read verdicts (section 4, `import --verdicts`):
the card is the second word of a verdict's first line and its label is ACCEPT (not broken) or any other
(broken). `nova-decide findings --record <file> --read-shadow <read-shadow.jsonl> [--heavy <import record>]`
prints one `shadow_read` item per gold, `readers` and, with `--heavy`, `heavy`: cards joined, `tp`, `fp`,
`fn`, `tn` and the percent precision and recall of the shadow read's broken answer (verdict BOUNCE; UNSURE
is not broken). A shadow read with no gold yet, or whose card was dropped, is not counted. The pin is
`TestShadowReadsAreScoredAgainstHeavyVerdictsAndReaders`.

## 14. The brief decision: card quality before add

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
--brief-dir` reads them (`CardFilePaths`), each card's id its file's name without `.md`.

**A batch.** Every card of one call is one batch (`MakeAll`): the record is read
once, a card it holds is answered from it, the rest are asked through the backend
at most `BriefWidth` (8, add's width and `--width`'s default) at a time, and every
new decision is appended in one write under the record's lock. Once the batch's
context is done no further card is asked; each card unanswered is its own error,
nothing is recorded for it, and the rest are. With no record a batch keeps
nothing. A batch has one deadline, `BriefDeadline` (a minute), in `nova-sprint add`
and in `nova-decide brief` alike (`--timeout` bounds each card under it). `nova-decide
brief` fails at exit 2 when a card went unanswered, so a script runs it again (a
recorded card costs nothing).

**Where it is asked.** `nova-sprint add`, under `JEV_API_KEY`, asks it of every
card it names with a brief (a card per brief file, or the one brief of each id;
a `--count` add names no id before the store numbers it, and a sentinel carries
no brief) after every check of its own (the arguments, `--score` among them, the
files, the size, shared `PATHS:`, the card lint), in both forms, so a card add
refuses costs no call, and before it writes. The whole batch has one
deadline, a minute: past it each unanswered card is a `NOTE brief:` line and the
add goes on. With a server named, add runs its checks and asks where it is typed
(the caller's key and files; the server's one line of control holds no backend
call) and sends the server the add with each card's op (`--brief-op <id>=<op>`)
and the record; `--brief-op` is that wire word only: typed on an add no server
runs it is refused, and the server takes an op only as `<id>@brief-...`, the
card's own, so no op can point a card's end at another card's decision; in that half the card lint runs only under `--rules`, since the
sprint's recorded rules file is the server's to read, so a brief the server's
lint refuses may have cost a call. One `BRIEF card=<id> op=<op> <reading>
recorded=<new|existing>` line per card; under `--json` the add's one JSON object
holds these lines and its `NOTE brief:` lines in its `brief` field, served or not. The record is
the coordinator's, `<root>/decide/brief.jsonl` with the root `~/nova-sprint`
(never a cache directory), or the file `add --decide-record` names. A card stores
its op and its record (`brief_op`, `brief_record`); a brief replaced by `nova-sprint
brief` drops both. With `decide_brief_bar` set, a card under it refuses the whole
add, exit 2, nothing written, naming each such card, its p and the questions it
failed, and saying the decision is uncalibrated. No key asks nothing and says
nothing: the key is the opt-in, and a keyless add is the add it was before this
section. A decision that cannot be made (the record unnamed, as when the home
directory cannot be found, or unmade; the bar unreadable; the backend failing a
card) is one `NOTE brief:` line and the add goes on. `nova-swarm lint --card <file> --decide` prints
the same reading on one `LINT DECIDE` line after the lint's own and never changes
its verdict (`--decide-answers` answers from a file, `--decide-record` records);
when the backend fails, the lint's verdict is printed all the same, then why the
decision was not made, exit 2.

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

## 15. hold-reason-classifier-bb.w4: the hold decision

`hold --report <file>` classifies a HOLD report as one of five classes with
probabilities, and extracts the proposed paths. The state is the report text alone.
Each PROPOSED line names one path the report proposes.

Two questions:
- `class` (choice): paths-too-narrow, missing-dependency, already-done, work-defect, or harness-failure, each with its p
- `proposed_paths` (noul): p that the report contains a PATHS-PROPOSED line

`ExtractProposedPaths` reads the PATHS-PROPOSED line if present; else returns the
path-like tokens on a line saying what the work needs or changes.

The five classes and their criteria:

| class | criterion |
| --- | --- |
| `paths-too-narrow` | the report says the paths are too narrow |
| `missing-dependency` | the report says a dependency is missing |
| `already-done` | the report says the work is already done |
| `work-defect` | the report says there is a defect in the work |
| `harness-failure` | the report says the harness failed |

The hold decision is shadow-only: it does not act on the report, only classifies it.
The record holds the classification for later calibration. The reports of the
labelled set are the fixtures under `internal/decide/testdata/holdreason/`, and
`labels.json` beside them is a person's class and proposed paths for each.
