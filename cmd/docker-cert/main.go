package main

import (
	"context"
	"log"
	"net/http" // Required for http.ErrServerClosed
	"os"
	"os/signal"
	"syscall"
	"time"

	"docker-cert/internal/acme" // Updated import path
	"docker-cert/internal/config"
	"docker-cert/internal/httpapi"
	"docker-cert/internal/renewal"
	"docker-cert/internal/storage"
)

func main() {
	// Setup logger with standard flags and a prefix
	logger := log.New(os.Stdout, "DOCKER-CERT: ", log.LstdFlags|log.Lshortfile)

	logger.Println("Starting ACME Certificate Manager...")

	// Load configuration from environment variables
	appConfig, err := config.LoadConfig(logger)
	if err != nil {
		logger.Fatalf("Failed to load configuration: %v", err)
	}

	// Ensure the base certificates path exists.
	// The storage functions will create domain-specific subdirectories.
	if err := os.MkdirAll(appConfig.CertsBasePath, 0755); err != nil {
		logger.Fatalf("Failed to create base certs directory %s: %v", appConfig.CertsBasePath, err)
	}
	// Attempt initial ownership change of the base path.
	// Errors here are logged as warnings as the application might still function if permissions are already correct
	// or if running as a user that can write to the path but not chown.
	if err := storage.ChownR(appConfig.CertsBasePath, appConfig.UID, appConfig.GID); err != nil {
		logger.Printf("WARNING: Initial chown of %s failed: %v. Ensure the running user has permissions to chown or that path permissions are already correct.", appConfig.CertsBasePath, err)
	}

	// Create ACME manager instance
	acmeManager, err := acme.NewManager(appConfig, logger)
	if err != nil {
		logger.Fatalf("Failed to create ACME manager: %v", err)
	}

	// Perform initial certificate management run
	logger.Println("Performing initial certificate management run...")
	if err := acmeManager.ManageCertificates(); err != nil {
		// Log as an error but don't necessarily exit; the scheduler might resolve it later.
		// For a production system, you might have more sophisticated error handling or startup checks here.
		logger.Printf("ERROR during initial certificate management: %v. The application will continue and rely on scheduled renewals.", err)
	} else {
		logger.Println("Initial certificate management run completed successfully.")
	}

	// Start HTTP server for health checks in a new goroutine
	httpServer := httpapi.NewServer(appConfig.InternalHTTPPort, logger)
	go func() {
		logger.Printf("Attempting to start healthcheck API server on internal port %s", appConfig.InternalHTTPPort)
		// ListenAndServe blocks until the server is shut down.
		// http.ErrServerClosed is expected on graceful shutdown, so not a fatal error.
		if startErr := httpServer.Start(); startErr != nil && startErr != http.ErrServerClosed {
			logger.Fatalf("Failed to start HTTP API server: %v", startErr)
		}
		logger.Println("HTTP API server has shut down.")
	}()

	// Start renewal scheduler if an interval is configured
	if appConfig.RenewalCheckInterval > 0 {
		renewalScheduler := renewal.NewScheduler(appConfig, acmeManager, logger)
		go renewalScheduler.Start() // Scheduler runs in its own goroutine
		// The scheduler's goroutine will exit when the main program exits.
		// For more explicit control, the scheduler could accept a context for cancellation.
	} else {
		logger.Println("Certificate renewal checks are disabled (RENEWAL_CHECK_INTERVAL_HOURS is zero or negative).")
	}

	logger.Println("ACME Certificate Manager is running. Press Ctrl+C to exit.")

	// Wait for interrupt signal for graceful shutdown
	quitChannel := make(chan os.Signal, 1)
	signal.Notify(quitChannel, syscall.SIGINT, syscall.SIGTERM)
	<-quitChannel // Block until a signal is received

	logger.Println("Shutting down ACME Certificate Manager...")

	// Create a context with a timeout for graceful shutdown of the HTTP server
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second) // 10-second timeout for shutdown
	defer cancelShutdown()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("Error during HTTP server graceful shutdown: %v", err)
	}
	// If renewalScheduler had a Stop() method using context, call it here.

	logger.Println("ACME Certificate Manager shut down gracefully.")
}
