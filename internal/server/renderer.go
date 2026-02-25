package server

import (
	"fmt"
	"strings"

	"github.com/asc/sax/internal/session"
	"github.com/asc/sax/internal/statusbar"
	"github.com/asc/sax/internal/tabbar"
	"github.com/asc/sax/internal/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// RenderContext holds the state needed to render a frame.
type RenderContext struct {
	Session    *session.Session
	Mode       statusbar.Mode
	Ports      []statusbar.PortInfo
	ShowHelp   bool
	CopyMode   bool
	CopyOverlay string
	LockScreen bool
	LockPrompt string
	WindowList bool
	WindowListOverlay string
	Width      int
	Height     int
}

// RenderFrame produces the full ANSI screen string for a client.
func RenderFrame(ctx *RenderContext) string {
	if ctx.LockScreen {
		return renderLockScreen(ctx)
	}

	if ctx.CopyMode && ctx.CopyOverlay != "" {
		return ctx.CopyOverlay
	}

	var lines []string

	// Tab bar — only shown when 2+ tabs exist
	if ctx.Session.ShowTabBar() {
		tabNames := ctx.Session.TabNames()
		lines = append(lines, tabbar.Render(tabNames, ctx.Session.ActiveTab, ctx.Width))
	}

	// Pane area
	tab := ctx.Session.CurrentTab()
	if tab != nil {
		cols, rows := ctx.Session.PaneArea()
		paneArea := renderPanes(tab, cols, rows)
		lines = append(lines, paneArea)
	}

	// Status bar
	paneInfo := ""
	if tab != nil {
		paneCount := tab.Layout.PaneCount()
		if paneCount > 1 {
			paneInfo = fmt.Sprintf("[%d panes]", paneCount)
		}
	}
	lines = append(lines, statusbar.Render(ctx.Mode, ctx.Ports, paneInfo, ctx.Width))

	result := strings.Join(lines, "\n")

	// Overlay command panel on right side if active
	if ctx.ShowHelp {
		result = overlayRight(result, renderCommandPanel(ctx.Height), ctx.Width, ctx.Height)
	}

	// Overlay window list on top if active
	if ctx.WindowList && ctx.WindowListOverlay != "" {
		result = overlayCenter(result, ctx.WindowListOverlay, ctx.Width, ctx.Height)
	}

	return result
}

// renderPanes renders the pane area with borders.
func renderPanes(tab *session.Tab, width, height int) string {
	if tab.Zoomed {
		if p, ok := tab.Panes[tab.ZoomedPane]; ok {
			return p.Render()
		}
	}

	rects := tab.Layout.Arrange(session.Rect{X: 0, Y: 0, W: width, H: height})

	// Single pane — render directly
	if len(rects) == 1 {
		for _, p := range tab.Panes {
			return p.Render()
		}
	}

	// Multiple panes — composite with borders
	paneRenders := make(map[string]string)
	for id := range rects {
		if p, ok := tab.Panes[id]; ok {
			paneRenders[id] = p.Render()
		}
	}

	return compositePanes(tab, rects, paneRenders, width, height)
}

// compositePanes assembles pane content with borders.
func compositePanes(tab *session.Tab, rects map[string]session.Rect, renders map[string]string, width, height int) string {
	grid := make([][]rune, height)
	for y := range grid {
		grid[y] = make([]rune, width)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}

	drawNodeBorders(tab.Layout, session.Rect{X: 0, Y: 0, W: width, H: height}, grid)

	// Classify borders as active/inactive using PUA markers
	if ar, ok := rects[tab.ActivePane]; ok {
		theme.ClassifyBorders(grid, ar.X, ar.Y, ar.W, ar.H, width, height)
	}

	rows := make([]string, height)
	for y := 0; y < height; y++ {
		rows[y] = string(grid[y])
	}

	for id, r := range rects {
		content, ok := renders[id]
		if !ok {
			continue
		}
		contentLines := strings.Split(content, "\n")
		for dy := 0; dy < r.H && dy < len(contentLines); dy++ {
			targetY := r.Y + dy
			if targetY >= height {
				break
			}
			line := contentLines[dy]
			rowRunes := []rune(rows[targetY])
			lineRunes := []rune(line)
			for dx := 0; dx < r.W && dx < len(lineRunes); dx++ {
				targetX := r.X + dx
				if targetX >= width {
					break
				}
				if targetX < len(rowRunes) {
					rowRunes[targetX] = lineRunes[dx]
				}
			}
			rows[targetY] = string(rowRunes)
		}
	}

	// Final pass: colorize border PUA markers
	for y := range rows {
		rows[y] = theme.ColorizeBorderRow(rows[y])
	}

	return strings.Join(rows, "\n")
}

func drawNodeBorders(node *session.LayoutNode, area session.Rect, grid [][]rune) {
	if node.IsLeaf {
		return
	}

	switch node.Dir {
	case session.SplitVertical:
		leftW := int(float64(area.W) * node.Ratio)
		if leftW < 1 {
			leftW = 1
		}
		borderX := area.X + leftW
		if borderX < len(grid[0]) {
			for y := area.Y; y < area.Y+area.H && y < len(grid); y++ {
				grid[y][borderX] = '\u2502'
			}
		}
		rightW := area.W - leftW - 1
		if rightW < 1 {
			rightW = 1
		}
		drawNodeBorders(node.First, session.Rect{X: area.X, Y: area.Y, W: leftW, H: area.H}, grid)
		drawNodeBorders(node.Sec, session.Rect{X: area.X + leftW + 1, Y: area.Y, W: rightW, H: area.H}, grid)

	case session.SplitHorizontal:
		topH := int(float64(area.H) * node.Ratio)
		if topH < 1 {
			topH = 1
		}
		borderY := area.Y + topH
		if borderY < len(grid) {
			for x := area.X; x < area.X+area.W && x < len(grid[borderY]); x++ {
				grid[borderY][x] = '\u2500'
			}
		}
		botH := area.H - topH - 1
		if botH < 1 {
			botH = 1
		}
		drawNodeBorders(node.First, session.Rect{X: area.X, Y: area.Y, W: area.W, H: topH}, grid)
		drawNodeBorders(node.Sec, session.Rect{X: area.X, Y: area.Y + topH + 1, W: area.W, H: botH}, grid)
	}
}

// renderCommandPanel generates the right-side command overlay panel as a styled string.
func renderCommandPanel(height int) string {
	keyLine := func(key, desc string) string {
		return " " + theme.CommandKeyStyle.Render(key) + "  " + desc
	}

	sectionHeader := func(title string) string {
		return theme.CommandHeaderStyle.Render(title)
	}

	var content strings.Builder
	content.WriteString(sectionHeader("  SAX Commands") + "\n")
	content.WriteString("  Prefix: Ctrl+S\n")
	content.WriteString("\n")
	content.WriteString(sectionHeader("  Tabs") + "\n")
	content.WriteString(keyLine("c", "New tab") + "\n")
	content.WriteString(keyLine("n/p", "Next/Prev tab") + "\n")
	content.WriteString(keyLine("1-9", "Go to tab N") + "\n")
	content.WriteString(keyLine("X", "Close tab") + "\n")
	content.WriteString(keyLine(`"`, "Window list") + "\n")
	content.WriteString("\n")
	content.WriteString(sectionHeader("  Panes") + "\n")
	content.WriteString(keyLine("v |", "Split vertical") + "\n")
	content.WriteString(keyLine("s -", "Split horizontal") + "\n")
	content.WriteString(keyLine("hjkl", "Navigate panes") + "\n")
	content.WriteString(keyLine("x", "Close pane") + "\n")
	content.WriteString(keyLine("z", "Zoom pane") + "\n")
	content.WriteString("\n")
	content.WriteString(sectionHeader("  Session") + "\n")
	content.WriteString(keyLine("d", "Detach") + "\n")
	content.WriteString(keyLine("[", "Copy/scroll mode") + "\n")
	content.WriteString(keyLine("]", "Paste") + "\n")
	content.WriteString(keyLine("H", "Toggle logging") + "\n")
	content.WriteString(keyLine("^x", "Lock session") + "\n")
	content.WriteString(keyLine("M", "Monitor activity") + "\n")
	content.WriteString(keyLine("_", "Monitor silence") + "\n")
	content.WriteString("\n")
	content.WriteString(keyLine("^Q", "Quit") + "\n")
	content.WriteString(keyLine("?", "Toggle panel"))

	panel := theme.HelpStyle.Render(content.String())

	// Truncate to available height
	lines := strings.Split(panel, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}

	return strings.Join(lines, "\n")
}

// overlayRight places overlay text on the right side of the base frame (ANSI-aware).
func overlayRight(base string, overlay string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	oH := len(overlayLines)
	oW := 0
	for _, l := range overlayLines {
		w := lipgloss.Width(l)
		if w > oW {
			oW = w
		}
	}

	startY := (height - oH) / 2
	startX := width - oW - 1
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	for i, ol := range overlayLines {
		y := startY + i
		if y >= len(baseLines) {
			break
		}
		// Truncate the base line at the overlay start position, then append overlay
		truncated := ansi.Truncate(baseLines[y], startX, "")
		// Pad truncated line to startX visual width
		truncW := lipgloss.Width(truncated)
		if truncW < startX {
			truncated += strings.Repeat(" ", startX-truncW)
		}
		baseLines[y] = truncated + ol
	}

	return strings.Join(baseLines, "\n")
}

func renderLockScreen(ctx *RenderContext) string {
	lines := []string{
		"",
		"  SAX - Session Locked",
		"",
		"  " + ctx.LockPrompt,
		"",
	}

	totalLines := len(lines)
	padTop := (ctx.Height - totalLines) / 2
	if padTop < 0 {
		padTop = 0
	}

	var result []string
	for i := 0; i < padTop; i++ {
		result = append(result, strings.Repeat(" ", ctx.Width))
	}
	for _, line := range lines {
		padLeft := (ctx.Width - len(line)) / 2
		if padLeft < 0 {
			padLeft = 0
		}
		padded := strings.Repeat(" ", padLeft) + line
		if len(padded) < ctx.Width {
			padded += strings.Repeat(" ", ctx.Width-len(padded))
		}
		result = append(result, padded)
	}
	for len(result) < ctx.Height {
		result = append(result, strings.Repeat(" ", ctx.Width))
	}

	return strings.Join(result, "\n")
}

// overlayCenter places overlay text centered on top of the base frame (ANSI-aware).
func overlayCenter(base, overlay string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	oH := len(overlayLines)
	oW := 0
	for _, l := range overlayLines {
		w := lipgloss.Width(l)
		if w > oW {
			oW = w
		}
	}

	startY := (height - oH) / 2
	startX := (width - oW) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	for i, ol := range overlayLines {
		y := startY + i
		if y >= len(baseLines) {
			break
		}
		olW := lipgloss.Width(ol)
		endX := startX + olW

		// Left portion: truncate base at startX
		left := ansi.Truncate(baseLines[y], startX, "")
		leftW := lipgloss.Width(left)
		if leftW < startX {
			left += strings.Repeat(" ", startX-leftW)
		}

		// Right portion: skip past the overlay area in the base line
		baseW := lipgloss.Width(baseLines[y])
		right := ""
		if endX < baseW {
			// Truncate base to endX chars, then take what's after
			full := baseLines[y]
			truncatedToEnd := ansi.Truncate(full, endX, "")
			truncEndW := lipgloss.Width(truncatedToEnd)
			if truncEndW < baseW {
				// Get remaining by cutting the prefix
				right = ansi.Cut(full, endX, baseW)
			}
		}

		baseLines[y] = left + ol + right
	}

	return strings.Join(baseLines, "\n")
}
