package codec

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
)

// Pooled compressors must not leak state between frames: many goroutines
// round-trip distinct payloads through every pooled codec at once.
func TestPooledCodecsRoundTripConcurrently(t *testing.T) {
	var wg sync.WaitGroup
	for _, name := range []string{"gzip", "zlib", "deflate", "zstd"} {
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(name string, g int) {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					input := bytes.Repeat([]byte(fmt.Sprintf("%s-%d-%d|", name, g, i)), 1+i%40)
					steps := []spec.CodecStep{{Type: name}}
					encoded, err := Encode(context.Background(), steps, input, 0, nil)
					if err != nil {
						t.Error(err)
						return
					}
					decoded, err := Decode(context.Background(), steps, encoded, 0, nil)
					if err != nil || !bytes.Equal(decoded, input) {
						t.Errorf("%s: round trip mismatch (%v)", name, err)
						return
					}
				}
			}(name, g)
		}
	}
	wg.Wait()
}

func TestZstdDecodeRespectsLimit(t *testing.T) {
	input := bytes.Repeat([]byte("z"), 100_000)
	encoded, err := Encode(context.Background(), []spec.CodecStep{{Type: "zstd"}}, input, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(context.Background(), []spec.CodecStep{{Type: "zstd"}}, encoded, 50_000, nil); err == nil {
		t.Fatal("decoding past the limit succeeded")
	}
	out, err := Decode(context.Background(), []spec.CodecStep{{Type: "zstd"}}, encoded, 200_000, nil)
	if err != nil || !bytes.Equal(out, input) {
		t.Fatalf("decode within limit: %v", err)
	}
}

// The per-message budget is shared across every decode of one message.
func TestMessageBudgetAccumulatesAcrossDecodes(t *testing.T) {
	msg := &message.Message{Fields: map[string]any{}, Meta: map[string]string{}}
	payload := bytes.Repeat([]byte("b"), 1<<20)
	encoded, _ := Encode(context.Background(), []spec.CodecStep{{Type: "gzip"}}, payload, 0, nil)
	budget := MessageBudget()
	rounds := budget / len(payload)
	for i := 0; i < rounds; i++ {
		if _, err := Decode(context.Background(), []spec.CodecStep{{Type: "gzip"}}, encoded, 0, msg); err != nil {
			t.Fatalf("decode %d within budget: %v", i, err)
		}
	}
	_, err := Decode(context.Background(), []spec.CodecStep{{Type: "gzip"}}, encoded, 0, msg)
	if !IsBudgetExceeded(err) {
		t.Fatalf("decode past the budget: %v", err)
	}
	// Without a message there is no cross-step budget, only the per-step cap.
	if _, err := Decode(context.Background(), []spec.CodecStep{{Type: "gzip"}}, encoded, 0, nil); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkCodecEncode(b *testing.B) {
	payload := bytes.Repeat([]byte(`{"type":"a-f-G","lat":38.9,"lon":-77.0,"id":"sensor-42","ts":"2026-10-17T00:00:00Z"}`), 8)
	for _, c := range []string{"gzip", "zstd", "snappy", "base64"} {
		b.Run(c, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Encode(context.Background(), []spec.CodecStep{{Type: c}}, payload, 0, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCodecDecode(b *testing.B) {
	payload := bytes.Repeat([]byte(`{"type":"a-f-G","lat":38.9,"lon":-77.0,"id":"sensor-42","ts":"2026-10-17T00:00:00Z"}`), 8)
	for _, c := range []string{"gzip", "zstd", "snappy", "base64"} {
		encoded, _ := Encode(context.Background(), []spec.CodecStep{{Type: c}}, payload, 0, nil)
		b.Run(c, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Decode(context.Background(), []spec.CodecStep{{Type: c}}, encoded, 0, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
