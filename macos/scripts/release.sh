#!/bin/bash
# Builds the skillm menu bar app for a release: archives it with the release
# version, signs it (Developer ID, hardened runtime, inside-out), notarizes and
# staples it, zips it, and writes the Sparkle appcast that installed apps read.
# It runs on a Mac that holds the credentials; there is no CI job for the app.
#
#   macos/scripts/release.sh [--dry-run] app-v<X.Y.Z>
#
# The app is versioned and released on its own, apart from the CLI (vX.Y.Z
# tags, goreleaser): it carries no CLI and drives the one the user installed,
# which `api_version` keeps compatible. Its tag is app-vX.Y.Z (X.Y.Z alone is
# taken as that); the version sets MARKETING_VERSION and so CFBundleVersion,
# which Sparkle compares. A release is built from a clean checkout of the tag.
#
# Output, in macos/build/release/<version>/dist/:
#   skillm_<version>_macos_app.zip          the notarized, stapled app
#   skillm_<version>_macos_app.zip.sha256
#   appcast.xml                              the feed the app reads
# macos/scripts/publish-release.sh uploads the zip to the app-v<version>
# release and appcast.xml to the fixed macos-appcast release.
#
# Credentials (none is stored in the repository):
#   signing   the "Developer ID Application" identity of the project's team
#             (DEVELOPMENT_TEAM in macos/project.yml), from the keychain.
#             SKILLM_SIGN_IDENTITY overrides it (a SHA-1 hash or a name).
#   notary    a notarytool keychain profile, SKILLM_NOTARY_PROFILE (default
#             skillm-notary), made with `xcrun notarytool store-credentials`;
#             SKILLM_NOTARY_KEYCHAIN names the keychain holding it (default:
#             the login keychain).
#   Sparkle   the private EdDSA key: SPARKLE_ED_PRIVATE_KEY when set, else the
#             login keychain item generate_keys made (account
#             SKILLM_SPARKLE_ACCOUNT, default skillm).
#
# One-time setup on a Mac that releases (team 6LH2JMGD3J):
#   1. Xcode > Settings > Accounts > Starberry Games GmbH > Manage
#      Certificates > + > Developer ID Application (Account Holder or Admin).
#   2. xcrun notarytool store-credentials skillm-notary \
#        --apple-id <apple-id> --team-id 6LH2JMGD3J
#      (asks for an app-specific password from account.apple.com), or with an
#      App Store Connect API key: --key AuthKey_<key id>.p8 --key-id <key id>
#      --issuer <issuer id>.
#   3. The Sparkle key is already in the login keychain of the Mac that ran
#      generate_keys (C4); on another Mac, import it with
#      generate_keys --account skillm -f sparkle_ed_private_key.txt.
#
# --dry-run tries the pipeline without distribution credentials: any signing
# identity (the Developer ID one when present, else Apple Development), no
# notarization or stapling, a dirty or untagged tree allowed, and the appcast
# only when SPARKLE_ED_PRIVATE_KEY is set. Its output goes to
# macos/build/release/<version>-dry-run/ and must never be published.
#
# SKILLM_RELEASE_DIR overrides macos/build/release; SKILLM_NOTARY_TIMEOUT the
# notarization wait (default 40m).
set -euo pipefail

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}
warn() { printf 'warning: %s\n' "$*" >&2; }
step() { printf '\n==> %s\n' "$*" >&2; }

dry_run=0
version=""
while [ $# -gt 0 ]; do
	case "$1" in
	--dry-run) dry_run=1 ;;
	-h | --help)
		sed -n '2,/^set -euo/{/^set -euo/d;s/^# \{0,1\}//;p;}' "$0"
		exit 0
		;;
	-*) die "unknown option $1" ;;
	*)
		[ -z "$version" ] || die "expected one version, got \"$version\" and \"$1\""
		version=$1
		;;
	esac
	shift
