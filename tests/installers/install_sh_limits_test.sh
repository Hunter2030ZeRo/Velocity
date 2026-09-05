#!/bin/sh

set -eu
export VELOCITY_NO_MODIFY_PATH=1

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

expect_rejected() {
	release_dir=$1
	install_dir=$2
	if VELOCITY_RELEASE_BASE_URL=file://$release_dir \
		sh "$installer" --target "$target" --install-dir "$install_dir" >/dev/null 2>&1; then
		fail "oversized release was accepted: $release_dir"
	fi
	[ ! -e "$install_dir/velocity" ] || fail "oversized release published velocity: $release_dir"
	[ ! -e "$install_dir/velocity-resolver" ] || fail "oversized release published resolver: $release_dir"
}

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH='' cd -- "$script_dir/../.." && pwd)
installer=$repo_root/install.sh
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
target=x86_64-unknown-linux-gnu
asset=velocity-$target.tar.gz

# A manifest larger than 1 MiB is rejected before parsing.
manifest_release=$test_root/manifest-release
mkdir -p "$manifest_release" "$test_root/payload"
printf 'velocity' >"$test_root/payload/velocity"
printf 'resolver' >"$test_root/payload/velocity-resolver"
tar -czf "$manifest_release/$asset" -C "$test_root/payload" velocity velocity-resolver
truncate -s 1048577 "$manifest_release/SHA256SUMS"
expect_rejected "$manifest_release" "$test_root/manifest-install"

# A sparse compressed asset larger than 256 MiB is rejected before copying.
archive_release=$test_root/archive-release
mkdir -p "$archive_release"
truncate -s 268435457 "$archive_release/$asset"
printf 'unused\n' >"$archive_release/SHA256SUMS"
expect_rejected "$archive_release" "$test_root/archive-install"

# A checksum-valid archive with one entry expanding beyond 128 MiB hits the extraction rlimit.
expanded_release=$test_root/expanded-release
expanded_payload=$test_root/expanded-payload
mkdir -p "$expanded_release" "$expanded_payload"
truncate -s 134217729 "$expanded_payload/velocity"
printf 'resolver' >"$expanded_payload/velocity-resolver"
tar -czf "$expanded_release/$asset" -C "$expanded_payload" velocity velocity-resolver
(
	cd "$expanded_release"
	sha256sum "$asset" >SHA256SUMS
)
expect_rejected "$expanded_release" "$test_root/expanded-install"

printf 'install.sh limit tests passed\n'
