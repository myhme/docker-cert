package ipupdater

import (
	"fmt"
	"io"
	"net/http"
	"strconv" // Added import for strconv
	"strings"
	"sync"
	"time"

	"docker-cert/internal/config"
)

type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

const (
	duckDNSUpdateURL = "https://www.duckdns.org/update"
	ipifyAPIIPv4     = "https://api.ipify.org?format=text"
	ipifyAPIIPv6     = "https://api64.ipify.org?format=text"
)

// Service handles IP updates.
type Service struct {
	config               *config.Config
	logger               Logger
	httpClient           *http.Client
	mu                   sync.Mutex // Protects access to lastIPs, lastError, lastSuccess, etc.
	lastIPv4             string
	lastIPv6             string
	lastError            error
	lastSuccess          time.Time
	lastCheckAttempt     time.Time
	isInitialized        bool
	initialUpdatePerformed bool
}

// ComponentStatus represents the health status of a component.
type ComponentStatus struct {
	Status             string `json:"status"` // e.g., "ok", "error", "initializing", "disabled"
	Message            string `json:"message"`
	LastIPv4Detected   string `json:"last_ipv4_detected,omitempty"`
	LastIPv6Detected   string `json:"last_ipv6_detected,omitempty"`
	LastSuccess        string `json:"last_success,omitempty"`
	LastCheckAttempt   string `json:"last_check_attempt,omitempty"`
	LastUpdateError    string `json:"last_update_error,omitempty"`
}


func NewService(cfg *config.Config, logger Logger) *Service {
	if logger == nil {
		panic("logger cannot be nil for IPUpdater Service")
	}
	return &Service{
		config:        cfg,
		logger:        logger,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		isInitialized: true, // Considered initialized upon creation
	}
}

// MaskString replaces parts of a sensitive string with asterisks for logging.
// It shows 'visibleChars' at the beginning and end if the string is long enough.
func MaskString(sensitiveString string, visibleChars int) string {
	if sensitiveString == "" {
		return ""
	}
	length := len(sensitiveString)
	if visibleChars < 0 {
		visibleChars = 0
	}
	if length <= visibleChars*2 {
		if length > 2 && visibleChars == 1 { // Show first and last if at least 3 chars long for 1 visible char
			return sensitiveString[:1] + strings.Repeat("*", length-2) + sensitiveString[length-1:]
		}
		return strings.Repeat("*", length)
	}
	return sensitiveString[:visibleChars] + strings.Repeat("*", length-(visibleChars*2)) + sensitiveString[length-visibleChars:]
}

// MaskIP attempts to mask an IP address.
// For IPv4, it shows the first and last octet.
// For IPv6, it shows a generic placeholder.
func MaskIP(ipAddress string) string {
	if ipAddress == "" {
		return ""
	}
	parts := strings.Split(ipAddress, ".")
	if len(parts) == 4 {
		validIPv4 := true
		for _, part := range parts {
			val, err := strconv.Atoi(part) // strconv.Atoi is used here
			if err != nil || val < 0 || val > 255 {
				validIPv4 = false
				break
			}
		}
		if validIPv4 {
			return fmt.Sprintf("%s.X.X.%s", parts[0], parts[3])
		}
	}
	if strings.Contains(ipAddress, ":") {
		return "XXXX:XXXX:...:XXXX"
	}
	return ipAddress
}

// SanitizeDuckDNSResponseBody attempts to mask an IP address found in the DuckDNS response body.
// Expected format: OK\nIP_ADDRESS\nSTATUS or KO
func SanitizeDuckDNSResponseBody(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) > 0 && strings.ToUpper(lines[0]) == "OK" {
		if len(lines) > 1 {
			lines[1] = MaskIP(lines[1]) // Mask potential IP on the second line
		}
		return strings.Join(lines, "\n")
	} else if strings.ToUpper(body) == "KO" {
		return "KO"
	}
	return "Response (format not fully parsed for masking)"
}

func (s *Service) GetCurrentPublicIPs() (ipv4, ipv6 string, err error) {
	s.logger.Println("IPUpdater: Fetching public IP addresses...")
	var wg sync.WaitGroup
	var errIPv4 error

	wg.Add(1)
	go func() {
		defer wg.Done()
		var fetchedIPv4 string
		fetchedIPv4, errIPv4 = s.fetchPublicIP(ipifyAPIIPv4, "IPv4")
		if errIPv4 != nil {
			s.logger.Printf("IPUpdater: Error fetching IPv4: %v", errIPv4)
		} else if fetchedIPv4 != "" {
			s.logger.Printf("IPUpdater: Fetched public IPv4: %s", MaskIP(fetchedIPv4)) // Use exported MaskIP
			ipv4 = fetchedIPv4
		}
	}()
	wg.Wait()

	if errIPv4 != nil {
		err = fmt.Errorf("failed to fetch IPv4 address: %w", errIPv4)
	}
	return ipv4, ipv6, err
}

