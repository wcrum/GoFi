package framing

import (
	"encoding/binary"
	"fmt"
	"io"
)

type lengthPrefixFramer struct {
	r         io.Reader
	prefixLen int // 2 or 4 bytes
	maxBytes  int
}

func newLengthPrefix(r io.Reader, prefixLen, maxBytes int) Framer {
	return &lengthPrefixFramer{r: r, prefixLen: prefixLen, maxBytes: maxBytes}
}

func (f *lengthPrefixFramer) ReadFrame() ([]byte, error) {
	hdr := make([]byte, f.prefixLen)
	if _, err := io.ReadFull(f.r, hdr); err != nil {
		return nil, err
	}
	var length uint32
	switch f.prefixLen {
	case 2:
		length = uint32(binary.BigEndian.Uint16(hdr))
	case 4:
		length = binary.BigEndian.Uint32(hdr)
	default:
		return nil, fmt.Errorf("framing: invalid prefix length %d", f.prefixLen)
	}
	if int(length) > f.maxBytes {
		return nil, fmt.Errorf("framing: declared frame size %d exceeds max %d", length, f.maxBytes)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(f.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
