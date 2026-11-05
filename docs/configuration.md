# Configuration

There are two levels of configuration:

- **Installation settings.** Environment variables. They apply to the
  full installation.
- **Tenant settings.** They are in the database. A system administrator
  changes them on the page `/admin/`. See [Administration](administration.md).

The server does not start if a necessary environment variable is
missing or not valid.

## Environment variables

| Variable                         | Default                 | Description |
|----------------------------------|-------------------------|-------------|
| `SKRIVA_DATABASE_URL`            | (necessary)             | PostgreSQL URL, for example `postgres://user@host:5432/skriva?sslmode=verify-full`. |
| `SKRIVA_ENCRYPTION_KEY`          | (necessary)             | 32 random bytes in base64. The key encryption key (KEK). It encrypts the client secrets and the keys of the tenants. See [Security: encryption at rest](security.md#encryption-at-rest). |
| `SKRIVA_ENCRYPTION_KEY_FILE`     |                         | A file that contains the key. Use this or the previous variable. |
| `SKRIVA_ENCRYPTION_KEY_PREVIOUS` |                         | The old KEK, only after a change of the KEK. At start, the server encrypts the keys and the client secrets again with the new KEK. Then remove this variable. `SKRIVA_ENCRYPTION_KEY_PREVIOUS_FILE` is also possible. |
| `SKRIVA_OIDC_ISSUER`             | (necessary)             | The issuer URL of the system identity provider. |
| `SKRIVA_OIDC_CLIENT_ID`          | (necessary)             | The client ID. |
| `SKRIVA_OIDC_CLIENT_SECRET`      | (necessary)             | The client secret. |
| `SKRIVA_OIDC_CLIENT_SECRET_FILE` |                         | A file that contains the client secret. |
| `SKRIVA_OIDC_SCOPES`             | `openid profile email`  | The scopes. Use spaces between the scopes. |
| `SKRIVA_OIDC_GROUPS_CLAIM`       | `groups`                | The ID token claim with the group names. |
| `SKRIVA_SYSTEM_ADMIN_GROUP`      |                         | The OIDC group of the system administrators. If it is empty, nobody can sign in at `/admin/`. |
| `SKRIVA_ADMIN_GROUP`             |                         | The admin group of the default tenant, if the tenant has none. |
| `SKRIVA_GROUP_ROLES`             |                         | More group roles in the default tenant, for example `editors=editor,writers=maintainer`. |
| `SKRIVA_SESSION_TTL`             | `4h`                    | The maximum life of a system administrator session. It must be one minute or more. |
| `SKRIVA_BASE_URL`                | `http://localhost:8080` | The public URL of the server. It must not have a path, a user, a query or a fragment. |
| `SKRIVA_LISTEN_ADDR`             | `:8080`                 | The address for the HTTP server. |
| `SKRIVA_TYPST`                   | `/typst` in the image   | The path of the [Typst](https://typst.app) program for the PDF export. If it is set, the program must exist, or the server does not start. If it is not set, the server finds `typst` in the `PATH`. Without the program, the PDF export is not available. |
| `SKRIVA_MMDR`                    | `/mmdr` in the image    | The path of the [mmdr](https://github.com/1jehuang/mermaid-rs-renderer) program for Mermaid diagrams. If it is set, the program must exist, or the server does not start. If it is not set, the server finds `mmdr` in the `PATH`. Without the program, a diagram is a code block. |
| `SKRIVA_TYPST_MEMORY`            | `1024`                  | The largest data memory of one Typst process, in MiB (16 to 65536). |
| `SKRIVA_MMDR_MEMORY`             | `256`                   | The largest data memory of one mmdr process, in MiB (16 to 65536). |
| `SKRIVA_LANGUAGE`                | `en`                    | The default language of the user interface: `en` (English) or `sv` (Swedish). A tenant can set a different language, and each user can select a language in the profile. A new tenant also gets this language as its content language. See [Languages](administration.md#languages). |

## The system identity provider

The `SKRIVA_OIDC_*` variables set the identity provider for the system
administration at `/admin/`. A user can sign in there only if the groups
claim contains `SKRIVA_SYSTEM_ADMIN_GROUP`.

At start, the server also does these steps for the default tenant:

1. If the tenant has no identity provider, it gets the `SKRIVA_OIDC_*`
   settings.
2. If the tenant has no admin group, it gets `SKRIVA_ADMIN_GROUP`.
3. Each group in `SKRIVA_GROUP_ROLES` gets its role.

Then the admin group of each tenant gets the `admin` role.

## The encryption key

Make a key with this command:

```sh
head -c 32 /dev/urandom | base64
```

Keep the key in a secret store. Make a backup of the key. Without the
key, the server does not start, and nobody can read the documents, the
files or the client secrets, also not from a backup.

## Base URL

The server uses `SKRIVA_BASE_URL` for these items:

- The redirect URLs: `<base URL>/admin/auth/callback` and
  `<base URL>/{tenant}/auth/callback`.
- The URLs after logout: `<base URL>/admin/signed-out` and
  `<base URL>/{tenant}/signed-out`.
- The cookie mode. If the URL starts with `https`, the cookies get the
  `Secure` attribute and the `__Host-` prefix. The server also sends the
  `Strict-Transport-Security` header.

Use an `https` URL in production.

## OIDC clients

Make a confidential client at each provider with these settings:

1. Set the flow to "authorization code".
2. Add the redirect URIs:
   - For the system administration: `<base URL>/admin/auth/callback`.
   - For each tenant that uses the provider:
     `<base URL>/{tenant}/auth/callback`.
3. Add the post-logout redirect URIs in the same way, with
   `signed-out` in place of `auth/callback`.
4. Add the group names to the ID token, in the groups claim. The claim
   must be a list of strings.
5. Enable PKCE with the method `S256`. Skriva always sends PKCE.

The tenant page in the system administration shows the two URIs for
the tenant.

The file `deploy/keycloak/realm-skriva.json` shows a Keycloak client for
development. It permits all redirect URIs on `http://localhost:8080`. Do
not use a wildcard in production. In Keycloak, set "Full group path" to
off. Then the group names do not start with `/`.

## Secrets

Do not put secrets in environment variables in production. Put each
secret in a file. Set the `_FILE` variable to the path of the file. With
Podman, use `podman secret`:

```sh
head -c 32 /dev/urandom | base64 | podman secret create skriva-key -
printf '%s' 'the-secret' | podman secret create skriva-oidc -
podman run --secret skriva-key --secret skriva-oidc \
  -e SKRIVA_ENCRYPTION_KEY_FILE=/run/secrets/skriva-key \
  -e SKRIVA_OIDC_CLIENT_SECRET_FILE=/run/secrets/skriva-oidc ...
```

The database password can also be in a file. Use the PostgreSQL
`passfile` parameter or the `PGPASSFILE` variable.
