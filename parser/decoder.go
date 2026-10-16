package parser

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

const defaultMaxDecodedBytes = 1 << 20

type decodingParser struct {
	parser Parser
	steps  []string
	max    int
}

func withDecoders(p Parser, cfg spec.ParserCfg) (Parser, error) {
	if len(cfg.Decoders) == 0 {
		return p, nil
	}
	for _, step := range cfg.Decoders {
		switch step {
		case "base64", "base64url", "hex", "gzip", "zlib", "url":
		default:
			return nil, fmt.Errorf("parser: unknown decoder %q", step)
		}
	}
	max := cfg.MaxDecodedBytes
	if max == 0 {
		max = defaultMaxDecodedBytes
	}
	return &decodingParser{parser: p, steps: append([]string(nil), cfg.Decoders...), max: max}, nil
}

// Parse uses a temporary message so parsers see the decoded payload, while
// taps, routing expressions' raw variable, and sinks retain the source bytes.
// Failed decoding never exposes partially decoded fields.
func (p *decodingParser) Parse(msg *message.Message) error {
	decoded := msg.Raw
	for i, step := range p.steps {
		var err error
		decoded, err = decode(step, decoded, p.max)
		if err != nil {
			return fmt.Errorf("parser: decoder %d (%s): %w", i+1, step, err)
		}
	}
	parsed := *msg
	parsed.Raw = decoded
	return p.parser.Parse(&parsed)
}

func decode(step string, data []byte, max int) ([]byte, error) {
	var reader io.Reader
	var closer io.Closer
	switch step {
	case "base64", "base64url":
		encoding := base64.StdEncoding
		if step == "base64url" {
			encoding = base64.URLEncoding
		}
		// Both padded and unpadded representations are common in protocols.
		if !bytes.ContainsRune(data, '=') {
			encoding = encoding.WithPadding(base64.NoPadding)
		}
		reader = base64.NewDecoder(encoding.Strict(), bytes.NewReader(data))
	case "hex":
		reader = hex.NewDecoder(bytes.NewReader(data))
	case "gzip":
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		reader, closer = r, r
	case "zlib":
		r, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		reader, closer = r, r
	case "url":
		// Query decoding never expands input. Limit the input first so a
		// deliberately oversized frame cannot allocate an unbounded string.
		if max <= int(^uint(0)>>1)/3 && len(data) > max*3 {
			return nil, fmt.Errorf("decoded payload exceeds %d bytes", max)
		}
		value, err := url.QueryUnescape(string(data))
		if err != nil {
			return nil, err
		}
		reader = strings.NewReader(value)
	default:
		return nil, fmt.Errorf("unknown decoder %q", step)
	}
	if closer != nil {
		defer closer.Close()
	}
	// Read at most the cap, then probe one byte rather than adding one to
	// max (which could overflow for an explicitly configured large limit).
	out, err := io.ReadAll(io.LimitReader(reader, int64(max)))
	if err != nil {
		return nil, err
	}
	var extra [1]byte
	n, err := io.ReadFull(reader, extra[:])
	if n != 0 {
		return nil, fmt.Errorf("decoded payload exceeds %d bytes", max)
	}
	if err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}
