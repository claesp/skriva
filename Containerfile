# Build stage.
FROM docker.io/library/golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/skriva ./cmd/skriva
# The PDF export writes temporary files. The run stage has no /tmp. COPY
# copies the contents of a folder, thus the folder is in /out/root.
RUN mkdir -p /out/root && mkdir -m 700 /out/root/tmp

# The Typst program for the PDF export. The program is a static binary.
# The digest makes sure that the build always uses the same version.
FROM ghcr.io/typst/typst:0.15.1@sha256:5410828f3b97f32fcc3eb95fed614c962d03f480f74cea06936540b95037d2ee AS typst

# Run stage. The image contains only the binaries, a temporary folder
# and CA certificates.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/root/ /
COPY --from=typst /bin/typst /typst
COPY --from=build /out/skriva /skriva
ENV SKRIVA_TYPST=/typst TMPDIR=/tmp
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/skriva"]
