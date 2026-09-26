#!/usr/bin/env bash
# select-packages.sh prints the ./cmd and ./internal Go packages a change
# touches, plus every in-repo package that imports one of them. The self-hosted
# test shards use it so a change pays for the packages it can move and not the
# whole 24-shard fan-out; the push to dev and the nightly run pass --all and
# keep the whole tree.
#
# usage:
#   select-packages.sh [--on-go-list-error=fail|whole-tree] --all
#   select-packages.sh [--on-go-list-error=fail|whole-tree] <base-sha>
#
# The diff is read against the event's own base (pull_request.base.sha or
# merge_group.base_sha). A go.mod or go.sum change puts every package in scope.
#
# NEVER SILENTLY NOTHING. Measured 2026-09-26 ~1:00 PM ET: on PR #4370's final
# head the shards reported `test (nothing)` because `go list` failed on a space
# runner (a shared GOCACHE race: "open /home/ubuntu/.cache/go-build/...: no
# such file or directory") inside a `< <(...)` that swallowed its exit status,
# and the script printed an empty list, so the caller said "0 package(s)
# touched: none" and a green run tested nothing. Every `go list` here goes
# through go_list below: a non-zero exit, or "cannot" or "no such file" on its
# stderr, is a failure, and so is a selection of zero packages from a diff that
# touches Go files. What a failure does is the caller's choice:
#
#   --on-go-list-error=fail        (the default; ci.yml's pull_request) exit 1
#                                  with the go list error printed: fast and
#                                  cheap, the runner is broken, re-run it.
#   --on-go-list-error=whole-tree  (ci.yml's merge_group, push and schedule)
#                                  print `WARN select-packages: go list failed
#                                  (<first line>); testing the whole tree` on
#                                  stderr and the whole tree read from the
#                                  tracked files on stdout: the landing must
#                                  not stall on one runner's cache.
#
# internal/ci's TestSelectPackagesNeverSilentlySelectsNothing holds both.
set -euo pipefail
cd "$(dirname "$0")/../.."

on_error=fail
case "${1:-}" in
  --on-go-list-error=fail) on_error=fail; shift ;;
  --on-go-list-error=whole-tree) on_error=whole-tree; shift ;;
  --on-go-list-error=*) echo "select-packages: unknown ${1}; want fail or whole-tree" >&2; exit 2 ;;
esac

errf=$(mktemp)
trap 'rm -f "$errf"' EXIT

# go_list runs `go list "$@"` and prints its stdout, or returns 1 with the
# error left in $errf. It is called as `x=$(go_list ...) || go_list_failed`,
# never inside `< <(...)`, whose exit status bash throws away.
go_list() {
  local out rc=0
  out=$(go list "$@" 2>"$errf") || rc=$?
  if [ "$rc" -ne 0 ] || grep -q -E 'cannot|no such file' "$errf"; then
    [ -s "$errf" ] || echo "go list exited $rc with nothing on stderr" >"$errf"
    return 1
  fi
  if [ -z "$out" ]; then
    echo "go list exited 0 and listed no packages" >"$errf"
    return 1
  fi
  printf '%s\n' "$out"
}

# tree_from_files is the whole tree without `go list`: every directory under
# cmd/, internal/ and tools/ holding a tracked .go file with no //go:build line,
# less testdata, vendor and _ or . directories. On 2026-09-26 it equalled
# `go list ./cmd/... ./internal/... ./tools/...` package for package.
tree_from_files() {
  git ls-files -z -- 'cmd/*.go' 'internal/*.go' 'tools/*.go' |
    xargs -0 grep -L '^//go:build' |
    grep -v -E '(^|/)(testdata|vendor|[_.][^/]*)/' |
    xargs -n1 dirname | sort -u | sed 's|^|./|'
}

# go_list_failed ends the script: exit 1 with the go list error (fail), or the
# WARN line and the whole tree (whole-tree).
go_list_failed() {
  local first
  first=$(grep -m1 . "$errf" || true)
  if [ "$on_error" = whole-tree ]; then
    echo "WARN select-packages: go list failed (${first}); testing the whole tree" >&2
    tree_from_files
    exit 0
  fi
  echo "ERROR select-packages: go list failed; failing the job rather than testing nothing (re-run on a healthy runner):" >&2
  cat "$errf" >&2
  exit 1
}

