# Operation

Skriva has three forms. Each release has all of them, for `amd64` and
`arm64`. See [Release](release.md) for the versions and the files of a
release.

| Form | Use it for |
|------|------------|
| Container image with Podman Compose | The usual installation. |
| Debian package (`.deb`) | Debian, Ubuntu and other Debian-derived distributions. |
| RPM package (`.rpm`) | Red Hat Enterprise Linux, AlmaLinux, Rocky Linux, Fedora and other Red Hat-derived distributions. |

All forms contain the [Typst](https://typst.app) program for the PDF
export and the [mmdr](https://github.com/1jehuang/mermaid-rs-renderer)
program for Mermaid diagrams. All forms need a PostgreSQL database and
an OpenID Connect provider. All connections use TLS: HTTPS for the
browsers, and TLS to the database and the OIDC provider. Give your own
certificates, from a CA that the clients trust. Without them, the
installation makes a private CA and self-signed certificates. See
[Configuration: TLS](configuration.md#tls) and [Security](security.md).

Before you install a release, verify its files. See
[Release: verify a release](release.md#verify-a-release).

## Install with Podman Compose

The compose file has the Skriva server and PostgreSQL. It refers to
each image by its digest. Thus it always starts the same images.

1. Download `skriva_<version>_compose.tar.gz` from the release, and
   extract it:

   ```sh
   tar -xzf skriva_0.1.3f9a2c1d4e5b_compose.tar.gz
   cd skriva_0.1.3f9a2c1d4e5b_compose
   ```

2. Edit `skriva.env`: the base URL, the OIDC issuer, the client ID and
   the group of the system administrators. See
   [Configuration](configuration.md).
3. If you have certificates, put them into the folder `secrets/tls`:
   `skriva.crt` and `skriva.key` for the web server (the certificate
   with its chain), `postgres.crt` and `postgres.key` for the database,
   and `ca.crt` with the CA certificates that verify the database and
   the OIDC provider. A file can contain more than one CA certificate.
4. Make the secrets. The script writes them into the folder `secrets`:
   the database password, the database URL, the encryption key, the OIDC
   client secret, and the certificates that you did not give. It makes
   the certificates with a private CA in `secrets/ca`, valid for 10
   years. The certificate of the web server gets the host of
   `SKRIVA_BASE_URL`.

   ```sh
   sh make-secrets.sh 'the-oidc-client-secret'
   ```

   Keep a copy of `secrets/encryption_key` in a safe place. Without it,
   nobody can read the documents. With the private CA, install
   `secrets/ca/ca.crt` in the browsers as a trusted CA.

5. Start the containers:

   ```sh
   podman compose up -d
   podman compose logs skriva
   ```

The server listens for HTTPS on port 8443. To use port 443, change the
port in `compose.yaml` to `"443:8443"`. PostgreSQL accepts only TLS
connections from the network. `podman compose` uses
`podman-compose` or `docker-compose`. With `docker-compose`, start the
Podman socket first: `systemctl --user enable --now podman.socket`. To
use `podman-compose`, set `PODMAN_COMPOSE_PROVIDER=podman-compose`.

The root file system of the Skriva container is read-only. The server
writes temporary files for the PDF export into a `tmpfs` at `/tmp`.

## Install a package

The packages install these files:

| File | Content |
|------|---------|
| `/usr/bin/skriva` | The server. |
| `/usr/lib/skriva/typst`, `/usr/lib/skriva/mmdr` | The programs for the PDF export and the Mermaid diagrams. |
| `/usr/lib/systemd/system/skriva.service` | The systemd service. |
| `/etc/skriva/skriva.env` | The settings. An update does not replace your changes. |

1. Install the package:

   ```sh
   sudo apt install ./skriva_0.1.3f9a2c1d4e5b-1_amd64.deb      # Debian, Ubuntu
   sudo dnf install ./skriva-0.1.3f9a2c1d4e5b-1.x86_64.rpm     # RHEL, Fedora
   ```

2. Edit `/etc/skriva/skriva.env`. The file contains secrets. Only root
   can read it.
3. The package makes a private CA in `/etc/skriva/ca` and the
   certificates of Skriva, Keycloak and PostgreSQL in `/etc/skriva/tls`,
   with the host name of the computer. Replace `skriva.crt`, `skriva.key`
   and `ca.crt` with your own files if you have them. Configure
   PostgreSQL for TLS with `postgres.crt` and `postgres.key`, or with your
   own certificate. See [Configuration: TLS](configuration.md#tls).
4. Start the service:

   ```sh
   sudo systemctl enable --now skriva
   journalctl -u skriva
   ```

The service runs as the system user `skriva` in a strict sandbox. The
server keeps no files of its own. All data is in PostgreSQL. The key of
the certificate, `/etc/skriva/tls/skriva.key`, must be readable for the
group `skriva`: `chgrp skriva skriva.key` and `chmod 640 skriva.key`.
The server listens for HTTPS on port 8443.

## Run the container without compose

The example uses Podman secrets for the encryption key, the client
secret and the key of the certificate. Use the image and the digest of
a release. Never use the tag `latest`: Skriva has no such tag.

```sh
head -c 32 /dev/urandom | base64 | podman secret create skriva-key -
printf '%s' 'the-client-secret' | podman secret create skriva-oidc -
podman secret create skriva-tls-key wiki.example.com.key

podman run -d --name skriva -p 8443:8443 \
  --read-only --tmpfs /tmp:mode=1777 \
  --secret skriva-key --secret skriva-oidc \
  --secret skriva-tls-key,uid=65532,mode=0400 \
  -v ./wiki.example.com.crt:/tls/skriva.crt:ro,Z \
  -e SKRIVA_TLS_CERT_FILE=/tls/skriva.crt \
  -e SKRIVA_TLS_KEY_FILE=/run/secrets/skriva-tls-key \
  -e SKRIVA_ENCRYPTION_KEY_FILE=/run/secrets/skriva-key \
  -e SKRIVA_OIDC_CLIENT_SECRET_FILE=/run/secrets/skriva-oidc \
  -e SKRIVA_SYSTEM_ADMIN_GROUP=wiki-system-admins \
  -e SKRIVA_DATABASE_URL='postgres://skriva@db.example.com/skriva?sslmode=verify-full' \
  -e SKRIVA_BASE_URL=https://wiki.example.com \
  -e SKRIVA_OIDC_ISSUER=https://sso.example.com/realms/example \
  -e SKRIVA_OIDC_CLIENT_ID=skriva \
  -e SKRIVA_ADMIN_GROUP=wiki-admins \
  ghcr.io/claesp/skriva:0.1.3f9a2c1d4e5b@sha256:...
```

Without a certificate, mount a volume on `/var/lib/skriva/tls`, for
example `-v skriva-tls:/var/lib/skriva/tls`. At the first start, the
server makes a private CA and a certificate in it.

## Build the container image

```sh
make image
```

The result is the image `localhost/skriva:<major>.<minor>.<revision>`,
for example `localhost/skriva:0.1.3f9a2c1d4e5b`. The build compiles
mmdr from source, thus the first build takes some minutes. To make all
release files for one architecture, run `make packages ARCH=amd64`. See
[Release](release.md).

## Start and stop

At start, the server does these steps:

1. It connects to the database. It tries again for 30 seconds.
2. The boot step. The server compares the schema version of the
   database with its own schema version:
   - The same version: the server continues.
   - The database is older: the server applies the missing migrations,
     in order. Each migration runs in one transaction. An advisory lock
     makes sure that only one server applies them.
   - The database is newer: the server stops with an error. An older
     version must not change the data of a newer schema. Install the
     newer version.

   The server also stops if a migration in the database is different
   from the same migration in the program (a SHA-256 checksum).
3. It prepares the default tenant and the keys. Then it accepts
   requests.

- If the OIDC provider is not available at start, the server starts
  anyway. It tries the provider again at the first sign-in.
- On `SIGTERM` or `SIGINT`, the server stops to accept new connections.
  It waits a maximum of 20 seconds for the open requests.

You can run more than one server with the same database. The servers
keep no state in memory, except caches: the OIDC provider data, the
keys of the tenants (5 minutes) and the diagram images. These caches are
safe with more than one server.

## Commands

| Command | Action |
|---------|--------|
| `skriva` | Start the server. |
| `skriva migrate` | Apply the database migrations and stop. It needs only `SKRIVA_DATABASE_URL`. |
| `skriva version` | Show the version and the schema version, for example `skriva 0.1.3f9a2c1d4e5b (schema version 1)`. |

The server also writes its version into the log at start.

## Update

A new version updates the database itself: at start, the server applies
the new migrations (see [Start and stop](#start-and-stop)). Only a new
major version changes the database schema. See
[Release](release.md#versions).

1. Read the release notes. They tell if the release changes the
   database schema.
2. If the major version changes, make a backup of the database (see
   [Backup](#backup)). You cannot go back to an older major version
   without the backup. Stop all servers of the old version.
3. Install the new version:
   - **Compose:** download the compose files of the new release. Copy
     your `skriva.env` and the folder `secrets` into the new folder.
     Then run `podman compose up -d` in the new folder. The volume of
     the database stays.
   - **Package:** install the new package. The package restarts the
     service if it runs.
4. Look at the log. It shows the migrations that the server applied.

To apply the migrations at a time that you select, set
`SKRIVA_AUTO_MIGRATE=false`. Then a server that needs migrations stops
with an error. Run `skriva migrate`, then start the servers. With
compose: `podman compose run --rm skriva migrate`.

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
- Changes to users, roles and role bindings. Changes to the members of
  groups are only in the audit log.
- Failed CSRF checks and failed sign-ins.

The logs do not contain session tokens, ID tokens or secrets.

The database also keeps an audit log for the administrators. See
[Administration: audit log](administration.md#audit-log). The server
writes each audit event in the same transaction as the change. Thus the
audit log cannot miss a change that the database saved.

## Maintenance

Every 10 minutes, the cleanup task does these steps:

1. It deletes the expired sessions and login data.
2. It permanently deletes these items:
   - the items that are in the trash for more than 30 days,
   - the audit events that are older than 365 days,
   - the uploaded images that no document uses after one day.
3. It encrypts the values that use an old data key again, for a maximum
   of 2 minutes. See [Encryption keys](#encryption-keys).

If one step fails, the other steps still run. The log shows the error.

## Encryption keys

The server encrypts the content of the documents with the keys of the
tenants. `SKRIVA_ENCRYPTION_KEY` encrypts these keys. See
[Security: encryption at rest](security.md#encryption-at-rest).

- **Keep the key safe.** Without the key, nobody can read the documents,
  also not from a backup. Keep a copy of the key in a safe place, apart
  from the backups.
- **New data key for a tenant.** Open the system administration, the
  tenant, then **Make a new data key** in **Encryption**. The cleanup
  task (every 10 minutes) encrypts the old values again. The tenant page
  shows the number of values that still use an old key.
- **New key encryption key.** Make a new key
  (`head -c 32 /dev/urandom | base64`).
  Set it in `SKRIVA_ENCRYPTION_KEY`, and the old key in
  `SKRIVA_ENCRYPTION_KEY_PREVIOUS`. Start the server. The log tells when
  the server wrapped the keys again. Then remove
  `SKRIVA_ENCRYPTION_KEY_PREVIOUS` and destroy the old key. With more than
  one server, start one server with both keys first.

## Backup

All data is in PostgreSQL, also the uploaded images. Also make a backup
of the encryption key. Without the key, nobody can read the documents,
the files and the client secrets of a backup. Use `pg_dump` or the
backup system of your database. Example:

```sh
pg_dump --format=custom --file=skriva.dump "$SKRIVA_DATABASE_URL"
```