func (s *Service) fetchPublicIP(apiURL, ipType string) (string, error) {
	resp, err := s.httpClient.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("failed to connect to %s service %s: %w", ipType, apiURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s service %s returned non-OK status: %d", ipType, apiURL, resp.StatusCode)
	}

	ipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read %s response body from %s: %w", ipType, apiURL, err)
	}
	return strings.TrimSpace(string(ipBytes)), nil
}

func (s *Service) UpdateDuckDNSIP(domain, token, ipv4, ipv6 string) error {
	s.mu.Lock()
	s.lastError = nil
	s.mu.Unlock()

	if domain == "" { return fmt.Errorf("DuckDNS domain for IP update cannot be empty") }
	if token == "" { return fmt.Errorf("DuckDNS token for IP update cannot be empty") }

	duckDNSSubdomain := strings.TrimSuffix(domain, ".duckdns.org")
	if duckDNSSubdomain == "" { return fmt.Errorf("invalid DuckDNS domain format for IP update: %s", domain) }

	s.logger.Printf("IPUpdater: Attempting to update DuckDNS IP for %s (subdomain: %s)", domain, duckDNSSubdomain)

	req, err := http.NewRequest("GET", duckDNSUpdateURL, nil)
	if err != nil { return fmt.Errorf("IPUpdater: failed to create DuckDNS update request: %w", err) }

	q := req.URL.Query()
	q.Add("domains", duckDNSSubdomain)
	q.Add("token", token)
	if ipv4 != "" { q.Add("ip", ipv4) }
	if ipv6 != "" { q.Add("ipv6", ipv6) }
	q.Add("verbose", "true")
	req.URL.RawQuery = q.Encode()

	logURL := *req.URL
	logQ := logURL.Query()
	if logQ.Has("token") { logQ.Set("token", MaskString(logQ.Get("token"), 4)) } // Use exported MaskString
	if logQ.Has("ip") { logQ.Set("ip", MaskIP(logQ.Get("ip"))) }                 // Use exported MaskIP
	if logQ.Has("ipv6") { logQ.Set("ipv6", MaskIP(logQ.Get("ipv6"))) }           // Use exported MaskIP
	logURL.RawQuery = logQ.Encode()
	s.logger.Printf("IPUpdater: Sending DuckDNS IP update request to URL: %s", logURL.String())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.mu.Lock()
		s.lastError = err
		s.mu.Unlock()
		return fmt.Errorf("IPUpdater: DuckDNS IP update API request for %s failed: %w", domain, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		s.mu.Lock()
		s.lastError = err
		s.mu.Unlock()
		return fmt.Errorf("IPUpdater: failed to read DuckDNS IP update response body: %w", err)
	}
	rawResponseString := string(bodyBytes)
	sanitizedResponseForLog := SanitizeDuckDNSResponseBody(rawResponseString) // Use exported SanitizeDuckDNSResponseBody
	s.logger.Printf("IPUpdater: Raw response from DuckDNS IP update: Status: %d, Body: %s", resp.StatusCode, sanitizedResponseForLog)

	responseLines := strings.Split(strings.TrimSpace(rawResponseString), "\n")
	isSuccess := len(responseLines) > 0 && strings.ToUpper(responseLines[0]) == "OK"

	if resp.StatusCode != http.StatusOK || !isSuccess {
		err = fmt.Errorf("DuckDNS IP update API request failed. Status: %d, Body: %s", resp.StatusCode, sanitizedResponseForLog)
		s.mu.Lock()
		s.lastError = err
		s.mu.Unlock()
		return err
	}

	s.logger.Printf("IPUpdater: DuckDNS IP update for %s successful. Response details: %s", domain, sanitizedResponseForLog)
	s.mu.Lock()
	s.lastSuccess = time.Now()
	s.mu.Unlock()
	return nil
}

