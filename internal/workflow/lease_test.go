package workflow_test

import (
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

func TestAcquireExclusiveBlocksOthers(t *testing.T) {
	t.Parallel()

	manager := workflow.NewLeaseManager()
	if _, err := manager.Acquire("unreal-editor", "agent_1", workflow.LeaseExclusive, time.Minute); err != nil {
		t.Fatalf("acquire exclusive: %v", err)
	}
	if _, err := manager.Acquire("unreal-editor", "agent_2", workflow.LeaseExclusive, time.Minute); err == nil {
		t.Fatal("expected a second exclusive lease to be rejected")
	}
	if _, err := manager.Acquire("unreal-editor", "agent_2", workflow.LeaseShared, time.Minute); err == nil {
		t.Fatal("expected a shared lease to be rejected while exclusive is held")
	}
}

func TestAcquireSharedAllowsMultipleHolders(t *testing.T) {
	t.Parallel()

	manager := workflow.NewLeaseManager()
	if _, err := manager.Acquire("docs", "agent_1", workflow.LeaseShared, time.Minute); err != nil {
		t.Fatalf("acquire first shared: %v", err)
	}
	if _, err := manager.Acquire("docs", "agent_2", workflow.LeaseShared, time.Minute); err != nil {
		t.Fatalf("acquire second shared: %v", err)
	}
	if _, err := manager.Acquire("docs", "agent_3", workflow.LeaseExclusive, time.Minute); err == nil {
		t.Fatal("expected exclusive to be rejected while shared leases are held")
	}

	leases := manager.List("docs")
	if len(leases) != 2 {
		t.Fatalf("unexpected lease count: %d", len(leases))
	}
}

func TestReleaseFreesResource(t *testing.T) {
	t.Parallel()

	manager := workflow.NewLeaseManager()
	lease, err := manager.Acquire("scene", "agent_1", workflow.LeaseExclusive, time.Minute)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := manager.Release("scene", lease.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := manager.Acquire("scene", "agent_2", workflow.LeaseExclusive, time.Minute); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestExpiredLeaseIsPruned(t *testing.T) {
	t.Parallel()

	manager := workflow.NewLeaseManager()
	if _, err := manager.Acquire("scene", "agent_1", workflow.LeaseExclusive, time.Millisecond); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if leases := manager.List("scene"); len(leases) != 0 {
		t.Fatalf("expected expired lease to be pruned, got %#v", leases)
	}
	if _, err := manager.Acquire("scene", "agent_2", workflow.LeaseExclusive, time.Minute); err != nil {
		t.Fatalf("acquire after expiry: %v", err)
	}
}

func TestReleaseUnknownLease(t *testing.T) {
	t.Parallel()

	manager := workflow.NewLeaseManager()
	if err := manager.Release("scene", "missing"); err == nil {
		t.Fatal("expected release of unknown lease to fail")
	}
}
