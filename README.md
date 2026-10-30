# Skriva

Skriva is a wiki. Users write pages in Markdown. The server keeps the
pages and all their versions in PostgreSQL. Users sign in with OpenID
Connect (OIDC). Role-based access control (RBAC) controls what each user
can do.

The web pages do not use JavaScript.

## Quick start

You must have Go 1.26, Podman and `make`.

1. Start PostgreSQL, Keycloak and Skriva in a Podman pod:

   ```sh
   make dev-app
   ```

2. Open <http://localhost:8080>.
3. Sign in as `alice` (password `alice`). Alice is an administrator.

The other test users are `bob` (editor) and `carol` (viewer).

## Documentation

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Access control (RBAC)](docs/access-control.md)
- [Security](docs/security.md)
- [Development and tests](docs/development.md)
- [Operation](docs/operation.md)
- [User guide](docs/user-guide.md)
