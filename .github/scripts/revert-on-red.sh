#!/usr/bin/env bash
# revert-on-red.sh: the mechanical revert of the push that turned main red.
#
# Run by revert-on-red.yml on the checked-out head_sha of a failed `ci`
# workflow_run on main. One of three guards stops it with a notice and exit 0
# (never a failure -- a skip is not a red):
#   * the head commit is itself a revert        -> no revert loops
#   * the parent commit's ci run was not green  -> the red predates this push
#   * main has moved past this commit           -> the newer run decides
# Otherwise it reverts the push, pushes to main, and if the ruleset refuses
# the push, opens a revert/<sha> PR and LEAVES IT OPEN with a notice -- this
# script enables no auto-merge and lands nothing on its own; either way it
# posts ONE comment on the merged pull request, naming the revert.
# Env: GITHUB_REPOSITORY, GITHUB_TOKEN, HEAD_SHA, RUN_ID.
set -euo pipefail
: "${GITHUB_REPOSITORY:?}" "${GITHUB_TOKEN:?}" "${HEAD_SHA:?}" "${RUN_ID:?}"
export GH_TOKEN="${GITHUB_TOKEN}"

repo="${GITHUB_REPOSITORY}"
head_sha="${HEAD_SHA}"
run_id="${RUN_ID}"

short() { printf '%s' "${1:0:12}"; }
notice() { echo "::notice::$*"; }

# --- guard 1: never revert a revert (no revert loops) ---
subject="$(git log -1 --format=%s "$head_sha")"
body="$(git log -1 --format=%B "$head_sha")"
if [[ "$subject" == Revert\ * || "$subject" == revert\ * || "$body" == *"This reverts commit"* ]]; then
  notice "head $(short "$head_sha") is itself a revert (\"$subject\"); no revert loops, skipping."
  exit 0
fi

# --- main must still be at the failed run's commit ---
git fetch origin main >/dev/null 2>&1
if [ "$(git rev-parse origin/main)" != "$head_sha" ]; then
  notice "main has moved past $(short "$head_sha") (now $(short "$(git rev-parse origin/main)")); the newer run decides, skipping."
  exit 0
fi

# --- guard 2: the parent commit's ci run on main must have been green ---
parent="$(git rev-parse --verify "$head_sha"^ 2>/dev/null || git rev-parse --verify "$head_sha"~1)"
echo "head=$(short "$head_sha") parent=$(short "$parent")"

parent_conclusion=""
for page in 1 2 3 4 5; do
  parent_conclusion="$(gh api "repos/$repo/actions/workflows/ci.yml/runs?branch=main&event=push&per_page=100&page=$page" \
    --jq '.workflow_runs[] | select(.head_sha == "'"$parent"'") | .conclusion' 2>/dev/null | head -n1)"
  [ -n "$parent_conclusion" ] && break
done
if [ -z "$parent_conclusion" ]; then
  notice "no ci push run found for parent $(short "$parent"); no green baseline, skipping."
  exit 0
fi
if [ "$parent_conclusion" != "success" ]; then
  notice "parent $(short "$parent") ci run concluded $parent_conclusion; the red predates this push, skipping."
  exit 0
fi

# --- the failing jobs, for the commit message and the comment ---
failing="$(gh api "repos/$repo/actions/runs/$run_id/jobs?per_page=100" \
  --jq '[.jobs[] | select(.conclusion != "success" and .conclusion != "skipped") | .name] | join(", ")' 2>/dev/null || true)"
failing="${failing:-no failing job named}"

# --- the revert ---
git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"

parents="$(git rev-list --parents -n 1 "$head_sha")"
if [ "$(wc -w <<<"$parents")" -ge 3 ]; then
  echo "merge commit: reverting with -m 1"
  git revert -m 1 --no-edit "$head_sha"
else
  echo "plain commit: reverting"
  git revert --no-edit "$head_sha"
fi

msg="revert $(short "$head_sha"): main red on $failing (mechanical revert-on-red; fix forward on a branch)"
git commit --amend -q -m "$msg"
new_sha="$(git rev-parse HEAD)"
echo "revert commit=$(short "$new_sha")"

# --- push to main, or open a revert/<sha> PR with auto-merge ---
push_log="$(mktemp)"
if git push origin HEAD:main >"$push_log" 2>&1; then
  echo "pushed revert $(short "$new_sha") to main"
else
  cat "$push_log"
  echo "direct push refused by the ruleset; opening a revert PR for somebody to land"
  branch="revert/$(short "$head_sha")"
  git branch -f "$branch" HEAD
  git push origin "$branch" || git push -f origin "$branch"
  if ! gh pr create --base main --head "$branch" --title "$msg" \
    --body "Mechanical revert-on-red. ci failed on main at $(short "$head_sha"); reverted as $(short "$new_sha"). Fix forward on a branch."; then
    echo "PR already exists for $branch; reusing it."
  fi
  pr_number="$(gh pr view "$branch" --json number --jq .number)"
  echo "revert PR #$pr_number open on $branch"
  # IT IS LEFT OPEN, DELIBERATELY. This used to enable auto-merge on the revert, which is
  # not an enqueue at all: it is a standing instruction the forge executes later with
  # nobody in the room. On 2026-09-18 twenty-seven open pull requests were carrying one and
  # four of them walked into the dev merge queue on their own (Glenn: nothing reaches the
  # dev merge queue but a batch). CI lands nothing by itself; a person or a batch lands
  # this, and the notice says so loudly enough to act on.
  notice "revert PR #$pr_number is OPEN on $branch and lands nothing by itself: main is red until somebody lands it -- nova-merge land --repo $repo --pr $pr_number, or merge it by hand."
fi

# --- ONE comment on the merged PR (found by the commit's PR association) ---
pr="$(gh api "repos/$repo/commits/$head_sha/pulls" --jq '.[] | select(.merged_at != null) | .number' 2>/dev/null | head -n1)"
if [ -n "$pr" ]; then
  body="Main was red on $failing. Reverted by $(short "$new_sha") (mechanical revert-on-red); fix forward on a branch."
  gh api "repos/$repo/issues/$pr/comments" -f body="$body" >/dev/null
  echo "commented on #$pr naming revert $(short "$new_sha")"
else
  notice "no merged PR found for $(short "$head_sha"); posting no comment."
fi