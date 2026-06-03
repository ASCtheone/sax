package saxrc

import "strings"

// NormalizeKey converts a .saxrc key specification into the string form that
// bubbletea's tea.KeyMsg.String() reports, so the client can match bindings by
// simple string comparison.
//
// Modifier prefixes (joined by '-'): C = ctrl, M/A = alt, S = shift.
// Examples:
//
//	C-a   -> "ctrl+a"
//	M-x   -> "alt+x"
//	C-M-x -> "ctrl+alt+x"
//	X     -> "X"        (bare shifted letter, as bubbletea reports it)
//	|     -> "|"
//	-     -> "-"
//	space -> "space"
//
// An empty or whitespace-only spec returns "".
func NormalizeKey(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}

	// A single character that is itself a separator or symbol (e.g. "-", "|")
	// has no modifiers.
	if len(spec) == 1 {
		return mapBareKey(spec)
	}

	parts := strings.Split(spec, "-")

	// Trailing '-' means the bound key is literally '-' (e.g. "C--").
	key := parts[len(parts)-1]
	mods := parts[:len(parts)-1]
	if key == "" {
		key = "-"
		if len(mods) > 0 {
			mods = mods[:len(mods)-1]
		}
	}

	var modNames []string
	hasCtrl := false
	for _, m := range mods {
		switch strings.ToUpper(m) {
		case "C", "CTRL":
			modNames = append(modNames, "ctrl")
			hasCtrl = true
		case "M", "A", "ALT", "META":
			modNames = append(modNames, "alt")
		case "S", "SHIFT":
			modNames = append(modNames, "shift")
		default:
			// Unknown modifier — treat the whole spec as a literal key.
			return mapBareKey(spec)
		}
	}

	key = mapBareKey(key)

	// bubbletea reports ctrl combinations with a lowercase letter
	// ("ctrl+a", never "ctrl+A").
	if hasCtrl && len(key) == 1 {
		key = strings.ToLower(key)
	}

	if len(modNames) == 0 {
		return key
	}
	return strings.Join(modNames, "+") + "+" + key
}

// mapBareKey maps named keys to their bubbletea string form, leaving single
// characters and unknown names untouched (case preserved for shifted letters).
func mapBareKey(k string) string {
	switch strings.ToLower(k) {
	case "space":
		return "space"
	case "enter", "return", "cr":
		return "enter"
	case "tab":
		return "tab"
	case "esc", "escape":
		return "esc"
	case "bspace", "backspace":
		return "backspace"
	case "up":
		return "up"
	case "down":
		return "down"
	case "left":
		return "left"
	case "right":
		return "right"
	}
	return k
}
