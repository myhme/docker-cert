# ---- Base Builder Stage (common setup, Go tools, vendored dependencies) ----
FROM golang:1.24-alpine AS base_builder
LABEL stage="base_builder"

WORKDIR /src/app

# Install git and ca-certificates (common build tools)
RUN apk add --no-cache git ca-certificates

# Copy module files and vendor directory first to leverage caching for dependencies
COPY go.mod go.sum ./
COPY vendor/ ./vendor/

# Set GOFLAGS to use vendored modules for subsequent Go commands
ENV GOFLAGS="-mod=vendor"

# Verify vendored dependencies
# This should be faster if GOFLAGS is set and vendor dir is complete.
RUN go mod verify

# ---- Builder for the main 'docker-cert' application ----
FROM base_builder AS cert_builder
LABEL stage="cert_builder"

# WORKDIR is inherited from base_builder (/src/app)

# Copy all source code.
# For better caching, you could try to copy only ./cmd/docker-cert/ and its specific dependencies
# if they are well-isolated from other parts of the monorepo.
# However, `COPY . .` is simpler if isolation is complex.
COPY . .

# Build the main application (Removed -a flag)
RUN echo "Building docker-cert..." && \
    CGO_ENABLED=0 go build \
        -ldflags '-s -w -extldflags "-static"' \
        -o /app/docker-cert ./cmd/docker-cert/main.go

# ---- Builder for the 'healthcheck' utility ----
FROM base_builder AS healthcheck_builder
LABEL stage="healthcheck_builder"

# WORKDIR is inherited

# Copy all source code. (See note in cert_builder about selective copying)
COPY . .

# Build the healthcheck utility (Removed -a flag)
RUN echo "Building healthcheck..." && \
    CGO_ENABLED=0 go build \
        -ldflags '-s -w -extldflags "-static"' \
        -o /app/healthcheck ./cmd/healthcheck/main.go

# --- Final Stage (Distroless) ---
FROM gcr.io/distroless/base-debian12 AS final

# The nonroot user (UID 1001, GID 1001) is the default in distroless/base.
USER 1001:1001
WORKDIR /app

# Copy the main application binary from the cert_builder stage
COPY --chown=1001:1001 --from=cert_builder /app/docker-cert /app/docker-cert

# Copy the healthcheck utility from the healthcheck_builder stage
COPY --chown=1001:1001 --from=healthcheck_builder /app/healthcheck /app/healthcheck

# Ensure binaries are executable (permissions are generally preserved by COPY,
# but good to ensure they were set in builder stage via go build defaults)

EXPOSE 8080
ENV INTERNAL_HTTP_PORT=8080

HEALTHCHECK --interval=1m30s --timeout=10s --start-period=45s --retries=3 \
  CMD ["/app/healthcheck"]

ENTRYPOINT ["/app/docker-cert"]