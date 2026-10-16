package sandbox

import (
	"errors"
	"testing"
)

// One slow probe on a busy host must not restart a worker mid-run.
func TestObserveHealthNeedsConsecutiveFailures(t *testing.T) {
	s := &Supervisor{}
	failure := errors.New("deadline exceeded")
	for i := 1; i < unhealthyThreshold; i++ {
		if !s.observeHealth("lib", failure) {
			t.Fatalf("failure %d restarted the worker before the threshold", i)
		}
	}
	if !s.observeHealth("lib", nil) {
		t.Fatal("a healthy probe must keep the worker")
	}
	for i := 1; i < unhealthyThreshold; i++ {
		if !s.observeHealth("lib", failure) {
			t.Fatalf("a success must reset the count; failure %d restarted", i)
		}
	}
	if s.observeHealth("lib", failure) {
		t.Fatal("consecutive failures at the threshold must restart the worker")
	}
	s.resetHealth("lib")
	if !s.observeHealth("lib", failure) {
		t.Fatal("reset must clear the count")
	}
}
