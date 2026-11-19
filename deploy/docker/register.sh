#!/bin/sh
# Create NetPulse external units and their agent tokens through the admin API.
#
#   ./register.sh unit <name> <host> [gateway]   create a unit, print slug + token
#   ./register.sh token <slug>                   rotate the token of an existing unit
#   ./register.sh list                           list units
#   ./register.sh fingerprint <host[:port]>      SHA-256 of a TLS certificate (OPNSENSE_CERT_FP)
#
# Environment: NETPULSE_URL (default http://127.0.0.1:3000), NETPULSE_USER
# (default admin), NETPULSE_PASS (default: AUTH_PASS from ./netpulse.env).
# Needs curl and python3. Tokens are printed once; put them in the pusher
# env files and nowhere else.
set -eu

URL=${NETPULSE_URL:-http://127.0.0.1:3000}
USER_NAME=${NETPULSE_USER:-admin}
if [ -z "${NETPULSE_PASS:-}" ] && [ -f "$(dirname "$0")/netpulse.env" ]; then
	NETPULSE_PASS=$(sed -n 's/^AUTH_PASS=//p' "$(dirname "$0")/netpulse.env" | head -1)
fi

die() { echo "register.sh: $*" >&2; exit 1; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1"; }

session() {
	[ -n "${NETPULSE_PASS:-}" ] || die "set NETPULSE_PASS or AUTH_PASS in netpulse.env"
	JAR=$(mktemp)
	trap 'rm -f "$JAR"' EXIT
	body=$(python3 -c 'import json,sys; print(json.dumps({"username": sys.argv[1], "password": sys.argv[2]}))' "$USER_NAME" "$NETPULSE_PASS")
	curl -fsS -c "$JAR" -H 'Content-Type: application/json' -d "$body" "$URL/api/auth/login" >/dev/null \
		|| die "login to $URL failed"
}

api() { # method path [json]
	if [ $# -ge 3 ]; then
		curl -sS -b "$JAR" -X "$1" -H 'Content-Type: application/json' -d "$3" "$URL$2"
	else
		curl -sS -b "$JAR" -X "$1" "$URL$2"
	fi
}

token() {
	out=$(api POST /api/agents "{\"slug\":\"$1\"}")
	echo "$out" | json 'd.get("token") or ""' | grep . || die "token for $1: $out"
}

case "${1:-}" in
unit)
	[ $# -ge 3 ] || die "usage: unit <name> <host> [gateway]"
	session
	gw=false; [ "${4:-}" = gateway ] && gw=true
	body=$(python3 -c 'import json,sys; print(json.dumps({"name": sys.argv[1], "host": sys.argv[2], "type": "external", "gateway": sys.argv[3] == "true", "inventory_mode": "agent"}))' "$2" "$3" "$gw")
	out=$(api POST /api/config/routers "$body")
	slug=$(echo "$out" | json 'd.get("router", {}).get("id", "")') || true
	[ -n "$slug" ] || die "create $2: $out"
	echo "slug:  $slug"
	echo "token: $(token "$slug")"
	;;
token)
	[ $# -eq 2 ] || die "usage: token <slug>"
	session
	echo "token: $(token "$2")"
	;;
list)
	session
	api GET /api/config/routers | json '"\n".join("%-20s %-10s %-16s %s" % (r["id"], r["type"], r["host"], "gateway" if r["is_gateway"] else "") for r in d["routers"])'
	;;
fingerprint)
	[ $# -eq 2 ] || die "usage: fingerprint <host[:port]>"
	hp=$2; case "$hp" in *:*) ;; *) hp="$hp:443" ;; esac
	echo | openssl s_client -connect "$hp" 2>/dev/null | openssl x509 -noout -fingerprint -sha256 | cut -d= -f2
	;;
*)
	sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
