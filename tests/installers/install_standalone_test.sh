#!/bin/sh

set -eu
export VELOCITY_NO_MODIFY_PATH=1

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH='' cd -- "$script_dir/../.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

# Given: only the two public installer files, outside the repository layout.
cp "$repo_root/install.sh" "$test_root/install.sh"
cp "$repo_root/install.ps1" "$test_root/install.ps1"

# When: their repository-local dependencies are inspected.
for installer in "$test_root/install.sh" "$test_root/install.ps1"; do
	if grep -F 'scripts/installers/' "$installer" >/dev/null; then
		fail "$installer requires repository-local helper files"
	fi
done

# Then: the PowerShell installer carries each helper consumed by its main flow.
for helper in Resolve-HttpsRedirect Save-HttpsFile Copy-ReleaseFile Get-ExpectedHash \
	Assert-SafeZipDirectory Expand-VelocityArchive Restore-PublishedPair; do
	grep -F "function $helper" "$test_root/install.ps1" >/dev/null || \
		fail "install.ps1 does not embed $helper"
done

printf 'standalone installer layout tests passed\n'
