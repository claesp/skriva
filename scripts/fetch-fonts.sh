#!/bin/sh
# Get the fonts of Skriva and copy them to internal/web/static/fonts. The
# Go program embeds these files. Thus the build of the Go program does not
# need Node.
#
# The script installs the exact versions in web/fonts/package-lock.json
# in a Node container. npm checks the integrity of each package. The
# packages cannot run install scripts.
#
# The name of each font folder contains the version of the package. The
# server tells browsers to keep the files for one year. Thus a new
# version must have a new folder. Do not change a file in an old folder.
set -eu

IMAGE=${NODE_IMAGE:-docker.io/library/node:24-alpine@sha256:8a32c8cf649a49915a66e81e7a609223c708d0adabf288ee3f4077d765542f85}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT="$ROOT/internal/web/static/fonts"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

podman run --rm \
	-v "$ROOT/web/fonts":/src:ro,Z \
	-v "$WORK":/work:Z \
	-w /work \
	"$IMAGE" sh -c '
		set -eu
		cp /src/package.json /src/package-lock.json .
		npm ci --ignore-scripts --no-audit --no-fund --loglevel=error
	'

# The upstream license files. They contain the correct copyright lines.
# The license of the Source Serif 4 package does not contain the
# copyright line of Adobe.
license_url() {
	case $1 in
	inter) echo https://raw.githubusercontent.com/rsms/inter/v4.0/LICENSE.txt ;;
	source-serif-4) echo https://raw.githubusercontent.com/adobe-fonts/source-serif/4.004R/LICENSE.md ;;
	jetbrains-mono) echo https://raw.githubusercontent.com/JetBrains/JetBrainsMono/v2.304/OFL.txt ;;
	esac
}

# The files for each font: the font name, the axes (wght or opsz) and the
# styles. Only the Latin and Latin Extended subsets are necessary. Other
# characters use the fallback fonts.
copy() {
	name=$1 axes=$2
	pkg="$WORK/node_modules/@fontsource-variable/$name"
	version=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$pkg/package.json")
	dir="$OUT/$name-$version"
	rm -rf "$dir"
	mkdir -p "$dir"
	for subset in latin latin-ext; do
		for style in normal italic; do
			cp "$pkg/files/$name-$subset-$axes-$style.woff2" "$dir/"
		done
	done
	curl -fsSL -o "$dir/LICENSE.txt" "$(license_url "$name")"
	echo "$dir"
}

rm -rf "$OUT"
copy inter wght
copy source-serif-4 opsz
copy jetbrains-mono wght
du -sh "$OUT"
