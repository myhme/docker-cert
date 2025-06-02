package acme

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/duckdns"
	"github.com/go-acme/lego/v4/registration"

	"docker-cert/internal/config"
	"docker-cert/internal/storage"
)

const (
	letsEncryptProdDirDefault = "https://acme-v02.api.letsencrypt.org/directory"
	letsEncryptStagDirDefault = "https://acme-staging-v02.api.letsencrypt.org/directory"
	defaultRenewDaysBefore    = 30
	caNameLetsEncrypt         = "letsencrypt"
)

type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
	Fatalf(format string, v ...interface{})
}

type Manager struct {
	config        *config.Config
	user          *User
	logger        Logger
	lastError     error // Store the last significant error encountered
	lastSuccess   time.Time // Store the timestamp of the last successful operation
	isInitialized bool
}

// Status represents the health status of a component.
type ComponentStatus struct {
	Status  string `json:"status"` // e.g., "ok", "error", "initializing"
	Message string `json:"message"`
}

func NewManager(cfg *config.Config, logger Logger) (*Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	accountKeyPath := filepath.Join(cfg.CertsBasePath, cfg.AccountKeyDir, "account.key")
	acmeUser, err := NewUser(cfg.LetsEncryptEmail, accountKeyPath, logger)
	if err != nil {
		return nil, fmt.Errorf("init ACME user: %w", err)
	}
	return &Manager{
		config:        cfg,
		user:          acmeUser,
		logger:        logger,
		isInitialized: true, // Considered initialized if user is created
	}, nil
}

func (m *Manager) createLegoClient() (*lego.Client, error) {
	var acmeDirURL string
	legoClientConfig := lego.NewConfig(m.user)
	acmeDirURL = letsEncryptProdDirDefault
	if m.config.Testing {
		acmeDirURL = letsEncryptStagDirDefault
	}
	legoClientConfig.CADirURL = acmeDirURL
	legoClientConfig.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(legoClientConfig)
	if err != nil {
		m.lastError = err
		return nil, fmt.Errorf("create lego client for %s (%s): %w", caNameLetsEncrypt, acmeDirURL, err)
	}

	duckDNSProvider, err := duckdns.NewDNSProvider() // Relies on DUCKDNS_TOKEN env var
	if err != nil {
		m.lastError = err
		return nil, fmt.Errorf("create official DuckDNS provider for %s: %w. Ensure DUCKDNS_TOKEN env var is set.", caNameLetsEncrypt, err)
	}

	// Set DNS01 provider options.
	err = client.Challenge.SetDNS01Provider(duckDNSProvider,
		dns01.AddRecursiveNameservers([]string{"1.1.1.1:53", "1.0.0.1:53", "8.8.8.8:53", "8.8.4.4:53"}),
		dns01.AddDNSTimeout(15*time.Minute),
		dns01.DisableAuthoritativeNssPropagationRequirement(), // Corrected DNS01 option
	)
	if err != nil {
		m.lastError = err
		return nil, fmt.Errorf("set DNS01 provider options for %s: %w", caNameLetsEncrypt, err)
	}

	m.user.Registration = nil // Reset before attempting registration
	m.logger.Printf("[%s] Attempting ACME user registration for %s...", caNameLetsEncrypt, m.user.GetEmail())
	regOpts := registration.RegisterOptions{TermsOfServiceAgreed: true}
	reg, regErr := client.Registration.Register(regOpts)
	if regErr != nil {
		m.lastError = regErr
		return nil, fmt.Errorf("[%s] ACME user registration for %s: %w", caNameLetsEncrypt, m.user.GetEmail(), regErr)
	}
	m.user.Registration = reg
	m.logger.Printf("[%s] ACME user %s registered/verified. URI: %s", caNameLetsEncrypt, m.user.GetEmail(), reg.URI)
	return client, nil
}

