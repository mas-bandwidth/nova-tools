RESULT work2-E01-F04-04 sha=a3abdd4ad6dd — nova-work E01-F04 acceptance criterion, criterion E01-F04-04 (docs/roadmaps/nova-work.sexp): Represent project and stream groupings as work-set categories without inferring kind from title or position
DONE
CRITERION E01-F04-04 STATE verified
BRANCH rowan/work2-E01-F04-04
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8640.lisp
SPEC-WORK docs/SPEC-WORK.md:1557-1561 — "Project and stream containers use `:type :work-set` with the existing explicit `:category` label (`\"project\"` or `\"stream\"`), plus stable `:id`, display `:title` and `:children`. ... its role must not be inferred from that title or from its position."
TEST TestE01F04RepresentProjectAndStreamGroupings
SUITE RUN 1 NOVA-WORK SLICE1 total=408 pass=400 fail=8
SUITE RUN 2 NOVA-WORK SLICE1 total=408 pass=400 fail=8
TEST STATE green — the kernel already stores kind as explicit :type and grouping label as explicit :category, and derives neither from title nor position; the roadmap row for E01-F04-04 can be ticked.
NOTE the 8 failures are environmental (socket bind permission denied, /tmp/nova-work-reqline-* mkdir failure), pre-existing and unrelated to this test; both runs are identical, so the suite is not order-dependent for this change.
git status --short
 M lisp/nova-work/tests/replays-8640.lisp
head feb340793e5d93f502f6efa216a4b6bbb1eaaa4d
Left owed none
