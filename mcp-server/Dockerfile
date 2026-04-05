FROM golang:latest AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg/ pkg/
COPY cmd/mcp-server/ cmd/mcp-server/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /mcp-server ./cmd/mcp-server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /mcp-server /mcp-server
ENTRYPOINT ["/mcp-server"]
