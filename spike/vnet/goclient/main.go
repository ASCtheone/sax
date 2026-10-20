// Go client for the spike: Go on darwin calls libSystem for sockets, but
// may resolve names itself, so this checks both paths.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	for _, url := range os.Args[1:] {
		resp, err := http.Get(url)
		if err != nil {
			fmt.Printf("  %-34s -> ERR %v\n", url, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("  %-34s -> %s\n", url, strings.TrimSpace(string(body)))
	}
}
