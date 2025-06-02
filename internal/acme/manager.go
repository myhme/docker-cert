package acme

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certificate" // Corrected import path
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/lego" // This package provides lego.ExternalAccountBinding
	"github.com/go-acme/lego/v4/registration" // This package provides registration.RegisterOptions

	"docker-cert/internal/config" // Assuming go.mod is "module docker-cert"
	"docker-cert/internal/dns"
	"docker-cert/internal/storage"
)

const (
	letsEncryptProdDirDefault = "https://acme-v02.api.letsencrypt.org/directory"
	letsEncryptStagDirDefault = "https://acme-staging-v02.api.letsencrypt.org/directory"
	zeroSSLProdDirDefault     = "https://acme.zerossl.com/v2/DV90"
	defaultRenewDaysBefore    = 30 // Renew if certificate expires within this many days
)

// Logger interface for dependency injection.
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
	Fatalf(format string, v ...interface{})
}

// Manager handles ACME interactions.
type Manager struct {
	config *config.Config
	user   *User
	logger Logger
}

// NewManager creates a new ACME manager.
func NewManager(cfg *config.Config, logger Logger) (*Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil for ACME Manager")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil for ACME Manager")
	}

	accountKeyPath := filepath.Join(cfg.CertsBasePath, cfg.AccountKeyDir, "account.key")
	acmeUser, err := NewUser(cfg.LetsEncryptEmail, accountKeyPath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ACME user: %w", err)
	}

	return &Manager{
		config: cfg,
		user:   acmeUser,
		logger: logger,
	}, nil
}

func (m *Manager) createLegoClient(caName string) (*lego.Client, error) {
	var acmeDirURL string
	legoClientConfig := lego.NewConfig(m.user)

	caNameLower := strings.ToLower(caName)
	switch caNameLower {
	case "letsencrypt":
		acmeDirURL = letsEncryptProdDirDefault
		if m.config.Testing {
			acmeDirURL = letsEncryptStagDirDefault
		}
	case "zerossl":
		if !m.config.ZeroSSLEnabled {
			return nil, fmt.Errorf("ZeroSSL is selected CA but not enabled (ZEROSSL_ENABLED=false)")
		}
		acmeDirURL = zeroSSLProdDirDefault
	default:
		return nil, fmt.Errorf("unknown certificate authority: %s", caName)
	}
	legoClientConfig.CADirURL = acmeDirURL
	legoClientConfig.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(legoClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create lego client for %s (%s): %w", caName, acmeDirURL, err)
	}

	duckDNSProvider, err := dns.NewDuckDNSProvider(m.config.DuckDNSToken, m.config.DuckDNSDomainForChallenge, m.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create DuckDNS provider for %s: %w", caName, err)
	}
	err = client.Challenge.SetDNS01Provider(duckDNSProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to set DNS01 provider for %s: %w", caName, err)
	}

	m.user.Registration = nil 
	m.logger.Printf("[%s] Attempting ACME user registration or loading existing for %s...", caName, m.user.GetEmail())
	regOpts := registration.RegisterOptions{TermsOfServiceAgreed: true}

	if caNameLower == "zerossl" && m.config.ZeroSSLEnabled {
		if m.config.ZeroSSLEabKid != "" && m.config.ZeroSSLEabHmacKey != "" {
			m.logger.Printf("[%s] Using EAB credentials for ZeroSSL registration.", caName)
			// The following lines for ExternalAccountBinding are CORRECT for lego/v4 v4.17.0 and v4.23.1.
			// If you are still getting "undefined" errors for `regOpts.ExternalAccountBinding` or `lego.ExternalAccountBinding`:
			// 1. VERIFY `go.mod`: Ensure it has `require github.com/go-acme/lego/v4 v4.23.1` (or your target v4 version).
			// 2. CLEAN MODULE CACHE: Run `go clean -modcache` in your terminal.
			// 3. TIDY DEPENDENCIES: Run `go mod tidy`.
			// 4. CHECK GO VERSION: Ensure your Go compiler version is compatible (e.g., Go 1.18+ for lego/v4).
			// 5. DOCKER BUILD CONTEXT: Ensure build stage correctly copies `go.mod`/`go.sum` BEFORE `go mod download`/`go build`.
			//    Try `docker build --no-cache ...` once.
			regOpts.ExternalAccountBinding = &lego.ExternalAccountBinding{ // This type comes from "github.com/go-acme/lego/v4/lego"
				KID:         m.config.ZeroSSLEabKid,
				HMACEncoded: m.config.ZeroSSLEabHmacKey, // Must be Base64 URL Encoded
			}
		} else {
			m.logger.Printf("WARNING: [%s] Attempting ZeroSSL registration without EAB KID/HMAC. This may fail for new accounts.", caName)
		}
	}

	reg, regErr := client.Registration.Register(regOpts)
	if regErr != nil {
		return nil, fmt.Errorf("[%s] ACME user registration for %s failed: %w", caName, m.user.GetEmail(), regErr)
	}
	m.user.Registration = reg
	m.logger.Printf("[%s] ACME user %s registered/verified. URI: %s", caName, m.user.GetEmail(), reg.URI)

	return client, nil
}

