// File: internal/ipupdater/duckdns_ip_updater.go
package ipupdater

import (
	"context" // Added for StopScheduler
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	// Assuming your module path, adjust if necessary
	"docker-cert/internal/config"
)

// Logger interface matches the one you defined.
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

const (
	duckDNSUpdateURL = "https://www.duckdns.org/update"
	ipifyAPIIPv4     = "https://api.ipify.org?format=text"
	ipifyAPIIPv6     = "https://api64.ipify.org?format=text" // Note: Your original code only fetches IPv4 in GetCurrentPublicIPs
)

// Service handles IP updates.
type Service struct {
	config               *config.Config // Renamed from AppConfig to Config to match your struct
	logger               Logger
	httpClient           *http.Client
	mu                   sync.Mutex // Protects access to lastIPs, lastError, lastSuccess, etc.
	lastIPv4             string
	lastIPv6             string // Keep even if only IPv4 is actively updated for now
	lastError            error
	lastSuccess          time.Time
	lastCheckAttempt     time.Time
	isInitialized        bool
	initialUpdatePerformed bool

	// Fields for graceful shutdown of the scheduler
	stopChan chan struct{}    // Channel to signal the scheduler's main loop to stop
	wg       sync.WaitGroup // To wait for the scheduler goroutine to finish
}

// ComponentStatus represents the health status of a component. (Copied from your version)
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
		// In a real scenario, you might return an error or use a default logger
		// For now, panicking is consistent with your original code if logger is critical
		panic("logger cannot be nil for IPUpdater Service")
	}
	return &Service{
		config:        cfg,
		logger:        logger,
		httpClient:    &http.Client{Timeout: 30 * time.Second}, // 30s timeout for HTTP requests
		isInitialized: true,                                    // Considered initialized upon creation
		stopChan:      make(chan struct{}),                     // Initialize stopChan
	}
}

// MaskString, MaskIP, SanitizeDuckDNSResponseBody functions from your code (unchanged)
func MaskString(sensitiveString string, visibleChars int) string {
	if sensitiveString == "" {
		return ""
	}
	length := len(sensitiveString)
	if visibleChars < 0 {
		visibleChars = 0
	}
	if length <= visibleChars*2 {
		if length > 2 && visibleChars == 1 {
			return sensitiveString[:1] + strings.Repeat("*", length-2) + sensitiveString[length-1:]
		}
		return strings.Repeat("*", length)
	}
	return sensitiveString[:visibleChars] + strings.Repeat("*", length-(visibleChars*2)) + sensitiveString[length-visibleChars:]
}

func MaskIP(ipAddress string) string {
	if ipAddress == "" {
		return ""
	}
	parts := strings.Split(ipAddress, ".")
	if len(parts) == 4 {
		validIPv4 := true
		for _, part := range parts {
			val, err := strconv.Atoi(part)
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
		return "XXXX:XXXX:...:XXXX" // Generic mask for IPv6
	}
	return ipAddress // Return original if not identifiable as IPv4 or containing ':'
}

func SanitizeDuckDNSResponseBody(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) > 0 && strings.ToUpper(lines[0]) == "OK" {
		if len(lines) > 1 {
			lines[1] = MaskIP(lines[1])
		}
		return strings.Join(lines, "\n")
	} else if strings.ToUpper(body) == "KO" {
		return "KO"
	}
	// Mask common "bad auth" or similar messages if needed, or return a generic message
	return "Response (format not fully parsed for masking or not OK/KO)"
}


// GetCurrentPublicIPs fetches only IPv4 as per your original implementation.
// Extended to potentially fetch IPv6 if ipifyAPIIPv6 were used.
func (s *Service) GetCurrentPublicIPs() (ipv4, ipv6 string, err error) {
	s.logger.Println("IPUpdater: Fetching public IP addresses...")
	var wg sync.WaitGroup // Local WaitGroup for concurrent IP fetches
	var errIPv4, errIPv6 error // Separate errors for v4 and v6

	// Fetch IPv4
	wg.Add(1)
	go func() {
		defer wg.Done()
		var fetchedIPv4 string
		fetchedIPv4, errIPv4 = s.fetchPublicIP(ipifyAPIIPv4, "IPv4")
		if errIPv4 != nil {
			s.logger.Printf("IPUpdater: Error fetching IPv4: %v", errIPv4)
		} else if fetchedIPv4 != "" {
			s.logger.Printf("IPUpdater: Fetched public IPv4: %s", MaskIP(fetchedIPv4))
			ipv4 = fetchedIPv4
		}
	}()

	// Example: Fetch IPv6 (currently not used by your UpdateDuckDNSIP logic but good for completeness)
	// wg.Add(1)
	// go func() {
	// 	defer wg.Done()
	// 	var fetchedIPv6 string
	// 	fetchedIPv6, errIPv6 = s.fetchPublicIP(ipifyAPIIPv6, "IPv6")
	// 	if errIPv6 != nil {
	// 		s.logger.Printf("IPUpdater: Error fetching IPv6: %v", errIPv6)
	// 	} else if fetchedIPv6 != "" {
	// 		s.logger.Printf("IPUpdater: Fetched public IPv6: %s", MaskIP(fetchedIPv6))
	// 		ipv6 = fetchedIPv6
	// 	}
	// }()

	wg.Wait() // Wait for all fetches to complete

	// Combine errors if any
	if errIPv4 != nil && errIPv6 != nil {
		err = fmt.Errorf("failed to fetch IPv4 (%w) and IPv6 (%w)", errIPv4, errIPv6)
	} else if errIPv4 != nil {
		err = fmt.Errorf("failed to fetch IPv4 address: %w", errIPv4)
	} else if errIPv6 != nil {
		err = fmt.Errorf("failed to fetch IPv6 address: %w", errIPv6)
	}

	return ipv4, ipv6, err
}

