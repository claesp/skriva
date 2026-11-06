.PHONY: editor fonts build test test-integration test-e2e lint check-release image packages version dev-up dev-app dev-down run

# The version: major.minor from internal/version/version.go, and the Git
# commit as the revision. See docs/release.md.
VERSION := $(shell sed -n 's/^const Version = "\(.*\)"$$/\1/p' internal/version/version.go)
REVISION := $(shell git rev-parse --short=12 HEAD)
LDFLAGS := -X github.com/claesp/skriva/internal/version.Revision=$(REVISION)

# Build the script of the editor (needs Podman). The result is in
# internal/web/static/editor.js.
editor:
	scripts/build-editor.sh

# Get the font files (needs Podman). The result is in
# internal/web/static/fonts.
fonts:
	scripts/fetch-fonts.sh

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/skriva ./cmd/skriva

version:
	@echo $(VERSION).$(REVISION)

# Unit tests. The tests that need PostgreSQL do not run.
test:
	go test ./...

# All tests, with a temporary PostgreSQL container.
test-integration:
	scripts/test-integration.sh

# End-to-end test against the development pod (see dev-app).
test-e2e:
	SKRIVA_E2E_URL=https://localhost:8443 SKRIVA_E2E_CA_FILE=.dev/tls/ca.crt \
		go test -count=1 -tags e2e ./e2e/

lint:
	gofmt -l . | (! grep .)
	go vet ./...

# The release rules: digests, no "latest" tags, schema and version.
check-release:
	scripts/check-release.sh >/dev/null

# The container image, with the tag major.minor.revision. Never use the
# tag "latest".
image:
	podman build --build-arg VERSION=$(VERSION) --build-arg REVISION=$(REVISION) \
		-t localhost/skriva:$(VERSION).$(REVISION) -f Containerfile .

# The release files for one architecture (amd64 or arm64) in dist/:
# the container image, the archive, and the Debian and RPM packages.
ARCH ?= amd64
packages:
	scripts/release-build.sh $(ARCH)

# Start PostgreSQL and Keycloak.
dev-up:
	scripts/dev.sh up

# Start PostgreSQL, Keycloak and the application in a container.
dev-app:
	scripts/dev.sh app

dev-down:
	scripts/dev.sh down

# Run the application on the host against the development pod.
run: build
	SKRIVA_DATABASE_URL="postgres://skriva:skriva@localhost:15432/skriva?sslmode=verify-full" \
	SKRIVA_OIDC_ISSUER=https://localhost:8543/realms/skriva \
	SKRIVA_TLS_CERT_FILE=.dev/tls/skriva.crt SKRIVA_TLS_KEY_FILE=.dev/tls/skriva.key \
	SKRIVA_TLS_CA_FILE=.dev/tls/ca.crt \
	SKRIVA_OIDC_CLIENT_ID=skriva \
	SKRIVA_OIDC_CLIENT_SECRET=skriva-dev-secret \
	SKRIVA_ENCRYPTION_KEY=ZGV2LWtleS1kZXYta2V5LWRldi1rZXktZGV2LWtleSE= \
	SKRIVA_SYSTEM_ADMIN_GROUP=skriva-system \
	SKRIVA_ADMIN_GROUP=skriva-admins \
	SKRIVA_GROUP_ROLES=skriva-editors=editor \
	./bin/skriva
