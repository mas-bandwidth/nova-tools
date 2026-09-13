#!/usr/bin/env bash
# release-certified.sh: does a green certification.yml run vouch for this exact commit?
#
# Asked twice by release.yml: once as the whole `certified` job, and again at the start of the
# `release` job, because one snapshot cannot see a run created after it was taken -- a red that
# starts after the first ask and finishes before the binaries are built would otherwise release.
# Env: GITHUB_REPOSITORY, SHA (the commit being built), REF (the ref, for the remedy line),
# GITHUB_RUN_ID (for the rerun line), GH_TOKEN. Exit 0 vouched; exit 1 refused, with the remedy.
# The whole shell is here rather than in the workflow so certification.yml's release dry-run can
# drive it with a fake `gh` (nova-tools#253): old-green/new-red, in-flight, single green.
set -euo pipefail
: "${GITHUB_REPOSITORY:?}" "${SHA:?}" "${REF:?}" "${GITHUB_RUN_ID:?}"
# ONE ASKER, AND IT READS THE STATUS RATHER THAN THE EXIT CODE, for the reason the
# release job's `ask` below states: `gh api` exits 1 on an empty answer and on no
# answer alike. `-i` puts the status line first and the body after the blank line.
# ONE SNAPSHOT, NO STATUS FILTER. Two asks (completed, then in flight) are two
# snapshots, and a run that finishes red between them is in neither answer. So every
# run on this commit is fetched once, and the decision is made from that one list:
# anything not yet completed (queued, waiting, in progress) refuses with a wait line;
# otherwise the run with the LATEST UPDATE decides, not the newest by creation or by
# array position, because a rerun of an older run id is newer evidence than a later
# run that was never rerun. The perf wall clock lives in certification, so a commit
# green once and red on a rerun is a commit the newest evidence says not to release.
set +e
out=$(gh api -i "repos/${GITHUB_REPOSITORY}/actions/workflows/certification.yml/runs?head_sha=${SHA}&per_page=100" 2>&1); rc=$?
set -e
case "$(printf '%s\n' "$out" | head -n 1)" in
  HTTP/*" 200 "*) ;;
  *)
    echo "asking GitHub for certification runs on $SHA did not answer 200 (gh exit $rc):"
    printf '%s\n' "$out"
    exit 1
    ;;
esac
body=$(printf '%s\n' "$out" | sed -e '1,/^[[:space:]]*$/d')
total=$(printf '%s' "$body" | jq -r '.total_count')
listed=$(printf '%s' "$body" | jq -r '.workflow_runs | length')
if [ "$total" != "$listed" ]; then
  echo "refusing: $total certification runs on $SHA but only $listed listed; the selection would be ambiguous"
  exit 1
fi
printf '%s' "$body" | jq -r '.workflow_runs[] | "  run \(.id) attempt \(.run_attempt) \(.status) \(.conclusion // "-") updated \(.updated_at) \(.html_url)"'
inflight=$(printf '%s' "$body" | jq -r '[.workflow_runs[] | select(.status != "completed")] | length')
if [ "$inflight" != 0 ]; then
  echo "refusing: $inflight certification run(s) on $SHA not yet completed; wait for certification-ok, then re-run this workflow: gh run rerun $GITHUB_RUN_ID -R $GITHUB_REPOSITORY"
  exit 1
fi
newest=$(printf '%s' "$body" | jq -r '(.workflow_runs | sort_by(.updated_at, .run_attempt, .id) | last) // {} | "\(.id // "none") \(.run_attempt // 0) \(.conclusion // "none") \(.updated_at // "-")"')
set -- $newest
echo "certification runs on $SHA: $total completed; latest update is run $1 attempt $2, concluded $3 at $4"
if [ "$3" != success ]; then
  echo "refusing: the latest certification evidence on $SHA is $3, so nothing vouches for this tree"
  echo "certify it first (a run still in progress does not count; wait for certification-ok):"
  echo "  gh workflow run certification.yml -R $GITHUB_REPOSITORY --ref $REF"
  echo "then re-run this workflow: gh run rerun $GITHUB_RUN_ID -R $GITHUB_REPOSITORY"
  exit 1
fi

