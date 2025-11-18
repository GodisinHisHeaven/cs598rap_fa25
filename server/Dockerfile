# Multi-stage build for Raft KV server
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o raft-kv ./cmd/server

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates curl iptables iproute2

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/raft-kv .

# Create data directory
RUN mkdir -p /data

EXPOSE 8080 9000 9090

CMD ["./raft-kv", "-data-dir=/data"]
