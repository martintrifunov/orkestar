// Package federation keeps clients connected to several machines at once.
// Each machine is a daemon reached over ssh; the manager tracks per-machine
// health, retries a lost connection on a backoff, and merges the boards into
// one view that names the machine each entity came from. It holds no
// credentials: ssh owns authentication, exactly as it does for --remote.
package federation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/machine"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// LocalID names the machine this client runs on.
const LocalID = "local"

// State is what the manager last learned about a machine.
type State string

const (
	Online  State = "online"
	Offline State = "offline"
)

// Dialer opens a client for a saved machine. It is a seam so tests can point
// at an in-process daemon instead of ssh.
type Dialer func(ctx context.Context, saved machine.Machine) (*ipc.Client, error)

// RemoteDialer is the real dialer: the daemon's socket carried over ssh, in
// the named session if the machine has one.
func RemoteDialer(ctx context.Context, saved machine.Machine) (*ipc.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	remote, err := ipc.ParseRemote(saved.Host)
	if err != nil {
		return nil, err
	}
	remote.Session = saved.Session
	return ipc.NewRemoteClient(remote), nil
}

// Connection is one machine and what the manager last saw on it.
type Connection struct {
	Machine  machine.Machine
	State    State
	Err      string
	Version  string
	Snapshot daemon.Snapshot

	client   *ipc.Client
	failures int
	retryAt  time.Time
	// dialing guards against two refreshes dialing the same machine at once.
	dialing bool
}

// Manager owns the local connection and the remote ones.
type Manager struct {
	mu          sync.Mutex
	localLabel  string
	localClient *ipc.Client
	local       Connection
	dial        Dialer
	remotes     map[string]*Connection
	backoffBase time.Duration
}

// New returns a manager for localClient and the given dialer. A nil dialer
// uses RemoteDialer.
func New(localLabel string, localClient *ipc.Client, dial Dialer) *Manager {
	if localLabel == "" {
		localLabel = "Local"
	}
	if dial == nil {
		dial = RemoteDialer
	}
	return &Manager{
		localLabel:  localLabel,
		localClient: localClient,
		local:       Connection{Machine: machine.Machine{ID: LocalID, Label: localLabel, Enabled: true}},
		dial:        dial,
		remotes:     map[string]*Connection{},
		backoffBase: 2 * time.Second,
	}
}

// SetBackoff changes the base reconnect delay. It is for tests; production
// uses the default.
func (m *Manager) SetBackoff(base time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backoffBase = base
}

// SetMachines reconciles the remote connections with the saved catalog. A
// machine that was disabled or removed is disconnected and forgotten; one that
// is already connected keeps its client and snapshot.
func (m *Manager) SetMachines(machines []machine.Machine) {
	m.mu.Lock()
	wanted := make(map[string]machine.Machine, len(machines))
	for _, saved := range machines {
		if saved.Enabled {
			wanted[saved.ID] = saved
		}
	}
	var closing []*ipc.Client
	for id, connection := range m.remotes {
		if _, keep := wanted[id]; !keep {
			if connection.client != nil {
				closing = append(closing, connection.client)
			}
			delete(m.remotes, id)
		}
	}
	for id, saved := range wanted {
		if existing, ok := m.remotes[id]; ok {
			if existing.Machine.Host != saved.Host || existing.Machine.Session != saved.Session {
				// The saved target moved under a stable ID: the old client
				// still dials the old host, so drop it and dial fresh.
				if existing.client != nil {
					closing = append(closing, existing.client)
				}
				m.remotes[id] = &Connection{Machine: saved, State: Offline}
				continue
			}
			existing.Machine = saved
			continue
		}
		m.remotes[id] = &Connection{Machine: saved, State: Offline}
	}
	m.mu.Unlock()
	// Closing a remote client reaps its ssh child, which can block; do it with
	// the lock released so it cannot stall every other machine.
	for _, client := range closing {
		client.Close()
	}
}

// Close releases every remote connection.
func (m *Manager) Close() {
	m.mu.Lock()
	var closing []*ipc.Client
	for _, connection := range m.remotes {
		if connection.client != nil {
			closing = append(closing, connection.client)
			connection.client = nil
		}
	}
	m.mu.Unlock()
	for _, client := range closing {
		client.Close()
	}
}

