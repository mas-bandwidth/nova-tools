RESULT work4s-E04-F03-02 sha=5298f6be12ea — nova-work E04-F03: does the contract say it? criterion E04-F03-02: Report open plus closed totals without double membership
DONE
CRITERION E04-F03-02 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "double membership" docs/SPEC-WORK.md — 0 hits
grep -in "open plus closed" docs/SPEC-WORK.md — 0 hits
grep -in "open.*closed.*total" docs/SPEC-WORK.md — 2 hits (lines 5814, 6275)
grep -in "partition" docs/SPEC-WORK.md — 52 hits
grep -n "cow-root-partition" docs/SPEC-WORK.md — 2 hits (lines 1799, 6246)
grep -n "E04-F03" docs/roadmaps/nova-work.sexp — 2 hits (lines 88, 754)
grep -rn "cow-root-partition" lisp/nova-work/tests/ — 1 hit (acceptance/slice-04-doing-and-journal.lisp:259)
grep -rn "revive-appends-and-counts-latest" lisp/nova-work/tests/ — 4 hits (replays-8660.lisp:6,21,24,...)
grep -rn "closed-rows-carry-revived-and-settles" lisp/nova-work/tests/ — 1 hit (acceptance/slice-01-reader.lisp:456)
grep -rn "activity-and-state-are-two-counts" lisp/nova-work/tests/ — 1 hit (acceptance/slice-04-doing-and-journal.lisp:300)
docs/SPEC-WORK.md:5814: "The root is a partition: `open=` and `closed=` sum to the scope's counted total and no id is counted twice, so the arithmetic is guarded by a finding rather than repaired at read time" (rule 18, "in two branches")
docs/SPEC-WORK.md:1796-1799: "**Each id is counted once and printed once**: `open=<n>` and `closed=<n>` partition the scope's counted ids, their sum is the scope's counted total, `closed-in=<n>` is the part of `closed=` whose settle stamp falls inside the window, and an id in both branches is a finding by rule 18 and not an arithmetic to be tidied at read time (replay `cow-root-partition`)."
docs/SPEC-WORK.md:2009-2011: "- **The branch counts are the root's own and partition the scope**: `open=<n>` is the scope's counted items still in O, `closed=<n>` those in C, and **their sum is the scope's counted total with no id in both**, which rule 18 makes a finding rather than an arithmetic tidied at read time."
docs/SPEC-WORK.md:6246: "- **`cow-root-partition`** — one id is in C or in O and never in both; `open=` plus `closed=` equals the scope's counted total on every ask; an event hand-written to put one id in both is a rule 18 finding at load and refused at the candidate gate."
:by-feature E04-F03 test names (docs/roadmaps/nova-work.sexp:88): cow-root-partition; revive-appends-and-counts-latest; closed-rows-carry-revived-and-settles; activity-and-state-are-two-counts — all four are in the tree (cow-root-partition: acceptance/slice-04-doing-and-journal.lisp:259; revive-appends-and-counts-latest: replays-8660.lisp:24; closed-rows-carry-revived-and-settles: acceptance/slice-01-reader.lisp:456; activity-and-state-are-two-counts: acceptance/slice-04-doing-and-journal.lisp:300)
git status --short: (empty)
Noticed: the sexp's per-feature detail entry for E04-F03 (docs/roadmaps/nova-work.sexp:754-763) records :state "missing" :evidence () while the :by-feature summary on line 88 records :verified 2 :total 3 with the same test names, and ROADMAP.md:483 shows "- [x] Report open plus closed totals without double membership" — an internal inconsistency between the summary and the detail entry, not evidence against the contract.