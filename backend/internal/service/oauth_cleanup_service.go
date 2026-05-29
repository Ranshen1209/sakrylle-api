package service

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

const oauthCleanupInterval = 1 * time.Hour

// OAuthCleanupService calls the oauth_cleanup_expired() PostgreSQL function on
// a regular schedule. The function was created by migration 145 but was never
// invoked from application code (Issue 18).
//
// The service follows the same Start/Stop/runLoop pattern used by
// IdempotencyCleanupService and UsageCleanupService.
type OAuthCleanupService struct {
	db *sql.DB

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
}

// NewOAuthCleanupService creates the service. db must be non-nil.
func NewOAuthCleanupService(db *sql.DB) *OAuthCleanupService {
	return &OAuthCleanupService{
		db:     db,
		stopCh: make(chan struct{}),
	}
}

// Start launches the background goroutine. Safe to call multiple times.
func (s *OAuthCleanupService) Start() {
	if s == nil || s.db == nil {
		return
	}
	s.startOnce.Do(func() {
		slog.Info("oauth_cleanup: started", "interval", oauthCleanupInterval)
		go s.runLoop()
	})
}

// Stop signals the background goroutine to exit. Safe to call multiple times.
func (s *OAuthCleanupService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
		slog.Info("oauth_cleanup: stopped")
	})
}

func (s *OAuthCleanupService) runLoop() {
	ticker := time.NewTicker(oauthCleanupInterval)
	defer ticker.Stop()

	// Run once immediately on startup to clear any backlog from downtime.
	s.cleanupOnce()

	for {
		select {
		case <-ticker.C:
			s.cleanupOnce()
		case <-s.stopCh:
			return
		}
	}
}

func (s *OAuthCleanupService) cleanupOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := s.db.ExecContext(ctx, "SELECT oauth_cleanup_expired()"); err != nil {
		slog.Warn("oauth_cleanup: cleanup failed", "error", err)
	}
}
