#!/bin/sh
# Build the release files for one architecture. See docs/release.md.
#
# Usage: scripts/release-build.sh amd64|arm64
#
# The script builds the container image localhost/skriva:<full>-<arch>.
# Then it copies the programs from the image. Thus the archive and the
# packages contain the same programs as the image. The results are in
# dist/:
#
#   skriva_<full>_linux_<arch>.tar.gz   the programs, the service and the settings
#   skriva_<full>_<arch>.deb            the Debian package
#   skriva-<full>-1.<rpm arch>.rpm      the RPM package
#   <arch>/bin/                         the programs (for the SBOM)
#
# <full> is major.minor.revision. The build of an architecture that is
# not the architecture of the computer needs QEMU and is slow.
set -eu

ARCH=${1:?usage: release-build.sh amd64|arm64}
case "$ARCH" in
amd64 | arm64) ;;
*)
	echo "release-build.sh: unknown architecture $ARCH" >&2
	exit 2
	;;
esac

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
eval "$(scripts/version.sh)"
# nFPM, from the Go module proxy. The Go checksum database verifies it.
NFPM=${NFPM:-github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0}
# The time of the commit makes the files the same for each build.
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct HEAD)
export SOURCE_DATE_EPOCH
IMAGE=localhost/skriva:$FULL-$ARCH
OUT=dist
BIN=$OUT/$ARCH/bin

podman build --platform "linux/$ARCH" \
	--build-arg VERSION="$VERSION" --build-arg REVISION="$REVISION" \
	--timestamp "$SOURCE_DATE_EPOCH" \
	-t "$IMAGE" -f Containerfile .

rm -rf "${OUT:?}/$ARCH"
mkdir -p "$BIN"
cid=$(podman create --platform "linux/$ARCH" "$IMAGE")
trap 'podman rm -f "$cid" >/dev/null 2>&1 || true' EXIT
for prog in skriva typst mmdr; do
	podman cp "$cid:/$prog" "$BIN/$prog"
done

# The archive.
name=skriva_${FULL}_linux_$ARCH
stage=$OUT/$ARCH/$name
mkdir -p "$stage"
cp "$BIN/skriva" "$BIN/typst" "$BIN/mmdr" README.md \
	deploy/packaging/skriva.service deploy/packaging/skriva.env "$stage/"
tar -C "$OUT/$ARCH" --sort=name --mtime="@$SOURCE_DATE_EPOCH" \
	--owner=0 --group=0 --numeric-owner -cf - "$name" | gzip -n >"$OUT/$name.tar.gz"
rm -rf "$stage"

# The packages. nFPM does not expand variables in all fields, thus the
# script writes the values into a copy of the configuration.
maintainer=${PKG_MAINTAINER:-$(git log -1 --format='%an <%ae>' HEAD)}
sed -e "s|\${PKG_VERSION}|$FULL|g" -e "s|\${PKG_ARCH}|$ARCH|g" \
	-e "s|\${PKG_BIN}|$BIN|g" -e "s|\${PKG_MAINTAINER}|$maintainer|g" \
	deploy/packaging/nfpm.yaml >"$OUT/$ARCH/nfpm.yaml"
for p in deb rpm; do
	go run "$NFPM" package -f "$OUT/$ARCH/nfpm.yaml" -p "$p" -t "$OUT/"
done

ls -l "$OUT"
