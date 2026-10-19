package listener

import (
	"encoding/binary"
	"net"
	"syscall"
	"unsafe"
)

// soRXQOvfl is SO_RXQ_OVFL: when set, each received datagram carries a
// control message with the socket's cumulative count of datagrams the kernel
// dropped because the receive queue was full.
const soRXQOvfl = 40

// tuneUDPSocket requests the receive buffer, enables kernel drop accounting,
// and returns the effective receive buffer size (the kernel doubles the
// request for bookkeeping and clamps it at net.core.rmem_max).
func tuneUDPSocket(conn *net.UDPConn, readBuffer int) (effective int, dropsSupported bool) {
	_ = conn.SetReadBuffer(readBuffer)
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, false
	}
	_ = raw.Control(func(fd uintptr) {
		effective, _ = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
		dropsSupported = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soRXQOvfl, 1) == nil
	})
	return effective, dropsSupported
}

// oobSize is room for the SO_RXQ_OVFL control message.
const oobSize = 64

// kernelDrops extracts the cumulative drop count from the control messages
// of one ReadMsgUDP; ok is false when the datagram carried none.
func kernelDrops(oob []byte) (drops uint64, ok bool) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return 0, false
	}
	for _, m := range msgs {
		if m.Header.Level == syscall.SOL_SOCKET && m.Header.Type == soRXQOvfl && len(m.Data) >= 4 {
			return uint64(nativeEndian.Uint32(m.Data[:4])), true
		}
	}
	return 0, false
}

// nativeEndian is the host byte order the kernel writes the counter in.
var nativeEndian = func() binary.ByteOrder {
	var x uint16 = 1
	if *(*byte)(unsafe.Pointer(&x)) == 1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}()
