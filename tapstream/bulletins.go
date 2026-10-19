package tapstream

import (
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/observability"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Bulletin converts one bulletin to its RPC form.
func Bulletin(b observability.Bulletin) *gofiv1.Bulletin {
	return &gofiv1.Bulletin{Id: b.ID, Flow: b.Flow, Severity: b.Severity, Component: b.Component, Message: b.Message,
		Count: int64(b.Count), FirstSeen: timestamppb.New(b.FirstSeen), LastSeen: timestamppb.New(b.LastSeen)}
}

// FromBulletin converts an RPC bulletin back (for merging remote boards).
func FromBulletin(b *gofiv1.Bulletin) observability.Bulletin {
	return observability.Bulletin{ID: b.Id, Flow: b.Flow, Severity: b.Severity, Component: b.Component, Message: b.Message,
		Count: uint64(b.Count), FirstSeen: b.FirstSeen.AsTime(), LastSeen: b.LastSeen.AsTime()}
}
