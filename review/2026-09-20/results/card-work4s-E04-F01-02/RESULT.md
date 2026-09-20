RESULT work4s-E04-F01-02 sha=5298f6be12ea — nova-work E04-F01: does the contract say it? criterion E04-F01-02: Count canonical IDs once and exclude references, attempts and history
DONE
CRITERION E04-F01-02 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "canonical" docs/SPEC-WORK.md | head -20 → 20 hits
grep -n "exclud" docs/SPEC-WORK.md | head -20 → many hits (lines 1046, 1882, 1958, 2044, 2117, 2140, 2291, 2314, 3274, 3506-3599)
grep -in "reference\|attempt\|history\|count" docs/SPEC-WORK.md | head -20 → many hits (lines 9-22, 31-36, 44-46, 94, 111-141, 212, 281-283, 330, 365, 373, 392, 401, 407, 511, 565, 588, 590, 592, 599, 663, 718, 740, 744, 776, 899, 1182, 1185, 1407, 1442, 1715, 1738, 1824, 1881, 1891, 1892, 1933, 2097, 2140, 2545, 2559, 2843, 3268, 3344, 3382, 3408, 3648, 3727, 3907, 5283, 5629, 5648)
docs/SPEC-WORK.md:2043-2044: "**The counters count canonical item ids once** and exclude references, attempts and history records; **the count's unit and revision are printed with it, on one named line**: the ask is `query --ask size`, and `QUERY OK`'s own `open=<n>`, `unit=<unit>` and `scope=<rev>` are the count, its unit and its revision, so there is no second line and no second spelling for this number; and **an open linked issue count and an open leaf-task count are separate counters, neither of which is silently labelled `|O|`**. Startup and recovery may reconstruct the counters from the canonical state — an ordinary query may not — and **no promise of constant time is made for an arbitrary new filter**, only for the counters this bullet names."
E04-F01 :by-feature sexp line 86: tests = "open-count-is-read-not-computed; cow-root-partition; indexes-and-counters; findings-across-c-and-o"
open-count-is-read-not-computed → found lisp/nova-work/tests/acceptance/slice-01-reader.lisp:220
cow-root-partition → found lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:259
indexes-and-counters → found lisp/nova-work/tests/replays-8664.lisp:141
findings-across-c-and-o → found lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:314
git status --short → (empty)
Noticed: The sexp subfeature text ("Count canonical IDs once and exclude references, attempts and history") at nova-work.sexp:735 matches almost verbatim the spec text at SPEC-WORK.md:2043-2044 ("count canonical item ids once and exclude references, attempts and history records"). The spec omits "IDs" in favor of "item ids" but the semantic content is identical. All four evidence test names listed in the sexp exist in the tree and are fully accounted for by defname comments or deftest declarations.