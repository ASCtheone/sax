package app

import tea "github.com/charmbracelet/bubbletea"

// Mode represents the current input mode.
type Mode int

const (
	ModeNormal Mode = iota
	ModePrefix
)

// PrefixKey is the key string that activates prefix mode (Ctrl+S).
const PrefixKey = "ctrl+s"

// handlePrefixKey processes a key press in prefix mode and returns
// the command to execute, plus whether the key was consumed.
func (m *Model) handlePrefixKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	key := msg.String()

	switch key {
	// Tab management
	case "c":
		return m.createTab(), true
	case "n":
		m.nextTab()
		return nil, true
	case "p":
		m.prevTab()
		return nil, true
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(key[0] - '1')
		m.goToTab(idx)
		return nil, true
	case "X":
		return m.closeTab(), true

	// Pane management
	case "v", "|":
		return m.splitVertical(), true
	case "s", "-":
		return m.splitHorizontal(), true
	case "h":
		m.navigatePane(DirLeft)
		return nil, true
	case "j":
		m.navigatePane(DirDown)
		return nil, true
	case "k":
		m.navigatePane(DirUp)
		return nil, true
	case "l":
		m.navigatePane(DirRight)
		return nil, true
	case "x":
		return m.closePane(), true
	case "z":
		m.toggleZoom()
		return nil, true

	// Help
	case "?":
		m.showHelp = !m.showHelp
		return nil, true

	default:
		return nil, false
	}
}

// Direction for pane navigation.
type Direction int

const (
	DirLeft Direction = iota
	DirRight
	DirUp
	DirDown
)