func (s *Service) CheckAndPerformIPUpdate() {
	s.mu.Lock()
	s.lastCheckAttempt = time.Now()
	s.mu.Unlock()

	if s.config.DuckDNSIPUpdateDomain == "" {
		return
	}

	s.logger.Println("IPUpdater: Checking public IP address...")
	currentIPv4, currentIPv6, err := s.GetCurrentPublicIPs()
	if err != nil {
		s.logger.Printf("IPUpdater: Error getting current public IP: %v. Skipping DuckDNS update.", err)
		s.mu.Lock()
		s.lastError = err
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if currentIPv4 == "" {
		s.logger.Println("IPUpdater: Could not determine current public IPv4. Skipping DuckDNS update.")
		s.lastError = fmt.Errorf("could not determine current public IPv4")
		return
	}

	needsUpdate := false
	if !s.initialUpdatePerformed {
		s.logger.Println("IPUpdater: Performing initial IP update check (will update regardless of change).")
		needsUpdate = true
		s.initialUpdatePerformed = true
	}


	if !needsUpdate && (currentIPv4 != s.lastIPv4) {
		s.logger.Printf("IPUpdater: Public IPv4 address changed from %s to %s. Updating DuckDNS.", MaskIP(s.lastIPv4), MaskIP(currentIPv4)) // Use exported MaskIP
		needsUpdate = true
	} else if !needsUpdate && currentIPv6 != "" && currentIPv6 != s.lastIPv6 {
		s.logger.Printf("IPUpdater: Public IPv6 address changed from %s to %s. Updating DuckDNS.", MaskIP(s.lastIPv6), MaskIP(currentIPv6)) // Use exported MaskIP
		needsUpdate = true
	} else if !needsUpdate {
		s.logger.Printf("IPUpdater: Public IPv4 address (%s) has not changed. No DuckDNS update needed.", MaskIP(currentIPv4)) // Use exported MaskIP
		s.lastError = nil
		return
	}

	if needsUpdate {
		updateErr := s.UpdateDuckDNSIP(s.config.DuckDNSIPUpdateDomain, s.config.DuckDNSIPUpdateToken, currentIPv4, currentIPv6)
		if updateErr != nil {
			s.logger.Printf("IPUpdater: Error updating DuckDNS IP: %v", updateErr)
			s.lastError = updateErr
		} else {
			s.logger.Printf("IPUpdater: Successfully updated DuckDNS with IPv4: %s (and IPv6: %s if applicable).", MaskIP(currentIPv4), MaskIP(currentIPv6)) // Use exported MaskIP
			s.lastIPv4 = currentIPv4
			s.lastIPv6 = currentIPv6
			s.lastSuccess = time.Now()
			s.lastError = nil
		}
	}
}

func (s *Service) StartScheduler() {
	if s.config.DuckDNSIPUpdateDomain == "" || s.config.DuckDNSIPUpdateInterval <= 0 {
		return
	}
	s.logger.Printf("IPUpdater: Starting IP update scheduler for %s. Interval: %v", s.config.DuckDNSIPUpdateDomain, s.config.DuckDNSIPUpdateInterval)
	go s.CheckAndPerformIPUpdate()

	ticker := time.NewTicker(s.config.DuckDNSIPUpdateInterval)
	for range ticker.C {
		s.CheckAndPerformIPUpdate()
	}
}

func (s *Service) GetStatus() ComponentStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isInitialized {
		return ComponentStatus{Status: "error", Message: "IP Updater not initialized"}
	}
	if s.config.DuckDNSIPUpdateDomain == "" {
		return ComponentStatus{Status: "disabled", Message: "IP Updater disabled: No domain configured."}
	}

	status := ComponentStatus{
		LastIPv4Detected: MaskIP(s.lastIPv4), // Use exported MaskIP
		LastIPv6Detected: MaskIP(s.lastIPv6), // Use exported MaskIP
	}
	if !s.lastCheckAttempt.IsZero() {
		status.LastCheckAttempt = s.lastCheckAttempt.Format(time.RFC3339)
	}

	if s.lastError != nil {
		status.Status = "error"
		status.Message = fmt.Sprintf("Last operation failed: %v", s.lastError)
		status.LastUpdateError = s.lastError.Error()
	} else if !s.lastSuccess.IsZero() {
		status.Status = "ok"
		status.Message = "IP Updater operational."
		status.LastSuccess = s.lastSuccess.Format(time.RFC3339)
	} else if !s.initialUpdatePerformed {
		status.Status = "initializing"
		status.Message = "IP Updater has not performed its initial update check yet."
	} else {
		status.Status = "ok"
		status.Message = "IP Updater is running; awaiting first successful update or subsequent check."
	}
	return status
}
