// Package tapstream converts observed pipeline messages to typed ConnectRPC
// traffic events shared by the portal and isolated listener relay.
package tapstream

import (
	"strings"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/pipeline"
	"google.golang.org/protobuf/types/known/structpb"
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
