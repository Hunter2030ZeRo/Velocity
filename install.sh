#!/bin/sh

set -eu
umask 077
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
LC_ALL=C; export PATH LC_ALL
unset GZIP TAR_OPTIONS

velocity_repository=${VELOCITY_REPOSITORY:-Hunter2030ZeRo/Velocity}
velocity_version=${VELOCITY_VERSION:-latest}
velocity_target=${VELOCITY_TARGET:-}
velocity_install_dir=${VELOCITY_INSTALL_DIR:-}
velocity_release_base=${VELOCITY_RELEASE_BASE_URL:-}
velocity_temp_dir=
velocity_stage_dir=
velocity_lock_dir='' velocity_lock_token=''
velocity_publish_pending=0 velocity_recovery_needed=0
velocity_max_archive_bytes=268435456 velocity_max_manifest_bytes=1048576 velocity_max_binary_bytes=134217728

usage() {
	cat <<'EOF'
Usage: install.sh [options]

  --version VERSION      release tag; defaults to latest
  --target TARGET        supported Linux target triple
  --install-dir PATH     defaults to $HOME/.local/bin
  --base-url URL         HTTPS release directory or file:///absolute/path
Environment: VELOCITY_VERSION, VELOCITY_TARGET, VELOCITY_INSTALL_DIR, VELOCITY_RELEASE_BASE_URL, VELOCITY_REPOSITORY.
EOF
}

fail() {
	printf 'velocity installer: %s\n' "$1" >&2
	exit 1
}

cleanup() {
	if [ "$velocity_publish_pending" -eq 1 ]; then
		if restore_previous_pair; then
			velocity_publish_pending=0
		else
			velocity_recovery_needed=1
			printf 'velocity installer: rollback incomplete; recovery files preserved at %s\n' "$velocity_stage_dir" >&2
		fi
	fi
	if [ "$velocity_recovery_needed" -eq 0 ] && [ -n "$velocity_stage_dir" ] && [ -d "$velocity_stage_dir" ]; then
		rm -rf "$velocity_stage_dir"
	fi
	if [ -n "$velocity_temp_dir" ] && [ -d "$velocity_temp_dir" ]; then
		rm -rf "$velocity_temp_dir"
	fi
	if [ "$velocity_recovery_needed" -eq 0 ] && [ -n "$velocity_lock_token" ] && [ -f "$velocity_lock_token" ]; then
		rm -f "$velocity_lock_token"
		rmdir "$velocity_lock_dir" || printf 'velocity installer: could not remove publication lock: %s\n' "$velocity_lock_dir" >&2
	fi
}

restore_previous_pair() {
	for velocity_restore_name in velocity velocity-resolver; do
		velocity_restore_backup=$velocity_stage_dir/.previous-$velocity_restore_name
		velocity_restore_destination=$velocity_install_dir/$velocity_restore_name
		if [ -f "$velocity_restore_backup" ]; then
			cp -p "$velocity_restore_backup" "$velocity_restore_destination" || return 1
		else
			rm -f "$velocity_restore_destination" || return 1
		fi
	done
}

publish_pair() {
	mv -f "$velocity_stage_dir/velocity" "$velocity_install_dir/velocity" || return 1
	mv -f "$velocity_stage_dir/velocity-resolver" "$velocity_install_dir/velocity-resolver"
}

arm_signal_traps() {
	trap 'exit 129' HUP
	trap 'exit 130' INT
	trap 'exit 143' TERM
}

canonicalize_install_dir() {
	[ ! -L "$velocity_install_dir" ] || fail "installation directory must not be a symbolic link: $velocity_install_dir"
	[ -d "$velocity_install_dir" ] || fail "installation path is not a directory: $velocity_install_dir"
	velocity_install_dir=$(CDPATH='' cd -- "$velocity_install_dir" && pwd -P) || fail 'could not resolve installation directory'
	case $velocity_install_dir in
	/ | //) fail 'refusing to install directly into the filesystem root' ;;
	esac
}

detect_target() {
	case $(uname -m) in
	x86_64 | amd64) printf '%s\n' x86_64-unknown-linux-gnu ;;
	aarch64 | arm64) printf '%s\n' aarch64-unknown-linux-gnu ;;
	*) fail "unsupported architecture: $(uname -m)" ;;
	esac
}

