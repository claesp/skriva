# Development and tests

## Necessary tools

- Go 1.26 or later.
- Podman 5 or later. The build of the editor script uses a Node
  container. Node on the host is not necessary.
- `make` and `curl`.

## Development environment

The script `scripts/dev.sh` starts all services in one Podman pod with
the name `skriva-dev`. The containers in the pod share one network.
Thus `localhost:8180` is the same Keycloak for the browser and for the
server. This is necessary because the OIDC issuer URL must be the same
for all.

| Service    | URL                          | Users |
|------------|------------------------------|-------|
| Skriva     | <http://localhost:8080>      | `alice/alice`, `bob/bob`, `carol/carol` |
| System administration | <http://localhost:8080/admin/> | `root/root` |
| Keycloak   | <http://localhost:8180>      | Admin console: `admin/admin` |
| PostgreSQL | `localhost:15432`            | `skriva/skriva` |

The test users have these groups and roles:

| User    | Group            | Role in tenant `dflt` |
|---------|------------------|------|
| `alice` | `skriva-admins`  | `admin` |
| `bob`   | `skriva-editors` | `editor` |
| `carol` | (none)           | `viewer` (the default role) |
| `root`  | `skriva-system`  | System administrator |

All tenants in development can use the same Keycloak client. The client
permits all redirect URIs on `http://localhost:8080`. For a new tenant,
use these settings:

| Setting       | Value |
|---------------|-------|
| Issuer URL    | `http://localhost:8180/realms/skriva` |
| Client ID     | `skriva` |
| Client secret | `skriva-dev-secret` |

The development scripts use a fixed encryption key. Do not use that key
in production.

Keycloak imports the realm only when the container starts for the first
time. After a change to `deploy/keycloak/realm-skriva.json`, remove the
container: `podman rm -f skriva-dev-keycloak`. Then run `make dev-up`
again.

### Commands

| Command          | Action |
|------------------|--------|
| `make dev-up`    | Start PostgreSQL and Keycloak. |
| `make run`       | Build and run Skriva on the host. Use this after `make dev-up`. |
| `make dev-app`   | Build the container image. Start all services in the pod. |
| `make dev-down`  | Remove the pod. The database volume stays. |
| `scripts/dev.sh down --volumes` | Remove the pod and the database volume. |

After a change to `0001_init.sql`, remove the database volume with
`scripts/dev.sh down --volumes`. Then start the pod again with
`make dev-up`. The server does not apply a changed migration file to an
existing database.
| `scripts/dev.sh logs app`       | Show the log of a container (`db`, `keycloak` or `app`). |

Do not use `make run` and `make dev-app` at the same time. Both use port
8080.

## Tests

There are three levels of tests.

| Command                 | Tests | Needs |
|-------------------------|-------|-------|
| `make test`             | Unit tests. The database tests do not run. | Go |
| `make test-integration` | All unit tests and database tests. | Podman |
| `make test-e2e`         | Sign-in through Keycloak, documents, RBAC, system administration and a second tenant. | `make dev-app` |

To run the benchmark for large spaces:

```sh
scripts/test-integration.sh -run '^$' -bench AccessLargeSpace ./internal/store/
```

`make test-integration` starts a temporary PostgreSQL container. Each
test package gets its own database. The script removes the container
after the tests. The tests run with the race detector.

The HTTP tests in `internal/web` use a fake identity provider. The
end-to-end test in `e2e` uses the real Keycloak login form.

## Editor

The source of the editor script is in `web/editor`:

| File | Contents |
|------|----------|
| `src/editor.js` | The script. It starts Tiptap and connects the toolbar of the page to the commands. |
| `package.json` | The exact versions of the packages. |
| `package-lock.json` | The versions and the checksums of all packages and their dependencies. |

Run `make editor` after a change to these files. The command runs
`scripts/build-editor.sh`. The script uses a Node container in Podman:

1. `npm ci --ignore-scripts` installs the packages from the lock file.
   The packages cannot run install scripts.
2. `npm audit` stops the build if a package has a known vulnerability
   with the level "moderate" or higher.
3. esbuild makes one minified file, `internal/web/static/editor.js`.

Commit `editor.js` together with the source. The Go program embeds the
file. Thus `go build` does not need Node.

To update Tiptap, change the versions in `package.json`. Use the same
version for all `@tiptap` packages. Then run this command and
`make editor`:

```sh
podman run --rm -v "$PWD/web/editor":/src:Z -w /src docker.io/library/node:24-alpine \
  npm install --ignore-scripts --no-audit --no-fund
```

Rules for the script:

- Do not use inline styles or style elements. The security policy of
  the page does not permit them. Use CSS classes in `style.css`.
- Each button of the toolbar is in the template `edit.html`. The
  attribute `data-command` names the command.
- The server checks all data from the editor again. Add a new node,
  mark or attribute to the list in `internal/doc` first. Otherwise the
  server removes it.

The tests in `internal/doc` and `internal/web` do not run the script.
Before a release, open the editor in a browser and try each button.

## Checks before a commit

1. Run `make lint`.
2. Run `make test-integration`.
3. If you changed the login or RBAC, run `make dev-app` and
   `make test-e2e`.
4. If you changed the editor, run `make editor`. Then try the editor in
   a browser.

## Rules for code and text

- Write documentation, code comments and commit messages in Simplified
  Technical English (ASD-STE100).
- Put documentation in the `docs` folder.
- Do not use JavaScript in the web pages. The only exception is the
  editor of a document.
- Put all SQL queries in the `internal/store` package.
- Give the tenant ID to each store function for tenant data. Use it in
  each query.
- Before the first release, change `0001_init.sql` for a schema change.
  Do not add migration code for old data.
