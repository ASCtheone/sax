package porttracker

import (
	"regexp"
	"strconv"
	"strings"
)

// Patterns to detect port numbers in terminal output.
var portPatterns = []*regexp.Regexp{
	// "listening on port 3000"
	regexp.MustCompile(`(?i)listening\s+on\s+(?:port\s+)?(\d{2,5})`),
	// "started on port 8080"
	regexp.MustCompile(`(?i)started\s+on\s+(?:port\s+)?(\d{2,5})`),
	// "http://localhost:3000" or "https://127.0.0.1:8080"
	regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d{2,5})`),
	// "port 3000" (standalone)
	regexp.MustCompile(`(?i)\bport\s+(\d{2,5})\b`),
	// ":3000" at word boundary (common in Node/Go output)
	regexp.MustCompile(`(?:^|\s):(\d{2,5})(?:\s|$|/)`),
	// "serving at 0.0.0.0:3000"
	regexp.MustCompile(`(?i)serving\s+(?:at|on)\s+\S*:(\d{2,5})`),
	// "Local: http://localhost:5173/"
	regexp.MustCompile(`(?i)local:\s+https?://\S+:(\d{2,5})`),
}

// ParsePorts extracts port numbers from a line of text.
func ParsePorts(line string) []int {
	seen := make(map[int]bool)
	var ports []int

	for _, pat := range portPatterns {
		matches := pat.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			port, err := strconv.Atoi(match[1])
			if err != nil || port < 1 || port > 65535 {
				continue
			}
			// Skip common non-port numbers
			if isUnlikelyPort(port) {
				continue
			}
			if !seen[port] {
				seen[port] = true
				ports = append(ports, port)
			}
		}
	}

	return ports
}

// ParsePortsFromOutput scans multi-line output for port references.
func ParsePortsFromOutput(output string) []int {
	var allPorts []int
	seen := make(map[int]bool)

	for _, line := range strings.Split(output, "\n") {
		for _, port := range ParsePorts(line) {
			if !seen[port] {
				seen[port] = true
				allPorts = append(allPorts, port)
			}
		}
	}

	return allPorts
}

func isUnlikelyPort(port int) bool {
	// Filter out common false positives (years, HTTP status codes, etc.)
	if port >= 1900 && port <= 2100 {
		return true // likely a year
	}
	if port == 404 || port == 500 || port == 200 || port == 301 || port == 302 {
		return true // HTTP status codes
	}
	return false
}
