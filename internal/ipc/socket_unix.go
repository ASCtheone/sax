//go:build !windows

package ipc

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func platformListen(name string) (net.Listener, error) {
	path := SocketPath(name)
	// Remove stale socket
	os.Remove(path)
	return net.Listen("unix", path)
}

func platformDial(name string) (net.Conn, error) {
	path := SocketPath(name)
	return net.DialTimeout("unix", path, 5*time.Second)
}

func platformListSessions() ([]SessionInfo, error) {
	dir := SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []SessionInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sock") {
			continue
		}
		sessName := strings.TrimSuffix(name, ".sock")
		info, err := entry.Info()
		if err != nil {
			continue
		}
		sessions = append(sessions, SessionInfo{
			Name:      sessName,
			Path:      filepath.Join(dir, name),
			CreatedAt: info.ModTime(),
		})
	}
	return sessions, nil
}

func platformCleanupSocket(name string) {
	os.Remove(SocketPath(name))
}
