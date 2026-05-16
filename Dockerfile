FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -s -w strips debug symbols → smaller binary (~30% smaller)
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o sml-api-bybos ./cmd/server

FROM alpine:3.20
# Security: run as non-root
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /app/sml-api-bybos .
USER app
EXPOSE 8200
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8200/health || exit 1
CMD ["./sml-api-bybos"]
