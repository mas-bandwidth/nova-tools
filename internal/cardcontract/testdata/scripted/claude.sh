#!/bin/sh
# The scripted child of the claude profile (docs/SPEC-CARD-CONTRACT.md, Writing a profile):
# what the family's models do with a card. It reads JOB.md, clones the repository it names
# into a directory of its own, branches, commits, pushes and opens a pull request.
set -e
repo=$(sed -n 's/^You are in a checkout of \([^ ]*\) on branch.*/\1/p' JOB.md)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/child.XXXXXX")
git clone "$repo" "$tmp/work"
cd "$tmp/work"
git checkout -q -b my-feature
echo "the change" >> f
git commit -q -am "the change"
git push -q -u origin my-feature
gh pr create --title "The change" --body "the body, line one
line two"
echo "scripted child: done"