// ManageCertificates attempts to obtain or renew certificates based on the configuration.
func (m *Manager) ManageCertificates() error {
	var lastError error
	certificateObtainedOrRenewed := false

	var targetDomains []string
	if len(m.config.LetsEncryptDomains) == 0 {
		return fmt.Errorf("no domains configured for certificate issuance")
	}

	if m.config.UseWildcard {
		baseDomain := strings.TrimPrefix(m.config.LetsEncryptDomains[0], "*.")
		wildcardDomain := "*." + baseDomain
		
		currentDomainsSet := make(map[string]bool)
		targetDomains = append(targetDomains, wildcardDomain)
		currentDomainsSet[wildcardDomain] = true

		if baseDomain != wildcardDomain && !currentDomainsSet[baseDomain] {
			targetDomains = append(targetDomains, baseDomain)
			currentDomainsSet[baseDomain] = true
		}
		
		for _, d := range m.config.LetsEncryptDomains {
			if !currentDomainsSet[d] {
				targetDomains = append(targetDomains, d)
				currentDomainsSet[d] = true
			}
		}
		m.logger.Printf("Wildcard mode enabled. Final target domains for certificate: %v", targetDomains)
	} else {
		currentDomainsSet := make(map[string]bool)
		for _, d := range m.config.LetsEncryptDomains {
			if !currentDomainsSet[d] {
				targetDomains = append(targetDomains, d)
				currentDomainsSet[d] = true
			}
		}
		m.logger.Printf("Non-wildcard mode. Target domains for certificate: %v", targetDomains)
	}
	
	if len(targetDomains) == 0 {
		return fmt.Errorf("no effective target domains after processing for certificate issuance")
	}
	primaryDomainForPath := targetDomains[0]

	liveCertFilePath := storage.GetLiveCertificatePath(m.config.CertsBasePath, primaryDomainForPath, "cert.pem")
	existingCertLeaf, errLoad := m.loadExistingCertificateLeaf(liveCertFilePath)

	if errLoad == nil && existingCertLeaf != nil {
		renewalThreshold := time.Duration(defaultRenewDaysBefore) * 24 * time.Hour
		if time.Now().After(existingCertLeaf.NotAfter.Add(-renewalThreshold)) {
			m.logger.Printf("Existing certificate for %s (and SANs) is nearing expiry (expires %s) or is already expired. Attempting renewal.",
				existingCertLeaf.Subject.CommonName, existingCertLeaf.NotAfter.Format(time.RFC3339))
		} else {
			m.logger.Printf("Certificate for %s (and SANs) is current and not yet due for renewal. Expires: %s",
				existingCertLeaf.Subject.CommonName, existingCertLeaf.NotAfter.Format(time.RFC3339))
			return nil 
		}
	} else {
		if errLoad != nil && !os.IsNotExist(errLoad) {
			m.logger.Printf("WARNING: Could not load or parse existing certificate at %s: %v. Will attempt to obtain a new one.", liveCertFilePath, errLoad)
		} else {
			m.logger.Printf("No existing valid certificate found at %s. Attempting to obtain a new one.", liveCertFilePath)
		}
	}

	for _, caName := range m.config.CAOrder {
		caNameLower := strings.ToLower(caName)
		if caNameLower == "zerossl" && !m.config.ZeroSSLEnabled {
			m.logger.Printf("Skipping CA '%s' as ZEROSSL_ENABLED is false.", caName)
			continue
		}

		m.logger.Printf("Attempting to obtain/renew certificate using CA: %s", caName)
		client, clientErr := m.createLegoClient(caName)
		if clientErr != nil {
			m.logger.Printf("ERROR: Failed to create lego client for CA %s: %v", caName, clientErr)
			lastError = clientErr
			continue
		}

		var certResource *certificate.Resource
		var opErr error
		
		m.logger.Printf("[%s] Attempting to obtain/renew certificate for domains: %v", caName, targetDomains)
		request := certificate.ObtainRequest{
			Domains:        targetDomains,
			Bundle:         true, 
			PreferredChain: m.config.PreferredChain,
		}
		certResource, opErr = client.Certificate.Obtain(request)

		if opErr != nil {
			m.logger.Printf("ERROR: [%s] Failed to obtain/renew certificate for domains %v: %v", caName, targetDomains, opErr)
			lastError = opErr
			continue 
		}

		m.logger.Printf("[%s] Successfully obtained/renewed certificate for domains %v. Certificate URL: %s", caName, targetDomains, certResource.CertURL)
		
		saveErr := storage.SaveCertificateResource(certResource, primaryDomainForPath, m.config, m.logger)
		if saveErr != nil {
			m.logger.Printf("ERROR: [%s] Failed to save certificate: %v", caName, saveErr)
			lastError = saveErr
			continue
		}

		m.logger.Printf("[%s] Certificate saved successfully for %s.", caName, primaryDomainForPath)
		certificateObtainedOrRenewed = true
		lastError = nil 
		break           
	}

	if !certificateObtainedOrRenewed {
		finalErrorMsg := "Failed to obtain/renew certificate from all configured CAs."
		if lastError != nil {
			finalErrorMsg = fmt.Sprintf("%s Last error: %v", finalErrorMsg, lastError)
		} else { 
			lastError = fmt.Errorf("no CAs were successfully processed, and no specific error was recorded (target domains: %v)", targetDomains)
		}
		m.logger.Printf("ERROR: %s", finalErrorMsg)
		return fmt.Errorf("all CAs failed: %w", lastError)
	}

	return nil
}

func (m *Manager) loadExistingCertificateLeaf(certPath string) (*x509.Certificate, error) {
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		return nil, err 
	}

	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate file %s: %w", certPath, err)
	}

	certs, err := certcrypto.ParsePEMBundle(certBytes)
	if err != nil || len(certs) == 0 {
		return nil, fmt.Errorf("failed to parse PEM bundle from %s or bundle is empty: %w", certPath, err)
	}
	return certs[0], nil
}
