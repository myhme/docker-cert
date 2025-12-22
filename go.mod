module docker-cert

go 1.24.0

require github.com/go-acme/lego/v4 v4.30.1 // Matching your 'go list -m' output

require (
	github.com/go-jose/go-jose/v4 v4.1.3 // indirect; indirect // New indirect dependency from newer lego
	github.com/miekg/dns v1.1.69 // indirect; indirect // Version might update
	golang.org/x/crypto v0.46.0 // indirect; indirect // Version might update
	golang.org/x/mod v0.30.0 // indirect
	golang.org/x/net v0.48.0 // indirect; indirect // Version might update
	golang.org/x/sys v0.39.0 // indirect; indirect // Version might update
	golang.org/x/text v0.32.0 // indirect; indirect // Version might update
	golang.org/x/tools v0.39.0 // indirect
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	golang.org/x/sync v0.19.0 // indirect
)
