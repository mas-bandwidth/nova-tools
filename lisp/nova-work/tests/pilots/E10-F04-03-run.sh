#!/usr/bin/env bash
# E10-F04-03 pilot (docs/SPEC-WORK.md:7228-7229), stages (3) and (4):
#   (3) read-only capture of an AUTHORISED real repository, reconciled against
#       the captured records;
#   (4) an import into a DISPOSABLE destination (a temp dir) with the originals
#       untouched: exported, loaded in a fresh engine, independently compared,
#       repeated and resumed, every gap listed.
# The authorised repository is mas-bandwidth/nova-pilot, a private sandbox
# (coordinator ruling 2026-09-23 1:35 PM ET); nothing in it is product.
# Every GitHub call goes through ghget, which issues GET only; a non-GET is
# impossible from this script. Auth comes from the caller's environment
# (GH_CONFIG_DIR); no secret value is read or written here.
#
# Stage 4 runs through nova-work itself (E10-F04-03-engine.lisp): the import
# builds a nova-work state from the capture and writes it with the engine's
# export writer (write-state-export) into the temp dir; a FRESH engine -- a
# separate SBCL process with an empty environment -- loads that export through
# the isolated read-only `state-load`, rebuilds the model with
# `read-loaded-snapshot`, hashes every loaded record itself and re-exports;
# this script compares that inventory to the capture.
#
# usage: E10-F04-03-run.sh <import-root-dir> <receipt-out.sexp> [<retain-export-dir>]
set -euo pipefail
REPO=${PILOT_REPO:-mas-bandwidth/nova-pilot}
ROOT=$1; OUT=$2; RETAIN=${3:-}
HERE=$(cd "$(dirname "$0")" && pwd); NW=$(cd "$HERE/../.." && pwd)
SBCL=$(command -v sbcl)
[ -z "$RETAIN" ] || [ ! -e "$RETAIN" ] || { echo "retain dir $RETAIN exists" >&2; exit 1; }
mkdir -p "$ROOT"
WORK=$(mktemp -d "$ROOT/pilot.XXXXXX")
CALLS=$WORK/calls; : > "$CALLS"

ghget() { printf 'GET %s\n' "$1" >> "$CALLS"; gh api --method GET "$1"; }
sha() { shasum -a 256 | cut -c1-64; }

# capture <dir>: the source's issues, comments and refs as one record per file
# (records/<id>) plus index.tsv "<id>\t<kind>\t<mapping>\t<sha256>".
capture() {
  local d=$1; mkdir -p "$d/records"
  ghget "repos/$REPO/issues?state=all&per_page=100" > "$d/issues.json"
  ghget "repos/$REPO/issues/comments?per_page=100" > "$d/comments.json"
  git ls-remote "https://github.com/$REPO.git" | sort > "$d/refs"
  jq -r '.[] | select(.pull_request|not) | .number' "$d/issues.json" | sort -n | while read -r n; do
    jq -j --argjson n "$n" '.[]|select(.number==$n)|"\(.title)\n\(.body // "")\n"' "$d/issues.json" > "$d/records/issue-$n"
    printf 'issue-%s\tissues\t%s#%s\t%s\n' "$n" "$REPO" "$n" "$(sha < "$d/records/issue-$n")"
  done > "$d/index.tsv"
  jq -r '.[] | "\(.id) \(.issue_url|split("/")|last)"' "$d/comments.json" | sort -n | while read -r id n; do
    jq -j --argjson id "$id" '.[]|select(.id==$id)|"\(.body)\n"' "$d/comments.json" > "$d/records/comment-$id"
    printf 'comment-%s\tcomments\t%s#%s/comment-%s\t%s\n' "$id" "$REPO" "$n" "$id" "$(sha < "$d/records/comment-$id")"
  done >> "$d/index.tsv"
  { cat "$d/index.tsv"; cat "$d/refs"; } | sha
}

# engine <verb> <args...>: one nova-work engine run, a new SBCL process with an
# empty environment (only PATH and HOME, for the fasl cache).
engine() {
  env -i PATH="$(dirname "$SBCL"):/usr/bin:/bin" HOME="$HOME" "$SBCL" --noinform \
    --non-interactive --no-userinit --no-sysinit \
    --eval '(require :asdf)' \
    --eval "(push #p\"$NW/\" asdf:*central-registry*)" \
    --eval "(handler-bind ((warning #'muffle-warning)) (asdf:load-system :nova-work))" \
    --load "$HERE/E10-F04-03-engine.lisp" \
    --eval '(nova-work-pilot:main (rest (member "--args" sb-ext:*posix-argv* :test (function string=))))' \
    --end-toplevel-options --args "$@" > "$WORK/engine.out" 2>&1 || true
  cat "$WORK/engine.out" >> "$WORK/engine.log"
  grep -E '^(IMPORT|LOAD|REC|REEXPORT)' "$WORK/engine.out" || true
}
field() { sed -n "s/.* $1=\([^ ]*\).*/\1/p"; }

before=$(capture "$WORK/before")
imp=$(engine import "$WORK/before" "$WORK/dest/" "$REPO")
rep=$(engine import "$WORK/before" "$WORK/repeat/" "$REPO")
part=$(engine import "$WORK/before" "$WORK/resume-partial/" "$REPO" 1)
res=$(engine import "$WORK/before" "$WORK/resume/" "$REPO" 0 "$WORK/resume-partial/")
after=$(capture "$WORK/after")
[ "$(printf '%s\n' "$imp" "$rep" "$part" "$res" | grep -c '^IMPORT OK')" = 4 ] \
  || { echo "an engine import failed; see $WORK/engine.log" >&2; exit 1; }
