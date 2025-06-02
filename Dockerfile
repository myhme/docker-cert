# Use a specific Go version for reproducibility
FROM golang:1.24-alpine AS builder

# Set working directory for the main application
WORKDIR /app

# Copy go.mod and go.sum first to leverage Docker cache
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy the rest of the application source code
COPY . .

# Build the main application
RUN CGO_ENABLED=0 go build -a -installsuffix cgo -ldflags '-s -w -extldflags "-static"' -o /app/docker-cert ./cmd/docker-cert/main.go

# Build the healthcheck utility
RUN CGO_ENABLED=0 go build -a -ldflags '-s -w -extldflags "-static"' -o /app/healthcheck ./cmd/healthcheck/main.go


# --- Final Stage ---
# Use a minimal base image like alpine or distroless.
# For distroless/static, ensure your Go binaries are fully static.
# FROM gcr.io/distroless/static-debian11 AS final # Example for distroless
FROM alpine:latest AS final

WORKDIR /app

# Copy the compiled application from the builder stage
COPY --from=builder /app/docker-cert /app/docker-cert
# Copy the compiled healthcheck utility from the builder stage
COPY --from=builder /app/healthcheck /app/healthcheck


# Expose the internal HTTP port (for health checks and API)
# This should match the INTERNAL_HTTP_PORT environment variable's default or actual value.
EXPOSE 8080

# Environment variable for the internal port (can be overridden)
ENV INTERNAL_HTTP_PORT=8080

# HEALTHCHECK instruction using the Go utility
HEALTHCHECK --interval=1m30s --timeout=10s --start-period=45s --retries=3 \
  CMD ["/app/healthcheck"]

# Command to run the application
ENTRYPOINT ["/app/docker-cert"]
