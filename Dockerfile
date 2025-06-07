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
RUN go mod verify

# ---- Builder for the main 'docker-cert' application ----
FROM base_builder AS cert_builder
LABEL stage="cert_builder"

# Copy all source code.
COPY . .

# Build the main application, ensuring it's statically linked
RUN echo "Building docker-cert..." && \
    CGO_ENABLED=0 go build \
        -ldflags '-s -w -extldflags "-static"' \
        -o /app/docker-cert ./cmd/docker-cert/main.go

# ---- Builder for the 'healthcheck' utility ----
FROM base_builder AS healthcheck_builder
LABEL stage="healthcheck_builder"

# Copy all source code.
COPY . .

# Build the healthcheck utility, ensuring it's statically linked
RUN echo "Building healthcheck..." && \
    CGO_ENABLED=0 go build \
        -ldflags '-s -w -extldflags "-static"' \
        -o /app/healthcheck ./cmd/healthcheck/main.go

# --- Final Stage (Distroless) ---
FROM gcr.io/distroless/base-debian12 AS final

# Set environment variables that might be used
ENV CERTS_BASE_PATH=/data/config
WORKDIR /app

# --- WORKAROUND for buildx issue ---
# 1. Copy binaries from build stages first. This happens as the default root user.
COPY --from=cert_builder /app/docker-cert /app/docker-cert
COPY --from=healthcheck_builder /app/healthcheck /app/healthcheck

# 2. As root, create the certs dir and change ownership of it AND the app binaries.
#    This avoids using --chown in the COPY command, which can confuse some buildx versions.
RUN mkdir -p ${CERTS_BASE_PATH} && chown nonroot:nonroot ${CERTS_BASE_PATH} /app/docker-cert /app/healthcheck

# 3. Now, switch to the non-root user for runtime.
USER nonroot

EXPOSE 8080
ENV INTERNAL_HTTP_PORT=8080

HEALTHCHECK --interval=1m30s --timeout=10s --start-period=45s --retries=3 \
  CMD ["/app/healthcheck"]

ENTRYPOINT ["/app/docker-cert"]
