#!/bin/sh
# Run the nova-work slice-1 acceptance cases under SBCL, non-interactively.
# Exit 0 when every case passes, 1 when any case fails.
#
# THE CI LANES (E10-F03-04; SPEC-WORK.md:7089-7100, :7235-7240).
#
#   per-change  (this default)  A per-change check targets one minute and must
#                               finish inside two: PER_CHANGE_CEILING below is
#                               that two-minute bound, measured and enforced on
#                               every default run. The named exhaustive
#                               fault/scale suites are left to the lane below
#                               and cost this lane nothing.
#
#   exhaustive  (--exhaustive)  The exhaustive fault and scale suites, explicit
#                               (EXHAUSTIVE_FAULT_SCALE_SUITES below) and run
#                               only from an explicit nightly or pre-release
#                               lane -- .github/workflows/nightly-slow.yml runs
#                               `run-tests.sh --exhaustive` -- never on every
#                               change. The two-minute bound does not reach
#                               here; the whole matrices may take as long as
#                               they take.
#
# FAILURES BLOCK THE GATES THEY AFFECT (E10-F03-04). A failing case exits with
# the suite's own status and a per-change run over the two-minute bound exits
# 1: the gate of the lane that ran -- the per-change gate (make test-lisp, the
# ci.yml lisp job) or the nightly/pre-release gate (nightly-slow.yml's
# nova-work-exhaustive leg) -- is blocked by that exit and by no swallowed one.
# No failure path here discards a status.
set -eu
here=$(cd "$(dirname "$0")" && pwd)

# The per-change ceiling in seconds: the two-minute law (SPEC-WORK.md:7235).
PER_CHANGE_CEILING=120

# The exhaustive fault/scale suites: SPEC-WORK.md:7094-7098's nightly-lane
# suites (whole matrices, none of which fits two minutes and a gate) and the
# named fault cases of E10-F03's own evidence (failures at the journal, apply,
# reply and publication boundaries). Named in one place, so nothing moves off
# the per-change path unnamed and the nightly lane has content to run.
EXHAUSTIVE_FAULT_SCALE_SUITES="source-inventory import-replay moving-source archive-completeness full-round-trip old-history atomic-mutation async-operations single-writer indexes-and-counters materialized-working-set batches-and-pipelines recovery schema-evolution hostile-data durable-journal-pre-append-failure-writes-nothing durable-journal-partial-write-refuses-without-truncation torn-tail-is-diagnosed-not-truncated crash-after-append-recovers-the-reply-once savepoint-write-failure-keeps-the-previous one-revision-publishes-together"

lane=per-change
for arg in "$@"; do
  case "$arg" in
    --exhaustive) lane=exhaustive ;;
    *) echo "run-tests.sh: unknown argument: $arg" >&2; exit 2 ;;
  esac
done

exhaustive_names=""
for suite in ${EXHAUSTIVE_FAULT_SCALE_SUITES}; do
  exhaustive_names="${exhaustive_names} \"${suite}\""
done

if [ "${lane}" = exhaustive ]; then
  lane_filter="(values)"
else
  # The per-change lane drops the named exhaustive fault/scale cases before
  # RUN-ALL: they run in the explicit nightly or pre-release lane above and
  # never on every change (SPEC-WORK.md:7235).
  lane_filter="(setf nova-work/tests::*tests* (delete-if (lambda (entry) (member (first entry) (list${exhaustive_names}) :test (function string=))) nova-work/tests::*tests*))"
fi

start=$(date +%s)
status=0
sbcl --non-interactive \
  --eval "(require :asdf)" \
  --eval "(push #p\"${here}/\" asdf:*central-registry*)" \
  --eval "(handler-bind ((warning #'muffle-warning)) (asdf:load-system :nova-work/tests))" \
  --eval "${lane_filter}" \
  --eval "(nova-work/tests:main)" || status=$?
end=$(date +%s)
elapsed=$((end - start))

if [ "${lane}" = per-change ] && [ "${elapsed}" -gt "${PER_CHANGE_CEILING}" ]; then
  echo "run-tests.sh: lane=per-change elapsed=${elapsed}s over the PER_CHANGE_CEILING=${PER_CHANGE_CEILING}s two-minute bound; the per-change gate is blocked" >&2
  exit 1
fi
if [ "${status}" -ne 0 ]; then
  echo "run-tests.sh: lane=${lane} FAIL; the ${lane} gate is blocked" >&2
fi
exit "${status}"
