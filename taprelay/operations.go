package taprelay

import (
	"context"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/tapstream"
)

// operations serves this runtime's bulletin board to the controller, which
// polls it (with a shared cache) instead of each browser reaching the Pod.
type operations struct {
	gofiv1connect.UnimplementedOperationsServiceHandler
}

func (operations) ListBulletins(_ context.Context, req *connect.Request[gofiv1.ListBulletinsRequest]) (*connect.Response[gofiv1.ListBulletinsResponse], error) {
	out := &gofiv1.ListBulletinsResponse{}
	for _, b := range observability.Bulletins(req.Msg.Flow) {
		out.Bulletins = append(out.Bulletins, tapstream.Bulletin(b))
	}
	return connect.NewResponse(out), nil
}