func (s *Service) fetchPublicIP(apiURL, ipType string) (string, error) {
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for %s service %s: %w", ipType, apiURL, err)
	}
	// It's good practice to set a User-Agent
	req.Header.Set("User-Agent", fmt.Sprintf("Docker-Cert-IPUpdater/1.0 (%s)", ipType))


	resp, err := s.httpClient.Get(apiURL) // Uses s.httpClient with its configured timeout
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
	s.lastError = nil // Clear previous error before attempting an update
	s.mu.Unlock()

	if domain == "" {
		return fmt.Errorf("DuckDNS domain for IP update cannot be empty")
	}
	if token == "" {
		return fmt.Errorf("DuckDNS token for IP update cannot be empty")
	}

	// DuckDNS expects only the subdomain part for the 'domains' parameter
	duckDNSSubdomain := strings.TrimSuffix(domain, ".duckdns.org")
	if duckDNSSubdomain == domain || duckDNSSubdomain == "" { // if not a .duckdns.org domain or empty after trim
		return fmt.Errorf("invalid DuckDNS domain format for IP update: %s (must be like 'yoursubdomain.duckdns.org')", domain)
	}

	s.logger.Printf("IPUpdater: Attempting to update DuckDNS IP for %s (subdomain: %s)", domain, duckDNSSubdomain)

	req, err := http.NewRequest("GET", duckDNSUpdateURL, nil)
	if err != nil {
		return fmt.Errorf("IPUpdater: failed to create DuckDNS update request: %w", err)
	}
	req.Header.Set("User-Agent", "Docker-Cert-IPUpdater/1.0")


	q := req.URL.Query()
	q.Add("domains", duckDNSSubdomain)
	q.Add("token", token)
	if ipv4 != "" {
		q.Add("ip", ipv4)
	} else {
		q.Add("ip", "") // Explicitly clear if IPv4 is not available
	}
	if ipv6 != "" { // DuckDNS supports ipv6 parameter
		q.Add("ipv6", ipv6)
	}
	// q.Add("verbose", "true") // verbose=true is useful for DuckDNS raw response

	req.URL.RawQuery = q.Encode()

	// Log the request URL with masked sensitive info
	logURL := *req.URL // Create a copy to modify for logging
	logQ := logURL.Query()
	if logQ.Has("token") {
		logQ.Set("token", MaskString(logQ.Get("token"), 4))
	}
	// IP is already public, but masking can be done for consistency if desired, though less critical here.
	// if logQ.Has("ip") { logQ.Set("ip", MaskIP(logQ.Get("ip"))) }
	// if logQ.Has("ipv6") { logQ.Set("ipv6", MaskIP(logQ.Get("ipv6"))) }
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
	// DuckDNS verbose response might not always match the simple "OK\nIP\nSTATUS" structure.
	// It often just returns "OK" or "KO".
	sanitizedResponseForLog := SanitizeDuckDNSResponseBody(rawResponseString)
	s.logger.Printf("IPUpdater: Response from DuckDNS IP update: Status: %d, Body: %s", resp.StatusCode, sanitizedResponseForLog)


	// DuckDNS returns "OK" for success, "KO" for failure in the body. HTTP status is usually 200.
	isSuccessInBody := strings.HasPrefix(strings.ToUpper(rawResponseString), "OK")

	if resp.StatusCode != http.StatusOK || !isSuccessInBody {
		// If body is KO, use that as the primary error message part
		errMsgBodyPart := rawResponseString
		if strings.HasPrefix(strings.ToUpper(rawResponseString), "KO") {
			errMsgBodyPart = "KO - Bad token or other issue."
		}

		err = fmt.Errorf("DuckDNS IP update API request failed. HTTP Status: %d, Body: %s", resp.StatusCode, errMsgBodyPart)
		s.mu.Lock()
		s.lastError = err
		s.mu.Unlock()
		return err
	}

	s.logger.Printf("IPUpdater: DuckDNS IP update for %s successful.", domain)
	s.mu.Lock()
	s.lastSuccess = time.Now()
	s.mu.Unlock()
	return nil
}

