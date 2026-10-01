#!/usr/bin/env bash
# Maintenance switch (PUT /admin/maintenance on the admin listener).
# on:     chat and task requests get 503 "maintenance" with Retry-After;
#         /healthz, /readyz, /v1/models and the admin API keep working.
# off:    back to normal.
# status: print whether maintenance is on (GET /admin/maintenance).
#
# Usage: scripts/maintenance.sh on|off|status
# Environment: LGAI_ADMIN_TOKEN (or LGAI_ADMIN_TOKEN_FILE), ADMIN_URL: see scripts/admin-lib.sh
set -euo pipefail
umask 077

# shellcheck source=scripts/admin-lib.sh
. "$(cd "$(dirname "$0")" && pwd)/admin-lib.sh"

case ${1:-} in
on | off | status) action=$1 ;;
*)
	sed -n '2,/^set -euo/p' "$0" | sed -e '/^set -euo/d' -e 's/^# \{0,1\}//' >&2
	exit 2
	;;
esac
[ $# -eq 1 ] || lgai_die "usage: maintenance.sh on|off|status"

lgai_admin_init
resp=$(mktemp "${TMPDIR:-/tmp}/lgai-maint.XXXXXX")
trap 'rm -f "$resp"' EXIT

case $action in
status)
	status=$(lgai_admin_call GET /admin/maintenance "" "$resp") || exit 1
	lgai_check_status "$status" "$resp" "maintenance status"
	if tr -d ' \n' <"$resp" | grep -q '"enabled":true'; then
		echo "maintenance is ON"
	else
		echo "maintenance is OFF"
	fi
	;;
on)
	status=$(lgai_admin_call PUT /admin/maintenance '{"enabled":true}' "$resp") || exit 1
	lgai_check_status "$status" "$resp" "maintenance on"
	echo "maintenance ON: generation returns 503 maintenance; health, models and admin still work"
	echo "turn it off with: bash scripts/maintenance.sh off"
	;;
off)
	status=$(lgai_admin_call PUT /admin/maintenance '{"enabled":false}' "$resp") || exit 1
	lgai_check_status "$status" "$resp" "maintenance off"
	echo "maintenance OFF: generation is open again"
	;;
esac
