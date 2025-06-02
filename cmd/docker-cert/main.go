// File: cmd/docker-cert/main.go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"docker-cert/internal/acme"    // Your existing import
	"docker-cert/internal/config"  // Your existing import
	"docker-cert/internal/httpapi" // Your existing import - THIS IS KEY FOR /healthz
	"docker-cert/internal/ipupdater" // Your existing import
	"docker-cert/internal/renewal" // Your existing import
	"docker-cert/internal/storage" // Your existing import
)

func main() {
	// Using your established logger
	logger := log.New(os.Stdout, "DOCKER-CERT: ", log.LstdFlags|log.Lshortfile)
	logger.Println("Starting ACME Certificate Manager...")

	appConfig, err := config.LoadConfig(logger)
	if err != nil {
		logger.Fatalf("Failed to load configuration: %v", err)
	}

	// Create base certs directory if it doesn't exist
	if err := os.MkdirAll(appConfig.CertsBasePath, 0755); err != nil {
		logger.Fatalf("Failed to create base certs directory %s: %v", appConfig.CertsBasePath, err)
	}
	// Attempt to set ownership of the certs directory
	if err := storage.ChownR(appConfig.CertsBasePath, appConfig.UID, appConfig.GID); err != nil {
		logger.Printf("WARNING: Initial chown of %s failed: %v. Ensure permissions are correct, especially if not running as root.", appConfig.CertsBasePath, err)
	}

	acmeManager, err := acme.NewManager(appConfig, logger)
	if err != nil {
		logger.Fatalf("Failed to create ACME manager: %v", err)
	}

	// Initialize IP Updater Service
	ipUpdateService := ipupdater.NewService(appConfig, logger)
	if appConfig.DuckDNSIPUpdateDomain != "" {
		logger.Println("Performing initial DuckDNS IP update check...")
		// Run initial update in a goroutine so it doesn't block startup if network is slow
		go ipUpdateService.CheckAndPerformIPUpdate() // Assuming this function exists and is non-blocking or short-lived
	}

	logger.Println("Performing initial certificate management run...")
	if err := acmeManager.ManageCertificates(); err != nil {
		// Log as error but continue, as renewals might fix it.
		logger.Printf("ERROR during initial certificate management: %v. Will rely on scheduled renewals.", err)
	} else {
		logger.Println("Initial certificate management run completed successfully.")
	}

	// Start HTTP server for health checks and API.
	// The /healthz endpoint needs to be defined within your httpapi package.
	httpServer := httpapi.NewServer(appConfig, acmeManager, ipUpdateService, logger) // Pass services
	go func() {
		logger.Printf("Attempting to start HTTP API server on internal port %s", appConfig.InternalHTTPPort)
		// Ensure httpapi.Start() listens on "0.0.0.0:" + port or ":" + port
		if startErr := httpServer.Start(); startErr != nil && startErr != http.ErrServerClosed {
			logger.Fatalf("Failed to start HTTP API server: %v", startErr)
		}
		logger.Println("HTTP API server has shut down.")
	}()

	// Start certificate renewal scheduler
	if appConfig.RenewalCheckInterval > 0 {
		renewalScheduler := renewal.NewScheduler(appConfig, acmeManager, logger)
		go renewalScheduler.Start() // Assuming Start() runs its own loop and respects shutdown
	} else {
		logger.Println("Certificate renewal checks disabled (interval is zero or negative).")
	}

	// Start IP update scheduler
	if appConfig.DuckDNSIPUpdateDomain != "" && appConfig.DuckDNSIPUpdateInterval > 0 {
		go ipUpdateService.StartScheduler() // Assuming StartScheduler() runs its own loop
	} else {
		logger.Println("Automatic DuckDNS IP updates disabled (domain not set or interval is zero/negative).")
	}

	logger.Println("ACME Certificate Manager is running. Press Ctrl+C or send SIGTERM to exit.")

	// Wait for interrupt signal for graceful shutdown
	quitChannel := make(chan os.Signal, 1)
	signal.Notify(quitChannel, syscall.SIGINT, syscall.SIGTERM)
	receivedSignal := <-quitChannel
	logger.Printf("Received signal: %s. Shutting down ACME Certificate Manager...", receivedSignal)

	// Create a context with a timeout for graceful shutdowns
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second) // Adjust timeout as needed
	defer cancelShutdown()

	// Shutdown HTTP server
	logger.Println("Attempting to gracefully shut down HTTP API server...")
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("Error during HTTP server graceful shutdown: %v", err)
	} else {
		logger.Println("HTTP API server shut down gracefully.")
	}

	// TODO: Add graceful shutdown for renewalScheduler and ipUpdateService if they have Stop() methods
	// Example:
	// if renewalScheduler != nil { // if it was started
	//     logger.Println("Stopping renewal scheduler...")
	//     renewalScheduler.Stop(shutdownCtx) // Assuming a Stop method that respects context
	// }
	// if ipUpdateService != nil && appConfig.DuckDNSIPUpdateDomain != "" && appConfig.DuckDNSIPUpdateInterval > 0 {
	//     logger.Println("Stopping IP update scheduler...")
	//     ipUpdateService.StopScheduler(shutdownCtx) // Assuming a Stop method
	// }

	logger.Println("ACME Certificate Manager shut down gracefully.")
}