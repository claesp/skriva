# Build stage.
FROM docker.io/library/golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/skriva ./cmd/skriva

# Run stage. The image contains only the binary and CA certificates.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/skriva /skriva
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/skriva"]
