// File: internal/renewal/scheduler.go
package renewal

import (
	"context"
	"log"
	"sync"
	"time"

	// Assuming these are your actual imports (adjust module path)
	"docker-cert/internal/acme"
	"docker-cert/internal/config"
)

type Scheduler struct {
	cfg         *config.Config
	acmeManager *acme.Manager
	logger      *log.Logger
	stopChan    chan struct{}    // Channel to signal the scheduler's main loop to stop
	wg          sync.WaitGroup // To wait for the main loop goroutine to finish
	// isRunning bool           // Optional: to prevent multiple starts if needed
}

func NewScheduler(cfg *config.Config, acmeManager *acme.Manager, logger *log.Logger) *Scheduler {
	// logger.Println("INFO: Renewal: Initializing Scheduler...") // Already logged in main
	return &Scheduler{
		cfg:         cfg,
		acmeManager: acmeManager,
		logger:      logger,
		stopChan:    make(chan struct{}), // Initialize the stop channel
	}
}

// Start begins the periodic certificate renewal checks.
func (s *Scheduler) Start() {
	// if s.isRunning { s.logger.Println("INFO: Renewal: Scheduler already running."); return }
	// s.isRunning = true

	s.wg.Add(1) // Increment for the main processing goroutine
	go func() {
		defer s.wg.Done() // Decrement when this goroutine exits
		s.logger.Printf("INFO: Renewal: Scheduler worker goroutine started. Check interval: %v\n", s.cfg.RenewalCheckInterval)

		ticker := time.NewTicker(s.cfg.RenewalCheckInterval)
		defer ticker.Stop()

	schedulerLoop:
		for {
			select {
			case <-ticker.C:
				s.logger.Println("INFO: Renewal: Performing scheduled certificate management run...")
				if err := s.acmeManager.ManageCertificates(); err != nil { // Assuming this is the core logic
					s.logger.Printf("ERROR: Renewal: Error during scheduled certificate management: %v\n", err)
				} else {
					s.logger.Println("INFO: Renewal: Scheduled certificate management run completed successfully.")
				}
			case <-s.stopChan: // Listen for the stop signal
				s.logger.Println("INFO: Renewal: Scheduler stop signal received, shutting down worker goroutine...")
				break schedulerLoop // Exit the loop
			}
		}
		s.logger.Println("INFO: Renewal: Scheduler worker goroutine stopped.")
	}()
}

// Stop signals the scheduler to shut down and waits for it to complete,
// respecting the provided context for a timeout on the wait.
func (s *Scheduler) Stop(ctx context.Context) {
	s.logger.Println("INFO: Renewal: Initiating scheduler stop...")
	// if !s.isRunning { s.logger.Println("INFO: Renewal: Scheduler not running or already stopped."); return }

	close(s.stopChan) // Signal the worker goroutine by closing the stop channel

	// Wait for the worker goroutine to finish, with a timeout from the context
	doneWaiting := make(chan struct{})
	go func() {
		s.wg.Wait() // Wait for all goroutines (added with s.wg.Add) to complete
		close(doneWaiting)
	}()

	select {
	case <-doneWaiting:
		s.logger.Println("INFO: Renewal: Scheduler stopped gracefully.")
	case <-ctx.Done(): // If the overall shutdown context times out
		s.logger.Printf("WARNING: Renewal: Timed out waiting for scheduler to stop: %v\n", ctx.Err())
	}
	// s.isRunning = false
}