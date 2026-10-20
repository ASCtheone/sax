package statusbar

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The bar must fill the width exactly: wider, and the terminal cuts the end
// off (the last digits of the right-most port).
func TestRenderFitsWidthExactly(t *testing.T) {
	ports := []PortInfo{{Port: 4200, Process: "node"}, {Port: 3000, Process: "node"}}
	for _, width := range []int{40, 80, 120, 200} {
		for _, info := range []string{"", "[2 panes]"} {
			bar := Render(ModeNormal, ports, info, width)
			if got := lipgloss.Width(bar); got != width {
				t.Errorf("width %d, paneInfo %q: bar is %d columns", width, info, got)
			}
			plain := ansi.Strip(bar)
			for _, port := range regexp.MustCompile(`:\d+`).FindAllString(plain, -1) {
				if port != ":4200" && port != ":3000" {
					t.Errorf("width %d: partial port %q in %q", width, port, plain)
				}
			}
			if width >= 80 && !strings.Contains(plain, "node :4200") || !strings.Contains(plain, "node :3000") {
				t.Errorf("width %d: missing ports in %q", width, plain)
			}
		}
	}
}

func TestRenderNeverOverflows(t *testing.T) {
	ports := []PortInfo{{Port: 4200, Process: "node"}, {Port: 3000, Process: "node"}, {Port: 5432, Process: "postgres"}}
	for _, width := range []int{10, 20, 30} {
		if got := lipgloss.Width(Render(ModePrefix, ports, "[3 panes]", width)); got > width {
			t.Errorf("width %d: bar is %d columns", width, got)
		}
	}
}
