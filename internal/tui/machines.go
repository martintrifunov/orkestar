package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/federation"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/machine"
)

// Machines were configurable only through `orkestar machine ...`; the
// interface that switches between them had no way to add or remove one. The
// overlay edits the same catalog under the same lock, so a form in the TUI
// and a command in another terminal cannot clobber each other.

type machinesListedMsg struct {
	machines []machine.Machine
	err      error
}

type machineAddedMsg struct {
	saved  machine.Machine
	client *ipc.Client
	err    error
}

type machineRemovedMsg struct {
	id  string
	err error
}

type machineSetEnabledMsg struct {
	saved  machine.Machine
	client *ipc.Client
	err    error
}

// loadMachines reads the saved catalog for the overlay.
func (m Model) loadMachines() tea.Cmd {
	path := m.machineCatalogPath
	return func() tea.Msg {
		catalog, err := machine.Load(path)
		if err != nil {
			return machinesListedMsg{err: err}
		}
		return machinesListedMsg{machines: catalog.List()}
	}
}

// mutateCatalog runs one catalog change under the cross-process lock. The
// lock matters more here than in a CLI verb that returns: two interfaces on
// one machine are exactly how a save gets lost.
func (m Model) mutateCatalog(change func(catalog *machine.Catalog) error) error {
	release, err := machine.AcquireCatalogLock(m.machineCatalogPath)
	if err != nil {
		return err
	}
	defer release()
	catalog, err := machine.Load(m.machineCatalogPath)
	if err != nil {
		return err
	}
	if err := change(catalog); err != nil {
		return err
	}
	return catalog.Save()
}

func (m Model) addMachine(host, label, session string) tea.Cmd {
	return func() tea.Msg {
		var added machine.Machine
		err := m.mutateCatalog(func(catalog *machine.Catalog) error {
			saved, err := catalog.Add(label, host, session)
			if err != nil {
				return err
			}
			added = saved
			return nil
		})
		if err != nil {
			return machineAddedMsg{err: err}
		}
		client, err := federation.RemoteDialer(context.Background(), added)
		return machineAddedMsg{saved: added, client: client, err: err}
	}
}

func (m Model) removeMachine(id string) tea.Cmd {
	return func() tea.Msg {
		err := m.mutateCatalog(func(catalog *machine.Catalog) error {
			return catalog.Remove(id)
		})
		return machineRemovedMsg{id: id, err: err}
	}
}

func (m Model) setMachineEnabled(id string, enabled bool) tea.Cmd {
	return func() tea.Msg {
		var updated machine.Machine
		err := m.mutateCatalog(func(catalog *machine.Catalog) error {
			saved, err := catalog.SetEnabled(id, enabled)
			if err != nil {
				return err
			}
			updated = saved
			return nil
		})
		if err != nil {
			return machineSetEnabledMsg{err: err}
		}
		var client *ipc.Client
		if enabled {
			dialed, err := federation.RemoteDialer(context.Background(), updated)
			if err != nil {
				return machineSetEnabledMsg{saved: updated, err: err}
			}
			client = dialed
		}
		return machineSetEnabledMsg{saved: updated, client: client}
	}
}

// openMachines shows the overlay. A --remote interface has no catalog of its
// own to manage: the profiles belong to the machine the person is sitting at.
func (m *Model) openMachines() tea.Cmd {
	if m.machineCatalogPath == "" {
		m.notice = "Machine management is unavailable in a --remote session."
		return nil
	}
	m.managingMachines = true
	m.manageAt = 0
	m.machinesErr = nil
	m.savedMachines = nil
	m.addingMachine = false
	m.machineHost, m.machineLabel, m.machineSession, m.machineField = "", "", "", 0
	return m.loadMachines()
}

func (m Model) selectedSavedMachine() (machine.Machine, bool) {
	if m.manageAt < 0 || m.manageAt >= len(m.savedMachines) {
		return machine.Machine{}, false
	}
	return m.savedMachines[m.manageAt], true
}

// canLeaveMachine reports whether the session may stop watching a machine
// right now. Leaving the selected one closes its panes, so a dirty editor has
// to be dealt with first, the same as a machine switch.
func (m Model) canLeaveMachine(id string) bool {
	if m.currentMachine().ID != id {
		return true
	}
	for _, pane := range m.visiblePanes() {
		if !m.canClose(pane) {
			return false
		}
	}
	return true
}

