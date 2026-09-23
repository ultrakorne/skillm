#!/bin/sh
# Builds the skillm CLI for every architecture Xcode builds the app for,
# joins the slices with lipo, puts the result at
# <app>/Contents/Helpers/skillm and signs it with the app's identity (the
# app's own signature, applied after this phase, then seals it).
#
# Run by the Skillm target's "Embed skillm CLI" build phase, which provides
# SRCROOT, TARGET_BUILD_DIR, CONTENTS_FOLDER_PATH, ARCHS, CONFIGURATION and
# the code-signing variables. SKILLM_VERSION (a build setting, "dev" unless
# a release build overrides it) is stamped into the binary the way
# .goreleaser.yaml does. GO overrides the go binary.
set -eu

: "${SRCROOT:?}" "${TARGET_BUILD_DIR:?}" "${CONTENTS_FOLDER_PATH:?}"
repo_root=$(cd "$SRCROOT/.." && pwd)

# Xcode.app runs build phases with a minimal PATH (xcodebuild keeps the
# caller's): add the usual places a Go toolchain lives, including mise's and
# asdf's shims.
PATH="$PATH:/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:$HOME/go/bin:$HOME/.local/share/mise/shims:$HOME/.asdf/shims"
go_bin=${GO:-$(command -v go || true)}
if [ -z "$go_bin" ]; then
	echo "error: go not found; install Go (brew install go) or set GO" >&2
	exit 1
fi

version=${SKILLM_VERSION:-dev}
if [ "${CONFIGURATION:-}" = "Release" ] && [ "$version" = "dev" ]; then
	# A dev CLI reports `method: dev`: no self-update check, and `upgrade`
	# gives the source-build answer. A release build must set SKILLM_VERSION
	# (and MARKETING_VERSION) from the release tag.
	echo "warning: bundling a skillm CLI stamped \"dev\" in a Release build; set SKILLM_VERSION to the release version" >&2
fi
out_dir="$TARGET_BUILD_DIR/$CONTENTS_FOLDER_PATH/Helpers"
work_dir="${DERIVED_FILE_DIR:-${TMPDIR:-/tmp}}/skillm-cli"
mkdir -p "$out_dir" "$work_dir"

set --
for arch in ${ARCHS:-$(uname -m)}; do
	case "$arch" in
	arm64) goarch=arm64 ;;
	x86_64) goarch=amd64 ;;
	*)
		echo "error: no Go architecture for $arch" >&2
		exit 1
		;;
	esac
	(cd "$repo_root" && CGO_ENABLED=0 GOOS=darwin GOARCH=$goarch "$go_bin" build -trimpath \
		-ldflags "-s -w -X github.com/ultrakorne/skillm/cmd.version=$version" \
		-o "$work_dir/skillm-$goarch" .)
	set -- "$@" "$work_dir/skillm-$goarch"
done
lipo -create "$@" -output "$out_dir/skillm.tmp"
mv -f "$out_dir/skillm.tmp" "$out_dir/skillm"

identity=${EXPANDED_CODE_SIGN_IDENTITY:-}
if [ "${CODE_SIGNING_ALLOWED:-NO}" = "YES" ] && [ -n "$identity" ]; then
	timestamp=--timestamp=none
	if [ "${CONFIGURATION:-}" = "Release" ] && [ "$identity" != "-" ]; then
		timestamp=--timestamp
	fi
	codesign --force --options runtime "$timestamp" --sign "$identity" "$out_dir/skillm"
fi
