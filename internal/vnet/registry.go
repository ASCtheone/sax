package vnet

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// The listener registry maps a virtual ip:port to the real loopback port of
// whoever listens there. The C shim writes it on bind(); the gateway reads it
// to reach sessions and writes its own entries for HTTP hosts. One file per
// entry: ports/<ip>_<port> containing "<real-port> <pid>\n".

func portPath(dir, ip string, port int) string {
	return filepath.Join(dir, portsDir, fmt.Sprintf("%s_%d", ip, port))
}

// LookupPort returns the real loopback port for ip:port in a router dir.
// Entries whose owner process is gone are treated as absent.
func LookupPort(dir, ip string, port int) (int, bool) {
	data, err := os.ReadFile(portPath(dir, ip, port))
	if err != nil {
		return 0, false
	}
	var real, pid int
	if n, _ := fmt.Sscanf(string(data), "%d %d", &real, &pid); n != 2 || real < 1 || real > 65535 {
		return 0, false
	}
	if !processAlive(pid) {
		return 0, false
	}
	return real, true
}

// RegisterPort records that ip:port is served on 127.0.0.1:real by pid.
func RegisterPort(dir, ip string, port, real, pid int) error {
	return writeAtomic(portPath(dir, ip, port), []byte(fmt.Sprintf("%d %d\n", real, pid)))
}

// UnregisterPort removes an entry; a missing entry is not an error.
func UnregisterPort(dir, ip string, port int) {
	_ = os.Remove(portPath(dir, ip, port))
}

func itoa(n int) string { return strconv.Itoa(n) }
