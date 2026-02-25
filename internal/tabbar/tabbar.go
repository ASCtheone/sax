package tabbar

import (
	"strings"

	"github.com/asc/sax/internal/theme"
	"github.com/charmbracelet/lipgloss"
)

// Render draws the tab bar across the given width.
func Render(tabs []string, activeIdx, width int) string {
	var parts []string

	for i, name := range tabs {
		var style lipgloss.Style
		if i == activeIdx {
			style = theme.TabActive
		} else {
			style = theme.TabInactive
		}
		parts = append(parts, style.Render(name))
	}

	bar := strings.Join(parts, " ")

	barLen := lipgloss.Width(bar)
	if barLen < width {
		bar += theme.TabBarBg.Render(strings.Repeat(" ", width-barLen))
	}

	return bar
}
