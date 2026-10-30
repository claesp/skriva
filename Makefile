.PHONY: build test test-integration test-e2e lint image dev-up dev-app dev-down run

build:
	go build -trimpath -o bin/skriva ./cmd/skriva

# Unit tests. The tests that need PostgreSQL do not run.
test:
	go test ./...

# All tests, with a temporary PostgreSQL container.
test-integration:
	scripts/test-integration.sh

# End-to-end test against the development pod (see dev-app).
test-e2e:
	SKRIVA_E2E_URL=http://localhost:8080 go test -count=1 -tags e2e ./e2e/

lint:
	gofmt -l . | (! grep .)
	go vet ./...

image:
	podman build -t localhost/skriva:latest -f Containerfile .

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
	SKRIVA_DATABASE_URL="postgres://skriva:skriva@localhost:15432/skriva?sslmode=disable" \
	SKRIVA_OIDC_ISSUER=http://localhost:8180/realms/skriva \
	SKRIVA_OIDC_CLIENT_ID=skriva \
	SKRIVA_OIDC_CLIENT_SECRET=skriva-dev-secret \
	SKRIVA_ENCRYPTION_KEY=ZGV2LWtleS1kZXYta2V5LWRldi1rZXktZGV2LWtleSE= \
	SKRIVA_SYSTEM_ADMIN_GROUP=skriva-system \
	SKRIVA_ADMIN_GROUP=skriva-admins \
	SKRIVA_GROUP_ROLES=skriva-editors=editor \
	./bin/skriva
