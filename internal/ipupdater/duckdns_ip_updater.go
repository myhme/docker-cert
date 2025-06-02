// File: internal/ipupdater/duckdns_ip_updater.go
package ipupdater

import (
	"context"
	"fmt"
	"io"
	"log" // Using standard log, consistent with other files
	"net/http"
	"os" // For getEnv helper, if you decide to use it here for constants
	"strconv"
	"strings"
	"sync"
	"time"

	// Assuming your module path, adjust if necessary
	"docker-cert/internal/config" // REPLACE with your actual module path
)

// Logger interface matches the one you defined earlier or use *log.Logger directly.
// For simplicity, this example will assume *log.Logger is passed or use the global log.
// If your main.go passes its `logger` (which is *log.Logger), Service.logger should be *log.Logger.
// Let's stick to *log.Logger for consistency with other example skeletons.
// type Logger interface {
// 	Printf(format string, v ...interface{})
// 	Println(v ...interface{})
// }

const (
	duckDNSUpdateURL = "https://www.duckdns.org/update"
	ipifyAPIIPv4     = "https://api.ipify.org?format=text"
	ipifyAPIIPv6     = "https://api64.ipify.org?format=text" // For potential future use
)

// Service handles IP updates.
type Service struct {
	config               *config.Config // Using AppConfig from your config package
	logger               *log.Logger    // Changed to *log.Logger for consistency
	httpClient           *http.Client
	mu                   sync.Mutex // Protects access to lastIPs, lastError, lastSuccess, etc.
	lastIPv4             string
	lastIPv6             string
	lastError            error
	lastSuccess          time.Time
	lastCheckAttempt     time.Time
	isInitialized        bool // From your original struct
	initialUpdatePerformed bool // Tracks if the first *successful* update has happened

	// Fields for graceful shutdown of the scheduler
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// ComponentStatus represents the health status of a component.
type ComponentStatus struct {
	Status             string `json:"status"`
	Message            string `json:"message"`
	LastIPv4Detected   string `json:"last_ipv4_detected,omitempty"`
	LastIPv6Detected   string `json:"last_ipv6_detected,omitempty"`
	LastSuccess        string `json:"last_success,omitempty"`
	LastCheckAttempt   string `json:"last_check_attempt,omitempty"`
	LastUpdateError    string `json:"last_update_error,omitempty"`
}

func NewService(cfg *config.Config, logger *log.Logger) *Service {
	if logger == nil {
		// Fallback or panic, consistent with your original check
		log.Println("CRITICAL: IPUpdater Service initialized with a nil logger, using global log.")
		logger = log.New(os.Stderr, "IPUPDATER_FALLBACK: ", log.LstdFlags|log.Lmicroseconds|log.Lshortfile)
	}
	return &Service{
		config:        cfg,
		logger:        logger,
		httpClient:    &http.Client{Timeout: 30 * time.Second}, // Timeout for external IP/DuckDNS calls
		isInitialized: true,
		stopChan:      make(chan struct{}),
	}
}

// MaskString, MaskIP, SanitizeDuckDNSResponseBody functions (copied from your version)
func MaskString(sensitiveString string, visibleChars int) string {
	if sensitiveString == "" { return "" }
	length := len(sensitiveString)
	if visibleChars < 0 { visibleChars = 0 }
	if length <= visibleChars*2 {
		if length > 2 && visibleChars == 1 { return sensitiveString[:1] + strings.Repeat("*", length-2) + sensitiveString[length-1:] }
		return strings.Repeat("*", length)
	}
	return sensitiveString[:visibleChars] + strings.Repeat("*", length-(visibleChars*2)) + sensitiveString[length-visibleChars:]
}

func MaskIP(ipAddress string) string {
	if ipAddress == "" { return "" }
	parts := strings.Split(ipAddress, ".")
	if len(parts) == 4 { // Basic IPv4 check
		validIPv4 := true
		for _, part := range parts {
			if val, err := strconv.Atoi(part); err != nil || val < 0 || val > 255 {
				validIPv4 = false; break
			}
		}
		if validIPv4 { return fmt.Sprintf("%s.x.x.%s", parts[0], parts[3]) }
	}
	if strings.Contains(ipAddress, ":") { return "xxxx:xxxx:...:xxxx" } // Generic IPv6 mask
	return ipAddress
}

func SanitizeDuckDNSResponseBody(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) > 0 && strings.ToUpper(lines[0]) == "OK" {
		if len(lines) > 1 { lines[1] = MaskIP(lines[1]) }
		return strings.Join(lines, "\n")
	} else if strings.ToUpper(body) == "KO" { return "KO" }
	return "Response (format not fully parsed for masking or not OK/KO)"
}


