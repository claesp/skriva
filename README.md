# Skriva

Skriva is a wiki. Users write documents in Markdown. The server keeps
the documents and all their versions in PostgreSQL.

- A **tenant** is the main organizational unit. Each tenant has its own
  identity provider (OpenID Connect), users, groups, roles and settings.
- A tenant has **spaces**. A space contains **folders** and
  **documents** in a tree with no depth limit.
- Role-based access control (RBAC) controls what each user can do.

The web pages do not use JavaScript.

## Quick start

You must have Go 1.26, Podman and `make`.

1. Start PostgreSQL, Keycloak and Skriva in a Podman pod:

   ```sh
   make dev-app
   ```

2. Open <http://localhost:8080>. The browser goes to the default tenant
   `dflt`.
3. Sign in as `alice` (password `alice`). Alice is an administrator of
   the default tenant.

The other test users are `bob` (editor) and `carol` (viewer). For the
system administration, open <http://localhost:8080/admin/> and sign in
as `root` (password `root`).

## Documentation

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Access control (RBAC)](docs/access-control.md)
- [Administration](docs/administration.md)
- [Security](docs/security.md)
- [Development and tests](docs/development.md)
- [Operation](docs/operation.md)
- [User guide](docs/user-guide.md)
