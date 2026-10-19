package sink

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"github.com/wcrum/gofi/message"
)

// stdoutFlushInterval bounds how long a line waits in the buffer.
const stdoutFlushInterval = 100 * time.Millisecond

// stdoutSink writes one JSON line per message through a buffered writer:
// an unbuffered write per message is a system call per message. A background
// flusher bounds latency; Flush and Close write everything out.
type stdoutSink struct {
	mu   sync.Mutex
	w    *bufio.Writer
	stop chan struct{}
	once sync.Once
	done chan struct{}
}

// stdoutWriter resolves os.Stdout on each write so a redirected stdout (tests)
// is honored.
type stdoutWriter struct{}

func (stdoutWriter) Write(p []byte) (int, error) { return os.Stdout.Write(p) }

func newStdout() *stdoutSink {
	s := &stdoutSink{w: bufio.NewWriterSize(stdoutWriter{}, 64<<10), stop: make(chan struct{}), done: make(chan struct{})}
	go s.flusher()
	return s
}

func (s *stdoutSink) flusher() {
	defer close(s.done)
	tick := time.NewTicker(stdoutFlushInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			s.mu.Lock()
			if s.w.Buffered() > 0 {
				_ = s.w.Flush()
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

func (s *stdoutSink) Send(msg *message.Message) error {
	var data []byte
	if msg.Payload != nil {
		data = msg.Payload
	} else {
		var err error
		data, err = json.Marshal(map[string]interface{}{"fields": msg.Fields, "meta": msg.Meta})
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write(data); err != nil {
		return err
	}
	return s.w.WriteByte('\n')
}

// Flush writes buffered lines out.
func (s *stdoutSink) Flush(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Flush()
}

func (s *stdoutSink) Close() error {
	s.once.Do(func() { close(s.stop) })
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Flush()
}

var _ io.Writer = stdoutWriter{}
