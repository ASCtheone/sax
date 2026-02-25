package server

import (
	"fmt"
	"strings"

	"github.com/asc/sax/internal/session"
	"github.com/asc/sax/internal/statusbar"
	"github.com/asc/sax/internal/tabbar"
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

// renderCommandPanel generates the right-side command overlay panel lines.
func renderCommandPanel(height int) []string {
	panelW := 34
	border := "\u2502" // │
	hline := strings.Repeat("\u2500", panelW-2)
	top := "\u250c" + hline + "\u2510"    // ┌───┐
	bottom := "\u2514" + hline + "\u2518"  // └───┘
	divider := "\u251c" + hline + "\u2524" // ├───┤

	pad := func(s string) string {
		gap := panelW - 2 - len(s)
		if gap < 0 {
			gap = 0
			s = s[:panelW-2]
		}
		return border + s + strings.Repeat(" ", gap) + border
	}

	header := func(s string) string {
		gap := panelW - 2 - len(s)
		left := gap / 2
		right := gap - left
		if left < 0 {
			left = 0
		}
		if right < 0 {
			right = 0
		}
		return border + strings.Repeat(" ", left) + s + strings.Repeat(" ", right) + border
	}

	var lines []string
	lines = append(lines, top)
	lines = append(lines, header(" SAX Commands "))
	lines = append(lines, header("Prefix: Ctrl+S"))
	lines = append(lines, divider)
	lines = append(lines, header("Tabs"))
	lines = append(lines, pad(" c      New tab"))
	lines = append(lines, pad(" n/p    Next/Prev tab"))
	lines = append(lines, pad(" 1-9    Go to tab N"))
	lines = append(lines, pad(" X      Close tab"))
	lines = append(lines, pad(` "      Window list`))
	lines = append(lines, divider)
	lines = append(lines, header("Panes"))
	lines = append(lines, pad(" v |    Split vertical"))
	lines = append(lines, pad(" s -    Split horizontal"))
	lines = append(lines, pad(" hjkl   Navigate panes"))
	lines = append(lines, pad(" x      Close pane"))
	lines = append(lines, pad(" z      Zoom pane"))
	lines = append(lines, divider)
	lines = append(lines, header("Session"))
	lines = append(lines, pad(" d      Detach"))
	lines = append(lines, pad(" [      Copy/scroll mode"))
	lines = append(lines, pad(" ]      Paste"))
	lines = append(lines, pad(" H      Toggle logging"))
	lines = append(lines, pad(" ^x     Lock session"))
	lines = append(lines, pad(" M      Monitor activity"))
	lines = append(lines, pad(" _      Monitor silence"))
	lines = append(lines, divider)
	lines = append(lines, pad(" ^Q     Quit"))
	lines = append(lines, pad(" ?      Toggle panel"))
	lines = append(lines, bottom)

	// Truncate if panel is taller than available height
	if len(lines) > height {
		lines = lines[:height]
	}

	return lines
}

// overlayRight places overlay lines on the right side of the base frame.
func overlayRight(base string, overlayLines []string, width, height int) string {
	baseLines := strings.Split(base, "\n")

	oH := len(overlayLines)
	oW := 0
	for _, l := range overlayLines {
		if len([]rune(l)) > oW {
			oW = len([]rune(l))
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
		row := []rune(baseLines[y])
		// Extend row if needed
		for len(row) < width {
			row = append(row, ' ')
		}
		olRunes := []rune(ol)
		for j, c := range olRunes {
			x := startX + j
			if x >= 0 && x < len(row) {
				row[x] = c
			}
		}
		baseLines[y] = string(row)
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

// overlayCenter places overlay text centered on top of the base frame.
func overlayCenter(base, overlay string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	oH := len(overlayLines)
	oW := 0
	for _, l := range overlayLines {
		if len(l) > oW {
			oW = len(l)
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
		row := []rune(baseLines[y])
		olRunes := []rune(ol)
		for j, c := range olRunes {
			x := startX + j
			if x < len(row) {
				row[x] = c
			}
		}
		baseLines[y] = string(row)
	}

	return strings.Join(baseLines, "\n")
}
