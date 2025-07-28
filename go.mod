module docker-cert

go 1.24.0

require github.com/go-acme/lego/v4 v4.25.1 // Matching your 'go list -m' output

require (
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect; indirect // Version might update with lego v4.23.1
	github.com/go-jose/go-jose/v4 v4.1.1 // indirect; indirect // New indirect dependency from newer lego
	github.com/miekg/dns v1.1.67 // indirect; indirect // Version might update
	golang.org/x/crypto v0.40.0 // indirect; indirect // Version might update
	golang.org/x/mod v0.25.0 // indirect
	golang.org/x/net v0.42.0 // indirect; indirect // Version might update
	golang.org/x/sys v0.34.0 // indirect; indirect // Version might update
	golang.org/x/text v0.27.0 // indirect; indirect // Version might update
	golang.org/x/tools v0.34.0 // indirect
)

require golang.org/x/sync v0.16.0 // indirect
