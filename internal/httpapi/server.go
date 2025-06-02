package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Logger interface for dependency injection.
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

// Server provides an HTTP server for health checks.
type Server struct {
	port   string
	logger Logger
	server *http.Server
}

// NewServer creates a new HTTP API server instance.
func NewServer(port string, logger Logger) *Server {
	if logger == nil {
		panic("logger cannot be nil for HTTP API Server")
	}
	return &Server{
		port:   port,
		logger: logger,
	}
}

// Start begins listening for HTTP requests. This is a blocking call.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthzHandler)

	s.server = &http.Server{
		Addr:         ":" + s.port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.logger.Printf("HTTP API server attempting to listen on port %s", s.port)
	// ListenAndServe always returns a non-nil error. After Shutdown or Close,
	// the returned error is http.ErrServerClosed.
	if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
		s.logger.Printf("CRITICAL: HTTP server ListenAndServe error on port %s: %v", s.port, err)
		return fmt.Errorf("HTTP server ListenAndServe error: %w", err)
	}
	// This part is reached if the server is gracefully shut down.
	s.logger.Println("HTTP API server shut down cleanly.")
	return nil
}

// healthzHandler responds to health check requests.
func (s *Server) healthzHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-f")
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintln(w, "OK")
	if err != nil {
		// This error typically means the client disconnected.
		// The header and status have likely already been sent.
		s.logger.Printf("Error writing health check response (client might have disconnected): %v", err)
	}
	// s.logger.Printf("Health check request successful from %s", r.RemoteAddr) // Can be too noisy
}

// Shutdown gracefully shuts down the HTTP server.
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
