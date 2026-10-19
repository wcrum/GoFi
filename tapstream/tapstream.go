// Package tapstream converts observed pipeline messages to typed ConnectRPC
// traffic events and route health shared by the portal and isolated
// listener relay.
package tapstream

import (
	"strings"
	"unicode/utf8"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/pipeline"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Event snapshots the traffic envelope after parsing/stage processing,
// bounded by the configured per-event limit (MaxBytes).
func Event(event pipeline.TapEvent) (*gofiv1.TrafficEvent, error) {
	return EventLimited(event, MaxBytes())
}

// EventLimited is Event with an explicit limit on raw and on every string in
// fields and meta (0: unlimited). Cut strings end in an ellipsis and the
// event's truncated flag is set. The message's own maps are never modified.
func EventLimited(event pipeline.TapEvent, maxBytes int) (*gofiv1.TrafficEvent, error) {
	if event.Msg == nil {
		return nil, nil
	}
	truncated := false
	raw := strings.ToValidUTF8(string(event.Msg.Raw), "�")
	if cut, did := clip(raw, maxBytes); did {
		raw, truncated = cut, true
	}
	fieldValues, did := clipValue(event.Msg.Fields, maxBytes)
	truncated = truncated || did
	fields, err := structpb.NewStruct(fieldValues.(map[string]any))
	if err != nil {
		return nil, err
	}
	meta := event.Msg.Meta
	for _, value := range meta {
		if _, did := clip(value, maxBytes); did {
			copied := make(map[string]string, len(meta))
			for k, v := range meta {
				copied[k], _ = clip(v, maxBytes)
			}
			meta, truncated = copied, true
			break
		}
	}
	return &gofiv1.TrafficEvent{
		Ts: event.Msg.Meta["ts"], Dropped: event.Dropped, DroppedBy: event.DroppedBy,
		Fields: fields, Meta: meta, Raw: raw, Truncated: truncated,
	}, nil
}

// clip bounds s to maxBytes of valid UTF-8 plus an ellipsis.
func clip(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…", true
}

// clipValue walks a parsed value and returns a copy with long strings
// clipped, or the value itself when nothing needed clipping.
func clipValue(value any, maxBytes int) (any, bool) {
	if maxBytes <= 0 {
		return value, false
	}
	switch v := value.(type) {
	case string:
		return clip(v, maxBytes)
	case map[string]any:
		var copied map[string]any
		for key, item := range v {
			clipped, did := clipValue(item, maxBytes)
			if !did {
				continue
			}
			if copied == nil {
				copied = make(map[string]any, len(v))
				for k, original := range v {
					copied[k] = original
				}
			}
			copied[key] = clipped
		}
		if copied == nil {
			return v, false
		}
		return copied, true
	case []any:
		var copied []any
		for i, item := range v {
			clipped, did := clipValue(item, maxBytes)
			if !did {
				continue
			}
			if copied == nil {
				copied = append(make([]any, 0, len(v)), v...)
			}
			copied[i] = clipped
		}
		if copied == nil {
			return v, false
		}
		return copied, true
	}
	return value, false
}

// Routes converts what this process observed on a flow's outbound routes.
func Routes(flow string) []*gofiv1.RouteHealth {
	var out []*gofiv1.RouteHealth
	for _, route := range observability.Routes(flow) {
		item := &gofiv1.RouteHealth{Route: route.Route, Successes: int64(route.Successes), Failures: int64(route.Failures), LastError: route.LastError, ErrorClass: route.ErrorClass, Failing: route.Failing()}
		if !route.LastSuccess.IsZero() {
			item.LastSuccessAt = timestamppb.New(route.LastSuccess)
		}
		if !route.LastFailure.IsZero() {
			item.LastFailureAt = timestamppb.New(route.LastFailure)
		}
		out = append(out, item)
	}
	return out
}

// Stats converts a flow's counters in this process to their RPC form. up is
// left to the caller, which knows whether the runtime is running.
func Stats(flow string) *gofiv1.FlowStats {
	c := observability.FlowSnapshot(flow)
	return &gofiv1.FlowStats{Flow: flow, Observed: c.Observed, Rejected: c.Rejected, ParseErrors: c.ParseErrors, Delivered: c.Delivered, SinkFailures: c.SinkFailures, TapDropped: c.TapDropped}
}
