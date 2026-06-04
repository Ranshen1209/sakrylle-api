package service

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

const (
	// DefaultOIDCKeyRotationIntervalHours is the default interval between
	// automatic key rotations (90 days).
	DefaultOIDCKeyRotationIntervalHours = 2160
	// DefaultOIDCAutoRotationEnabled controls whether automatic key rotation
	// runs by default.
	DefaultOIDCAutoRotationEnabled = true
)

// OIDCKeyRotationScheduler manages automatic background rotation of OIDC
// signing keys (both RSA and EC). It reads configuration from the settings
// service and starts a ticker-based goroutine.
//
// Rotation sequence:
//  1. Rotate both RSA and EC keys.
//  2. Wait for the grace period (so RPs can refresh their JWKS cache).
//  3. Clean up expired keys.
//  4. Sleep until the next rotation interval.
//
// Failures are logged but never abort the scheduler — a failed rotation
// is retried on the next tick.
type OIDCKeyRotationScheduler struct {
	keySvc    *OIDCKeyService
	settingSvc SettingRepository

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{} // closed when the goroutine exits
}

// NewOIDCKeyRotationScheduler creates the scheduler. It does not start
// the background goroutine — call Start() for that.
func NewOIDCKeyRotationScheduler(keySvc *OIDCKeyService, settingSvc SettingRepository) *OIDCKeyRotationScheduler {
	return &OIDCKeyRotationScheduler{
		keySvc:     keySvc,
		settingSvc: settingSvc,
		done:       make(chan struct{}),
	}
}

// Start launches the background rotation goroutine. It is idempotent and
// safe to call multiple times (only the first call has effect).
func (s *OIDCKeyRotationScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		return // Already started
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	go s.loop(ctx)
	slog.Info("oidc key rotation scheduler started")
}

// Stop signals the background goroutine to exit and waits for it to finish.
// Idempotent and safe to call when not started.
func (s *OIDCKeyRotationScheduler) Stop() {
	s.mu.Lock()
	if s.cancel == nil {
		s.mu.Unlock()
		return
	}
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()

	cancel()
	<-s.done
	slog.Info("oidc key rotation scheduler stopped")
}

func (s *OIDCKeyRotationScheduler) loop(ctx context.Context) {
	defer close(s.done)

	// Recover from panics so a single rotation failure doesn't kill the scheduler.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("oidc key rotation: panic recovered, scheduler will restart on next tick", "panic", r)
		}
	}()

	// Sleep a short time on startup to let the rest of the system initialize
	// before the first rotation check. Rotation is a long-lived background task;
	// there is no urgency to run it at boot.
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}

	for {
		if !s.isEnabled(ctx) {
			// Check every 5 minutes whether auto-rotation has been re-enabled.
			slog.Debug("oidc auto rotation disabled; waiting for enable")
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Minute):
				continue
			}
		}

		intervalHours := s.rotationIntervalHours(ctx)
		gracePeriodSec := s.gracePeriodSeconds(ctx)

		slog.Info("oidc key rotation cycle starting",
			"interval_hours", intervalHours,
			"grace_period_seconds", gracePeriodSec)

		// Execute rotation.
		s.rotateBoth(ctx)

		// Wait for grace period so RPs can pick up the new keys from JWKS.
		if gracePeriodSec > 0 {
			slog.Info("oidc key rotation: waiting grace period before cleanup",
				"grace_period_seconds", gracePeriodSec)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(gracePeriodSec) * time.Second):
			}
		}

		// Clean up expired keys.
		s.cleanupExpired(ctx)

		// Sleep until next rotation interval.
		interval := time.Duration(intervalHours) * time.Hour
		slog.Info("oidc key rotation cycle complete; next rotation scheduled",
			"next_in_hours", intervalHours)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (s *OIDCKeyRotationScheduler) rotateBoth(ctx context.Context) {
	if err := s.keySvc.RotateKey(ctx); err != nil {
		slog.Error("oidc auto rotation: failed to rotate RSA key", "error", err)
	} else {
		slog.Info("oidc auto rotation: RSA key rotated successfully")
	}

	if err := s.keySvc.RotateECKey(ctx); err != nil {
		slog.Error("oidc auto rotation: failed to rotate EC key", "error", err)
	} else {
		slog.Info("oidc auto rotation: EC key rotated successfully")
	}
}

func (s *OIDCKeyRotationScheduler) cleanupExpired(ctx context.Context) {
	deleted, err := s.keySvc.CleanupExpiredKeys(ctx)
	if err != nil {
		slog.Error("oidc auto rotation: cleanup failed", "error", err)
	} else {
		slog.Info("oidc auto rotation: expired keys cleaned up", "deleted", deleted)
	}
}

func (s *OIDCKeyRotationScheduler) isEnabled(ctx context.Context) bool {
	if s.settingSvc == nil {
		return DefaultOIDCAutoRotationEnabled
	}
	v, err := s.settingSvc.GetValue(ctx, "oidc_auto_rotation_enabled")
	if err != nil {
		slog.Warn("oidc auto rotation: failed to read setting, using default",
			"error", err, "default", DefaultOIDCAutoRotationEnabled)
		return DefaultOIDCAutoRotationEnabled
	}
	if v == "" {
		return DefaultOIDCAutoRotationEnabled
	}
	return v == "true" || v == "1"
}

func (s *OIDCKeyRotationScheduler) rotationIntervalHours(ctx context.Context) int {
	v, err := s.settingSvc.GetValue(ctx, "oidc_key_rotation_interval_hours")
	if err != nil || v == "" {
		return DefaultOIDCKeyRotationIntervalHours
	}
	hours, err := strconv.Atoi(v)
	if err != nil || hours <= 0 {
		slog.Warn("oidc auto rotation: invalid interval, using default",
			"value", v, "default", DefaultOIDCKeyRotationIntervalHours)
		return DefaultOIDCKeyRotationIntervalHours
	}
	return hours
}

func (s *OIDCKeyRotationScheduler) gracePeriodSeconds(ctx context.Context) int {
	v, err := s.settingSvc.GetValue(ctx, "oidc_grace_period_ttl_seconds")
	if err != nil || v == "" {
		return defaultGracePeriodSec
	}
	sec, err := strconv.Atoi(v)
	if err != nil || sec < 0 {
		slog.Warn("oidc auto rotation: invalid grace period, using default",
			"value", v, "default", defaultGracePeriodSec)
		return defaultGracePeriodSec
	}
	return sec
}
