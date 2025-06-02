# Docker-Cert: ACME Certificate Manager

Docker-Cert is a Go application designed to automatically obtain and renew SSL/TLS certificates from ACME Certificate Authorities (CAs) like Let's Encrypt and ZeroSSL. It uses the DNS-01 challenge method with DuckDNS, eliminating the need to expose any ports on your server for certificate validation. Certificates are stored in a structured, configurable path, making them easily accessible for other services.

This project is containerized using Docker with a minimal distroless image for enhanced security.

## Features

-   **ACME Client:** Supports Let's Encrypt and ZeroSSL (with EAB credentials).
-   **DNS-01 Challenge:** Uses DuckDNS for domain validation. No open ports required for challenges.
-   **Automatic Renewal:** Periodically checks and renews certificates before they expire.
-   **CA Redundancy:** Configurable order to try multiple CAs if one fails.
-   **Structured Storage:** Saves certificates in a "folder-wise" manner (e.g., `/data/config/your.domain.com/live/...`).
-   **Configurable Paths & Permissions:** Certificate storage path, UID, and GID for certificate files are configurable via environment variables.
-   **Dockerized:** Runs in a lightweight, secure distroless Docker container.
-   **Healthcheck Endpoint:** Includes an HTTP endpoint for Docker health checks.
-   **Modular Go Project Structure.**

## Project directory

```
docker-cert/
├── .github/
│   ├── dependabot.yml
│   └── workflows/
│       └── ci-cd.yml
├── cmd/
│   └── docker-cert/
│       └── main.go
├── internal/
│   ├── acme/
│   │   ├── manager.go
│   │   └── user.go
│   ├── config/
│   │   └── config.go
│   ├── dns/
│   │   └── duckdns_provider.go
│   ├── httpapi/
│   │   └── server.go
│   ├── renewal/
│   │   └── scheduler.go
│   └── storage/
│       └── certificate_storage.go
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
└── README.md
```

## Prerequisites

-   **Docker and Docker Compose:** To build and run the application.
-   **DuckDNS Account:** You need a DuckDNS token and a DuckDNS domain (e.g., `your-subdomain.duckdns.org`).
-   **DNS CNAME Records:** For each domain you want a certificate for (e.g., `my.service.com`), you must set up a CNAME record in your primary DNS provider pointing to your DuckDNS domain for the ACME challenge:
    -   Record Type: `CNAME`
    -   Name/Host: `_acme-challenge.my.service.com`
    -   Value/Target: `_acme-challenge.your-acme-challenge-subdomain.duckdns.org`
        (Here, `your-acme-challenge-subdomain.duckdns.org` is the value you'll set for `DUCKDNS_DOMAIN_FOR_CHALLENGE`).
-   **(Optional) ZeroSSL EAB Credentials:** If you plan to use ZeroSSL, you'll need External Account Binding (EAB) KID and HMAC Key from your ZeroSSL dashboard for new account registrations.

## Go Module Path

This project uses `docker-cert` as its Go module path in the `go.mod` file. All internal import paths reflect this (e.g., `import "docker-cert/internal/config"`). If you clone this into a structure that requires a different module path (e.g., under a VCS host like `github.com/your-username/docker-cert`), you will need to update the `go.mod` file and all internal import paths accordingly.

## Configuration

Configuration is managed via environment variables. Create a `.env` file in the project root directory (where `docker-compose.yml` is located) with the following variables:

# --- Core ACME Config ---
LETSENCRYPT_EMAIL=your-email@example.com
# Comma-separated list of domains for the cert (e.g., my.service.com,[www.my.service.com](https://www.my.service.com))
LETSENCRYPT_DOMAIN=my.service.com
LETSENCRYPT_WILDCARD=false # If true, first domain in LETSENCRYPT_DOMAIN is base for *.
TESTING=false # true for staging CA endpoints, false for production
RENEWAL_CHECK_INTERVAL_HOURS=12
# Order of CAs to try (comma-separated: letsencrypt,zerossl)
CA_ORDER=letsencrypt,zerossl
# Optional: Preferred certificate chain (e.g., "ISRG Root X1")
# LETSENCRYPT_PREFERRED_CHAIN=

# --- DuckDNS Provider Config ---
DUCKDNS_TOKEN=YOUR_DUCKDNS_TOKEN_HERE
# The DuckDNS FQDN used for ACME challenges (e.g., your-acme-challenge-subdomain.duckdns.org)
DUCKDNS_DOMAIN_FOR_CHALLENGE=your-acme-challenge-subdomain.duckdns.org

# --- ZeroSSL Config (Only if ZEROSSL_ENABLED=true in CA_ORDER and you want to use it) ---
ZEROSSL_ENABLED=true # Set to true if ZeroSSL is in CA_ORDER and you want to use it
ZEROSSL_EAB_KID=YOUR_ZEROSSL_EAB_KID # Required for new ZeroSSL accounts
ZEROSSL_EAB_HMAC_KEY=YOUR_ZEROSSL_EAB_HMAC_KEY # Required for new ZeroSSL accounts

# --- Certificate Storage Path & Permissions ---
# Base path inside the container where certificates will be stored.
CERTS_BASE_PATH=/data/config
UID=0 # User ID for cert files (0 for root)
GID=0 # Group ID for cert files (0 for root)

# --- HTTP API (for Healthcheck) ---
# Internal port the Go application's HTTP server listens on for healthchecks.
# This port is NOT exposed externally by default in docker-compose.
INTERNAL_HTTP_PORT=8080
# DOCKER_CERT_API_HOST_PORT=8081 # If you wanted to expose the API externally, uncomment and map in docker-compose.yml

