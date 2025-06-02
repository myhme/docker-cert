// File: cmd/docker-cert/main.go
package main

import (
	"context"
	"log"
	"net/http" // Keep for http.ErrServerClosed
	"os"
	"os/signal"
	"syscall"
	"time"

	// Assuming these are your actual internal package paths
	"docker-cert/internal/acme"    // REPLACE docker-cert with your module path
	"docker-cert/internal/config"  // REPLACE
	"docker-cert/internal/httpapi" // REPLACE
	"docker-cert/internal/ipupdater" // REPLACE
	"docker-cert/internal/renewal" // REPLACE                               
	"docker-cert/internal/storage" // REPLACE
)

const (
	// This timeout is for the overall application shutdown process
	// Individual services (like HTTP server, schedulers) might have their own internal timeouts
	// for their specific shutdown steps, which should ideally be less than this.
	applicationShutdownTimeout = 15 * time.Second
)

func main() {
	// Initialize your application-specific logger
	// Using Lmicroseconds for more precise timing, Lshortfile for quick location of log origin.
	logger := log.New(os.Stdout, "DOCKER-CERT: ", log.LstdFlags|log.Lmicroseconds|log.Lshortfile)
	logger.Println("INFO: Starting ACME Certificate Manager...")

	// --- 1. Load Configuration ---
	appConfig, err := config.LoadConfig(logger) // Your LoadConfig should handle defaults
	if err != nil {
		logger.Fatalf("FATAL: Failed to load configuration: %v", err)
	}
	// It's good to log some key config values (but be careful with secrets)
	logger.Printf("INFO: Configuration loaded. CertsBasePath: %s, UID: %d, GID: %d, HTTP Port: %s",
		appConfig.CertsBasePath, appConfig.UID, appConfig.GID, appConfig.InternalHTTPPort)

	// --- 2. Prepare Storage ---
	if err := os.MkdirAll(appConfig.CertsBasePath, 0755); err != nil {
		logger.Fatalf("FATAL: Failed to create base certs directory %s: %v", appConfig.CertsBasePath, err)
	}
	if err := storage.ChownR(appConfig.CertsBasePath, appConfig.UID, appConfig.GID); err != nil {
		logger.Printf("WARN: Initial chown of %s failed: %v. Ensure permissions are correct, especially if not running as root.", appConfig.CertsBasePath, err)
	}

	// --- 3. Initialize Core Services ---
	acmeManager, err := acme.NewManager(appConfig, logger)
	if err != nil {
		logger.Fatalf("FATAL: Failed to create ACME manager: %v", err)
	}

	ipUpdateService := ipupdater.NewService(appConfig, logger)

	// Initialize HTTP API Server (this will include /healthz)
	// The httpapi.NewServer should configure routes, timeouts, and the listening address.
	httpAPIServer := httpapi.NewServer(appConfig, acmeManager, ipUpdateService, logger)

	// Declare scheduler variables for access in the shutdown sequence
	var renewalScheduler *renewal.Scheduler // Using var so it's nil if not initialized

	// --- 4. Perform Initial Tasks ---
	if appConfig.DuckDNSIPUpdateDomain != "" {
		logger.Println("INFO: Performing initial DuckDNS IP update check (non-blocking)...")
		go ipUpdateService.CheckAndPerformIPUpdate() // Assumes this is a non-blocking or quick operation
	}

	logger.Println("INFO: Performing initial certificate management run...")
	if err := acmeManager.ManageCertificates(); err != nil {
		logger.Printf("ERROR: Error during initial certificate management: %v. Application will continue and rely on scheduled renewals.", err)
	} else {
		logger.Println("INFO: Initial certificate management run completed successfully.")
	}

	// --- 5. Start Background Services (in goroutines) ---
	// Start HTTP API server (which includes health checks)
	go func() {
		logger.Printf("INFO: Attempting to start HTTP API server on internal port %s", appConfig.InternalHTTPPort)
		if startErr := httpAPIServer.Start(); startErr != nil && startErr != http.ErrServerClosed {
			// If the HTTP server, essential for healthchecks, fails to start, it's often fatal.
			logger.Fatalf("FATAL: Failed to start HTTP API server: %v", startErr)
		}
		logger.Println("INFO: HTTP API server has shut down.")
	}()

	// Start certificate renewal scheduler
	if appConfig.RenewalCheckInterval > 0 {
		renewalScheduler = renewal.NewScheduler(appConfig, acmeManager, logger)
		go renewalScheduler.Start()
		logger.Println("INFO: Certificate renewal scheduler started.")
	} else {
		logger.Println("INFO: Certificate renewal checks disabled (interval is zero or negative).")
	}

	// Start IP update scheduler
	if appConfig.DuckDNSIPUpdateDomain != "" && appConfig.DuckDNSIPUpdateInterval > 0 {
		go ipUpdateService.StartScheduler()
		logger.Println("INFO: Automatic DuckDNS IP update scheduler started.")
	} else {
		logger.Println("INFO: Automatic DuckDNS IP updates disabled (domain not set or interval is zero/negative).")
	}

	logger.Println("INFO: ACME Certificate Manager is fully initialized and running. Press Ctrl+C or send SIGTERM to exit.")

	// --- 6. Wait for Shutdown Signal ---
	quitChannel := make(chan os.Signal, 1)
	signal.Notify(quitChannel, syscall.SIGINT, syscall.SIGTERM)
	receivedSignal := <-quitChannel // Block until a signal is received

	logger.Printf("INFO: Received signal: %s. Initiating graceful shutdown of ACME Certificate Manager...", receivedSignal)

	// --- 7. Perform Graceful Shutdown ---
	// Create a context with a timeout for the entire shutdown sequence.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), applicationShutdownTimeout)
	defer cancelShutdown() // Important to release resources if shutdown completes early or panics

	// Shutdown HTTP API server first (to stop accepting new requests)
	logger.Println("INFO: Attempting to gracefully shut down HTTP API server...")
	if err := httpAPIServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("ERROR: Error during HTTP API server graceful shutdown: %v", err)
	} else {
		logger.Println("INFO: HTTP API server shut down gracefully.")
	}

	// Shutdown background schedulers
	if renewalScheduler != nil { // Check if it was initialized and started
		logger.Println("INFO: Attempting to gracefully shut down renewal scheduler...")
		renewalScheduler.Stop(shutdownCtx) // Assuming renewal.Scheduler has a Stop(context.Context) method
	}

	if appConfig.DuckDNSIPUpdateDomain != "" && appConfig.DuckDNSIPUpdateInterval > 0 {
		logger.Println("INFO: Attempting to gracefully shut down IP update scheduler...")
		ipUpdateService.StopScheduler(shutdownCtx) // Assuming ipupdater.Service has a StopScheduler(context.Context) method
	}

	// Add any other service/goroutine shutdowns here, ensuring they respect shutdownCtx

	logger.Println("INFO: All services have been instructed to shut down.")
	logger.Println("INFO: ACME Certificate Manager shut down gracefully.")
}