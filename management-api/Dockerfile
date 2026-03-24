FROM golang:latest AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg/ pkg/
COPY cmd/management-api/ cmd/management-api/
COPY migrations/ migrations/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app ./cmd/management-api

FROM gcr.io/distroless/static-debian12
COPY --from=builder /app /app
COPY --from=builder /src/migrations /migrations
ENTRYPOINT ["/app"]
