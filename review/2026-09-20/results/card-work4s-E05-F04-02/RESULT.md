RESULT work4s-E05-F04-02 sha=5298f6be12ea — nova-work E05-F04: does the contract say it? criterion E05-F04-02: Keep merged fix, verified behavior and published distribution separate
DONE
CRITERION E05-F04-02 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n 'merged fix' docs/SPEC-WORK.md | head -20 → 2 hits
grep -in 'verified behaviour' docs/SPEC-WORK.md | head -20 → 1 hit
grep -n 'published distribution' docs/SPEC-WORK.md | head -20 → 1 hit
grep -in 'separate\|separation\|keep.*separate' docs/SPEC-WORK.md | head -20 → 22 hits (includes relevant lines)
grep -in 'evidence obligation' docs/SPEC-WORK.md | head -20 → 2 hits
docs/SPEC-WORK.md:7204-7206: "- **`shared-prerequisite-owned-once`** — a shared prerequisite owned once and referenced by every affected cell, with integration and release gates beside feature completion: **a merged fix, verified behaviour and a published distribution are three evidence obligations**."
docs/SPEC-WORK.md:2085-2086: "still in O, because **a merged fix is not a distributed one** (Glenn, 23:36Z: *a merged fix can be C while the separately tracked release task remains O; do not call distributed just because merged*)"
docs/SPEC-WORK.md:6295-6296: Feature **`merged-is-not-distributed`** — a fix in C with `landed=<sha>` and `released=-` while its release task is open, and `released=<version>` on the same row once that task settles.
:by-feature E05-F04 tests: "parent-green-needs-dependencies; merged-is-not-distributed"
parent-green-needs-dependencies: EXISTS at lisp/nova-work/tests/acceptance/slice-11-dependencies.lisp:127
merged-is-not-distributed: EXISTS at lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp:6
sexp :evidence for E05-F04: () (empty — no stale evidence entries)
git status --short: (empty)
Noticed The spec expresses the criterion as "three evidence obligations" rather than explicitly saying "keep ... separate", but enumerating them as distinct obligations achieves the same semantic result. Additionally, line 2085 reinforces this by stating "a merged fix is not a distributed one" in the context of the `released=` field, and replay event `merged-is-not-distributed` is named at line 2089. The sexp tracks E05-F04 with :verified 2 :total 3 (one sub-feature untested), and both test names from the summary list exist in the tree.