func (m *Manager) ManageCertificates() error {
	m.lastError = nil // Reset last error at the beginning of an operation
	certificateObtainedOrRenewed := false
	if len(m.config.LetsEncryptDomains) == 0 {
		err := fmt.Errorf("no domains configured for certificate issuance")
		m.lastError = err
		return err
	}

	targetDomains := m.determineTargetDomains()
	if len(targetDomains) == 0 {
		err := fmt.Errorf("no effective target domains derived from configuration")
		m.lastError = err
		return err
	}
	primaryDomainForPath := targetDomains[0]

	if err := m.checkAndLogExistingCertStatus(primaryDomainForPath); err != nil {
		if err.Error() == "cert_ok_no_renewal_needed" {
			m.lastSuccess = time.Now() // Consider this a successful state for health check
			return nil
		}
		// Other errors during check are warnings, proceed to attempt renewal/issuance
	}

	m.logger.Printf("Attempting to obtain/renew certificate using CA: %s", caNameLetsEncrypt)
	client, clientErr := m.createLegoClient()
	if clientErr != nil {
		m.logger.Printf("ERROR: Failed to create lego client for CA %s: %v", caNameLetsEncrypt, clientErr)
		m.lastError = clientErr // Already set in createLegoClient
		return clientErr
	}

	certResource, opErr := m.obtainOrRenewWithClient(client, targetDomains)
	if opErr != nil {
		m.lastError = opErr // Already set in obtainOrRenewWithClient
		// No return here, try to save even if there was an error (though certResource might be nil)
	} else {
		saveErr := storage.SaveCertificateResource(certResource, primaryDomainForPath, m.config, m.logger)
		if saveErr != nil {
			m.logger.Printf("ERROR: [%s] Failed to save certificate: %v", caNameLetsEncrypt, saveErr)
			m.lastError = saveErr
		} else {
			m.logger.Printf("[%s] Certificate saved successfully for %s.", caNameLetsEncrypt, primaryDomainForPath)
			certificateObtainedOrRenewed = true
			m.lastSuccess = time.Now()
			m.lastError = nil // Clear error on full success
		}
	}


	if !certificateObtainedOrRenewed {
		errMsg := "Failed to obtain/renew certificate."
		finalErr := m.lastError
		if finalErr == nil { // If no specific error was recorded but still failed
			finalErr = fmt.Errorf("certificate operation for %s did not succeed for domains: %v (no specific error recorded)", caNameLetsEncrypt, targetDomains)
		}
		m.logger.Printf("ERROR: %s Last error: %v", errMsg, finalErr)
		return fmt.Errorf("certificate operation failed: %w", finalErr)
	}
	return nil
}

func (m *Manager) determineTargetDomains() []string {
	var targetDomains []string
	if m.config.UseWildcard {
		baseDomain := strings.TrimPrefix(m.config.LetsEncryptDomains[0], "*.")
		wildcardDomain := "*." + baseDomain
		currentDomainsSet := make(map[string]bool)
		targetDomains = append(targetDomains, wildcardDomain)
		currentDomainsSet[wildcardDomain] = true
		if baseDomain != wildcardDomain && !currentDomainsSet[baseDomain] { // Add base domain if not already wildcard
			targetDomains = append(targetDomains, baseDomain)
			currentDomainsSet[baseDomain] = true
		}
		// Add any other explicitly listed domains if they are not already covered
		for _, d := range m.config.LetsEncryptDomains {
			if !currentDomainsSet[d] {
				targetDomains = append(targetDomains, d)
				currentDomainsSet[d] = true
			}
		}
		m.logger.Printf("Wildcard mode. Effective target domains for ACME request: %v", targetDomains)
	} else {
		currentDomainsSet := make(map[string]bool)
		for _, d := range m.config.LetsEncryptDomains {
			if !currentDomainsSet[d] {
				targetDomains = append(targetDomains, d)
				currentDomainsSet[d] = true
			}
		}
		m.logger.Printf("Non-wildcard mode. Target domains: %v", targetDomains)
	}
	return targetDomains
}

