package codec

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Compressor state is expensive (a gzip writer is about 800 KiB, a zstd
// encoder more), so writers and readers are pooled and Reset between frames
// instead of allocated per message.

var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}
var zlibWriters = sync.Pool{New: func() any { return zlib.NewWriter(io.Discard) }}
var flateWriters = sync.Pool{New: func() any {
	w, _ := flate.NewWriter(io.Discard, flate.DefaultCompression)
	return w
}}

// compressPooled runs one pooled writer over data.
func compressPooled(step string, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(data)/2 + 64)
	var w interface {
		io.WriteCloser
		Reset(io.Writer)
	}
	switch step {
	case "gzip":
		gw := gzipWriters.Get().(*gzip.Writer)
		defer gzipWriters.Put(gw)
		w = gw
	case "zlib":
		zw := zlibWriters.Get().(*zlib.Writer)
		defer zlibWriters.Put(zw)
		w = zw
	default:
		fw := flateWriters.Get().(*flate.Writer)
		defer flateWriters.Put(fw)
		w = fw
	}
	w.Reset(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// zstdEncoder is one shared encoder: EncodeAll is safe for concurrent use
// and keeps its own per-call state in an internal pool.
var zstdEncoder = sync.OnceValues(func() (*zstd.Encoder, error) {
	return zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithZeroFrames(true))
})

func zstdCompress(data []byte) ([]byte, error) {
	enc, err := zstdEncoder()
	if err != nil {
		return nil, err
	}
	return enc.EncodeAll(data, make([]byte, 0, len(data)/2+64)), nil
}

// zstdDecoders pools DecodeAll decoders per output limit (the limit is part
// of the decoder's options, and a flow's limit rarely varies).
var zstdDecoders sync.Map // int(max) -> *sync.Pool

func zstdDecompress(data []byte, max int) ([]byte, error) {
	p, ok := zstdDecoders.Load(max)
	if !ok {
		window := min(uint64(max), zstd.MaxWindowSize)
		if window < zstd.MinWindowSize {
			window = zstd.MinWindowSize
		}
		p, _ = zstdDecoders.LoadOrStore(max, &sync.Pool{New: func() any {
			d, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxWindow(window), zstd.WithDecoderMaxMemory(uint64(max)+1))
			if err != nil {
				return err
			}
			return d
		}})
	}
	pool := p.(*sync.Pool)
	got := pool.Get()
	d, ok := got.(*zstd.Decoder)
	if !ok {
		return nil, got.(error)
	}
	defer pool.Put(d)
	out, err := d.DecodeAll(data, make([]byte, 0, min(4*len(data), max)))
	if err != nil {
		return nil, err
	}
	if len(out) > max {
		return nil, errTooLarge(max)
	}
	return out, nil
}

var gzipReaders = sync.Pool{New: func() any { return new(gzip.Reader) }}
var zlibReaders sync.Pool // io.ReadCloser with zlib.Resetter
var flateReaders = sync.Pool{New: func() any { return flate.NewReader(bytes.NewReader(nil)) }}

// pooledReader returns a reset decompressing reader for data and a release
// function to return it to its pool.
func pooledReader(step string, data []byte) (io.ReadCloser, func(), error) {
	switch step {
	case "gzip":
		r := gzipReaders.Get().(*gzip.Reader)
		if err := r.Reset(bytes.NewReader(data)); err != nil {
			gzipReaders.Put(r)
			return nil, nil, err
		}
		return r, func() { gzipReaders.Put(r) }, nil
	case "zlib":
		if got := zlibReaders.Get(); got != nil {
			r := got.(io.ReadCloser)
			if err := r.(zlib.Resetter).Reset(bytes.NewReader(data), nil); err != nil {
				zlibReaders.Put(r)
				return nil, nil, err
			}
			return r, func() { zlibReaders.Put(r) }, nil
		}
		r, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, nil, err
		}
		return r, func() { zlibReaders.Put(r) }, nil
	default: // deflate
		r := flateReaders.Get().(io.ReadCloser)
		_ = r.(flate.Resetter).Reset(bytes.NewReader(data), nil)
		return r, func() { flateReaders.Put(r) }, nil
	}
}
