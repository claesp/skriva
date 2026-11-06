#!/bin/sh
# Print the version of the work tree as shell variables:
#
#   VERSION   major.minor, from internal/version/version.go
#   REVISION  the short Git commit (12 digits)
#   FULL      major.minor.revision
#
# Usage: eval "$(scripts/version.sh)"
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
VERSION=$(sed -n 's/^const Version = "\([0-9][0-9]*\.[0-9][0-9]*\)"$/\1/p' "$ROOT/internal/version/version.go")
if [ -z "$VERSION" ]; then
	echo "version.sh: no Version in internal/version/version.go" >&2
	exit 1
fi
REVISION=$(git -C "$ROOT" rev-parse --short=12 HEAD)
echo "VERSION=$VERSION"
echo "REVISION=$REVISION"
echo "FULL=$VERSION.$REVISION"
