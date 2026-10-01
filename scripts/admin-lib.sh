# shellcheck shell=bash
# Shared helpers for scripts/mint-keys.sh, revoke-prefix.sh and maintenance.sh.
# Source it; do not run it. Works with macOS bash 3.2, curl and POSIX tools.
#
# Environment:
#   LGAI_ADMIN_TOKEN       the admin token (or LGAI_ADMIN_TOKEN_FILE: a file holding it).
#                          Never accepted as an argument.
#   ADMIN_URL              the admin listener, default http://127.0.0.1:8081.
#                          Server: ssh -N -L 8081:127.0.0.1:8081 <gateway host>, then the default works.
#
# The token never appears on a command line: curl reads the Authorization
# header from a config file on a pipe (process substitution), so it is not
# visible in ps. --noproxy '*' keeps it away from any HTTP(S)_PROXY.

lgai_die() {
	printf '%s: %s\n' "${0##*/}" "$*" >&2
	exit 1
}

lgai_admin_init() {
	command -v curl >/dev/null 2>&1 || lgai_die "curl is required"
	ADMIN_URL=${ADMIN_URL:-http://127.0.0.1:8081}
	ADMIN_URL=${ADMIN_URL%/}
	if [ -z "${LGAI_ADMIN_TOKEN:-}" ] && [ -n "${LGAI_ADMIN_TOKEN_FILE:-}" ]; then
		[ -r "$LGAI_ADMIN_TOKEN_FILE" ] || lgai_die "cannot read LGAI_ADMIN_TOKEN_FILE"
		LGAI_ADMIN_TOKEN=$(tr -d '\r\n' <"$LGAI_ADMIN_TOKEN_FILE")
	fi
	[ -n "${LGAI_ADMIN_TOKEN:-}" ] ||
		lgai_die "set LGAI_ADMIN_TOKEN (or LGAI_ADMIN_TOKEN_FILE) in the environment; it is never taken as an argument"
	case $LGAI_ADMIN_TOKEN in
	*'"'* | *'\'* | *' '*) lgai_die "LGAI_ADMIN_TOKEN contains a quote, backslash or space" ;;
	esac
}

# lgai_admin_call METHOD PATH JSON_BODY OUTFILE
# Writes the response body to OUTFILE and prints the HTTP status code.
# An empty JSON_BODY sends no body (GET).
lgai_admin_call() {
	local data=()
	[ -z "$3" ] || data=(-H 'Content-Type: application/json' --data-binary "$3")
	# ${data[@]+...}: bash 3.2 with set -u treats an empty array as unbound.
	curl -sS --noproxy '*' --max-time 30 -X "$1" \
		-K <(printf 'header = "Authorization: Bearer %s"\n' "$LGAI_ADMIN_TOKEN") \
		-H 'Accept: application/json' ${data[@]+"${data[@]}"} \
		-o "$4" -w '%{http_code}' "$ADMIN_URL$2" ||
		lgai_die "cannot reach $ADMIN_URL (gateway running with LGAI_ADMIN_TOKEN set? on the server: ssh -N -L 8081:127.0.0.1:8081 <host>)"
}

# lgai_error_text FILE: the "message" and "code" of an OpenAI-style error body.
lgai_error_text() {
	local msg code
	msg=$(sed -n 's/.*"message"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$1" | head -n 1)
	code=$(sed -n 's/.*"code"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$1" | head -n 1)
	if [ -n "$msg$code" ]; then
		printf '%s (%s)' "${msg:-error}" "${code:-no code}"
	else
		head -c 300 "$1"
	fi
}

# lgai_check_status STATUS FILE ACTION: exit with a readable message unless 2xx.
lgai_check_status() {
	case $1 in
	2??) return 0 ;;
	401) lgai_die "$3: HTTP 401, admin token missing: $(lgai_error_text "$2")" ;;
	403) lgai_die "$3: HTTP 403, wrong admin token: $(lgai_error_text "$2")" ;;
	404) lgai_die "$3: HTTP 404: admin API disabled (LGAI_ADMIN_TOKEN unset on the gateway), wrong ADMIN_URL, or an older gateway: $(lgai_error_text "$2")" ;;
	*) lgai_die "$3: HTTP $1: $(lgai_error_text "$2")" ;;
	esac
}

# lgai_json_number FILE FIELD: the first numeric value of FIELD in FILE.
lgai_json_number() {
	tr -d '\n' <"$1" | sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p" | head -n 1
}
