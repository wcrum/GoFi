package codec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/pierrec/lz4/v4"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

func TestEveryBuiltinRoundTrips(t *testing.T) {
	input := []byte(`{"sensor":"alpha","note":"a b&c=d/é"}`)
	for _, name := range spec.BuiltinCodecs {
		if slices.Contains(spec.DecodeOnlyCodecs, name) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			steps := []spec.CodecStep{{Type: name}}
			encoded, err := Encode(context.Background(), steps, input, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(context.Background(), steps, encoded, 0, nil)
			if err != nil || !bytes.Equal(decoded, input) {
				t.Fatalf("round trip %q: %v", decoded, err)
			}
		})
	}
}

func TestChainsRunInOrder(t *testing.T) {
	input := []byte("payload")
	encode := []spec.CodecStep{{Type: "gzip"}, {Type: "base64"}}
	encoded, err := Encode(context.Background(), encode, input, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Decoding reverses the order: base64 first, then gzip.
	decoded, err := Decode(context.Background(), []spec.CodecStep{{Type: "base64"}, {Type: "gzip"}}, encoded, 0, nil)
	if err != nil || string(decoded) != "payload" {
		t.Fatalf("%q %v", decoded, err)
	}
	if _, err := Decode(context.Background(), encode, encoded, 0, nil); err == nil {
		t.Fatal("decoding in the wrong order must fail")
	}
}

func TestPythonStepsUseTheInstalledRunner(t *testing.T) {
	SetPythonRunner(func(_ context.Context, ref *spec.PythonRef, kind string, data []byte, _ *message.Message) ([]byte, error) {
		return append([]byte(kind+":"+ref.Function+":"), data...), nil
	})
	t.Cleanup(func() { SetPythonRunner(nil) })
	steps := []spec.CodecStep{{Type: "python", Python: &spec.PythonRef{Library: "lib", Function: "wrap"}}, {Type: "hex"}}
	out, err := Encode(context.Background(), steps, []byte("x"), 0, nil)
	if err != nil || string(out) != "656e636f6465723a777261703a78" { // hex("encoder:wrap:x")
		t.Fatalf("%q %v", out, err)
	}
}

func TestCodecStepJSONShorthandAndValidation(t *testing.T) {
	var cfg spec.ParserCfg
	if err := json.Unmarshal([]byte(`{"format":"json","decoders":["base64",{"type":"python","python":{"library":"l","function":"f"}}]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Decoders[0].Type != "base64" || cfg.Decoders[1].Python.Function != "f" {
		t.Fatalf("%+v", cfg.Decoders)
	}
	for _, bad := range []spec.CodecStep{{Type: "rot13"}, {Type: "python"}, {Type: "base64", Python: &spec.PythonRef{Library: "l", Function: "f"}}} {
		if err := bad.Validate("decoder"); err == nil {
			t.Fatalf("%+v must be rejected", bad)
		}
	}
	if err := (&spec.Encoding{Format: "xml"}).Validate(); err == nil {
		t.Fatal("unknown encoding format must be rejected")
	}
}

func TestBzip2DecodesOnly(t *testing.T) {
	compressed, _ := base64.StdEncoding.DecodeString("QlpoOTFBWSZTWVVaRPcAAAIZgEAAEAASZMAQIAAiAGnqEAMF07Yhg8XckU4UJBVWkT3A") // Python bz2.compress(b"hello bzip2")
	out, err := Decode(context.Background(), []spec.CodecStep{{Type: "bzip2"}}, compressed, 0, nil)
	if err != nil || string(out) != "hello bzip2" {
		t.Fatalf("%q %v", out, err)
	}
	if err := (spec.CodecStep{Type: "bzip2"}).Validate("encoder"); err == nil {
		t.Fatal("bzip2 has no encoder")
	}
	if err := (spec.CodecStep{Type: "bzip2"}).Validate("decoder"); err != nil {
		t.Fatal(err)
	}
}

func TestCompressedOutputStaysWithinTheLimit(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 64<<10)
	for _, name := range []string{"gzip", "zlib", "deflate", "zstd", "snappy", "lz4", "brotli"} {
		t.Run(name, func(t *testing.T) {
			steps := []spec.CodecStep{{Type: name}}
			bomb, err := Encode(context.Background(), steps, big, 1<<20, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(context.Background(), steps, bomb, 4<<10, nil); err == nil || !strings.Contains(err.Error(), "exceeds") && !strings.Contains(err.Error(), "memory") && !strings.Contains(err.Error(), "window") {
				t.Fatalf("a payload over the limit must be refused: %v", err)
			}
		})
	}
}

// allocated is the bytes allocated while fn runs.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestDeclaredBuffersOverTheLimitAreRefusedBeforeAllocating(t *testing.T) {
	payload := bytes.Repeat([]byte("abc"), 2000)
	var lz4Frame, brotliStream bytes.Buffer
	lw := lz4.NewWriter(&lz4Frame)
	if err := lw.Apply(lz4.BlockSizeOption(lz4.Block4Mb)); err != nil {
		t.Fatal(err)
	}
	lw.Write(payload)
	lw.Close()
	bw := brotli.NewWriterOptions(&brotliStream, brotli.WriterOptions{Quality: 6, LGWin: 24})
	bw.Write(payload)
	bw.Close()
	for name, data := range map[string][]byte{"lz4": lz4Frame.Bytes(), "brotli": brotliStream.Bytes()} {
		t.Run(name, func(t *testing.T) {
			var err error
			bytesUsed := allocated(func() { _, err = Decode(context.Background(), []spec.CodecStep{{Type: name}}, data, 4096, nil) })
			if err == nil || !strings.Contains(err.Error(), "limit") {
				t.Fatalf("a declared buffer over the limit is refused: %v", err)
			}
			if bytesUsed > 256<<10 {
				t.Fatalf("refusing allocated %d bytes", bytesUsed)
			}
			// Within the default limit, the same stream decodes.
			if out, err := Decode(context.Background(), []spec.CodecStep{{Type: name}}, data, 16<<20, nil); err != nil || !bytes.Equal(out, payload) {
				t.Fatalf("within the limit: %v", err)
			}
		})
	}
}

func TestBrotliWindowHeaderParsing(t *testing.T) {
	for lgwin := 10; lgwin <= 24; lgwin++ {
		var buf bytes.Buffer
		w := brotli.NewWriterOptions(&buf, brotli.WriterOptions{Quality: 1, LGWin: lgwin})
		w.Write([]byte("x"))
		w.Close()
		// The encoder declares at least an 18-bit window.
		window := 1<<max(lgwin, 18) - 16
		err := checkBrotliWindow(buf.Bytes(), window)
		if err != nil {
			t.Errorf("lgwin %d at its own window: %v", lgwin, err)
		}
		if window > brotliMinWindow {
			if err := checkBrotliWindow(buf.Bytes(), window-1); err == nil {
				t.Errorf("lgwin %d below its window must be refused", lgwin)
			}
		}
	}
}
