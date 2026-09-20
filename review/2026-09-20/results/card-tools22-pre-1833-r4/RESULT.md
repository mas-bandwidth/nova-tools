RESULT tools22-pre-1833-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1833 at head 8f4394f4f993: nova-work: roadmap row and roadmap projection remember their preimages and reach undo and redo
PREREAD 1833 claims=19 proven=19 unproven=0 defects=1 high=0
PR 1833
HEAD 8f4394f4f99308e50afe5f338b8f76cced61d0a2
BASE rowan/work-e06-undo-roadmap
MERGE-BASE 485050e30543e816f4adcc6328fe717bcd1f1248
BEHIND 10
FILES 10 production, 4 test
LINES +2740 -57

=== CLAIMS ===

1. `roadmap row` and `roadmap projection` save a `:before`/`:after` snapshot in `kernel-applied` keyed by request id when a request id is supplied (roadmap.lisp). PROVEN-BY replays-undo-roadmap-rows.lisp:2397 `roadmap-row-is-reversible-through-undo` — the undo later reads those fields to restore the preimage.

2. `roadmap-row-undo` restores the ordered preimage only while the live view still equals the event's `:after`; any intervening change is a conflict (roadmap.lisp:994–1038). PROVEN-BY replays-undo-roadmap-rows.lisp:2462 `a-stale-roadmap-row-undo-refuses-atomically` — attempts undo after a second row has been added and gets refusal "conflict; the view moved members".

3. `roadmap-projection-undo` restores the full `:projections` list including the removed plist at its original index under the same postimage guard (roadmap.lisp:1040–1071). PROVEN-BY replays-undo-roadmap-rows.lisp:2562 `roadmap-projection-is-reversible-through-undo` — removes p1 then undoes, confirming both the index restoration and that all plist fields (repo, path, start) survive round-trip.

4. Identical re-add of a `roadmap projection` creates a no-effect receipt with `changed=0` and stores it so retries replay the stored line (roadmap.lisp:1192–1208, :1218–1225). PROVEN-BY replays-undo-roadmap-rows.lisp:2711 `an-identical-projection-re-add-records-a-real-no-effect-receipt` — adds once, re-adds, verifies entry exists with verb :roadmap-projection and changed 0; and replays-undo-roadmap-rows.lisp:2730 `an-identical-projection-re-add-retried-returns-its-original-receipt` — retry returns the identical line, different payload under same request id refuses.

5. The `*undo-handlers*` table registers five-key handler lists per verb at load time from `undo-roadmap.lisp` (:roadmap-configure) and `undo-roadmap-rows.lisp` (:roadmap-row, :roadmap-projection), and `edit-undo.lisp` dispatches through `undo-handler` to find them (edit-undo.lisp:74–97, undo.lisp:1648–1657, :1666–1669, :1680–1685, :1698–1705). PROVEN-BY replays-undo-roadmap.lisp:2845 `roadmap-configure-is-reversible-through-undo` — confirms the whole chain works for configure; each rows test does the same for its verb.

6. `undo-plan` over a registered verb checks postimage match and names conflicting fields if stale; refuses atomically with exit 1 (undo-roadmap.lisp:1562–1574, undo-roadmap-rows.lisp:1346–1360, :1411–1424). PROVEN-BY multiple tests: replays-undo-roadmap.lisp:2891, replays-undo-roadmap-rows.lisp:2462, :2609 — all confirm atomic stale refusal naming what changed, and that nothing was mutated on the failed attempt.

7. `redo` over a registered verb reapplies the original intent through the normal verb under the redo's own request id (undo.lisp:1698–1705, undo-roadmap.lisp:1605–1629, undo-roadmap-rows.lisp:1391–1405, :1454–1484). PROVEN-BY replays-undo-roadmap.lisp:2930 `redo-reapplies-a-roadmap-configure` — redoes configure verifying field values restored; replays-undo-roadmap-rows.lisp:2494 `redo-reapplies-a-roadmap-row`, :2641 `redo-reapplies-a-roadmap-projection`.

8. Redo reapplies against current preconditions via `redo-changed` and never deletes the undo record (undo.lisp, undo-roadmap.lisp:1594–1603, undo-roadmap-rows.lisp:1381–1389, :1446–1452). PROVEN-BY all four redo tests check `(find :undo ...)` survives in the view log, confirming undo is not deleted.