func (s *Service) CheckAndPerformIPUpdate() {
	s.mu.Lock()
	s.lastCheckAttempt = time.Now()
	s.mu.Unlock()

	// Use s.config fields directly now, as they are part of the Service struct
	if s.config.DuckDNSIPUpdateDomain == "" {
		s.logger.Println("IPUpdater: IP update check skipped (no DuckDNSIPUpdateDomain configured).")
		return
	}

	s.logger.Println("IPUpdater: Checking public IP address...")
	currentIPv4, currentIPv6, err := s.GetCurrentPublicIPs() // currentIPv6 might be empty
	if err != nil {
		s.logger.Printf("IPUpdater: Error getting current public IP: %v. Skipping DuckDNS update.", err)
		s.mu.Lock()
		s.lastError = err
		s.mu.Unlock()
		return
	}

	// s.mu is locked below this critical section
	s.mu.Lock()

	if currentIPv4 == "" && currentIPv6 == "" { // Adjusted to check if both are empty
		s.logger.Println("IPUpdater: Could not determine current public IP (neither IPv4 nor IPv6). Skipping DuckDNS update.")
		s.lastError = fmt.Errorf("could not determine current public IP")
		s.mu.Unlock()
		return
	}

	// Determine if an update is needed
	needsUpdate := false
	reason := ""

	if !s.initialUpdatePerformed {
		s.logger.Println("IPUpdater: Performing initial IP update (will update regardless of change).")
		needsUpdate = true
		s.initialUpdatePerformed = true // Set this flag under lock
	} else {
		if currentIPv4 != "" && currentIPv4 != s.lastIPv4 {
			reason = fmt.Sprintf("IPv4 changed from %s to %s", MaskIP(s.lastIPv4), MaskIP(currentIPv4))
			needsUpdate = true
		}
		// Only consider IPv6 if it's available and different, and IPv4 hasn't changed (or isn't available)
		if !needsUpdate && currentIPv6 != "" && currentIPv6 != s.lastIPv6 {
			reason = fmt.Sprintf("IPv6 changed from %s to %s", MaskIP(s.lastIPv6), MaskIP(currentIPv6))
			needsUpdate = true
		}
	}

	if !needsUpdate {
		s.logger.Printf("IPUpdater: Public IP address has not changed (IPv4: %s). No DuckDNS update needed.", MaskIP(s.lastIPv4))
		s.lastError = nil // Clear last error if no update needed and no new error occurred
		s.mu.Unlock()
		return
	}

	// If update is needed, release lock before calling UpdateDuckDNSIP (which takes its own lock)
	// then re-acquire to update state. This avoids lock-ordering issues if UpdateDuckDNSIP were to call
	// another method on `s` that also takes the lock. However, UpdateDuckDNSIP takes its own internal lock for its state.
	// For simplicity and correctness here, we can keep the lock for the state update.
	// No, UpdateDuckDNSIP should be callable independently.
	// Let's update the local state (lastIPv4, lastIPv6) *after* a successful update.
	s.mu.Unlock() // Release lock before network call

	s.logger.Printf("IPUpdater: %s. Updating DuckDNS.", reason)
	// Pass the correct token from config
	updateErr := s.UpdateDuckDNSIP(s.config.DuckDNSIPUpdateDomain, s.config.DuckDNSToken, currentIPv4, currentIPv6)

	s.mu.Lock() // Re-acquire lock to update state
	if updateErr != nil {
		s.logger.Printf("IPUpdater: Error updating DuckDNS IP: %v", updateErr)
		s.lastError = updateErr
	} else {
		s.logger.Printf("IPUpdater: Successfully updated DuckDNS with IPv4: %s (and IPv6: %s if applicable).", MaskIP(currentIPv4), MaskIP(currentIPv6))
		s.lastIPv4 = currentIPv4
		s.lastIPv6 = currentIPv6 // Store it even if DuckDNS primarily uses IPv4 via 'ip' param
		s.lastSuccess = time.Now()
		s.lastError = nil
	}
	s.mu.Unlock()
}