download_file() {
	velocity_download_url=$1
	velocity_download_destination=$2
	velocity_download_max=$3
	velocity_download_blocks=$(((velocity_download_max + 511) / 512))
	case $velocity_download_url in
	file:///*)
		velocity_download_source=${velocity_download_url#file://}
		[ -f "$velocity_download_source" ] || fail "release file not found: $velocity_download_source"
		[ "$(wc -c <"$velocity_download_source")" -le "$velocity_download_max" ] || fail "release file exceeds $velocity_download_max bytes"
		(ulimit -f "$velocity_download_blocks" && cp "$velocity_download_source" "$velocity_download_destination") || fail 'could not copy bounded release file'
		;;
	https://*)
		(ulimit -f "$velocity_download_blocks" && curl -q --fail --location --silent --show-error --retry 3 \
			--connect-timeout 15 --max-redirs 10 --proto '=https' --proto-redir '=https' \
			--max-filesize "$velocity_download_max" --max-time 300 --speed-limit 1024 --speed-time 30 \
			--output "$velocity_download_destination" "$velocity_download_url") || fail 'could not download bounded release file'
		;;
	*)
		fail 'release URLs must use HTTPS or an explicit file:/// path'
		;;
	esac
	[ "$(wc -c <"$velocity_download_destination")" -le "$velocity_download_max" ] || fail "release file exceeds $velocity_download_max bytes"
}

while [ "$#" -gt 0 ]; do
	case $1 in
	--version) [ "$#" -ge 2 ] || fail '--version requires a value'; velocity_version=$2; shift 2 ;;
	--target) [ "$#" -ge 2 ] || fail '--target requires a value'; velocity_target=$2; shift 2 ;;
	--install-dir) [ "$#" -ge 2 ] || fail '--install-dir requires a value'; velocity_install_dir=$2; shift 2 ;;
	--base-url) [ "$#" -ge 2 ] || fail '--base-url requires a value'; velocity_release_base=$2; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*) fail "unknown option: $1" ;;
	esac
done

velocity_host_os=$(uname -s); [ "$velocity_host_os" = Linux ] || fail "unsupported operating system: $velocity_host_os"

case $velocity_version in
'' | *[!A-Za-z0-9._-]*) fail "invalid release version: $velocity_version" ;;
esac
case $velocity_repository in
'' | /* | */ | */*/* | *[!A-Za-z0-9._/-]*) fail "invalid repository: $velocity_repository" ;;
esac

if [ -z "$velocity_target" ]; then
	velocity_target=$(detect_target)
fi
case $velocity_target in
x86_64-unknown-linux-gnu | aarch64-unknown-linux-gnu | \
	x86_64-unknown-linux-musl | aarch64-unknown-linux-musl) ;;
*) fail "unsupported Linux target: $velocity_target" ;;
esac

if [ -z "$velocity_install_dir" ]; then
	[ -n "${HOME:-}" ] || fail 'HOME is not set; pass --install-dir'
	velocity_install_dir=$HOME/.local/bin
fi
[ -n "$velocity_install_dir" ] || fail 'installation directory is empty'
if [ -e "$velocity_install_dir" ] || [ -L "$velocity_install_dir" ]; then
	canonicalize_install_dir
fi

if [ -z "$velocity_release_base" ]; then
	if [ "$velocity_version" = latest ]; then
		velocity_release_base=https://github.com/$velocity_repository/releases/latest/download
	else
		velocity_release_base=https://github.com/$velocity_repository/releases/download/$velocity_version
	fi
fi
velocity_release_base=${velocity_release_base%/}
case $velocity_release_base in
https://* | file:///*) ;;
*) fail 'release base must use HTTPS or an explicit file:/// path' ;;
esac

for velocity_command in awk chmod cp mktemp mv sha256sum tar timeout uname wc; do
	command -v "$velocity_command" >/dev/null 2>&1 || fail "required command not found: $velocity_command"
done
case $velocity_release_base in
https://*) command -v curl >/dev/null 2>&1 || fail 'required command not found: curl' ;;
esac

velocity_asset=velocity-$velocity_target.tar.gz
velocity_temp_dir=$(mktemp -d /tmp/velocity-install.XXXXXX) || fail 'could not create temporary directory'
trap cleanup EXIT
arm_signal_traps
velocity_archive=$velocity_temp_dir/$velocity_asset
velocity_checksums=$velocity_temp_dir/SHA256SUMS

download_file "$velocity_release_base/$velocity_asset" "$velocity_archive" "$velocity_max_archive_bytes"
download_file "$velocity_release_base/SHA256SUMS" "$velocity_checksums" "$velocity_max_manifest_bytes"

if ! velocity_expected_hash=$(awk -v asset="$velocity_asset" '
NF == 2 {
	name = $2; sub(/^\*/, "", name)
	if (name == asset && length($1) == 64 && $1 !~ /[^0-9A-Fa-f]/) {
		matches++; hash = tolower($1)
	}
}
END { if (matches != 1) exit 1; print hash }' "$velocity_checksums"); then
	fail "SHA256SUMS must contain exactly one checksum for $velocity_asset"
