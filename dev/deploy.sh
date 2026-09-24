#!/usr/bin/env bash
#
# Upload the built bundle to a Mattermost server and enable it.
#
# Talks to the server over HTTP, so it works whether the server is on this
# machine or another one. Needs curl and nothing else.
#
#   make dist && dev/deploy.sh
#
# Configuration comes from dev/.env, or the environment, or both:
#
#   MM_SERVER_URL      where the server is (default http://localhost:8065)
#   MM_ADMIN_TOKEN     a personal access token, or
#   MM_ADMIN_USERNAME  an admin login, and
#   MM_ADMIN_PASSWORD  their password
#
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(dirname "$here")"

# shellcheck disable=SC1091
. "$here/_env.sh"
load_dev_env "$here/.env"

server_url="${MM_SERVER_URL:-http://localhost:8065}"
server_url="${server_url%/}"

die() {
	echo "error: $*" >&2
	exit 1
}

command -v curl >/dev/null || die "curl is not installed"

# Read the id straight out of the manifest, so this script cannot drift from
# what is actually being built.
plugin_id="$(sed -n 's/^[[:space:]]*"id"[[:space:]]*:[[:space:]]*"\(.*\)",/\1/p' "$repo/plugin.json")"
[[ -n "$plugin_id" ]] || die "could not read the plugin id from plugin.json"

bundle="$(ls -t "$repo"/dist/*.tar.gz 2>/dev/null | head -1 || true)"
[[ -n "$bundle" ]] || die "no bundle in dist/. Run 'make dist' first (or 'make build dist')."

echo "==> Bundle:  $(basename "$bundle")"
echo "==> Server:  $server_url"

# --- Wait for the server ----------------------------------------------------

echo -n "==> Waiting for the server"
for attempt in $(seq 1 60); do
	if curl -fsS --max-time 5 "$server_url/api/v4/system/ping" >/dev/null 2>&1; then
		echo " — up"
		break
	fi
	if [[ "$attempt" == 60 ]]; then
		echo
		die "the server at $server_url never answered. Is it running, and is MM_SERVER_URL right?"
	fi
	echo -n "."
	sleep 2
done

# --- Authenticate -----------------------------------------------------------

token="${MM_ADMIN_TOKEN:-}"

if [[ -z "$token" ]]; then
	[[ -n "${MM_ADMIN_USERNAME:-}" && -n "${MM_ADMIN_PASSWORD:-}" ]] ||
		die "set MM_ADMIN_TOKEN, or MM_ADMIN_USERNAME and MM_ADMIN_PASSWORD, in dev/.env"

	echo "==> Signing in as $MM_ADMIN_USERNAME"
	headers="$(mktemp)"
	trap 'rm -f "$headers"' EXIT

	# The session token comes back in a header, not the body.
	status="$(curl -sS -o /dev/null -D "$headers" -w '%{http_code}' \
		-X POST "$server_url/api/v4/users/login" \
		-H 'Content-Type: application/json' \
		--data-binary "$(printf '{"login_id":"%s","password":"%s"}' "$MM_ADMIN_USERNAME" "$MM_ADMIN_PASSWORD")")"

	if [[ "$status" != "200" ]]; then
		die "sign-in failed (HTTP $status). Wrong credentials, or the account does not exist yet — see dev/README.md for creating the first one."
	fi

	token="$(awk 'BEGIN{IGNORECASE=1} /^token:/ {print $2}' "$headers" | tr -d '\r')"
	[[ -n "$token" ]] || die "signed in but the server returned no token"
fi

auth=(-H "Authorization: Bearer $token")

# --- Upload -----------------------------------------------------------------

echo "==> Uploading"
body="$(mktemp)"
trap 'rm -f "$body" "${headers:-}"' EXIT

status="$(curl -sS -o "$body" -w '%{http_code}' \
	-X POST "$server_url/api/v4/plugins" \
	"${auth[@]}" \
	-F "plugin=@$bundle" \
	-F "force=true")"

case "$status" in
201 | 200) ;;
401 | 403) die "the server refused the upload (HTTP $status). Is that account a system admin?" ;;
400)
	if grep -q 'EnableUploads' "$body" 2>/dev/null; then
		die "plugin uploads are disabled on this server. Set PluginSettings.EnableUploads (the dev compose file does)."
	fi
	die "the server rejected the bundle (HTTP $status): $(head -c 400 "$body")"
	;;
*) die "upload failed (HTTP $status): $(head -c 400 "$body")" ;;
esac

# --- Enable -----------------------------------------------------------------

echo "==> Enabling $plugin_id"
status="$(curl -sS -o "$body" -w '%{http_code}' \
	-X POST "$server_url/api/v4/plugins/$plugin_id/enable" "${auth[@]}")"

[[ "$status" == "200" ]] || die "could not enable the plugin (HTTP $status): $(head -c 400 "$body")"

cat <<EOF

Deployed and enabled.

  Configure it:  $server_url/admin_console/plugins/plugin_$plugin_id
  Then, in any channel:  /yacal help

Until the Yandex client ID and secret are set, every command will say so and
name what is missing. docs/admin-setup.md explains where those come from.
EOF
