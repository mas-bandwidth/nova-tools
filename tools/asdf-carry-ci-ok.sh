#!/usr/bin/env bash
# asdf-carry-ci-ok.sh: is `ci-ok` green at EXACTLY this commit?
#
# Split out of asdf-carry-verify.sh so the one network call it makes is a file a
# reviewer can read on its own, and so the verifier can be driven without the
# network in its own test.
#
# The word EXACTLY is the whole point. `gh pr checks` answers about a PR's
# current head, and a PR's head moves: asking it a minute after a push answers
# about a different commit than the one whose approval is being carried. This
# asks the commit itself, by sha, and prints what it found so the verdict file
# records the measurement and not just a yes.
#
# Usage: tools/asdf-carry-ci-ok.sh <sha> [owner/repo]
# Exit 0 only when a check run named ci-ok completed with conclusion success.
set -u

sha=${1:-}
[ -n "$sha" ] || { echo "asdf-carry-ci-ok: a commit sha is required" >&2; exit 2; }
repo=${2:-mas-bandwidth/nova-tools}

# rowan-claude, never the bench's default account.
export GH_CONFIG_DIR=${GH_CONFIG_DIR:-$HOME/.config/gh-rowan}

runs=$(gh api --paginate \
  "repos/$repo/commits/$sha/check-runs" \
  --jq '.check_runs[] | [.name, .status, (.conclusion // "none")] | @tsv' 2>&1) || {
  echo "could not read check runs for $sha: $(printf '%s' "$runs" | head -2 | tr '\n' ' ')"
  exit 3
}

if [ -z "$runs" ]; then
  echo "no check runs at all are recorded for $sha (an unpushed or unbuilt commit answers nothing)"
  exit 4
fi

line=$(printf '%s\n' "$runs" | awk -F'\t' '$1 == "ci-ok" { print; exit }')
if [ -z "$line" ]; then
  echo "no check run named ci-ok at $sha; the checks present are: $(printf '%s\n' "$runs" | awk -F'\t' '{printf "%s(%s) ", $1, ($3=="none"?$2:$3)}')"
  exit 5
fi

status=$(printf '%s' "$line" | awk -F'\t' '{print $2}')
conclusion=$(printf '%s' "$line" | awk -F'\t' '{print $3}')
echo "ci-ok status=$status conclusion=$conclusion at $sha"
[ "$status" = completed ] && [ "$conclusion" = success ]
