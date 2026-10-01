#!/usr/bin/env bash
# Mint a batch of lab keys (POST /admin/keys/batch on the admin listener) and
# write, into OUT_DIR:
#   keys.csv       id,name,key_prefix,expires_at,key; mode 0600. The ONLY copy
#                  of the plaintext keys: keep it on the admin laptop, never
#                  in chat, Moodle, a shared doc or Git; delete it after the lab.
#   keys-slips.md  one printable slip per key (seat, base URL, key, 3-line
#                  Python example). Print, cut, hand out in person, shred after.
# No key is printed to the terminal.
#
# Usage: scripts/mint-keys.sh COUNT NAME_PREFIX [EXPIRES_AT]
#   COUNT        1..200. Lab: 40 keys for 30 students (DECISIONS D7).
#   NAME_PREFIX  seat-label prefix, e.g. lab- -> lab-01 ... lab-40.
#                Seat labels only, never real names (security #13).
#   EXPIRES_AT   RFC 3339, e.g. 2026-10-14T18:00:00Z: lab end + 1 h (security #15).
#
# Environment:
#   LGAI_ADMIN_TOKEN (or LGAI_ADMIN_TOKEN_FILE), ADMIN_URL: see scripts/admin-lib.sh
#   PUBLIC_BASE_URL  the URL students use, printed on the slips,
#                    e.g. https://lgai.example.org/v1 (default http://127.0.0.1:8080/v1)
#   OUT_DIR          output directory (default: current directory)
#
# Example:
#   export LGAI_ADMIN_TOKEN=...   # from the password manager, not typed on the projector
#   PUBLIC_BASE_URL=https://lgai.example.org/v1 scripts/mint-keys.sh 40 lab- 2026-10-14T18:00:00Z
set -euo pipefail
umask 077

# shellcheck source=scripts/admin-lib.sh
. "$(cd "$(dirname "$0")" && pwd)/admin-lib.sh"

usage() {
	sed -n '2,/^set -euo/p' "$0" | sed -e '/^set -euo/d' -e 's/^# \{0,1\}//' >&2
	exit 2
}

[ $# -ge 2 ] && [ $# -le 3 ] || usage
count=$1
prefix=$2
expires=${3:-}

[[ $count =~ ^[0-9]+$ ]] && [ "$count" -ge 1 ] && [ "$count" -le 200 ] || lgai_die "COUNT must be 1..200"
[[ $prefix =~ ^[A-Za-z0-9._-]{1,40}$ ]] || lgai_die "NAME_PREFIX must match [A-Za-z0-9._-]{1,40}"
if [ -n "$expires" ]; then
	[[ $expires =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})$ ]] ||
		lgai_die "EXPIRES_AT must be RFC 3339, e.g. 2026-10-14T18:00:00Z"
else
	echo "warning: no EXPIRES_AT; these keys never expire unless revoked (runbook: lab end + 1 h)" >&2
fi

base=${PUBLIC_BASE_URL:-http://127.0.0.1:8080/v1}
base=${base%/}
case $base in */v1) ;; *) base=$base/v1 ;; esac
case $base in
https://*) ;;
http://127.0.0.1* | http://localhost*) ;;
http://*) echo "warning: $base is plain HTTP: keys travel in clear text (security #23)" >&2 ;;
*) lgai_die "PUBLIC_BASE_URL must start with http:// or https://" ;;
esac

out=${OUT_DIR:-.}
mkdir -p "$out"
csv=$out/keys.csv
slips=$out/keys-slips.md
for f in "$csv" "$slips"; do
	[ ! -e "$f" ] || lgai_die "$f exists; move it away first (it may hold the only copy of earlier keys)"
done

lgai_admin_init

body=$(printf '{"count":%d,"name_prefix":"%s"' "$count" "$prefix")
[ -z "$expires" ] || body=$(printf '%s,"expires_at":"%s"' "$body" "$expires")
body="$body}"

