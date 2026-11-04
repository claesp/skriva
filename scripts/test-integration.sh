#!/bin/sh
# Run the integration tests against a temporary PostgreSQL container.
# The tests of the PDF export run Typst in a container. The tests of
# Mermaid diagrams run mmdr in a container. The script builds the mmdr
# stage of the Containerfile for this. The script removes the containers
# and the temporary files after the tests.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
IMAGE=${POSTGRES_IMAGE:-docker.io/library/postgres:17-alpine}
# Use the same Typst image as the Containerfile.
TYPST_IMAGE=${TYPST_IMAGE:-$(sed -n 's|^FROM \(ghcr.io/typst/typst[^ ]*\) AS typst$|\1|p' "$ROOT/Containerfile")}
NAME=skriva-test-db-$$
WORK=$(mktemp -d)

cleanup() {
	podman rm -f "$NAME" >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

# The PDF export runs Typst with an empty environment. Thus the wrapper
# sets the variables that Podman needs. The temporary folder of the
# export has the same path in the container.
mkdir "$WORK/tmp"
{
	echo '#!/bin/sh'
	echo "export HOME='$HOME' PATH='$PATH' XDG_RUNTIME_DIR='${XDG_RUNTIME_DIR:-}'"
	echo "exec podman run --rm --network none --read-only -v '$WORK/tmp:$WORK/tmp:Z' --entrypoint /bin/typst '$TYPST_IMAGE' \"\$@\""
} >"$WORK/typst"
chmod +x "$WORK/typst"
podman image exists "$TYPST_IMAGE" || podman pull -q "$TYPST_IMAGE" >/dev/null
export SKRIVA_TEST_TYPST="$WORK/typst" SKRIVA_TEST_TYPST_TMP="$WORK/tmp"

# mmdr reads the diagram from the standard input. The first build of the
# stage takes some minutes. Later builds use the cache of Podman.
MMDR_IMAGE=localhost/skriva-mmdr:test
podman build -q --target mmdr -t "$MMDR_IMAGE" -f "$ROOT/Containerfile" "$ROOT" >/dev/null
{
	echo '#!/bin/sh'
	echo "export HOME='$HOME' PATH='$PATH' XDG_RUNTIME_DIR='${XDG_RUNTIME_DIR:-}'"
	echo "exec podman run --rm -i --network none --read-only --preserve-fds 1 --entrypoint /out/bin/mmdr '$MMDR_IMAGE' \"\$@\""
} >"$WORK/mmdr"
chmod +x "$WORK/mmdr"
export SKRIVA_TEST_MMDR="$WORK/mmdr"

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
