#!/usr/bin/env bash
# live-packages.sh reads Go packages on stdin, one per line, as ./cmd/x or as
# the full import path, and prints the ones that are not deprecated, unchanged.
#
# DEPRECATED PACKAGES ARE NEVER TESTED (Glenn 2026-09-27: "Tests do not run for
# deprecated tools and modules. ... We don't run their tests. We don't stop
# builds for them. We don't bog down CI for them."). deprecated/PACKAGES names
# the packages that are deprecated and still in the tree because living tools
# import them: a path names that package and everything under it, and a line
# `keep <path>` names one package under such a path that stays tested until it
# is lifted out into a shared module. Everything under deprecated/ itself is a
# separate Go module, which `go list ./...` never lists.
#
# Every place that chooses the packages a run tests reads its list through
# this script: select-packages.sh and ci.yml's hosted deal. internal/ci's
# TestDeprecatedPackagesAreNeverSelected holds both.
set -euo pipefail
list="$(cd "$(dirname "$0")/../.." && pwd)/deprecated/PACKAGES"
[ -f "$list" ] || exec cat
exec awk -v list="$list" -v mod="github.com/mas-bandwidth/nova-tools/" '
  BEGIN {
    while ((getline line < list) > 0) {
      sub(/#.*/, "", line); gsub(/^[ \t]+|[ \t]+$/, "", line)
      if (line == "") continue
      if (line ~ /^keep[ \t]/) { sub(/^keep[ \t]+/, "", line); keep[line] = 1 }
      else drop[n++] = line
    }
  }
  {
    p = $0
    if (index(p, mod) == 1) p = substr(p, length(mod) + 1)
    if (substr(p, 1, 2) == "./") p = substr(p, 3)
    if (p in keep) { print; next }
    for (i = 0; i < n; i++) if (p == drop[i] || index(p, drop[i] "/") == 1) next
    print
  }'
