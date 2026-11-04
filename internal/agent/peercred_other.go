//go:build !linux

package agent

import (
	"net"
	"os"
)

// peerUID on non-Linux systems (development only; the agent itself needs
// Linux) treats every local caller as the agent's own user.
func peerUID(c net.Conn) int {
	if _, ok := c.(*net.UnixConn); ok {
		return os.Geteuid()
	}
	return -1
}
