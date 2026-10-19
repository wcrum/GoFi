package driver

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// transportOf maps a listen network or network-sink type ("tcp", "udp",
// "quic") to the L4 protocol its port carries. QUIC runs over UDP, so every
// port publication (Docker host port, Service, UDS allow, Gateway listener
// and route) treats it as UDP; the QUIC handshake and streams themselves are
// the source's and sink's business. Anything else, including "", is TCP.
func transportOf(network string) corev1.Protocol {
	switch strings.ToLower(network) {
	case "udp", "quic":
		return corev1.ProtocolUDP
	default:
		return corev1.ProtocolTCP
	}
}

// isNetworkTarget reports whether a selector-target network is one GoFi can
// resolve Pods for: plain TCP/UDP or QUIC (whose Pod ports are UDP).
func isNetworkTarget(network string) bool {
	switch network {
	case "tcp", "udp", "quic":
		return true
	}
	return false
}
