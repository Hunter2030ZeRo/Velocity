#!/bin/sh

set -eu

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

assert_file_equals() {
	expected_file=$1
	actual_file=$2
	cmp "$expected_file" "$actual_file" || fail "$actual_file did not match"
}

assert_file_contains() {
	expected=$1
	actual_file=$2
	[ "$(cat "$actual_file")" = "$expected" ] || fail "$actual_file had unexpected content"
}

make_payload() {
	payload_dir=$1
	mkdir -p "$payload_dir"
	cat >"$payload_dir/velocity" <<'EOF'
#!/bin/sh
printf 'velocity fixture\n'
EOF
	cat >"$payload_dir/velocity-resolver" <<'EOF'
#!/bin/sh
printf 'resolver fixture\n'
EOF
	chmod 755 "$payload_dir/velocity" "$payload_dir/velocity-resolver"
}

make_release() {
	release_dir=$1
	payload_dir=$2
	asset_name=$3
	mkdir -p "$release_dir"
	tar -czf "$release_dir/$asset_name" -C "$payload_dir" velocity velocity-resolver
	(
		cd "$release_dir"
		sha256sum "$asset_name" >SHA256SUMS
	)
}

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH='' cd -- "$script_dir/../.." && pwd)
installer=$repo_root/install.sh
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

target=x86_64-unknown-linux-gnu
asset=velocity-$target.tar.gz
payload=$test_root/payload
release=$test_root/release
install_dir=$test_root/install

# Given: a checksum manifest and archive containing both sibling executables.
make_payload "$payload"
make_release "$release" "$payload" "$asset"

# When: a pinned release is installed from an offline release directory.
VELOCITY_RELEASE_BASE_URL=file://$release \
	sh "$installer" --version v1.2.3 --target "$target" --install-dir "$install_dir"

# Then: both verified executables are installed together and remain executable.
assert_file_equals "$payload/velocity" "$install_dir/velocity"
assert_file_equals "$payload/velocity-resolver" "$install_dir/velocity-resolver"
[ -x "$install_dir/velocity" ] || fail 'velocity was not executable'
[ -x "$install_dir/velocity-resolver" ] || fail 'resolver was not executable'
[ "$("$install_dir/velocity")" = 'velocity fixture' ] || fail 'velocity did not run'
[ "$("$install_dir/velocity-resolver")" = 'resolver fixture' ] || fail 'resolver did not run'

# Given: a newer checksum-valid release for the same target.
sed 's/velocity fixture/velocity upgraded/' "$payload/velocity" >"$test_root/velocity-upgraded"
sed 's/resolver fixture/resolver upgraded/' "$payload/velocity-resolver" >"$test_root/resolver-upgraded"
mv "$test_root/velocity-upgraded" "$payload/velocity"
mv "$test_root/resolver-upgraded" "$payload/velocity-resolver"
chmod 755 "$payload/velocity" "$payload/velocity-resolver"
make_release "$release" "$payload" "$asset"

# When: the installer is run again for that release.
VELOCITY_RELEASE_BASE_URL=file://$release \
	sh "$installer" --target "$target" --install-dir "$install_dir" >/dev/null

# Then: both siblings are replaced by the verified release.
assert_file_equals "$payload/velocity" "$install_dir/velocity"
assert_file_equals "$payload/velocity-resolver" "$install_dir/velocity-resolver"
[ "$("$install_dir/velocity")" = 'velocity upgraded' ] || fail 'velocity was not upgraded'
[ "$("$install_dir/velocity-resolver")" = 'resolver upgraded' ] || fail 'resolver was not upgraded'

# Given: a target for a different operating system.
os_install=$test_root/os-install

# When: installation is attempted with the Windows target.
if VELOCITY_RELEASE_BASE_URL=file://$release \
	sh "$installer" --target x86_64-pc-windows-msvc --install-dir "$os_install" >/dev/null 2>&1; then
	fail 'non-Linux target was accepted'
fi

# Then: no executable is published.
[ ! -e "$os_install/velocity" ] || fail 'target mismatch published velocity'

# Given: root aliases and a missing release source that makes a regressed path safe to exercise.
root_guard_output=$test_root/root-guard-output

