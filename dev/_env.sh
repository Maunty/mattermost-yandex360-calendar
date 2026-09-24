# Shared by deploy.sh and bootstrap.sh. Not executable; meant to be sourced.

# load_dev_env reads dev/.env, but never over the top of a variable that is
# already set.
#
# That precedence is the whole point. The dev container sets MM_SERVER_URL to
# the Mattermost service name on the compose network, because that is how one
# container reaches another. A dev/.env written for the host will say
# localhost, which is right from the host and wrong from in here — and a plain
# `set -a; . .env` would let the file win and quietly send the deploy nowhere.
#
# Values are taken literally, with one pair of surrounding quotes removed if
# present. This is not a shell parser: no expansion, no substitution.
load_dev_env() {
	local file="$1" line key value

	[[ -f "$file" ]] || return 0

	while IFS= read -r line || [[ -n "$line" ]]; do
		line="${line%$'\r'}"
		[[ "$line" =~ ^[[:space:]]*(#|$) ]] && continue
		[[ "$line" == *=* ]] || continue

		key="${line%%=*}"
		key="${key//[[:space:]]/}"
		[[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue

		# Already set in the environment: leave it alone.
		[[ -n "${!key-}" ]] && continue

		value="${line#*=}"
		if [[ "$value" == \"*\" || "$value" == \'*\' ]]; then
			value="${value:1:${#value}-2}"
		fi

		export "$key=$value"
	done <"$file"
}
