#!/usr/bin/env bash
set -euo pipefail

version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

# A retry must reuse the tag already assigned to this commit.
current_tag=$(git tag --points-at HEAD --list 'v*' | grep -E "$version_pattern" | sort -V | tail -n 1 || true)
if [[ -n "$current_tag" ]]; then
  printf '%s\n' "$current_tag"
  exit 0
fi

previous_tag=$(git tag --merged HEAD --list 'v*' | grep -E "$version_pattern" | sort -V | tail -n 1 || true)
if [[ -z "$previous_tag" ]]; then
  printf 'v0.1.0\n'
  exit 0
fi

version=${previous_tag#v}
IFS=. read -r major minor patch <<< "$version"
commits=$(git log --format='%s%n%b' "$previous_tag..HEAD")

if grep -Eq '^[[:alnum:]]+(\([^)]+\))?!:|^BREAKING[- ]CHANGE:' <<< "$commits"; then
  printf 'v%d.0.0\n' "$((major + 1))"
elif grep -Eq '^feat(\([^)]+\))?:' <<< "$commits"; then
  printf 'v%d.%d.0\n' "$major" "$((minor + 1))"
else
  printf 'v%d.%d.%d\n' "$major" "$minor" "$((patch + 1))"
fi
