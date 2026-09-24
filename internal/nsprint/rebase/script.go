package rebase

import (
	"strconv"
	"strings"
)

// renderScript writes the card. The values are validated and single-quoted
// before they reach the script, so a branch name cannot become a second
// command. The script fetches the two refs it was given, rebases, and
// either pushes or reports one conflict. It never asks a model to resolve.
func renderScript(rec Record, key string) string {
	return strings.NewReplacer(
		"[[BASETEXT]]", rec.Base,
		"[[BRANCHTEXT]]", rec.Branch,
		"[[HEADTEXT]]", rec.Head,
		"[[AUTHORTEXT]]", rec.Author,
		"[[REPOTEXT]]", rec.Repo,
		"[[KEYTEXT]]", key,
		"[[BASE]]", shQuote(rec.Base),
		"[[BRANCH]]", shQuote(rec.Branch),
		"[[HEAD]]", shQuote(rec.Head),
		"[[AUTHOR]]", shQuote(rec.Author),
		"[[KEY]]", shQuote(key),
		"[[PR]]", strconv.Itoa(rec.Number),
	).Replace(scriptTpl)
}

func shQuote(s string) string { return "'" + s + "'" }

// scriptTpl is the card body. A range-diff is empty of differences when
// every non-blank line is an identical pairing (`=`). git range-diff prints
// those `=` lines even when the patch did not change, so "empty" is not a
// zero-length file. A blank file is not evidence the patch held (the carry
// verifier refuses that), and a `!`, `<`, `>` or hunk does not carry.
const scriptTpl = `#!/bin/sh
# KIND: script
# MODEL_CALLS: 0
# KEY: [[KEYTEXT]]
# PR: [[PR]]
# HEAD: [[HEADTEXT]]
# BASE: [[BASETEXT]]
# BRANCH: [[BRANCHTEXT]]
# AUTHOR: [[AUTHORTEXT]]
# REPO: [[REPOTEXT]]
# NO-SUBAGENTS
# Rebase this checkout onto origin/BASE. Do not call a model. A conflict
# is one fix task for the author and this card is not cut again.
set -eu
unset CDPATH

# Empty of differences: at least one '=' pairing and nothing else.
# A blank file, a '!' line, an unpaired commit or a hunk does not carry.
range_diff_empty() {
  f=$1
  if [ ! -s "$f" ]; then
    return 1
  fi
  found=0
  while IFS= read -r line || [ -n "$line" ]; do
    if [ -z "$line" ]; then
      continue
    fi
    if ! printf '%s\n' "$line" | grep -E -q '^ *[0-9-]+: *[0-9a-f]+ *= [0-9-]+: *[0-9a-f]+ '; then
      return 1
    fi
    found=1
  done < "$f"
  [ "$found" -eq 1 ]
}

if [ "${1:-}" = "--classify" ]; then
  if [ "${2:-}" = "" ] || [ ! -f "$2" ]; then
    echo "REBASE REFUSED: --classify wants the range-diff file; pass the file git range-diff wrote" >&2
    exit 2
  fi
  if range_diff_empty "$2"; then
    echo "READS carry"
  else
    echo "READS drop"
  fi
  exit 0
fi

if [ "$#" -ne 2 ]; then
  echo "REBASE REFUSED: usage: card.sh <checkout> <work-dir>; pass the scratch clone and a work directory" >&2
  exit 2
fi

git_dir=$1
work=$2
base=[[BASE]]
branch=[[BRANCH]]
old_head=[[HEAD]]
pr=[[PR]]
author=[[AUTHOR]]
card_key=[[KEY]]

export GIT_EDITOR=true
export GIT_SEQUENCE_EDITOR=true
export GIT_TERMINAL_PROMPT=0

cd "$git_dir"

if ! git config user.email >/dev/null 2>&1; then
  echo "REBASE REFUSED: this clone has no user.email; set it and run the card again" >&2
  exit 2
fi

if ! git fetch --no-tags origin "$base" "$branch" >&2; then
  echo "REBASE REFUSED: could not fetch origin ${base} and ${branch}; check the clone's origin and run the card again" >&2
  exit 2
fi

if ! git rev-parse --verify --quiet "origin/${base}^{commit}" >/dev/null; then
  echo "REBASE REFUSED: origin/${base} is not a commit after fetch; fetch that ref and run the card again" >&2
  exit 2
fi

git checkout -q --detach "$old_head"
have=$(git rev-parse HEAD)
if [ "$have" != "$old_head" ]; then
  echo "REBASE REFUSED: checked out ${have}, card head is ${old_head}; check out that head and run the card again" >&2
  exit 2
fi

old_base=$(git merge-base "$old_head" "origin/${base}")

set +e
git rebase "origin/${base}" >/dev/null 2>&1
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  rebase_dir=$(git rev-parse --git-path rebase-merge)
  apply_dir=$(git rev-parse --git-path rebase-apply)
  if [ -d "$rebase_dir" ] || [ -d "$apply_dir" ]; then
    git rebase --abort
    echo "FIX ${pr}:${old_head}:conflict"
    echo "AUTHOR ${author}"
    echo "NOCARD ${card_key}"
    exit 3
  fi
  echo "REBASE REFUSED: git rebase exited ${rc} without a conflict; fix the checkout and run the card again" >&2
  exit 2
fi

new_head=$(git rev-parse HEAD)
new_base=$(git rev-parse "origin/${base}")
rd="${work}/range-diff.txt"
git range-diff --no-color "${old_base}..${old_head}" "${new_base}..${new_head}" > "$rd"
if range_diff_empty "$rd"; then
  reads=carry
else
  reads=drop
fi

if ! git push --force-with-lease="refs/heads/${branch}:${old_head}" origin "HEAD:refs/heads/${branch}" >&2; then
  echo "REBASE REFUSED: the rebased head was not pushed; origin/${branch} is not ${old_head}" >&2
  exit 2
fi

echo "READS ${reads}"
echo "PUSH ${new_head}"
exit 0
`