// StartScheduler starts the periodic IP update checks.
// This method is intended to be run as a goroutine by the caller (e.g., in main.go).
func (s *Service) StartScheduler() {
	// Check config conditions from s.config, as it's part of the Service struct
	if s.config.DuckDNSIPUpdateDomain == "" || s.config.DuckDNSIPUpdateInterval <= 0 {
		s.logger.Println("IPUpdater: Scheduler not started (domain not configured or interval invalid).")
		return // Do not proceed if not configured to run
	}

	s.wg.Add(1)       // Increment counter for this scheduler goroutine
	defer s.wg.Done() // Ensure counter is decremented when this goroutine exits

	s.logger.Printf("IPUpdater: Scheduler worker goroutine started for domain '%s'. Update interval: %v\n",
		s.config.DuckDNSIPUpdateDomain, s.config.DuckDNSIPUpdateInterval)

	// Perform an initial check immediately upon starting the scheduler
	// This runs within this goroutine, so it blocks the first tick, which is fine.
	s.CheckAndPerformIPUpdate()

	ticker := time.NewTicker(s.config.DuckDNSIPUpdateInterval)
	defer ticker.Stop()

schedulerLoop:
	for {
		select {
		case <-ticker.C:
			s.logger.Println("IPUpdater: Performing scheduled IP update check...")
			s.CheckAndPerformIPUpdate()
		case <-s.stopChan: // Listen for the stop signal
			s.logger.Println("IPUpdater: Scheduler stop signal received, shutting down worker goroutine...")
			break schedulerLoop // Exit the loop
		}
	}
	s.logger.Println("IPUpdater: Scheduler worker goroutine stopped.")
}

// StopScheduler signals the IP update scheduler to shut down gracefully.
func (s *Service) StopScheduler(ctx context.Context) {
	// Check if scheduler was meant to run; if not, nothing to stop.
	if s.config.DuckDNSIPUpdateDomain == "" || s.config.DuckDNSIPUpdateInterval <= 0 {
		s.logger.Println("IPUpdater: StopScheduler called, but scheduler was not configured to run.")
		return
	}

	s.logger.Println("INFO: IPUpdater: Initiating scheduler stop...")
	// Note: Closing a nil channel or a channel multiple times will panic.
	// Ensure stopChan is not nil (it is initialized in NewService) and handle potential race if Stop is called multiple times.
	// A simple way is to use a sync.Once or check if already closed, though for this pattern,
	// closing it once is the main goal. The scheduler loop will exit.

	// Safely close the channel if it hasn't been closed yet.
	// This requires a bit more state management or can be simplified if StopScheduler is guaranteed to be called once.
	// For this example, assume it's called once during shutdown.
	close(s.stopChan) // Signal the worker goroutine by closing the stop channel

	// Wait for the worker goroutine to finish, with a timeout from the context
	doneWaiting := make(chan struct{})
	go func() {
		s.wg.Wait() // Wait for the scheduler goroutine (wg.Add(1) in StartScheduler) to complete
		close(doneWaiting)
	}()

	select {
	case <-doneWaiting:
		s.logger.Println("INFO: IPUpdater: Scheduler stopped gracefully.")
	case <-ctx.Done(): // If the overall shutdown context times out
		s.logger.Printf("WARNING: IPUpdater: Timed out waiting for scheduler to stop: %v\n", ctx.Err())
	}
}


// GetStatus returns the current operational status of the IP updater.
func (s *Service) GetStatus() ComponentStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isInitialized { // Should always be true after NewService
		return ComponentStatus{Status: "error", Message: "IP Updater not initialized"}
	}
	if s.config.DuckDNSIPUpdateDomain == "" {
		return ComponentStatus{Status: "disabled", Message: "IP Updater disabled: No domain configured."}
	}

	status := ComponentStatus{
		LastIPv4Detected: MaskIP(s.lastIPv4),
		LastIPv6Detected: MaskIP(s.lastIPv6),
	}
	if !s.lastCheckAttempt.IsZero() {
		status.LastCheckAttempt = s.lastCheckAttempt.Format(time.RFC3339Nano) // More precision
	}

	if s.lastError != nil {
		status.Status = "error"
		// Avoid overly verbose error messages directly in status; log them separately.
		status.Message = "Last operation failed. Check logs for details."
		status.LastUpdateError = s.lastError.Error() // Keep full error here for API consumers if needed
	} else if !s.lastSuccess.IsZero() {
		status.Status = "ok"
		status.Message = "IP Updater operational."
		status.LastSuccess = s.lastSuccess.Format(time.RFC3339Nano) // More precision
	} else if !s.initialUpdatePerformed {
		status.Status = "initializing"
		status.Message = "IP Updater has not performed its initial successful update check yet."
	} else {
		// This case implies it's initialized, no error, but also no success yet (e.g. IP hasn't changed after initial)
		status.Status = "ok" // Or perhaps "idle" or "monitoring"
		status.Message = "IP Updater is monitoring; no IP change detected since last successful update or initial check."
	}
	return status
}