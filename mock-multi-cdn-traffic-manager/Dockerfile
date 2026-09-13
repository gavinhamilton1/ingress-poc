FROM golang:latest AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg/ pkg/
COPY cmd/mock-multi-cdn-traffic-manager/ cmd/mock-multi-cdn-traffic-manager/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app ./cmd/mock-multi-cdn-traffic-manager

FROM gcr.io/distroless/static-debian12
COPY --from=builder /app /app
# Baked in at build time rather than bind-mounted at runtime — see the same
# note on mock-akamai-gtm/Dockerfile: mounting certs/ live from a
# cloud-synced host path (e.g. OneDrive) can hit "resource deadlock
# avoided" (EDEADLK) reading the files. This is now the component holding
# the jpm.com TLS cert, having replaced mock-akamai-gtm as the actual
# internet-facing front door.
COPY certs/jpm.com.crt certs/jpm.com.key /certs/
ENTRYPOINT ["/app"]
