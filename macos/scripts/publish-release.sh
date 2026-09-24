#!/bin/bash
# Publishes what release.sh built: the zipped app and its SHA-256 go to the
# app's own GitHub release (mac-v<version>, created here with --latest=false
# when it does not exist yet: "latest" stays the CLI's release, which
# install.sh and skillm upgrade read), then appcast.xml replaces the feed on
# the fixed macos-appcast release, which installed apps read
# (releases/download/macos-appcast/appcast.xml). Existing assets of the same
# name are replaced, so a failed run can be repeated.
#
#   macos/scripts/publish-release.sh mac-v<X.Y.Z>
#
# The mac-v<version> tag must be pushed first (git push origin mac-v<version>).
# Needs the GitHub CLI, logged in (or GH_TOKEN). SKILLM_RELEASE_DIR as in
# release.sh.
set -euo pipefail

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

[ $# = 1 ] || die "usage: publish-release.sh mac-v<X.Y.Z>"
version=${1#mac-v}
[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || die "\"$1\" is not mac-vX.Y.Z"
tag=mac-v$version
feed_tag=macos-appcast

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
repo=$(printf '%s\n' "$feed" | sed -n "s|^https://github\\.com/\\([^/]*/[^/]*\\)/releases/download/$feed_tag/appcast\\.xml\$|\\1|p")
[ -n "$repo" ] || die "cannot read the GitHub repository from SUFeedURL \"$feed\""
grep -qF "url=\"https://github.com/$repo/releases/download/$tag/$zip_name\"" "$dist/appcast.xml" ||
	die "appcast.xml does not point at the $tag release's $zip_name"

command -v gh >/dev/null 2>&1 || die "the GitHub CLI (gh) is not installed"

notes="The skillm menu bar app $version for macOS. Installed apps offer it in their menu; it drives the skillm CLI, which is released separately."
if [ -e "$out/NOT-NOTARIZED" ]; then
	notes+="

This build is not notarized. Unzip it, move skillm.app to Applications and open it; when macOS blocks it, go to System Settings > Privacy & Security and click Open Anyway (or run \`xattr -dr com.apple.quarantine /Applications/skillm.app\`). Updates from the app's menu open without asking."
fi

# The app's release first, so the feed never names a zip that is not there.
if ! gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
	gh release create "$tag" --repo "$repo" --verify-tag --latest=false \
		--title "skillm for macOS $version" --notes "$notes" ||
		die "could not create the $tag release (push the tag first: git push origin $tag)"
fi
gh release upload "$tag" --repo "$repo" --clobber "$dist/$zip_name" "$dist/$zip_name.sha256"

if ! gh release view "$feed_tag" --repo "$repo" >/dev/null 2>&1; then
	gh release create "$feed_tag" --repo "$repo" --latest=false \
		--title "skillm app update feed" \
		--notes "Holds appcast.xml, the feed installed skillm apps read. Replaced by every app release; download the app from its mac-vX.Y.Z release."
fi
gh release upload "$feed_tag" --repo "$repo" --clobber "$dist/appcast.xml"
printf 'Uploaded %s and its .sha256 to %s %s, and appcast.xml to %s\n' "$zip_name" "$repo" "$tag" "$feed_tag" >&2
