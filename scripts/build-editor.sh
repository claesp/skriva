#!/bin/sh
# Build the script of the WYSIWYG editor in a Node container. The result
# is internal/web/static/editor.js. The Go program embeds this file. Thus
# the build of the Go program does not need Node.
#
# The script installs the exact versions in web/editor/package-lock.json.
# The packages cannot run install scripts.
set -eu

IMAGE=${NODE_IMAGE:-docker.io/library/node:24-alpine@sha256:8a32c8cf649a49915a66e81e7a609223c708d0adabf288ee3f4077d765542f85}
ROOT=$(cd "$(dirname "$0")/.." && pwd)

podman run --rm \
	-v "$ROOT/web/editor":/src:Z \
	-v "$ROOT/internal/web/static":/out:Z \
	-w /src \
	"$IMAGE" sh -c '
		set -eu
		npm ci --ignore-scripts --no-audit --no-fund --loglevel=error
		npm audit --omit=dev --audit-level=moderate
		npx --no-install esbuild src/editor.js \
			--bundle --minify --format=iife --target=es2020 \
			--legal-comments=eof --outfile=/out/editor.js
	'
ls -l "$ROOT/internal/web/static/editor.js"
