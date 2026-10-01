#!/usr/bin/env bash
# Revoke keys in bulk (POST /admin/keys/revoke on the admin listener).
# Revocation takes effect on each key's next request; streams already running
# finish within their max_tokens.
#
# Usage: scripts/revoke-prefix.sh NAME_PREFIX     revoke every key whose name starts with NAME_PREFIX
#        scripts/revoke-prefix.sh --all [--yes]   revoke EVERY key (kill switch); asks to confirm
#                                                 unless --yes is given
# One key only: DELETE /admin/keys/{id} (id is in keys.csv or GET /admin/keys).
#
# Environment: LGAI_ADMIN_TOKEN (or LGAI_ADMIN_TOKEN_FILE), ADMIN_URL: see scripts/admin-lib.sh
#
# Examples:
#   scripts/revoke-prefix.sh lab-          # the whole class, e.g. after a photo of the slips leaked
#   scripts/revoke-prefix.sh loadtest      # the load-test key right after the run (security #12)
#   scripts/revoke-prefix.sh --all --yes   # after the lab
set -euo pipefail
umask 077

# shellcheck source=scripts/admin-lib.sh
. "$(cd "$(dirname "$0")" && pwd)/admin-lib.sh"

usage() {
	sed -n '2,/^set -euo/p' "$0" | sed -e '/^set -euo/d' -e 's/^# \{0,1\}//' >&2
	exit 2
}

all=false
yes=false
prefix=
for arg in "$@"; do
	case $arg in
	--all) all=true ;;
	--yes) yes=true ;;
	-h | --help) usage ;;
	-*) usage ;;
	*)
		[ -z "$prefix" ] || usage
		prefix=$arg
		;;
	esac
done

if $all; then
	[ -z "$prefix" ] || lgai_die "give either NAME_PREFIX or --all, not both"
	if ! $yes; then
		[ -t 0 ] || lgai_die "--all needs --yes when not run interactively"
		printf 'Revoke EVERY key on %s? Type "revoke all" to confirm: ' "${ADMIN_URL:-http://127.0.0.1:8081}" >&2
		read -r answer
		[ "$answer" = "revoke all" ] || lgai_die "not confirmed; nothing revoked"
	fi
	body='{"all":true}'
	what="all keys"
else
	[ -n "$prefix" ] || usage
	[[ $prefix =~ ^[A-Za-z0-9._-]{1,100}$ ]] || lgai_die "NAME_PREFIX must match [A-Za-z0-9._-]{1,100}"
	body=$(printf '{"name_prefix":"%s"}' "$prefix")
	what="keys named $prefix*"
fi

lgai_admin_init
resp=$(mktemp "${TMPDIR:-/tmp}/lgai-revoke.XXXXXX")
trap 'rm -f "$resp"' EXIT
status=$(lgai_admin_call POST /admin/keys/revoke "$body" "$resp") || exit 1
lgai_check_status "$status" "$resp" "revoke"

n=$(lgai_json_number "$resp" revoked)
echo "revoked ${n:-?} key(s): $what"
