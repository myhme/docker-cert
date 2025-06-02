package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("INTERNAL_HTTP_PORT")
	if port == "" {
		port = "8080" // Default port if not set
	}

	healthURL := fmt.Sprintf("http://localhost:%s/healthz", port)

	client := http.Client{
		Timeout: 5 * time.Second, // Set a reasonable timeout
	}

	resp, err := client.Get(healthURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Health check failed: Error connecting to %s: %v\n", healthURL, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Optionally, you could read and parse the JSON body here to check OverallStatus
		// For now, a 200 OK from /healthz is considered healthy by this basic checker.
		// Your /healthz endpoint already returns 503 if internally unhealthy.
		// So, checking for resp.StatusCode == http.StatusOK is sufficient.
		os.Exit(0) // Healthy
	}

	fmt.Fprintf(os.Stderr, "Health check failed: Received status code %d from %s\n", resp.StatusCode, healthURL)
	os.Exit(1) // Unhealthy
}
