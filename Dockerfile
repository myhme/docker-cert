# ---- Build Stage ----
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build tools if necessary (e.g., git for private modules)
# RUN apk --no-cache add git

# Copy Go module files and download dependencies first to leverage Docker cache
COPY go.mod go.sum ./
RUN go mod download
RUN go mod verify

# Copy the rest of the application source code
COPY . .

# Build the Go application as a static binary
# The output binary will be named 'docker-cert' and placed in /app/bin/
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags '-s -w -extldflags "-static"' -o /app/bin/docker-cert ./cmd/docker-cert

# ---- Final Stage ----
# Use Google's distroless base image. It contains only the application and its runtime dependencies.
# It includes ca-certificates, which are necessary for making HTTPS calls.
FROM gcr.io/distroless/base-debian12 AS final
# For an even smaller image, if your app is fully static and doesn't need system CAs or timezone:
# FROM gcr.io/distroless/static-debian12

WORKDIR /app

# Copy the compiled binary from the builder stage
COPY --from=builder /app/bin/docker-cert /app/docker-cert

# Distroless base-debian12 includes ca-certificates and timezone data.
# If using distroless/static and need them, uncomment below:
# COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# The application will run as root by default in distroless, which is fine for this use case
# as it needs to manage file permissions based on UID/GID env vars.
# If you wanted to run as non-root, you'd need to ensure the certs path is writable.
# USER nonroot:nonroot

# ENTRYPOINT defines the command to run when the container starts.
ENTRYPOINT ["/app/docker-cert"]
