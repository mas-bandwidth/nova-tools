RESULT work4m-E04-F01-25 sha=5298f6be12ea — nova-work E04-F01 criterion E04-F01-25 (sexp id E04-F01-03): Label features, leaves and member grains with revision
DONE
CRITERION E04-F01-03 STATE verified
BRANCH rowan/work4m-E04-F01-25
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/kernel.lisp lisp/nova-work/tests/decide.lisp
SPEC docs/SPEC-WORK.md:1989 — "Units are labelled on every line: `unit=features` or `unit=leaves`; a comparison never changes unit silently."
SPEC docs/SPEC-WORK.md:2104 — "`size` / `size --node X` | total required leaves, done, unknown, unverified, deferred, cancelled, superseded, since-baseline"
SPEC docs/SPEC-WORK.md:2044-2046 — "the count's unit and revision are printed with it, on one named line: the ask is `query --ask size`, and `QUERY OK`'s own `open=<n>`, `unit=<unit>` and `scope=<rev>` are the count, its unit and its revision"
TEST TestE04F01LabelFeaturesLeavesAndMember
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
RED TEST TestE04F01LabelFeaturesLeavesAndMember FAIL spec=docs/SPEC-WORK.md:1989;2104;2044-2046 expected=size-ask-labels-its-count-with-the-leaves-grain;never-a-unit-the-spec-names-nowhere;revision-printed-beside-the-count: the size ask labels its count with the leaves grain, not a unit the spec never names: expected "leaves" got "items"
RED-COUNT NOVA-WORK SLICE1 total=420 pass=411 fail=9
GREEN NOVA-WORK SLICE1 total=420 pass=412 fail=8
CONTROL NOVA-WORK SLICE1 total=420 pass=411 fail=9
CONTROL-FAIL TEST TestE04F01LabelFeaturesLeavesAndMember FAIL spec=docs/SPEC-WORK.md:1989;2104;2044-2046 expected=size-ask-labels-its-count-with-the-leaves-grain;never-a-unit-the-spec-names-nowhere;revision-printed-beside-the-count: the size ask labels its count with the leaves grain, not a unit the spec never names: expected "leaves" got "items"
GIT-STATUS (clean working tree after commit; pre-commit showed only the two paths below)
STATUS  M lisp/nova-work/src/kernel.lisp
STATUS  M lisp/nova-work/tests/decide.lisp
HEAD fdd2b515e67c5367d6bbc32c4d3a5dc7fd96680d
Noticed The `ask-size` form prints `open=<n>` from `wstate-root-open` (the root open-item counter, which counts containers as items — five at the seed) while labelling it `unit=leaves`. The label is now a grain the spec names, but the `open=` value is still the root item count, not the leaf count. The spec's `size` ask is "total required leaves"; reconciling `open=` to the leaf grain is a larger change than this label card names and would break the existing `open-count-is-read-not-computed` assertions that read `open=` off the root counter. Left unpaired deliberately.
Left owed A next card should decide whether `query --ask size`'s `open=` should report leaf grain (via `wstate-leaf-open`) or remain the root open-item counter, and if the former, update `open-count-is-read-not-computed` and any other `ask-size` reader accordingly. The `since-baseline=` (member-grain) and `row-kind=`/`unit=features|epics|work-sets` pluralisation halves of the units bullet also remain unwired in this slice: only the `size` ask prints a `unit=` line today, and no other ask yet emits the four named grains.
