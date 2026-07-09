// Command ss is the sax shell — a versatile shell built in the sax repository
// but shipped as its own binary. This is the proof-of-concept: an interactive
// loop that launches programs. Build with `go build -o ss.exe ./cmd/ss`.
package main

import (
	"fmt"
	"os"

	"github.com/asc/sax/internal/builtins/cat"
	"github.com/asc/sax/internal/builtins/echo"
	"github.com/asc/sax/internal/builtins/env"
	"github.com/asc/sax/internal/builtins/ls"
	"github.com/asc/sax/internal/builtins/mkdir"
	"github.com/asc/sax/internal/builtins/rm"
	"github.com/asc/sax/internal/shell"
)

func main() {
	sh, err := shell.New(os.Stdin, os.Stdout, os.Stderr, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ss: %v\n", err)
		os.Exit(1)
	}

	// Feature builtins are wired in here, the composition root. Each lives in
	// its own package and the shell only knows the builtins.Command contract.
	sh.Register(ls.New(), cat.New(), echo.New(), mkdir.New(), rm.New(), env.New())

	fmt.Fprintln(os.Stdout, "ss — sax shell (poc). builtins: ls, cat, echo, mkdir, rm, env, cd, pwd, exit. type 'exit' to quit.")
	if err := sh.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ss: %v\n", err)
		os.Exit(1)
	}
}
