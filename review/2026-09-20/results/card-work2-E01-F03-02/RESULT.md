RESULT work2-E01-F03-02 sha=a3abdd4ad6dd — nova-work E01-F03 acceptance criterion, criterion E01-F03-02 (docs/roadmaps/nova-work.sexp): Keep containment as a forest and references as a separate graph
DONE
CRITERION E01-F03-02 STATE verified
BRANCH rowan/work2-E01-F03-02
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/decide.lisp
SPEC-WORK line: docs/SPEC-WORK.md:876-881 — "Containment and reference are two different edges… :children is canonical containment: every node has at most one containment parent, the containment edges form a forest, and a node is counted once, under that parent… :deps … are references: they form a graph, they carry no count and no cost". (Key phrases: "the containment edges form a forest" at :878; "they form a graph, they carry no count" at :880.)
TEST: TestE01F03KeepContainmentAsAForest (in lisp/nova-work/tests/decide.lisp)
SUITE run 1: NOVA-WORK SLICE1 total=408 pass=400 fail=8
SUITE run 2: NOVA-WORK SLICE1 total=408 pass=400 fail=8
Both count lines agree; the suite is not order-dependent. The 8 failures are pre-existing and environmental: each is a request-line test failing with "Can't create directory /tmp/nova-work-reqline-…" because /tmp is not writable in this sandbox (touch /tmp/… → Permission denied). They are unrelated to this card; my test is a pure in-memory seed and does not touch /tmp.
MY TEST: GREEN. TestE01F03KeepContainmentAsAForest PASS spec=docs/SPEC-WORK.md:876-881.
MEANING: the kernel already keeps containment as a forest (one containment parent per node, counted once under that parent) and references as a separate graph (:deps reverse edges carrying no count). The E01-F03-02 roadmap row is verified; it can be ticked.
git status --short (after commit): (clean)
head 960dc7e0b3d1fcc3b7f01cd27fea9b11d6e06b8c
Left owed:
