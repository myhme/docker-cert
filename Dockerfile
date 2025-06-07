# (Previous builder stages remain the same...)

# --- Final Stage (Distroless) ---
FROM gcr.io/distroless/base-debian12 AS final

# The default user is 'nonroot' (UID 65532, GID 65532)
# We will ensure the working directory and certs directory are owned by this user.
# The default CertsBasePath in your config is likely '/certs'
ENV CERTS_BASE_PATH=/data/config

WORKDIR /app

# Create the directory for certificates and set its ownership to the nonroot user.
# Do this as root BEFORE switching the user.
RUN mkdir -p ${CERTS_BASE_PATH} && chown nonroot:nonroot ${CERTS_BASE_PATH}

# Now, switch to the non-root user for all subsequent operations and for runtime.
USER nonroot

# Copy the binaries from the builder stages.
# The --chown flag is useful here to ensure the nonroot user owns the binaries.
COPY --chown=nonroot:nonroot --from=cert_builder /app/docker-cert /app/docker-cert
COPY --chown=nonroot:nonroot --from=healthcheck_builder /app/healthcheck /app/healthcheck

EXPOSE 8080
ENV INTERNAL_HTTP_PORT=8080

HEALTHCHECK --interval=1m30s --timeout=10s --start-period=45s --retries=3 \
  CMD ["/app/healthcheck"]

ENTRYPOINT ["/app/docker-cert"]
