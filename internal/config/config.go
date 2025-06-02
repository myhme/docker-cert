package config

import (
	"fmt"
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
	CAOrder             []string
	PreferredChain      string

	DuckDNSToken             string
	DuckDNSDomainForChallenge string

	ZeroSSLEnabled    bool
	ZeroSSLEabKid     string
	ZeroSSLEabHmacKey string

	CertsBasePath    string
	UID              int
	GID              int
	AccountKeyDir    string

	InternalHTTPPort string
}

// LoadConfig loads configuration from environment variables.
func LoadConfig(logger Logger) (*Config, error) {
	cfg := &Config{
		LetsEncryptEmail:     getEnv("LETSENCRYPT_EMAIL", ""),
		UseWildcard:          parseBool(getEnv("LETSENCRYPT_WILDCARD", "false")),
		Testing:              parseBool(getEnv("TESTING", "false")),
		RenewalCheckInterval: parseDuration(getEnv("RENEWAL_CHECK_INTERVAL_HOURS", "12"), 12*time.Hour, logger),
		CAOrder:              parseStringSlice(getEnv("CA_ORDER", "letsencrypt,zerossl")),
		PreferredChain:       getEnv("LETSENCRYPT_PREFERRED_CHAIN", ""),

		DuckDNSToken:             getEnv("DUCKDNS_TOKEN", ""),
		DuckDNSDomainForChallenge: getEnv("DUCKDNS_DOMAIN_FOR_CHALLENGE", ""),

		ZeroSSLEnabled:    parseBool(getEnv("ZEROSSL_ENABLED", "false")),
		ZeroSSLEabKid:     getEnv("ZEROSSL_EAB_KID", ""),
		ZeroSSLEabHmacKey: getEnv("ZEROSSL_EAB_HMAC_KEY", ""),

		CertsBasePath:    getEnv("CERTS_BASE_PATH", "/data/config"),
		UID:              parseInt(getEnv("UID", "0"), 0, logger),
		GID:              parseInt(getEnv("GID", "0"), 0, logger),
		InternalHTTPPort: getEnv("INTERNAL_HTTP_PORT", "8080"),
	}
	cfg.AccountKeyDir = "accounts/default"

	if err := validateRequired(cfg); err != nil {
		return nil, err
	}
	logWarnings(cfg, logger)

	return cfg, nil
}

func validateRequired(cfg *Config) error {
	if cfg.DuckDNSToken == "" || cfg.DuckDNSToken == "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX" {
		return fmt.Errorf("DUCKDNS_TOKEN environment variable must be set and not default placeholder")
	}
	if cfg.DuckDNSDomainForChallenge == "" {
		return fmt.Errorf("DUCKDNS_DOMAIN_FOR_CHALLENGE environment variable must be set")
	}
	domainsStr := getEnv("LETSENCRYPT_DOMAIN", "")
	if domainsStr == "" {
		return fmt.Errorf("LETSENCRYPT_DOMAIN environment variable must be set")
	}
	cfg.LetsEncryptDomains = parseStringSlice(domainsStr)
	if len(cfg.LetsEncryptDomains) == 0 {
		return fmt.Errorf("LETSENCRYPT_DOMAIN must contain at least one domain")
	}
	return nil
}

func logWarnings(cfg *Config, logger Logger) {
	if logger == nil { return }

	if cfg.LetsEncryptEmail == "" { // This check is now more of a strong warning as LoadConfig ensures it's not empty
		logger.Println("WARNING: LETSENCRYPT_EMAIL is empty or using placeholder. This is required for ACME registration. Please set it for real notifications.")
	}
	if cfg.ZeroSSLEnabled && (cfg.ZeroSSLEabKid == "" || cfg.ZeroSSLEabHmacKey == "") {
		logger.Println("WARNING: ZeroSSL is enabled but ZEROSSL_EAB_KID or ZEROSSL_EAB_HMAC_KEY are not set. Account registration with ZeroSSL might fail for new accounts.")
	}
	for _, ca := range cfg.CAOrder {
		if strings.ToLower(ca) == "zerossl" && !cfg.ZeroSSLEnabled {
			logger.Printf("WARNING: 'zerossl' is in CA_ORDER, but ZEROSSL_ENABLED is false. ZeroSSL will be skipped.")
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
	return err == nil && b
}

func parseInt(str string, fallback int, logger Logger) int {
	i, err := strconv.Atoi(str)
	if err != nil {
		if logger != nil { 
			logger.Printf("WARNING: Config: Could not parse integer from '%s' for env var, using fallback %d. Error: %v", str, fallback, err)
		}
		return fallback
	}
	return i
}

func parseDuration(str string, fallback time.Duration, logger Logger) time.Duration {
	val, err := strconv.Atoi(str)
	if err != nil {
		if logger != nil {
			logger.Printf("WARNING: Config: Could not parse duration (hours) from '%s' for env var, using fallback %v. Error: %v", str, fallback, err)
		}
		return fallback
	}
	if val < 0 { 
		if logger != nil {
			logger.Printf("WARNING: Config: Negative duration (hours) from '%s' is invalid for env var, using fallback %v.", str, fallback)
		}
		return fallback
	}
	return time.Duration(val) * time.Hour
}

func parseStringSlice(str string) []string {
	if str == "" {
		return []string{}
	}
	parts := strings.Split(str, ",")
	var result []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
