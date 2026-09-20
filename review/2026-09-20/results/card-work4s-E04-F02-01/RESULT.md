RESULT work4s-E04-F02-01 sha=5298f6be12ea — nova-work E04-F02: does the contract say it? criterion E04-F02-01: Compute green feature cells over applicable rows per axis member
DONE
CRITERION E04-F02-01 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "green feature cells" docs/SPEC-WORK.md | head -20 → 5 hits
grep -n "feature cells" docs/SPEC-WORK.md | head -20 → 4 hits
grep -n "applicable rows" docs/SPEC-WORK.md | head -20 → 16 hits
grep -in "compute.*green" docs/SPEC-WORK.md | head -20 → 0 hits
grep -n "per axis" docs/SPEC-WORK.md | head -20 → 2 hits
docs/SPEC-WORK.md:1935: - **Language completion** on a roadmap = `100 * green feature cells / applicable feature rows` for that axis member, where applicable rows are the live rows less those the axis member has a recorded out-of-scope event for. *Partial cells do not contribute fractions of a completed feature to this number* (5653970526). **The divisor of that division prints under its own name**: `percent` prints `green=<k> applicable=<n> rows=<n> baseline-rows=<n0>`, where `applicable=` is the divisor above, `rows=` is the roadmap's required-set cardinality and `baseline-rows=` is the membership its last `:baseline` recorded, so a new denominator is visible beside the old (5649089106) and the divisor beside both. **`applicable=` is per axis member; `rows=` and `baseline-rows=` are the roadmap's and are the same under every `--axis`**, so nine beside ten reads as one row out of scope for one member and never as a row removed. **`percent` over zero applicable rows prints `green=0 applicable=0` and no percentage**, because a percentage of nothing is not zero, and exits 0.
docs/roadmaps/nova-work.sexp:87:      (:feature "E04-F02" :verified 3 :total 3 :tests "percent-axis-on-a-matrix")
docs/roadmaps/nova-work.sexp:743:        (          :id "E04-F02"
docs/roadmaps/nova-work.sexp:745:          :subfeatures (            "Compute green feature cells over applicable rows per axis member"
docs/roadmaps/nova-work.sexp:752:          :state "missing"
docs/roadmaps/nova-work.sexp:753:          :evidence ()
Test names from sexp :by-feature for E04-F02: "percent-axis-on-a-matrix" → EXISTS at lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp:519 (deftest at line 522).
Note: sexp line 752 shows :state "missing" but ROADMAP.md:470 marks the row [x] done. The sexp line 87 header row shows :verified 3 :total 3 which disagrees with the :by-feature body :state "missing" and empty :evidence.
git status --short 
Noted: The sexp body (:by-feature) at lines 743-753 shows E04-F02 as :state "missing" with empty :evidence, while the header row at line 87 claims :verified 3 :total 3 and road-map.md:470 has [x]. This inconsistency between the sexp body and the header/ROADMAP.md is a separate finding from the criterion-verdict task. The contract (SPEC-WORK.md:1935-1948) does fully state E04-F02-01 — computing green feature cells over applicable rows per axis member — via the Language completion definition and percent output specification in the Counting section.