// Refresh probes every machine, recording health and refreshing the snapshot
// of the ones that answer. A machine that just failed is not retried until its
// backoff elapses, so a dead host does not stall the others.
func (m *Manager) Refresh(ctx context.Context) {
	m.refreshLocal(ctx)

	m.mu.Lock()
	type target struct {
		id         string
		connection *Connection
	}
	targets := make([]target, 0, len(m.remotes))
	for id, connection := range m.remotes {
		targets = append(targets, target{id, connection})
	}
	m.mu.Unlock()
	// Sort by the id copied under the lock; reading Connection.Machine here
	// would race a concurrent SetMachines.
	sort.Slice(targets, func(left, right int) bool { return targets[left].id < targets[right].id })
	// Refresh concurrently: one slow or dead host must not stall the others
	// behind its dial and ping timeouts. Each machine carries its own
	// timeouts and backoff, so a refresh costs the slowest machine, not the
	// sum of them.
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.refreshRemote(ctx, target.id, target.connection)
		}()
	}
	wg.Wait()
}

func (m *Manager) refreshLocal(ctx context.Context) {
	m.mu.Lock()
	client := m.localClient
	m.mu.Unlock()
	if client == nil {
		return
	}
	var status map[string]string
	pingContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Call(pingContext, "system.ping", nil, &status); err != nil {
		m.mu.Lock()
		m.local.State = Offline
		m.local.Err = err.Error()
		m.mu.Unlock()
		return
	}
	var snapshot daemon.Snapshot
	snapshotContext, snapshotCancel := context.WithTimeout(ctx, 5*time.Second)
	defer snapshotCancel()
	snapshotErr := client.Call(snapshotContext, "system.snapshot", nil, &snapshot)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.local.State = Online
	m.local.Err = ""
	m.local.Version = status["version"]
	if snapshotErr == nil {
		m.local.Snapshot = snapshot
	}
}

func (m *Manager) refreshRemote(ctx context.Context, id string, connection *Connection) {
	m.mu.Lock()
	if m.remotes[id] != connection {
		m.mu.Unlock()
		return
	}
	if !connection.retryAt.IsZero() && time.Now().Before(connection.retryAt) {
		m.mu.Unlock()
		return
	}
	client := connection.client
	saved := connection.Machine
	if client == nil {
		if connection.dialing {
			// Another refresh is already dialing this machine.
			m.mu.Unlock()
			return
		}
		connection.dialing = true
	}
	m.mu.Unlock()

	if client == nil {
		dialed, err := m.dial(ctx, saved)
		m.mu.Lock()
		connection.dialing = false
		if err != nil {
			detached := m.recordFailureLocked(connection, err)
			m.mu.Unlock()
			if detached != nil {
				detached.Close()
			}
			return
		}
		if m.remotes[id] != connection {
			// The machine was removed while dialing; do not leak the client.
			m.mu.Unlock()
			dialed.Close()
			return
		}
		connection.client = dialed
		client = dialed
		m.mu.Unlock()
	}

	var status map[string]string
	pingContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := client.Call(pingContext, "system.ping", nil, &status)
	cancel()
	if err != nil {
		m.recordFailure(connection, err)
		return
	}
	var snapshot daemon.Snapshot
	snapshotContext, snapshotCancel := context.WithTimeout(ctx, 10*time.Second)
	err = client.Call(snapshotContext, "system.snapshot", nil, &snapshot)
	snapshotCancel()
	if err != nil {
		m.recordFailure(connection, err)
		return
	}

	m.mu.Lock()
	connection.State = Online
	connection.Err = ""
	connection.Version = status["version"]
	connection.Snapshot = snapshot
	connection.failures = 0
	connection.retryAt = time.Time{}
	m.mu.Unlock()
}

func (m *Manager) recordFailure(connection *Connection, err error) {
	m.mu.Lock()
	client := m.recordFailureLocked(connection, err)
	m.mu.Unlock()
	if client != nil {
		client.Close()
	}
}

