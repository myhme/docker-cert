package config

import (
	"fmt"
	"log" // Added for defaultLogger
	"os"
	"strconv"
	"strings"
	"time"
)

// Logger is an interface for logging.
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
	Fatalf(format string, v ...interface{})
}

// Config holds all application configuration.
type Config struct {
	LetsEncryptEmail    string
	LetsEncryptDomains  []string
	UseWildcard         bool
	Testing             bool
	RenewalCheckInterval time.Duration
	PreferredChain      string

	DuckDNSToken string // Primary token for ACME DNS challenges

	// Fields for automatic IP updates
	DuckDNSIPUpdateDomain    string        // Domain to update (e.g., mydomain.duckdns.org)
	DuckDNSIPUpdateInterval  time.Duration // How often to check/update the IP
	DuckDNSIPUpdateToken     string        // Token for IP updates (defaults to DuckDNSToken if empty)

	CertsBasePath    string
	UID              int
	GID              int
	AccountKeyDir    string // Relative to CertsBasePath

	InternalHTTPPort string
	APIAuthToken     string // Optional Bearer token for API
}

// maskString replaces parts of a sensitive string with asterisks for logging.
// It shows 'visibleChars' at the beginning and end if the string is long enough.
func maskString(sensitiveString string, visibleChars int) string {
	if sensitiveString == "" {
		return ""
	}
	length := len(sensitiveString)
	if visibleChars < 0 {
		visibleChars = 0 // Ensure visibleChars is not negative
	}

	if length <= visibleChars*2 {
		// String is too short to mask with visible ends, or visibleChars is large
		// Mask the entire string or based on a minimal visibility rule if preferred
		if length > 2 && visibleChars == 1 { // Show first and last if at least 3 chars long for 1 visible char
			return sensitiveString[:1] + strings.Repeat("*", length-2) + sensitiveString[length-1:]
		}
		return strings.Repeat("*", length)
	}
	// Show 'visibleChars' from the start and 'visibleChars' from the end
	return sensitiveString[:visibleChars] + strings.Repeat("*", length-(visibleChars*2)) + sensitiveString[length-visibleChars:]
}

