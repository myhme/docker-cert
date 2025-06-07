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
	// Remember to replace "docker-cert" with your actual Go module path.
	"docker-cert/internal/acme"
	"docker-cert/internal/config"
	"docker-cert/internal/httpapi"
	"docker-cert/internal/ipupdater"
	"docker-cert/internal/renewal"
)

const (
	// This timeout is for the overall application shutdown process.
	applicationShutdownTimeout = 15 * time.Second
)

func main() {
	// Initialize your application-specific logger
	logger := log.New(os.Stdout, "DOCKER-CERT: ", log.LstdFlags|log.Lmicroseconds|log.Lshortfile)
	logger.Println("INFO: Starting ACME Certificate Manager...")

	// --- 1. Load Configuration ---
	appConfig, err := config.LoadConfig(logger)
	if err != nil {
		logger.Fatalf("FATAL: Failed to load configuration: %v", err)
	}
	// Log key config values (but be careful with secrets)
	logger.Printf("INFO: Configuration loaded. CertsBasePath: %s, HTTP Port: %s",
		appConfig.CertsBasePath, appConfig.InternalHTTPPort)

	// --- 2. Prepare Storage (REMOVED) ---
	// The creation and ownership of the CertsBasePath are now handled in the Dockerfile.
	// This makes the container image responsible for setting up the environment,
	// and the application code doesn't need root-like permissions (chown).
	/*
		if err := os.MkdirAll(appConfig.CertsBasePath, 0755); err != nil {
			logger.Fatalf("FATAL: Failed to create base certs directory %s: %v", appConfig.CertsBasePath, err)
		}
		if err := storage.ChownR(appConfig.CertsBasePath, appConfig.UID, appConfig.GID); err != nil {
			logger.Printf("WARN: Initial chown of %s failed: %v.", appConfig.CertsBasePath, err)
		}
	*/

	// --- 3. Initialize Core Services ---
	acmeManager, err := acme.NewManager(appConfig, logger)
	if err != nil {
		logger.Fatalf("FATAL: Failed to create ACME manager: %v", err)
	}

	ipUpdateService := ipupdater.NewService(appConfig, logger)

	// Initialize HTTP API Server (this will include /healthz)
	httpAPIServer := httpapi.NewServer(appConfig, acmeManager, ipUpdateService, logger)

	// Declare scheduler for access in shutdown sequence
	var renewalScheduler *renewal.Scheduler

	// --- 4. Perform Initial Tasks ---
	if appConfig.DuckDNSIPUpdateDomain != "" {
		logger.Println("INFO: Performing initial DuckDNS IP update check (non-blocking)...")
		go ipUpdateService.CheckAndPerformIPUpdate()
	}

	logger.Println("INFO: Performing initial certificate management run...")
	if err := acmeManager.ManageCertificates(); err != nil {
		logger.Printf("ERROR: Error during initial certificate management: %v. Will rely on scheduled renewals.", err)
	} else {
		logger.Println("INFO: Initial certificate management run completed successfully.")
	}

	// --- 5. Start Background Services ---
	// Start HTTP API server
	go func() {
		logger.Printf("INFO: Attempting to start HTTP API server on internal port %s", appConfig.InternalHTTPPort)
		if startErr := httpAPIServer.Start(); startErr != nil && startErr != http.ErrServerClosed {
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
		logger.Println("INFO: Certificate renewal checks disabled.")
	}

	// Start IP update scheduler
	if appConfig.DuckDNSIPUpdateDomain != "" && appConfig.DuckDNSIPUpdateInterval > 0 {
		go ipUpdateService.StartScheduler()
		logger.Println("INFO: Automatic DuckDNS IP update scheduler started.")
	} else {
		logger.Println("INFO: Automatic DuckDNS IP updates disabled.")
	}

	logger.Println("INFO: ACME Certificate Manager is fully initialized and running.")

	// --- 6. Wait for Shutdown Signal ---
	quitChannel := make(chan os.Signal, 1)
	signal.Notify(quitChannel, syscall.SIGINT, syscall.SIGTERM)
	receivedSignal := <-quitChannel

	logger.Printf("INFO: Received signal: %s. Initiating graceful shutdown...", receivedSignal)

	// --- 7. Perform Graceful Shutdown ---
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), applicationShutdownTimeout)
	defer cancelShutdown()

	// Shutdown HTTP API server
	logger.Println("INFO: Shutting down HTTP API server...")
	if err := httpAPIServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("ERROR: Error during HTTP API server graceful shutdown: %v", err)
	} else {
		logger.Println("INFO: HTTP API server shut down gracefully.")
	}

	// Shutdown schedulers
	if renewalScheduler != nil {
		logger.Println("INFO: Shutting down renewal scheduler...")
		renewalScheduler.Stop(shutdownCtx)
	}

	if appConfig.DuckDNSIPUpdateDomain != "" && appConfig.DuckDNSIPUpdateInterval > 0 {
		logger.Println("INFO: Shutting down IP update scheduler...")
		ipUpdateService.StopScheduler(shutdownCtx)
	}

	logger.Println("INFO: ACME Certificate Manager shut down gracefully.")
}
