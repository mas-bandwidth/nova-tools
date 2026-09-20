RESULT work2-E08-F01-02 sha=a3abdd4ad6dd — nova-work E08-F01 acceptance criterion, criterion E08-F01-02 (docs/roadmaps/nova-work.sexp): Provide bounded family/verb help and machine discovery with schema hash
DONE
CRITERION E08-F01-02 STATE unmet

BRANCH rowan/work2-E08-F01-02
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-notes.lisp

SPEC-WORK line found: docs/SPEC-WORK.md:2664-2666 — "spelling open; it is pinned by **one generated
schema file** (every verb with its op, event kind, ordered fields and grammar line) and **one
coverage test** over it". This is the schema the criterion's "family/verb help" reads and whose
"schema hash" is the digest that lets a client refuse a stale discovered copy. The three exact
words of the criterion ("schema hash", "family/verb help", "machine discovery") do not appear
verbatim anywhere in docs/SPEC-WORK.md; :2664 is the closest contractual line, and the `help`
verb existence is only recorded at docs/SPEC-WORK.md:2347, the fleet (machine) listing at
docs/SPEC-WORK.md:3557-3584.

TEST: TestE08F01ProvideBoundedFamilyVerbHelp (in lisp/nova-work/tests/replays-notes.lisp).

Suite counts (two runs, identical):
  NOVA-WORK SLICE1 total=408 pass=399 fail=9
  NOVA-WORK SLICE1 total=408 pass=399 fail=9

My test is RED. It fails with: "expected verb :STATE-TO-DONE to belong to a named family for
bounded family/verb help, but the schema groups no family" (and would equally fail on the
missing :schema-hash). The kernel's verb schema today is the flat *mutation-grammar* — three
mutation verbs each with an event kind and ordered fields, but no family grouping, no help
listing, no schema hash, and no hash on a machine row. So the criterion is genuinely UNMET, and
the roadmap row is correctly left unticked.

NOTE on the other 8 failures in the run: they are pre-existing and environmental, not caused by
this card — `endpoint-is-local-and-private` ("Socket error in bind: 13 (Permission denied)") and
the seven `request-line.lisp` socket tests ("Can't create directory /tmp/nova-work-reqline-…"), all
due to the sandbox denying /tmp socket bind and directory creation. My test is in-memory and
touches no /tmp or socket.

git status --short (before commit, exactly one path):
  M lisp/nova-work/tests/replays-notes.lisp

head = 565600a97869210de77f7dde3e274c177f417384

Left owed: none beyond this card — one test landed, naming docs/SPEC-WORK.md:2664, proving the
criterion unmet. The criterion remains open work for whoever cuts E08-F01.