// recordFailureLocked records the failure and detaches the connection's client
// so the caller can close it with the lock released. m.mu must be held.
func (m *Manager) recordFailureLocked(connection *Connection, err error) *ipc.Client {
	connection.failures++
	connection.State = Offline
	connection.Err = err.Error()
	var client *ipc.Client
	if connection.client != nil {
		client = connection.client
		connection.client = nil
	}
	backoff := m.backoffBase
	for attempt := 1; attempt < connection.failures && backoff < time.Minute; attempt++ {
		backoff *= 2
	}
	// The loop above can double once past the cap (64s from a 2s base), so
	// clamp: retryAt is a promise about the next attempt, not an estimate.
	if backoff > time.Minute {
		backoff = time.Minute
	}
	connection.retryAt = time.Now().Add(backoff)
	return client
}

// MachineStatus is one machine's health, for a status line.
type MachineStatus struct {
	Machine machine.Machine
	Local   bool
	State   State
	Err     string
	Version string
}

// Status returns every machine's health, local first.
func (m *Manager) Status() []MachineStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() []MachineStatus {
	statuses := make([]MachineStatus, 0, len(m.remotes)+1)
	statuses = append(statuses, MachineStatus{Machine: m.local.Machine, Local: true, State: m.local.State, Err: m.local.Err, Version: m.local.Version})
	for _, connection := range m.remotes {
		statuses = append(statuses, MachineStatus{Machine: connection.Machine, State: connection.State, Err: connection.Err, Version: connection.Version})
	}
	sort.Slice(statuses, func(left, right int) bool {
		if statuses[left].Local != statuses[right].Local {
			return statuses[left].Local
		}
		return statuses[left].Machine.Label < statuses[right].Machine.Label
	})
	return statuses
}

// ScopedWorkspace, ScopedAgent and ScopedTask are entities tagged with the
// machine they came from, since two machines can both have a "w1" or a
// "term_abc".
type ScopedWorkspace struct {
	MachineID    string
	MachineLabel string
	Workspace    daemon.Workspace
}

type ScopedAgent struct {
	MachineID    string
	MachineLabel string
	Agent        daemon.Agent
}

type ScopedTask struct {
	MachineID    string
	MachineLabel string
	Task         workflow.Task
}

// Board is every online machine's board, merged.
type Board struct {
	Machines   []MachineStatus
	Workspaces []ScopedWorkspace
	Agents     []ScopedAgent
	Tasks      []ScopedTask
	// Attention counts the agents across machines waiting on a person.
	Attention int
}

// Board merges the snapshots of every online machine, local first.
func (m *Manager) Board() Board {
	m.mu.Lock()
	defer m.mu.Unlock()

	board := Board{Machines: m.statusLocked()}
	appendSnapshot := func(id, label string, snapshot daemon.Snapshot) {
		for _, workspace := range snapshot.Workspaces {
			board.Workspaces = append(board.Workspaces, ScopedWorkspace{MachineID: id, MachineLabel: label, Workspace: workspace})
		}
		for _, agent := range snapshot.Agents {
			board.Agents = append(board.Agents, ScopedAgent{MachineID: id, MachineLabel: label, Agent: agent})
			if isAttention(agent.State) {
				board.Attention++
			}
		}
		for _, task := range snapshot.Tasks {
			board.Tasks = append(board.Tasks, ScopedTask{MachineID: id, MachineLabel: label, Task: task})
		}
	}

	if m.local.State == Online {
		appendSnapshot(LocalID, m.local.Machine.Label, m.local.Snapshot)
	}
	ids := make([]string, 0, len(m.remotes))
	for id := range m.remotes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		connection := m.remotes[id]
		if connection.State == Online {
			appendSnapshot(id, connection.Machine.Label, connection.Snapshot)
		}
	}
	return board
}

// ClientFor returns the client that can act on a machine's entities. LocalID,
// or an empty id, returns the local client.
func (m *Manager) ClientFor(machineID string) (*ipc.Client, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if machineID == "" || machineID == LocalID {
		return m.localClient, m.localClient != nil
	}
	connection, ok := m.remotes[machineID]
	if !ok || connection.client == nil {
		return nil, false
	}
	return connection.client, true
}

func isAttention(state string) bool {
	switch state {
	case "waiting_input", "waiting_permission", "waiting_resource":
		return true
	default:
		return false
	}
}

// String is a short label for a machine, used in logs and status lines.
func (s MachineStatus) String() string {
	label := s.Machine.Label
	if s.Err != "" {
		return fmt.Sprintf("%s\t%s\t%s", label, s.State, s.Err)
	}
	return fmt.Sprintf("%s\t%s", label, s.State)
}
