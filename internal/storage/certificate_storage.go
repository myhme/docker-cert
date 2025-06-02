package storage

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"docker-cert/internal/config" // <-- Updated import path
)

// Logger interface for dependency injection.
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

// SanitizeDomainForPath replaces characters unsuitable for directory names.
func SanitizeDomainForPath(domain string) string {
	sanitized := strings.ReplaceAll(domain, "*.", "_wildcard.")
	return sanitized
}

// GetLiveCertificatePath constructs the path to a specific file in the live directory.
func GetLiveCertificatePath(certsBasePath, primaryDomain, fileName string) string {
    domainFolderName := SanitizeDomainForPath(primaryDomain)
    domainSpecificBasePath := filepath.Join(certsBasePath, domainFolderName)
    return filepath.Join(domainSpecificBasePath, "live", fileName)
}

// SaveCertificateResource saves the certificate, private key, and chain to disk.
func SaveCertificateResource(certRes *certificate.Resource, primaryDomainForPath string, cfg *config.Config, logger Logger) error {
	if certRes == nil {
		return fmt.Errorf("certificate resource is nil, cannot save")
	}
	if logger == nil {
		panic("logger cannot be nil for SaveCertificateResource")
	}

	domainFolderName := SanitizeDomainForPath(primaryDomainForPath)
	domainSpecificBasePath := filepath.Join(cfg.CertsBasePath, domainFolderName)

	livePath := filepath.Join(domainSpecificBasePath, "live")
	archivePath := filepath.Join(domainSpecificBasePath, "archive")

	logger.Printf("Ensuring directory structure for domain %s: base path %s", primaryDomainForPath, domainSpecificBasePath)
	if err := os.MkdirAll(livePath, 0755); err != nil {
		return fmt.Errorf("failed to create live directory %s: %w", livePath, err)
	}
	if err := os.MkdirAll(archivePath, 0755); err != nil {
		return fmt.Errorf("failed to create archive directory %s: %w", archivePath, err)
	}

	version := time.Now().Format("20060102-150405")

	var fullChainBytes []byte
	if certRes.Certificate != nil {
		fullChainBytes = append(fullChainBytes, certRes.Certificate...)
	}
	if certRes.IssuerCertificate != nil {
		fullChainBytes = append(fullChainBytes, certRes.IssuerCertificate...)
	}

	filesToSave := map[string][]byte{
		fmt.Sprintf("cert-%s.pem", version):      certRes.Certificate,
		fmt.Sprintf("privkey-%s.pem", version):   certRes.PrivateKey,
		fmt.Sprintf("chain-%s.pem", version):     certRes.IssuerCertificate,
		fmt.Sprintf("fullchain-%s.pem", version): fullChainBytes,
	}

	for fileName, content := range filesToSave {
		if len(content) == 0 {
			logger.Printf("Skipping save for archive/%s as content is empty/nil", fileName)
			continue
		}
		filePath := filepath.Join(archivePath, fileName)
		perm := os.FileMode(0600)
		if strings.Contains(fileName, "privkey") {
			perm = 0600
		} else {
			perm = 0644
		}

		err := os.WriteFile(filePath, content, perm)
		if err != nil {
			return fmt.Errorf("failed to write %s (permissions %o): %w", filePath, perm, err)
		}
		logger.Printf("Saved %s (permissions %o)", filePath, perm)
	}

	liveSymlinks := map[string]string{
		"cert.pem":      fmt.Sprintf("cert-%s.pem", version),
		"privkey.pem":   fmt.Sprintf("privkey-%s.pem", version),
		"chain.pem":     fmt.Sprintf("chain-%s.pem", version),
		"fullchain.pem": fmt.Sprintf("fullchain-%s.pem", version),
	}

	for symlinkName, archiveFileName := range liveSymlinks {
		symlinkPath := filepath.Join(livePath, symlinkName)
		targetPath := filepath.Join("..", "archive", archiveFileName)

		if _, err := os.Stat(filepath.Join(archivePath, archiveFileName)); os.IsNotExist(err) {
			logger.Printf("Skipping symlink for %s as target archive file %s does not exist.", symlinkName, archiveFileName)
			if _, lerr := os.Lstat(symlinkPath); lerr == nil {
				if rerr := os.Remove(symlinkPath); rerr != nil {
					logger.Printf("Warning: Failed to remove old symlink %s: %v", symlinkPath, rerr)
				}
			}
			continue
		}

		if _, err := os.Lstat(symlinkPath); err == nil {
			if err := os.Remove(symlinkPath); err != nil {
				logger.Printf("Warning: Failed to remove existing file/symlink at %s: %v. Attempting to proceed.", symlinkPath, err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat existing symlink path %s: %w", symlinkPath, err)
		}

		if err := os.Symlink(targetPath, symlinkPath); err != nil {
			return fmt.Errorf("failed to create symlink %s -> %s: %w", symlinkPath, targetPath, err)
		}
		logger.Printf("Created symlink %s -> %s", symlinkPath, targetPath)
	}
	
	logger.Printf("Applying ownership (UID: %d, GID: %d) to %s", cfg.UID, cfg.GID, domainSpecificBasePath)
	if err := ChownR(domainSpecificBasePath, cfg.UID, cfg.GID); err != nil {
		logger.Printf("WARNING: Failed to set ownership on %s: %v. Check permissions and UID/GID validity. Ensure the application has privileges to chown.", domainSpecificBasePath, err)
	}

	return nil
}

// ChownR recursively changes ownership of a path.
func ChownR(path string, uid, gid int) error {
	isRoot := os.Geteuid() == 0

	if uid != 0 { // Only lookup if not root user
		_, err := user.LookupId(strconv.Itoa(uid))
		if err != nil && isRoot { // If root can't find user, it's an issue
			// return fmt.Errorf("ChownR: UID %d lookup failed as root: %w. User may not exist.", uid, err)
		} else if err != nil && !isRoot {
			// log.Printf("ChownR: Warning - UID %d lookup failed: %v. Chown might fail.", uid, err)
		}
	}
	if gid != 0 { // Only lookup if not root group
		_, err := user.LookupGroupId(strconv.Itoa(gid))
		if err != nil && isRoot {
			// return fmt.Errorf("ChownR: GID %d lookup failed as root: %w. Group may not exist.", gid, err)
		} else if err != nil && !isRoot {
			// log.Printf("ChownR: Warning - GID %d lookup failed: %v. Chown might fail.", gid, err)
		}
	}

	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Attempt chown. This may fail if not running as root or without sufficient privileges.
		if chownErr := os.Chown(name, uid, gid); chownErr != nil {
			// Suppress error spam if not root and it's a permission issue, as it's expected.
			// if !isRoot && os.IsPermission(chownErr) {
				// return nil // Silently ignore permission errors if not root
			// }
			// For now, let's just return the error to be logged by the caller.
			return fmt.Errorf("failed to chown %s to UID %d GID %d: %w", name, uid, gid, chownErr)
		}
		return nil
	})
}
