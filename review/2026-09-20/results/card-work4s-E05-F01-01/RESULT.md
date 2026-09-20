RESULT work4s-E05-F01-01 sha=5298f6be12ea — nova-work E05-F01: does the contract say it? criterion E05-F01-01: Bind pointer, criterion, against revision and generation to evidence
DONE
CRITERION E05-F01-01 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
SEARCH grep -n "pointer criterion revision generation evidence" docs/SPEC-WORK.md | head -20 (0 hits)
SEARCH grep -in "bind pointer" docs/SPEC-WORK.md | head -20 (0 hits)
SEARCH grep -n "evidence" docs/SPEC-WORK.md | head -20 (20 hits)
SEARCH grep -n "generation" docs/SPEC-WORK.md | head -20 (13 hits)
SEARCH grep -n "against.*revision" docs/SPEC-WORK.md | head -10 (10 hits)
CONTRACT docs/SPEC-WORK.md:996-998
  `:evidence` — `:pointer`, `:criterion` (an `:acceptance` id on the node), `:against <sha>` (the tree it was read against), `:generation` (the node's, at the time of writing), `:attempt` (optional).
CONTRACT docs/SPEC-WORK.md:548-550
  **per node** — its derived state, generation, scope revision, source revision, lease state, and, **for every event of its evidence set, that event's five fields: `:pointer`, `:criterion`, `:against`, `:stamp` and `:generation**"
SEXP-EVIDENCE verify-job-criterion-reads-the-revision -> FOUND lisp/nova-work/tests/acceptance/slice-13-verifier.lisp:140
SEXP-EVIDENCE verify-resolver-identity-is-the-command -> FOUND lisp/nova-work/tests/acceptance/slice-13-verifier.lisp:214
SEXP-EVIDENCE a-removed-or-corrected-need-is-unmet -> FOUND lisp/nova-work/tests/replays-785-gate.lisp:415
SEXP-EVIDENCE regression-opens-repair-work -> FOUND lisp/nova-work/tests/replays-8648.lisp:24
SEXP-EVIDENCE-CONTAINER (:feature "E05-F01" ... :evidence ()) — no evidence recorded in sexp
GIT-STATUS (empty)
Noticed The roadmap says "against revision" and the spec says ":against <sha>"; these are the same concept — the sha the evidence was read against is the revision. All four test names listed in the sexp's :by-feature entry for E05-F01 exist in the tree.