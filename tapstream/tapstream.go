// Package tapstream encodes pipeline.Tap events as Server-Sent Events and
// writes them to an http.ResponseWriter. It's the shared implementation
// behind both the local tap subscribe path (api.streamListener, used by the
// in-process driver) and the cross-process tap relay (taprelay, used by the
// Docker/Kubernetes drivers) so both paths produce byte-identical SSE frames.
package tapstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/wcrum/gofi/pipeline"
)

// Event is the JSON shape sent over SSE for each observed message.
type Event struct {
	Timestamp string                 `json:"ts"`
	Dropped   bool                   `json:"dropped"`
	DroppedBy string                 `json:"droppedBy,omitempty"`
	Fields    map[string]interface{} `json:"fields"`
	Meta      map[string]string      `json:"meta"`
	Raw       string                 `json:"raw"`
}

// Serve subscribes to tap and writes SSE frames to w until ctx is done or
// the client disconnects. It returns an error if the response writer does
// not support flushing (streaming is unsupported).
func Serve(ctx context.Context, w http.ResponseWriter, tap *pipeline.Tap) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return fmt.Errorf("tapstream: response writer does not support flushing")
	}

	ch := tap.Subscribe()
	defer tap.Unsubscribe(ch)

	// Heartbeat every 15s so the browser doesn't time out.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case ev := <-ch:
			data := Event{
				Timestamp: ev.Msg.Meta["ts"],
				Dropped:   ev.Dropped,
				DroppedBy: ev.DroppedBy,
				Fields:    ev.Msg.Fields,
				Meta:      ev.Msg.Meta,
				Raw:       string(ev.Msg.Raw),
			}
			b, err := json.Marshal(data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}
