#!/usr/bin/env bash
# Synchronize GitHub labels from .github/labels.yml.
# The YAML file is the canonical definition. This script reads it and
# creates or updates each label. It does not delete labels that are absent
# from the file, and it does not add a YAML library: the file is a flat
# list of name, color, and description.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

LABELS_FILE=".github/labels.yml"

if ! command -v gh >/dev/null 2>&1; then
  echo "the GitHub gh CLI is required" >&2
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "gh is not authenticated; run 'gh auth login' and retry" >&2
  gh auth status >&2 || true
  exit 1
fi

if [[ ! -f "$LABELS_FILE" ]]; then
  echo "missing $LABELS_FILE (run this script from the repository root)" >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to read $LABELS_FILE" >&2
  exit 1
fi

python3 - "$LABELS_FILE" <<'PY' | while IFS=$'\t' read -r name color description; do
import sys
from pathlib import Path

path = Path(sys.argv[1])
labels = []
current = None

def unquote(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
        return value[1:-1]
    return value

for lineno, raw in enumerate(path.read_text().splitlines(), start=1):
    stripped = raw.strip()
    if not stripped or stripped.startswith("#"):
        continue
    if stripped.startswith("- name:"):
        if current is not None:
            labels.append(current)
        current = {"name": unquote(stripped.split(":", 1)[1]), "line": lineno}
        continue
    if current is None:
        sys.exit(f"{path}:{lineno}: expected a label name")
    if stripped.startswith("color:"):
        current["color"] = unquote(stripped.split(":", 1)[1]).removeprefix("#")
        continue
    if stripped.startswith("description:"):
        current["description"] = unquote(stripped.split(":", 1)[1])
        continue
    sys.exit(f"{path}:{lineno}: unrecognized label field")

if current is not None:
    labels.append(current)

if not labels:
    sys.exit(f"{path}: no labels found")

for label in labels:
    name = label.get("name", "")
    color = label.get("color", "")
    description = label.get("description", "")
    line = label["line"]
    if not name or not color or not description:
        sys.exit(f"{path}:{line}: each label needs name, color, and description")
    if len(color) != 6 or any(ch not in "0123456789abcdefABCDEF" for ch in color):
        sys.exit(f"{path}:{line}: color must be six hex digits")
    if "\t" in name or "\t" in description or "\n" in description:
        sys.exit(f"{path}:{line}: label fields cannot contain tabs or newlines")
    print(f"{name}\t{color}\t{description}")
PY
  if [[ -z "$name" ]]; then
    echo "empty label name in $LABELS_FILE" >&2
    exit 1
  fi
  echo "Synchronizing label: $name"
  gh label create "$name" \
    --color "$color" \
    --description "$description" \
    --force
done
