# Stage 1: Build
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /build/edge-dns  ./cmd/edgedns/ && \
    go build -ldflags="-s -w" -o /build/publisher  ./cmd/publisher/ && \
    go build -ldflags="-s -w" -o /build/admin-api  ./cmd/adminapi/ && \
    go build -ldflags="-s -w" -o /build/sniproxy  ./cmd/sniproxy/

# Stage 2: Runtime (default — edge-dns)
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /build/edge-dns   /app/edge-dns
COPY --from=builder /build/publisher  /app/publisher
COPY --from=builder /build/admin-api  /app/admin-api
COPY --from=builder /build/sniproxy  /app/sniproxy

EXPOSE 8053/tcp
EXPOSE 8053/udp
EXPOSE 8443/tcp
EXPOSE 8853/tcp
EXPOSE 443/tcp

ENTRYPOINT ["/app/edge-dns"]
