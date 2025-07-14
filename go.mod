module docker-cert

go 1.24

require github.com/go-acme/lego/v4 v4.24.0 // Matching your 'go list -m' output

require (
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect; indirect // Version might update with lego v4.23.1
	github.com/go-jose/go-jose/v4 v4.1.0 // indirect; indirect // New indirect dependency from newer lego
	github.com/miekg/dns v1.1.66 // indirect; indirect // Version might update
	golang.org/x/crypto v0.38.0 // indirect; indirect // Version might update
	golang.org/x/mod v0.24.0 // indirect
	golang.org/x/net v0.40.0 // indirect; indirect // Version might update
	golang.org/x/sys v0.33.0 // indirect; indirect // Version might update
	golang.org/x/text v0.25.0 // indirect; indirect // Version might update
	golang.org/x/tools v0.32.0 // indirect
)

require golang.org/x/sync v0.14.0 // indirect
