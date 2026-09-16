package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// theme is the resolved styling for the interface. It lives on the model as a
// value rather than in package variables so two models can be rendered at once
// — which the tests do, in parallel — without one theme bleeding into another.
type theme struct {
	name        string
	accent      lipgloss.Style
	dim         lipgloss.Style
	selected    lipgloss.Style
	panel       lipgloss.Style
	error       lipgloss.Style
	directory   lipgloss.Style
	accentColor color.Color
}

// ThemeNames lists the palettes a user can name in tui.json, in cycle order.
func ThemeNames() []string { return []string{"orkestar", "light"} }

// themes holds the palettes. "orkestar" is the original dark palette and the
// default; "light" is for a light terminal background.
var themes = map[string]theme{
	"orkestar": {
		name:        "orkestar",
		accent:      lipgloss.NewStyle().Foreground(lipgloss.Color("#D7A84B")).Bold(true),
		dim:         lipgloss.NewStyle().Foreground(lipgloss.Color("#777777")),
		selected:    lipgloss.NewStyle().Foreground(lipgloss.Color("#111111")).Background(lipgloss.Color("#D7A84B")).Bold(true),
		panel:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F5F5F")).Padding(0, 1),
		error:       lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")),
		directory:   lipgloss.NewStyle().Foreground(lipgloss.Color("#78A9E8")),
		accentColor: lipgloss.Color("#D7A84B"),
	},
	"light": {
		name:        "light",
		accent:      lipgloss.NewStyle().Foreground(lipgloss.Color("#8C5A00")).Bold(true),
		dim:         lipgloss.NewStyle().Foreground(lipgloss.Color("#6B6B6B")),
		selected:    lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#8C5A00")).Bold(true),
		panel:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#B0B0B0")).Padding(0, 1),
		error:       lipgloss.NewStyle().Foreground(lipgloss.Color("#B00020")),
		directory:   lipgloss.NewStyle().Foreground(lipgloss.Color("#1A4FA0")),
		accentColor: lipgloss.Color("#8C5A00"),
	},
}

// resolveTheme returns the named palette, or the default for an unknown name
// so a typo in tui.json degrades to something usable rather than nothing.
func resolveTheme(name string) theme {
	if resolved, ok := themes[name]; ok {
		return resolved
	}
	return themes["orkestar"]
}
