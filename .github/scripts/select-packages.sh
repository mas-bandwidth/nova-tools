#!/usr/bin/env bash
# select-packages.sh prints the ./cmd and ./internal Go packages a change
# touches, plus every in-repo package that imports one of them. The self-hosted
# test shards use it so a change pays for the packages it can move and not the
# whole 24-shard fan-out; the push to dev and the nightly run pass --all and
# keep the whole tree.
#
# usage:
#   select-packages.sh --all          # every package
#   select-packages.sh <base-sha>     # the packages the base..HEAD diff touches
#
# The diff is read against the event's own base (pull_request.base.sha or
# merge_group.base_sha). A go.mod or go.sum change puts every package in scope.
# Prints nothing when the change touches no Go package — docs, lisp, or an
# otherwise inert edit — so the caller can choose "run nothing".
set -euo pipefail
cd "$(dirname "$0")/../.."

list_all() {
  local pkg
  while read -r pkg; do
    [ -n "$pkg" ] || continue
    printf './%s\n' "${pkg#github.com/mas-bandwidth/nova-tools/}"
  done < <(go list ./cmd/... ./internal/...)
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
while IFS= read -r line; do all+=("$line"); done < <(list_all)

# The changed .go files name the directories that moved; their import paths are
# the `want` set.
changed_dirs=$(git diff --name-only "$base" HEAD -- '*.go' | xargs -r -n1 dirname | sort -u)
want=""
for d in $changed_dirs; do want="$want ./$d"; done

# internal/ci holds the class tests that read the workflow and script files as
# text; it scans the tree rather than importing what it guards, so no *.go diff
# can name it as a dependent. A cmd/nova-swarm edit (PR #1073) left an
# internal/ci class test red and no shard was selected to run it, so the branch
# sat for two hours. Select it on every run, not only when the diff touches
# .github/: a change that can move the workflow's law always pays for the
# package that holds it.
want="$want ./internal/ci"

# internal/docs has the same blind spot for the same reason: it reads AGENTS.md,
# docs/SPEC-CI.md and the rest of docs/ as text, so no *.go diff can name it --
# and a docs-only change names no directory at all. A pull request that added a
# class rule to SPEC-CI.md's index without naming it on AGENTS.md therefore went
# green and turned the integration batch that carried it red: #1364
# (toolchainroots) and #1409 (hostseam), one gate round each. Select it on every
# run, like internal/ci above.
want="$want ./internal/docs"

# dependents: every package in the tree that imports a changed one.
while read -r pkg deps; do
  p="./${pkg#github.com/mas-bandwidth/nova-tools/}"
  for dep in $deps; do
    case "$dep" in github.com/mas-bandwidth/nova-tools/*) ;; *) continue ;; esac
    dp="./${dep#github.com/mas-bandwidth/nova-tools/}"
    case " $want " in *" $dp "*) want="$want $p"; break ;; esac
  done
done < <(go list -f '{{.ImportPath}}{{range .Deps}} {{.}}{{end}}' ./cmd/... ./internal/...)

for pkg in "${all[@]}"; do
  case " $want " in *" $pkg "*) printf '%s\n' "$pkg" ;; esac
done
