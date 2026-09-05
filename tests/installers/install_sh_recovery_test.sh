#!/bin/sh

set -eu
export VELOCITY_NO_MODIFY_PATH=1

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
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
failfs=$test_root/failfs.so
mkdir -p "$payload" "$release" "$install_dir"
cc -Wall -Wextra -Werror -shared -fPIC -o "$failfs" "$script_dir/failfs.c" -ldl
printf 'new velocity' >"$payload/velocity"
printf 'new resolver' >"$payload/velocity-resolver"
tar -czf "$release/$asset" -C "$payload" velocity velocity-resolver
(
	cd "$release"
	sha256sum "$asset" >SHA256SUMS
)
printf 'old velocity' >"$install_dir/velocity"
printf 'old resolver' >"$install_dir/velocity-resolver"

# Force the second publication and every rollback attempt to fail under an unsafe caller umask.
if (umask 000; LD_PRELOAD=$failfs VELOCITY_FAIL_RENAME_DEST=$install_dir/velocity-resolver \
	VELOCITY_FAIL_OPEN_DEST=$install_dir/velocity VELOCITY_RELEASE_BASE_URL=file://$release \
	sh "$installer" --target "$target" --install-dir "$install_dir" >"$test_root/output" 2>&1); then
	fail 'rollback-failure injection unexpectedly succeeded'
fi

# The lock and the only copies of the prior pair must remain for manual recovery.
[ -d "$install_dir/.velocity-install.lock" ] || fail 'rollback failure released the publication lock'
[ "$(stat -c '%a' "$install_dir/.velocity-install.lock")" = 700 ] || fail 'publication lock inherited an unsafe caller umask'
recovery_stage=
for candidate in "$install_dir"/.velocity-install.*; do
	if [ "$candidate" != "$install_dir/.velocity-install.lock" ] && [ -d "$candidate" ]; then
		recovery_stage=$candidate
	fi
done
[ -n "$recovery_stage" ] || fail 'rollback failure removed the recovery stage'
[ -f "$recovery_stage/.previous-velocity" ] || fail 'prior velocity backup was removed'
[ -f "$recovery_stage/.previous-velocity-resolver" ] || fail 'prior resolver backup was removed'
grep -F 'recovery files preserved' "$test_root/output" >/dev/null || fail 'recovery path was not reported'

printf 'install.sh recovery tests passed\n'
