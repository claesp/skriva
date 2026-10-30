# Configuration

The server reads its settings from environment variables. The server
does not start if a necessary setting is missing or not valid.

## Settings

| Variable                         | Default                 | Description |
|----------------------------------|-------------------------|-------------|
| `SKRIVA_DATABASE_URL`            | (necessary)             | PostgreSQL URL, for example `postgres://user:pw@host:5432/skriva?sslmode=require`. |
| `SKRIVA_OIDC_ISSUER`             | (necessary)             | The issuer URL of the OIDC provider. |
| `SKRIVA_OIDC_CLIENT_ID`          | (necessary)             | The client ID. |
| `SKRIVA_OIDC_CLIENT_SECRET`      | (necessary)             | The client secret. |
| `SKRIVA_OIDC_CLIENT_SECRET_FILE` |                         | A file that contains the client secret. Use this or the previous variable. |
| `SKRIVA_OIDC_SCOPES`             | `openid profile email`  | The scopes. Use spaces between the scopes. |
| `SKRIVA_OIDC_GROUPS_CLAIM`       | `groups`                | The ID token claim with the group names. |
| `SKRIVA_BASE_URL`                | `http://localhost:8080` | The public URL of the server. |
| `SKRIVA_LISTEN_ADDR`             | `:8080`                 | The address for the HTTP server. |
| `SKRIVA_ADMIN_GROUP`             |                         | An OIDC group. At start, the server gives the `admin` role to this group. |
| `SKRIVA_GROUP_ROLES`             |                         | More group roles, for example `editors=editor,writers=maintainer`. |
| `SKRIVA_DEFAULT_ROLE`            | `viewer`                | The role of all signed-in users. Use `none` for no role. |
| `SKRIVA_SESSION_TTL`             | `12h`                   | The maximum life of a session. |

## Base URL

The server uses `SKRIVA_BASE_URL` for these items:

- The redirect URL for OIDC: `<base URL>/auth/callback`.
- The URL after logout: `<base URL>/signed-out`.
- The cookie mode. If the URL starts with `https`, the cookies get the
  `Secure` attribute and the `__Host-` prefix. The server also sends the
  `Strict-Transport-Security` header.

Use an `https` URL in production.

## OIDC provider

Make a confidential client at the provider with these settings:

1. Set the flow to "authorization code".
2. Set the redirect URI to `<base URL>/auth/callback`.
3. Set the post-logout redirect URI to `<base URL>/signed-out`.
4. Add the group names to the ID token, in the claim
   `SKRIVA_OIDC_GROUPS_CLAIM`. The claim must be a list of strings.
5. Enable PKCE with the method `S256`. Skriva always sends PKCE.

The file `deploy/keycloak/realm-skriva.json` shows a correct Keycloak
client. In Keycloak, set "Full group path" to off. Then the group names
do not start with `/`.

## Secrets

Do not put the client secret in an environment variable in production.
Put the secret in a file. Set `SKRIVA_OIDC_CLIENT_SECRET_FILE` to the
path of the file. With Podman, use `podman secret`:

```sh
printf '%s' 'the-secret' | podman secret create skriva-oidc -
podman run --secret skriva-oidc \
  -e SKRIVA_OIDC_CLIENT_SECRET_FILE=/run/secrets/skriva-oidc ...
```

The database password can also be in a file. Use the PostgreSQL
`passfile` parameter or the `PGPASSFILE` variable.
