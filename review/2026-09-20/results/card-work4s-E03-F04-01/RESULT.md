RESULT work4s-E03-F04-01 sha=5298f6be12ea — nova-work E03-F04: does the contract say it? criterion E03-F04-01: Support evidence-guarded state transitions including blocked, done, deferred, cancelled and superseded
DONE
CRITERION E03-F04-01 SPEC PARTIAL

REPO mas-bandwidth/nova-tools
NO-BRANCH

SEARCHES:
| grep -n "evidence-guarded state transitions" docs/SPEC-WORK.md — 0 hits
| grep -in "evidence-guarded" docs/SPEC-WORK.md — 0 hits
| grep -n "state transition" docs/SPEC-WORK.md — 0 hits
| grep -in "blocked.*done.*deferred" docs/SPEC-WORK.md — 0 hits
| grep -n "blocked\|done\|deferred\|cancelled\|superseded" docs/SPEC-WORK.md | head -20 — 100+ hits
| grep -in "evidence.*:blocked\|:blocked.*evidence" docs/SPEC-WORK.md — 0 hits
| grep -in "evidence.*:defer\|:defer.*evidence" docs/SPEC-WORK.md — 1 hit (line 1100: `:defer` — `:reason`; `:cancel` — `:evidence`)
| grep -in "evidence.*:supersede\|:supersede.*evidence" docs/SPEC-WORK.md — 1 hit (line 1091: `:evidence` that the worker stopped, not about supersede)

CONTRACT LINES (docs/SPEC-WORK.md):
- 1100: `:defer` — `:reason`; `:cancel` — `:evidence`, `:reason`;
- 1101-1102: `:supersede` — `:superseded-by`, `:reason`;
- 1237-1240: `A :to :done transition must name evidence events whose criteria cover every :acceptance entry of the task`
- 1222-1224: `from :cancel-requested to :doing (withdrawn) or, by a :cancel scope event carrying evidence that the worker stopped, to :cancelled`
- 1214-1216: `its :evidence must cover every attempt of the node that was live or uncertain at the request`
- 1241-1242: `A transition to :blocked without :blocked-by is refused.`
- 1228-1229: `a :defer from :todo, :doing, :blocked, :review, :cancel-requested or :unknown to :deferred`
- 1229-1230: `a :supersede from the same six to :superseded`
- 137: `done needs evidence bound to the task's acceptance criteria and verified`
- 141: `:deferred, :cancelled and :superseded are scope events; they never enter the done count`

PARTIAL — The spec states evidence-guarded transitions for `done` (must name evidence events, 1237-1240) and `cancelled` (carries `:evidence` field, 1100; must cover every attempt, 1214-1216; carries evidence that worker stopped, 1222-1224). For `blocked`, the spec requires a `:blocked-by` reference (1241-1242) rather than formal `:evidence`. For `deferred` and `superseded`, the spec does NOT require evidence: `:defer` carries only `:reason` (1100), and `:supersede` carries only `:superseded-by` and `:reason` (1101-1102), with no `:evidence` field. The clause of the roadmap row that has no contract is "evidence-guarded" for transitions to `:deferred` and `:superseded`.

SEXP E03-F04 :by-feature: (:feature "E03-F04" :verified 3 :total 4 :tests "correct-is-a-linked-segment; regression-opens-repair-work; reopen-revives; stop-is-a-hold-not-a-cancel; containers-settle-with-their-members; completed-view-mutation")
Test names in tree:
- correct-is-a-linked-segment — FOUND (lisp/nova-work/tests/replays-8640.lisp:162, acceptance/slice-05-durable-journal.lisp:626)
- regression-opens-repair-work — FOUND (lisp/nova-work/tests/replays-8648.lisp:24, acceptance/slice-07-replays-mid.lisp:140)
- reopen-revives — FOUND (lisp/nova-work/tests/acceptance/slice-02-close-and-counters.lisp:503)
- stop-is-a-hold-not-a-cancel — FOUND (acceptance/slice-05-durable-journal.lisp:580, slice-09-replays-holds.lisp:9)
- containers-settle-with-their-members — FOUND (lisp/nova-work/tests/acceptance/slice-02-close-and-counters.lisp:278)
- completed-view-mutation — FOUND (lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp:327)

git status --short: (empty)

Noticed: The sexp subfeatures entry at line 707 lacks the `:evidence` list (it is empty `()`), while the :by-feature entry at line 84 reports 3/4 verified. The unchecked subfeature is E03-F04-01 (the criterion this card examines), consistent with ROADMAP.md line 415 showing the checkbox as `- [ ]` (open).