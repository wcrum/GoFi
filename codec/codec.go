// Package codec implements GoFi's ordered byte transformations: built-in
// codecs (base64, base64url, hex, gzip, zlib, url) in both directions, and
// Python steps (@gofi.decoder / @gofi.encoder) through an injected runner so
// this package never depends on the worker RPC client.
package codec

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// DefaultMaxBytes caps each step's output when no limit is configured.
const DefaultMaxBytes = 1 << 20

// PythonRunner invokes a Python codec function ("decoder" or "encoder") with
// the current bytes; msg supplies fields and meta for context.
type PythonRunner func(ctx context.Context, ref *spec.PythonRef, kind string, data []byte, msg *message.Message) ([]byte, error)

var (
	runnerMu sync.RWMutex
	runner   PythonRunner
)

// SetPythonRunner installs the runner used for Python steps.
func SetPythonRunner(r PythonRunner) { runnerMu.Lock(); runner = r; runnerMu.Unlock() }

func runPython(ctx context.Context, step spec.CodecStep, kind string, data []byte, msg *message.Message) ([]byte, error) {
	runnerMu.RLock()
	r := runner
	runnerMu.RUnlock()
	if r == nil {
		return nil, fmt.Errorf("python %s %s.%s: no Python runtime is available", kind, step.Python.Library, step.Python.Function)
	}
	return r(ctx, step.Python, kind, data, msg)
}

// Decode applies steps in order (decoding direction).
func Decode(ctx context.Context, steps []spec.CodecStep, data []byte, max int, msg *message.Message) ([]byte, error) {
	return apply(ctx, "decoder", steps, data, max, msg)
}

// Encode applies steps in order (encoding direction).
func Encode(ctx context.Context, steps []spec.CodecStep, data []byte, max int, msg *message.Message) ([]byte, error) {
	return apply(ctx, "encoder", steps, data, max, msg)
}

func apply(ctx context.Context, kind string, steps []spec.CodecStep, data []byte, max int, msg *message.Message) ([]byte, error) {
	if max <= 0 {
		max = DefaultMaxBytes
	}
	out := data
	for i, step := range steps {
		var err error
		switch {
		case step.Type == "python":
			out, err = runPython(ctx, step, kind, out, msg)
			if err == nil && len(out) > max {
				err = fmt.Errorf("output exceeds %d bytes", max)
			}
		case kind == "decoder":
			out, err = decodeBuiltin(step.Type, out, max)
		default:
			out, err = encodeBuiltin(step.Type, out, max)
		}
		if err != nil {
			return nil, fmt.Errorf("%s %d (%s): %w", kind, i+1, label(step), err)
		}
	}
	return out, nil
}

func label(step spec.CodecStep) string {
	if step.Type == "python" && step.Python != nil {
		return "python " + step.Python.Library + "." + step.Python.Function
	}
	return step.Type
}

// encodeBuiltin is the inverse of decodeBuiltin for each built-in codec.
func encodeBuiltin(step string, data []byte, max int) ([]byte, error) {
	var out []byte
	switch step {
	case "base64":
		out = []byte(base64.StdEncoding.EncodeToString(data))
	case "base64url":
		out = []byte(base64.URLEncoding.EncodeToString(data))
	case "hex":
		out = []byte(hex.EncodeToString(data))
	case "gzip", "zlib":
		var buf bytes.Buffer
		var w io.WriteCloser
		if step == "gzip" {
			w = gzip.NewWriter(&buf)
		} else {
			w = zlib.NewWriter(&buf)
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		out = buf.Bytes()
	case "url":
		out = []byte(url.QueryEscape(string(data)))
	default:
		return nil, fmt.Errorf("unknown encoder %q", step)
	}
	if len(out) > max {
		return nil, fmt.Errorf("encoded payload exceeds %d bytes", max)
	}
	return out, nil
}

func decodeBuiltin(step string, data []byte, max int) ([]byte, error) {
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
