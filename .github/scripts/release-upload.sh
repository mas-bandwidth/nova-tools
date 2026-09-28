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

NOTES_FILE="docs/RELEASE-NOTES-${TAG}.md"
if [ ! -f "$NOTES_FILE" ]; then
  echo "refusing: $NOTES_FILE does not exist" >&2
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
releases=$(gh api --paginate "repos/${GITHUB_REPOSITORY}/releases")
matching_count=$(printf '%s' "$releases" | jq -s --arg tag "$TAG" '[.[][]? | select(.tag_name == $tag and .draft == true)] | length')

if [ "$matching_count" -eq 1 ]; then
  gh release upload "$TAG" --clobber dist/*
  exit 0
elif [ "$matching_count" -eq 0 ]; then
  gh release create "$TAG" --draft --verify-tag --title "$TAG" --notes-file "$NOTES_FILE" dist/*
  exit 0
else
  echo "refusing: ambiguous: $TAG has $matching_count matching drafts" >&2
  exit 1
fi
