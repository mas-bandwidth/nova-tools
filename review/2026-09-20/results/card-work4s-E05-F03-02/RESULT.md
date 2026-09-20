RESULT work4s-E05-F03-02 sha=5298f6be12ea — nova-work E05-F03: does the contract say it? criterion E05-F03-02: Support attested criteria with reviewer identity and result
DONE
CRITERION E05-F03-02 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "attested" docs/SPEC-WORK.md | head -20 → 9 hits
grep -in "reviewer identity|reviewer.*result|identity.*reviewer|crit.*review" docs/SPEC-WORK.md | head -20 → 13 hits
grep -n "criteria|criterion" docs/SPEC-WORK.md | head -20 → 19 hits
docs/SPEC-WORK.md:932: `(:id "c1" :kind :test :subject "test:internal/lockfile/TestLockRule1@<rev>" :predicate :passes)`, where `:kind` is `:test`, `:job`, `:merged` or `:attested`, `:subject` names the
docs/SPEC-WORK.md:934: `:attested` the criterion text a reviewer signs), and `:predicate` is what must be true of it
docs/SPEC-WORK.md:935: (`:passes`, `:succeeds`, `:merged-at`, `:attested-by`); `node add --acceptance` takes exactly
docs/SPEC-WORK.md:1005: - `:review-attest` — a reviewer's attestation that a result satisfies an `:attested`
docs/SPEC-WORK.md:1006: criterion: `:criterion`, `:result` (a pointer), `:against <sha>`, `:generation` (the
docs/SPEC-WORK.md:1007: node's, at the time of writing), `:by` the reviewer. **It is an evidence event**: it
docs/SPEC-WORK.md:1008: carries `:pointer` = its `:result` and is what a `:to :done` names for an `:attested`
docs/SPEC-WORK.md:1009: criterion, under the same generation rule as every other evidence event.
docs/SPEC-WORK.md:1285: sha; an `:attested` criterion only by a `:review-attest` event naming the reviewer, the
docs/SPEC-WORK.md:1286: criterion, the result pointer and the revision (a `note:` or a bare `commit:`/`file:` never
E05-F03 `:by-feature` entry from nova-work.sexp:
  Tests listed: "review-cycles-stay-visible; an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less; reconcile-preserves-contradiction; regression-opens-repair-work; reuse-only-valid-review"
  review-cycles-stay-visible → PRESENT in lisp/nova-work/tests/replays-8648.lisp
  an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less → PRESENT in lisp/nova-work/tests/replays-785-gate.lisp
  reconcile-preserves-contradiction → PRESENT in lisp/nova-work/tests/replays-8640.lisp
  regression-opens-repair-work → PRESENT in lisp/nova-work/tests/replays-8648.lisp
  reuse-only-valid-review → PRESENT in lisp/nova-work/tests/replays-8648.lisp
git status --short → (empty)
Noticed: The spec defines `:attested-by` as a valid predicate (line 935), and the `:review-attest` event schema explicitly carries both `:by` (the reviewer's name — reviewer identity) and `:result` (a pointer — the result), plus the generation-bound `:criterion`. All three clauses of the roadmap row ("attested criteria", "reviewer identity", "result") are stated in the contract.
