#!/usr/bin/env bash
# Writes the GitHub release notes for a tag to stdout: the tag's section of
# CHANGELOG.md (Unreleased for a pre-release), how to install, then the merged
# pull requests GitHub lists. The Release workflow uses it.
#
#   scripts/release-notes.sh v0.2.0 > notes.md
set -euo pipefail

tag="$1"
repo="${GITHUB_REPOSITORY:-yeixio/boxoffice}"
owner="${repo%%/*}"
version="${tag#v}"
here="$(dirname "$0")"

if [[ "$tag" == *-* ]]; then
  # A pre-release ships what's under Unreleased so far.
  "$here/changelog-section.sh" "${version%%-*}" 2>/dev/null ||
    "$here/changelog-section.sh" Unreleased 2>/dev/null || true
else
  "$here/changelog-section.sh" "$version"
fi

cat <<NOTES

### Install

- **Docker:** \`docker pull ghcr.io/${repo}:${version}\`
- **Kubernetes:** \`helm install boxoffice oci://ghcr.io/${owner}/charts/boxoffice --version ${version}\`
NOTES

if [[ "$tag" == *-* ]]; then
  printf '\n**Pre-release.** `latest` stays on the last stable release; pull `%s` to try this one.\n' "$version"
fi

# The tag must already be on GitHub for this part.
if [ -z "${NO_PR_LIST:-}" ]; then
  echo
  gh api "repos/${repo}/releases/generate-notes" -f tag_name="$tag" --jq .body
fi
