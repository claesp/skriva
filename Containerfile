# Each base image has a tag and the digest of its multi-arch index. The
# digest makes sure that the build always uses the same image. See
# docs/security.md (Container) for an update.

# Build stage.
FROM docker.io/library/golang:1.26-alpine@sha256:27f2d3b83cfc5803c8078f8e730af0f7c431bf067afaec8f60a36fb763187cba AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# REVISION is the Git commit. The build context has no .git folder, thus
# Go cannot find the commit. Without REVISION the revision is "dev".
ARG REVISION=""
RUN CGO_ENABLED=0 go build -trimpath \
	-ldflags="-s -w -X github.com/claesp/skriva/internal/version.Revision=${REVISION}" \
	-o /out/skriva ./cmd/skriva
# The PDF export writes temporary files. The run stage has no /tmp. COPY
# copies the contents of a folder, thus the folder is in /out/root.
# The server makes its TLS certificate in /var/lib/skriva/tls if the
# operator gives none. Mount a volume there to keep it.
RUN mkdir -p /out/root/var/lib/skriva && mkdir -m 700 /out/root/tmp /out/root/var/lib/skriva/tls

# The Typst program for the PDF export. The program is a static binary.
FROM ghcr.io/typst/typst:0.15.1@sha256:032e292249bcd378480cc7c142cfa324b63ef8aadeb88d7e7230320c4c9c422f AS typst

# The mmdr program for Mermaid diagrams (mermaid-rs-renderer). The
# project has no static binary, thus the stage builds the program from
# source. The build on Alpine Linux makes a static binary. The lock file
# of the crate sets the versions of all dependencies. cargo-auditable
# records the dependencies in the binary. Thus the SBOM of a release can
# list them.
FROM docker.io/library/rust:1.99-alpine@sha256:27cd9befd4a05d6f6eb407c3e28365f5092f008bfc5946207cca76ae5179ef23 AS mmdr
RUN cargo install --locked cargo-auditable@0.7.7 && \
	cargo auditable install --locked --no-default-features --features cli \
	--root /out mermaid-rs-renderer@0.3.1

# Run stage. The image contains only the binaries, a temporary folder
# and CA certificates.
FROM scratch
ARG VERSION=""
ARG REVISION=""
LABEL org.opencontainers.image.title="Skriva" \
	org.opencontainers.image.description="A wiki with encrypted documents" \
	org.opencontainers.image.source="https://github.com/claesp/skriva" \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.vendor="Skriva Maintainers" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.revision="${REVISION}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/root/ /
COPY --from=typst /bin/typst /typst
COPY --from=mmdr /out/bin/mmdr /mmdr
COPY --from=build /out/skriva /skriva
ENV SKRIVA_TYPST=/typst SKRIVA_MMDR=/mmdr TMPDIR=/tmp
USER 65532:65532
EXPOSE 8443
ENTRYPOINT ["/skriva"]
