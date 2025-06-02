package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"
)

const (
	defaultHealthcheckURL         = "http://localhost:8080/healthz"
	defaultRequestTimeoutSeconds  = 8
	envHealthcheckURL             = "HEALTHCHECK_URL"
	envHealthcheckTimeoutSeconds  = "HEALTHCHECK_TIMEOUT_SECONDS"
)

func main() {
	targetURL := getEnv(envHealthcheckURL, defaultHealthcheckURL)
	timeoutSeconds := getEnvAsInt(envHealthcheckTimeoutSeconds, defaultRequestTimeoutSeconds)

	fmt.Printf("[%s] Healthcheck: Initiating check for URL: %s with timeout: %d seconds\n",
		time.Now().UTC().Format(time.RFC3339), targetURL, timeoutSeconds)

	client := &http.Client{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] Healthcheck: FAILED - Error creating request for %s: %v\n",
			time.Now().UTC().Format(time.RFC3339), targetURL, err)
		os.Exit(1)
		return
	}

	// Optional: Add a custom User-Agent
	req.Header.Set("User-Agent", "Docker-Healthcheck/1.0")

	resp, err := client.Do(req)
	if err != nil {
		// This is where "context deadline exceeded" would typically be caught
		fmt.Fprintf(os.Stderr, "[%s] Healthcheck: FAILED - Error performing GET request to %s: %v\n",
			time.Now().UTC().Format(time.RFC3339), targetURL, err)
		os.Exit(1)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices { // Check for 2xx status codes
		fmt.Printf("[%s] Healthcheck: SUCCESS - Received status %s from %s\n",
			time.Now().UTC().Format(time.RFC3339), resp.Status, targetURL)
		os.Exit(0) // Healthy
	} else {
		fmt.Fprintf(os.Stderr, "[%s] Healthcheck: FAILED - Received non-2xx status %s from %s\n",
			time.Now().UTC().Format(time.RFC3339), resp.Status, targetURL)
		os.Exit(1) // Unhealthy
	}
}

// Helper function to get environment variable or default string
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// Helper function to get environment variable as int or default int
func getEnvAsInt(key string, fallback int) int {
	if valueStr, ok := os.LookupEnv(key); ok {
		if valueInt, err := strconv.Atoi(valueStr); err == nil {
			return valueInt
		}
		fmt.Fprintf(os.Stderr, "Warning: Invalid integer value for env var %s: %s. Using default %d.\n", key, valueStr, fallback)
	}
	return fallback
}