#!/usr/bin/env bash
#
# Create the first system admin and a team on a fresh dev server.
#
# Mattermost has a chicken-and-egg problem: you need an admin to do anything
# and there is no admin yet. The way out is that the very first account created
# on a server is made a system admin, and creating it needs no credentials.
#
# This goes over HTTP rather than through `docker exec` and mmctl, so it works
# the same from the dev container, from the host, or from another machine
# entirely — none of which can be assumed to have a Docker socket.
#
#   make dev-admin
#
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck disable=SC1091
. "$here/_env.sh"
load_dev_env "$here/.env"

server_url="${MM_SERVER_URL:-http://localhost:8065}"
server_url="${server_url%/}"

username="${MM_ADMIN_USERNAME:-admin}"
password="${MM_ADMIN_PASSWORD:-DevAdmin123!}"
email="${MM_ADMIN_EMAIL:-$username@example.com}"
team_name="${MM_TEAM_NAME:-dev}"

die() {
	echo "error: $*" >&2
	exit 1
}

command -v curl >/dev/null || die "curl is not installed"

body="$(mktemp)"
headers="$(mktemp)"
trap 'rm -f "$body" "$headers"' EXIT

# Pull one field out of a JSON object without requiring jq, which is not on
# every machine this might run from.
json_field() {
	sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p" "$2" | head -1
}

echo "==> Server: $server_url"
echo -n "==> Waiting for it"
for attempt in $(seq 1 90); do
	if curl -fsS --max-time 5 "$server_url/api/v4/system/ping" >/dev/null 2>&1; then
		echo " — up"
		break
	fi
	if [[ "$attempt" == 90 ]]; then
		echo
		die "no answer from $server_url. First start runs database migrations and can take a couple of minutes; 'make dev-logs' will say where it is."
	fi
	echo -n "."
	sleep 2
done

# --- The first account ------------------------------------------------------

echo "==> Creating $username"
status="$(curl -sS -o "$body" -w '%{http_code}' \
	-X POST "$server_url/api/v4/users" \
	-H 'Content-Type: application/json' \
	--data-binary "$(printf '{"email":"%s","username":"%s","password":"%s"}' "$email" "$username" "$password")")"

case "$status" in
201)
	echo "    created, and a system admin because it is the first account"
	;;
400 | 403)
	if grep -qi 'already\|exists\|taken' "$body"; then
		echo "    already there — carrying on"
	else
		die "the server would not create the account (HTTP $status): $(head -c 400 "$body")"
	fi
	;;
*) die "the server would not create the account (HTTP $status): $(head -c 400 "$body")" ;;
esac

# --- Sign in ----------------------------------------------------------------

status="$(curl -sS -o "$body" -D "$headers" -w '%{http_code}' \
	-X POST "$server_url/api/v4/users/login" \
	-H 'Content-Type: application/json' \
	--data-binary "$(printf '{"login_id":"%s","password":"%s"}' "$username" "$password")")"

[[ "$status" == "200" ]] ||
	die "could not sign in as $username (HTTP $status). If the account predates this script, the password in dev/.env is not its password."

token="$(awk 'BEGIN{IGNORECASE=1} /^token:/ {print $2}' "$headers" | tr -d '\r')"
[[ -n "$token" ]] || die "signed in but the server returned no token"
user_id="$(json_field id "$body")"

# --- A team to put it in ----------------------------------------------------
#
# Reminders and the daily summary are direct messages, so they arrive without
# one. The web interface needs a team before it will show you anything.

echo "==> Creating the '$team_name' team"
status="$(curl -sS -o "$body" -w '%{http_code}' \
	-X POST "$server_url/api/v4/teams" \
	-H "Authorization: Bearer $token" \
	-H 'Content-Type: application/json' \
	--data-binary "$(printf '{"name":"%s","display_name":"%s","type":"O"}' "$team_name" "$team_name")")"

case "$status" in
201) team_id="$(json_field id "$body")" ;;
400)
	# Already exists: look it up instead.
	curl -fsS -o "$body" "$server_url/api/v4/teams/name/$team_name" \
		-H "Authorization: Bearer $token" || die "the team exists but could not be read back"
	team_id="$(json_field id "$body")"
	echo "    already there — carrying on"
	;;
*) die "could not create the team (HTTP $status): $(head -c 400 "$body")" ;;
esac

if [[ -n "$team_id" && -n "$user_id" ]]; then
	curl -sS -o /dev/null \
		-X POST "$server_url/api/v4/teams/$team_id/members" \
		-H "Authorization: Bearer $token" \
		-H 'Content-Type: application/json' \
		--data-binary "$(printf '{"team_id":"%s","user_id":"%s"}' "$team_id" "$user_id")" || true
fi

cat <<EOF

Ready.

  Sign in at ${MM_SITE_URL:-http://localhost:8065}
  Username:  $username
  Password:  $password

Next: make deploy
EOF
