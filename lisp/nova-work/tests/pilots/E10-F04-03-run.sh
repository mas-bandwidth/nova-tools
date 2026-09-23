#!/usr/bin/env bash
# E10-F04-03 pilot (docs/SPEC-WORK.md:7228-7229), stages (3) and (4):
#   (3) read-only capture of an AUTHORISED real repository, reconciled against
#       the captured records;
#   (4) an import into a DISPOSABLE destination (a temp dir) with the originals
#       untouched: exported, loaded by a fresh process, independently compared,
#       repeated and resumed, every gap listed.
# The authorised repository is mas-bandwidth/nova-pilot, a private sandbox
# (coordinator ruling 2026-09-23 1:35 PM ET); nothing in it is product.
# Every GitHub call goes through ghget, which issues GET only; a non-GET is
# impossible from this script. Auth comes from the caller's environment
# (GH_CONFIG_DIR); no secret value is read or written here.
#
# usage: E10-F04-03-run.sh <import-root-dir> <receipt-out.sexp>
set -euo pipefail
REPO=${PILOT_REPO:-mas-bandwidth/nova-pilot}
ROOT=$1; OUT=$2
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

# import <src-capture> <dest> [limit]: write records into a disposable git repo;
# with a limit, stop after that many (an interrupted import); rerun resumes by
# writing only the records the destination lacks.
import() {
  local s=$1 d=$2 limit=${3:-0} i=0
  [ -d "$d/.git" ] || git init -q "$d"
  mkdir -p "$d/records"
  while IFS=$'\t' read -r id _ _ _; do
    [ -f "$d/records/$id" ] && continue
    cp "$s/records/$id" "$d/records/$id"; i=$((i+1))
    [ "$limit" -gt 0 ] && [ "$i" -ge "$limit" ] && break
  done < "$s/index.tsv"
  git -C "$d" add -A
  git -C "$d" -c user.name=pilot -c user.email=pilot@invalid commit -qm import --allow-empty
}

# load <dest>: a fresh process with an empty environment re-reads the
# destination and prints its own index (independent of the importer).
load() {
  env -i PATH=/usr/bin:/bin bash -c 'cd "$1/records" && for f in *; do printf "%s\t%s\n" "$f" "$(shasum -a 256 < "$f" | cut -c1-64)"; done' _ "$1" | sort
}

before=$(capture "$WORK/before")
dest=$WORK/dest;   import "$WORK/before" "$dest"
repeat=$WORK/repeat; import "$WORK/before" "$repeat"
resume=$WORK/resume; import "$WORK/before" "$resume" 1; import "$WORK/before" "$resume"
after=$(capture "$WORK/after")

want=$(cut -f1,4 "$WORK/before/index.tsv" | sort)
gaps=$(diff <(printf '%s\n' "$want") <(load "$dest") | grep '^[<>]' || true)
tree=$(git -C "$dest" rev-parse HEAD^{tree})
rtree=$(git -C "$repeat" rev-parse HEAD^{tree})
stree=$(git -C "$resume" rev-parse HEAD^{tree})
n=$(wc -l < "$WORK/before/index.tsv" | tr -d ' ')
if [ -z "$gaps" ] && [ "$before" = "$after" ] && [ "$tree" = "$rtree" ] && [ "$tree" = "$stree" ]; then
  disp="INVENTORY OK count=$n"
else
  disp="INVENTORY FAIL"
fi
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
  echo ' :destination (:kind :temp-dir :records ('
  load "$dest" | while IFS=$'\t' read -r id s; do
    k=$(awk -F'\t' -v id="$id" '$1==id{print $2"\t"$3}' "$WORK/before/index.tsv")
    printf '    (:id "%s" :kind :%s :original "%s" :mapping "%s")\n' "$id" "${k%%$'\t'*}" "$s" "${k#*$'\t'}"
  done
  echo '   )'
  printf '   :tree "%s" :repeat-tree "%s" :resume-tree "%s" :gaps (%s))\n' "$tree" "$rtree" "$stree" \
    "$(printf '%s' "$gaps" | sed 's/.*/"&"/' | tr '\n' ' ')"
  printf ' :disposition "%s")\n' "$disp"
} > "$OUT"
echo "PILOT $disp repo=$REPO before=${before:0:12} after=${after:0:12} tree=${tree:0:12} calls=$(wc -l < "$CALLS" | tr -d ' ') mutations=$nonget"
echo "WORK $WORK"
