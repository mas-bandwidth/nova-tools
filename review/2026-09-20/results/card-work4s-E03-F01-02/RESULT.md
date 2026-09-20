RESULT work4s-E03-F01-02 sha=5298f6be12ea — nova-work E03-F01: does the contract say it? criterion E03-F01-02: Validate multi-node changes all-or-none
DONE
CRITERION E03-F01-02 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "Validate multi-node changes all-or-none" docs/SPEC-WORK.md — 0 hits
grep -in "multi-node" docs/SPEC-WORK.md — 1 hit
grep -n "all.or.none" docs/SPEC-WORK.md — 10 hits
grep -n "multi.node.change\|multi-node" docs/SPEC-WORK.md — 1 hit
grep -n "changes all-or-none\|validate.*all.or.none\|all.or.none.*change\|multi.node.*all" docs/SPEC-WORK.md — 1 hit
grep -n "atomic.*validated\|validated.*envelope\|atomic.*envelope" docs/SPEC-WORK.md — 7 hits
grep -n "validate" docs/SPEC-WORK.md | grep -i "multi\|node\|change\|all\|none" — 0 hits
grep -n "multi.*node.*atomic\|atomic.*multi.*node\|single.*envelope\|atomically" docs/SPEC-WORK.md — 13 hits
docs/SPEC-WORK.md:2917 — "a multi-node change is one atomic validated envelope"
E03-F01 sexp :evidence — "PR300@d21d65f0: internal two-event envelope and fake-journal ordering; durable journal/transport dedup remains incomplete; 26/26 subset tests, no full feature verification"
E03-F01 sexp :tests — "journal-records-before-it-applies; two-event-candidate-is-all-or-none; durable-journal-multi-event-envelope-never-partly-publishes; durable-journal-changed-payload-refuses; dedup-refuses-past-its-bound"
Test "journal-records-before-it-applies" — IN TREE lisp/nova-work/tests/acceptance/slice-02-close-and-counters.lisp:6
Test "two-event-candidate-is-all-or-none" — IN TREE lisp/nova-work/tests/acceptance/slice-01-reader.lisp:324
Test "durable-journal-multi-event-envelope-never-partly-publishes" — IN TREE lisp/nova-work/tests/acceptance/slice-03-containers.lisp:180
Test "durable-journal-changed-payload-refuses" — IN TREE lisp/nova-work/tests/acceptance/slice-03-containers.lisp:158
Test "dedup-refuses-past-its-bound" — IN TREE lisp/nova-work/tests/acceptance/slice-02-close-and-counters.lisp:117
git status --short — (empty)
Noticed: The sexp records 5 test names for E03-F01 but :verified 3 :total 3. The test names themselves are all in the tree and findable by grep. The "multi-node" string appears exactly once in the contract (line 2917), in the "The verbs" section; the contract does not use the phrase "all-or-none" directly on that line but "one atomic validated envelope" is semantically equivalent — an atomic envelope is accepted or refused whole, which is all-or-none.