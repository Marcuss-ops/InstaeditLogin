# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o instaedit-server ./cmd/server/main.go

# Run stage
FROM alpine:3.21
WORKDIR /app

# Install certificates and create a non-root user
RUN apk --no-cache add ca-certificates wget && \
    adduser -D -g '' appuser

# Copy the compiled binary and set ownership
COPY --from=builder /app/instaedit-server .
RUN chown -R appuser:appuser /app

# Run as non-root user
USER appuser

# Expose the port the server listens on
EXPOSE 8080

# Health check for Railway/container orchestrators
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/api/v1/health || exit 1

CMD ["./instaedit-server"]
