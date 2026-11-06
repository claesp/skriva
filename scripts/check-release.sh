#!/bin/sh
# Check the release rules. See docs/release.md.
#
# Usage:
#   scripts/check-release.sh          Check the work tree (CI of each push).
#   scripts/check-release.sh v0.2     Check the release tag v0.2 (release CI).
#
# The rules:
#   1. No container image uses the tag "latest".
#   2. Each image in the Containerfile, the compose files and the scripts
#      has a digest (@sha256:...). Each GitHub action has a commit SHA.
#   3. A release never changes a migration of an older release.
#   4. A new migration (a change of the database schema) needs a new
#      major version.
#   5. A release tag is "v" + the version in internal/version/version.go,
#      and the version is higher than the version of the last release.
#
# Messages go to standard error. Standard output gets the results as
# key=value lines, for $GITHUB_OUTPUT:
#   version=0.2  revision=3f9a2c1d4e5b  full=0.2.3f9a2c1d4e5b
#   previous=v0.1  schema_changed=false
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
TAG=${1:-}
failed=0
fail() {
	echo "check-release: $*" >&2
	failed=1
}

eval "$(scripts/version.sh)"

# Rule 1. The pattern is in two parts, thus this file does not match.
latest=':late''st\b'
if git grep -nP "$latest" -- Containerfile Makefile scripts deploy docs .github README.md >&2; then
	fail 'do not use the tag "latest" for container images (see the lines above)'
fi

# Rule 2.
digest='@sha256:[0-9a-f]{64}'
git grep -nE '^FROM ' -- Containerfile | grep -vE "$digest| scratch$|FROM [a-z0-9]+$" >&2 &&
	fail "each FROM line in the Containerfile needs an image digest"
git grep -nE '^[[:space:]]*image:' -- 'deploy/*.yaml' 'deploy/*.yml' |
	grep -vE "$digest|image: @IMAGE@$" >&2 &&
	fail "each image in a compose file needs a digest"
git grep -nE '_IMAGE=\$\{[A-Z_]+:-' -- scripts | grep -vE "$digest|:-localhost/|Containerfile" >&2 &&
	fail "each image in a script needs a digest"
git grep -nE '^[[:space:]]*-?[[:space:]]*uses:' -- .github | grep -vE 'uses: [^@]+@[0-9a-f]{40}( |$)' >&2 &&
	fail "each GitHub action needs a full commit SHA"

# The last release before this one.
if [ -n "$TAG" ]; then
	base="$TAG^"
else
	base=HEAD
fi
previous=$(git describe --tags --abbrev=0 --match 'v[0-9]*' "$base" 2>/dev/null || true)
schema_changed=false

if [ -n "$previous" ]; then
	prev_version=${previous#v}
	prev_major=${prev_version%%.*}
	prev_minor=${prev_version#*.}
	major=${VERSION%%.*}
	minor=${VERSION#*.}

	# Rule 3.
	for f in $(git ls-tree --name-only "$previous" internal/db/migrations/); do
		if ! git diff --quiet "$previous" -- "$f"; then
			fail "$f changed after release $previous: never change a migration, add a new one"
		fi
	done
	# Rule 4.
	if [ -n "$(git diff --name-only --diff-filter=A "$previous" -- internal/db/migrations/)" ]; then
		schema_changed=true
		if [ "$major" -le "$prev_major" ]; then
			fail "the database schema changed after $previous: the version must be $((prev_major + 1)).0 (internal/version/version.go)"
		fi
	fi
	# Rule 5 (the order). The work tree can have the version of the last
	# release until the next release.
	if [ "$major" -lt "$prev_major" ] || { [ "$major" -eq "$prev_major" ] && [ "$minor" -lt "$prev_minor" ]; }; then
		fail "the version $VERSION is lower than the last release $previous"
	fi
	if [ -n "$TAG" ] && [ "$major" -eq "$prev_major" ] && [ "$minor" -eq "$prev_minor" ]; then
		fail "the version $VERSION is the version of the last release $previous"
	fi
fi

# Rule 5 (the tag).
if [ -n "$TAG" ]; then
	if [ "$TAG" != "v$VERSION" ]; then
		fail "the tag $TAG is not v$VERSION (internal/version/version.go)"
	fi
	if [ "$(git rev-parse "$TAG^{commit}")" != "$(git rev-parse HEAD)" ]; then
		fail "the tag $TAG is not the current commit"
	fi
fi

if [ "$failed" -ne 0 ]; then
	exit 1
fi
echo "check-release: OK (version $FULL, last release ${previous:-none}, schema changed: $schema_changed)" >&2
echo "version=$VERSION"
echo "revision=$REVISION"
echo "full=$FULL"
echo "previous=$previous"
echo "schema_changed=$schema_changed"
