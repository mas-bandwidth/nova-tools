RESULT work2-E07-F06-01 sha=a3abdd4ad6dd — nova-work E07-F06 acceptance criterion, criterion E07-F06-01 (docs/roadmaps/nova-work.sexp): Omit private nodes and descendants from rendered output files
DONE
CRITERION E07-F06-01 STATE unmet

BRANCH rowan/work2-E07-F06-01
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-concurrency-witness.lisp

SPEC-WORK line (docs/SPEC-WORK.md:947-949):
  "A node may carry `:private true`; `render` never writes a private node or
   its descendants into an output file, and a public view that reaches one
   through a parent prints `private=<n>` and nothing of it (5653982211)."

TEST: TestE07F06OmitPrivateNodesAndDescendants (added to replays-concurrency-witness.lisp)

Suite counts (both runs identical, order-stable):
  NOVA-WORK SLICE1 total=408 pass=399 fail=9
  NOVA-WORK SLICE1 total=408 pass=399 fail=9

Test result: RED. The test constructs a zero-axis view whose :private list names
the node "acme/work/secret" and whose members include its descendant
"acme/work/secret/child". render-view emits "row=acme/work/secret/child" — the
descendant is written to the rendered output even though only its parent is
named private. Meaning for the roadmap row: E07-F06-01 is UNMET in its
"descendants" half — the render filters private members by exact id match
(roadmap.lisp render-view-body / r8621-render-body) but computes no descendant
closure and prints no `private=<n>` for a node reached through a private
ancestor. The kernel is not yet verified and the test is the RED that proves it.

(The 8 other failures in the suite are pre-existing environment failures in the
request-line tests — "Can't create directory /tmp/nova-work-reqline-…" — unrelated
to this card's single-file change.)

git status --short: (clean; the only touched file was committed)
  lisp/nova-work/tests/replays-concurrency-witness.lisp

head 162f00ef771be6527c38e2ead818c5ab639b4958

Left owed: none — criterion assessed, RED test landed, not repaired (production
code is out of scope for this verify card).
