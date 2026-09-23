#!/bin/bash
# Uploads what release.sh built to the GitHub release of its tag: the zipped
# app, its SHA-256 and appcast.xml (installed apps read
# releases/latest/download/appcast.xml). Existing assets of the same name are
# replaced, so a failed run can be repeated.
#
#   macos/scripts/publish-release.sh <version>
#
# The release must already exist with goreleaser's darwin archives and
# checksums.txt: the bundled CLI only reports a newer skillm (and so the app
# only asks Sparkle) when the release carries its platform's archive. Needs
# the GitHub CLI, logged in (or GH_TOKEN). SKILLM_RELEASE_DIR as in release.sh.
set -euo pipefail

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

[ $# = 1 ] || die "usage: publish-release.sh <version>"
version=${1#v}
[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "version \"$version\" is not X.Y.Z"
tag=v$version

script_dir=$(cd "$(dirname "$0")" && pwd)
macos_dir=$(dirname "$script_dir")
out=${SKILLM_RELEASE_DIR:-$macos_dir/build/release}/$version
dist=$out/dist
zip_name=skillm_${version}_macos_app.zip

[ ! -e "$out/DRY-RUN" ] || die "$out was built by a dry run; never publish it"
for f in "$zip_name" "$zip_name.sha256" appcast.xml; do
	[ -f "$dist/$f" ] || die "$dist/$f is missing; run macos/scripts/release.sh $tag first"
done
(cd "$dist" && shasum -a 256 -c "$zip_name.sha256" >/dev/null) || die "$zip_name does not match its .sha256"

# The repository is the one the app's feed names.
feed=$(/usr/libexec/PlistBuddy -c 'Print :SUFeedURL' "$out/skillm.app/Contents/Info.plist")
repo=$(printf '%s\n' "$feed" | sed -n 's|^https://github\.com/\([^/]*/[^/]*\)/releases/latest/download/appcast\.xml$|\1|p')
[ -n "$repo" ] || die "cannot read the GitHub repository from SUFeedURL \"$feed\""

command -v gh >/dev/null 2>&1 || die "the GitHub CLI (gh) is not installed"
assets=$(gh release view "$tag" --repo "$repo" --json assets --jq '.assets[].name') ||
	die "no $tag release in $repo yet; goreleaser creates it"
for need in "skillm_${version}_darwin_arm64.tar.gz" "skillm_${version}_darwin_amd64.tar.gz" checksums.txt; do
	printf '%s\n' "$assets" | grep -qxF "$need" ||
		die "the $tag release has no $need: the app's CLI could not see this release, so the app would never offer it"
done

gh release upload "$tag" --repo "$repo" --clobber \
	"$dist/$zip_name" "$dist/$zip_name.sha256" "$dist/appcast.xml"
printf 'Uploaded %s, its .sha256 and appcast.xml to %s %s\n' "$zip_name" "$repo" "$tag" >&2