list_all() {
  local pkgs pkg
  pkgs=$(go_list ./cmd/... ./internal/... ./tools/...) || go_list_failed
  while read -r pkg; do
    [ -n "$pkg" ] || continue
    printf './%s\n' "${pkg#github.com/mas-bandwidth/nova-tools/}"
  done <<<"$pkgs"
}

if [ "${1:-}" = "--all" ]; then
  list_all
  exit 0
fi

base="$1"
git fetch -q --depth=1 origin "$base" 2>/dev/null || true

# A go.mod or go.sum change can move any package, so it puts the whole tree in
# scope.
if git diff --name-only "$base" HEAD | grep -q -E '^(go\.mod|go\.sum)$'; then
  list_all
  exit 0
fi

all=()
all_out=$(go_list ./cmd/... ./internal/... ./tools/...) || go_list_failed
while IFS= read -r line; do
  [ -n "$line" ] || continue
  all+=("./${line#github.com/mas-bandwidth/nova-tools/}")
done <<<"$all_out"

# The changed .go files name the directories that moved; their import paths are
# the `want` set.
changed_dirs=$(git diff --name-only "$base" HEAD -- '*.go' | xargs -r -n1 dirname | sort -u)
want=""
for d in $changed_dirs; do want="$want ./$d"; done

# dependents: every package in the tree that imports a changed one.
deps_out=$(go_list -f '{{.ImportPath}}{{range .Deps}} {{.}}{{end}}' ./cmd/... ./internal/... ./tools/...) || go_list_failed
while read -r pkg deps; do
  p="./${pkg#github.com/mas-bandwidth/nova-tools/}"
  for dep in $deps; do
    case "$dep" in github.com/mas-bandwidth/nova-tools/*) ;; *) continue ;; esac
    dp="./${dep#github.com/mas-bandwidth/nova-tools/}"
    case " $want " in *" $dp "*) want="$want $p"; break ;; esac
  done
done <<<"$deps_out"

# THE TWO CLASS-TEST PACKAGES ARE ADDED AFTER THE DEPENDENTS, not before. Added
# before, they dragged their own importers (cmd/nova-ci, cmd/nova-merge,
# cmd/nova-work) into every PR's shards although the change touched none of
# them: all 95 PR runs read on 2026-09-23 carried those five packages, and
# cmd/nova-merge is the slowest package on the Macs (342 s). When a change
# does touch internal/ci or internal/docs, the diff names them above and the
# loop still selects their importers, so no change loses a dependent (#2795).

# internal/ci holds the class tests that read the workflow and script files as
# text; it scans the tree rather than importing what it guards, so no *.go diff
# can name it as a dependent. A cmd/nova-swarm edit (PR #1073) left an
# internal/ci class test red and no shard was selected to run it, so the branch
# sat for two hours. Select it on every run, not only when the diff touches
# .github/: a change that can move the workflow's law always pays for the
# package that holds it.
want="$want ./internal/ci"

# internal/docs holds the front page's contract: AGENTS.md must name every class
# rule indexed in docs/SPEC-CI.md. It, too, reads the tree as text instead of
# importing what it guards, so a docs-only change can break its class test
# (#1504) without naming a dependent in the import graph. Select it on every run
# for the same reason: the red surfaces in an integration batch instead of on
# the PR that broke it (#1364, #1409).
want="$want ./internal/docs"

# tools/testdur holds the two-minute rule's suite budget tests; when
# docs/TEST-DURATIONS.md is updated, tools/testdur must be judged.
if git diff --name-only "$base" HEAD 2>/dev/null | grep -q '^docs/TEST-DURATIONS\.md$'; then
  want="$want ./tools/testdur"
fi

selected=""
for pkg in "${all[@]}"; do
  case " $want " in *" $pkg "*) selected="$selected$pkg"$'\n' ;; esac
done

# ZERO PACKAGES FROM A GO DIFF IS AN ERROR, not "nothing to test": a change
# that moved a .go file under cmd/, internal/ or tools/ and selected nothing
# means the selection broke, the #4370 shape.
go_changed=$(git diff --name-only "$base" HEAD -- 'cmd/*.go' 'internal/*.go' 'tools/*.go')
if [ -z "$selected" ] && [ -n "$go_changed" ]; then
  echo "select-packages: the diff touches Go files ($(echo "$go_changed" | head -n1)) but selected zero packages" >"$errf"
  go_list_failed
fi
printf '%s' "$selected"
