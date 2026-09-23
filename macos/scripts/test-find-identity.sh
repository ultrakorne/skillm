#!/bin/bash
# Tests find-identity.sh against a stub `security` (no keychain or
# certificate needed). Run by the release workflow before it builds.
#
#   macos/scripts/test-find-identity.sh
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
stub=$(mktemp -d)
trap 'rm -rf "$stub"' EXIT

dev=1111111111111111111111111111111111111111
other=2222222222222222222222222222222222222222
ours=3333333333333333333333333333333333333333
cat >"$stub/security" <<STUB
#!/bin/bash
[ "\${SECURITY_STUB_EMPTY:-}" = 1 ] && { echo "     0 valid identities found"; exit 0; }
cat <<'OUT'
  1) $dev "Apple Development: Samir Hadi (ABCDE12345)"
  2) $other "Developer ID Application: Someone Else (ZZZZZZZZZZ)"
  3) $ours "Developer ID Application: Starberry Games GmbH (6LH2JMGD3J)"
     3 valid identities found
OUT
STUB
chmod +x "$stub/security"

failed=0
check() { # check <want> <args...>
	local want=$1 got
	shift
	got=$(PATH="$stub:$PATH" "$script_dir/find-identity.sh" "$@")
	if [ "$got" = "$want" ]; then
		printf 'ok    %s -> %s\n' "$*" "${got:-(none)}"
	else
		printf 'FAIL  %s -> %s, want %s\n' "$*" "${got:-(none)}" "${want:-(none)}"
		failed=1
	fi
}

check "$ours" "Developer ID Application" 6LH2JMGD3J
check "$other" "Developer ID Application" ZZZZZZZZZZ
check "" "Developer ID Application" ABCDE12345
check "" "Developer ID Application" 6LH2JMGD3
check "$other" "Developer ID Application"
check "$dev" "Apple Development"
check "" "Developer ID Installer" 6LH2JMGD3J
SECURITY_STUB_EMPTY=1 check "" "Developer ID Application" 6LH2JMGD3J

exit "$failed"
