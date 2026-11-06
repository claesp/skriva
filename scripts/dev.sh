#!/bin/sh
# Manage the development environment with Podman.
#
# All containers are in one pod. Thus they share the network namespace,
# and "localhost" is the same for the browser, the application and
# Keycloak. This keeps the OIDC issuer URL the same for all of them.
#
# Usage: scripts/dev.sh up|app|down|status|logs [container]
set -eu

POD=skriva-dev
ROOT=$(cd "$(dirname "$0")/.." && pwd)

POSTGRES_IMAGE=${POSTGRES_IMAGE:-docker.io/library/postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24}
KEYCLOAK_IMAGE=${KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.0@sha256:09a381c715ab0b111835b70f2905955274843a219c6f27efb348e4d9f4086858}
APP_IMAGE=${APP_IMAGE:-localhost/skriva:dev}

DB_PORT=${DB_PORT:-15432}
KC_PORT=8180
APP_PORT=8080

# The encryption key for development. Do not use it in production.
DEV_KEY=ZGV2LWtleS1kZXYta2V5LWRldi1rZXktZGV2LWtleSE=

wait_for() {
	name=$1
	url=$2
	printf 'Waiting for %s' "$name"
	i=0
	until curl -fsS -o /dev/null "$url"; do
		i=$((i + 1))
		if [ "$i" -gt 120 ]; then
			echo
			echo "$name did not start. See: podman logs $POD-$name" >&2
			exit 1
		fi
		printf '.'
		sleep 1
	done
	echo ' ready'
}

up() {
	if ! podman pod exists "$POD"; then
		podman pod create --name "$POD" \
			-p "127.0.0.1:$DB_PORT:5432" \
			-p "127.0.0.1:$KC_PORT:$KC_PORT" \
			-p "127.0.0.1:$APP_PORT:$APP_PORT"
	fi

	if ! podman container exists "$POD-db"; then
		podman run -d --pod "$POD" --name "$POD-db" \
			-e POSTGRES_USER=skriva \
			-e POSTGRES_PASSWORD=skriva \
			-e POSTGRES_DB=skriva \
			-v "$POD-pgdata:/var/lib/postgresql/data" \
			--health-cmd "pg_isready -U skriva" \
			"$POSTGRES_IMAGE"
	fi

	if ! podman container exists "$POD-keycloak"; then
		podman run -d --pod "$POD" --name "$POD-keycloak" \
			-e KC_BOOTSTRAP_ADMIN_USERNAME=admin \
			-e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
			-e KC_HEALTH_ENABLED=true \
			-v "$ROOT/deploy/keycloak:/opt/keycloak/data/import:ro,Z" \
			"$KEYCLOAK_IMAGE" start-dev --import-realm --http-port=$KC_PORT
	fi
	podman start "$POD-db" "$POD-keycloak" >/dev/null

	wait_for keycloak "http://localhost:$KC_PORT/realms/skriva/.well-known/openid-configuration"
	echo "PostgreSQL: postgres://skriva:skriva@localhost:$DB_PORT/skriva"
	echo "Keycloak:   http://localhost:$KC_PORT (admin console user: admin/admin)"
}

app() {
	up
	eval "$("$ROOT/scripts/version.sh")"
	podman build --build-arg VERSION="$VERSION" --build-arg REVISION="$REVISION" \
		-t "$APP_IMAGE" -f "$ROOT/Containerfile" "$ROOT"
	podman rm -f "$POD-app" >/dev/null 2>&1 || true
	podman run -d --pod "$POD" --name "$POD-app" \
		-e SKRIVA_DATABASE_URL="postgres://skriva:skriva@localhost:5432/skriva?sslmode=disable" \
		-e SKRIVA_BASE_URL="http://localhost:$APP_PORT" \
		-e SKRIVA_OIDC_ISSUER="http://localhost:$KC_PORT/realms/skriva" \
		-e SKRIVA_OIDC_CLIENT_ID=skriva \
		-e SKRIVA_OIDC_CLIENT_SECRET=skriva-dev-secret \
		-e SKRIVA_ENCRYPTION_KEY="$DEV_KEY" \
		-e SKRIVA_SYSTEM_ADMIN_GROUP=skriva-system \
		-e SKRIVA_ADMIN_GROUP=skriva-admins \
		-e SKRIVA_GROUP_ROLES=skriva-editors=editor \
		"$APP_IMAGE"
	wait_for app "http://localhost:$APP_PORT/readyz"
	echo "Skriva:     http://localhost:$APP_PORT (users: alice/alice, bob/bob, carol/carol)"
	echo "System:     http://localhost:$APP_PORT/admin/ (user: root/root)"
}

down() {
	podman pod rm -f "$POD" >/dev/null 2>&1 || true
	if [ "${1:-}" = "--volumes" ]; then
		podman volume rm -f "$POD-pgdata" >/dev/null 2>&1 || true
	fi
	echo "Removed pod $POD"
}

case "${1:-}" in
up) up ;;
app) app ;;
down) shift; down "$@" ;;
status) podman pod ps --filter "name=$POD"; podman ps -a --pod --filter "pod=$POD" ;;
logs) podman logs -f "$POD-${2:-app}" ;;
*)
	echo "usage: $0 up|app|down [--volumes]|status|logs [db|keycloak|app]" >&2
	exit 2
	;;
esac
