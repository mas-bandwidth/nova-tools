#!/bin/sh
# The scripted child of the OpenAI profile (docs/SPEC-CARD-CONTRACT.md, Writing a profile):
# it works in a linked worktree, commits, records a push and finishes with a pull request.
set -eu

job=$(pwd)
repo="$job/repo"
work="$job/worktree"
body="$job/pull-request.md"

git -C "$repo" worktree add -b openai/change "$work" HEAD
printf 'the change\n' >> "$work/f"
git -C "$work" add f
git -C "$work" commit -q -m "the change"
git -C "$work" push -u origin HEAD

cat > "$body" <<'EOF'
## Summary

the body, line two
EOF

cd "$work"
gh pr create --title "The change" --body-file "$body"
echo "scripted child: done"
