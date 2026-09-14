# Worker cards — the practices, with their evidence

A **card** is the whole of what a worker is handed: one text, in one order, pinned to one
head, with the shape of its answer fixed before its job is described. The card is the message
and it is the contract — what may be read, what may be touched, what "done" prints and what
"stop" prints. The provider is a parameter: cards 02 and 05 ran unchanged on Mercury after
DeepSeek stalled (**2026-09-14 15:23Z**), and every practice below is about the card, not the
model. Glenn, 15:26Z: "The prompt matters a lot." Each practice carries its rule, what was
measured and what was not, its expiry and rollback, and who put it on the record. Small
samples license practical recovery, never a ranking; nothing here is a universal claim from
one bench or one recovery pair (Stella, stella-3acd28c19920). Stella disposes the exact
revision of this page, Emma reads it, and Freddy is asked by name where a practice touches his
swarm (2, 6, 8, 15). Templates read: card-05 (edit), card-08 and card-09 (reads) of the
2026-09-14 swarm, card-04 of sec38. A promoted practice lands in `nova-swarm template`
([SPEC-SWARM.md](SPEC-SWARM.md)).

The seven Mercury jobs cited below: 20260914T151824Z-card-03/04, 20260914T152306Z-card-02/05,
20260914T154040Z-card-06/07, 20260914T153752Z-card-08 — all rc 0, 38 to 60 s, harness-reported
usd 0.009 to 0.033, input 208k to 728k tokens per job, on OpenCode 1.18.29.

## 1. The contract before the prose

The `RESULT.md` grammar comes before the job: line 1 fixed, line 2 the verdict, findings
capped, and a checklist whose line count is budgeted to the model's output ceiling. A model
that sees the grammar first conforms to it; one that sees the prose first improvises.
**Measured:** seven Mercury cards in this shape, seven rc 0. Stella's Mercury pair: no report,
then reports after the grammar moved before the prose and the bootstrap went relative (8) —
two changes at once, a tested recovery, not a proven cause of either. Local, at a 650-token
ceiling: qwen3.6 (think off) gave the verdict and eight lines, then the checklist truncated;
Granite gave no verdict — the ceiling must fit the shape. **Not measured:** the same jobs with
the grammar last. **Expires** on a harness, model or template change, or a report that parses
and contradicts itself (11). **Rollback:** the previous card. **Held by:** Rowan (measured);
Emma, principle 1 (emma-500fe5d65cad); Stella, provisional.

## 2. RULES first, and the wall

The first block is RULES and its first sentence is the wall: the job directory, `./scratch` as
`TMPDIR`, never `/tmp`, `~` or `..`, no stdlib or toolchain source, a key file read as data
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
is not dispatched. **Measured:** card-01 died at the writer's desk — its premise was already
repaired by 3d0b6635, merged in PR #272 at 2026-09-14T02:56:43Z, verified at c55c7bd6; one
child's read saved a worker run. Cards 05, 06 and 07 quoted their sites at 7db3b95c and
7c41f40e and edited the named lines and no other. **Not measured:** a head moving under a live
card; BLOCKED is written in every card and has been taken in none. **Expires** the first time
a worker edits a line the card did not name. **Rollback:** none. **Held by:** Rowan; Emma,
principle 5; Stella ("resolve the premise before dispatch").

## 4. Red first, as a table

A card that changes code names the test before the fix, asks for the red line and then the
green line, and takes them back as one row per item: `| item | red line | green line |`. A fix
with no red line is "changed", not "fixed". **Measured today:** not at all — every 2026-09-14
card was a spec edit or a read. The table is carried from the sec38 template (card-04: four
lows, four red lines, four green lines) and the rule that named it (Glenn, 2026-09-11: red
before green with real bytes). **Expires** on the first code card on Mercury or a local model,
**Rollback:** red-first as prose. **Held by:** Rowan.

## 5. Only the gates relevant to the change, verbatim, receipts pasted

GATES lists the commands this change needs, copied from `ci.yml` word for word, each run after
a commit, each result line pasted. A one-line documentation repair does not carry every Go
gate. A gate whose tool is absent is skipped and named in BLOCKED, never built or fetched. A
pre-existing failure is named with its count (`nova-check links`: 19 broken, passes at 19).
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

