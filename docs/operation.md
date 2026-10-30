# Operation

## Build the container image

```sh
make image
```

The result is the image `localhost/skriva:latest`.

## Run the container

The example uses a Podman secret for the client secret.

```sh
printf '%s' 'the-client-secret' | podman secret create skriva-oidc -

podman run -d --name skriva -p 8080:8080 \
  --secret skriva-oidc \
  -e SKRIVA_OIDC_CLIENT_SECRET_FILE=/run/secrets/skriva-oidc \
  -e SKRIVA_DATABASE_URL='postgres://skriva@db.example.com/skriva?sslmode=verify-full' \
  -e SKRIVA_BASE_URL=https://wiki.example.com \
  -e SKRIVA_OIDC_ISSUER=https://sso.example.com/realms/example \
  -e SKRIVA_OIDC_CLIENT_ID=skriva \
  -e SKRIVA_ADMIN_GROUP=wiki-admins \
  localhost/skriva:latest
```

Put a reverse proxy with TLS in front of the server. See
[Security](security.md).

## Start and stop

- At start, the server connects to the database. It tries again for 30
  seconds. Then it applies the migrations.
- If the OIDC provider is not available at start, the server starts
  anyway. It tries the provider again at the first sign-in.
- On `SIGTERM` or `SIGINT`, the server stops to accept new connections.
  It waits a maximum of 20 seconds for the open requests.

You can run more than one server with the same database. The servers
keep no state in memory, except a cache of the OIDC provider data.

## Health checks

| Path       | Result |
|------------|--------|
| `/healthz` | `200` if the process runs. Use it for the liveness check. |
| `/readyz`  | `200` if the database responds, else `503`. Use it for the readiness check. |

## Logs

The server writes JSON logs to standard error. Each request gives one
log line with the method, path, status, duration and user ID. The logs
also record these events:

- Sign-in of a user.
- Deletion of a page.
- Changes to users, roles and role bindings.
- Failed CSRF checks and failed sign-ins.

The logs do not contain session tokens, ID tokens or secrets.

## Maintenance

The server deletes expired sessions and login data every 10 minutes.

## Backup

All data is in PostgreSQL. Use `pg_dump` or the backup system of your
database. Example:

```sh
pg_dump --format=custom --file=skriva.dump "$SKRIVA_DATABASE_URL"
```
