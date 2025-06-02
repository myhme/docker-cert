package dns

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const duckDNSUpdateURLDefault = "https://www.duckdns.org/update"

// DuckDNSProvider implements the challenge.Provider interface for DuckDNS.
type DuckDNSProvider struct {
	token      string
	baseDomain string
	client     *http.Client
	logger     Logger
}

// Logger interface for dependency injection.
type Logger interface {
	Printf(format string, v ...interface{})
}

// NewDuckDNSProvider returns a new DuckDNSProvider instance.
func NewDuckDNSProvider(token, baseDomain string, logger Logger) (*DuckDNSProvider, error) {
	if token == "" {
		return nil, fmt.Errorf("DuckDNS token cannot be empty")
	}
	if baseDomain == "" {
		return nil, fmt.Errorf("DuckDNS base domain for challenges cannot be empty")
	}
	if !strings.HasSuffix(baseDomain, ".duckdns.org") {
		return nil, fmt.Errorf("DuckDNS base domain '%s' must end with .duckdns.org", baseDomain)
	}
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil for DuckDNSProvider")
	}

	return &DuckDNSProvider{
		token:      token,
		baseDomain: baseDomain,
		client:     &http.Client{Timeout: 60 * time.Second},
		logger:     logger,
	}, nil
}

// Present creates a TXT record to fulfill the DNS-01 challenge.
func (d *DuckDNSProvider) Present(domain, token, keyAuth string) error {
	duckDNSSubdomain := strings.TrimSuffix(d.baseDomain, ".duckdns.org")
	txtValue := keyAuth

	logValue := txtValue
	if len(logValue) > 30 {
		logValue = logValue[:30] + "..."
	}
	d.logger.Printf("DuckDNS: Presenting TXT record for challenge domain %s (using DuckDNS subdomain: %s) with value (truncated for log): %s", domain, duckDNSSubdomain, logValue)

	req, err := http.NewRequest("GET", duckDNSUpdateURLDefault, nil)
	if err != nil {
		return fmt.Errorf("DuckDNS: failed to create request: %w", err)
	}

	q := req.URL.Query()
	q.Add("domains", duckDNSSubdomain)
	q.Add("token", d.token)
	q.Add("txt", txtValue)
	req.URL.RawQuery = q.Encode()

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("DuckDNS: API request to present TXT record for %s failed: %w", duckDNSSubdomain, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("DuckDNS: failed to read response body: %w", err)
	}

	responseString := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(responseString, "OK") {
		return fmt.Errorf("DuckDNS: API request to present TXT record failed. Status: %d, Body: %s", resp.StatusCode, responseString)
	}

	d.logger.Printf("DuckDNS: Successfully presented TXT record for %s (subdomain: %s)", domain, duckDNSSubdomain)
	return nil
}

// CleanUp removes the TXT record after the challenge.
func (d *DuckDNSProvider) CleanUp(domain, token, keyAuth string) error {
	duckDNSSubdomain := strings.TrimSuffix(d.baseDomain, ".duckdns.org")
	txtValue := keyAuth

	logValue := txtValue
	if len(logValue) > 30 {
		logValue = logValue[:30] + "..."
	}
	d.logger.Printf("DuckDNS: Cleaning up TXT record for challenge domain %s (using DuckDNS subdomain: %s), original value (truncated): %s", domain, duckDNSSubdomain, logValue)

	req, err := http.NewRequest("GET", duckDNSUpdateURLDefault, nil)
	if err != nil {
		return fmt.Errorf("DuckDNS: failed to create cleanup request: %w", err)
	}

	q := req.URL.Query()
	q.Add("domains", duckDNSSubdomain)
	q.Add("token", d.token)
	q.Add("txt", txtValue)
	q.Add("clear", "true")
	req.URL.RawQuery = q.Encode()

	resp, err := d.client.Do(req)
	if err != nil {
		d.logger.Printf("WARNING: DuckDNS: API request to cleanup TXT record for %s potentially failed (request error): %v. This might leave a TXT record behind.", duckDNSSubdomain, err)
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		d.logger.Printf("WARNING: DuckDNS: Failed to read cleanup response body for %s: %v. This might leave a TXT record behind.", duckDNSSubdomain, err)
		return nil
	}
	
	responseString := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(responseString, "OK") {
		d.logger.Printf("WARNING: DuckDNS: API request to cleanup TXT record potentially failed. Status: %d, Body: %s. This might leave a TXT record behind.", resp.StatusCode, responseString)
	} else {
		d.logger.Printf("DuckDNS: Successfully initiated cleanup of TXT record for %s (subdomain: %s)", domain, duckDNSSubdomain)
	}
	return nil
}

// Timeout returns the timeout and interval values for the DNS challenge.
func (d *DuckDNSProvider) Timeout() (timeout, interval time.Duration) {
	return 5 * time.Minute, 30 * time.Second
}
