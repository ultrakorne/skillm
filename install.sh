#!/bin/sh
# install.sh — download and install the skillm binary.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ultrakorne/skillm/master/install.sh | sh
#
# Honours:
#   SKILLM_VERSION   pin a release tag (default: latest)
#   SKILLM_BIN_DIR   override install dir (default: /usr/local/bin, fallback ~/.local/bin)
#
# POSIX sh, no bashisms.

set -eu

REPO="ultrakorne/skillm"
BINARY="skillm"

err() {
	printf 'error: %s\n' "$1" >&2
	exit 1
}

info() {
	printf '%s\n' "$1" >&2
}

# Pick a download tool.
if command -v curl >/dev/null 2>&1; then
	dl() { curl -fsSL "$1"; }
	dl_out() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	dl() { wget -qO- "$1"; }
	dl_out() { wget -qO "$2" "$1"; }
else
	err "need curl or wget to download skillm"
fi

# Detect OS.
os=$(uname -s)
case "$os" in
	Linux) os="linux" ;;
	Darwin) os="darwin" ;;
	*) err "unsupported OS: $os (skillm supports Linux and macOS)" ;;
esac

# Detect architecture, normalised to goreleaser names.
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) err "unsupported architecture: $arch (skillm supports amd64 and arm64)" ;;
esac

# Resolve the version tag.
version="${SKILLM_VERSION:-}"
if [ -z "$version" ]; then
	info "resolving latest release..."
	version=$(dl "https://api.github.com/repos/${REPO}/releases/latest" \
		| grep '"tag_name"' \
		| head -n1 \
		| sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')
	[ -n "$version" ] || err "could not determine the latest release tag; set SKILLM_VERSION"
fi

# Archive layout must match .goreleaser.yaml: skillm_<version>_<os>_<arch>.tar.gz
# Release tags are prefixed with "v"; the version inside the archive name is not.
ver_no_v=${version#v}
asset="${BINARY}_${ver_no_v}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${version}/${asset}"

info "downloading ${BINARY} ${version} (${os}/${arch})..."

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t skillm)
trap 'rm -rf "$tmp"' EXIT INT TERM

dl_out "$url" "$tmp/$asset" || err "download failed: $url"
tar -xzf "$tmp/$asset" -C "$tmp" || err "failed to extract $asset"

[ -f "$tmp/$BINARY" ] || err "archive did not contain the $BINARY binary"
chmod +x "$tmp/$BINARY"

# Choose an install directory.
bindir="${SKILLM_BIN_DIR:-}"
if [ -z "$bindir" ]; then
	if [ -w /usr/local/bin ] 2>/dev/null; then
		bindir="/usr/local/bin"
	elif [ "$(id -u)" = "0" ]; then
		bindir="/usr/local/bin"
	else
		bindir="$HOME/.local/bin"
	fi
fi

mkdir -p "$bindir" 2>/dev/null || err "cannot create install dir: $bindir"

if mv "$tmp/$BINARY" "$bindir/$BINARY" 2>/dev/null; then
	:
elif command -v sudo >/dev/null 2>&1 && [ "$bindir" = "/usr/local/bin" ]; then
	info "elevating with sudo to write to $bindir"
	sudo mv "$tmp/$BINARY" "$bindir/$BINARY" || err "failed to install to $bindir"
else
	err "cannot write to $bindir (set SKILLM_BIN_DIR to a writable directory)"
fi

info "installed ${BINARY} to ${bindir}/${BINARY}"

# Warn if the install dir is not on PATH.
case ":$PATH:" in
	*":$bindir:"*) ;;
	*) info "note: ${bindir} is not on your PATH; add it to use '${BINARY}' directly" ;;
esac
