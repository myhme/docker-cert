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
# Using distroless/base which has a default non-root user (UID 65532)
FROM gcr.io/distroless/base-debian12 AS final

WORKDIR /app

# Copy the binaries from the builder stages.
# By default, COPY preserves permissions. Go builds binaries with execute permissions for all.
# We will set the user to 'nonroot' which is provided by the base image.
# This user is UID 65532, GID 65532.
COPY --from=cert_builder /app/docker-cert /app/docker-cert
COPY --from=healthcheck_builder /app/healthcheck /app/healthcheck

# Set the user to the default non-root user provided by the distroless image.
# This is a more secure default than running as root.
# You can override this at runtime with `docker run --user <UID>:<GID> ...`
USER nonroot

EXPOSE 8080
ENV INTERNAL_HTTP_PORT=8080

HEALTHCHECK --interval=1m30s --timeout=10s --start-period=45s --retries=3 \
  CMD ["/app/healthcheck"]

ENTRYPOINT ["/app/docker-cert"]