resp=$(mktemp "$out/.mint-response.XXXXXX")
trap 'rm -f "$resp"' EXIT
status=$(lgai_admin_call POST /admin/keys/batch "$body" "$resp") || exit 1
lgai_check_status "$status" "$resp" "mint"

# Response: {"object":"list","data":[{"id":1,"name":"lab-01","key":"lgai_...",
# "key_prefix":"lgai_xxxxxxx","expires_at":"..."|null,...},...]}. Key objects
# are flat, so splitting on "}" yields one object per record; field order and
# whitespace do not matter. Needs no jq.
rows=$(awk '
	function str(s, f,   re, v) {
		re = "\"" f "\"[ \t\r\n]*:[ \t\r\n]*\"[^\"]*\""
		if (!match(s, re)) return ""
		v = substr(s, RSTART, RLENGTH)
		sub(/^"[^"]*"[ \t\r\n]*:[ \t\r\n]*"/, "", v)
		sub(/"$/, "", v)
		return v
	}
	function num(s, f,   re, v) {
		re = "\"" f "\"[ \t\r\n]*:[ \t\r\n]*[0-9]+"
		if (!match(s, re)) return ""
		v = substr(s, RSTART, RLENGTH)
		sub(/^.*:[ \t\r\n]*/, "", v)
		return v
	}
	BEGIN { RS = "}" }
	{
		key = str($0, "key")
		if (key !~ /^lgai_[A-Za-z0-9_-]+$/) next
		print num($0, "id") "," str($0, "name") "," str($0, "key_prefix") "," str($0, "expires_at") "," key
	}
' "$resp")

n=0
[ -z "$rows" ] || n=$(printf '%s\n' "$rows" | wc -l | tr -d ' ')
[ "$n" -gt 0 ] || lgai_die "the response (HTTP $status) contained no keys; nothing written"

{
	echo "id,name,key_prefix,expires_at,key"
	printf '%s\n' "$rows"
} >"$csv"
chmod 600 "$csv"

now=$(date -u +%Y-%m-%dT%H:%MZ)
{
	echo "# Lab key slips"
	echo
	echo "Generated $now for \`$base\`: $n keys, prefix \`$prefix\`. Cut along the rules and hand out in person."
	echo "Spares stay with the admin. Shred all slips after the lab."
	printf '%s\n' "$rows" | while IFS=, read -r id name kprefix exp key; do
		echo
		echo "---"
		echo
		echo "### Seat $name"
		echo
		if [ -n "$exp" ]; then
			echo "Your personal key for today's lab. Keep it to yourself; never put it in code or push it to Git. It stops working at $exp."
		else
			echo "Your personal key for today's lab. Keep it to yourself; never put it in code or push it to Git."
		fi
		echo
		echo '```sh'
		echo "export OPENAI_BASE_URL=$base"
		echo "export OPENAI_API_KEY=$key"
		echo '```'
		echo
		echo '```python'
		echo 'from openai import OpenAI  # pip install openai; it reads the two variables above'
		echo 'stream = OpenAI().chat.completions.create(model="coder", stream=True, messages=[{"role": "user", "content": "Explain pointers in C"}])'
		echo 'for c in stream: print(c.choices[0].delta.content or "" if c.choices else "", end="", flush=True)'
		echo '```'
		echo
		echo "PowerShell: \`\$env:OPENAI_BASE_URL=\"$base\"\` and \`\$env:OPENAI_API_KEY=\"<key above>\"\`. Key id $id, starts with \`$kprefix\`."
	done
} >"$slips"
chmod 600 "$slips"

echo "minted $n key(s) with prefix '$prefix'${expires:+, expiring $expires}"
echo "  $csv   (0600; the only copy of the keys; delete after the lab)"
echo "  $slips (print, cut, hand out; shred after the lab)"
[ "$n" -eq "$count" ] || echo "warning: asked for $count keys but the response held $n" >&2
