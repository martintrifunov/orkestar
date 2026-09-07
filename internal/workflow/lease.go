package workflow

import (
	"fmt"
	"sync"
	"time"
)

// LeaseMode is whether a resource lease excludes other holders.
type LeaseMode string

const (
	// LeaseShared allows multiple concurrent holders.
	LeaseShared LeaseMode = "shared"
	// LeaseExclusive allows exactly one holder and excludes shared leases.
	LeaseExclusive LeaseMode = "exclusive"
)

// Lease is a time-bounded claim on a named resource, such as a mutable
// game editor, scene, or deployment target.
type Lease struct {
	ID         string    `json:"id"`
	Resource   string    `json:"resource"`
	HolderID   string    `json:"holder_id"`
	Mode       LeaseMode `json:"mode"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (l Lease) expired(now time.Time) bool {
	return !l.ExpiresAt.IsZero() && now.After(l.ExpiresAt)
}

// LeaseManager tracks active resource leases and enforces exclusivity:
// an exclusive lease excludes every other lease on the same resource, and
// shared leases exclude only exclusive ones.
type LeaseManager struct {
	mu     sync.Mutex
	leases map[string]map[string]Lease // resource -> lease ID -> Lease
}

// NewLeaseManager returns an empty LeaseManager.
func NewLeaseManager() *LeaseManager {
	return &LeaseManager{leases: make(map[string]map[string]Lease)}
}

// Acquire claims resource for holderID in the given mode for duration. It
// fails if the request conflicts with an existing, unexpired lease.
func (m *LeaseManager) Acquire(resource, holderID string, mode LeaseMode, duration time.Duration) (Lease, error) {
	if resource == "" || holderID == "" {
		return Lease{}, fmt.Errorf("resource and holder are required")
	}
	if mode != LeaseShared && mode != LeaseExclusive {
		return Lease{}, fmt.Errorf("invalid lease mode %q", mode)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	active := m.pruneLocked(resource, now)
	for _, existing := range active {
		if mode == LeaseExclusive || existing.Mode == LeaseExclusive {
			return Lease{}, fmt.Errorf("resource %q is already held by %q", resource, existing.HolderID)
		}
	}

	id, err := newID("lease")
	if err != nil {
		return Lease{}, err
	}
	lease := Lease{
		ID:         id,
		Resource:   resource,
		HolderID:   holderID,
		Mode:       mode,
		AcquiredAt: now,
	}
	if duration > 0 {
		lease.ExpiresAt = now.Add(duration)
	}

	if m.leases[resource] == nil {
		m.leases[resource] = make(map[string]Lease)
	}
	m.leases[resource][id] = lease
	return lease, nil
}

// Release gives up a lease before it expires.
func (m *LeaseManager) Release(resource, leaseID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	byID, ok := m.leases[resource]
	if !ok {
		return fmt.Errorf("no leases held on resource %q", resource)
	}
	if _, ok := byID[leaseID]; !ok {
		return fmt.Errorf("lease %q does not exist on resource %q", leaseID, resource)
	}
	delete(byID, leaseID)
	if len(byID) == 0 {
		delete(m.leases, resource)
	}
	return nil
}

// List returns every unexpired lease on resource.
func (m *LeaseManager) List(resource string) []Lease {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pruneLocked(resource, time.Now().UTC())
}

// ListAll returns every unexpired lease across all resources.
func (m *LeaseManager) ListAll() []Lease {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	var all []Lease
	for resource := range m.leases {
		all = append(all, m.pruneLocked(resource, now)...)
	}
	return all
}

// pruneLocked removes expired leases for resource and returns what
// remains. m.mu must already be held.
func (m *LeaseManager) pruneLocked(resource string, now time.Time) []Lease {
	byID, ok := m.leases[resource]
	if !ok {
		return nil
	}
	active := make([]Lease, 0, len(byID))
	for id, lease := range byID {
		if lease.expired(now) {
			delete(byID, id)
			continue
		}
		active = append(active, lease)
	}
	if len(byID) == 0 {
		delete(m.leases, resource)
	}
	return active
}

func (m *LeaseManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leases = make(map[string]map[string]Lease)
}
