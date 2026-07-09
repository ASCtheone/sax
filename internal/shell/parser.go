package shell

import "fmt"

// Parse splits a command line into fields, honoring single and double quotes
// for grouping. It is intentionally minimal for the POC: no variable expansion,
// globbing, pipes, or redirection yet — just enough to launch a program with
// arguments.
//
// Backslash is treated literally (not as an escape) so Windows paths such as
// C:\Users\asc tokenize as written; quoting is the way to include spaces.
func Parse(line string) ([]string, error) {
	var (
		fields []string
		cur    []rune
		hasCur bool
		quote  rune // 0 when unquoted, otherwise '\'' or '"'
	)

	flush := func() {
		if hasCur {
			fields = append(fields, string(cur))
			cur = cur[:0]
			hasCur = false
		}
	}

	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur = append(cur, r)
			}
			hasCur = true
		case r == '\'' || r == '"':
			quote = r
			hasCur = true
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			flush()
		default:
			cur = append(cur, r)
			hasCur = true
		}
	}

	if quote != 0 {
		return nil, fmt.Errorf("unclosed %c quote", quote)
	}
	flush()
	return fields, nil
}