// LoadConfig loads configuration from environment variables.
func LoadConfig(logger Logger) (*Config, error) {
	if logger == nil {
		// Fallback to standard log if no logger is provided, though ideally it should always be passed.
		// This is more of a safeguard. In a real app, you might panic or return an error.
		fmt.Println("WARNING: Config.LoadConfig called with nil logger. Using standard log for config loading errors.")
		logger = &defaultLogger{} // Use a simple fallback logger
	}

	// Load LETSENCRYPT_DOMAIN string first as it might be used for DUCKDNS_IP_UPDATE_DOMAIN default
	letsEncryptDomainStr := getEnv("LETSENCRYPT_DOMAIN", "")

	cfg := &Config{
		LetsEncryptEmail:     getEnv("LETSENCRYPT_EMAIL", ""),
		UseWildcard:          parseBool(getEnv("LETSENCRYPT_WILDCARD", "false")),
		Testing:              parseBool(getEnv("TESTING", "false")),
		RenewalCheckInterval: parseDuration(getEnv("RENEWAL_CHECK_INTERVAL_HOURS", "12h"), 12*time.Hour, logger),
		PreferredChain:       strings.ToLower(getEnv("LETSENCRYPT_CHAIN", "default")),

		DuckDNSToken: getEnv("DUCKDNS_TOKEN", ""), // This is the primary token

		DuckDNSIPUpdateDomain:    getEnv("DUCKDNS_IP_UPDATE_DOMAIN", ""), // Specific domain for IP updates
		DuckDNSIPUpdateInterval:  parseDuration(getEnv("DUCKDNS_IP_UPDATE_INTERVAL_SECONDS", "300s"), 300*time.Second, logger), // e.g., 5 minutes
		DuckDNSIPUpdateToken:     getEnv("DUCKDNS_IP_UPDATE_TOKEN", ""), // Specific token for IP updates

		CertsBasePath:    getEnv("CERTS_BASE_PATH", "/data/config"),
		UID:              parseInt(getEnv("UID", "0"), 0, logger),
		GID:              parseInt(getEnv("GID", "0"), 0, logger),
		InternalHTTPPort: getEnv("INTERNAL_HTTP_PORT", "8080"),
		APIAuthToken:     getEnv("API_AUTH_TOKEN", ""), // Optional
	}
	cfg.AccountKeyDir = "accounts/default" // Hardcoded relative path for ACME account key

	// Populate LetsEncryptDomains from the loaded string
	if letsEncryptDomainStr == "" {
		cfg.LetsEncryptDomains = []string{} // Will be caught by validation
	} else {
		cfg.LetsEncryptDomains = parseStringSlice(letsEncryptDomainStr)
	}


	// Default DUCKDNS_IP_UPDATE_DOMAIN if not explicitly set and LETSENCRYPT_DOMAIN is available
	if cfg.DuckDNSIPUpdateDomain == "" && len(cfg.LetsEncryptDomains) > 0 {
		firstDomainFromList := cfg.LetsEncryptDomains[0]
		// Ensure it's a .duckdns.org domain or a subdomain that would resolve to one for IP updates.
		// For simplicity, we assume if LETSENCRYPT_DOMAIN is used, it's the target for IP updates.
		// If it's a wildcard, strip the wildcard part for the IP update domain.
		cfg.DuckDNSIPUpdateDomain = strings.TrimPrefix(strings.TrimSpace(firstDomainFromList), "*.")
		logger.Printf("INFO: DUCKDNS_IP_UPDATE_DOMAIN not set, defaulting to first LETSENCRYPT_DOMAIN: %s", cfg.DuckDNSIPUpdateDomain)
	}

	// Default DUCKDNS_IP_UPDATE_TOKEN to DUCKDNS_TOKEN if the specific IP update token is not set
	if cfg.DuckDNSIPUpdateToken == "" {
		cfg.DuckDNSIPUpdateToken = cfg.DuckDNSToken
		if cfg.DuckDNSIPUpdateDomain != "" && cfg.DuckDNSToken != "" { // Only log if IP updates are active and a token will be used
			logger.Println("INFO: DUCKDNS_IP_UPDATE_TOKEN not set, defaulting to DUCKDNS_TOKEN for IP updates.")
		}
	}

	// Validate all required fields
	if err := validateRequired(cfg, logger); err != nil {
		return nil, err
	}
	logWarnings(cfg, logger) // Log non-critical warnings

	return cfg, nil
}

func validateRequired(cfg *Config, logger Logger) error {
	// DUCKDNS_TOKEN is essential for DNS-01 challenges
	if cfg.DuckDNSToken == "" || cfg.DuckDNSToken == "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX" {
		return fmt.Errorf("DUCKDNS_TOKEN environment variable must be set with your actual DuckDNS token and not be the default placeholder")
	}

	// If IP updates are configured (domain is set), then a token for it must be available
	// (which defaults to DUCKDNS_TOKEN, so this check is implicitly covered by the above if DUCKDNS_IP_UPDATE_TOKEN is not set)
	if cfg.DuckDNSIPUpdateDomain != "" && cfg.DuckDNSIPUpdateToken == "" {
		// This scenario implies DUCKDNS_IP_UPDATE_DOMAIN is set, DUCKDNS_IP_UPDATE_TOKEN is explicitly empty,
		// AND DUCKDNS_TOKEN was also empty (which would have been caught above).
		// So, this is a redundant check if the default logic is sound, but good for explicit clarity.
		return fmt.Errorf("DUCKDNS_IP_UPDATE_DOMAIN ('%s') is set, but no token (DUCKDNS_IP_UPDATE_TOKEN or DUCKDNS_TOKEN) is available for IP updates", cfg.DuckDNSIPUpdateDomain)
	}


	if len(cfg.LetsEncryptDomains) == 0 {
		originalLetsEncryptDomainStr := getEnv("LETSENCRYPT_DOMAIN", "")
		if originalLetsEncryptDomainStr == "" {
			return fmt.Errorf("LETSENCRYPT_DOMAIN environment variable must be set (e.g., mydomain.duckdns.org or my.customdomain.com)")
		}
		return fmt.Errorf("LETSENCRYPT_DOMAIN ('%s') parsed into an empty list of domains. Ensure it's a valid, non-empty domain or a comma-separated list of domains", originalLetsEncryptDomainStr)
	}
	return nil
}

