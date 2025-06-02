# ---- Builder Stage ----
# Use a specific Go version for reproducibility. Alpine variants are smaller.
FROM golang:1.24-alpine AS builder

# Set the working directory for the builder stage
WORKDIR /src/app

# Install git and ca-certificates.
# Git might be needed if any modules (even with vendoring) have submodules or need git for versioning.
# ca-certificates are good for any potential HTTPS calls during build (though vendoring minimizes this).
RUN apk add --no-cache git ca-certificates

# Copy go.mod and go.sum first to leverage Docker cache for dependency resolution steps.
COPY go.mod go.sum ./

# --- Dependency Handling: Vendoring (Recommended) ---
# 1. Locally, run: `go mod tidy && go mod vendor`
# 2. Commit the 'vendor' directory to your repository.
# Copy the vendor directory.
COPY vendor/ ./vendor/

# Set GOFLAGS to use the vendored modules. This ensures that Go uses the packages from the vendor directory.
ENV GOFLAGS="-mod=vendor"

# Verify dependencies.
RUN go mod verify

# Copy the rest of the application source code
# This should come after dependency handling to optimize Docker layer caching.
COPY . .

# Build the main application
# CGO_ENABLED=0 ensures a static binary without C dependencies.
# -ldflags '-s -w' strips debug symbols and DWARF info, reducing binary size.
# -extldflags "-static"' attempts to statically link against external libraries (for truly static binaries).
RUN CGO_ENABLED=0 go build -a -ldflags '-s -w -extldflags "-static"' -o /app/docker-cert ./cmd/docker-cert/main.go

# Build the healthcheck utility
RUN CGO_ENABLED=0 go build -a -ldflags '-s -w -extldflags "-static"' -o /app/healthcheck ./cmd/healthcheck/main.go

# Ensure the built binaries are executable before copying to the final stage.
RUN chmod +x /app/docker-cert /app/healthcheck


# --- Final Stage ---
# Use a distroless base image. base-debian12 includes essential libraries like glibc and CA certificates.
FROM gcr.io/distroless/base-debian12 AS final

# The nonroot user (UID 1001, GID 1001) is the default in distroless/base.
# We will explicitly set it for clarity and ensure our files are owned by it.
USER 1001:1001

# Set the working directory for the final image.
# This directory will be used by the nonroot user.
WORKDIR /app

# Copy the compiled application binary from the builder stage.
# Use --chown to set the owner to the nonroot user (UID 1001, GID 1001).
# Permissions (including execute) set in the builder stage are preserved.
COPY --chown=1001:1001 --from=builder /app/docker-cert /app/docker-cert

# Copy the compiled healthcheck utility from the builder stage.
COPY --chown=1001:1001 --from=builder /app/healthcheck /app/healthcheck

# Expose the internal HTTP port (for health checks and API).
# This should match the INTERNAL_HTTP_PORT environment variable's default or actual value.
EXPOSE 8080

# Environment variable for the internal port (can be overridden at runtime).
ENV INTERNAL_HTTP_PORT=8080

# HEALTHCHECK instruction using the Go utility.
# The nonroot user (1001) must have execute permission on /app/healthcheck.
HEALTHCHECK --interval=1m30s --timeout=10s --start-period=45s --retries=3 \
  CMD ["/app/healthcheck"]

# Command to run the application.
# The nonroot user (1001) must have execute permission on /app/docker-cert.
ENTRYPOINT ["/app/docker-cert"]

# Optional: Default command arguments if your entrypoint needs them.
# CMD ["--default-arg", "value"]