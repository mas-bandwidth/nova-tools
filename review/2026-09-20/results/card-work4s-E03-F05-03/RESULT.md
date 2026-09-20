RESULT work4s-E03-F05-03 sha=5298f6be12ea — nova-work E03-F05: does the contract say it? criterion E03-F05-03: Preserve original history and refuse irreversible or uncertain effects
DONE
CRITERION E03-F05-03 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "Preserve original history" docs/SPEC-WORK.md | head -20  ->  0 hits (roadmap wording, not spec wording)
grep -in "irreversible" docs/SPEC-WORK.md | head -20  ->  2 hits (2847, 2927)
grep -in "uncertain" docs/SPEC-WORK.md | head  ->  10+ hits (489, 490, 1215, 1729, 1730, 2111, 2705, 2804, 2810, 2847, ...)
grep -in "preserve" docs/SPEC-WORK.md | head -20  ->  20+ hits
grep -in "history" docs/SPEC-WORK.md | head -20  ->  20+ hits
grep -in "checkpoint" docs/SPEC-WORK.md | head  ->  hits
grep -n "not reversible here" docs/SPEC-WORK.md | head  ->  5 hits (2861 table header, 2904, 5990, 6536, ...)
grep -cni "Preserve original history" docs/SPEC-WORK.md  ->  0
grep -cni "irreversible" docs/SPEC-WORK.md  ->  2
grep -cni "uncertain operation is refused" docs/SPEC-WORK.md  ->  1
grep -cni "original event stays" docs/SPEC-WORK.md  ->  1
grep -cni "not reversible here" docs/SPEC-WORK.md  ->  5
grep -n "E03-F05" docs/roadmaps/nova-work.sexp | head -10  ->  2 hits (85 by-feature, 719 feature definition)
grep -rn "undo-appends-and-preserves\|redo-refuses-a-stale-plan\|edit-undo-preserves-later-work\|undo-refuses-an-external-effect\|undo-names-its-reversible-set" lisp/nova-work/tests/ | head -10  ->  hits
docs/SPEC-WORK.md:2847  "outcome is known, and **a generic undo of an irreversible or uncertain operation is refused**."
docs/SPEC-WORK.md:2833-2834  "provenance for the engine to build a **typed compensating envelope**: the original event stays / exactly where it is, the reversal is appended with its lineage"
docs/SPEC-WORK.md:2844-2846  "currently support and can never erase that they happened. **A sent message, a paid execution, a / publication and a source deletion are not undone by rewinding local state** — they are reported as / external effects with their own compensating workflow"
docs/SPEC-WORK.md:2848  "**Resetting shared Git history is never the undo mechanism**"
docs/SPEC-WORK.md:2927  "| reversible mistakes | undo-plan/undo and redo-plan/redo of named requests; appending compensating history and refusing conflicting or irreversible effects |"
BY-FEATURE E03-F05 tests (docs/roadmaps/nova-work.sexp:85) and tree presence:
  undo-appends-and-preserves      -> IN TREE  lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp:362 (deftest)
  redo-refuses-a-stale-plan       -> IN TREE  lisp/nova-work/tests/acceptance/slice-07-replays-mid.lisp:6 (deftest)
  edit-undo-preserves-later-work  -> IN TREE  lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp:1892 (deftest; an earlier deftest-pending at :852 was superseded)
  undo-refuses-an-external-effect -> IN TREE  lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp:1676 (deftest)
  undo-names-its-reversible-set   -> IN TREE  lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp:1779 (deftest)
git status --short: (empty)
Noticed: the E03-F05 :feature definition (docs/roadmaps/nova-work.sexp:719) still says :state "missing" with :evidence () while the :by-feature row (:verified 3 :total 3) and all five named tests are in the tree — the feature definition's state/evidence appears stale relative to the by-feature record. Base HEAD confirmed 5298f6be12eaa0f7e6622334d2b6a1eb427649e3; read-only, nothing changed.