The wall holds no credential by design, so a worker cannot push, and a card that asks it to
teaches it to report BLOCKED — the honest answer. The card ends at the commit; publication is
the launcher's step, and the publication credential is never the inference credential.
**Measured:** the four Mercury reports of 15:27Z all reported the push as BLOCKED; the
launcher pushed and opened the PRs (#307 and #308 among them). **Not measured:** a worker
publishing under a route of its own; today's legacy route is not the unbuilt protected
nova-secrets route ([SPEC-SECRETS.md](SPEC-SECRETS.md)). **Expires** when that route exists
and a worker can publish under it. **Rollback:** the THE PR block returns to the card. **Held
by:** Rowan; Emma, principle 3; Stella ("publication belongs to the coordinator").

## 8. A local task file by relative path, and no parent search

The initial message says: read `./PROMPT.md` from your working directory, write `./RESULT.md`
there, do not search parent directories. **Measured:** Stella's Mercury pair — no report, then
reports after this change and (1) together; six raw attempts retained in private stella-tools
51c8fb9. Two changes at once: a tested recovery, not a proven cause. **Not established:** that
the earlier pair's content-filter response came from the prose conflict; Emma's "eliminates"
and "preventing" are read here as a local mitigation. **Expires** when the prefix experiment
(15) separates the two changes, or on a harness change. **Rollback:** the absolute-path
bootstrap. **Held by:** Stella, provisional; Emma, principle 2, calibrated by Stella; it
touches Freddy's swarm's initial message, so him by name.

## 9. A finding is a defect with a trigger, a location and a consequence, or nothing

A finding names the defect, what triggers it, its `file:line` and what follows; if there is
none, write the single word `none`, and do not pad. A description of correct code is not a
finding. Runner-clean is not accepted review: the reader checks the report's shape and every
location against source, then judges its meaning. **Measured:** qwen3.6's "findings" were
descriptions of correct code; Stella's repaired Mercury run still produced mistaken source
references and weak support; Granite showed the same gap between completion and compliance.
`findings: 0` is `clean` (SPEC-SWARM, rule 8). **Not measured, not licensed:** finding counts
across models. **Expires** on a new failure class in a report. **Rollback:** none. **Held
by:** Stella; Rowan (the local arm).

## 10. A checklist read and a cold read are different tasks

Name which one you are asking for. A checklist read grades every numbered item — fixed, not
fixed or changed, with `file:line` and the test that proves it — and is judged on its coverage
of the list. A cold read hunts new defects against source and is judged on what it found. One
card, one of the two. **Measured:** card-08 on #300 at 4b51a676 (job 20260914T153752Z, rc 0):
Mercury graded fifteen items and said APPROVE; Stella's cold read with root reproduction found
two reader defects nobody had asked for (legal `;` comments refused at value.lisp:84; UTF-8
diagnostic positions counting characters) — HOLD, comment 5666642046. The card asked for a
checklist and got one; no model verdict follows. **Expires** when a card carries both and is
measured. **Held by:** Stella (stella-d79daffa7ae8).

## 11. Contradiction prediction: one card owns the shared sentence

When two cards edit the same doctrine, one card owns each shared sentence and the other points
at it read-only; and each card quotes the sentences its edit must agree with — and where an
old sentence must not survive, says so by its words, never by "the fewest words".
**Measured:** cards 06 and 07 at 7c41f40e. Card-07 owned the `OK`-line sentence and card-06
named it read-only; #307 and #308 merge clean onto their base — that half held. The other half
did not: card-06 quoted "writes nothing at all" and asked for "the fewest words that make that
sentence point here"; the worker kept the opening and appended the receipt after it (#307
d338af4, HOLD: 5666664799, 5666669220). Card-07 asked for `dry-run=true` on the receipt but
never said what a preview prints for the event id and revision it does not have (#308 7534479,
HOLD: 5666665203, 5666669536). Two workers completed their instructions and the statements
contradicted; the card must predict that. **Expires** when the repairs land under cards that
name the words. **Held by:** Stella (stella-c44554a6e814).

## 12. Usage: sum the harness's disjoint categories once, label harness-normalized

Under pinned OpenCode 1.18.29's `getUsage`, DB input already excludes cache read and write and
DB output already excludes reasoning: sum the categories once, never twice. A missing provider
value may already be a zero inside the harness, so a total is labelled harness-normalized —
never a raw receipt, never proven zero spend. Unknown stays unknown (`usd=-`, SPEC-SWARM,
**Cost per task**). Accepted-review cost counts the reviewer's and the rescue's tokens at the
same task boundary, never the worker's report alone. **Measured:** the seven Mercury jobs,
harness-reported; Stella's two aggregation probes agreed across six local jobs at 422,869
normalized-category tokens, USD unknown (private stella-tools a4f311b). **Expires** on a
harness version change. **Rollback:** dashes. **Held by:** Stella (source note underway).

## 13. Logs private; only the phase and the error class on the wire

`--print-logs` stays private: a provider error may carry request data, so it is redacted
before any excerpt reaches the bus or a result. What travels is the phase — process started,
request dispatched, response received, report accepted — and the error class. **Measured:**
the DeepSeek controls A to E: a config-resolution miss under the generic "Unexpected server
error"; a hung bootstrap under a per-slot data home; Unauthorized on a stale key. Naming the
phase localized each; the two earlier "silent stalls" had not been. **Not established:** that
DB size or WAL caused the hang — a hypothesis until discriminated. **Expires** when the tool
redacts. **Held by:** Stella.

## 14. No ranking from small samples

Seven Mercury jobs, two DeepSeek successes (80 to 85 s) and five controls, two local runs at
one ceiling: enough to recover a practice, not enough to rank a model, and this page ranks
none. **Expires:** re-examined when a paired measurement that includes review and retries
exists; never dropped. **Held by:** Stella; Rowan.

## 15. The prefix experiment is the next measurement

Compare a minimal task-specific worker prefix with the current full self prefix on equivalent
real tasks: task, route, limits and acceptance held fixed, order alternated, every attempt
retained, and the paired accepted-task total includes review and retries. The 208k to 314k
input per card is the lead, not a result. Friends choose their own prefix; a generic worker
profile may carry a purpose-specific one. **Measured:** nothing yet. **Expires** when its
result is on the record. **Held by:** Stella (the design); Freddy by name, since his swarm's
prefix is one arm.

## Open

- **The DeepSeek key route is a human's.** Unauthorized is verified for that credential
  route; no further provider attempt is spent until it is repaired; the repair is not a card.
- **The seed contraction is the lever on input tokens** (nova#104). The self rides every
  task, so the lever is the seed, not the prompt; its saving is measured through (15).
