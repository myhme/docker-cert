// File: cmd/docker-cert/main.go
package main

import (
	"context"
	"fmt"
	"log" // Using standard log. Consider a structured logger for production.
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	defaultPort           = "8080"
	serverReadTimeout     = 5 * time.Second
	serverReadHeaderTimeout = 3 * time.Second
	serverWriteTimeout    = 10 * time.Second
	serverIdleTimeout     = 120 * time.Second
	shutdownTimeout       = 30 * time.Second
)

// healthzHandler is a lightweight handler that responds with 200 OK.
// This is targeted by the Docker healthcheck.
func healthzHandler(w http.ResponseWriter, r *http.Request) {
	// Log the request (optional, but useful for seeing if healthchecks are hitting)
	log.Printf("INFO: Main App: /healthz endpoint hit by %s (User-Agent: %s)\n", r.RemoteAddr, r.UserAgent())

	// This endpoint should be very fast.
	// Avoid any blocking calls or heavy computations.
	// If you need to check a critical component's status, ensure that check is quick.
	// Example:
	// if !isSomeCriticalComponentOkay() {
	//    http.Error(w, "Critical component unhealthy", http.StatusServiceUnavailable)
	//    return
	// }

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte("OK"))
	if err != nil {
		// This error during write is less common but good to log.
		log.Printf("ERROR: Main App: Failed to write /healthz response: %v\n", err)
	}
}

// rootHandler provides a basic response for the root path.
func rootHandler(w http.ResponseWriter, r *http.Request) {
	// If you only want to serve /healthz and nothing else on this port,
	// you might have this return 404 for path "/" as well,
	// or not register a handler for "/" at all (letting the ServeMux handle it as 404).
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		log.Printf("INFO: Main App: 404 Not Found for path: %s\n", r.URL.Path)
		return
	}
	log.Printf("INFO: Main App: / (root) endpoint hit by %s\n", r.RemoteAddr)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("docker-cert application is running. Health check available at /healthz."))
}

func main() {
	// Configure logger
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds) // Example: include microseconds for more precise timing

	log.Println("INFO: Main App: Starting docker-cert application...")

	// Get port from environment variable, default if not set.
	// This should match the ENV INTERNAL_HTTP_PORT in your Dockerfile.
	port := os.Getenv("INTERNAL_HTTP_PORT")
	if port == "" {
		port = defaultPort
		log.Printf("INFO: Main App: INTERNAL_HTTP_PORT not set, defaulting to %s\n", port)
	}
	listenAddr := fmt.Sprintf(":%s", port) // Listen on all available network interfaces (e.g., 0.0.0.0)

	// Create a new ServeMux (HTTP request router)
	mux := http.NewServeMux()

	// Register handlers
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/", rootHandler) // Handles the root path and any other undefined paths (as 404)

	// Configure the HTTP server
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadTimeout:       serverReadTimeout,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}

	// Channel to listen for OS signals for graceful shutdown
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	// Goroutine to start the HTTP server
	go func() {
		log.Printf("INFO: Main App: HTTP server starting to listen on %s\n", listenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("FATAL: Main App: HTTP server ListenAndServe failed: %v\n", err)
		}
		log.Println("INFO: Main App: HTTP server has stopped listening.")
	}()

	// ---------------------------------------------------------------------
	// V V V V V V V V V V V V V V V V V V V V V V V V V V V V V V V V V V V
	//
	// YOUR ACTUAL DOCKER-CERT APPLICATION LOGIC GOES HERE.
	// This could involve:
	//  - Initializing ACME clients.
	//  - Setting up DuckDNS updaters.
	//  - Starting background tasks or schedulers.
	//  - Etc.
	//
	// For this runnable example, we'll just log that it's "running"
	// and then the main goroutine will block waiting for a shutdown signal.
	// In a real app, you might have other blocking calls or a select loop.
	//
	log.Println("INFO: Main App: Core application logic would be running here.")
	log.Println("INFO: Main App: Application is ready and awaiting termination signal (Ctrl+C)...")
	//
	// ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^ ^
	// ---------------------------------------------------------------------


	// Block until a shutdown signal is received
	sig := <-stopChan
	log.Printf("INFO: Main App: Received signal: %s. Initiating graceful shutdown...\n", sig)

	// Create a context with a timeout for the graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Attempt to gracefully shut down the HTTP server
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("FATAL: Main App: HTTP server graceful shutdown failed: %v\n", err)
	} else {
		log.Println("INFO: Main App: HTTP server shut down gracefully.")
	}

	// Perform any other cleanup your application needs before exiting
	log.Println("INFO: Main App: Application cleanup finished.")
	log.Println("INFO: Main App: Exiting.")
}