func (m *Manager) checkAndLogExistingCertStatus(primaryDomainForPath string) error {
	liveCertFilePath := storage.GetLiveCertificatePath(m.config.CertsBasePath, primaryDomainForPath, "cert.pem")
	existingCertLeaf, errLoad := m.loadExistingCertificateLeaf(liveCertFilePath)
	if errLoad == nil && existingCertLeaf != nil {
		renewalThreshold := time.Duration(defaultRenewDaysBefore) * 24 * time.Hour
		if time.Now().After(existingCertLeaf.NotAfter.Add(-renewalThreshold)) {
			m.logger.Printf("Existing cert for %s (SANs: %v) is near expiry (expires %s). Attempting renewal.", existingCertLeaf.Subject.CommonName, existingCertLeaf.DNSNames, existingCertLeaf.NotAfter.Format(time.RFC3339))
		} else {
			m.logger.Printf("Certificate for %s (SANs: %v) is current and not yet due for renewal. Expires: %s", existingCertLeaf.Subject.CommonName, existingCertLeaf.DNSNames, existingCertLeaf.NotAfter.Format(time.RFC3339))
			return fmt.Errorf("cert_ok_no_renewal_needed")
		}
	} else {
		if errLoad != nil && !os.IsNotExist(errLoad) { // Log if error is not "file not found"
			m.logger.Printf("WARN: Could not load or parse existing certificate at %s: %v. Will attempt to obtain a new certificate.", liveCertFilePath, errLoad)
		} else { // File does not exist
			m.logger.Printf("No existing certificate found at %s. Attempting to obtain a new certificate.", liveCertFilePath)
		}
	}
	return nil // Proceed to obtain/renew
}

func (m *Manager) obtainOrRenewWithClient(client *lego.Client, targetDomains []string) (*certificate.Resource, error) {
	m.logger.Printf("[%s] Attempting to obtain/renew certificate for domains: %v", caNameLetsEncrypt, targetDomains)
	request := certificate.ObtainRequest{
		Domains: targetDomains,
		Bundle:  true, // Include issuer certificate in cert.pem
	}
	if m.config.PreferredChain != "default" && m.config.PreferredChain != "" {
		request.PreferredChain = m.config.PreferredChain
	}
	certResource, opErr := client.Certificate.Obtain(request)
	if opErr != nil {
		m.logger.Printf("ERROR: [%s] Failed to obtain/renew certificate for %v: %v", caNameLetsEncrypt, targetDomains, opErr)
		m.lastError = opErr // Store the error
		return nil, opErr
	}
	m.logger.Printf("[%s] Successfully obtained/renewed certificate for %v. Certificate URL: %s", caNameLetsEncrypt, targetDomains, certResource.CertURL)
	return certResource, nil
}

func (m *Manager) loadExistingCertificateLeaf(certPath string) (*x509.Certificate, error) {
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		return nil, err // File does not exist
	}
	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate file %s: %w", certPath, err)
	}
	certs, err := certcrypto.ParsePEMBundle(certBytes) // lego's helper to parse PEM bundle
	if err != nil || len(certs) == 0 {
		return nil, fmt.Errorf("failed to parse PEM bundle from %s or bundle is empty: %w", certPath, err)
	}
	return certs[0], nil // Return the leaf certificate
}

// GetStatus returns the current operational status of the ACME Manager.
func (m *Manager) GetStatus() ComponentStatus {
	if !m.isInitialized || m.user == nil {
		return ComponentStatus{Status: "error", Message: "ACME manager not initialized"}
	}
	if m.lastError != nil {
		return ComponentStatus{Status: "error", Message: fmt.Sprintf("Last operation failed: %v", m.lastError)}
	}
	if m.lastSuccess.IsZero() {
		return ComponentStatus{Status: "initializing", Message: "ACME manager initialized, no successful operation yet"}
	}
	return ComponentStatus{Status: "ok", Message: fmt.Sprintf("ACME manager operational. Last success: %s", m.lastSuccess.Format(time.RFC3339))}
}
