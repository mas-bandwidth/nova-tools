#!/usr/bin/env bash
# release-upload.sh: attach the built artifacts to the release for TAG, and never to a
# release that is already published.
#
# Env: GITHUB_REPOSITORY, TAG, GH_TOKEN. dist/* is uploaded. A release that does not exist
# is created as a DRAFT (--draft) so the artifacts sit on a draft until the configured
# verification/publication step, not a parallel published release. A release that already
# exists is uploaded only while it is still a draft; a published release is refused in one
# line rather than extended, so no run can overwrite another release's notes or publish
# assets into a release that already crossed the boundary.
#
# Driven by certification.yml's release dry-run through a fake `gh` (nova-tools#199): the
# fixture answers 200/404 and the `draft` field on the release body decides the branch.
set -euo pipefail
: "${GITHUB_REPOSITORY:?}" "${TAG:?}" "${GH_TOKEN:?}"

NOTES_TAG="docs/RELEASE-NOTES-${TAG}.md"
NOTES_VER="docs/RELEASE-NOTES-${TAG#v}.md"
if [ -f "$NOTES_TAG" ]; then
  NOTES_FILE="$NOTES_TAG"
elif [ -f "$NOTES_VER" ]; then
  NOTES_FILE="$NOTES_VER"
else
  if [ "$NOTES_TAG" = "$NOTES_VER" ]; then
    echo "refusing: $NOTES_TAG does not exist" >&2
  else
    echo "refusing: neither $NOTES_TAG nor $NOTES_VER exists" >&2
  fi
  exit 1
fi

# ONE ASKER, AND IT READS THE STATUS RATHER THAN THE EXIT CODE, for the same reason
# release-certified.sh states: `gh api` exits 1 on a 404 and on no answer alike. 200 and
# 404 are answers; anything else is not, and the whole response is pasted before exit.
ask() {
  local out rc
  set +e
  out=$(gh api -i "repos/${GITHUB_REPOSITORY}/releases/tags/${TAG}" 2>&1); rc=$?
  set -e
  case "$(printf '%s\n' "$out" | head -n 1)" in
    HTTP/*" 200 "*) CODE=200; BODY=$(printf '%s\n' "$out" | sed -e '1,/^[[:space:]]*$/d') ;;
    HTTP/*" 404 "*) CODE=404 ;;
    *)
      echo "asking GitHub about releases/tags/${TAG} answered neither 200 nor 404 (gh exit $rc):"
      printf '%s\n' "$out"
      exit 1
      ;;
  esac
}

ask
if [ "$CODE" = 200 ]; then
  draft=$(printf '%s' "$BODY" | jq -r '.draft // false')
  id=$(printf '%s' "$BODY" | jq -r '.id // "?"')
  if [ "$draft" != true ]; then
    echo "refusing: $TAG already has a published release $id; upload to its draft, never a published release"
    exit 1
  fi
  gh release upload "$TAG" --clobber dist/*
  exit 0
fi

# 404: list drafts from repos/${GITHUB_REPOSITORY}/releases
set +e
releases=$(gh api --paginate "repos/${GITHUB_REPOSITORY}/releases" 2>&1)
api_rc=$?
set -e
if [ "$api_rc" -ne 0 ]; then
  echo "asking GitHub about repos/${GITHUB_REPOSITORY}/releases failed (gh exit $api_rc):" >&2
  printf '%s\n' "$releases" >&2
  exit 1
fi

set +e
summary=$(printf '%s' "$releases" | jq -s --arg tag "$TAG" '
  if length == 0 then
    error("releases listing is empty")
  elif all(.[]; type == "array") then
    if all(.[][]; type == "object" and has("id") and has("tag_name") and has("draft")) then
      [.[][]]
    else
      error("releases listing contains item missing required metadata")
    end
  else
    error("releases listing is not an array")
  end
  | [ .[] | select(.tag_name == $tag) ]
  | {
      published_id: ([ .[] | select(.draft == false) | .id ][0] // null),
      draft_count: ([ .[] | select(.draft == true) ] | length)
    }
' 2>&1)
jq_rc=$?
set -e
if [ "$jq_rc" -ne 0 ]; then
  echo "refusing: releases listing from repos/${GITHUB_REPOSITORY}/releases is invalid or non-array:" >&2
  printf '%s\n' "$summary" >&2
  exit 1
fi

published_id=$(printf '%s' "$summary" | jq -r '.published_id')
draft_count=$(printf '%s' "$summary" | jq -r '.draft_count')

if [ "$published_id" != "null" ]; then
  echo "refusing: $TAG already has a published release $published_id; upload to its draft, never a published release"
  exit 1
fi

if [ "$draft_count" -eq 1 ]; then
  gh release upload "$TAG" --clobber dist/*
  exit 0
elif [ "$draft_count" -eq 0 ]; then
  gh release create "$TAG" --draft --verify-tag --title "$TAG" --notes-file "$NOTES_FILE" dist/*
  exit 0
else
  echo "refusing: ambiguous: $TAG has $draft_count matching drafts" >&2
  exit 1
fi
