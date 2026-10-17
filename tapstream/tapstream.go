// Package tapstream converts observed pipeline messages to typed ConnectRPC
// traffic events and route health shared by the portal and isolated
// listener relay.
package tapstream

import (
	"strings"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/pipeline"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Event snapshots the traffic envelope after parsing/stage processing.
func Event(event pipeline.TapEvent) (*gofiv1.TrafficEvent, error) {
	if event.Msg == nil {
		return nil, nil
	}
	fields, err := structpb.NewStruct(event.Msg.Fields)
	if err != nil {
		return nil, err
	}
	return &gofiv1.TrafficEvent{
		Ts: event.Msg.Meta["ts"], Dropped: event.Dropped, DroppedBy: event.DroppedBy,
		Fields: fields, Meta: event.Msg.Meta, Raw: strings.ToValidUTF8(string(event.Msg.Raw), "\uFFFD"),
	}, nil
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
