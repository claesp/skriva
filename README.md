# Skriva

> **Caution!** This application was created with the aid of AI tools.
> While managed and reviewed by humans, the code and functionality may
> not be comparable to non-AI work.

Skriva is a wiki. Users write documents in a WYSIWYG editor, with
headings, lists, tables, alignment, images, draw.io diagrams and Mermaid
diagrams. The server keeps the documents, their images and all their
versions in PostgreSQL, encrypted. A document can have an information
classification, and users can export it as Markdown, HTML or PDF.

- A **tenant** is the main organizational unit. Each tenant has its own
  identity provider (OpenID Connect), users, groups, roles and settings.
- A tenant has **spaces**. A space contains **folders** and
  **documents** in a tree with no depth limit.
- Role-based access control (RBAC) controls what each user can do.

The web pages do not use JavaScript. The only exception is the editor
of a document.

## License

Skriva is free software under the [MIT License](LICENSE). The fonts in
`internal/web/static/fonts` have their own licenses (SIL Open Font
License), in the folder of each font.

## Install

Each [release](https://github.com/claesp/skriva/releases) has a
container image with a Podman Compose file, and Debian and RPM packages
for `amd64` and `arm64`. All files have SHA-256 checksums, SBOMs and
attestations. See [Operation](docs/operation.md) and
[Release](docs/release.md).

## Quick start

To try Skriva on your computer, you must have Go 1.26, Podman and
`make`.

1. Start PostgreSQL, Keycloak and Skriva in a Podman pod:

   ```sh
   make dev-app
   ```

2. Open <https://localhost:8443>. The browser goes to the default tenant
   `dflt`.
3. Sign in as `alice` (password `alice`). Alice is an administrator of
   the default tenant.

The other test users are `bob` (editor) and `carol` (viewer). For the
system administration, open <https://localhost:8443/admin/> and sign in
as `root` (password `root`). The certificates come from a private CA of
the development environment. To remove the browser warning, install
`.dev/tls/ca.crt` in the browser.

## Documentation

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Access control (RBAC)](docs/access-control.md)
- [Administration](docs/administration.md)
- [Page design](docs/design.md)
- [WYSIWYG editors: research](docs/wysiwyg-editors.md)
- [Fonts: research](docs/fonts.md)
- [Encryption at rest: research](docs/encryption-at-rest.md)
- [Security](docs/security.md)
- [Development and tests](docs/development.md)
- [Operation](docs/operation.md)
- [Release](docs/release.md)
- [User guide](docs/user-guide.md)
