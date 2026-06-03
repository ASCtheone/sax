package saxrc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxSourceDepth = 8

// Load reads and parses the default rc file (see DefaultPath). A missing file
// is not an error: it yields an empty Config. Parse problems are collected in
// Config.Warnings rather than failing, so a malformed rc never prevents the
// daemon from starting.
func Load() *Config {
	return LoadPath(DefaultPath())
}

// LoadPath reads and parses the rc file at path. A missing file yields an empty
// Config with no warnings.
func LoadPath(path string) *Config {
	cfg := newConfig()
	cfg.Path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("cannot read %s: %v", path, err))
		}
		return cfg
	}

	p := &parser{cfg: cfg, visited: map[string]bool{}}
	if abs, err := filepath.Abs(path); err == nil {
		p.visited[abs] = true
	}
	p.parse(string(data), filepath.Dir(path))
	return cfg
}

// Parse parses rc content directly. baseDir resolves relative `source` paths.
// Intended for tests and embedding; most callers want Load/LoadPath.
func Parse(content, baseDir string) *Config {
	cfg := newConfig()
	p := &parser{cfg: cfg, visited: map[string]bool{}}
	p.parse(content, baseDir)
	return cfg
}

type parser struct {
	cfg     *Config
	visited map[string]bool
	depth   int
}

// parse processes rc content. baseDir is the directory used to resolve
// relative `source` includes.
func (p *parser) parse(content, baseDir string) {
	// Strip a leading UTF-8 byte-order mark, which editors (notably on
	// Windows) often prepend and which would otherwise corrupt the first
	// directive.
	content = strings.TrimPrefix(content, "\ufeff")

	lines := logicalLines(content)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line.text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		toks := tokenize(trimmed)
		if len(toks) == 0 {
			continue
		}

		directive := toks[0]

		// `function name { ... }` may span multiple logical lines until the
		// closing brace, so it consumes lines greedily.
		if directive == "function" {
			i = p.parseFunction(lines, i)
			continue
		}

		p.parseDirective(directive, toks[1:], trimmed, line.num, baseDir)
	}
}

func (p *parser) parseDirective(directive string, args []string, raw string, lineNum int, baseDir string) {
	switch directive {
	case "set":
		if len(args) < 2 {
			p.warn(lineNum, "set requires a key and value")
			return
		}
		p.cfg.Options[args[0]] = strings.Join(args[1:], " ")

	case "setenv":
		if len(args) < 1 {
			p.warn(lineNum, "setenv requires a name")
			return
		}
		key := args[0]
		val := strings.Join(args[1:], " ")
		p.cfg.Env = append(p.cfg.Env, EnvVar{Key: key, Value: val})

	case "export":
		key, val, ok := splitAssignment(strings.Join(args, " "))
		if !ok {
			p.warn(lineNum, "export requires KEY=value")
			return
		}
		p.cfg.Env = append(p.cfg.Env, EnvVar{Key: key, Value: val})

	case "alias":
		name, val, ok := splitAssignment(strings.Join(args, " "))
		if !ok || name == "" {
			p.warn(lineNum, "alias requires name=command")
			return
		}
		p.cfg.Aliases[name] = val

	case "bind":
		if len(args) < 2 {
			p.warn(lineNum, "bind requires a key and command")
			return
		}
		key := NormalizeKey(args[0])
		if key == "" {
			p.warn(lineNum, "bind has an empty key")
			return
		}
		p.cfg.Binds = append(p.cfg.Binds, Bind{Key: key, Command: strings.Join(args[1:], " ")})

	case "unbind":
		if len(args) < 1 {
			p.warn(lineNum, "unbind requires a key")
			return
		}
		key := NormalizeKey(args[0])
		if key != "" {
			p.cfg.Unbinds = append(p.cfg.Unbinds, key)
		}

	case "hook":
		if len(args) < 2 {
			p.warn(lineNum, "hook requires an event and command")
			return
		}
		event := args[0]
		p.cfg.Hooks[event] = append(p.cfg.Hooks[event], strings.Join(args[1:], " "))

	case "run":
		if len(args) < 1 {
			p.warn(lineNum, "run requires a command")
			return
		}
		p.cfg.RunCmds = append(p.cfg.RunCmds, strings.Join(args, " "))

	case "source":
		if len(args) < 1 {
			p.warn(lineNum, "source requires a path")
			return
		}
		p.source(strings.Join(args, " "), baseDir, lineNum)

	default:
		p.warn(lineNum, fmt.Sprintf("unknown directive %q", directive))
	}
}

