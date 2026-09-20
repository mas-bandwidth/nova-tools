RESULT work4s-E04-F01-03 sha=5298f6be12ea — nova-work E04-F01: does the contract say it? criterion E04-F01-03: Label features, leaves and member grains with revision
DONE
CRITERION E04-F01-03 SPEC PARTIAL

REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "label\|Label" docs/SPEC-WORK.md → 27 hits
grep -in "feature.*grain\|grain.*feature\|leaf.*grain\|member grain\|member grain" docs/SPEC-WORK.md → 0 hits
grep -n "grain" docs/SPEC-WORK.md → 15 hits
grep -n "scope-revision\|source-revision" docs/SPEC-WORK.md → 5 hits

docs/SPEC-WORK.md:548 — "- **per node** — its derived state, generation, scope revision, source revision, lease state,"
(Features are nodes. This line gives them `scope revision` and `source revision`.)

PARTIAL: clause "leaves" has no contract. Clause "member grains" has no contract. The spec labels features with revision (as per-node data at line 548), but no line states that leaves carry a revision label, and no line states that roadmap axis members ("member grains") carry a revision label. The phrase "member grain" does not appear anywhere in SPEC-WORK.md. "Leaf grain" appears at lines 1963, 1976, 1978 referring to counting granularity, not entity labeling.

E04-F01 :by-feature tests from sexp line 86:
  open-count-is-read-not-computed → EXISTS (slice-01-reader.lisp:220)
  cow-root-partition → EXISTS (slice-04-doing-and-journal.lisp:259)
  indexes-and-counters → EXISTS (replays-8664.lisp:141)
  findings-across-c-and-o → EXISTS (slice-05-durable-journal.lisp:314)
All four test names exist in the tree. No stale evidence found.

git status --short:
?? repo/

Noticed: SPEC-WORK.md:1342 defines `:source-revision` as a field on repository work-set nodes specifically. Roadmaps carry `:scope-revision` (line 901). But there is no rule stating that a leaf task or a roadmap axis member carries any revision field — only that closed-index rows record each settle/revive's item "its scope and source revisions" (line 595), which is a historical record about the closure event, not a live label on the leaf or member itself.
