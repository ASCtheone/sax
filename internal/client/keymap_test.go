package client

import (
	"bytes"
	"testing"

	"github.com/asc/sax/internal/saxrc"
)

func TestBuildKeymapDefaults(t *testing.T) {
	prefix, km := buildKeymap(nil)
	if prefix != DefaultPrefix {
		t.Errorf("prefix = %q, want %q", prefix, DefaultPrefix)
	}
	if km["c"] != "new-tab" || km["|"] != "split-v" || km["?"] != "help" {
		t.Errorf("default keymap missing expected bindings: %#v", km)
	}
}

func TestBuildKeymapOverrides(t *testing.T) {
	rc := saxrc.Parse(`
set prefix C-a
bind r reload
bind | split-h
unbind '"'
`, "")

	prefix, km := buildKeymap(rc)
	if prefix != "ctrl+a" {
		t.Errorf("prefix = %q, want ctrl+a", prefix)
	}
	if km["r"] != "reload" {
		t.Errorf("custom bind r = %q, want reload", km["r"])
	}
	if km["|"] != "split-h" {
		t.Errorf("rebound | = %q, want split-h (overrides default split-v)", km["|"])
	}
	if _, ok := km[`"`]; ok {
		t.Error(`unbind '"' should have removed the window-list binding`)
	}
	// Untouched defaults remain.
	if km["c"] != "new-tab" {
		t.Errorf("default binding c = %q, want new-tab", km["c"])
	}
}

func TestPrefixLiteralBytes(t *testing.T) {
	tests := []struct {
		prefix string
		want   []byte
	}{
		{"ctrl+s", []byte{0x13}},
		{"ctrl+a", []byte{0x01}},
		{"ctrl+b", []byte{0x02}},
		{"ctrl+x", []byte{0x18}},
		{"alt+x", []byte{0x13}}, // non-ctrl falls back to Ctrl+S
	}
	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			if got := prefixLiteralBytes(tt.prefix); !bytes.Equal(got, tt.want) {
				t.Errorf("prefixLiteralBytes(%q) = %v, want %v", tt.prefix, got, tt.want)
			}
		})
	}
}