done
[ -n "$version" ] || die "usage: release.sh [--dry-run] app-v<X.Y.Z>"
version=${version#app-v}
[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
	die "\"$version\" is not an app release app-vX.Y.Z (CLI tags are vX.Y.Z and have no app)"
tag=app-v$version

script_dir=$(cd "$(dirname "$0")" && pwd)
macos_dir=$(dirname "$script_dir")
repo_root=$(dirname "$macos_dir")
release_root=${SKILLM_RELEASE_DIR:-$macos_dir/build/release}
if [ "$dry_run" = 1 ]; then
	out=$release_root/$version-dry-run
else
	out=$release_root/$version
fi
derived=$release_root/DerivedData
dist=$out/dist
app=$out/skillm.app
zip_name=skillm_${version}_macos_app.zip

for tool in xcodegen xcodebuild git; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool not found (brew install $tool)"
done

# --- the source ------------------------------------------------------------

step "Checking the checkout"
if [ -n "$(git -C "$repo_root" status --porcelain)" ]; then
	[ "$dry_run" = 1 ] || die "the working tree has changes; release from a clean checkout of $tag"
	warn "the working tree has changes (dry run)"
fi
if ! git -C "$repo_root" tag --points-at HEAD | grep -qx "$tag"; then
	[ "$dry_run" = 1 ] || die "HEAD is not tagged $tag; check out the release tag first"
	warn "HEAD is not tagged $tag (dry run)"
fi
if grep -q 'CODE_SIGN_ENTITLEMENTS' "$macos_dir/project.yml"; then
	# The app is signed here, not by Xcode, so an entitlements file would be
	# dropped silently.
	die "project.yml sets CODE_SIGN_ENTITLEMENTS; teach release.sh to sign the app with them"
fi

team=$(sed -n 's/^[[:space:]]*DEVELOPMENT_TEAM:[[:space:]]*\([A-Z0-9]\{10\}\).*/\1/p' "$macos_dir/project.yml" | head -n 1)
[ -n "$team" ] || die "no DEVELOPMENT_TEAM in macos/project.yml"

# --- the signing identity --------------------------------------------------

step "Finding the signing identity"
identity=${SKILLM_SIGN_IDENTITY:-}
if [ -z "$identity" ]; then
	identity=$("$script_dir/find-identity.sh" "Developer ID Application" "$team")
fi
if [ -z "$identity" ] && [ "$dry_run" = 1 ]; then
	identity=$("$script_dir/find-identity.sh" "Apple Development")
	[ -z "$identity" ] || warn "no Developer ID Application identity; signing with Apple Development (dry run)"
fi
if [ -z "$identity" ]; then
	die "no \"Developer ID Application: … ($team)\" identity in the keychain. Create it in Xcode → Settings → Accounts → (team $team) → Manage Certificates → + → Developer ID Application (needs the Account Holder or Admin role), or set SKILLM_SIGN_IDENTITY"
fi
printf 'identity %s\n' "$identity" >&2

sparkle_account=${SKILLM_SPARKLE_ACCOUNT:-skillm}
notary_profile=${SKILLM_NOTARY_PROFILE:-skillm-notary}
notary_args=(--keychain-profile "$notary_profile")
if [ -n "${SKILLM_NOTARY_KEYCHAIN:-}" ]; then
	notary_args+=(--keychain "$SKILLM_NOTARY_KEYCHAIN")
fi
if [ "$dry_run" = 0 ]; then
	# Fail before the build, not after it.
	xcrun notarytool history "${notary_args[@]}" >/dev/null 2>&1 ||
		die "notarytool profile \"$notary_profile\" does not work. Create it: xcrun notarytool store-credentials $notary_profile --apple-id <apple-id> --team-id $team (an app-specific password from appleid.apple.com), or with an App Store Connect API key (--key, --key-id, --issuer)"
fi

# --- archive ---------------------------------------------------------------

rm -rf "$out"
mkdir -p "$out" "$dist"
if [ "$dry_run" = 1 ]; then
	printf 'Built by release.sh --dry-run: not notarized, never publish.\n' >"$out/DRY-RUN"
fi

step "Generating the Xcode project"
(cd "$macos_dir" && xcodegen generate --quiet)

# Xcode builds the universal app. Nothing is signed here: signing is done
# below, the same way on every Mac, without provisioning or Xcode's account.
step "Archiving the skillm app $version"
xcodebuild archive -quiet \
	-project "$macos_dir/Skillm.xcodeproj" -scheme Skillm -configuration Release \
	-destination 'generic/platform=macOS' \
	-archivePath "$out/skillm.xcarchive" -derivedDataPath "$derived" \
	ONLY_ACTIVE_ARCH=NO \
	MARKETING_VERSION="$version" \
	CODE_SIGNING_ALLOWED=NO
ditto "$out/skillm.xcarchive/Products/Applications/skillm.app" "$app"

sparkle_bin=$derived/SourcePackages/artifacts/sparkle/Sparkle/bin
[ -x "$sparkle_bin/generate_appcast" ] || die "Sparkle's generate_appcast is not at $sparkle_bin"

# --- check what was built --------------------------------------------------

step "Checking the app"
plist=$app/Contents/Info.plist
plist_get() { /usr/libexec/PlistBuddy -c "Print :$1" "$plist" 2>/dev/null || true; }
[ "$(plist_get CFBundleShortVersionString)" = "$version" ] || die "CFBundleShortVersionString is not $version"
[ "$(plist_get CFBundleVersion)" = "$version" ] ||
	die "CFBundleVersion is \"$(plist_get CFBundleVersion)\", not $version: Sparkle compares it with the appcast's version"
# The feed is a fixed release, never "latest": that is the CLI's release,
# which install.sh and skillm upgrade read.
feed=$(plist_get SUFeedURL)
[[ $feed =~ ^https://github\.com/[^/]+/[^/]+/releases/download/macos-appcast/appcast\.xml$ ]] ||
	die "SUFeedURL \"$feed\" is not a GitHub releases/download/macos-appcast/appcast.xml URL"
repo_url=${feed%/releases/download/macos-appcast/appcast.xml}
public_key=$(plist_get SUPublicEDKey)
[ "$(printf '%s' "$public_key" | base64 -D 2>/dev/null | wc -c | tr -d ' ')" = 32 ] ||
	die "SUPublicEDKey \"$public_key\" is not an Ed25519 public key"

# The app carries no CLI: it drives the one the user installed.
[ ! -e "$app/Contents/Helpers" ] || die "the app has Contents/Helpers: it must not bundle a CLI"
[ -f "$app/Contents/Resources/install.sh" ] || die "the app lacks install.sh (Install skillm CLI runs it)"
archs=$(lipo -archs "$app/Contents/MacOS/skillm")
for arch in arm64 x86_64; do
	[[ " $archs " == *" $arch "* ]] || die "the app has no $arch slice ($archs)"
done

# --- sign ------------------------------------------------------------------

# Inside-out, as Sparkle's documentation describes (never --deep): Sparkle's
# XPC services and helpers, Sparkle, then the app.
# Every piece gets the hardened runtime and a secure timestamp, which
# notarization requires. The Downloader service keeps its entitlements.
step "Signing"
sign() { codesign --force --timestamp --options runtime --sign "$identity" "$@"; }
sparkle=$app/Contents/Frameworks/Sparkle.framework
for part in Versions/B/XPCServices/Installer.xpc Versions/B/XPCServices/Downloader.xpc Versions/B/Autoupdate Versions/B/Updater.app; do
	[ -e "$sparkle/$part" ] || die "Sparkle's layout changed: no $part; update the signing order in release.sh"
done
sign "$sparkle/Versions/B/XPCServices/Installer.xpc"
sign --preserve-metadata=entitlements "$sparkle/Versions/B/XPCServices/Downloader.xpc"
sign "$sparkle/Versions/B/Autoupdate"
sign "$sparkle/Versions/B/Updater.app"
sign "$sparkle"
sign "$app"

step "Checking the signatures"
codesign --verify --deep --strict --verbose=2 "$app"
while IFS= read -r -d '' file; do
	case "$(file -b "$file")" in
	*Mach-O*) ;;
	*) continue ;;
	esac
	info=$(codesign -d --verbose=2 "$file" 2>&1) || die "$file is not signed"
	[[ $info == *"TeamIdentifier=$team"* ]] || die "$file is not signed by team $team"
	printf '%s\n' "$info" | grep -q '^CodeDirectory .*flags=.*runtime' || die "$file lacks the hardened runtime"
	[[ $info == *"Timestamp="* ]] || die "$file has no secure timestamp"
	if [ "$dry_run" = 0 ]; then
		[[ $info == *"Authority=Developer ID Application:"* ]] || die "$file is not signed with a Developer ID Application certificate"
	fi
	if codesign -d --entitlements - "$file" 2>/dev/null | grep -q 'get-task-allow'; then
		die "$file has the get-task-allow entitlement, which notarization refuses"
	fi
done < <(find "$app" -type f -perm -u+x -print0)

# --- notarize --------------------------------------------------------------

if [ "$dry_run" = 0 ]; then
	step "Notarizing (this can take a few minutes)"
	mkdir -p "$out/notarize"
	ditto -c -k --sequesterRsrc --keepParent "$app" "$out/notarize/skillm.zip"
	# Submit, keep the submission ID, then wait: if the wait is cut short,
	# submit.json still names the submission, and `xcrun notarytool info <id>`
	# / `log <id>` find it later. On its timeout this script ends a slow
	# notarization and fetches what it can.
	xcrun notarytool submit "$out/notarize/skillm.zip" "${notary_args[@]}" \
		--output-format json >"$out/notarize/submit.json" || true
	submission=$(plutil -extract id raw -o - "$out/notarize/submit.json" 2>/dev/null || true)
	if [ -z "$submission" ]; then
		cat "$out/notarize/submit.json" >&2 || true
		die "notarytool did not accept the upload"
	fi
	printf 'submission %s\n' "$submission" >&2
	xcrun notarytool wait "$submission" "${notary_args[@]}" \
		--timeout "${SKILLM_NOTARY_TIMEOUT:-40m}" --output-format json >"$out/notarize/wait.json" || true
	result=$(plutil -extract status raw -o - "$out/notarize/wait.json" 2>/dev/null || true)
	xcrun notarytool log "$submission" "${notary_args[@]}" "$out/notarize/log.json" >/dev/null 2>&1 ||
		warn "could not fetch the notarization log (there is none until Apple finishes)"
	if [ "$result" != Accepted ]; then
		cat "$out/notarize/wait.json" >&2 || true
		[ -f "$out/notarize/log.json" ] && cat "$out/notarize/log.json" >&2
		die "notarization of submission $submission ended with status \"${result:-unknown}\" (xcrun notarytool info $submission ${notary_args[*]})"
	fi
	xcrun stapler staple "$app"
	xcrun stapler validate "$app"
	if spctl --status 2>&1 | grep -q 'assessments enabled'; then
		assessment=$(spctl --assess --type execute --verbose=2 "$app" 2>&1) || die "Gatekeeper refuses the app: $assessment"
		[[ $assessment == *"source=Notarized Developer ID"* ]] || die "Gatekeeper does not see a notarized app: $assessment"
	else
		# Gatekeeper can be turned off; the stapled ticket was validated above.
		warn "Gatekeeper is off on this Mac; skipping its assessment"
	fi
else
	warn "skipping notarization and stapling (dry run)"
fi

# --- package ---------------------------------------------------------------

step "Packaging"
ditto -c -k --sequesterRsrc --keepParent "$app" "$dist/$zip_name"
(cd "$dist" && shasum -a 256 "$zip_name" >"$zip_name.sha256")

# --- appcast ---------------------------------------------------------------

# One item, the new release, pointing at the zip on its app-v<version>
# release: publish-release.sh replaces the feed on every release, so an
# installed app only ever needs the newest.
download_prefix=$repo_url/releases/download/$tag/
if [ -n "${SPARKLE_ED_PRIVATE_KEY:-}" ] || [ "$dry_run" = 0 ]; then
	step "Writing the appcast"
	archives=$out/appcast-archives
	mkdir -p "$archives"
	cp "$dist/$zip_name" "$archives/"
	appcast_args=(
		--download-url-prefix "$download_prefix"
		--full-release-notes-url "$repo_url/releases/tag/$tag"
		--link "$repo_url"
		-o "$dist/appcast.xml"
	)
	if [ -n "${SPARKLE_ED_PRIVATE_KEY:-}" ]; then
		printf '%s' "$SPARKLE_ED_PRIVATE_KEY" |
			"$sparkle_bin/generate_appcast" --ed-key-file - "${appcast_args[@]}" "$archives"
	else
		# macOS may ask once to let generate_appcast read the key.
		"$sparkle_bin/generate_appcast" --account "$sparkle_account" "${appcast_args[@]}" "$archives"
	fi

	step "Checking the appcast"
	appcast=$dist/appcast.xml
	[ "$(grep -c '<item>' "$appcast")" = 1 ] || die "appcast.xml should hold exactly one item"
	grep -qF "<sparkle:version>$version</sparkle:version>" "$appcast" || die "appcast.xml does not list version $version"
	grep -qF "url=\"$download_prefix$zip_name\"" "$appcast" || die "appcast.xml does not point at $download_prefix$zip_name"
	size=$(stat -f %z "$dist/$zip_name")
	grep -qF "length=\"$size\"" "$appcast" || die "appcast.xml's length is not the zip's size ($size)"
	signature=$(sed -n 's/.*sparkle:edSignature="\([^"]*\)".*/\1/p' "$appcast" | head -n 1)
	[ -n "$signature" ] ||
		die "appcast.xml has no EdDSA signature: generate_appcast signs only with the private key that matches the app's SUPublicEDKey (see its warning above)"
	xcrun swift "$script_dir/verify-update-signature.swift" "$dist/$zip_name" "$signature" "$public_key"
else
	warn "no appcast: set SPARKLE_ED_PRIVATE_KEY to write one in a dry run"
fi

step "Done"
ls -l "$dist" >&2
if [ "$dry_run" = 1 ]; then
	warn "dry run: $dist is not notarized; do not publish it"
else
	printf '\nPublish the %s release and its feed: macos/scripts/publish-release.sh %s\n' "$tag" "$tag" >&2
fi
