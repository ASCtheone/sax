package theme

import "github.com/charmbracelet/lipgloss"

var (
	// StatusBar styles
	StatusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#3c3836")).
			Foreground(lipgloss.Color("#ebdbb2")).
			Padding(0, 1)

	StatusBarAccent = lipgloss.NewStyle().
			Background(lipgloss.Color("#458588")).
			Foreground(lipgloss.Color("#ebdbb2")).
			Padding(0, 1).
			Bold(true)

	StatusBarPort = lipgloss.NewStyle().
			Background(lipgloss.Color("#689d6a")).
			Foreground(lipgloss.Color("#1d2021")).
			Padding(0, 1)

	// Tab bar styles
	TabActive = lipgloss.NewStyle().
			Background(lipgloss.Color("#458588")).
			Foreground(lipgloss.Color("#ebdbb2")).
			Padding(0, 1).
			Bold(true)

	TabInactive = lipgloss.NewStyle().
			Background(lipgloss.Color("#3c3836")).
			Foreground(lipgloss.Color("#a89984")).
			Padding(0, 1)

	TabBarBg = lipgloss.NewStyle().
			Background(lipgloss.Color("#282828")).
			Foreground(lipgloss.Color("#a89984"))

	// Pane border styles
	PaneBorderActive = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#458588"))

	PaneBorderInactive = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#504945"))

	// Help overlay
	HelpStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#1d2021")).
			Foreground(lipgloss.Color("#ebdbb2")).
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#458588"))

	// Mode indicator
	ModeNormal = lipgloss.NewStyle().
			Background(lipgloss.Color("#458588")).
			Foreground(lipgloss.Color("#ebdbb2")).
			Padding(0, 1).
			Bold(true)

	ModePrefix = lipgloss.NewStyle().
			Background(lipgloss.Color("#d79921")).
			Foreground(lipgloss.Color("#1d2021")).
			Padding(0, 1).
			Bold(true)
)
