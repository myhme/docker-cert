# Docker-Cert: Let's Encrypt Certificate Manager & DuckDNS IP Updater

Docker-Cert is a Go application designed to:
1.  Automatically obtain and renew SSL/TLS certificates from Let's Encrypt using the DNS-01 challenge with DuckDNS.
2.  Automatically update your DuckDNS domain's IP address.

It eliminates the need to expose any ports on your server for certificate validation. Certificates and account keys are stored in a structured, configurable path. The application also provides an HTTP API for manual control and status checks.

This project is containerized using Docker with a minimal distroless image for enhanced security and now uses the official `lego` library's DuckDNS provider.

## Features

-   **Let's Encrypt Client:** Obtains and renews certificates from Let's Encrypt.
-   **DuckDNS IP Updater:** Periodically checks and updates your DuckDNS domain's A/AAAA records.
-   **DNS-01 Challenge:** Uses DuckDNS via `lego`'s official provider for certificate validation. No open ports required.
-   **Automatic Renewal:** Periodically checks and renews certificates before they expire.
-   **Structured & Secure Storage:** Saves certificates and account keys with secure file permissions.
-   **Configurable Paths & Permissions:** Storage path, UID, and GID are configurable.
-   **HTTP API:** Endpoints for health, manual renewal, IP checks, and IP updates (optionally token-protected).
-   **Dockerized:** Runs in a lightweight, secure distroless Docker container.
-   **Modular Go Project Structure.**

## Project Directory Structure (Source Code)

```text
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
│   ├── httpapi/
│   │   └── server.go
│   ├── ipupdater/
│   │   └── duckdns_ip_updater.go
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

* **Docker and Docker Compose:** To build and run the application.
* **DuckDNS Account:** You need a DuckDNS token.
* **DNS CNAME Records (if not using DuckDNS domain directly):** If `LETSENCRYPT_DOMAIN` is a custom domain (e.g., `my.customdomain.com`), you must have a CNAME record pointing its ACME challenge to a DuckDNS domain you control:
    `_acme-challenge.my.customdomain.com` CNAME to `_acme-challenge.your-duckdns-subdomain.duckdns.org`.
    Your `DUCKDNS_TOKEN` must be able to update `your-duckdns-subdomain.duckdns.org`.
    If `LETSENCRYPT_DOMAIN` is itself a DuckDNS domain (e.g., `myname.duckdns.org`), no CNAME is needed; `lego` will update it directly.

## Go Module Path

This project uses `docker-cert` as its Go module path in the `go.mod` file. All internal import paths reflect this (e.g., `import "docker-cert/internal/config"`).

## Configuration

Configuration is managed via environment variables. Create a `.env` file in the project root directory.

```env
# --- Let's Encrypt ACME Config ---
# Your email address for ACME registration and notifications.
# Optional: If left empty, registration will be attempted without an email.
# However, providing an email is STRONGLY RECOMMENDED to receive important CA notifications.
# IMPORTANT: Use a real email from a valid domain (e.g., not @example.com).
LETSENCRYPT_EMAIL=your-valid-email@yourdomain.com

# Comma-separated list of domains for the cert (e.g., my.service.com,[www.my.service.com](https://www.my.service.com)).
# This is also used as the default for DUCKDNS_IP_UPDATE_DOMAIN if not explicitly set.
LETSENCRYPT_DOMAIN=my.service.com
LETSENCRYPT_WILDCARD=false # If true, first domain in LETSENCRYPT_DOMAIN is base for *.
TESTING=false # Use Let's Encrypt staging if true
RENEWAL_CHECK_INTERVAL_HOURS=12h # Duration string, e.g., 12h, 30m
LETSENCRYPT_CHAIN="default" # Or e.g., "ISRG Root X1"

# --- DuckDNS Config ---
# Token used for ACME DNS-01 challenges AND by default for IP updates (if DUCKDNS_IP_UPDATE_TOKEN is not set).
DUCKDNS_TOKEN=YOUR_DUCKDNS_TOKEN_HERE

# --- DuckDNS IP Updater Config (Optional) ---
# The DuckDNS domain whose A/AAAA record should be periodically updated.
# If empty, this defaults to the first domain listed in LETSENCRYPT_DOMAIN (wildcard prefix removed).
# Set explicitly if you want to update a different DuckDNS domain for IP than your LETSENCRYPT_DOMAIN.
# DUCKDNS_IP_UPDATE_DOMAIN=another.duckdns.org

# Interval for checking and updating the IP address. Examples: "300s" (5 minutes), "10m", "1h".
DUCKDNS_IP_UPDATE_INTERVAL_SECONDS=300s
# Optional: Specific token for IP updates. If empty or not set, DUCKDNS_TOKEN is used.
# DUCKDNS_IP_UPDATE_TOKEN=YOUR_SEPARATE_IP_UPDATE_TOKEN_IF_ANY

