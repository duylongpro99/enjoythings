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
TOXIPROXY_OVERLAY="$DRILLS_DIR/toxics/docker-compose.toxiproxy.yml"
CHAOSLLM_OVERLAY="$DRILLS_DIR/chaosllm/docker-compose.chaosllm.yml"

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

# compose_with_overlay <overlay> <args...> runs compose with one extra overlay
# and every active env override layered on top, so a component already re-pointed
# by env.set keeps its override when recreated alongside the overlay's service.
compose_with_overlay() {
	_overlay=$1
	shift
	_files="-f docker-compose.yml -f $_overlay"
	if [ -d "$OVERRIDES_DIR" ]; then
		for _override in "$OVERRIDES_DIR"/*.yml; do
			[ -f "$_override" ] && _files="$_files -f $_override"
		done
	fi
	_env=$(env_file)
	# shellcheck disable=SC2086
	(cd "$SERVICES_DIR" && docker compose $_env $_files "$@")
}

# compose_with_toxiproxy / compose_with_chaosllm bring the drill-only fault
# containers up in the base project so teardown removes them as orphans.
compose_with_toxiproxy() { compose_with_overlay "$TOXIPROXY_OVERLAY" "$@"; }
compose_with_chaosllm() { compose_with_overlay "$CHAOSLLM_OVERLAY" "$@"; }

# toxi_cli runs the Toxiproxy CLI inside the running proxy container (admin API
# on 127.0.0.1:8474 from the container's own point of view).
toxi_cli() { compose_with_toxiproxy exec -T toxiproxy /toxiproxy-cli "$@"; }

# toxi_create creates a proxy, tolerating one that already exists.
toxi_create() {
	toxi_cli create "$1" --listen "0.0.0.0:$2" --upstream "$3" >/dev/null 2>&1 || true
}

# edge_lookup <a> <b> prints, for a supported Toxiproxy edge:
#   CLIENT_ENV PROXY_NAME LISTEN_PORT UPSTREAM CLIENT_URL
# CLIENT_URL is what the client env var is set to so it dials the proxy instead
# of the upstream. Unsupported edges die (see drills/toxics/README.md).
edge_lookup() {
	case "$1->$2" in
	"payment-processor->stub-payment-rail")
		printf '%s %s %s %s %s' PAYMENT_RAIL_URL pp-rail 18190 stub-payment-rail:18090 http://toxiproxy:18190 ;;
	"fraud-worker->ledger")
		printf '%s %s %s %s %s' LEDGER_GRPC_ADDR fw-ledger 19091 ledger:9091 toxiproxy:19091 ;;
	"fraud-worker->verification")
		printf '%s %s %s %s %s' VERIFICATION_GRPC_ADDR fw-verif 19094 verification:9094 toxiproxy:19094 ;;
	*)
		die "unsupported net edge: $1 -> $2 (see drills/toxics/README.md)" ;;
	esac
}

# known_component fails unless the name appears in target.yaml.
known_component() {
	grep -q "name: *$1," "$ADAPTER_DIR/target.yaml" || die "unknown component: $1"
}