# When: each alias is passed as the installation directory.
for root_alias in /tmp/.. /. //; do
	if VELOCITY_RELEASE_BASE_URL=file://$test_root/missing-release \
		sh "$installer" --target "$target" --install-dir "$root_alias" >"$root_guard_output" 2>&1; then
		fail "root alias was accepted: $root_alias"
	fi

	# Then: canonical validation rejects it before even consulting the missing release.
	grep -F 'refusing to install' "$root_guard_output" >/dev/null || fail "root alias lacked an explicit refusal: $root_alias"
done

# Given: existing binaries and an archive whose bytes no longer match SHA256SUMS.
checksum_install=$test_root/checksum-install
mkdir -p "$checksum_install"
printf 'existing velocity\n' >"$checksum_install/velocity"
printf 'existing resolver\n' >"$checksum_install/velocity-resolver"
cp "$checksum_install/velocity" "$test_root/expected-velocity"
cp "$checksum_install/velocity-resolver" "$test_root/expected-resolver"
printf 'tampered' >>"$release/$asset"

# When: installation is attempted with the invalid checksum.
if VELOCITY_RELEASE_BASE_URL=file://$release \
	sh "$installer" --target "$target" --install-dir "$checksum_install" >/dev/null 2>&1; then
	fail 'checksum mismatch was accepted'
fi

# Then: the existing sibling executables remain untouched.
assert_file_equals "$test_root/expected-velocity" "$checksum_install/velocity"
assert_file_equals "$test_root/expected-resolver" "$checksum_install/velocity-resolver"

# Given: an existing sibling pair and a valid replacement whose second publication will fail.
atomic_payload=$test_root/atomic-payload
atomic_release=$test_root/atomic-release
atomic_install=$test_root/atomic-install
failfs=$test_root/failfs.so
make_payload "$atomic_payload"
make_release "$atomic_release" "$atomic_payload" "$asset"
cc -Wall -Wextra -Werror -shared -fPIC -o "$failfs" "$script_dir/failfs.c" -ldl
mkdir -p "$atomic_install"
printf 'old velocity' >"$atomic_install/velocity"
printf 'old resolver' >"$atomic_install/velocity-resolver"

# When: only the final velocity-resolver publication is rejected.
if LD_PRELOAD=$failfs VELOCITY_FAIL_RENAME_DEST=$atomic_install/velocity-resolver \
	VELOCITY_RELEASE_BASE_URL=file://$atomic_release \
	sh "$installer" --target "$target" --install-dir "$atomic_install" >/dev/null 2>&1; then
	fail 'second publication failure was accepted'
fi

# Then: the complete prior pair was restored.
assert_file_contains 'old velocity' "$atomic_install/velocity"
assert_file_contains 'old resolver' "$atomic_install/velocity-resolver"

# Given: another publisher owns the per-installation lock.
locked_install=$test_root/locked-install
mkdir -p "$locked_install/.velocity-install.lock"
printf 'locked velocity' >"$locked_install/velocity"
printf 'locked resolver' >"$locked_install/velocity-resolver"

# When: another installation attempts to publish into the same directory.
if VELOCITY_RELEASE_BASE_URL=file://$atomic_release \
	sh "$installer" --target "$target" --install-dir "$locked_install" >/dev/null 2>&1; then
	fail 'concurrent publisher bypassed the publication lock'
fi

# Then: neither existing sibling changes.
assert_file_contains 'locked velocity' "$locked_install/velocity"
assert_file_contains 'locked resolver' "$locked_install/velocity-resolver"

# Given: a checksum-valid archive with an unexpected third entry.
layout_payload=$test_root/layout-payload
layout_release=$test_root/layout-release
layout_install=$test_root/layout-install
make_payload "$layout_payload"
printf 'unexpected\n' >"$layout_payload/extra"
mkdir -p "$layout_release"
tar -czf "$layout_release/$asset" -C "$layout_payload" velocity velocity-resolver extra
(
	cd "$layout_release"
	sha256sum "$asset" >SHA256SUMS
)

# When: the unexpected archive layout is installed.
if VELOCITY_RELEASE_BASE_URL=file://$layout_release \
	sh "$installer" --target "$target" --install-dir "$layout_install" >/dev/null 2>&1; then
	fail 'unexpected archive entry was accepted'
fi

# Then: no executable is published.
[ ! -e "$layout_install/velocity" ] || fail 'invalid archive published velocity'
[ ! -e "$layout_install/velocity-resolver" ] || fail 'invalid archive published resolver'

printf 'install.sh tests passed\n'
