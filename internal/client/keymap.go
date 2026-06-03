package client

import (
	"strings"

	"github.com/asc/sax/internal/saxrc"
)

// DefaultPrefix is the prefix key when ~/.saxrc does not override it.
const DefaultPrefix = "ctrl+s"

// defaultKeymap returns the built-in prefix-mode bindings: a map from the key
// string bubbletea reports to a sax command string. ~/.saxrc bind/unbind
// directives layer on top of this.
func defaultKeymap() map[string]string {
	return map[string]string{
		// Tabs
		"c": "new-tab",
		"n": "next-tab",
		"p": "prev-tab",
		"1": "select-tab 1",
		"2": "select-tab 2",
		"3": "select-tab 3",
		"4": "select-tab 4",
		"5": "select-tab 5",
		"6": "select-tab 6",
		"7": "select-tab 7",
		"8": "select-tab 8",
		"9": "select-tab 9",
		"X": "close-tab",
		`"`: "window-list",
		// Panes
		"v": "split-v",
		"|": "split-v",
		"s": "split-h",
		"-": "split-h",
		"h": "pane-left",
		"j": "pane-down",
		"k": "pane-up",
		"l": "pane-right",
		"x": "close-pane",
		"z": "zoom",
		// Session
		"d":      "detach",
		"[":      "copy-mode",
		"]":      "paste",
		"H":      "toggle-log",
		"ctrl+x": "lock",
		"M":      "monitor-activity",
		"_":      "monitor-silence",
		"?":      "help",
	}
}

// buildKeymap resolves the prefix key and the effective prefix-mode keymap from
// ~/.saxrc, starting from the defaults and applying set prefix, unbind, and
// bind directives in that order.
func buildKeymap(rc *saxrc.Config) (prefix string, keymap map[string]string) {
	prefix = DefaultPrefix
	keymap = defaultKeymap()

	if rc == nil {
		return prefix, keymap
	}

	if p := rc.Prefix(); p != "" {
		prefix = p
	}
	for _, key := range rc.Unbinds {
		delete(keymap, key)
	}
	for _, b := range rc.Binds {
		keymap[b.Key] = b.Command
	}
	return prefix, keymap
}

// prefixLiteralBytes returns the raw byte(s) to send to the PTY when the prefix
// key is pressed twice (the "send the prefix through literally" gesture). For a
// "ctrl+<letter>" prefix this is the corresponding C0 control byte; otherwise
// it falls back to Ctrl+S (0x13), matching the historical default.
func prefixLiteralBytes(prefix string) []byte {
	if strings.HasPrefix(prefix, "ctrl+") {
		rest := prefix[len("ctrl+"):]
		if len(rest) == 1 {
			c := rest[0]
			if c >= 'a' && c <= 'z' {
				return []byte{c - 'a' + 1}
			}
			if c >= 'A' && c <= 'Z' {
				return []byte{c - 'A' + 1}
			}
		}
	}
	return []byte{0x13}
}
