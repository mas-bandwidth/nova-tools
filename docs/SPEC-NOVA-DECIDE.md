# nova-decide: a decision system trained from its own record

`nova-decide` makes typed decisions with probabilities and keeps every one, so
how far each kind of decision can be trusted is read from what it got right.
A backend is the transport that answers; the concept is the decision system
above it: decisions, backends, and the record.

## 1. The two sides over one record

| side | verbs | what it does |
| --- | --- | --- |
| decide | `ask`, `read`, `gate` | asks a schema over a state through a backend; prints every answer with its probabilities; appends the decision to the record |
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

## 9. The gate decision

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
failure is, else `flaky` when one is (the flaky ones are rerun), else `pre-existing`.

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
