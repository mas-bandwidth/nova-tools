# Proposal: nova-decide, decisions and learning

**Status: proposed acceptance contract; implementation comparison pinned to dev
`2ced4eeae1b5fe760074b32d4dcc3b66a39a755d`.** Layers 0–6 have implementation
on that revision. Implementation and adoption are separate: the checks below
remain requirements until their evidence is supplied. The five added layer PRs
are #5204 (layer 2), #5206 (layer 3), #5201 (layer 4), #5207 (layer 5) and #5203
(layer 6). This comparison is a source review, not an execution of those PRs or
an independent reproduction of their model evaluations.

The original layer-0 source review covers
[PR #5175](https://github.com/mas-bandwidth/nova-tools/pull/5175) at
`05c273e04a4cbf65257b09a6a1c3c9833469cbcc`. Its retrospective first-read
calibration covers 234 cards in a reported 97 seconds for about $0.05.
It demonstrates useful ranking, but three known wrong cards fall below the
proposed automatic-pass threshold. No first-read replacement threshold is
established. Export and backend training remain acceptance targets.

nova-decide is a decision system that learns from the results of its decisions.
It owns both sides: asking a named set of typed questions through a backend,
and recording the later outcomes that calibrate those questions and produce
training data. Jev is the first backend. The backend is replaceable; the
question definitions, evidence and outcome history belong to the tool.

The immediate use is a cheap first read of a sprint diff. The remaining layers
use the same record to classify attempts, choose work tiers, explain gate
failures by category, score landed work, answer routine judgments and improve
cards before they enter the sprint. Each layer earns adoption on its own
measured errors, coverage and cost. A successful first-read experiment does
not certify the other decisions.

## One record, one owner

The current tool keeps one append-only JSON-lines file selected by `--record`.
A decision line contains `id`, decision name, schema hash, backend name, time,
input file names and hashes, exact state, answers and usage. An outcome line
attaches `id`, `label`, optional note and time. Writes use the existing file-lock
library. Loading folds outcomes into decisions; calibration reads that record
without storing a second derived truth. The sprint's work and merge state stays
in nova-table. This proposal does not replace that foundation or add a second
sprint state store.

The asking side records successful validated answers. The learning side
attaches a label once and computes threshold counts and ranking AUC. Another
label for the same decision is refused; correction history, training export
and backend training are not implemented. A landed card is not thereby correct,
and a prediction never labels its own training example.

For reconstructable learning, the acceptance target adds the full versioned
question definition, resolved backend/model identity, policy identity, elapsed
time and failed or unknown attempts. Outcome corrections need explicit
provenance and a modelled transition before they become training labels. Input
identity must resolve the card attempt, task, PATHS and exact base/head rather
than depend on a moving branch name. Training exports retain the lineage that
keeps related cards out of opposing splits.

The caller must restrict packets to content authorized for the selected backend
and exclude credentials, unrelated logs and private message bodies. The current
tool does not enforce authorization or redact arbitrary packet content. The
acceptance target makes redaction or omitted context visible to the decision policy. Artifact access and
retention follow the source data policy; training export is a separate declared
use, not an implicit consequence of asking a question.

Provider requests precede the record lock. Concurrent callers can both send a
request for one operation, even though only one decision is retained. A crash
after sending can leave an unknown response, and a failed append can lose a paid
answer. The tool does not promise exactly-once provider billing. Current failure
paths record nothing; a complete operational record must retain those attempts.

The staged calibration reports 3,050,084 state bytes for 234 cards, measured by
its author's dry run: about 2.9 MiB, or about 291 MiB at 100 times that population,
before record metadata and outcomes. These reported sizes have not been
independently reproduced. One process remains a plausible fit. The present code
loads the full record on each call and computes AUC by comparing positive and
negative pairs. A 23,400-decision drive measures total bytes, memory, load time
and calibration time before adding any index, cache or paging layer. At balanced
labels that AUC implementation performs 136,890,000 pair comparisons; repeated
whole-record loads also grow with accumulated history. Those costs are explicit
acceptance questions, not a reason to invent a distributed store.

The PR names its record model as owed. Its initial transitions are record,
replay and attach. The model and tests must establish one retained decision per
ID and an outcome only after its decision. Crash and concurrent-read behavior
need their own evidence; the lock alone does not establish durability. Failure
records, outcome correction and training export extend that same model before
those transitions are adopted.

## Implementation map and adoption bars

The layer numbers below are stable. The implementation's numbered spec sections
are a different index: layer 4 maps to section 9, layer 2 to sections 10–11,
layer 3 to section 12, layer 5 to section 13 and layer 6 to section 14 of
[the implemented spec](https://github.com/mas-bandwidth/nova-tools/blob/2ced4eeae1b5fe760074b32d4dcc3b66a39a755d/docs/SPEC-NOVA-DECIDE.md)
at the pinned dev revision. The executable contract
there describes the implemented verbs; this proposal defines the additional
acceptance evidence for their adoption. A discrepancy is an open requirement,
not permission to weaken the bar to match the implementation.

| Layer | Implemented surface | Routing controls and acceptance status |
| --- | --- | --- |
| 0. Questions and record | `ask`, `outcome`, `calibrate`; shared [decide library](https://github.com/mas-bandwidth/nova-tools/tree/2ced4eeae1b5fe760074b32d4dcc3b66a39a755d/internal/decide) | Typed validation and replay exist; complete distributions, resolved model identity, failed-call records and record-model evidence remain required. |
| 1. First read | `read`, sprint `FirstRead`; PR #5188 | `decide_bounce` routes at or above its bar; `decide_review` routes below its bar to an okay read without a strings read. The interval between them uses strings. This implemented route has no accepted replacement threshold in this proposal. |
| 2. Grade and attempt | `grade`, `attempt`; PR #5204 | `decide_grade`, `decide_attempt_no_result` and `decide_attempt_nothing_to_do` ship empty. The reported 0.7 candidates do not authorize activation. Only the latter two attempt classes can change finish routing. |
| 3. Gate cause | `gate`, member and lander integration; PR #5206 | `decide_gate_flaky` and `decide_gate_preexisting` ship empty. Both must be probabilities and, when both are set, sum above 1. Reported 0.8 candidates remain unaccepted; a model answer cannot turn a red gate green. |
| 4. Score and findings | `score`, `findings`, post-land scoring; PR #5201 | `decide_score_bar` ships empty: scores are retained without raising its judgment. `findings --bar` defaults to 0.5 for clustering only. The reported 0.7 score candidate does not establish a finder rule or an independent calibration. |
| 5. Routine judgment | `nova-sprint answer`; PR #5207 | `decide_judgment_bar` ships empty; `--bar` can override it. No bar means no action, including an interrupted action's resume. The 0.8 result is in-sample agreement with coordinator verbs, not an accepted action policy. |
| 6. Card quality | `brief`, sprint add and lint integration; PR #5203 | `decide_brief_bar` ships empty, report only. The 0.5 need-question display cutoff is diagnostic, not a convergence bar. The implementation labels readings `uncalibrated=true`; no adoption bar is established. |

These are source defaults, not a reading of a running sprint's configuration.
A nonempty setting or an installed provider key does not supply the missing
adoption evidence. Each routing policy needs a versioned eligible population,
independent labels, a frozen threshold, error and coverage limits, and a rollback
condition. Diagnostic scoring can collect evidence without approving automated
routing. No setting is changed by this review.

The source comparison leaves these requirements open:

- **Layer 1:** `FirstRead` routes on `p(defect)` alone. It records but does not
  reconcile the verdict and `inside_paths` answers, and supplies no rule text.
  The acceptance check below still requires contradictory or incomplete evidence
  to escalate and exact path enforcement to remain mechanical. A merged route
  does not resolve the three demonstrated low-score wrong cases.
- **Layer 2:** the implementation adds `provider-failure` as a sixth attempt
  class. It never overrides a native provider-failure report with a model
  classification; `done`, `needs-pro`, `wrong-scope` and `provider-failure` are
  diagnostic only. Outcomes such as `landed`, `later-<tier>` and `dropped` describe
  subsequent fate; they do not independently establish that an attempt was
  correct, empty, or impossible. `nothing-to-do` needs verified satisfaction,
  and tier adoption needs matched convergence evidence rather than the tier
  that happened to land the card.
- **Layer 3:** the source distinguishes an unrun base from a passing base and
  retains `red-again` when a red rerun cannot distinguish causes. The reported
  base-run calibration routes 24 of 39 labelled flaky failures as pre-existing
  at 0.8. Keep the bars unset while adjudicating that confusion and validating
  actual rerun and gate behavior.
- **Layer 4:** findings are clusters of model scores, not independently verified
  defects. No generated finder rule is accepted without its separate clean set,
  mutation witness and measured false positives. Post-land scoring is detection
  after the event; it supplies no retrospective permission to skip a read.
- **Layer 5:** the source limits choices to offered verbs, reserves drop and
  release for the coordinator, lists payment failures without asking, prevents
  repeated application and records applying/applied/refused acts. Acceptance
  still needs stale-state and interrupted-action evidence for the complete
  consumer, including grouped judgments and the state at execution. The fixture
  has no ready-to-accept examples; results for other kinds cannot certify that
  kind. A card landing later does not prove a chosen judgment was sound.
- **Layer 6:** the implementation can refuse under an explicitly configured bar,
  but missing keys, unavailable configuration or unanswered requests can leave
  add proceeding with a note. Report-only fallback is permissible while
  collecting evidence; before adopting a semantic admission gate, its policy
  must explicitly define these unknown cases. A brief-only packet also cannot
  establish repository-wide ownership or dependency correctness by itself.

## The layers and their checks

### 0. Typed questions, backend and evidence record

`ask` is the generic entry and `read` the first named decision. `ask` takes
`--schema`, `--state`, `--backend` and `--record`; `read` replaces schema/state
with `--card`, `--diff` and optional `--rule`. Both accept `--op` and a positive
`--timeout`; `fixed` additionally requires `--answers`. Their `--dry-run` reads
inputs and an existing record but makes no backend call or write. An existing
operation can return its recorded answer. Decision logic lives apart from
transport. The deployment invokes it through nova-secrets; the current adapter
reads JEV_API_KEY from its environment and cannot establish who supplied it.
Key-redaction checks must establish that credentials do not enter errors or
records. Provider failure
is a failed call, not a low-confidence LAND.

The command follows [the tool standard](STANDARD.md): one result value with
line and JSON renderings, one refusal grammar, explicit effects, bounded output
and a visible remedy. The current parser validates required questions, known types and answer keys,
allowed choices and supplied probabilities in range. The acceptance target
additionally requires complete probability distributions and their normalization
according to the provider schema; the inspected implementation allows partial
choice maps, so missing options can be mistaken for zero. Answers are confined to the declared typed alternatives; the backend supplies
no generated prose, patch, command or explanation. Acceptance requires rejection
of incomplete or uninterpretable answers and preservation of the distinction
between an answer distribution and a separate confidence statistic. The current
sparse-map and confidence-fallback behavior does not yet meet that requirement.

The [provider request](https://docs.typesafe.ai/introduction/quickstart)
contains `state`, `model` and named `questions`; the response contains resolved
`model`, `answers` and `usage`. A Choice answer carries `choice`,
`probabilities` and `confidence`. The following is a synthetic backend shape
example for the verdict question, not an observed result or the nova-decide CLI
envelope. The built-in read includes all five questions.

```json
{
  "state": "A review packet with task, allowed paths, base, head, diff and context",
  "model": "jev-latest",
  "questions": {
    "verdict": {
      "type": "choice",
      "instructions": "Which review disposition does the supplied evidence support?",
      "criteria": {
        "LAND": "The task and checks are satisfied without an introduced defect",
        "BOUNCE": "A specific defect or task violation requires repair",
        "UNSURE": "Available evidence does not settle the review"
      }
    }
  }
}
```

The acceptance target retains the requested model and the resolved model from
the reply; the inspected record keeps only the backend name. A moving alias
alone is insufficient provenance. The nova command renders one `result` plus `facts` and answer `items` through
`internal/tool`. Success facts include `id`, `decision`, `backend`, optional
`verdict` and its `p`, input/output tokens and `recorded=new|existing`. Each
answer item is `{"kind":"answer","fields":{"question":...,"type":...,"value":...,"p":...}}`.
The CLI's `p` is a sorted, three-decimal string; persisted answers instead have
full-precision probability maps. Consumers of calibration data use the record,
not the rounded display. Exit 0 means the query completed, including a BOUNCE
answer; exit 1 is an outcome-label conflict; exit 2 covers usage, backend and
record failures. A result is not a merge authorization.

The `ask` schema is `{"name":...,"questions":{...}}`, with `choice` or `noul`
questions. `fixed` reads a map of answers shaped as `{"noul":0.2}` or
`{"choice":"LAND","p":{"LAND":0.8,"BOUNCE":0.1,"UNSURE":0.1}}`; these numbers
illustrate the format and are not measured model outputs. The record stores a
schema hash, but arbitrary `ask` schemas also need their full definition retained
for later training and reproduction.

[Choice confidence](https://docs.typesafe.ai/confidence) is a rescaling of the
largest answer probability relative to an even split. With three choices,
probability 0.9 corresponds to confidence 0.85. They are different thresholds.
The policy names which statistic it uses; neither number is an empirically
established 90% chance that a sprint diff is correct. A Noul answer instead
supplies a yes-probability without a separate confidence field. The read's first four questions are Nouls, with no explicit unknown answer.
The acceptance policy therefore needs a local abstention rule for missing
context and inconclusive probabilities; the fifth question includes UNSURE.
The inspected adapter incorrectly substitutes confidence when a Choice reply
omits its probability map. That fallback needs repair before its records are
used for probability calibration; missing distributions must not acquire
invented probabilities.

**Check:** schema fixtures through the real parser; malformed and incomplete
responses; injected timeout and backend failure; identical line/JSON facts;
key-redaction tests; replay and outcome-link tests over the record. Unit tests
use a fake backend, no provider requests. The transport has a separate
functional check. The drive reports calls, tokens, p50/p95 elapsed time and
record overhead. These are measurements, not a promise of the provider's
advertised latency. Layer 0 supplies infrastructure and evidence; it has cost,
not an independent saving.

### 1. The first read

The question set asks:

1. Does the diff do what the task states?
2. Is every listed target line changed, or covered by an explicitly permitted
   and verified exception?
3. Are all changed paths inside the task's allowed PATHS?
4. Does the diff introduce a defect?
5. Is the disposition LAND, BOUNCE or UNSURE?

The implemented IDs are `does_task`, `lines_changed`, `inside_paths`, `defect`
and `verdict`. The first four return the probability of yes, with the displayed
value yes at 0.5 and above. That display boundary is not an adoption threshold.
The proposed local policy distinguishes insufficient evidence from a supported answer. Question 2 checks coverage of the listed targets; questions 1 and 4 judge
meaning. A bad rewrite can touch every target and still fail the read. An
already satisfied target needs evidence and an explicitly permitted exception,
not a forced edit or an unrecorded omission. The combined sprint policy also
requires an exact path comparison, including both sides of a rename; nova-decide
itself supplies a model score for question 3, not that deterministic check. A
semantic answer cannot overrule the path gate. The current state contains only the card, optional rule and diff. To support
a broader decision, question 4 needs enough surrounding code and relevant tests
to assess meaning; this is an input-extension requirement. A diff-only packet cannot establish every
cross-file invariant, and unavailable context counts as insufficient evidence.
The final disposition is checked against the component answers and mechanical
gates; contradictory answers escalate rather than pass. Marginal probabilities
are not multiplied as though the five questions were independent; the final
action policy itself is calibrated.

**Check:** the [twenty-card fixture](fixtures/nova-decide-read/README.md)
contains ten reviewed wrong heads and ten reviewed okay heads, reconstructed
input packets and independently grounded expected answers. Its immutable work
diffs are identified, but its captured task versions and transformed packets
are not verified copies of the original calibration inputs. Personal and host
aliases are pseudonymized with the transformation disclosed. The proposed
consuming unit test checks packet construction, schema parsing and policy with
a fake backend; it does not assert that a live model always predicts the
expected answer. Live predictions are a separate calibration evaluation over
exact packets, with no later review text or repaired descendants in their inputs.

**Adoption:** a frozen threshold and eligibility policy replaces a first strings
read only after the heldout calibration meets the declared false-LAND risk and
coverage limits. Until then this layer runs beside the strings read. Outside
the accepted region, on conflicting answers or on incomplete evidence, the strings read remains.
BOUNCE uses the existing rework path; UNSURE reaches the coordinator's inbox.
The probabilistic verdict never bypasses source gates or grants merge authority.

A strings reader still handles explanation, ambiguous specifications, missing
context, architectural and cross-card interactions, and disagreements. Required
pro-card reads remain until separately calibrated. The coordinator still merges
in work order, including cross-stream decisions, in the main session. Ongoing
sampling of accepted high-confidence reads detects errors that abstention-only
review misses.

### 2. Work tier before an attempt, result after it

The first decision grades the card as script, flash or pro before dealing it.
The second classifies an attempt as done, nothing to do, wrong scope, no result,
needs pro or provider failure. These have different input times and labels. Convergence means reaching an independently accepted implementation of the
original task within a declared attempt budget. Record attempts until acceptance,
abandonment and exhausted budgets as well as first-attempt acceptance. A script
grade means a fully specified deterministic transformation with a mechanical
check; flash means bounded work with little unresolved judgment; pro means the
card still requires substantive reasoning or reconstruction. These are proposed
classes whose predictive value is measured, not guarantees of model ability.
The grading input contains no later result; the result classifier cannot save
an already spent attempt. Existing eligibility, capacity and gate rules remain
authoritative.

**Check:** exact-card fixtures for all six attempt classes; a missing RESULT is not
classified as success; scope comparison remains mechanical; a claimed no-op
requires a verified satisfied task. Compare grades on matched task classes using the same attempt budget and
independent acceptance criteria: more tasks accepted within budget or fewer
attempts at unchanged quality is improved convergence. Report cost and wall time
as separate outcomes, including escalations, rework and uncompleted cards. The common record
joins the chosen tier to the actual outcome so misroutes become training data.

### 3. Gate cause

A decision classifies a failed gate as flaky, caused by this change or
pre-existing, with abstention when the evidence does not distinguish them. It
uses the head and base results, failed test identity and relevant logs. A guess
that a failure is flaky does not turn a gate green or waive a repair.

**Check:** replay adjudicated base/head failure pairs and repeat-run evidence;
measure each class's confusion rate, especially caused failures mislabeled as
flaky or pre-existing. The coordinator receives the category and the evidence
record. Only the existing gate policy authorizes a rerun or further action.
Actual rerun/repair outcomes attach to the same decision.

### 4. Score landed diffs and derive finder rules

Every landed diff receives a versioned typed assessment. Findings with shared
features are candidate finder rules, not automatically accepted rules. Scoring
uses reviewed outcomes for calibration; known repair outcomes never leak into
an earlier decision's input. A learned rule has its own version and provenance.

**Check:** independent samples include both high and low scores. A candidate
finder rule catches its known defects and runs against a separate clean set;
its class test has a mutation that goes red. Measure false positives, missed
findings and subsequent avoided failures. Backend scores identify where to
look; the rule's code and tests establish a mechanical check. Scoring itself
adds cost before an applied rule saves anything.

### 5. Routine coordinator judgments

The decision chooses only among the verbs printed in the current judgment,
plus abstention. It receives the judgment identity and relevant state revision.
It produces a choice, never a generated shell command. The consuming tool
rechecks that the choice is still offered and permitted at execution time.
A stale or unresolved judgment returns to the coordinator.

**Check:** offered-choice membership, unknown choices, stale state, replay and
refused execution fixtures; measured error and coverage for each judgment kind.
The execution result and any coordinator correction become outcome evidence.
Holistic merges and release decisions remain with the coordinator; this layer
reduces repetitive decisions whose policy is actually established.

### 6. Card quality before add

A semantic check scores whether the card is complete, testable, within scope
and assigned dependencies that match the work. Deterministic card lint remains
the first check. Unclear requirements return for explanation; they are not
silently rewritten by a typed backend.

**Check:** complete and deliberately defective card fixtures, missing
prerequisites, intersecting file ownership, impossible acceptance criteria and
already satisfied tasks. Evaluate against later attempts and coordinator
adjudication. Track useful cards incorrectly rejected as well as failed work
prevented. The record connects a card revision's assessment to its own outcome,
not to an edited successor's success.

## Calibration is the replacement gate

A probability is an input to a policy, not proof that the policy is safe. A
reproducible calibration supplies the exact cohort, labels, question version,
resolved model, packet construction, predictions and evaluation procedure. The
completed report names 234 cards: 159 okay, 44 ugly, 30 wrong and one outside PATHS.
Independent arithmetic over its appendix reproduces wrong-versus-okay AUC
0.869 and, at defect score 0.5, 21 wrong cards flagged and 22 okay cards flagged.
The supplied report does not include immutable packet hashes or full-precision
answer records, so that review does not independently reproduce provider calls
or the input-to-label joins.
Its separate baseline sentence says 14 wrong in round one plus 15 in round two,
which totals 29, so the manifest must reconcile that discrepancy. The landed-only
cohort exposes false approvals but cannot measure the strings readers' overall
defect recall: approval is part of the sample-selection process.

The proposed two thresholds produce these retrospective outcomes from the
displayed scores:

| Proposed action | Okay | Wrong | Ugly | Outside PATHS | Total |
| --- | ---: | ---: | ---: | ---: | ---: |
| BOUNCE, defect >= 0.5 | 22 | 21 | 17 | 0 | 60 |
| Strings read, 0.3 <= defect < 0.5 | 40 | 6 | 12 | 0 | 58 |
| LAND unread, defect < 0.3 | 97 | 3 | 15 | 1 | 116 |

The low-score wrong cards are `r1-namesd-03` (0.14, incorrect sibling-file
references), `r2-diaryd-08` (0.28, fragments and an invented reason), and
`r1-diaryr-14` (0.29, removed live citations and counter details). The reported
mechanical backstops have no supplied execution receipts proving that they
reject these exact defects. The low band also contains the outside-PATHS card
`r2-negd-42` (0.29); deterministic path enforcement remains mandatory. Do not
enable unread landing from this experiment. Preserve repository-aware review
for context, truth of comments, citations and cross-file references while
measuring the combined policy in shadow mode.

Before threshold selection, define the eligible card classes, acceptable
false-LAND risk, confidence level and minimum useful coverage. Tune on a
development split and freeze the policy before evaluating an untouched holdout.
Group revisions of the same card, related repairs and near-duplicate edits into
one split. If all reviewed cards already influenced the questions or threshold,
a new prospective cohort is needed for independent confirmation.

Report counts and rates for good, defective and unresolved labels against LAND,
BOUNCE, UNSURE and request failure. In particular, report both defective cards
passed divided by all defective cards, and defective cards passed divided by
all auto-LANDs. Coverage retains abstentions, failures and missing outcomes in
its denominator. Report BOUNCE errors too. Confidence intervals, reliability
bins and the Brier score accompany point estimates; strata name their sizes.
A balanced twenty-card regression fixture cannot estimate deployment prevalence
or establish a safe threshold on its own.

**Current disposition: retain the first strings read; no replacement threshold
established.** The completed run supersedes the earlier HTTP 402 attempt. Its
low-band errors rule out the proposed unread-LAND policy without demonstrated
backstops and independent evaluation. The next decision
answers whether a first flash-card read is replaceable, the exact question and
backend versions, the threshold and its eligible scope, and the reads still
required. Ongoing outcome review and a policy rollback rule protect against
changed tasks or backend drift. Training uses the common record, preserves
heldout lineage, and evaluates a candidate before it replaces the current
backend or policy. Even in a hypothetical run with zero misses among only 30 defective cards,
the one-sided 95% binomial upper bound on the defective-pass rate is still
about 9.5% (1-0.05^(1/30)), even before considering sampling bias. Zero misses
in that cohort alone does not establish a near-zero failure rate.

## Cost: count avoided work once

The supplied planning observations are roughly $0.33 per flash card, $8.70 per
pro card, reads around one third of per-card spend, and two rounds of Opus-child
review. Their underlying cost ledger is not yet verified; they
are not prices for an individual review or measured savings from nova-decide.

[The provider's announcement](https://typesafe.ai/blog/introducing-system-one-models-and-jev)
advertises $0.042 per million input tokens, free output and 70–500 ms response
time. A 10,000-input-token call at that rate costs $0.00042 before retries and
other overhead. These are provider claims and arithmetic, not sprint benchmark
results. Type correctness does not establish semantic correctness.

Let C be total cost per accepted card, R1 its first-read cost, x the fraction
whose first read is safely replaced, D the replacement decision cost and E the
added fallback/rework cost. Layer 1 saves x*R1-D-E. If all reviews cost C/3 and
there are two equally priced reads, R1=C/6: at C=$0.33, replacing one read has a
conditional gross ceiling of $0.055 per card at full coverage. One 10,000-token
call would leave $0.05458 before fallback and other costs. Neither the equal
split nor that packet size is measured. Replacing one of two review rounds
never earns both rounds' saving.

| Layer | Incremental saving to measure | Dollar interpretation of supplied observations |
| --- | --- | --- |
| 0. Query and record | Negative backend, storage and learning cost | No independent positive saving; count each call once in its consuming layer. |
| 1. Read | Safely avoided first-read bills minus decisions and added fallback/rework | Conditional $0.055 gross ceiling above; actual R1 and coverage pending. |
| 2. Grade and result | Avoided remaining attempts and cheaper equally successful work, minus decisions and extra repair | $8.70-$0.33=$8.37 gross difference per genuinely substitutable card; remove review components already counted in layer 1. |
| 3. Gate cause | Eliminated rerun and repair bills minus decisions and misclassification | No priced baseline supplied; machine minutes are not model dollars. |
| 4. Score and rules | Future avoided failures attributable to a verified rule, minus scoring and training | Immediate scoring is an expense; later savings require a measured cohort. |
| 5. Judgment | Billable coordinator turns avoided minus decisions and escalation | No turn-cost baseline supplied; reduced waiting is reported separately. |
| 6. Card quality | Additional failed attempts prevented minus decisions and false rejection | Count only events not already credited to grading, gates or finder rules. |

One event ledger owns the attribution. It includes all failed attempts, backend
retries, retained reads, repairs, outcome review and learning overhead. Savings
use accepted cards of matched quality and task mix as the denominator. The
combined baseline-minus-intervention bill checks the sum of layer estimates;
latency, slot time and dollar cost stay separate. This makes it possible to
choose a cheaper decision without concealing the cost of a wrong one.
