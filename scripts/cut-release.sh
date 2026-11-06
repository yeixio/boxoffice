#!/usr/bin/env bash
# Picks the next release tag for the checked-out commit, checks the commit is
# ready to ship, and pushes the tag. The Release workflow runs it when someone
# presses Run workflow; it works the same from a terminal.
#
#   BUMP=patch|minor|major   which part of the last stable version goes up
#   VERSION=v1.2.3[-beta.1]  an exact version instead (pre-releases need it)
#   CHANGELOG=CHANGELOG.md   a stable release needs a "## [1.2.3]" section there
#   CI_WORKFLOW=ci.yml       the commit must have passed this workflow...
#   FORCE=1                  ...unless FORCE is set
#   DRY_RUN=1                check and print the tag, but push nothing
#
# Prints tag=<tag> and prerelease=true|false, to $GITHUB_OUTPUT when set.
set -euo pipefail

BUMP="${BUMP:-patch}"
CI_WORKFLOW="${CI_WORKFLOW:-ci.yml}"
sha="$(git rev-parse HEAD)"

fail() {
  echo "::error title=Not released::$*" >&2
  exit 1
}

# Release tags are vX.Y.Z; any other tags aren't releases.
last="$(git tag -l 'v[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n1 || true)"
last="${last:-v0.0.0}"

# Spaces and a capital V, as phones type it, are fine: " V1.2.3" is v1.2.3.
VERSION="$(printf '%s' "${VERSION:-}" | tr -d '[:space:]')"
VERSION="${VERSION#[vV]}"
if [ -n "$VERSION" ]; then
  tag="v${VERSION}"
elif [ "$last" = v0.0.0 ]; then
  # The first release is 0.1.0, whichever part was picked.
  tag="v0.1.0"
else
  IFS=. read -r major minor patch <<<"${last#v}"
  case "$BUMP" in
    major) tag="v$((major + 1)).0.0" ;;
    minor) tag="v${major}.$((minor + 1)).0" ;;
    patch) tag="v${major}.${minor}.$((patch + 1))" ;;
    *) fail "BUMP must be patch, minor or major, not '$BUMP'." ;;
  esac
fi

[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] ||
  fail "$tag isn't a version like v1.2.3 or v1.2.3-beta.1."
prerelease=false
[[ "$tag" == *-* ]] && prerelease=true

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  fail "$tag already exists."
fi

# A new version must come after the last stable one: v1.2.3-beta.1 is
# older than v1.2.3, so only its v1.2.3 part counts.
core="${tag%%-*}"
newest="$(printf '%s\n%s\n' "$last" "$core" | sort -V | tail -n1)"
if [ "$core" = "$last" ] || [ "$newest" != "$core" ]; then
  fail "$tag doesn't come after the last release, $last."
fi

# Two stable versions on one commit ship the same thing twice. A pre-release
# followed by the stable version on the same commit is fine.
for released in $(git tag --points-at "$sha" -l 'v[0-9]*'); do
  if [[ "$released" != *-* ]]; then
    fail "This commit is already released as $released. Merge something new first."
  fi
done

# The notes come from CHANGELOG.md: rename Unreleased to "## [1.2.3] - <date>"
# in a pull request first.
if [ -n "${CHANGELOG:-}" ] && [ "$prerelease" = false ] &&
  ! scripts/changelog-section.sh "${tag#v}" "$CHANGELOG" >/dev/null; then
  fail "Rename Unreleased in $CHANGELOG to ## [${tag#v}] - $(date -u +%Y-%m-%d) on main first, then release again."
fi

if [ -z "${FORCE:-}" ]; then
  run="$(gh run list --workflow "$CI_WORKFLOW" --commit "$sha" --limit 1 --json databaseId,status,conclusion \
    --jq '.[0] | "\(.databaseId) \(.status) \(.conclusion)"')"
  read -r run_id status conclusion <<<"${run:-none none none}"
  if [ "$run_id" = none ]; then
    fail "$CI_WORKFLOW hasn't run on ${sha:0:7}. Release from a commit on main, or set FORCE."
  fi
  if [ "$status" != completed ]; then
    echo "Waiting for $CI_WORKFLOW on ${sha:0:7} to finish..."
    # Polled rather than gh run watch, which needs checks: read.
    while [ "$status" != completed ]; do
      sleep 30
      read -r status conclusion <<<"$(gh run view "$run_id" --json status,conclusion --jq '"\(.status) \(.conclusion)"')"
    done
  fi
  if [ "$conclusion" != success ]; then
    fail "$CI_WORKFLOW didn't pass on ${sha:0:7} ($conclusion). Fix it first, or set FORCE."
  fi
fi

echo "Releasing ${sha:0:7} as $tag (last release $last)."
if [ -z "${DRY_RUN:-}" ]; then
  git tag -a "$tag" -m "$tag" "$sha"
  git push origin "refs/tags/$tag"
fi

{
  echo "tag=$tag"
  echo "prerelease=$prerelease"
} | tee -a "${GITHUB_OUTPUT:-/dev/null}"
