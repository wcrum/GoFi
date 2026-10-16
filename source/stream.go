package source

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

func streamHeader(cfg *spec.StreamSubscribe) http.Header {
	header := http.Header{}
	sink.ApplyHTTPAuth(header, cfg.Auth)
	for name, value := range cfg.Headers {
		header.Set(name, value)
	}
	return header
}

// runWebSocket reads messages until the connection ends; each text or binary
// message is one emit.
func runWebSocket(ctx context.Context, flow string, cfg *spec.StreamSubscribe, emit Emit) error {
	conn, _, err := websocket.Dial(ctx, cfg.URL, &websocket.DialOptions{HTTPHeader: streamHeader(cfg)})
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(MaxMessageBytes)
	if cfg.Send != "" {
		if err := conn.Write(ctx, websocket.MessageText, []byte(cfg.Send)); err != nil {
			return fmt.Errorf("send: %w", err)
		}
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		observability.SourceEvent(flow, "websocket", "ok")
		emit(ctx, data, "websocket:"+cfg.URL)
	}
}

// sseStream is one SSE source; lastID survives reconnects and is resent as
// Last-Event-ID so the server can resume.
type sseStream struct {
	cfg    *spec.StreamSubscribe
	lastID string
}

// run reads a text/event-stream until it ends; each event's data
// (multi-line data joined with "\n") is one emit.
func (s *sseStream) run(ctx context.Context, flow string, emit Emit) error {
	return s.read(ctx, func(data []byte) {
		observability.SourceEvent(flow, "sse", "ok")
		emit(ctx, data, "sse:"+s.cfg.URL)
	})
}

func (s *sseStream) read(ctx context.Context, onEvent func([]byte)) error {
	cfg := s.cfg
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return err
	}
	req.Header = streamHeader(cfg)
	req.Header.Set("Accept", "text/event-stream")
	if s.lastID != "" {
		req.Header.Set("Last-Event-ID", s.lastID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 64<<10), MaxMessageBytes)
	var data bytes.Buffer
	hasData := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if hasData {
				onEvent(bytes.Clone(data.Bytes()))
			}
			data.Reset()
			hasData = false
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			hasData = true
		case "id":
			s.lastID = value
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}
