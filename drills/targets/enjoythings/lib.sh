# Shared helpers for the enjoythings target adapter. Sourced, not executed.
# POSIX sh.

ADAPTER_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DRILLS_DIR=$(CDPATH= cd -- "$ADAPTER_DIR/../.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "$DRILLS_DIR/.." && pwd)
SERVICES_DIR="$REPO_ROOT/services"
OVERRIDES_DIR="$DRILLS_DIR/.overrides"
LOADGEN_OVERLAY="$DRILLS_DIR/loadgen/docker-compose.loadgen.yml"

# Endpoints as seen from the host, for probes and the engineer. Host ports
# follow the same *_PORT overrides Compose reads from the root .env.
env_port() {
	_port=""
	[ -f "$REPO_ROOT/.env" ] && _port=$(sed -n "s/^$1=//p" "$REPO_ROOT/.env" | tail -n 1)
	printf '%s' "${_port:-$2}"
}
export GATEWAY_URL="${GATEWAY_URL:-http://localhost:$(env_port GATEWAY_PORT 8080)}"
export DATABASE_URL="${DATABASE_URL:-postgres://enjoythings:enjoythings_dev_password@localhost:$(env_port POSTGRES_PORT 5432)/enjoythings?sslmode=disable}"
export JWT_SECRET="${JWT_SECRET:-local-dev-jwt-secret-change-me}"

die() { printf 'enjoythings: %s\n' "$*" >&2; exit 1; }
log() { printf 'enjoythings: %s\n' "$*" >&2; }

# compose <args...> runs docker compose for the platform stack, with the root
# .env when present and every active env override layered on top.
compose() {
	set -- "$@"
	_files="-f docker-compose.yml"
	if [ -d "$OVERRIDES_DIR" ]; then
		for _override in "$OVERRIDES_DIR"/*.yml; do
			[ -f "$_override" ] && _files="$_files -f $_override"
		done
	fi
	_env=""
	[ -f "$REPO_ROOT/.env" ] && _env="--env-file $REPO_ROOT/.env"
	# shellcheck disable=SC2086
	(cd "$SERVICES_DIR" && docker compose $_env $_files "$@")
}

# compose_with_loadgen is compose plus the traffic generator overlay.
compose_with_loadgen() {
	_env=""
	[ -f "$REPO_ROOT/.env" ] && _env="--env-file $REPO_ROOT/.env"
	# shellcheck disable=SC2086
	(cd "$SERVICES_DIR" && docker compose $_env -f docker-compose.yml -f "$LOADGEN_OVERLAY" "$@")
}

# known_component fails unless the name appears in target.yaml.
known_component() {
	grep -q "name: *$1," "$ADAPTER_DIR/target.yaml" || die "unknown component: $1"
}
