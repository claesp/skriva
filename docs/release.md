# Release

This document tells how the project numbers the versions, which rules
apply, and how to make and verify a release. For the installation and
the update, see [Operation](operation.md).

## Versions

A version has three parts: `major.minor.revision`, for example
`0.1.3f9a2c1d4e5b`.

| Part | Value | Change it when |
|------|-------|----------------|
| major | A number | The database schema changes. |
| minor | A number | Any other change goes into a release. Set it to 0 when the major version changes. |
| revision | The Git commit, 12 hexadecimal digits | Each commit changes it. The build sets it. |

Major and minor are in `internal/version/version.go` (`const Version`).
The first version is 0.1. A release has a Git tag `v<major>.<minor>`,
for example `v0.1`. Each release has a new major or minor version.
Thus the order of the versions is correct, also for the packages.

A build from a work tree with changes that are not committed has a
revision that ends with `-dirty`. A build without Git has the revision
`dev`.

## Rules

`scripts/check-release.sh` checks these rules for each push (workflow
`CI`) and for each release (workflow `Release`).

1. **A change of the database schema needs a new major version.** A
   schema change is a new file in `internal/db/migrations`. Change the
   version in the same change: from 0.4 to 1.0, from 1.2 to 2.0.
2. **Never change a migration of a release.** Add a new migration
   file. At start, the server stops if a migration in the database is
   different from the migration in the program.
3. **Never use the tag `latest` for a container image.** Skriva has no
   such tag.
4. **Each image has a digest**: in the Containerfile, in the compose
   files and in the scripts (`name:tag@sha256:...`). The tag shows only
   the version for the reader. The compose file of a release refers to
   the Skriva image by its digest.
5. **Each GitHub action has a full commit SHA**, not a tag. The comment
   after the SHA shows the version.
6. **The release tag is `v` + the version** in
   `internal/version/version.go`, and the version is higher than the
   version of the last release.

The schema of the release 0.1 is `0001_init.sql`. After the tag `v0.1`,
this file does not change.

## Make a release

1. Set the new version in `internal/version/version.go`. Use a new
   major version if `internal/db/migrations` has a new file after the
   last release. `make check-release` tells you.
2. Commit the change, and push it to `main`. Wait for the workflow
   `CI`.
3. Make the tag on that commit, and push it:

   ```sh
   git tag -a v0.2 -m "Skriva 0.2"
   git push origin v0.2
   ```

The tag starts the workflow `Release` (`.github/workflows/release.yml`):

1. **check**: the release rules, the lint, the unit tests and the
   integration tests.
2. **build**, for `amd64` and `arm64`, on a runner of that
   architecture:
   - `scripts/release-build.sh` builds the container image. Then it
     copies the programs from the image into the archive and the
     packages. Thus all forms contain the same programs.
   - The job pushes the image of the architecture to
     `ghcr.io/claesp/skriva:<full>-<arch>`.
   - Syft makes the SBOMs (SPDX) of the programs and of the image.
   - The job attests the provenance and the SBOM of each file and of the
     image.
3. **publish**:
   - It makes the multi-architecture image with the tags
     `<major>.<minor>.<revision>` and `<major>.<minor>`.
   - It writes `compose.yaml` from `deploy/compose/compose.template.yaml`
     with the digest of the image.
   - It writes `SHA256SUMS` and attests the remaining files.
   - It makes the GitHub release with all files. If the release changes
     the database schema, the release notes tell this.

If a step fails, no GitHub release is made. Fix the problem, and make a
new release with a new minor version. Do not move a tag.

Once only, after the first release: in the settings of the package
`skriva` on GitHub, set the visibility to public. Then everybody can
pull the image without a sign-in.

To make the files of a release on your computer, without a push:

```sh
make packages ARCH=amd64
```

The files are in `dist/`. The build of the other architecture needs
QEMU.

## Files of a release

`<full>` is `major.minor.revision`.

| File | Content |
|------|---------|
| `skriva_<full>_linux_<arch>.tar.gz` | The programs, the systemd service and the settings file. |
| `skriva_<full>-1_<arch>.deb` | The Debian package. |
| `skriva-<full>-1.<x86_64\|aarch64>.rpm` | The RPM package. |
| `skriva_<full>_compose.tar.gz` | `compose.yaml`, `skriva.env` and `make-secrets.sh`. |
| `compose.yaml` | The compose file only. |
| `skriva_<full>_linux_<arch>.spdx.json` | The SBOM of the programs in the archive and the packages. |
| `skriva_<full>_image_<arch>.spdx.json` | The SBOM of the container image of the architecture. |
| `SHA256SUMS` | The SHA-256 checksums of all files. |

The container image is `ghcr.io/claesp/skriva`. The release notes show
its digest.

The SBOMs list the Go modules of Skriva and the Rust crates of mmdr
(the build records them with `cargo auditable`). The Typst program
comes from the official Typst image. Its SBOM shows only the program,
not its crates.

## Verify a release

Verify the checksums of the files that you downloaded:

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

Verify the attestations with the GitHub CLI. An attestation proves that
the workflow of this repository made the file, from the commit of the
tag:

```sh
gh attestation verify skriva_0.1.3f9a2c1d4e5b-1_amd64.deb --repo claesp/skriva
gh attestation verify oci://ghcr.io/claesp/skriva@sha256:... --repo claesp/skriva
```

To see the SBOM attestation of a file, add
`--predicate-type https://spdx.dev/Document/v2.3`. For the image, use
the digest of the image of your architecture: the SBOM of the image is
attested for each architecture. This command shows the digests:

```sh
podman manifest inspect ghcr.io/claesp/skriva@sha256:...
```
