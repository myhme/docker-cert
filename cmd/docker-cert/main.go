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

	// REPLACE docker-cert with your actual Go module path in these imports
	"docker-cert/internal/acme"
	"docker-cert/internal/config"
	"docker-cert/internal/httpapi"
	"docker-cert/internal/ipupdater"
	"docker-cert/internal/renewal"
	"docker-cert/internal/storage"
)

const (
	applicationShutdownTimeout = 15 * time.Second
)

func main() {
	logger := log.New(os.Stdout, "DOCKER-CERT: ", log.LstdFlags|log.Lmicroseconds|log.Lshortfile)
	logger.Println("INFO: Starting ACME Certificate Manager...")

	appConfig, err := config.LoadConfig(logger)
	if err != nil {
		logger.Fatalf("FATAL: Failed to load configuration: %v", err)
	}
	logger.Printf("INFO: Configuration loaded. CertsBasePath: %s, UID: %d, GID: %d, HTTP Port: %s",
		appConfig.CertsBasePath, appConfig.UID, appConfig.GID, appConfig.InternalHTTPPort)

	if err := os.MkdirAll(appConfig.CertsBasePath, 0755); err != nil {
		logger.Fatalf("FATAL: Failed to create base certs directory %s: %v", appConfig.CertsBasePath, err)
	}
	if err := storage.ChownR(appConfig.CertsBasePath, appConfig.UID, appConfig.GID); err != nil {
		logger.Printf("WARN: Initial chown of %s failed: %v. Ensure permissions are correct.", appConfig.CertsBasePath, err)
	}

	acmeManager, err := acme.NewManager(appConfig, logger)
	if err != nil {
		logger.Fatalf("FATAL: Failed to create ACME manager: %v", err)
	}

	ipUpdateService := ipupdater.NewService(appConfig, logger)
	httpAPIServer := httpapi.NewServer(appConfig, acmeManager, ipUpdateService, logger)
	var renewalScheduler *renewal.Scheduler

	// Perform Initial Tasks (Certificate Management)
	// The initial IP update is now handled by the IP Updater Scheduler's first run.
	if appConfig.DuckDNSIPUpdateDomain != "" {
		logger.Println("INFO: Initial DuckDNS IP update will be handled by the IP updater scheduler's first run.")
		// REMOVED: go ipUpdateService.CheckAndPerformIPUpdate()
	}

	logger.Println("INFO: Performing initial certificate management run...")
	if err := acmeManager.ManageCertificates(); err != nil {
		logger.Printf("ERROR: Error during initial certificate management: %v. Application will continue and rely on scheduled renewals.", err)
	} else {
		logger.Println("INFO: Initial certificate management run completed successfully.")
	}

	// Start Background Services
	go func() {
		logger.Printf("INFO: Attempting to start HTTP API server on internal port %s", appConfig.InternalHTTPPort)
		if startErr := httpAPIServer.Start(); startErr != nil && startErr != http.ErrServerClosed {
			logger.Fatalf("FATAL: Failed to start HTTP API server: %v", startErr)
		}
		logger.Println("INFO: HTTP API server has shut down.")
	}()

	if appConfig.RenewalCheckInterval > 0 {
		renewalScheduler = renewal.NewScheduler(appConfig, acmeManager, logger)
		go renewalScheduler.Start()
		logger.Println("INFO: Certificate renewal scheduler started.")
	} else {
		logger.Println("INFO: Certificate renewal checks disabled.")
	}

	if appConfig.DuckDNSIPUpdateDomain != "" && appConfig.DuckDNSIPUpdateInterval > 0 {
		go ipUpdateService.StartScheduler()
		logger.Println("INFO: Automatic DuckDNS IP update scheduler started.")
	} else {
		logger.Println("INFO: Automatic DuckDNS IP updates disabled.")
	}

	logger.Println("INFO: ACME Certificate Manager is fully initialized and running. Press Ctrl+C or send SIGTERM to exit.")

	quitChannel := make(chan os.Signal, 1)
	signal.Notify(quitChannel, syscall.SIGINT, syscall.SIGTERM)
	receivedSignal := <-quitChannel

	logger.Printf("INFO: Received signal: %s. Initiating graceful shutdown...", receivedSignal)

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), applicationShutdownTimeout)
	defer cancelShutdown()

	logger.Println("INFO: Attempting to gracefully shut down HTTP API server...")
	if err := httpAPIServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("ERROR: Error during HTTP API server graceful shutdown: %v", err)
	} else {
		logger.Println("INFO: HTTP API server shut down gracefully.")
	}

	if renewalScheduler != nil {
		logger.Println("INFO: Attempting to gracefully shut down renewal scheduler...")
		renewalScheduler.Stop(shutdownCtx)
	}

	if appConfig.DuckDNSIPUpdateDomain != "" && appConfig.DuckDNSIPUpdateInterval > 0 {
		logger.Println("INFO: Attempting to gracefully shut down IP update scheduler...")
		ipUpdateService.StopScheduler(shutdownCtx)
	}

	logger.Println("INFO: All services have been instructed to shut down.")
	logger.Println("INFO: ACME Certificate Manager shut down gracefully.")
}