#!/usr/bin/env bash
# plan-merge-shards.sh <leg> <cap-seconds> <package>... : print the PLAN line and
# the shard count a merge group's hosted leg needs, derived from the MEASURED
# size table for that leg rather than from a fixed 1/3/6.
#
# WORK IN PROGRESS (lane ci, PR rowan/ci-fullrepo-deal). Nothing calls this yet:
# the workflow still deals by the fixed count. What is settled is the derivation
# and the numbers it rests on, measured out of the hurt below; what is not yet
# written is the workflow wiring (dynamic matrix from the plan job), the class
# tests and the SPEC-CI entries.
#
# THE HURT: merge-group run 35394167164 (batch 11b), darwin shards 0 and 1
# CANCELLED at the ten-minute leg cap, the group dequeued. Deterministic, not a
# flake: the group changed go.mod, `select the packages this group changes` puts
# EVERY package in scope for a go.mod/go.sum change, and ~90 packages were dealt
# into six FIXED shards.
#
# WHAT THE LOG SAYS (job 105759102245, darwin shard 0, read line by line):
#
#	step span                                601 s (killed)
#	packages reached                         54 of ~90
#	`go test -list` before each package      159.8 s over those 54 = 3.0 s each
#	`go test` actually running tests         398.8 s
#	checkout + setup-go before the step       12 s
#
# That is the finding, and it changes the shape of the fix: HALF OF EVERY SHARD
# IS FIXED COST. Each shard runs `go test -list` for EVERY selected package,
# whether or not it holds a test for that shard, so the full-repo scope costs
# ~90 x 3.0 = 270 s per shard before a single test runs. A fixed cost does not
# shrink when shards are added: raising the shard count alone cannot fit this
# scope under the cap, and LOWERING it (the naive sum/budget answer for this
# tree is 3) would be worse than the six that just died.
#
# The measured totals say the same thing from the other end. Six darwin shards
# spent >=2557 s of wall between them, of which 6 x (12 + 270) = ~1692 s was
# setup and listing; the tests themselves came to roughly 850 s, against a whole
# tree of 636.0 s measured quiet in testdata/ci/package-sizes-darwin.tsv. The
# tests were never the problem. The dealing was.
#
# SO THE DEAL IS BY PACKAGE, NOT BY TEST NAME, once the scope is wide. A shard
# that owns whole packages runs `go test -list` for its own packages only, pays
# no share of anyone else's, and the sum it carries is a sum this table can
# predict. That is also what makes the card's formula exact rather than
# optimistic: bin-packing whole packages is what `sum / budget` describes.
#
#	budget  = (cap - SETUP) / MARGIN     measured quiet-host seconds per shard
#	shards  = max( ceil( sum(selected) / budget ), packages that exceed budget )
#
# with MARGIN = 2.0, the margin already stated in the darwin table and in
# internal/ci's darwin_shards_class_test.go (cmd/nova-merge: 68.8 s quiet against
# ~147 s loaded, 2.1x), and SETUP = 20 s for checkout and setup-go (12 s
# measured, rounded up). For the darwin leg's ten-minute cap that is a budget of
# 290 measured seconds, and the whole tree at 636.0 s needs THREE shards, each
# carrying ~212 s quiet -> ~424 s loaded, inside the 600 s cap with room. The
# largest single package (cmd/nova-wake, 120.3 s) is well under the budget, so
# the per-package ceiling opens no extra shard here; the rule stays because the
# next measurement may not be so kind.
#
# UNMEASURED PACKAGES: never guessed downward. One package with tests is absent
# from the darwin table (internal/redisq) and eight have no test files at all.
# The rule this lane has not yet written into code: a package with tests and no
# measurement is charged the largest measured size, and a package with no test
# files is charged nothing, which needs the plan job to know which is which.
#
# PLAN line (what the plan job prints, one per leg):
#
#	PLAN darwin packages=<n> seconds=<s> shards=<k> cap=<c>
#
set -euo pipefail
echo "plan-merge-shards.sh: not wired in yet; see the header for the derivation" >&2
exit 2