member=$(printf '%s' "$imp" | field member); manifest=$(printf '%s' "$imp" | field manifest)
rmember=$(printf '%s' "$rep" | field member)
pmember=$(printf '%s' "$part" | field member); pnew=$(printf '%s' "$part" | field new)
smember=$(printf '%s' "$res" | field member); snew=$(printf '%s' "$res" | field new); skept=$(printf '%s' "$res" | field kept)

# the fresh engine loads the disposable destination and reports its own inventory
loaded=$(engine load "$WORK/dest/" "$WORK/load/" "$REPO")
loadline=$(printf '%s\n' "$loaded" | awk -F'\t' '$1=="LOAD"{print $2}')
reexport=$(printf '%s\n' "$loaded" | awk -F'\t' '$1=="REEXPORT"{print $2}')
printf '%s\n' "$loaded" | awk -F'\t' '$1=="REC"{print $2"\t"$3"\t"$4"\t"$5}' > "$WORK/loaded.tsv"
want=$(cut -f1,4 "$WORK/before/index.tsv" | sort)
gaps=$( { diff <(printf '%s\n' "$want") <(cut -f1,4 "$WORK/loaded.tsv" | sort) | grep '^[<>]' || true
          printf '%s\n' "$loaded" | awk -F'\t' '$1=="REC" && $6!="hash-ok"{print "> "$2" "$6}'
          [ "$reexport" = "$member" ] || echo "re-export $reexport is not member $member"; } )
n=$(wc -l < "$WORK/before/index.tsv" | tr -d ' ')
if [ -z "$gaps" ] && [ -n "$member" ] && [ "${loadline#LOAD OK}" != "$loadline" ] \
   && [ "$before" = "$after" ] && [ "$member" = "$rmember" ] && [ "$member" = "$smember" ] \
   && [ "$pmember" != "$member" ]; then
  disp="INVENTORY OK count=$n"
else
  disp="INVENTORY FAIL"
fi
[ -z "$RETAIN" ] || cp -R "$WORK/dest" "$RETAIN"
nonget=$(grep -vc '^GET ' "$CALLS" || true)

rec() { while IFS=$'\t' read -r id kind map s; do
          printf '    (:id "%s" :kind :%s :original "%s" :mapping "%s")\n' "$id" "$kind" "$s" "$map"
        done < "$1"; }
{
  echo ';;;; E10-F04-03 pilot receipt, written by E10-F04-03-run.sh; do not hand-edit.'
  echo '(:receipt "E10-F04-03"'
  printf ' :spec "docs/SPEC-WORK.md:7228-7229"\n'
  printf ' :repository "%s" :authorised-by "coordinator ruling 2026-09-23 1:35 PM ET"\n' "$REPO"
  printf ' :at "%s" :runner "%s"\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${PILOT_RUNNER:-rowan}"
  printf ' :calls (%s)\n' "$(sed 's/^\([A-Z]*\) \(.*\)$/("\1" "\2")/' "$CALLS" | tr '\n' ' ')"
  printf ' :mutations %s\n' "$nonget"
  printf ' :refs (%s)\n' "$(awk '{printf "(\"%s\" \"%s\") ", $2, $1}' "$WORK/before/refs")"
  printf ' :source-before "%s" :source-after "%s"\n' "$before" "$after"
  echo ' :captured ('; rec "$WORK/before/index.tsv"; echo '   )'
  printf ' :destination (:kind :temp-dir :retained "%s"\n' "${RETAIN:+tests/pilots/$(basename "$RETAIN")}"
  printf '   :import (:engine "nova-work write-state-export" :manifest-sha256 "%s" :member-sha256 "%s")\n' "$manifest" "$member"
  printf '   :repeat-member-sha256 "%s"\n' "$rmember"
  printf '   :resume (:partial-new %s :partial-member-sha256 "%s" :new %s :kept %s :member-sha256 "%s")\n' \
    "${pnew:-0}" "$pmember" "${snew:-0}" "${skept:-0}" "$smember"
  printf '   :load (:engine "fresh SBCL %s process, env -i, nova-work state-load + read-loaded-snapshot"\n' "$("$SBCL" --version | awk '{print $2}')"
  printf '          :line "%s" :reexport-sha256 "%s")\n' "$loadline" "$reexport"
  echo '   :records ('
  while IFS=$'\t' read -r id kind map s; do
    printf '    (:id "%s" :kind :%s :original "%s" :mapping "%s")\n' "$id" "$kind" "$s" "$map"
  done < "$WORK/loaded.tsv"
  echo '   )'
  printf '   :gaps (%s))\n' "$(printf '%s' "$gaps" | sed '/^$/d; s/.*/"&"/' | tr '\n' ' ')"
 printf ' :disposition "%s")\n' "$disp"
} > "$OUT"
echo "PILOT $disp repo=$REPO before=${before:0:12} after=${after:0:12} member=${member:0:12} load=${loadline:0:7} calls=$(wc -l < "$CALLS" | tr -d ' ') mutations=$nonget"
echo "WORK $WORK"
