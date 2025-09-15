module docker-cert

go 1.24.0

require github.com/go-acme/lego/v4 v4.26.0 // Matching your 'go list -m' output

require (
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect; indirect // Version might update with lego v4.23.1
	github.com/go-jose/go-jose/v4 v4.1.2 // indirect; indirect // New indirect dependency from newer lego
	github.com/miekg/dns v1.1.68 // indirect; indirect // Version might update
	golang.org/x/crypto v0.42.0 // indirect; indirect // Version might update
	golang.org/x/mod v0.27.0 // indirect
	golang.org/x/net v0.44.0 // indirect; indirect // Version might update
	golang.org/x/sys v0.36.0 // indirect; indirect // Version might update
	golang.org/x/text v0.29.0 // indirect; indirect // Version might update
	golang.org/x/tools v0.36.0 // indirect
)

require golang.org/x/sync v0.17.0 // indirect
