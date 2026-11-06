#!/usr/bin/env bash
# Prints one version's section of CHANGELOG.md, without its heading, and fails
# when the section is missing or empty. "Unreleased" picks that section.
#
#   scripts/changelog-section.sh 0.2.0 [CHANGELOG.md]
#   scripts/changelog-section.sh Unreleased
#
# A version's heading is "## [0.2.0] - 2026-11-06" (Keep a Changelog), and
# "## 0.2.0" or "## v0.2.0" work too.
set -euo pipefail

version="${1#v}"
file="${2:-CHANGELOG.md}"

section="$(awk -v want="$version" '
  /^## / {
    name = $2
    gsub(/^\[|\]$/, "", name)
    sub(/^v/, "", name)
    if (inside) exit
    inside = (tolower(name) == tolower(want))
    next
  }
  # Link definitions at the bottom ([0.2.0]: https://...) end the last section.
  inside && /^\[[^]]+\]: / { exit }
  inside { print }
' "$file")"

# Trim the blank lines around it.
section="$(printf '%s\n' "$section" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')"

if [ -z "$(printf '%s' "$section" | tr -d '[:space:]')" ]; then
  echo "No \"$version\" section with anything in it in $file." >&2
  exit 1
fi
printf '%s\n' "$section"
