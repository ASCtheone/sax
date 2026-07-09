package ls

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/asc/sax/internal/builtins"
)

// ANSI color codes. Type colors are bold; extension colors are plain so the
// listing reads as "structure first (dirs/links/exec), content second".
const (
	cReset = "\x1b[0m"

	cBoldBlue  = "\x1b[1;34m" // directories
	cBoldCyan  = "\x1b[1;36m" // symlinks
	cBoldGreen = "\x1b[1;32m" // executables

	cRed           = "\x1b[31m"
	cGreen         = "\x1b[32m"
	cYellow        = "\x1b[33m"
	cMagenta       = "\x1b[35m"
	cCyan          = "\x1b[36m"
	cWhite         = "\x1b[37m"
	cBrightRed     = "\x1b[91m"
	cBrightGreen   = "\x1b[92m"
	cBrightYellow  = "\x1b[93m"
	cBrightMagenta = "\x1b[95m"
	cGray          = "\x1b[90m"
)

// extColors maps a lowercased extension to its color. Grouped by category so
// related files read alike.
var extColors = buildExtColors()

func buildExtColors() map[string]string {
	m := map[string]string{}
	add := func(color string, exts ...string) {
		for _, e := range exts {
			m[e] = color
		}
	}
	// Archives / compressed.
	add(cRed, ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".zst", ".7z", ".rar", ".lz", ".lzma", ".cab", ".deb", ".rpm")
	// Images.
	add(cMagenta, ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".svg", ".webp", ".ico", ".tiff", ".heic")
	// Video.
	add(cBrightMagenta, ".mp4", ".mkv", ".mov", ".avi", ".webm", ".flv", ".wmv", ".m4v")
	// Audio.
	add(cCyan, ".mp3", ".wav", ".flac", ".ogg", ".m4a", ".aac", ".opus")
	// Source code.
	add(cYellow, ".go", ".js", ".jsx", ".ts", ".tsx", ".py", ".rs", ".c", ".h", ".cpp", ".hpp", ".cc",
		".java", ".rb", ".php", ".swift", ".kt", ".scala", ".lua", ".sh", ".bash", ".zsh", ".ps1",
		".html", ".css", ".scss", ".sql", ".vim")
	// Config / data.
	add(cBrightYellow, ".json", ".yaml", ".yml", ".toml", ".ini", ".env", ".conf", ".cfg", ".properties", ".lock")
	// Documents.
	add(cWhite, ".md", ".txt", ".rst", ".pdf", ".doc", ".docx", ".odt", ".rtf", ".csv", ".tsv", ".xlsx", ".epub")
	// Executables / binaries (non-Unix-bit platforms rely on this too).
	add(cBrightGreen, ".exe", ".bat", ".cmd", ".com", ".msi", ".app", ".bin", ".o", ".so", ".dll", ".dylib")
	// Backups / noise.
	add(cGray, ".bak", ".old", ".tmp", ".swp", ".swo", ".orig", ".log")
	return m
}

// colorFor returns the ANSI color for an entry, or "" for the default.
// Structural type (dir/link/exec) wins over extension.
func colorFor(e entry) string {
	switch {
	case e.info.Mode()&os.ModeSymlink != 0:
		return cBoldCyan
	case e.info.IsDir():
		return cBoldBlue
	case isExecutable(e):
		return cBoldGreen
	}
	if c, ok := extColors[strings.ToLower(filepath.Ext(e.name))]; ok {
		return c
	}
	return ""
}

// classifySuffix returns the -F indicator for an entry, or "".
func classifySuffix(e entry, o options) string {
	if !o.classify {
		return ""
	}
	switch {
	case e.info.Mode()&os.ModeSymlink != 0:
		return "@"
	case e.info.IsDir():
		return "/"
	case isExecutable(e):
		return "*"
	}
	return ""
}

// plainLabel is the uncolored label (optional git cells, name, classifier),
// used for width math.
func plainLabel(e entry, o options) string {
	prefix := ""
	if o.gitCol {
		prefix = gitPlain(e) + " "
	}
	return prefix + e.name + classifySuffix(e, o)
}

// coloredLabel is the label as displayed: an optional git status, then the name
// colored by type/extension when color is enabled, then any classifier.
func coloredLabel(env *builtins.Env, o options, e entry) string {
	suffix := classifySuffix(e, o)

	name := e.name + suffix
	if env.Color {
		if color := colorFor(e); color != "" {
			name = color + e.name + cReset + suffix
		}
	}

	if !o.gitCol {
		return name
	}
	if env.Color {
		return gitColored(e) + " " + name
	}
	return gitPlain(e) + " " + name
}
