package theme

import (
	"fmt"
	"strings"

	"github.com/asc/sax/internal/config"
	"github.com/charmbracelet/lipgloss"
)

var (
	// StatusBar styles
	StatusBarStyle  lipgloss.Style
	StatusBarAccent lipgloss.Style
	StatusBarPort   lipgloss.Style

	// Tab bar styles
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	TabBarBg    lipgloss.Style

	// Pane border styles
	PaneBorderActive   lipgloss.Style
	PaneBorderInactive lipgloss.Style

	// Help overlay
	HelpStyle lipgloss.Style

	// Command panel styles
	CommandHeaderStyle lipgloss.Style
	CommandKeyStyle    lipgloss.Style

	// Mode indicator
	ModeNormal lipgloss.Style
	ModePrefix lipgloss.Style

	// Border color raw ANSI sequences
	BorderActiveColor   string
	BorderInactiveColor string
)

// PUA marker runes for border classification
const (
	PUABorderActiveV   = '\uE000'
	PUABorderActiveH   = '\uE001'
	PUABorderInactiveV = '\uE002'
	PUABorderInactiveH = '\uE003'
)

func init() {
	Init(config.DefaultTheme())
}

// Init builds all lipgloss style vars from the given theme colors.
func Init(t config.Theme) {
	StatusBarStyle = lipgloss.NewStyle().
		Background(lipgloss.Color(t.Surface)).
		Foreground(lipgloss.Color(t.Fg)).
		Padding(0, 1)

	StatusBarAccent = lipgloss.NewStyle().
		Background(lipgloss.Color(t.Accent)).
		Foreground(lipgloss.Color(t.Fg)).
		Padding(0, 1).
		Bold(true)

	StatusBarPort = lipgloss.NewStyle().
		Background(lipgloss.Color(t.Success)).
		Foreground(lipgloss.Color(t.SurfaceDark)).
		Padding(0, 1)

	TabActive = lipgloss.NewStyle().
		Background(lipgloss.Color(t.Accent)).
		Foreground(lipgloss.Color(t.Fg)).
		Padding(0, 1).
		Bold(true)

	TabInactive = lipgloss.NewStyle().
		Background(lipgloss.Color(t.Surface)).
		Foreground(lipgloss.Color(t.FgMuted)).
		Padding(0, 1)

	TabBarBg = lipgloss.NewStyle().
		Background(lipgloss.Color(t.Bg)).
		Foreground(lipgloss.Color(t.FgMuted))

	PaneBorderActive = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Accent))

	PaneBorderInactive = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.BorderInactive))

	HelpStyle = lipgloss.NewStyle().
		Background(lipgloss.Color(t.SurfaceDark)).
		Foreground(lipgloss.Color(t.Fg)).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.Accent))

	CommandHeaderStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Accent)).
		Bold(true)

	CommandKeyStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Accent))

	ModeNormal = lipgloss.NewStyle().
		Background(lipgloss.Color(t.Accent)).
		Foreground(lipgloss.Color(t.Fg)).
		Padding(0, 1).
		Bold(true)

	ModePrefix = lipgloss.NewStyle().
		Background(lipgloss.Color(t.AccentSecondary)).
		Foreground(lipgloss.Color(t.SurfaceDark)).
		Padding(0, 1).
		Bold(true)

	BorderActiveColor = HexToANSI(t.Accent)
	BorderInactiveColor = HexToANSI(t.BorderInactive)
}

// HexToANSI converts a hex color like "#7dcfff" to an ANSI true-color foreground escape.
func HexToANSI(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return ""
	}
	var r, g, b int
	_, _ = fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// ClassifyBorders scans the rune grid and replaces plain border chars (│ ─)
// with PUA marker runes based on adjacency to the active pane rect.
func ClassifyBorders(grid [][]rune, activeX, activeY, activeW, activeH, width, height int) {
	for y := 0; y < height && y < len(grid); y++ {
		for x := 0; x < width && x < len(grid[y]); x++ {
			ch := grid[y][x]
			switch ch {
			case '\u2502': // │ vertical border
				if isAdjacentToActive(x, y, activeX, activeY, activeW, activeH) {
					grid[y][x] = PUABorderActiveV
				} else {
					grid[y][x] = PUABorderInactiveV
				}
			case '\u2500': // ─ horizontal border
				if isAdjacentToActive(x, y, activeX, activeY, activeW, activeH) {
					grid[y][x] = PUABorderActiveH
				} else {
					grid[y][x] = PUABorderInactiveH
				}
			}
		}
	}
}

// isAdjacentToActive checks if a border position is directly adjacent to the active pane rect.
func isAdjacentToActive(bx, by, ax, ay, aw, ah int) bool {
	// Vertical border: immediately left (bx == ax-1) or right (bx == ax+aw) of active pane
	// and within the pane's vertical span
	if by >= ay && by < ay+ah {
		if bx == ax-1 || bx == ax+aw {
			return true
		}
	}
	// Horizontal border: immediately above (by == ay-1) or below (by == ay+ah) of active pane
	// and within the pane's horizontal span
	if bx >= ax && bx < ax+aw {
		if by == ay-1 || by == ay+ah {
			return true
		}
	}
	return false
}

// ColorizeBorderRow replaces PUA marker runes in a string with ANSI-colored border chars.
func ColorizeBorderRow(row string) string {
	reset := "\x1b[0m"
	var b strings.Builder
	b.Grow(len(row) * 2)
	for _, r := range row {
		switch r {
		case PUABorderActiveV:
			b.WriteString(BorderActiveColor)
			b.WriteRune('\u2502')
			b.WriteString(reset)
		case PUABorderActiveH:
			b.WriteString(BorderActiveColor)
			b.WriteRune('\u2500')
			b.WriteString(reset)
		case PUABorderInactiveV:
			b.WriteString(BorderInactiveColor)
			b.WriteRune('\u2502')
			b.WriteString(reset)
		case PUABorderInactiveH:
			b.WriteString(BorderInactiveColor)
			b.WriteRune('\u2500')
			b.WriteString(reset)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
