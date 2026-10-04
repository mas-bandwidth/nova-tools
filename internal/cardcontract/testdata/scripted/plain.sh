#!/bin/sh
# The scripted child of the plain profile (docs/SPEC-CARD-CONTRACT.md, Writing a profile):
# it works in the staged checkout, commits, pushes, and writes RESULT.md in the shape.
set -e
cd repo
echo "the change" >> f
git commit -q -am "the change"
git push
printf 'head: %s\nbranch: %s\nverdict: ok\ngate: -\noutput: -\nreport: done plainly\n' "$(git rev-parse HEAD)" "$(git symbolic-ref --short HEAD)" > ../RESULT.md
echo "scripted child: done"
