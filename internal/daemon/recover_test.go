package daemon

import (
	"strings"
	"testing"
	"time"
)

// TestRecoverPanicReturnsErrorInsteadOfCrashing is the regression test for the
// crash that took down the whole daemon: a panic anywhere in one pane's
// rendering must turn into an error for that pane, not propagate and kill
// every other session the daemon owns.
func TestRecoverPanicReturnsErrorInsteadOfCrashing(t *testing.T) {
	err := recoverPanic("term_1", func() { panic("index out of range [28] with length 28") })
	if err == nil {
		t.Fatal("panic was not recovered into an error")
	}
	if !strings.Contains(err.Error(), "index out of range") {
		t.Fatalf("recovered error lost the panic message: %v", err)
	}
}

func TestRecoverPanicPassesThroughANormalCall(t *testing.T) {
	ran := false
	if err := recoverPanic("term_1", func() { ran = true }); err != nil {
		t.Fatalf("unexpected error from a call that never panicked: %v", err)
	}
	if !ran {
		t.Fatal("fn was never called")
	}
}

// TestGuardContainsAPanic is the goroutine-backstop half of the fix: a panic
// inside a long-lived goroutine wrapped in guard must not escape it, since
// nothing calls recover for a goroutine started with a bare "go" and an
// unrecovered panic there kills the whole daemon.
func TestGuardContainsAPanic(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		guard("test.guarded", func() { panic("boom") })
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("guarded goroutine never returned")
	}
}
