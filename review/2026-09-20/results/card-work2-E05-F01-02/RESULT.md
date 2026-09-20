RESULT work2-E05-F01-02 sha=a3abdd4ad6dd — nova-work E05-F01 acceptance criterion, criterion E05-F01-02 (docs/roadmaps/nova-work.sexp): Require matching criterion kind, exact subject and predicate
DONE
CRITERION E05-F01-02 STATE unmet
BRANCH rowan/work2-E05-F01-02
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8660.lisp

SPEC-WORK: docs/SPEC-WORK.md:936-937 — "an evidence pointer qualifies a criterion only when its kind matches, **its subject is the criterion's subject** (a passing test of another name qualifies nothing)"; qualification gate at :1281-1288, kind/subject/predicate derived at read time at :1330.

TEST: TestE05F01RequireMatchingCriterionKindExact (in lisp/nova-work/tests/replays-8660.lisp)

RUN 1: NOVA-WORK SLICE1 total=408 pass=399 fail=9
RUN 2: NOVA-WORK SLICE1 total=408 pass=399 fail=9
(suite is stable; the two counts agree)

TEST RESULT: RED. The gate `verify-qualifies-p` (lisp/nova-work/src/verifier.lisp:247) enforces the matching-kind half — it admits a `test:` pointer for a `:test` criterion and rejects a `run:` pointer — but it never compares the evidence subject to the criterion's subject, so a `test:` pointer naming a different test (another name) still qualifies, returning T where the spec requires NIL. The failing assertion: "a passing test of another name qualifies no :test criterion: expected NIL got T".

ROADMAP MEANING: the E05-F01-02 row is unverified because the criterion is genuinely unmet, not merely unlanded. The exact-subject (and predicate) dimensions are not enforced by the qualification gate; only matching kind is.

git status --short:
 M lisp/nova-work/tests/replays-8660.lisp

head 887e9fda286f48a106c5c624c84bd446b1553d69

Left owed: none — the test is committed at the recorded head, names its SPEC-WORK line (936-937), and is the RED proving the criterion unmet.
