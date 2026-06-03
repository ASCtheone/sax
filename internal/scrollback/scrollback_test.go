package scrollback

import (
	"fmt"
	"strings"
	"testing"
)

func TestAppendOutput(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   []string
	}{
		{
			name:   "simple lines",
			writes: []string{"hello\nworld\n"},
			want:   []string{"hello", "world"},
		},
		{
			name:   "trailing partial line is not committed",
			writes: []string{"hello\nworld"},
			want:   []string{"hello"},
		},
		{
			name:   "CRLF",
			writes: []string{"one\r\ntwo\r\n"},
			want:   []string{"one", "two"},
		},
		{
			name:   "blank lines preserved",
			writes: []string{"a\n\nb\n"},
			want:   []string{"a", "", "b"},
		},
		{
			name: "progress bar single write collapses to final state",
			writes: []string{
				"Loading 10%\rLoading 50%\rLoading 99%\rDone       \n",
			},
			want: []string{"Done"},
		},
		{
			name: "progress bar split across writes, same-width label collapses",
			writes: []string{
				"Loading 10%\r",
				"Loading 50%\r",
				"Loading 99%\r",
				"Loading OK!\n",
			},
			want: []string{"Loading OK!"},
		},
		{
			name: "realistic progress bar that clears line before final message",
			writes: []string{
				"[>   ] 10%\r",
				"[==> ] 50%\r",
				"[====] 99%\r",
				"\x1b[2KBuild complete\n",
			},
			want: []string{"Build complete"},
		},
		{
			name: "progress bar with erase-to-end before shorter text",
			writes: []string{
				"Loading something long\r\x1b[KDone\n",
			},
			want: []string{"Done"},
		},
		{
			name: "erase entire line CSI 2K",
			writes: []string{
				"garbage text\r\x1b[2KFresh\n",
			},
			want: []string{"Fresh"},
		},
		{
			name: "shorter overwrite without erase keeps tail of old line",
			writes: []string{
				"Loading something long\rDone\n",
			},
			// "Loading something long" → \r resets cursor, "Done" overwrites
			// the first 4 chars → "Done" + "ing something long".
			want: []string{"Doneing something long"},
		},
		{
			name: "sgr color codes are preserved around text",
			writes: []string{
				"\x1b[31mred\x1b[0m and \x1b[32mgreen\x1b[0m\n",
			},
			want: []string{"\x1b[31mred\x1b[0m and \x1b[32mgreen\x1b[0m"},
		},
		{
			name: "cursor move sequences are ignored for text",
			writes: []string{
				"hello\x1b[5Cworld\n",
			},
			want: []string{"helloworld"},
		},
		{
			name: "tab expands to next multiple of 8",
			writes: []string{
				"a\tb\n",
			},
			want: []string{"a       b"},
		},
		{
			name: "backspace moves cursor left",
			writes: []string{
				"abcX\bY\n",
			},
			want: []string{"abcY"},
		},
		{
			name: "utf8 multi-byte runes",
			writes: []string{
				"café\nnaïve\n",
			},
			want: []string{"café", "naïve"},
		},
		{
			name: "mixed control chars ignored",
			writes: []string{
				"hel\x01\x02lo\n",
			},
			want: []string{"hello"},
		},
		{
			name: "BEL is discarded, text preserved",
			writes: []string{
				"alert\x07!\n",
			},
			want: []string{"alert!"},
		},
		{
			name: "OSC title sequence skipped",
			writes: []string{
				"\x1b]0;window title\x07hello\n",
			},
			want: []string{"hello"},
		},
		{
			name: "OSC title sequence with ST terminator",
			writes: []string{
				"\x1b]0;title\x1b\\hello\n",
			},
			want: []string{"hello"},
		},
		{
			name: "sgr color survives progress bar \\r overwrite",
			writes: []string{
				"\x1b[31mLoading 10%\r\x1b[33mLoading 99%\n",
			},
			// Both sequences stack in the serialized form (no reset between
			// them), but the terminal applies them in order and the final
			// foreground is yellow — visually identical to just "\x1b[33m".
			want: []string{"\x1b[31m\x1b[33mLoading 99%\x1b[0m"},
		},
		{
			name: "additive sgr: red then bold keeps both",
			writes: []string{
				"\x1b[31mred\x1b[1mbold\x1b[0m\n",
			},
			// "red" in red, "bold" in red+bold.
			want: []string{"\x1b[31mred\x1b[0m\x1b[31m\x1b[1mbold\x1b[0m"},
		},
		{
			name: "sgr reset returns to default",
			writes: []string{
				"\x1b[31mred\x1b[0m default\n",
			},
			want: []string{"\x1b[31mred\x1b[0m default"},
		},
		{
			name: "CSI m with no params is a reset",
			writes: []string{
				"\x1b[31mred\x1b[m plain\n",
			},
			want: []string{"\x1b[31mred\x1b[0m plain"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBuffer(100)
			for _, w := range tt.writes {
				b.AppendOutput([]byte(w))
			}
			got := b.AllLines()
			if len(got) != len(tt.want) {
				t.Fatalf("line count = %d, want %d\ngot: %q\nwant: %q",
					len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestPendingLine(t *testing.T) {
	b := NewBuffer(10)
	b.AppendOutput([]byte("Loading 50%"))
	if got := b.PendingLine(); got != "Loading 50%" {
		t.Errorf("pending = %q, want %q", got, "Loading 50%")
	}
	if b.LineCount() != 0 {
		t.Errorf("LineCount = %d, want 0", b.LineCount())
	}

	// Overwrite via carriage return — pending should be the overwritten state.
	b.AppendOutput([]byte("\rLoading 90%"))
	if got := b.PendingLine(); got != "Loading 90%" {
		t.Errorf("pending after overwrite = %q, want %q", got, "Loading 90%")
	}

	// Commit with \n — pending drains, ring buffer has one line.
	b.AppendOutput([]byte("\n"))
	if got := b.PendingLine(); got != "" {
		t.Errorf("pending after commit = %q, want empty", got)
	}
	if b.LineCount() != 1 {
		t.Fatalf("LineCount = %d, want 1", b.LineCount())
	}
	if b.LineAt(0) != "Loading 90%" {
		t.Errorf("line = %q, want %q", b.LineAt(0), "Loading 90%")
	}
}

func TestTotalWrittenAndSerials(t *testing.T) {
	b := NewBuffer(3) // small capacity to test eviction

	b.AppendOutput([]byte("a\nb\nc\n"))
	if b.TotalWritten() != 3 {
		t.Errorf("TotalWritten = %d, want 3", b.TotalWritten())
	}
	if b.OldestSerial() != 0 {
		t.Errorf("OldestSerial = %d, want 0", b.OldestSerial())
	}

	// Serial 0 is "a".
	if line, ok := b.LineAtSerial(0); !ok || line != "a" {
		t.Errorf("LineAtSerial(0) = (%q, %v), want (a, true)", line, ok)
	}
	// Serial 2 is "c".
	if line, ok := b.LineAtSerial(2); !ok || line != "c" {
		t.Errorf("LineAtSerial(2) = (%q, %v), want (c, true)", line, ok)
	}
	// Serial 3 does not exist yet.
	if _, ok := b.LineAtSerial(3); ok {
		t.Errorf("LineAtSerial(3) should be out of range")
	}

	// Add one more — evicts "a".
	b.AppendOutput([]byte("d\n"))
	if b.TotalWritten() != 4 {
		t.Errorf("TotalWritten = %d, want 4", b.TotalWritten())
	}
	if b.OldestSerial() != 1 {
		t.Errorf("OldestSerial = %d, want 1", b.OldestSerial())
	}
	if _, ok := b.LineAtSerial(0); ok {
		t.Errorf("LineAtSerial(0) should be evicted")
	}
	if line, ok := b.LineAtSerial(1); !ok || line != "b" {
		t.Errorf("LineAtSerial(1) = (%q, %v), want (b, true)", line, ok)
	}
}

func TestRingBufferWrapping(t *testing.T) {
	b := NewBuffer(3)
	for i := 0; i < 10; i++ {
		b.AppendOutput([]byte(fmt.Sprintf("line-%d\n", i)))
	}
	got := b.AllLines()
	want := []string{"line-7", "line-8", "line-9"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("AllLines = %v, want %v", got, want)
	}
	if b.TotalWritten() != 10 {
		t.Errorf("TotalWritten = %d, want 10", b.TotalWritten())
	}
	if b.OldestSerial() != 7 {
		t.Errorf("OldestSerial = %d, want 7", b.OldestSerial())
	}
}
