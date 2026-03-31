FROM golang:1.25 AS builder

WORKDIR /workspace

# Copy go module files first for better layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy entire project (operator imports from the same module).
COPY . .

# Build the operator binary.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o /ingress-operator \
    ./cmd/ingress-operator

# -------------------------------------------------------------------
# Runtime image
# -------------------------------------------------------------------
FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /ingress-operator /ingress-operator

USER 65532:65532

ENTRYPOINT ["/ingress-operator"]
