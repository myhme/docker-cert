package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/registration"
)

// User implements the registration.User interface for ACME.
type User struct {
	Email        string
	Registration *registration.Resource
	privateKey   crypto.PrivateKey
	keyPath      string
	logger       Logger // Assuming Logger interface is defined/accessible
}

// NewUser creates or loads an ACME user.
// email can be empty if the user chooses not to provide one for registration.
func NewUser(email, keyPath string, logger Logger) (*User, error) {
	// Removed the check: if email == "" { return nil, fmt.Errorf("ACME user email cannot be empty") }
	// Lego library handles empty email for registration.

	if keyPath == "" {
		return nil, fmt.Errorf("ACME user key path cannot be empty")
	}
	if logger == nil {
		// Depending on how critical logging is here, either panic or handle.
		// For now, let's assume logger is always provided by the caller (NewManager).
		// If it could be nil, add a fallback or return an error.
		// return nil, fmt.Errorf("logger cannot be nil for ACME User")
		// For simplicity, we'll proceed, assuming logger is non-nil from NewManager.
		// A production app might have a default logger or stricter checks.
	}

	user := &User{
		Email:   email, // Email can now be an empty string
		keyPath: keyPath,
		logger:  logger,
	}

	keyDir := filepath.Dir(keyPath)
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory for ACME user key %s: %w", keyDir, err)
	}

	if _, err := os.Stat(keyPath); err == nil {
		keyBytes, errFileRead := os.ReadFile(keyPath)
		if errFileRead != nil {
			return nil, fmt.Errorf("failed to read existing ACME user key from %s: %w", keyPath, errFileRead)
		}
		privateKey, errParse := certcrypto.ParsePEMPrivateKey(keyBytes)
		if errParse != nil {
			return nil, fmt.Errorf("failed to parse existing ACME user key PEM from %s: %w", keyPath, errParse)
		}
		user.privateKey = privateKey
		if logger != nil { // Check logger before using
			logger.Printf("Loaded existing ACME user private key from %s", keyPath)
		}
	} else if os.IsNotExist(err) {
		privateKey, genErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if genErr != nil {
			return nil, fmt.Errorf("failed to generate new ACME user private key: %w", genErr)
		}
		user.privateKey = privateKey

		pemKey := certcrypto.PEMEncode(privateKey)
		if writeErr := os.WriteFile(keyPath, pemKey, 0600); writeErr != nil {
			return nil, fmt.Errorf("failed to save new ACME user private key to %s: %w", keyPath, writeErr)
		}
		if logger != nil { // Check logger before using
			logger.Printf("Generated and saved new ACME user private key to %s", keyPath)
		}
	} else {
		return nil, fmt.Errorf("failed to stat ACME user key file %s: %w", keyPath, err)
	}

	return user, nil
}

// GetEmail returns the user's email address.
func (u *User) GetEmail() string {
	return u.Email
}

// GetRegistration returns the user's ACME registration resource.
func (u *User) GetRegistration() *registration.Resource {
	return u.Registration
}

// GetPrivateKey returns the user's private key.
func (u *User) GetPrivateKey() crypto.PrivateKey {
	return u.privateKey
}
