package ls

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/asc/sax/internal/builtins"
)

// columnGap is the number of spaces between columns in grid output.
const columnGap = 2

// render picks the output style based on options and terminal width.
func (c *Command) render(env *builtins.Env, o options, ents []entry) {
	switch {
	case o.long:
		renderLong(env, o, ents)
	case o.one || env.Width <= 0:
		// One per line: explicitly requested, or output is not a terminal.
		for _, e := range ents {
			fmt.Fprintln(env.Stdout, coloredLabel(env, o, e))
		}
	default:
		renderColumns(env, o, ents)
	}
}

// renderColumns lays entries out in fitted, down-then-across columns (the GNU
// ls default). Column widths use the visible label length, so ANSI color codes
// don't distort alignment.
func renderColumns(env *builtins.Env, o options, ents []entry) {
	n := len(ents)
	if n == 0 {
		return
	}

	plain := make([]string, n)
	colored := make([]string, n)
	for i, e := range ents {
		plain[i] = plainLabel(e, o)
		colored[i] = coloredLabel(env, o, e)
	}

	cols, rows, widths := bestLayout(plain, env.Width)

	var sb strings.Builder
	for row := 0; row < rows; row++ {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			idx := col*rows + row // column-major fill
			if idx >= n {
				continue
			}
			line.WriteString(colored[idx])
			pad := widths[col] - dispLen(plain[idx])
			if col != cols-1 {
				pad += columnGap
			}
			for k := 0; k < pad; k++ {
				line.WriteByte(' ')
			}
		}
		sb.WriteString(strings.TrimRight(line.String(), " "))
		sb.WriteByte('\n')
	}
	fmt.Fprint(env.Stdout, sb.String())
}

// bestLayout finds the largest column count whose total width fits, returning
// the column count, row count, and per-column widths.
func bestLayout(plain []string, width int) (cols, rows int, widths []int) {
	n := len(plain)
	if width <= 0 {
		width = 80
	}

	upper := n
	if max := width/(1+columnGap) + 1; upper > max {
		upper = max
	}
	for try := upper; try >= 1; try-- {
		r := (n + try - 1) / try
		w := make([]int, try)
		total := 0
		for col := 0; col < try; col++ {
			best := 0
			for row := 0; row < r; row++ {
				idx := col*r + row
				if idx >= n {
					break
				}
				if l := dispLen(plain[idx]); l > best {
					best = l
				}
			}
			w[col] = best
			total += best
			if col > 0 {
				total += columnGap
			}
		}
		if total <= width || try == 1 {
			return try, r, w
		}
	}
	return 1, n, []int{0}
}

// renderLong prints the long (-l) format: mode, size, time, name.
func renderLong(env *builtins.Env, o options, ents []entry) {
	sizes := make([]string, len(ents))
	sizeW := 0
	for i, e := range ents {
		if o.human {
			sizes[i] = humanSize(e.info.Size())
		} else {
			sizes[i] = strconv.FormatInt(e.info.Size(), 10)
		}
		if l := len(sizes[i]); l > sizeW {
			sizeW = l
		}
	}

	for i, e := range ents {
		name := coloredLabel(env, o, e)
		if e.info.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(e.path); err == nil {
				name += " -> " + target
			}
		}
		fmt.Fprintf(env.Stdout, "%s %*s %s %s\n",
			e.info.Mode().String(), sizeW, sizes[i], formatTime(e.info.ModTime()), name)
	}
}

// formatTime mirrors GNU ls: time of day for recent files, year for old ones.
func formatTime(t time.Time) string {
	const sixMonths = time.Hour * 24 * 182
	age := time.Since(t)
	if age > sixMonths || age < -sixMonths {
		return t.Format("Jan _2  2006")
	}
	return t.Format("Jan _2 15:04")
}

// humanSize formats a byte count like 1.2K, 3.4M (powers of 1024).
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	val := float64(n) / float64(div)
	suffix := "KMGTPE"[exp]
	if val < 10 {
		return fmt.Sprintf("%.1f%c", val, suffix)
	}
	return fmt.Sprintf("%.0f%c", val, suffix)
}

// dispLen returns the visible width of a label in runes.
func dispLen(s string) int {
	return utf8.RuneCountInString(s)
}
