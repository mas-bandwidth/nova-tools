RESULT work4s-E03-F03-01 sha=5298f6be12ea — nova-work E03-F03: does the contract say it? criterion E03-F03-01: Support baseline, discovery, require, dependency add/remove and prioritize
DONE
CRITERION E03-F03-01 SPEC PARTIAL
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "baseline" docs/SPEC-WORK.md | head -20 → 34 hits
grep -in "discovery" docs/SPEC-WORK.md | head -20 → 37 hits
grep -in "require" docs/SPEC-WORK.md | head -20 → 37 hits
grep -in "dependency" docs/SPEC-WORK.md | head -20 → 27 hits
grep -n "add/remove" docs/SPEC-WORK.md | head -20 → 1 hit
grep -n "prioritise\|prioritize" docs/SPEC-WORK.md | head -20 → 10 hits
docs/SPEC-WORK.md:2929 "| scope and dependencies | baseline, discovery, dependency add/remove, prioritise, defer, cancel, reopen, supersede |" — REQUIRED COORDINATOR OPERATIONS table listing these as needed operations
docs/SPEC-WORK.md:1090 "- `:baseline`, `:discovery`, `:remove`, `:require`, `:defer`, `:cancel` (carrying ..." — scope log event kinds
docs/SPEC-WORK.md:1141 "| `:baseline` | sets the set to the members it records | the event's node's own | `event` |" — delta table entry
docs/SPEC-WORK.md:1142 "| `:discovery` | adds the named member at the bottom of the listing | the event's node's own | ..." — delta table entry
docs/SPEC-WORK.md:1147 "| `:require` | with `:to false` removes the event's node from the set; with `:to true` adds it at the bottom of the listing, as a `:discovery` does. **It detaches nothing** ... | its containment parent's own | `node require` only |" — delta table entry
Clause with no contract: **dependency add/remove** — no verb or event kind exists for adding or removing a `:deps` reference between nodes. The spec covers `:deps` (reverse-dependency index) on lines 2117–2118 but defines no mutation operation (no `node dep-add`, no `node dep-remove`, no event kind `:dep-add`/`:dep-remove`). "Dependency add/remove" maps neither to `:require` (which manages required-member membership) nor to `:remove` (which detaches from containment); it is a distinct concept: managing the reference-edges in a node's `:deps` field. All other items in the criterion are covered by contract: baseline (`event --kind :baseline`, line 1141), discovery (`event --kind :discovery`, line 1142), require (`node require` / `:require`, line 1147), prioritise (`prioritise` verb, lines 2331, 3167–3177), defer (`event --kind :defer`), cancel (`event --kind :cancel`), reopen (`event --kind :reopen`), supersede (`event --kind :supersede`).
:Evidence in sexp: E03-F03 :evidence () (empty). :by-feature test name recorded at line 83 of nova-work.sexp: "shared-prerequisite-owned-once".
Test "shared-prerequisite-owned-once": EXISTS at lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp:21, but is a stub — contains `(ok t "slice 1 carries no prerequisites: NEEDS-KERNEL prerequisite ownership")` on line 24; test does NOT exercise any of the five scoped operations.
git status --short → (empty)
Noticed: the sexp entry for E03-F03 (line 703) has :state "missing" and :evidence (). The ROADMAP.md lists three sub-items under E03-F03; only the third ("Keep shared prerequisites singly owned and referenced") is checked [x]. The sexp line-count shows E03-F03 verified=1/total=3, matching the roadmap checkmark count. The sole evidence test targets the prerequisite-ownership concern (the third sub-item), not the first sub-criterion (E03-F03-01) which remains entirely unverified.
