#!/usr/bin/env bash
# Publishes the GitHub release of the version in build/darwin-arm64, with
# its signed and notarized disk image, after actnow.dev took it
# (.github/workflows/release.yml, step "GitHub release"; docs/release.md).
# People who come from GitHub, awesome lists or Homebrew download it there
# without an account; installed copies keep updating from actnow.dev.
#
#   scripts/release-github.sh [vVERSION]
#
# A beta (a version with a "-") is a prerelease. When the release exists
# already, its title and notes are set again and the disk image replaced.
# Needs gh and GH_TOKEN (or GITHUB_TOKEN); GITHUB_REPOSITORY defaults to
# hehehai/Auralink.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
fail() { echo "release-github: $*" >&2; exit 1; }
command -v jq >/dev/null || fail "needs jq"
command -v gh >/dev/null || fail "needs the GitHub CLI (gh)"
[[ -n "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ]] || fail "GH_TOKEN is not set"

version="$(jq -r .version mygo.json)"
min_macos="$(jq -r '.macos.minimumSystemVersion // "15.0"' mygo.json)"
tag="${1:-v$version}"
[[ "$tag" == "v$version" ]] || fail "the tag $tag is not v$version, the version in mygo.json"
repo="${GITHUB_REPOSITORY:-hehehai/Auralink}"

shopt -s nullglob
images=(build/darwin-arm64/*.dmg)
(( ${#images[@]} == 1 )) || fail "expected one disk image in build/darwin-arm64, found ${#images[@]}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
dmg="$work/Auralink-$version-arm64.dmg"
cp "${images[0]}" "$dmg"

notes="$work/notes.md"
{
  printf '**Download:** Auralink-%s-arm64.dmg below (signed and notarized; macOS %s+, Apple silicon), or https://actnow.dev/auralink\n\n' "$version" "$min_macos"
  awk -v version="$version" '
    index($0, "## [" version "]") == 1 || $0 == "## " version { found = 1; next }
    found && /^## / { exit }
    found { print }
  ' CHANGELOG.md
} >"$notes"
grep -q '[^[:space:]]' <(tail -n +3 "$notes") || fail "CHANGELOG.md has no section for $version"

title="Auralink $version"
beta=()
[[ "$version" == *-* ]] && beta=(--prerelease)
if gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
  gh release edit "$tag" --repo "$repo" --title "$title" --notes-file "$notes" ${beta[@]+"${beta[@]}"}
  gh release upload "$tag" --repo "$repo" --clobber "$dmg"
else
  gh release create "$tag" --repo "$repo" --verify-tag --title "$title" --notes-file "$notes" ${beta[@]+"${beta[@]}"} "$dmg"
fi
echo "release-github: $repo $tag published with Auralink-$version-arm64.dmg"
