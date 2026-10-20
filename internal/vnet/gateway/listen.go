package gateway

import (
	"net"
)

// listenHost binds the host-facing listener. Loopback addresses are used as
// is. A wildcard bind (the only way to get port 80 without root on macOS)
// is wrapped so connections from other machines are dropped on accept.
func listenHost(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return ln, nil
	}
	return loopbackOnly{ln}, nil
}

// loopbackOnly accepts connections from this machine only.
type loopbackOnly struct{ net.Listener }

func (l loopbackOnly) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if tcp, ok := conn.RemoteAddr().(*net.TCPAddr); ok && tcp.IP.IsLoopback() {
			return conn, nil
		}
		_ = conn.Close()
	}
}
