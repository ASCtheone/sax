//go:build windows

package ipc

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

func platformListen(name string) (net.Listener, error) {
	pipePath := SocketPath(name)
	cfg := &winio.PipeConfig{
		SecurityDescriptor: "", // default security
		MessageMode:        false,
		InputBufferSize:    512 * 1024,
		OutputBufferSize:   512 * 1024,
	}
	return winio.ListenPipe(pipePath, cfg)
}

func platformDial(name string) (net.Conn, error) {
	pipePath := SocketPath(name)
	timeout := 5 * time.Second
	return winio.DialPipe(pipePath, &timeout)
}

func platformListSessions() ([]SessionInfo, error) {
	// On Windows, we can't enumerate named pipes easily.
	// Instead, read the sessions directory for PID files.
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
		if !strings.HasSuffix(name, ".pid") {
			continue
		}
		sessName := strings.TrimSuffix(name, ".pid")
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
	// Named pipes are cleaned up automatically when the listener closes.
	// Just remove the PID file.
	os.Remove(PidPath(name))
}
