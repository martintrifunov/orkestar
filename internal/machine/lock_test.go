package machine_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/machine"
)

// A second acquirer waits while the lock is held and proceeds once it is
// released, rather than writing past it.
func TestCatalogLockExcludesAndReleases(t *testing.T) {
	path := t.TempDir() + "/machines.json"
	release, err := machine.AcquireCatalogLock(path)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	go func() {
		second, err := machine.AcquireCatalogLock(path)
		if err != nil {
			t.Error(err)
			close(acquired)
			return
		}
		acquired <- second
	}()
	select {
	case <-acquired:
		t.Fatal("the lock was acquired twice")
	default:
	}
	release()
	select {
	case second := <-acquired:
		if second == nil {
			t.Fatal("the second acquirer failed")
		}
		second()
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not released")
	}
}

// Parallel read-modify-write cycles must all survive: without the lock the
// second save would silently drop the first update.
func TestCatalogLockKeepsParallelAdds(t *testing.T) {
	path := t.TempDir() + "/machines.json"
	const writers = 20
	var wg sync.WaitGroup
	failures := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			release, err := machine.AcquireCatalogLock(path)
			if err != nil {
				failures <- err
				return
			}
			defer release()
			catalog, err := machine.Load(path)
			if err != nil {
				failures <- err
				return
			}
			if _, err := catalog.Add("", fmt.Sprintf("host-%d.example.com", i), ""); err != nil {
				failures <- err
				return
			}
			if err := catalog.Save(); err != nil {
				failures <- err
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	catalog, err := machine.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.List()) != writers {
		t.Fatalf("expected %d machines, got %d: a parallel update was lost", writers, len(catalog.List()))
	}
}
