FROM golang:latest AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg/ pkg/
COPY cmd/mock-akamai-gtm/ cmd/mock-akamai-gtm/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app ./cmd/mock-akamai-gtm

FROM gcr.io/distroless/static-debian12
COPY --from=builder /app /app
# Baked in at build time rather than bind-mounted at runtime: mounting
# certs/ live from a cloud-synced host path (e.g. OneDrive) into the
# container can hit "resource deadlock avoided" (EDEADLK) reading the
# files, because the sync agent's file locking conflicts with the
# container runtime's bind-mount read path. A build-time COPY has no such
# runtime dependency and works the same regardless of where the repo lives
# on disk.
COPY certs/jpm.com.crt certs/jpm.com.key /certs/
ENTRYPOINT ["/app"]
