// Package codec implements GoFi's ordered byte transformations: built-in
// codecs (spec.BuiltinCodecs: text encodings and compression) in both
// directions, bzip2 for decoding only, and
// Python steps (@gofi.decoder / @gofi.encoder) through an injected runner so
// this package never depends on the worker RPC client.
package codec

import (
	"bytes"
	"compress/bzip2"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"runtime"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/snappy"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
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
	case "base32":
		out = []byte(base32.StdEncoding.EncodeToString(data))
	case "gzip", "zlib", "deflate", "zstd", "lz4", "brotli":
		var buf bytes.Buffer
		var w io.WriteCloser
		switch step {
		case "gzip":
			w = gzip.NewWriter(&buf)
		case "zlib":
			w = zlib.NewWriter(&buf)
		case "deflate":
			w, _ = flate.NewWriter(&buf, flate.DefaultCompression)
		case "zstd":
			encoder, err := zstd.NewWriter(&buf, zstd.WithEncoderConcurrency(1))
			if err != nil {
				return nil, err
			}
			w = encoder
		case "lz4":
			// Small blocks keep frames within any reader's block limit.
			writer := lz4.NewWriter(&buf)
			if err := writer.Apply(lz4.BlockSizeOption(lz4.Block64Kb)); err != nil {
				return nil, err
			}
			w = writer
		case "brotli":
			// The smallest window that covers the input, so a reader's
			// window limit (lz77 history) accepts it.
			w = brotli.NewWriterOptions(&buf, brotli.WriterOptions{Quality: 6, LGWin: brotliWindowBits(len(data))})
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		out = buf.Bytes()
	case "snappy":
		// The block format (Prometheus remote write, Kafka, Cassandra).
		out = snappy.Encode(nil, data)
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
	case "base32":
		encoding := base32.StdEncoding
		if !bytes.ContainsRune(data, '=') {
			encoding = encoding.WithPadding(base32.NoPadding)
		}
		reader = base32.NewDecoder(encoding, bytes.NewReader(data))
	case "hex":
		reader = hex.NewDecoder(bytes.NewReader(data))
	case "deflate":
		release := acquireDecompressor()
		defer release()
		r := flate.NewReader(bytes.NewReader(data))
		reader, closer = r, r
	case "zstd":
		release := acquireDecompressor()
		defer release()
		// Cap the window too: a frame may declare one far larger than the
		// output limit, and the decoder would allocate it up front.
		window := min(uint64(max), zstd.MaxWindowSize)
		if window < zstd.MinWindowSize {
			window = zstd.MinWindowSize
		}
		r, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxWindow(window), zstd.WithDecoderMaxMemory(uint64(max)+1))
		if err != nil {
			return nil, err
		}
		reader, closer = r, zstdCloser{r}
	case "snappy":
		// The block header states the decoded length: refuse before allocating.
		n, err := snappy.DecodedLen(data)
		if err != nil {
			return nil, err
		}
		if n > max {
			return nil, fmt.Errorf("decoded payload exceeds %d bytes", max)
		}
		out, err := snappy.Decode(nil, data)
		if err != nil {
			return nil, err
		}
		return out, nil
	case "lz4":
		// The reader allocates the frame's declared block size up front.
		if err := checkLZ4Frame(data, max); err != nil {
			return nil, err
		}
		release := acquireDecompressor()
		defer release()
		reader = lz4.NewReader(bytes.NewReader(data))
	case "brotli":
		// The reader allocates the stream's declared window up front.
		if err := checkBrotliWindow(data, max); err != nil {
			return nil, err
		}
		release := acquireDecompressor()
		defer release()
		reader = brotli.NewReader(bytes.NewReader(data))
	case "bzip2":
		release := acquireDecompressor()
		defer release()
		reader = bzip2.NewReader(bytes.NewReader(data))
	case "gzip":
		release := acquireDecompressor()
		defer release()
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		reader, closer = r, r
	case "zlib":
		release := acquireDecompressor()
		defer release()
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

// zstdCloser adapts zstd.Decoder.Close, which returns nothing.
type zstdCloser struct{ d *zstd.Decoder }

func (c zstdCloser) Close() error { c.d.Close(); return nil }

// decompressors bounds concurrent decompressions: each holds megabytes of
// decoder state (bzip2 about 3.6 MB) however small its input, so a burst of
// tiny frames must not multiply that without limit.
var decompressors = make(chan struct{}, 2*runtime.GOMAXPROCS(0))

func acquireDecompressor() (release func()) {
	decompressors <- struct{}{}
	return func() { <-decompressors }
}

// The smallest block or window a reader always accepts, whatever the output
// limit: LZ4's smallest block, and the 18-bit window Brotli encoders declare
// at minimum (andybalholm/brotli never writes less).
const (
	lz4MinBlock     = 64 << 10
	brotliMinWindow = 1<<18 - 16
)

// checkLZ4Frame refuses an LZ4 frame whose declared block size exceeds the
// output limit (the reader allocates it before decoding anything), and the
// legacy format, whose blocks are always 8 MB.
func checkLZ4Frame(data []byte, max int) error {
	const frameMagic, legacyMagic = 0x184D2204, 0x184C2102
	if len(data) < 6 {
		return fmt.Errorf("lz4: frame too short")
	}
	switch magic := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24; magic {
	case frameMagic:
	case legacyMagic:
		return fmt.Errorf("lz4: the legacy frame format is not supported")
	default:
		return fmt.Errorf("lz4: not an LZ4 frame")
	}
	// BD byte: bits 6-4 select 64 KB (4) to 4 MB (7).
	id := (data[5] >> 4) & 7
	if id < 4 {
		return fmt.Errorf("lz4: invalid block size")
	}
	if block := 1 << (16 + 2*(int(id)-4)); block > max && block > lz4MinBlock {
		return fmt.Errorf("lz4: %d-byte blocks exceed the %d-byte limit", block, max)
	}
	return nil
}

// checkBrotliWindow refuses a Brotli stream whose declared window (WBITS,
// RFC 7932 section 9.1) exceeds the output limit.
func checkBrotliWindow(data []byte, max int) error {
	if len(data) == 0 {
		return fmt.Errorf("brotli: empty stream")
	}
	b := int(data[0])
	if len(data) > 1 {
		b |= int(data[1]) << 8
	}
	var bits int
	switch {
	case b&1 == 0:
		bits = 16
	case (b>>1)&7 != 0:
		bits = 17 + (b>>1)&7
	case (b>>4)&7 == 1:
		return fmt.Errorf("brotli: large-window streams are not supported")
	case (b>>4)&7 != 0:
		bits = 8 + (b>>4)&7
	default:
		bits = 17
	}
	if window := 1<<bits - 16; window > max && window > brotliMinWindow {
		return fmt.Errorf("brotli: %d-byte window exceeds the %d-byte limit", window, max)
	}
	return nil
}

// brotliWindowBits is the smallest Brotli window covering n bytes (10-24).
func brotliWindowBits(n int) int {
	bits := 10
	for bits < 24 && 1<<bits-16 < n {
		bits++
	}
	return bits
}