// parseFunction parses a `function name { cmd; cmd }` block starting at lines[i]
// and returns the index of the last line it consumed. The body may span several
// logical lines; commands are separated by ';' or newlines.
func (p *parser) parseFunction(lines []logicalLine, i int) int {
	first := strings.TrimSpace(lines[i].text)
	toks := tokenize(first)
	if len(toks) < 2 {
		p.warn(lines[i].num, "function requires a name")
		return i
	}
	name := toks[1]

	// Collect the body from the opening '{' to the matching '}', which may be
	// on the same line or subsequent lines.
	var body strings.Builder
	open := strings.Index(first, "{")
	if open < 0 {
		// Body starts on a following line.
		j := i + 1
		for ; j < len(lines); j++ {
			if strings.Contains(lines[j].text, "{") {
				open = 0
				first = lines[j].text
				i = j
				break
			}
		}
		if open < 0 {
			p.warn(lines[i].num, "function missing '{'")
			return i
		}
	}

	rest := first[strings.Index(first, "{")+1:]
	end := i
	for {
		if close := strings.Index(rest, "}"); close >= 0 {
			body.WriteString(rest[:close])
			break
		}
		body.WriteString(rest)
		body.WriteString("\n")
		end++
		if end >= len(lines) {
			p.warn(lines[i].num, fmt.Sprintf("function %q missing '}'", name))
			break
		}
		rest = lines[end].text
	}

	cmds := splitCommands(body.String())
	if len(cmds) > 0 {
		p.cfg.Funcs[name] = cmds
	}
	return end
}

// source reads and parses an included rc file, guarding against cycles and
// runaway recursion depth.
func (p *parser) source(path, baseDir string, lineNum int) {
	if p.depth >= maxSourceDepth {
		p.warn(lineNum, "source nesting too deep")
		return
	}

	path = ExpandHome(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if p.visited[abs] {
		p.warn(lineNum, fmt.Sprintf("source cycle: %s already included", path))
		return
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		p.warn(lineNum, fmt.Sprintf("cannot source %s: %v", path, err))
		return
	}

	p.visited[abs] = true
	p.depth++
	p.parse(string(data), filepath.Dir(abs))
	p.depth--
}

func (p *parser) warn(lineNum int, msg string) {
	p.cfg.Warnings = append(p.cfg.Warnings, fmt.Sprintf("line %d: %s", lineNum, msg))
}

// --- lexical helpers ---

type logicalLine struct {
	text string
	num  int // 1-based line number of the start of this logical line
}

// logicalLines splits content into logical lines, joining any physical line
// that ends with an unescaped backslash with the following line.
func logicalLines(content string) []logicalLine {
	raw := strings.Split(content, "\n")
	var out []logicalLine
	var cur strings.Builder
	startNum := 0

	for idx, line := range raw {
		line = strings.TrimRight(line, "\r")
		if startNum == 0 {
			startNum = idx + 1
		}
		if strings.HasSuffix(line, "\\") && !strings.HasSuffix(line, "\\\\") {
			cur.WriteString(line[:len(line)-1])
			continue
		}
		cur.WriteString(line)
		out = append(out, logicalLine{text: cur.String(), num: startNum})
		cur.Reset()
		startNum = 0
	}
	if cur.Len() > 0 {
		out = append(out, logicalLine{text: cur.String(), num: startNum})
	}
	return out
}

// tokenize splits a line into whitespace-separated tokens, honoring single and
// double quotes (quotes are removed; their contents are kept verbatim). A
// quoted empty string produces an empty token.
func tokenize(s string) []string {
	var toks []string
	var cur strings.Builder
	inSingle, inDouble, started := false, false, false

	flush := func() {
		if started {
			toks = append(toks, cur.String())
			cur.Reset()
			started = false
		}
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
			started = true
		case inDouble:
			if c == '"' {
				inDouble = false
			} else {
				cur.WriteByte(c)
			}
			started = true
		case c == '\'':
			inSingle = true
			started = true
		case c == '"':
			inDouble = true
			started = true
		case c == ' ' || c == '\t':
			flush()
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	flush()
	return toks
}

// splitAssignment splits "KEY=value" (or "KEY = value", already token-joined)
// into key and value on the first '='. Returns ok=false if there is no '='.
func splitAssignment(s string) (key, val string, ok bool) {
	idx := strings.Index(s, "=")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:idx])
	val = strings.TrimSpace(s[idx+1:])
	return key, val, true
}

// splitCommands splits a function body into individual commands on ';' and
// newlines, trimming whitespace and dropping empties.
func splitCommands(body string) []string {
	fields := strings.FieldsFunc(body, func(r rune) bool {
		return r == ';' || r == '\n'
	})
	var out []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
