# Shared helpers for the enjoythings target adapter. Sourced, not executed.
# POSIX sh.

ADAPTER_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DRILLS_DIR=$(CDPATH= cd -- "$ADAPTER_DIR/../.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "$DRILLS_DIR/.." && pwd)
# Docker builds and reads its .env from BUILD_ROOT. A sealed drill points this
# at the run's isolated tree via DRILL_BUILD_ROOT, so the stack boots from
# faulted source with no diff to read (spec §6, slice 2); unset, it is the main
# checkout and every path below is unchanged. Framework state (OVERRIDES_DIR,
# the loadgen overlay) and probe URLs stay anchored to the main checkout.
BUILD_ROOT="${DRILL_BUILD_ROOT:-$REPO_ROOT}"
SERVICES_DIR="$BUILD_ROOT/services"
OVERRIDES_DIR="$DRILLS_DIR/.overrides"
LOADGEN_OVERLAY="$DRILLS_DIR/loadgen/docker-compose.loadgen.yml"

# env_file prints the --env-file flag for compose: the build tree's .env when it
# has one, else the main checkout's.
env_file() {
	if [ -f "$BUILD_ROOT/.env" ]; then printf -- '--env-file %s' "$BUILD_ROOT/.env"
	elif [ -f "$REPO_ROOT/.env" ]; then printf -- '--env-file %s' "$REPO_ROOT/.env"
	fi
}

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
	_env=$(env_file)
	# shellcheck disable=SC2086
	(cd "$SERVICES_DIR" && docker compose $_env $_files "$@")
}

# compose_with_loadgen is compose plus the traffic generator overlay.
compose_with_loadgen() {
	_env=$(env_file)
	# shellcheck disable=SC2086
	(cd "$SERVICES_DIR" && docker compose $_env -f docker-compose.yml -f "$LOADGEN_OVERLAY" "$@")
}

# known_component fails unless the name appears in target.yaml.
known_component() {
	grep -q "name: *$1," "$ADAPTER_DIR/target.yaml" || die "unknown component: $1"
}
