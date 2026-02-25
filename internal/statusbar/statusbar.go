package statusbar

import (
	"fmt"
	"strings"

	"github.com/asc/sax/internal/theme"
	"github.com/charmbracelet/lipgloss"
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
	if len(portParts) > 0 {
		right = strings.Join(portParts, " ")
	}

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := width - leftW - rightW
	if gap < 0 {
		gap = 0
	}

	bar := left + theme.StatusBarStyle.Render(strings.Repeat(" ", gap)) + right

	barW := lipgloss.Width(bar)
	if barW < width {
		bar += theme.StatusBarStyle.Render(strings.Repeat(" ", width-barW))
	}

	return bar
}
