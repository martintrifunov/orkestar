// Package machine stores the SSH machines Orkestar can attach to. A profile is
// a label, an ssh target and an optional remote session; the daemon stays on
// that machine and a client reaches it over ssh exactly as --remote does. The
// catalog is configuration, kept under the user config directory, and holds no
// credentials: authentication is left to ssh.
package machine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

// Machine is one saved ssh target.
type Machine struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Host    string `json:"host"`
	Session string `json:"session,omitempty"`
	Enabled bool   `json:"enabled"`
}

// Catalog is the saved machine list, bound to the file it was loaded from.
type Catalog struct {
	path     string
	Machines []Machine `json:"machines"`
}

// Load reads the catalog at path. A missing file is an empty catalog rather
// than an error: machines are optional.
func Load(path string) (*Catalog, error) {
	catalog := &Catalog{path: path}
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return catalog, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read machine catalog %s: %w", path, err)
	}
	if err := json.Unmarshal(encoded, catalog); err != nil {
		return nil, fmt.Errorf("decode machine catalog %s: %w", path, err)
	}
	catalog.path = path
	return catalog, nil
}

// Save writes the catalog, creating its directory. It is sorted by label so
// the file is stable between writes.
func (c *Catalog) Save() error {
	if c.path == "" {
		return errors.New("machine catalog has no path")
	}
	sort.Slice(c.Machines, func(left, right int) bool {
		if c.Machines[left].Label != c.Machines[right].Label {
			return c.Machines[left].Label < c.Machines[right].Label
		}
		return c.Machines[left].ID < c.Machines[right].ID
	})
	encoded, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("create machine catalog directory: %w", err)
	}
	return os.WriteFile(c.path, encoded, 0o600)
}

// List returns the machines sorted by label.
func (c *Catalog) List() []Machine {
	ordered := append([]Machine(nil), c.Machines...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Label != ordered[right].Label {
			return ordered[left].Label < ordered[right].Label
		}
		return ordered[left].ID < ordered[right].ID
	})
	return ordered
}

// Add saves a machine, defaulting its label to the host and rejecting a
// duplicate target. The host is validated the way --remote validates it.
func (c *Catalog) Add(label, host, session string) (Machine, error) {
	// Trim first so validation, duplicate detection and the stored value all
	// agree: otherwise " workbox" and "workbox" would look like two machines.
	host = strings.TrimSpace(host)
	if _, err := ipc.ParseRemote(host); err != nil {
		return Machine{}, err
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = host
	}
	session = strings.TrimSpace(session)
	for _, existing := range c.Machines {
		if existing.Host == host && existing.Session == session {
			return Machine{}, fmt.Errorf("a machine for %s already exists", host)
		}
	}
	id, err := newID()
	if err != nil {
		return Machine{}, err
	}
	machine := Machine{ID: id, Label: label, Host: host, Session: session, Enabled: true}
	c.Machines = append(c.Machines, machine)
	return machine, nil
}

// Find returns a saved machine by ID.
func (c *Catalog) Find(id string) (Machine, error) {
	index, err := c.index(id)
	if err != nil {
		return Machine{}, err
	}
	return c.Machines[index], nil
}

// Rename changes a machine's displayed label.
func (c *Catalog) Rename(id, label string) (Machine, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return Machine{}, errors.New("a machine label cannot be empty")
	}
	index, err := c.index(id)
	if err != nil {
		return Machine{}, err
	}
	c.Machines[index].Label = label
	return c.Machines[index], nil
}

// SetEnabled turns a saved machine on or off without forgetting it.
func (c *Catalog) SetEnabled(id string, enabled bool) (Machine, error) {
	index, err := c.index(id)
	if err != nil {
		return Machine{}, err
	}
	c.Machines[index].Enabled = enabled
	return c.Machines[index], nil
}

// Remove forgets a machine.
func (c *Catalog) Remove(id string) error {
	index, err := c.index(id)
	if err != nil {
		return err
	}
	c.Machines = append(c.Machines[:index], c.Machines[index+1:]...)
	return nil
}

func (c *Catalog) index(id string) (int, error) {
	for index, candidate := range c.Machines {
		if candidate.ID == id {
			return index, nil
		}
	}
	return -1, fmt.Errorf("machine %q does not exist", id)
}

func newID() (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate machine ID: %w", err)
	}
	return "m_" + hex.EncodeToString(raw), nil
}