# --- Certificate Storage Path & Permissions ---
# Base path inside the container where certificates and account keys will be stored.
CERTS_BASE_PATH=/data/config
# User ID for the owner of the certificate files and directories created by this application. '0' for root.
UID=0
# Group ID for the owner of the certificate files and directories. '0' for root.
GID=0

# --- HTTP API ---
# Internal port the Go application's HTTP server listens on for healthchecks and API.
INTERNAL_HTTP_PORT=8080
# Optional: Set a bearer token to secure POST/action-triggering API endpoints. If empty, these API endpoints are unprotected.
# API_AUTH_TOKEN=a_very_secure_random_token_here

```

## Runtime Directory & File Structure (Inside Container)
-----------------------------------------------------

The application creates and manages its files within the directory specified by CERTS\_BASE\_PATH (e.g., /data/config by default). This entire path is typically mounted as a Docker volume for persistence.

**Key Structure & Permissions:**

1.  **ACME Account Information:**
    
    *   **Path:** /data/config/accounts/default/account.key (The default subdirectory is part of AccountKeyDir in config.go)
        
    *   Contains the private key for your Let's Encrypt account.
        
    *   Directory accounts/default/ permissions: 0700 (owner rwx, no group/other access).
        
    *   File account.key permissions: 0600 (owner rw, no group/other access).
        
2.  **Domain Certificates (for each domain, e.g., my.service.com):**Let domain\_folder be my.service.com (or \_wildcard\_.my.service.com for wildcards).
    
    *   **Archive Directory:** /data/config/\[domain\_folder\]/archive/
        
        *   Contains timestamped versions of your certificate files:
            
            *   cert-\[timestamp\].pem (permissions: 0644)
                
            *   chain-\[timestamp\].pem (permissions: 0644)
                
            *   fullchain-\[timestamp\].pem (permissions: 0644)
                
            *   privkey-\[timestamp\].pem (permissions: 0600 - private key)
                
        *   Directory permissions: 0755.
            
    *   **Live Directory:** /data/config/\[domain\_folder\]/live/
        
        *   Contains symlinks to the current active certificate files in the archive directory:
            
            *   cert.pem -> ../../archive/\[domain\_folder\]/cert-\[latest\_timestamp\].pem
                
            *   chain.pem -> ../../archive/\[domain\_folder\]/chain-\[latest\_timestamp\].pem
                
            *   fullchain.pem -> ../../archive/\[domain\_folder\]/fullchain-\[latest\_timestamp\].pem
                
            *   privkey.pem -> ../../archive/\[domain\_folder\]/privkey-\[latest\_timestamp\].pem
                
        *   Directory permissions: 0755.
            
        *   Web servers and other services should be configured to use the files in this live directory.
            

**Ownership:**

*   All files and directories created by docker-cert under CERTS\_BASE\_PATH will be owned by the UID and GID specified in the environment variables.
    

## HTTP API Endpoints
------------------

The internal HTTP server listens on the port defined by INTERNAL\_HTTP\_PORT.If API\_AUTH\_TOKEN is set, applicable endpoints require an Authorization: Bearer header.

*   **GET /healthz**: Health check endpoint. Always public.
    
*   **POST /api/v1/certificates/renew**: (Protected if token set)Manually triggers a certificate renewal check process.Returns 202 Accepted if initiated.
    
*   **GET /api/v1/ip/current**: (Protected if token set)Returns the current public IPv4 and IPv6 (if available) addresses.Example response: {"ipv4": "1.2.3.4", "ipv6": "2001:db8::1"}
    
*   **POST /api/v1/duckdns/update-ip**: (Protected if token set)Manually triggers a DuckDNS IP update check.Returns 202 Accepted if initiated.
    

## Backup and Restore
------------------

*   **Backup:** The docker-compose.yml file defines a named volume (e.g., certs\_data\_volume) that is mounted to CERTS\_BASE\_PATH. To back up all your ACME account keys and domain certificates, **back up this Docker volume**.
    
*   **Restore:** Restore the backed-up Docker volume data and then start the docker-cert container.
    
*   **Reruns:** The application checks for existing account keys and certificates and will only attempt to obtain or renew if necessary.


## Development
-----------

1.  Ensure Go (version 1.22 or higher) is installed.
    
2.  Clone the repository.
    
3.  Run go mod tidy.
    
4.  Build locally: go build -o docker-cert-local ./cmd/docker-cert
    

## CI/CD & Dependency Management
-----------------------------

*   **GitHub Actions:** Workflow in .github/workflows/ci-cd.yml.
    
*   **Dependabot:** Configuration in .github/dependabot.yml.
