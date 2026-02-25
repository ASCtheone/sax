package porttracker

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// ListeningPort represents a port found via system scan.
type ListeningPort struct {
	Port    int
	PID     int32
	Process string
}

// ScanListeningPorts returns all TCP ports in LISTEN state.
func ScanListeningPorts(ctx context.Context) ([]ListeningPort, error) {
	conns, err := net.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return nil, fmt.Errorf("get connections: %w", err)
	}

	var ports []ListeningPort
	seen := make(map[int]bool)

	for _, conn := range conns {
		if conn.Status != "LISTEN" {
			continue
		}
		port := int(conn.Laddr.Port)
		if seen[port] {
			continue
		}
		seen[port] = true

		lp := ListeningPort{
			Port: port,
			PID:  conn.Pid,
		}

		// Try to get process name
		if conn.Pid > 0 {
			if proc, err := process.NewProcessWithContext(ctx, conn.Pid); err == nil {
				if name, err := proc.NameWithContext(ctx); err == nil {
					lp.Process = name
				}
			}
		}

		ports = append(ports, lp)
	}

	return ports, nil
}

// ScanPortsForPIDs scans for listening ports belonging to specific PIDs.
func ScanPortsForPIDs(ctx context.Context, pids []int32) ([]ListeningPort, error) {
	conns, err := net.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return nil, err
	}

	pidSet := make(map[int32]bool)
	for _, pid := range pids {
		pidSet[pid] = true
	}

	var ports []ListeningPort
	seen := make(map[int]bool)

	for _, conn := range conns {
		if conn.Status != "LISTEN" {
			continue
		}
		if !pidSet[conn.Pid] {
			continue
		}
		port := int(conn.Laddr.Port)
		if seen[port] {
			continue
		}
		seen[port] = true

		lp := ListeningPort{
			Port: port,
			PID:  conn.Pid,
		}
		if proc, err := process.NewProcessWithContext(ctx, conn.Pid); err == nil {
			if name, err := proc.NameWithContext(ctx); err == nil {
				lp.Process = name
			}
		}
		ports = append(ports, lp)
	}

	return ports, nil
}
