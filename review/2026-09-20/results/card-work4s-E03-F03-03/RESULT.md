RESULT work4s-E03-F03-03 sha=5298f6be12ea — nova-work E03-F03: does the contract say it? criterion E03-F03-03: Keep shared prerequisites singly owned and referenced
DONE
CRITERION E03-F03-03 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
SEARCH grep -n "shared prerequisites" docs/SPEC-WORK.md | head -20 → 4 hits (lines 5684, 7083, 7780)
SEARCH grep -in "singly owned" docs/SPEC-WORK.md | head -20 → 0 hits
SEARCH grep -n "prerequisite" docs/SPEC-WORK.md | head -20 → 10+ hits (lines 2915, 5399, 5539, 5684, 6498, 7083, 7204, 7780, 7802)
SEARCH grep -n "owned once" docs/SPEC-WORK.md | head -20 → 2 hits (lines 882, 7204)
SEARCH grep -in "referenced" docs/SPEC-WORK.md | head -20 → 10+ hits (lines 879, 884, 1136, 1347, 1578, 2386, 2411, 5496, 5543, 5544, 5567, 6497, 6577, 7204)
SEARCH grep -n "shared-prerequisite-owned-once" docs/SPEC-WORK.md | head -20 → 1 hit (line 7204)
SEARCH grep -n "keep shared prerequisites singly owned and referenced" docs/SPEC-WORK.md | head -20 → 0 hits
CONTRACT docs/SPEC-WORK.md:7204:
  - **`shared-prerequisite-owned-once`** — a shared prerequisite owned once and referenced by every
    affected cell, with integration and release gates beside feature completion: **a merged fix,
    verified behaviour and a published distribution are three evidence obligations**.
ROADMAP ROADMAP.md:405:
  - [x] Keep shared prerequisites singly owned and referenced
SEXP-DEFN docs/roadmaps/nova-work.sexp:694-700:
  (:feature "E03-F03" :title "Scope, baseline and dependency changes"
    :subfeatures ("Support baseline, discovery, require, dependency add/remove and prioritize"
                  "Record author, reason, scope revision and exact member delta"
                  "Keep shared prerequisites singly owned and referenced")
    :depends-on ("E03-F02") ... :state "missing" :evidence ())
SEXP-EVIDENCE docs/roadmaps/nova-work.sexp:83:
  (:feature "E03-F03" :verified 1 :total 3 :tests "shared-prerequisite-owned-once")
TEST-IN-TREE shared-prerequisite-owned-once → lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp:21 (deftest), lisp/nova-work/tests/replays-8650.lisp:46 (deftest) — both present
GIT-STATUS (empty)
Noticed The sexp :evidence for the E03-F03 feature definition (line 694) is empty, but the :by-feature entry (line 83) records :tests "shared-prerequisite-owned-once" with :verified 1 :total 3. The two tests named `shared-prerequisite-owned-once` exist in the tree (slice-08-replays-late.lisp:21 and replays-8650.lisp:46) referencing SPEC-WORK.md:5719 and SPEC-WORK.md:6365 respectively, while the spec rule is now at docs/SPEC-WORK.md:7204 — the line-number references in the test definitions have drifted from the current spec.