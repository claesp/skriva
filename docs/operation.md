# Operation

## Build the container image

```sh
make image
```

The result is the image `localhost/skriva:latest`.

## Run the container

The example uses Podman secrets for the encryption key and the client
secret.

```sh
head -c 32 /dev/urandom | base64 | podman secret create skriva-key -
printf '%s' 'the-client-secret' | podman secret create skriva-oidc -

podman run -d --name skriva -p 8080:8080 \
  --secret skriva-key --secret skriva-oidc \
  -e SKRIVA_ENCRYPTION_KEY_FILE=/run/secrets/skriva-key \
  -e SKRIVA_OIDC_CLIENT_SECRET_FILE=/run/secrets/skriva-oidc \
  -e SKRIVA_SYSTEM_ADMIN_GROUP=wiki-system-admins \
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
log line with the method, path, status, duration, tenant and user ID. The logs
also record these events:

- Sign-in of a user.
- Creation, change and deletion of tenants and spaces.
- Deletion of folders and documents.
- Changes to users, roles and role bindings.
- Failed CSRF checks and failed sign-ins.

The logs do not contain session tokens, ID tokens or secrets.

The database also keeps an audit log for the administrators. See
[Administration: audit log](administration.md#audit-log). The server
writes each audit event in the same transaction as the change. Thus the
audit log cannot miss a change that the database saved.

## Maintenance

The server deletes expired sessions and login data every 10 minutes.
At the same time, it permanently deletes the items that are in the
trash for more than 30 days, the audit events that are older than 365
days, and the uploaded images that no document uses after one day.

## Encryption keys

The content of the documents is encrypted with keys of the tenants. The
keys are encrypted with `SKRIVA_ENCRYPTION_KEY`. See
[Security: encryption at rest](security.md#encryption-at-rest).

- **Keep the key safe.** Without the key, nobody can read the documents,
  also not from a backup. Keep a copy of the key in a safe place, apart
  from the backups.
- **New data key for a tenant.** Open the system administration, the
  tenant, then **Make a new data key** in **Encryption**. The cleanup
  task (every 10 minutes) encrypts the old values again. The tenant page
  shows the number of values that still use an old key.
- **New key encryption key.** Make a new key (`openssl rand -base64 32`).
  Set it in `SKRIVA_ENCRYPTION_KEY`, and the old key in
  `SKRIVA_ENCRYPTION_KEY_PREVIOUS`. Start the server. The log tells when
  the server wrapped the keys again. Then remove
  `SKRIVA_ENCRYPTION_KEY_PREVIOUS` and destroy the old key. With more than
  one server, start one server with both keys first.

## Backup

All data is in PostgreSQL, also the uploaded images. Also make a backup of the encryption key.
Without the key, the server cannot read the client secrets of the
tenants. Use `pg_dump` or the backup system of your database. Example:

```sh
pg_dump --format=custom --file=skriva.dump "$SKRIVA_DATABASE_URL"
```
