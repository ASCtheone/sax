package statusbar

import (
	"fmt"
	"strings"

	"github.com/asc/sax/internal/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// PortInfo represents a detected port for display.
type PortInfo struct {
	Port    int
	Process string
}

// Mode represents the current input mode.
type Mode int

const (
	ModeNormal Mode = iota
	ModePrefix
)

// Render draws the status bar across the given width.
func Render(mode Mode, ports []PortInfo, paneInfo string, width int) string {
	var left, right string

	switch mode {
	case ModePrefix:
		left = theme.ModePrefix.Render(" PREFIX ")
	default:
		left = theme.ModeNormal.Render(" SAX ")
	}

	if paneInfo != "" {
		left += " " + theme.StatusBarStyle.Render(paneInfo)
	}

	var portParts []string
	for _, p := range ports {
		label := fmt.Sprintf(":%d", p.Port)
		if p.Process != "" {
			label = fmt.Sprintf("%s :%d", p.Process, p.Port)
		}
		portParts = append(portParts, theme.StatusBarPort.Render(label))
	}
	// Show whole ports only: drop the first ones until the rest fits.
	for len(portParts) > 0 && lipgloss.Width(left)+lipgloss.Width(strings.Join(portParts, " ")) > width {
		portParts = portParts[1:]
	}
	if len(portParts) > 0 {
		right = strings.Join(portParts, " ")
	}

	// The filler must be exactly gap columns: StatusBarStyle has horizontal
	// padding, which would push the bar past the width and get its end (the
	// last port's digits) cut off by the terminal.
	fill := theme.StatusBarStyle.UnsetPadding()
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	bar := left + fill.Render(strings.Repeat(" ", gap)) + right

	if barW := lipgloss.Width(bar); barW > width {
		bar = ansi.Truncate(bar, width, "") // too narrow for everything
	} else if barW < width {
		bar += fill.Render(strings.Repeat(" ", width-barW))
	}
	return bar
}
