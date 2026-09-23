#!/bin/bash
# Prints the SHA-1 of the first valid codesigning identity of a certificate
# kind in the keychain search list, or nothing when there is none.
#
#   macos/scripts/find-identity.sh <certificate kind> [team ID]
#
# e.g. find-identity.sh "Developer ID Application" 6LH2JMGD3J matches
# "Developer ID Application: Starberry Games GmbH (6LH2JMGD3J)". The name is
# matched as fixed strings, never as a regular expression, so the team's
# parentheses are literal. release.sh uses it; test-find-identity.sh tests it.
set -euo pipefail

[ $# -ge 1 ] && [ $# -le 2 ] || {
	printf 'usage: find-identity.sh <certificate kind> [team ID]\n' >&2
	exit 2
}
kind=$1
team=${2:-}

{ security find-identity -v -p codesigning 2>/dev/null || true; } |
	{ grep -F "\"$kind: " || true; } |
	{ if [ -n "$team" ]; then grep -F "($team)\"" || true; else cat; fi; } |
	sed -n 's/^ *[0-9][0-9]*) \([0-9A-F]\{40\}\) .*/\1/p' | head -n 1