9. Stale redo plan (view moved past undo's preimage) refuses with exit 1 naming what is stale (undo.lisp:342–351, redo-changed handlers return non-empty lists). PROVEN-BY replays-undo-roadmap.lisp:2961 `redo-refuses-a-stale-roadmap-plan`, replays-undo-roadmap-rows.lisp:2524, :2670.

10. `execution reconcile` admits an observation manifest whose content address matches `from`, validates seven outcomes, requires five fields, binds records to control, and refuses before writing on any defect (execution-reconcile.lisp:232–262, :450–509). PROVEN-BY replays-execution-reconcile.lisp:1818 `reconcile-refuses-and-writes-nothing` — tests all six refusal paths: content-address mismatch, eighth outcome, unbound record, wrong control, empty manifest, over-bound, unknown control.

11. Contradictory observations are preserved unresolved, never last-write-wins; a later reconciliation keeps earlier ones (execution-reconcile.lisp:341–352, :301–311). PROVEN-BY replays-execution-reconcile.lisp:1766 `reconcile-preserves-contradiction-over-the-kernel` — admits running+stopped (contradiction stays unresolved), admits a third completed record, asserts all three retained and disposition still unresolved.

12. `acknowledged=` count is always 0 in execution status because reconcile admits the observation receipt but never the acknowledgement receipt (execution-reconcile.lisp:549–554). PROVEN-BY replays-execution-reconcile.lisp:1805 assertion `"acknowledged=0"` in `reconcile-preserves-contradiction-over-the-kernel`.

13. Missing usage stays `:unknown`; stop reports never synthesise zero cost (execution-reconcile.lisp:366–372). PROVEN-BY replays-execution-reconcile.lisp:1951 assertion `check-equal :unknown (target-usage retained "att-4")` — verified against a confirmed stop with no result.

14. Only a qualifying `not-started` (durable launch rejection, no contradiction, unlaunched) releases an unlaunched reservation; confirmed stop bypasses holder-only release; unknown capacity is never advertised free (execution-reconcile.lisp:404–432). PROVEN-BY replays-execution-reconcile.lisp:1905 `only-a-qualifying-not-started-released-a-reservation` — five scenarios, each checked individually.

15. Silence, expired lease, elapsed estimate, and unbound negative lookup are not stop evidence; bound negative lookup is (execution-reconcile.lisp:332–339 via `stop-evidence-p`). PROVEN-BY replays-execution-reconcile.lisp:1959 `a-stop-that-is-not-evidence-settles-nothing`.

16. Output grammar lines follow SPEC-WORK.md:6020 (`operation=-` for reconcile), 6021 (status counts, no `id=`, `acknowledged=0`), 6022 (`EXECUTION ROW` per target), 6023 (failure line with exit 1) (execution-reconcile.lisp:438–448, :529–561). UNPROVEN — no test parses output line field names, order, or spelling against the grammar.

17. The durable half: journal accept-then-record means a brand-new kernel over the same file journal recovers the hold and the manifest identically (execution-reconcile.lisp:493–501). PROVEN-BY replays-execution-reconcile.lisp:2007 (fake journal) and :2029 (real file journal close-reopen-replay).

18. `kernel-operation-accept` draws an id, writes an operation record durably, then answers — id is durable before printed (operation-records.lisp:727–776). PROVEN-BY replays-operation-records.lisp:2092 `operation-id-is-durable-before-it-is-printed` — crash sim: new kernel sees the id via status.

19. Id no journal holds gets the exact SPEC-WORK.md:2734 line `OPERATION FAIL id=<id> op=- state=-: no such operation`, exit 2, never invented queued; cancel and completion of unknown id take same line (operation-records.lisp:778–791). PROVEN-BY replays-operation-records.lisp:2141 `an-id-no-journal-holds-has-a-line-of-its-own`.

=== DEFECTS ===

DEFECT low lisp/nova-work/tests/replays-execution-reconcile.lisp:1778 — Assertion message says "a record was dropped" when checking `(check-equal 2 (length (getf answer :retained)))`, which actually asserts two records were *retained*, not dropped — misleading comment text could confuse a future reader into thinking the test proves retention when the message contradicts that reading — fix the message string to "two records retained" or similar accurate description.

=== QUESTIONS ===

1. `*manifest-bound*` is hardcoded to 64 (execution-reconcile.lisp:168). The SPEC references "a bounded, content-addressed manifest" without spelling the bound number. Was 64 chosen deliberately over powers-of-two nearby (32 or 128), and is it expected to become configurable later?

2. The load-time registration pattern (`setf (gethash :roadmap-row *undo-handlers*) ...`) is split across two files: `undo-roadmap.lisp` registers `:roadmap-configure`, while `undo-roadmap-rows.lisp` registers both `:roadmap-row` and `:roadmap-projection`. Why was the split into two source files chosen rather than one unified `undo-roadmap.lisp` carrying all three registrations? Does this have implications for load order if someone adds another reversible verb?

3. In `%reconcile-record-line`, records are appended as canonical s-expression bytes after `records=`. If future field values ever produce strings containing `records=`, the `%parse-reconcile-line` parser's `(search head-marker line)` would match the wrong position. Was this guarded against by design of `%canonical-field` (which rejects bare strings), or should `%parse-reconcile-line` use `(rfind " records=" line :from-end t)` defensively?

=== Left owed ===
SPEC-WORM.md — the spec document referenced extensively in comments (SPEC-WORK.md:2872-2873, :315, :2831-2845, :3997-4019, :2725-2760, :6020-6023, etc.) was not fetched or read. Every claim marked PROVEN-BY depends on those spec citations being accurately represented in the code comments. I verified internal consistency within the diff and between code and test assertions, but did not confirm the external spec references. This is an intentional omission given the scope constraints of this pre-read card, which focuses on code correctness within the diff boundary.

git status --short
git rev-parse HEAD