func (s *Service) fetchPublicIP(apiURL, ipType string) (string, error) {
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for %s service %s: %w", ipType, apiURL, err)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("%s-IPUpdater/1.0 (%s)", s.config.AppNameOrDefault(), ipType)) // Using AppName from config

	resp, err := s.httpClient.Do(req)
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

func (s *Service) GetCurrentPublicIPs() (ipv4, ipv6 string, err error) {
	s.logger.Println("INFO: IPUpdater: Fetching public IP addresses...")
	var wg sync.WaitGroup
	var errIPv4, errIPv6 error

	wg.Add(1)
	go func() {
		defer wg.Done()
		fetchedIPv4, e4 := s.fetchPublicIP(ipifyAPIIPv4, "IPv4")
		if e4 != nil {
			s.logger.Printf("ERROR: IPUpdater: Error fetching IPv4: %v", e4)
			errIPv4 = e4
		} else if fetchedIPv4 != "" {
			s.logger.Printf("INFO: IPUpdater: Fetched public IPv4: %s", MaskIP(fetchedIPv4))
			ipv4 = fetchedIPv4
		}
	}()

	// Example: Enable IPv6 fetching if needed by uncommenting
	// wg.Add(1)
	// go func() {
	// 	defer wg.Done()
	// 	fetchedIPv6, e6 := s.fetchPublicIP(ipifyAPIIPv6, "IPv6")
	// 	if e6 != nil {
	// 		s.logger.Printf("ERROR: IPUpdater: Error fetching IPv6: %v", e6)
	// 		errIPv6 = e6
	// 	} else if fetchedIPv6 != "" {
	// 		s.logger.Printf("INFO: IPUpdater: Fetched public IPv6: %s", MaskIP(fetchedIPv6))
	// 		ipv6 = fetchedIPv6
	// 	}
	// }()

	wg.Wait()

	if errIPv4 != nil && errIPv6 != nil {
		err = fmt.Errorf("failed to fetch IPv4 (%w) and IPv6 (%w)", errIPv4, errIPv6)
	} else if errIPv4 != nil {
		err = errIPv4
	} else if errIPv6 != nil {
		err = errIPv6
	}
	return ipv4, ipv6, err
}

func (s *Service) UpdateDuckDNSIP(domain, token, ipv4, ipv6 string) error {
	s.mu.Lock()
	s.lastError = nil // Clear previous error for this specific attempt
	s.mu.Unlock()

	if domain == "" { return fmt.Errorf("DuckDNS domain for IP update cannot be empty") }
	if token == "" { return fmt.Errorf("DuckDNS token for IP update cannot be empty") }

	duckDNSSubdomain := strings.TrimSuffix(domain, ".duckdns.org")
	if duckDNSSubdomain == domain || duckDNSSubdomain == "" {
		return fmt.Errorf("invalid DuckDNS domain format for IP update: %s (must be 'yoursubdomain.duckdns.org')", domain)
	}

	s.logger.Printf("INFO: IPUpdater: Attempting to update DuckDNS IP for %s (subdomain: %s)", domain, duckDNSSubdomain)

	req, err := http.NewRequest("GET", duckDNSUpdateURL, nil)
	if err != nil { return fmt.Errorf("IPUpdater: failed to create DuckDNS update request: %w", err) }
	req.Header.Set("User-Agent", fmt.Sprintf("%s-IPUpdater/1.0", s.config.AppNameOrDefault()))


	q := req.URL.Query()
	q.Add("domains", duckDNSSubdomain)
	q.Add("token", token)
	if ipv4 != "" { q.Add("ip", ipv4) } else { q.Add("ip", "") } // Explicitly clear if no IPv4
	if ipv6 != "" { q.Add("ipv6", ipv6) }
	// DuckDNS usually returns OK/KO, verbose isn't strictly necessary for programmatic checks
	// q.Add("verbose", "true")
	req.URL.RawQuery = q.Encode()

	logURL := *req.URL
	logQ := logURL.Query()
	if logQ.Has("token") { logQ.Set("token", MaskString(logQ.Get("token"), 4)) }
	logURL.RawQuery = logQ.Encode()
	s.logger.Printf("INFO: IPUpdater: Sending DuckDNS IP update request to URL: %s", logURL.String())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.mu.Lock(); s.lastError = err; s.mu.Unlock()
		return fmt.Errorf("IPUpdater: DuckDNS IP update API request for %s failed: %w", domain, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		s.mu.Lock(); s.lastError = err; s.mu.Unlock()
		return fmt.Errorf("IPUpdater: failed to read DuckDNS IP update response body: %w", err)
	}
	rawResponseString := string(bodyBytes)
	s.logger.Printf("INFO: IPUpdater: Response from DuckDNS IP update: Status: %d, Body: %s", resp.StatusCode, SanitizeDuckDNSResponseBody(rawResponseString))

	isSuccessInBody := strings.HasPrefix(strings.ToUpper(rawResponseString), "OK")
	if resp.StatusCode != http.StatusOK || !isSuccessInBody {
		errMsg := fmt.Sprintf("DuckDNS IP update API request failed. HTTP Status: %d, Body: %s", resp.StatusCode, rawResponseString)
		s.mu.Lock(); s.lastError = fmt.Errorf(errMsg); s.mu.Unlock()
		return fmt.Errorf(errMsg)
	}

	s.logger.Printf("INFO: IPUpdater: DuckDNS IP update for %s successful.", domain)
	s.mu.Lock(); s.lastSuccess = time.Now(); s.mu.Unlock()
	return nil
}

// CheckAndPerformIPUpdate contains the refined logic.
func (s *Service) CheckAndPerformIPUpdate() {
	s.mu.Lock()
	s.lastCheckAttempt = time.Now()
	s.mu.Unlock()

	if s.config.DuckDNSIPUpdateDomain == "" {
		s.logger.Println("INFO: IPUpdater: IP update check skipped (no DuckDNSIPUpdateDomain configured).")
		return
	}

	s.logger.Println("INFO: IPUpdater: Checking public IP address...")
	currentIPv4, currentIPv6, err := s.GetCurrentPublicIPs()
	if err != nil {
		s.logger.Printf("ERROR: IPUpdater: Error getting current public IP: %v. Skipping DuckDNS update.", err)
		s.mu.Lock(); s.lastError = err; s.mu.Unlock()
		return
	}

	s.mu.Lock() // Lock for reading and modifying shared state (lastIPs, initialUpdatePerformed)

	needsUpdate := false
	reasonForUpdate := "No IP change detected"

	if currentIPv4 == "" && currentIPv6 == "" { // If no IP could be fetched
		s.logger.Println("WARN: IPUpdater: Could not determine current public IP (neither IPv4 nor IPv6). Skipping DuckDNS update.")
		s.lastError = fmt.Errorf("could not determine current public IP")
		s.mu.Unlock()
		return
	}

	if !s.initialUpdatePerformed {
		s.logger.Println("INFO: IPUpdater: Performing initial IP update (will update regardless of current IP value for the first successful update).")
		needsUpdate = true
		reasonForUpdate = "Initial update required"
		// initialUpdatePerformed will be set to true only after a *successful* update below
	} else {
		if currentIPv4 != "" && currentIPv4 != s.lastIPv4 {
			reasonForUpdate = fmt.Sprintf("IPv4 changed from %s to %s", MaskIP(s.lastIPv4), MaskIP(currentIPv4))
			needsUpdate = true
		}
		// Add IPv6 check if relevant and if IPv4 didn't change
		if !needsUpdate && currentIPv6 != "" && currentIPv6 != s.lastIPv6 {
			reasonForUpdate = fmt.Sprintf("IPv6 changed from %s to %s", MaskIP(s.lastIPv6), MaskIP(currentIPv6))
			needsUpdate = true
		}
		// Consider if current IP fetch fails AFTER initial success
		if !needsUpdate && (currentIPv4 == "" && s.lastIPv4 != "") {
			s.logger.Printf("WARN: IPUpdater: Could not fetch current IPv4, last known was %s. No update attempted this cycle.", MaskIP(s.lastIPv4))
			// Decide if this scenario should trigger an update (e.g., to clear the IP at DuckDNS by sending empty 'ip=')
		}
	}

	if !needsUpdate {
		s.logger.Printf("INFO: IPUpdater: %s. No DuckDNS update needed this cycle.", reasonForUpdate)
		// s.lastError = nil; // Clearing lastError here might hide a previous fetch error if no update is needed now.
		                      // Only clear lastError on explicit success.
		s.mu.Unlock()
		return
	}
	s.mu.Unlock() // Release lock before making the network call for DuckDNS update

	s.logger.Printf("INFO: IPUpdater: %s. Attempting to update DuckDNS.", reasonForUpdate)
	updateErr := s.UpdateDuckDNSIP(s.config.DuckDNSIPUpdateDomain, s.config.DuckDNSToken, currentIPv4, currentIPv6)

	s.mu.Lock() // Re-acquire lock to update state
	if updateErr != nil {
		s.logger.Printf("ERROR: IPUpdater: Error updating DuckDNS IP: %v", updateErr)
		s.lastError = updateErr
		// Do not set initialUpdatePerformed to true if the initial update (or any update) fails
	} else {
		s.logger.Printf("INFO: IPUpdater: Successfully updated DuckDNS with IPv4: %s (and IPv6: %s if applicable).", MaskIP(currentIPv4), MaskIP(currentIPv6))
		s.lastIPv4 = currentIPv4
		s.lastIPv6 = currentIPv6
		s.lastSuccess = time.Now()
		s.lastError = nil
		if !s.initialUpdatePerformed { // Set this flag only after the first *successful* update
			s.initialUpdatePerformed = true
			s.logger.Println("INFO: IPUpdater: Initial IP update successfully performed and recorded.")
		}
	}
	s.mu.Unlock()
}

// StartScheduler starts the periodic IP update checks.
// This method is intended to be run as a goroutine by the caller (e.g., in main.go).
func (s *Service) StartScheduler() {
	if s.config.DuckDNSIPUpdateDomain == "" || s.config.DuckDNSIPUpdateInterval <= 0 {
		s.logger.Println("INFO: IPUpdater: Scheduler not started (domain not configured or interval invalid).")
		return
	}

	s.wg.Add(1)
	defer s.wg.Done()

	s.logger.Printf("INFO: IPUpdater: Scheduler worker goroutine started for domain '%s'. Update interval: %v\n",
		s.config.DuckDNSIPUpdateDomain, s.config.DuckDNSIPUpdateInterval)

	// Perform an initial check immediately upon starting the scheduler.
	// This runs within this goroutine, blocking the first tick, which is fine.
	s.CheckAndPerformIPUpdate()

	ticker := time.NewTicker(s.config.DuckDNSIPUpdateInterval)
	defer ticker.Stop()

schedulerLoop:
	for {
		select {
		case <-ticker.C:
			s.logger.Println("INFO: IPUpdater: Performing scheduled IP update check...")
			s.CheckAndPerformIPUpdate()
		case <-s.stopChan:
			s.logger.Println("INFO: IPUpdater: Scheduler stop signal received, shutting down worker goroutine...")
			break schedulerLoop
		}
	}
	s.logger.Println("INFO: IPUpdater: Scheduler worker goroutine stopped.")
}

// StopScheduler signals the IP update scheduler to shut down gracefully.
func (s *Service) StopScheduler(ctx context.Context) {
	if s.config.DuckDNSIPUpdateDomain == "" || s.config.DuckDNSIPUpdateInterval <= 0 {
		s.logger.Println("INFO: IPUpdater: StopScheduler called, but scheduler was not configured to run or already stopped.")
		return
	}

	s.logger.Println("INFO: IPUpdater: Initiating scheduler stop...")

	// Ensure closing stopChan is safe (e.g., if StopScheduler could be called multiple times)
	// For this pattern, we assume one call during application shutdown.
	// If multiple calls are possible, add a sync.Once or check if already closed.
	// panic: close of closed channel
	// To prevent panic on double close, we can use a flag or sync.Once
	// For simplicity here, assuming single call from main's shutdown sequence.
	alreadyClosed := false
	s.mu.Lock() // Protect access to stopChan if checking state, though close is idempotent on effect after first.
	// A select with a default can check if a channel is closed, but that's for reading.
	// The simplest for now is to rely on single-call semantics from main.
	// If more robustness is needed:
	// if s.stopChan != nil { // check if not nil
	//    select {
	//    case <-s.stopChan:
	//        // already closed
	//        alreadyClosed = true
	//    default:
	//        // not closed yet
	//    }
	//    if !alreadyClosed {
	//        close(s.stopChan)
	//    }
	// }
	// However, the main pattern usually just closes it, assuming it's the designated shutdown signal path.
	// Let's assume it hasn't been closed yet.
	// A panic here implies a logic error in how StopScheduler is called or how stopChan is managed.
	// We make stopChan in NewService, so it's not nil.
	// The primary concern is closing it more than once. If StartScheduler wasn't called, wg would be 0.
	// The wg.Add is in StartScheduler. If StartScheduler never ran, stopChan exists but wg might not be incremented.
	// This is why the initial config check in StopScheduler is important.

	// This method's primary goal is to signal and wait.
	// The check for whether it *should* run is good.

	select {
	case <-s.stopChan:
		// Already closed or being closed by another goroutine, unusual in this specific pattern
		s.logger.Println("INFO: IPUpdater: stopChan already closed when StopScheduler called.")
	default:
		close(s.stopChan) // Signal the worker goroutine
	}


	doneWaiting := make(chan struct{})
	go func() {
		s.wg.Wait() // Wait for the scheduler goroutine to finish
		close(doneWaiting)
	}()

	select {
	case <-doneWaiting:
		s.logger.Println("INFO: IPUpdater: Scheduler stopped gracefully.")
	case <-ctx.Done():
		s.logger.Printf("WARNING: IPUpdater: Timed out waiting for scheduler to stop: %v\n", ctx.Err())
	}
}

// GetStatus returns the current operational status of the IP updater. (Copied from your version, with minor log/message tweaks)
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
		LastIPv4Detected: MaskIP(s.lastIPv4),
		LastIPv6Detected: MaskIP(s.lastIPv6),
	}
	if !s.lastCheckAttempt.IsZero() {
		status.LastCheckAttempt = s.lastCheckAttempt.Format(time.RFC3339Nano)
	}

	if s.lastError != nil {
		status.Status = "error"
		status.Message = "Last operation failed. Check logs for details." // More user-friendly
		status.LastUpdateError = s.lastError.Error()
	} else if !s.lastSuccess.IsZero() {
		status.Status = "ok"
		status.Message = "IP Updater operational and last update was successful."
		status.LastSuccess = s.lastSuccess.Format(time.RFC3339Nano)
	} else if !s.initialUpdatePerformed { // This means no successful update has occurred yet.
		status.Status = "initializing"
		status.Message = "IP Updater is initializing; awaiting first successful update."
	} else {
		// Initial update was successful, but current state might be just monitoring (no recent errors/successes)
		status.Status = "ok"
		status.Message = "IP Updater is monitoring; no IP change detected or new update since last success."
	}
	return status
}

// Helper for config to get AppName or a default
// This should ideally be part of the config.AppConfig struct or a method on it.
// Adding it here for now if not available.
func (c *config.Config) AppNameOrDefault() string {
	// Assuming AppConfig might have an AppName field, or use a default
	// if hasattr(c, "AppName") && c.AppName != "" { return c.AppName }
	return "Docker-Cert" // Default app name for User-Agent
}