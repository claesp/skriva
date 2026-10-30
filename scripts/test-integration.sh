#!/bin/sh
# Run the integration tests against a temporary PostgreSQL container.
# The script removes the container after the tests.
set -eu

IMAGE=${POSTGRES_IMAGE:-docker.io/library/postgres:17-alpine}
NAME=skriva-test-db-$$

cleanup() { podman rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

podman run -d --rm --name "$NAME" \
	-e POSTGRES_USER=test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=test \
	-p 127.0.0.1::5432 \
	--tmpfs /var/lib/postgresql/data \
	"$IMAGE" -c fsync=off -c synchronous_commit=off >/dev/null

PORT=$(podman port "$NAME" 5432/tcp | sed 's/.*://')

i=0
until podman exec "$NAME" pg_isready -U test -h 127.0.0.1 >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -gt 60 ]; then
		echo "PostgreSQL did not start" >&2
		exit 1
	fi
	sleep 0.5
done

export SKRIVA_TEST_DATABASE_URL="postgres://test:test@127.0.0.1:$PORT/test?sslmode=disable"
go test -count=1 -race "$@" ./...