fi
velocity_actual_hash=$(sha256sum "$velocity_archive")
velocity_actual_hash=${velocity_actual_hash%% *}
[ "$velocity_actual_hash" = "$velocity_expected_hash" ] || fail "checksum mismatch for $velocity_asset"

velocity_listing=$velocity_temp_dir/archive-entries
(ulimit -f 8 && timeout 120 tar -tzf "$velocity_archive" >"$velocity_listing") || fail "could not inspect bounded $velocity_asset"
velocity_cli_entries=0
velocity_resolver_entries=0
while IFS= read -r velocity_entry; do
	case $velocity_entry in
	velocity) velocity_cli_entries=$((velocity_cli_entries + 1)) ;;
	velocity-resolver) velocity_resolver_entries=$((velocity_resolver_entries + 1)) ;;
	*) fail "unexpected archive entry: $velocity_entry" ;;
	esac
done <"$velocity_listing"
[ "$velocity_cli_entries" -eq 1 ] || fail 'archive must contain velocity exactly once'
[ "$velocity_resolver_entries" -eq 1 ] || fail 'archive must contain velocity-resolver exactly once'

velocity_extract_dir=$velocity_temp_dir/extracted
mkdir -p "$velocity_extract_dir"
velocity_tar_limit_blocks=$((velocity_max_binary_bytes / 512))
(ulimit -f "$velocity_tar_limit_blocks" && timeout 120 tar -xzf "$velocity_archive" -C "$velocity_extract_dir" velocity velocity-resolver) || fail 'could not safely extract release archive'
velocity_expanded_bytes=0
for velocity_binary in velocity velocity-resolver; do
	velocity_binary_path=$velocity_extract_dir/$velocity_binary
	if [ ! -f "$velocity_binary_path" ] || [ -L "$velocity_binary_path" ] || [ ! -s "$velocity_binary_path" ]; then
		fail "archive entry is not a non-empty regular file: $velocity_binary"
	fi
	velocity_binary_bytes=$(wc -c <"$velocity_binary_path")
	[ "$velocity_binary_bytes" -le "$velocity_max_binary_bytes" ] || fail "archive entry exceeds $velocity_max_binary_bytes bytes: $velocity_binary"
	velocity_expanded_bytes=$((velocity_expanded_bytes + velocity_binary_bytes))
done
[ "$velocity_expanded_bytes" -le "$velocity_max_archive_bytes" ] || fail "archive expands beyond $velocity_max_archive_bytes bytes"

mkdir -p "$velocity_install_dir"
canonicalize_install_dir
velocity_lock_candidate=$velocity_install_dir/.velocity-install.lock
trap '' HUP INT TERM
if ! mkdir "$velocity_lock_candidate" 2>/dev/null; then
	arm_signal_traps
	fail "another installation is publishing, or a stale lock exists: $velocity_lock_candidate"
fi
velocity_lock_dir=$velocity_lock_candidate
velocity_lock_token=$(mktemp "$velocity_lock_dir/owner.XXXXXX") || {
	rmdir "$velocity_lock_dir"; arm_signal_traps; fail 'could not record publication lock ownership'
}
arm_signal_traps
velocity_stage_dir=$(mktemp -d "$velocity_install_dir/.velocity-install.XXXXXX") || fail 'could not create installation stage'
cp "$velocity_extract_dir/velocity" "$velocity_stage_dir/velocity"
cp "$velocity_extract_dir/velocity-resolver" "$velocity_stage_dir/velocity-resolver"
chmod 755 "$velocity_stage_dir/velocity" "$velocity_stage_dir/velocity-resolver"

for velocity_binary in velocity velocity-resolver; do
	velocity_destination=$velocity_install_dir/$velocity_binary
	if [ -e "$velocity_destination" ] || [ -L "$velocity_destination" ]; then
		if [ ! -f "$velocity_destination" ] || [ -L "$velocity_destination" ]; then
			fail "existing destination is not a regular file: $velocity_destination"
		fi
		cp -p "$velocity_destination" "$velocity_stage_dir/.previous-$velocity_binary"
	fi
done
velocity_publish_pending=1
if ! publish_pair; then
	if ! restore_previous_pair; then
		fail 'publication failed and the previous sibling pair could not be fully restored'
	fi
	velocity_publish_pending=0
	fail 'could not publish both executables; the previous sibling pair was restored'
fi
velocity_publish_pending=0

printf 'Installed Velocity (%s) to %s\n' "$velocity_target" "$velocity_install_dir"
case :${PATH:-}: in
*:$velocity_install_dir:*) ;;
*) printf 'Add %s to PATH to run velocity.\n' "$velocity_install_dir" ;;
esac
