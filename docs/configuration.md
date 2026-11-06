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
| `SKRIVA_DATABASE_URL_FILE`       |                         | A file that contains the URL, if the URL contains a password. Use this or the previous variable. |
| `SKRIVA_AUTO_MIGRATE`            | `true`                  | `true`: at start, the server applies the database migrations. `false`: the server stops if the database needs migrations. Then run `skriva migrate`. See [Operation: updates](operation.md#update). |
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
| `SKRIVA_BASE_URL`                | `https://localhost:8443` | The public URL of the server. It must not have a path, a user, a query or a fragment. With TLS, it must be an `https` URL. Without TLS, the default is `http://localhost:8080`. |
| `SKRIVA_LISTEN_ADDR`             | `:8443`                 | The address of the web server. Without TLS, the default is `:8080`. |
| `SKRIVA_TLS`                     | `true`                  | `true`: the web server uses HTTPS. `false`: HTTP, only behind a reverse proxy that does TLS. See [TLS](#tls). |
| `SKRIVA_TLS_CERT_FILE`           |                         | The certificate of the web server and its chain, in PEM. Set it with `SKRIVA_TLS_KEY_FILE`. |
| `SKRIVA_TLS_KEY_FILE`            |                         | The private key of the certificate, in PEM. |
| `SKRIVA_TLS_DIR`                 | `/var/lib/skriva/tls`   | The folder of the generated certificate, if `SKRIVA_TLS_CERT_FILE` is not set. |
| `SKRIVA_TLS_CA_FILE`             |                         | A PEM file with more trusted CA certificates for the connections to the database and to the OIDC providers. The server always trusts the system certificates too. |
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
development. It permits all redirect URIs on `https://localhost:8443`. Do
not use a wildcard in production. In Keycloak, set "Full group path" to
off. Then the group names do not start with `/`.

## TLS

The server uses TLS for all its connections:

| Connection | Settings |
|------------|----------|
| Browser to Skriva (HTTPS) | `SKRIVA_TLS_CERT_FILE` and `SKRIVA_TLS_KEY_FILE`. |
| Skriva to PostgreSQL | `sslmode=verify-full` in `SKRIVA_DATABASE_URL`. The server trusts the system certificates and `SKRIVA_TLS_CA_FILE`. The URL parameter `sslrootcert` replaces them. |
| Skriva to the OIDC providers (for example Keycloak) | An `https` issuer URL. The server trusts the system certificates and `SKRIVA_TLS_CA_FILE`. |

If the connection to the database does not verify the certificate of
the server, the log shows a warning at start. A Unix socket needs no
TLS.

**Use your own certificates.** Give a certificate and its chain from a
CA that the browsers and the servers trust. Then the clients can
validate the certificates. The server reads the certificate files again
when they change, thus a new certificate (for example from an ACME
client) needs no restart.

**Generated certificates.** If `SKRIVA_TLS_CERT_FILE` is not set, the
server makes a private CA and a certificate in `SKRIVA_TLS_DIR` at the
first start, and uses them after that. The certificate has the host of
`SKRIVA_BASE_URL`, `localhost`, `127.0.0.1` and `::1`. The folder must be
writable. In a container, mount a volume on it. The browsers show a
warning, until you install the CA (`ca.crt` in the folder) in them.

The command `skriva certs` makes a private CA and the certificates of
Skriva, Keycloak and PostgreSQL, valid for 10 years. The compose files
and the packages use it if you give no certificates. It uses a CA that
exists, and it never replaces a file:

```sh
skriva certs -dir tls -ca-dir ca -skriva-hosts wiki.example.com \
  -keycloak-hosts sso.example.com -postgres-hosts db.example.com
```

| File | Use |
|------|-----|
| `ca/ca.crt`, `ca/ca.key` | The CA. Keep the key in a safe place. Only `skriva certs` needs it. |
| `tls/ca.crt` | A copy of the CA certificate, for the clients (`SKRIVA_TLS_CA_FILE`, the browsers). |
| `tls/skriva.crt`, `tls/skriva.key` | Skriva (`SKRIVA_TLS_CERT_FILE`, `SKRIVA_TLS_KEY_FILE`). |
| `tls/keycloak.crt`, `tls/keycloak.key` | Keycloak: `--https-certificate-file` and `--https-certificate-key-file`. |
| `tls/postgres.crt`, `tls/postgres.key` | PostgreSQL: `ssl = on`, `ssl_cert_file` and `ssl_key_file`. The PostgreSQL user must own the key, with mode `0600`. |

Each certificate also has the names `localhost`, `127.0.0.1` and `::1`,
and a service name: `skriva`, `keycloak`, or `db` and `postgres`.

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