func logWarnings(cfg *Config, logger Logger) {
	if cfg.LetsEncryptEmail == "" {
		logger.Println("WARNING: LETSENCRYPT_EMAIL is not set. Proceeding with ACME account registration without an email address. You will not receive important CA notifications (e.g., about expiring certificates if this tool fails).")
	}
	if cfg.PreferredChain == "default" || cfg.PreferredChain == "" {
		logger.Println("INFO: LETSENCRYPT_CHAIN is 'default' or unset. No specific certificate chain will be preferred during issuance.")
	} else {
		logger.Printf("INFO: LETSENCRYPT_CHAIN is set to '%s'. This chain will be preferred if available from the CA.", cfg.PreferredChain)
	}
	if cfg.APIAuthToken == "" {
		logger.Println("WARNING: API_AUTH_TOKEN is not set. API endpoints for triggering actions (like manual renewal or IP update) will be UNPROTECTED.")
	}
	if cfg.DuckDNSIPUpdateDomain == "" {
		logger.Println("INFO: DUCKDNS_IP_UPDATE_DOMAIN is not configured. Automatic DuckDNS IP updates will be disabled.")
	} else {
		logger.Printf("INFO: Automatic IP updates enabled for DuckDNS domain: %s. Update interval: %v.", cfg.DuckDNSIPUpdateDomain, cfg.DuckDNSIPUpdateInterval)
		// The following check is important: if DuckDNSIPUpdateToken is empty, it means DuckDNSToken was also empty (due to defaulting logic and prior validation).
		if cfg.DuckDNSIPUpdateToken == "" {
			logger.Println("WARNING: DuckDNS IP updates are configured for a domain, but no valid token (DUCKDNS_IP_UPDATE_TOKEN or DUCKDNS_TOKEN) is available. IP updates will likely fail.")
		}
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func parseBool(str string) bool {
	b, err := strconv.ParseBool(str)
	// Returns false if parsing fails or if the string is not "true"
	return err == nil && b
}

func parseInt(str string, fallback int, logger Logger) int {
	i, err := strconv.Atoi(str)
	if err != nil {
		logger.Printf("WARNING: Config: Could not parse integer from '%s' for an environment variable, using fallback %d. Error: %v", str, fallback, err)
		return fallback
	}
	return i
}

func parseDuration(str string, defaultVal time.Duration, logger Logger) time.Duration {
	if str == "" {
		logger.Printf("INFO: Config: Duration string is empty for an environment variable, using fallback %v.", defaultVal)
		return defaultVal
	}
	d, err := time.ParseDuration(str)
	if err != nil {
		logger.Printf("WARNING: Config: Could not parse duration string '%s', using fallback %v. Error: %v", str, defaultVal, err)
		return defaultVal
	}
	if d <= 0 { // Allow 0 for disabling (e.g. interval), but negative is generally invalid.
	            // For intervals, 0 or negative means disabled or use default, which this function handles by returning defaultVal if parse fails or d <=0.
	            // Let's refine: if d is explicitly 0 and that's a valid "disable" value, the caller should handle it.
	            // Here, we assume positive duration for intervals.
		logger.Printf("WARNING: Config: Non-positive duration '%s' (parsed as %v) is invalid for an interval, using fallback %v.", str, d, defaultVal)
		return defaultVal
	}
	return d
}

func parseStringSlice(str string) []string {
	if str == "" {
		return []string{}
	}
	parts := strings.Split(str, ",")
	var result []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" { // Ensure non-empty string after trimming
			result = append(result, trimmed)
		}
	}
	return result
}

// defaultLogger is a fallback logger if nil is passed to LoadConfig.
type defaultLogger struct{}

func (l *defaultLogger) Printf(format string, v ...interface{}) { fmt.Printf(format+"\n", v...) }
func (l *defaultLogger) Println(v ...interface{})               { fmt.Println(v...) }
func (l *defaultLogger) Fatalf(format string, v ...interface{}) { log.Fatalf(format, v...) } // Uses the imported "log" package
