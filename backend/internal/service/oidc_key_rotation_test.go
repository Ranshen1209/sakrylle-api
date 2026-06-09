package service

import (
	"context"
	"testing"
)

// TestRunGuarded_RecoversPanic verifies that a panic inside the guarded
// function does not propagate out of runGuarded, so the scheduler loop can
// continue to the next iteration.
func TestRunGuarded_RecoversPanic(t *testing.T) {
	ran := false
	// If runGuarded re-panicked, the deferred recover below would catch it and
	// fail the test. If it returns normally, execution proceeds to set ran.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("runGuarded leaked a panic: %v", r)
			}
		}()
		runGuarded("boom", func() { panic("boom") })
		ran = true
	}()
	if !ran {
		t.Fatal("expected execution to continue after guarded panic")
	}
}

// TestRotationScheduler_NilSettingSvc_UsesDefaults verifies that the interval
// accessors tolerate a nil settingSvc and return their defaults rather than
// panicking with a nil dereference.
func TestRotationScheduler_NilSettingSvc_UsesDefaults(t *testing.T) {
	s := &OIDCKeyRotationScheduler{} // settingSvc is nil
	ctx := context.Background()

	if got := s.rotationIntervalHours(ctx); got != DefaultOIDCKeyRotationIntervalHours {
		t.Fatalf("rotationIntervalHours = %d, want %d", got, DefaultOIDCKeyRotationIntervalHours)
	}
	if got := s.gracePeriodSeconds(ctx); got != defaultGracePeriodSec {
		t.Fatalf("gracePeriodSeconds = %d, want %d", got, defaultGracePeriodSec)
	}
}
