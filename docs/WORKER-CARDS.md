# Worker cards — the practices, with their evidence

A **card** is the whole of what a worker is handed: one text, in one order, pinned to one
head, with the shape of its answer fixed before its job is described. The card is the message
and it is the contract — what may be read, what may be touched, what "done" prints and what
"stop" prints. The provider is a parameter: cards 02 and 05 ran unchanged on Mercury after
DeepSeek stalled (**2026-09-14 15:23Z**), and every practice here is about the card. Glenn,
15:26Z: "The prompt matters a lot." Each practice carries its rule, what was measured and what
was not, its expiry and rollback, and who put it on the record. Small samples license
practical recovery, never a ranking; nothing here is a universal claim from one bench or one
recovery pair (Stella, stella-3acd28c19920). Stella disposes the exact revision of this page,
Emma reads it, and Freddy is asked by name where a practice touches his swarm (2, 6, 8, 9,
16). Templates read: cards 05 (edit), 08 and 09 (reads) of 2026-09-14, sec38 card-04. A
promoted practice lands in `nova-swarm template` ([SPEC-SWARM.md](SPEC-SWARM.md)).

**The practices `nova-swarm lint --card` checks mechanically, by their rule tokens.** A card
writer meets these tokens on a `LINT DRIFT` line and has to know which practice they belong
to; before 2026-09-18 they were written down nowhere at all (#1464), and a bench's clone of
the tool is months behind the binary installed on it. Each drift now carries its own remedy
line, and `nova-swarm lint --rules` prints every token and what it wants — that listing, not
this table, is what a bench with a stale clone reads.

| rule token | practice |
| --- | --- |
| `result-first` | 1 — line 1 is the contract line, in either form the tools write today: `RESULT <label> sha=<sha12>` as `cut` writes it, or `RESULT: <CARD-id> <what done looks like>`. The two disagree by one colon and #1741 settles which stands |
| `result-last` | 1, 25 — the last step writes `RESULT.md`, whose line 1 is that contract line |
| `no-sandbox` | 2 — a card runs inside the wall and never invokes it |
| `files-named` | 3 — the work is anchored to a named file or package |
| `red-test` | 4, 23 — a reproducing test named, or `probe`/`read` for a card that only reads |
| `test-command` | 5 — the gate written verbatim, or `no tests` said in words |
| `clone-step` | 17, 25 — `STEP 1` clones or `cd`s into the repository. The WHOLE step is read, its line and the lines under it down to the next `STEP`, so the wording of the STEP line is yours and the command is the rule |
| `steps-numbered` | 17 — `STEP <n>.` lines, numbered 1, 2, 3 in order |
| `deadline` | 17 — a deadline, or `finish within <n> minutes` |
| `scratch-absolute` | 25 — scratch named against a root, never as a bare relative word |
| `no-parent-path` | 25 — no `../` anywhere: the wall refuses every path above the job |
| `size` | — the card is under the 12000-byte ceiling, so it is read in one window. The ceiling is never silent: every lint ends with the card's size and the cap, on the `LINT OK` line or on a `LINT SIZE` line |

Four more tokens read the **typed header** `cut` writes under the contract line —
`KIND:`, `PATHS:`, `TEST:`, `LEGS:`, `SOURCE:` (`docs/SPEC-TOOLWORK.md` §5 rule 1). They fire
on any card that declares one of those five lines, and on any card at all under
`lint --card <file> --typed`; a card written before §5 carries no header and is checked by the
twelve rules above only. The grammar is the gate's own: `internal/pulse/cardheader.go` parses
these lines at `accept`, and the lint accepts exactly what it accepts and refuses what it
refuses, so a card that lints clean on the bench is not rejected at the gate for its header.

| rule token | what it wants |
| --- | --- |
| `kind-declared` | a `KIND: <kind>` line with a kind on it; `cut` writes it from the pool row and a model never does |
| `paths-declared` | a `PATHS: <glob>[, <glob>...]` line, repository-relative, no `..`, every glob holding at least one literal segment — or `PATHS: none` for a card that changes nothing |
| `test-named` | a `TEST: <package> <TestName>` line — two fields, the name a Go test name — or `TEST: none` where the kind declares no gate |
| `paused` | the coordinator has not paused this kind. The remedy is never a rerun: it is `nova-pulse trust --set trial`. Checked only when `lint --card` is handed the state with `--trust <file>`, in the shape `nova-pulse trust` prints |


The seven Mercury jobs cited below: 20260914T151824Z-card-03/04, 20260914T152306Z-card-02/05,
20260914T154040Z-card-06/07, 20260914T153752Z-card-08 — all rc 0, 38 to 60 s, harness-reported
usd 0.009 to 0.033, input 208k to 728k tokens per job, on OpenCode 1.18.29.

## 1. The contract before the prose

The `RESULT.md` grammar comes before the job: line 1 fixed, line 2 the verdict, findings
capped, and a checklist whose line count is budgeted to the model's output ceiling. Putting
the grammar first may improve conformance — a hypothesis, not a measurement; no same-job
comparison exists, and the observed runs establish recovery, not causation. **Measured:** seven
Mercury cards in this shape, seven rc 0. Stella's Mercury pair: no report, then reports after
the grammar moved before the prose and the bootstrap went relative (9) — two changes at once,
a tested recovery, not a proven cause of either. Local, at a 650-token ceiling: qwen3.6 (think
off) gave the verdict and eight lines, then the checklist truncated; Granite gave no verdict —
the ceiling must fit the shape. **Not measured:** the same jobs with the grammar last.
**Expires** on a harness, model or template change, or a report that parses and contradicts
itself (12). **Rollback:** the previous card. **Held by:** Rowan (measured); Emma, principle 1
(emma-500fe5d65cad); Stella, provisional.

## 2. RULES first, and the wall

The first block is RULES and its first sentence is the wall: the job directory, the `TMPDIR`
the runner already exported (a native card never sets one: the runner hands it
`<slot>/tmp/<label>`, outside every repo, and a card that re-exported `$PWD/scratch` put its
temp dir inside the job's repo and failed nova-wake's `TestAwakeRefusesNonBus` for a reason it
did not cause, #460), never `/tmp`, `~` or `..`, no stdlib or toolchain source, a key file read as data
and never sourced, the deadline held by the machinery and named in the card, and a refused
read is not the end of the run. **Measured:** seven of seven Mercury jobs rc 0 with a report
in shape, none ended on a refused read; the four of 15:27Z ran with no rephrasing, no
wandering, no filter refusal. The hurt behind each clause is in SPEC-SWARM's table. **Not
measured:** a card without the wall on these models. **Expires** on a change to the sandbox
rule or the harness's permission model. **Rollback:** none; the wall is a floor. **Held by:**
SPEC-SWARM; it is Freddy's swarm's wall too, so a reworded RULES block asks him by name.

## 3. Anchors as `file:line` at a pinned head, verified by the card writer

Every place the worker reads or edits is `file:line` at one head the card prints and the
worker must see from `git rev-parse HEAD`; the card writer opened every anchor at that head
before dispatch. Head moved: BLOCKED naming the head you got, and stop. Premise dead: the card
is not dispatched. The clone URL is https, never a host alias: card-13 died at the clone on
`git@github-rowan:`, which the wall does not resolve. **Measured:** card-01 died at the
writer's desk — its premise was already repaired by 3d0b6635, merged in PR #272 at
2026-09-14T02:56:43Z, verified at c55c7bd6; one child's read saved a worker run. Cards 05, 06
and 07 quoted their sites at 7db3b95c and 7c41f40e and edited the named lines and no other.
**Not measured:** a head moving under a live card; BLOCKED is written in every card and has
been taken in none. **Expires** the first time a worker edits a line the card did not name.
**Rollback:** none. **Held by:** Rowan; Emma, principle 5; Stella ("resolve the premise before
dispatch").

## 4. Red first, as a table

A card that changes code names the test before the fix, asks for the red line and then the
green line, and takes them back as one row per item: `| item | red line | green line |`. A fix
with no red line is "changed", not "fixed". **Measured today:** card-13 changed code at
96034af8 and reported red first, thirteen green — but the reported evidence did not cover the
claimed defects: the UTF-8 test only round-trips a valid string, and the comment test omits
forbidden-looking syntax; an independent reproduction still fails both families (PR300
comment 5667395159). **Limitation:** a test must assert the literal expected failure and be
run unchanged against the broken revision; a pasted red/green row or an `expected=` label is
not sufficient. This failed repair and its review costs are preserved here, not erased.
The table is carried from the sec38 template (card-04: four
lows, four red lines, four green lines) and the rule that named it (Glenn, 2026-09-11: red
before green with real bytes). **Expires** on the first code card on Mercury or a local model,
**Rollback:** red-first as prose. **Held by:** Rowan.

## 5. Only the gates relevant to the change, verbatim, receipts pasted

GATES lists the commands this change needs, copied from `ci.yml` word for word, each run after
a commit, each result line pasted. A one-line documentation repair does not carry every Go
gate. A gate whose tool is absent is skipped and named in BLOCKED, never built or fetched. A
pre-existing failure is named by its failure set, not only its count, compared against the
pinned base (`nova-check links`: 19 known broken links, same named targets at the pinned base;
no new failures) — a count alone can accept a newly broken link when a different old failure
disappears. A known unchanged failure may be documented; any new or changed failure needs a
disposition. A cherry-pick card's gates include the conflict-marker grep: card-16 left `<<<<<<< HEAD` inside
a fenced grammar block, and `git diff --check` caught it on the read, not in the card.
**Measured:** cards 05, 06 and 07 carried `git diff --check`, `git diff --stat` and
`nova-check links` only, and their reports pasted all three; card-08's one gate was the suite
script. **Expires** on a `ci.yml` change. **Rollback:** the full gate block. **Held by:**
Stella ("only the gates relevant to the change"); Emma, principle 4.

## 6. PERMITTED as the last permission line

One line after the gates names everything beyond the job directory the worker may touch, and
says of itself "this is the last permission line", so nothing later in the card can be read as
widening it. `go doc` is permitted where a stdlib symbol's semantics are needed; stdlib source
is not. **Measured:** on 2026-09-13 cards died on stdlib, toolchain and `..` reads; with the
line in every card, 2026-09-14's seven Mercury jobs ended on no refused read. **Not
measured:** the line placed earlier in the card. **Expires** on a change to the harness's
permission model. **Held by:** Rowan; shared with Freddy's swarm, so him by name.

## 7. The card ends at the commit; push and PR are the launcher's

The wall holds no repository-publication credential by design (an inference credential is a
different capability), so a worker cannot push, and a card that asks it to
teaches it to report BLOCKED — the honest answer. The card ends at the commit; publication is
the launcher's step, and the publication credential is never the inference credential.
**Measured:** the four Mercury reports of 15:27Z all reported the push as BLOCKED; the
launcher pushed and opened the PRs (#307 and #308 among them). **Not measured:** a worker
publishing under a route of its own; today's legacy route is not the unbuilt protected
nova-secrets route ([SPEC-SECRETS.md](SPEC-SECRETS.md)). **Expires** when that route exists
and a worker can publish under it. **Rollback:** the THE PR block returns to the card. **Held
by:** Rowan; Emma, principle 3; Stella ("publication belongs to the coordinator").

## 8. The push step checks the branch

Before pushing a worker's commit the launcher prints `git branch --show-current` and refuses
main, master or the PR's base; `git diff --stat <base>..HEAD` must match the card's TOUCHED
list; the push names the branch by explicit refspec, never bare `HEAD`. The card says that
`git branch --show-current` must equal `<branch>` before the commit, and `RESULT.md` line 1
carries the branch. **Measured:** card-24's Mercury worker committed its one-line HARNESSES.md edit on
main in its clone though the card named a branch; the launcher's bare `git push origin HEAD`
landed f2ca7db on mas-bandwidth/nova main at **16:32Z** with no read; reverted within the
minute (f45fa3d3), reapplied as nova#106 from a branch. The day's eleven earlier pushes went
to feature branches by the same bare command; only the branch differed. **Not measured:**
where in the run the worker left the card's branch. **Expires** when the push is a tool that
refuses by construction. **Rollback:** none; a check, not a practice under test. **Held by:**
Rowan (the hurt, 16:32Z).

## 9. A local task file by relative path, and no parent search

The initial message says: read `./PROMPT.md` from your working directory, write `./RESULT.md`
there, do not search parent directories. **Measured:** Stella's Mercury pair — no report, then
reports after this change and (1) together; six raw attempts retained in private stella-tools
51c8fb9. Two changes at once: a tested recovery, not a proven cause. **Not established:** that
the earlier pair's content-filter response came from the prose conflict; Emma's "eliminates"
and "preventing" are read here as a local mitigation. **Expires** when the prefix experiment
(16) separates the two changes, or on a harness change. **Rollback:** the absolute-path
bootstrap. **Held by:** Stella, provisional; Emma, principle 2, calibrated by Stella; it
touches Freddy's swarm's initial message, so him by name.

## 10. A finding is a defect with a trigger, a location and a consequence, or nothing

A finding names the defect, what triggers it, its `file:line` and what follows; if there is
none, write the single word `none`, and do not pad. A description of correct code is not a
finding. Runner-clean is not accepted review: the reader checks the report's shape and every
location against source, then judges its meaning. **Measured:** qwen3.6's "findings" were
descriptions of correct code; Stella's repaired Mercury run still produced mistaken source
references and weak support; Granite showed the same gap between completion and compliance.
`findings: 0` is `clean` (SPEC-SWARM, rule 8). **Not measured, not licensed:** finding counts
across models. **Expires** on a new failure class in a report. **Rollback:** none. **Held
by:** Stella; Rowan (the local arm).

## 11. A checklist read and a cold read are different tasks

Name which one you are asking for. A checklist read grades every numbered item — fixed, not
fixed or changed, with `file:line` and the test that proves it — and is judged on its coverage
of the list. A cold read hunts new defects against source and is judged on what it found. One
card, one of the two. **Measured:** card-08 on #300 at 4b51a676 (job 20260914T153752Z, rc 0):
Mercury graded fifteen items and said APPROVE; Stella's cold read with root reproduction found
two reader defects nobody had asked for (legal `;` comments refused at value.lisp:84; UTF-8
diagnostic positions counting characters) — HOLD, comment 5666642046. The card asked for a
checklist and got one; no model verdict follows. **Expires** when a card carries both and is
measured. **Held by:** Stella (stella-d79daffa7ae8).

## 12. Contradiction prediction: one card owns the shared sentence

When two cards edit the same doctrine, one card owns each shared sentence and the other points
at it read-only; and each card quotes the sentences its edit must agree with — and where an
old sentence must not survive, says so by its words, never by "the fewest words".
**Measured:** cards 06 and 07 at 7c41f40e. Card-07 owned the `OK`-line sentence and card-06
named it read-only; #307 and #308 merge clean onto their base — that half held. The other half
did not: card-06 quoted "writes nothing at all" and asked for "the fewest words that make that
sentence point here"; the worker kept the opening and appended the receipt after it (#307
d338af4, HOLD: 5666664799, 5666669220). Card-07 asked for `dry-run=true` on the receipt but
never said what a preview prints for the event id and revision it does not have (#308 7534479,
HOLD: 5666665203, 5666669536). Two completed instructions contradicted each other; the card
must predict that. **Expires** when the repairs land under cards that name the words. **Held
by:** Stella (stella-c44554a6e814).

## 13. Usage: sum the harness's disjoint categories once, label harness-normalized

Under pinned OpenCode 1.18.29's `getUsage`, DB input already excludes cache read and write and
DB output already excludes reasoning: sum the categories once, never twice. A missing provider
value may already be a zero inside the harness, so a total is labelled harness-normalized —
never a raw receipt, never proven zero spend. Unknown stays unknown (`usd=-`, SPEC-SWARM,
**Cost per task**). Accepted-review cost counts the reviewer's and the rescue's tokens at the
same task boundary, never the worker's report alone. **Measured:** the seven Mercury jobs,
harness-reported; Stella's two aggregation probes agreed across six locally launched
provider attempts (not six local-model runs) at 422,869
normalized-category tokens, USD unknown (private stella-tools a4f311b). **Expires** on a
harness version change. **Rollback:** dashes. **Held by:** Stella (source note completed at
stella-tools a4f311b).

## 14. Logs private; only the phase and the error class on the wire

`--print-logs` stays private: a provider error may carry request data, so it is redacted
before any excerpt reaches the bus or a result. What travels is the phase — process started,
request dispatched, response received, report accepted — and the error class. **Measured:**
the DeepSeek controls A to E: a config-resolution miss under the generic "Unexpected server
error"; a hung bootstrap under a per-slot data home; Unauthorized on a stale key. Naming the
phase localized each; the earlier "silent stalls" had not been. **Not established:** that DB
size or WAL caused the hang — a hypothesis until discriminated. **Expires** when the tool
redacts. **Held by:** Stella.

## 15. No ranking from small samples

Seven Mercury jobs, two DeepSeek successes (80 to 85 s) and five controls, two local runs at
one ceiling: enough to recover a practice, not enough to rank a model, and this page ranks
none. **Expires:** re-examined when a paired measurement that includes review and retries
exists; never dropped. **Held by:** Stella; Rowan.

## 16. The prefix experiment is the next measurement

Compare a minimal task-specific worker prefix with the current full self prefix on equivalent
real tasks: task, route, limits and acceptance held fixed, order alternated, every attempt
retained, and the paired accepted-task total includes review and retries. The 208k to 314k
input per card is the lead, not a result. Friends choose their own prefix; a generic worker
profile may carry a purpose-specific one. **Measured:** nothing yet. **Expires** when its
result is on the record. **Held by:** Stella (the design); Freddy by name, since his swarm's
prefix is one arm.

## 17. Numbered-steps shape for DeepSeek on OpenCode

A card for a DeepSeek model states the working directory and the clone as step 1, with one
command per line, numbered steps with one check each, the verdict vocabulary inside the step,
the RESULT shape last and short, no capitalised contract block and no launcher text in its
contract lines. **The word check reads lines 1-3 — the contract line, the role line and
`STEP 1` — and nothing below them**: a card that *quotes* an issue mentioning a launcher is a
card about one, not a card run by one, and refusing it cost a batch 34 cards (nova-tools
issue #529).
**Measured:** 2026-09-15, three read cards in the capitalised-contract shape stalled 20
minutes with no output on opencode/deepseek-v4-flash (cards 50, 51, 52); the same job in the
numbered shape ran in 148 s for USD 0.007 with correct quoted evidence (card 55); four writing
cards in the plain shape all ran; Mercury tolerates both. **Not measured:** the same numbered
shape on another model family. **Expires** 2026-10-15 or on the next harness version,
whichever comes first. **Rollback:** the capitalised-contract shape. **Held by:** Glenn,
2026-09-15: "You are responsible for prompting. If we don't get the result we want, fix the
prompt."

## 18. Scratch notes live in the repo directory as notes.txt

A card's scratch notes are written to `notes.txt` inside the repo directory it names, never
under `../scratch` or any path above the job, and `notes.txt` is never committed. **Measured:**
2026-09-15: card 255 died when the wall refused `jobs/scratch`, a path above the job; a note
beside the work is inside the wall. **Not measured:** a note file inside the job directory
refused. **Expires** on a change to the wall's path rule. **Rollback:** notes above the job.
**Held by:** Rowan.

## 19. A text-only card says so on its second line

A text-only card states on its second line that it forbids `go build`, `go test` and any test
harness, so a worker spends no budget verifying what it was told not to build. **Measured:**
2026-09-15: card 250 spent its whole budget building a harness to verify a grammar edit and
changed nothing. **Not measured:** the same card with the second line present. **Expires** on
a harness or template change. **Rollback:** the second line dropped. **Held by:** Rowan.

## 20. A chain step after a card gates on the verdict line

A chain step that follows a card gates on `RESULT.md`'s verdict line, never on mergeability
alone; a merged but abstained card does not advance the chain. **Measured:** 2026-09-15: #239
merged on a chain while its card had abstained; corrected by #415. **Not measured:** a chain
that gates on the verdict from the start. **Expires** on a chain rule change. **Rollback:**
gating on mergeability alone. **Held by:** Rowan.

## 21. A tool card names the sequence a friend runs through the verb it changes

A card that changes a verb names, in its contract, THE SEQUENCE a friend runs that verb inside
-- the verbs before it and after it, in order -- and adds the round trip for that sequence
beside the verb's own tests, named `TestFriendSequence<Sequence>`, which is the name CI's
`e2e` job selects on. A verb's own tests see what the verb prints; only the
sequence sees the state it LEAVES, which is what the next verb refuses over. **Measured:**
2026-09-15: three breakages in one sitting, every one of them a verb passing its own tests --
`wait` left `from-<me>/BEAT` uncommitted and the `send` after it refused over that file (#488,
#459); `nova-version snapshot` wrote a manifest `nova-version report` refuses as an unknown
kind (#571); `nova-review packet` diffed against the lane's stale base, 130 KB of packet for a
12-line PR (#418). **Not measured:** how many sequences a verb is in, so a card names the one
it changes and not a catalogue. **Expires** on a change to how the tools compose, or 2026-12-15.
**Rollback:** per-verb tests alone. **Held by:** Glenn, 2026-09-15: "make sure to capture the
dogfood breakage with new tests"; the sequences are `-run TestFriendSequence` in CI's `e2e`
job.

## 22. A card's line-1 contract never carries the answer

Line 1 is identity and nothing else — `RESULT <label> sha=<sha12>`, the label and the hash of
the text below it; the verdict is line 2 and the findings come after. Since `gather` scores a
`RESULT.md` **done on line 1 alone, whatever the harness exit code** (SPEC-SWARM, **gather**,
landed in #577, pinned by `done-whatever-the-exit-code`; the rc is recorded on the line), a line 1 that stated the expected verdict, the expected count or the fix
would be a card a worker completes by echoing it. The contract line says only "this is the
card I was given"; everything that must be earned sits below it, where the reader reads.
**Measured:** 2026-09-15, the practice-17 shape on 326 cards, line 1 never holding a verdict,
and `harvest` disposing every card by line 2 (SPEC-PULSE rule 11). **Not measured:** a card
whose line 1 carried the answer. **Expires** on a change to the contract-line grammar.
**Rollback:** none; this is what line 1 is for. **Held by:** Rowan.

## 23. A fix card names its reproducing test, quotes its red line, and names the sequence

A card that fixes a fault names the test that reproduces it before the fix, the red line the
test prints on the broken revision, and the friend sequence the verb sits in (21); the PR it
becomes carries the `red:` line and the test file, and the manager refuses a fix PR that
carries neither before the push (landed in #587, `manager-refuses-fix-pr-without-test`). The
test answers inside the fast tier — under one minute per package, two at most; a timing-shaped
test takes a fake clock or a sync point, or the `slow` tag and the nightly job (#516, landed
in #606).
**Measured:** 2026-09-15/16, every pit-stop fix landed red first, named after the sentence it
broke: #577 (three tests), #586 (`TestNativeConfigChecksOnlyTheModelsProvider` and the
absolutize pair, resolved rather than weakened), #581 (three, each run red against a mutated
pull), #588 (`TestWaitLeavesCheckoutClean`, `TestSendAfterWaitBeatSucceeds`); and the false
reds those tests replaced cost retries all day. **Not measured:** a fix card without a named
test that landed clean. **Expires** when the push tool refuses a test-less fix by
construction. **Rollback:** practice 4 as prose. **Held by:** Rowan; Glenn, 2026-09-15:
"make sure to capture the dogfood breakage with new tests".

## 24. Hedged cards and unproven routes stay off the critical path

A card on the critical path says one thing and expects one outcome: no "if possible", no "or
else do X", no route whose harness has not been proven to parse the model's tool calls. A
hedge is a second card hidden inside the first, and the worker picks the cheaper branch. A
local model is one slot and never a card the day waits on: the adoption probe records which
local models the harness can drive (`probe-local-model-supports-tools`, SPEC-SWARM, **The
verbs**, `native`). **Measured:** 2026-09-15/16, a walled local run reported `NATIVE OK rc=0`
having written nothing — the model emitted its tool calls as raw text the harness did not
parse (#591); the coordinator's first diagnosis blamed the wall, a second read found the
route. **Not measured:** a hedged card that returned the branch the writer meant. **Expires**
when the probe is in the adopt step and every local route on the table has a probe line.
**Rollback:** none. **Held by:** Rowan; Glenn's one-slot rule for local models.

## 25. The runner owns `TMPDIR`; `RESULT.md` lives at the job root; a checkout may pre-exist

Three places a card must not choose for itself. `TMPDIR` is exported by `native` outside every
repository and printed as `tmp=<path>`; a card exports none (#460, landed in #558; the `STEP 1`
export in SPEC-PULSE **The card** is now redundant and comes out with the template, and a test
that asserts "not a repo" under the old export was a red the card did not cause). `RESULT.md`
is written at the job root, never under `repo/`; `gather` copies a misplaced one up and says
so, and the card is still wrong (#594, landed in #603; the bench pull first, #581). `STEP 1` tolerates an existing checkout, because
a pre-cloned `repo/` from a bench mirror is how no card pays a clone (#553 rule 3, open).
**Measured:** 2026-09-15, cards 247, 266 and 353 reported `TestAwakeRefusesNonBus` red under
`TMPDIR=<job>/scratch`; on the same day several cards wrote `RESULT.md` into `repo/` and were
scored `no-result`. **Not measured:** a card that set its own `TMPDIR` outside the job.
**Expires** when the `STEP 1` template loses the export and #553's pre-clone lands. **Rollback:** the
`STEP 1` export. **Held by:** Rowan.

## 26. One amendment, one section file

A spec amendment or a replay amendment edits one section file, never the whole
monolith: one file under `docs/spec-pulse/` per `## ` section of SPEC-PULSE.md
(included by the top file, checked by `internal/pulse/layout560_test.go`), and
one file under `lisp/nova-work/tests/acceptance/` per replay slice (loaded by
`lisp/nova-work/tests/acceptance.lisp`). Every replay PR used to append to one
`acceptance.lisp` and every SPEC-PULSE amendment edited one file, and all of
them conflicted (nova-tools #560). Parallel PRs that touch different sections
now touch different files and merge clean.
**Measured:** nothing yet; this practice licenses the layout, and the first two
parallel amendment PRs that merge without conflict are its evidence.
**Expires** when the layout changes. **Rollback:** the single-file specs.
**Held by:** Rowan.

## Open

- **The DeepSeek key route is a human's.** Unauthorized is verified for that credential
  route; no further provider attempt is spent until it is repaired; the repair is not a card.
- **The seed contraction may reduce input tokens** (nova#104). For routes that actually
  load seed-derived text on each task, shrinking it may reduce input; verify the actual loaded
  prefix and compare accepted-task totals in (16). Neither per-task loading nor this saving
  is established for every route.
