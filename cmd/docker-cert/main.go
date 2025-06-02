package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"docker-cert/internal/acme"
	"docker-cert/internal/config"
	"docker-cert/internal/httpapi"
	"docker-cert/internal/ipupdater" // New import
	"docker-cert/internal/renewal"
	"docker-cert/internal/storage"
)

func main() {
	logger := log.New(os.Stdout, "DOCKER-CERT: ", log.LstdFlags|log.Lshortfile)
	logger.Println("Starting ACME Certificate Manager...")

	appConfig, err := config.LoadConfig(logger)
	if err != nil {
		logger.Fatalf("Failed to load configuration: %v", err)
	}

	if err := os.MkdirAll(appConfig.CertsBasePath, 0755); err != nil {
		logger.Fatalf("Failed to create base certs directory %s: %v", appConfig.CertsBasePath, err)
	}
	if err := storage.ChownR(appConfig.CertsBasePath, appConfig.UID, appConfig.GID); err != nil {
		logger.Printf("WARNING: Initial chown of %s failed: %v. Ensure permissions are correct.", appConfig.CertsBasePath, err)
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
		go ipUpdateService.CheckAndPerformIPUpdate()
	}


	logger.Println("Performing initial certificate management run...")
	if err := acmeManager.ManageCertificates(); err != nil {
		logger.Printf("ERROR during initial certificate management: %v. Will rely on renewals.", err)
	} else {
		logger.Println("Initial certificate management run completed successfully.")
	}

	// Start HTTP server for health checks and API
	httpServer := httpapi.NewServer(appConfig, acmeManager, ipUpdateService, logger) // Pass services
	go func() {
		logger.Printf("Attempting to start HTTP API server on internal port %s", appConfig.InternalHTTPPort)
		if startErr := httpServer.Start(); startErr != nil && startErr != http.ErrServerClosed {
			logger.Fatalf("Failed to start HTTP API server: %v", startErr)
		}
		logger.Println("HTTP API server has shut down.")
	}()

	// Start certificate renewal scheduler
	if appConfig.RenewalCheckInterval > 0 {
		renewalScheduler := renewal.NewScheduler(appConfig, acmeManager, logger)
		go renewalScheduler.Start()
	} else {
		logger.Println("Certificate renewal checks disabled.")
	}

	// Start IP update scheduler
	if appConfig.DuckDNSIPUpdateDomain != "" && appConfig.DuckDNSIPUpdateInterval > 0 {
		go ipUpdateService.StartScheduler()
	} else {
		logger.Println("Automatic DuckDNS IP updates disabled (domain not set or interval is zero/negative).")
	}


	logger.Println("ACME Certificate Manager is running. Press Ctrl+C to exit.")

	quitChannel := make(chan os.Signal, 1)
	signal.Notify(quitChannel, syscall.SIGINT, syscall.SIGTERM)
	<-quitChannel

	logger.Println("Shutting down ACME Certificate Manager...")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("Error during HTTP server graceful shutdown: %v", err)
	}
	// Add shutdown for ipUpdateService scheduler if it had a Stop method

	logger.Println("ACME Certificate Manager shut down gracefully.")
}