func (m *Model) machinesView() string {
	if m.addingMachine {
		return "Add machine\n\nHost: " + m.machineCursor(0, m.machineHost) +
			"\nLabel: " + m.machineCursor(1, m.machineLabel) +
			"\nSession: " + m.machineCursor(2, m.machineSession) +
			"\n\nHost is an ssh target such as user@host; label defaults to it, and a session is the\nremote named daemon to attach to.\n\nEnter saves · Tab switches field · Esc cancels"
	}
	lines := []string{"Machines", "", "  Local  " + m.currentMachine().Label + "  (this daemon)"}
	if len(m.savedMachines) == 0 {
		lines = append(lines, m.theme.dim.Render("  No saved machines. Press a to add one."))
	}
	for index, saved := range m.savedMachines {
		state := "enabled"
		if !saved.Enabled {
			state = "disabled"
		}
		detail := saved.Host
		if saved.Session != "" {
			detail += "  session " + saved.Session
		}
		if m.currentMachine().ID == saved.ID {
			detail += "  · on screen"
		}
		line := fmt.Sprintf("%-14s %-9s %s", saved.Label, state, detail)
		if index == m.manageAt {
			line = m.theme.selected.Render(" " + line + " ")
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	if m.machinesErr != nil {
		lines = append(lines, "", m.theme.error.Render(m.machinesErr.Error()))
	}
	return strings.Join(lines, "\n") + "\n\na add · e enable/disable · x remove · esc close"
}

func (m Model) machineCursor(field int, value string) string {
	if m.machineField == field {
		return value + "▏"
	}
	return value
}

func (m *Model) machineInput() *string {
	switch m.machineField {
	case 1:
		return &m.machineLabel
	case 2:
		return &m.machineSession
	}
	return &m.machineHost
}

func (m Model) updateMachines(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.addingMachine {
		return m.updateMachineForm(k)
	}
	switch k.String() {
	case "esc", "q":
		m.managingMachines = false
	case "up", "k":
		if m.manageAt > 0 {
			m.manageAt--
		}
	case "down", "j":
		if m.manageAt+1 < len(m.savedMachines) {
			m.manageAt++
		}
	case "a":
		m.addingMachine = true
		m.machineHost, m.machineLabel, m.machineSession, m.machineField = "", "", "", 0
		m.machinesErr = nil
	case "e":
		if saved, ok := m.selectedSavedMachine(); ok && (!saved.Enabled || m.canLeaveMachine(saved.ID)) {
			m.machinesErr = nil
			return m, m.setMachineEnabled(saved.ID, !saved.Enabled)
		}
	case "x":
		if saved, ok := m.selectedSavedMachine(); ok && m.canLeaveMachine(saved.ID) {
			m.machinesErr = nil
			return m, m.removeMachine(saved.ID)
		}
	}
	return m, nil
}

func (m Model) updateMachineForm(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.Code {
	case tea.KeyEscape:
		m.addingMachine = false
		return m, nil
	case tea.KeyTab:
		m.machineField = (m.machineField + 1) % 3
		return m, nil
	case tea.KeyEnter:
		host := strings.TrimSpace(m.machineHost)
		if host == "" {
			m.machineField = 0
			m.machinesErr = fmt.Errorf("a machine needs an ssh target, such as user@host")
			return m, nil
		}
		m.addingMachine = false
		return m, m.addMachine(host, m.machineLabel, m.machineSession)
	case tea.KeyBackspace:
		field := m.machineInput()
		if r := []rune(*field); len(r) > 0 {
			*field = string(r[:len(r)-1])
		}
		return m, nil
	}
	field := m.machineInput()
	if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		*field += k.Text
	} else if k.Mod == 0 && k.Code >= 32 && k.Code < 127 {
		*field += string(k.Code)
	}
	return m, nil
}

// addMachineToSession attaches a dialed machine so the switcher and the
// merged board can use it without a restart.
func (m *Model) addMachineToSession(saved machine.Machine, client *ipc.Client) {
	if client == nil {
		return
	}
	for _, existing := range m.machines {
		if existing.ID == saved.ID {
			return
		}
	}
	m.machines = append(m.machines, Machine{
		ID:         saved.ID,
		Label:      saved.Label,
		Client:     client,
		LayoutPath: m.machineLayoutPath(saved.ID),
	})
}

func (m Model) machineLayoutPath(id string) string {
	if m.machineLayoutRoot == "" || strings.ContainsAny(id, `/\`) {
		return ""
	}
	return filepath.Join(m.machineLayoutRoot, "machines", id, "layout.json")
}

// detachSavedMachine stops watching a machine in this session, selecting the
// local daemon first when it was the one on screen. It reports whether the
// machine was actually detached; a refusal means a pane would not close.
func (m *Model) detachSavedMachine(id string) bool {
	index := -1
	for i, candidate := range m.machines {
		if candidate.ID == id {
			index = i
			break
		}
	}
	if index <= 0 {
		return false
	}
	if m.machineIndex == index {
		m.switchMachine(-index)
		if m.machineIndex == index {
			return false
		}
	} else if m.machineIndex > index {
		m.machineIndex--
	}
	removed := m.machines[index]
	m.machines = append(m.machines[:index], m.machines[index+1:]...)
	if removed.Client != nil {
		removed.Client.Close()
	}
	views := m.remote[:0]
	for _, view := range m.remote {
		if view.ID != id {
			views = append(views, view)
		}
	}
	m.remote = views
	return true
}

// dropSavedMachine removes a profile from the overlay's list.
func (m *Model) dropSavedMachine(id string) {
	kept := m.savedMachines[:0]
	for _, saved := range m.savedMachines {
		if saved.ID != id {
			kept = append(kept, saved)
		}
	}
	m.savedMachines = kept
	if m.manageAt >= len(m.savedMachines) {
		m.manageAt = max(0, len(m.savedMachines)-1)
	}
}

// forgetMachineLayout drops a removed machine's remembered panes. Re-adding
// later should not restore a dead arrangement.
func (m Model) forgetMachineLayout(id string) {
	if m.machineLayoutRoot == "" || strings.ContainsAny(id, `/\`) {
		return
	}
	_ = os.RemoveAll(filepath.Join(m.machineLayoutRoot, "machines", id))
}

// sortSavedMachines keeps the overlay in the catalog's own order, which is by
// label, after an add.
func sortSavedMachines(machines []machine.Machine) {
	sort.Slice(machines, func(left, right int) bool {
		if machines[left].Label != machines[right].Label {
			return machines[left].Label < machines[right].Label
		}
		return machines[left].ID < machines[right].ID
	})
}
