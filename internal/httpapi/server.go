package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"docker-cert/internal/acme"
	"docker-cert/internal/config"
	"docker-cert/internal/ipupdater"
)

type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

// Server provides an HTTP server for health checks and API endpoints.
type Server struct {
	config      *config.Config
	port        string
	logger      Logger
	server      *http.Server
	acmeManager *acme.Manager
	ipUpdateSvc *ipupdater.Service
}

// HealthStatusResponse defines the structure for the health check JSON response.
type HealthStatusResponse struct {
	OverallStatus string                       `json:"overall_status"`
	Timestamp     string                       `json:"timestamp"`
	Checks        map[string]ComponentStatus `json:"checks"`
}

// ComponentStatus is a generic status structure for sub-components.
type ComponentStatus struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}


func NewServer(cfg *config.Config, manager *acme.Manager, ipSvc *ipupdater.Service, logger Logger) *Server {
	if logger == nil {
		panic("logger cannot be nil for HTTP API Server")
	}
	return &Server{
		config:      cfg,
		port:        cfg.InternalHTTPPort,
		logger:      logger,
		acmeManager: manager,
		ipUpdateSvc: ipSvc,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthzHandler)

	mux.Handle("/api/v1/certificates/renew", s.authMiddleware(http.HandlerFunc(s.renewCertificatesHandler)))
	mux.Handle("/api/v1/ip/current", s.authMiddleware(http.HandlerFunc(s.getCurrentIPHandler)))
	mux.Handle("/api/v1/duckdns/update-ip", s.authMiddleware(http.HandlerFunc(s.updateDuckDNSIPHandler)))

	s.server = &http.Server{
		Addr:         ":" + s.port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.logger.Printf("HTTP API server attempting to listen on port %s", s.port)
	if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
		s.logger.Printf("CRITICAL: HTTP server ListenAndServe error on port %s: %v", s.port, err)
		return fmt.Errorf("HTTP server ListenAndServe error: %w", err)
	}
	s.logger.Println("HTTP API server shut down cleanly.")
	return nil
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.config.APIAuthToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			s.logger.Printf("API: Missing Authorization header from %s for %s", r.RemoteAddr, r.URL.Path)
			http.Error(w, "Unauthorized: Missing Authorization header", http.StatusUnauthorized)
			return
		}
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			s.logger.Printf("API: Invalid Authorization header format from %s for %s", r.RemoteAddr, r.URL.Path)
			http.Error(w, "Unauthorized: Invalid Authorization header format", http.StatusUnauthorized)
			return
		}
		if parts[1] != s.config.APIAuthToken {
			s.logger.Printf("API: Invalid token from %s for %s", r.RemoteAddr, r.URL.Path)
			http.Error(w, "Unauthorized: Invalid token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) healthzHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	overallHealthy := true
	response := HealthStatusResponse{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Checks:    make(map[string]ComponentStatus),
	}

	if s.acmeManager != nil {
		acmeStatus := s.acmeManager.GetStatus()
		response.Checks["acme_manager"] = ComponentStatus{Status: acmeStatus.Status, Message: acmeStatus.Message}
		if acmeStatus.Status != "ok" && acmeStatus.Status != "initializing" {
			overallHealthy = false
		}
	} else {
		response.Checks["acme_manager"] = ComponentStatus{Status: "error", Message: "ACME manager is not available"}
		overallHealthy = false
	}

	if s.ipUpdateSvc != nil {
		ipStatus := s.ipUpdateSvc.GetStatus()
		httpIPStatus := ComponentStatus{
			Status:  ipStatus.Status,
			Message: ipStatus.Message,
			Details: make(map[string]interface{}),
		}
		if ipStatus.LastIPv4Detected != "" { httpIPStatus.Details["last_ipv4_detected"] = ipStatus.LastIPv4Detected }
		if ipStatus.LastIPv6Detected != "" { httpIPStatus.Details["last_ipv6_detected"] = ipStatus.LastIPv6Detected }
		if ipStatus.LastSuccess != "" { httpIPStatus.Details["last_success_time"] = ipStatus.LastSuccess }
		if ipStatus.LastCheckAttempt != "" { httpIPStatus.Details["last_check_attempt_time"] = ipStatus.LastCheckAttempt }
		if ipStatus.LastUpdateError != "" { httpIPStatus.Details["last_error"] = ipStatus.LastUpdateError }

		response.Checks["ip_updater"] = httpIPStatus
		if ipStatus.Status == "error" || (s.config.DuckDNSIPUpdateDomain != "" && ipStatus.Status == "disabled") {
			overallHealthy = false
		}
	} else {
		response.Checks["ip_updater"] = ComponentStatus{Status: "error", Message: "IP updater service is not available"}
		overallHealthy = false
	}
	
	if s.config != nil {
	    response.Checks["configuration"] = ComponentStatus{Status: "ok", Message: "Configuration loaded."}
	} else {
	    response.Checks["configuration"] = ComponentStatus{Status: "error", Message: "Configuration not loaded."}
	    overallHealthy = false
	}


	if overallHealthy {
		response.OverallStatus = "healthy"
		w.WriteHeader(http.StatusOK)
	} else {
		response.OverallStatus = "unhealthy"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Printf("Error encoding health check response: %v", err)
	}
}


func (s *Server) renewCertificatesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	s.logger.Println("API: Received request to renew certificates.")
	go func() {
		if s.acmeManager == nil {
			s.logger.Println("API: Cannot renew certificates, ACME manager not available.")
			return
		}
		if err := s.acmeManager.ManageCertificates(); err != nil {
			s.logger.Printf("API: Error during manual certificate renewal trigger: %v", err)
		} else {
			s.logger.Println("API: Manual certificate renewal trigger completed successfully.")
		}
	}()
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintln(w, "Certificate renewal process initiated.")
}

func (s *Server) getCurrentIPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.ipUpdateSvc == nil {
		http.Error(w, "IP Updater service not available", http.StatusInternalServerError)
		return
	}
	s.logger.Println("API: Received request to get current IP.")
	ipv4, ipv6, err := s.ipUpdateSvc.GetCurrentPublicIPs()
	if err != nil {
		s.logger.Printf("API: Error getting current IP: %v", err)
		http.Error(w, fmt.Sprintf("Failed to get public IP: %v", err), http.StatusInternalServerError)
		return
	}
	response := map[string]string{
		"ipv4": ipupdater.MaskIP(ipv4), // Use exported MaskIP
		"ipv6": ipupdater.MaskIP(ipv6), // Use exported MaskIP
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Printf("API: Error encoding IP response: %v", err)
	}
}

func (s *Server) updateDuckDNSIPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.ipUpdateSvc == nil {
		http.Error(w, "IP Updater service not available", http.StatusAccepted)
		fmt.Fprintln(w, "DuckDNS IP update process initiated, but service unavailable.")
		return
	}
	s.logger.Println("API: Received request to update DuckDNS IP.")
	go s.ipUpdateSvc.CheckAndPerformIPUpdate()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintln(w, "DuckDNS IP update process initiated.")
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		s.logger.Println("HTTP API server was not started, nothing to shut down.")
		return nil
	}
	s.logger.Println("Attempting to shut down HTTP API server gracefully...")
	err := s.server.Shutdown(ctx)
	if err != nil {
		s.logger.Printf("HTTP API server shutdown error: %v", err)
		return err
	}
	s.logger.Println("HTTP API server has been shut down.")
	return nil
}
