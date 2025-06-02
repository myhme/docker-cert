package renewal

import (
	"time"

	"docker-cert/internal/acme" // <-- Updated import path
	"docker-cert/internal/config"
)

// Logger interface for dependency injection.
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

// Scheduler handles periodic certificate renewal checks.
type Scheduler struct {
	config      *config.Config
	acmeManager *acme.Manager
	logger      Logger
	ticker      *time.Ticker
	// done chan bool // For explicit stop, if needed beyond program termination
}

// NewScheduler creates a new renewal scheduler.
func NewScheduler(cfg *config.Config, manager *acme.Manager, logger Logger) *Scheduler {
	if logger == nil {
		panic("logger cannot be nil for Renewal Scheduler")
	}
	return &Scheduler{
		config:      cfg,
		acmeManager: manager,
		logger:      logger,
		// done: make(chan bool),
	}
}

// Start begins the renewal check loop. This should be run in a goroutine.
func (s *Scheduler) Start() {
	if s.config.RenewalCheckInterval <= 0 {
		s.logger.Println("Renewal scheduler disabled: interval is zero or negative.")
		return
	}

	s.logger.Printf("Renewal scheduler starting with check interval: %v", s.config.RenewalCheckInterval)
	s.ticker = time.NewTicker(s.config.RenewalCheckInterval)
	defer s.ticker.Stop()

	for {
		// For this example, the loop will run until the program terminates.
		// If a more graceful shutdown of this specific goroutine is needed independent
		// of program termination, a 'done' channel or context cancellation would be used.
		<-s.ticker.C
		s.performRenewalCheck()
	}
}

func (s *Scheduler) performRenewalCheck() {
	s.logger.Println("Performing scheduled certificate renewal check...")
	if err := s.acmeManager.ManageCertificates(); err != nil {
		s.logger.Printf("ERROR during scheduled certificate management: %v", err)
	} else {
		s.logger.Println("Scheduled certificate management run completed successfully.")
	}
}

// Stop signals the renewal scheduler to terminate (example if using a done channel).
// func (s *Scheduler) Stop() {
//  if s.ticker != nil {
//      s.logger.Println("Signaling renewal scheduler to stop...")
//      s.done <- true // This would require the Start loop to select on s.done
//  }
// }
