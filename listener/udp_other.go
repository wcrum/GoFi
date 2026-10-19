//go:build !linux

package listener

import "net"

const oobSize = 0

// tuneUDPSocket requests the receive buffer; kernel drop accounting is
// Linux-only.
func tuneUDPSocket(conn *net.UDPConn, readBuffer int) (effective int, dropsSupported bool) {
	_ = conn.SetReadBuffer(readBuffer)
	return 0, false
}

func kernelDrops([]byte) (uint64, bool) { return 0, false }
