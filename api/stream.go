package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/tapstream"
)

type tapSubscription struct {
	target driver.TapTarget
	cancel context.CancelFunc
}

// WatchTraffic multiplexes all selected listeners over one Connect server
// stream, including listeners added or replaced while the portal stays open.
func (h *Handler) WatchTraffic(ctx context.Context, req *connect.Request[gofiv1.WatchTrafficRequest], stream *connect.ServerStream[gofiv1.WatchTrafficResponse]) error {
	if req.Msg.Name != "" {
		if _, found := h.ctrl.Get(req.Msg.Name); !found {
			return rpcError(connect.CodeNotFound, errors.New("listener not found"))
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	events := make(chan *gofiv1.WatchTrafficResponse, 64)
	subscriptions := map[string]tapSubscription{}
	send := func(event *gofiv1.WatchTrafficResponse) {
		select {
		case events <- event:
		default:
		} // Slow viewers cannot block ingestion.
	}
	refresh := func() {
		wanted := map[string]bool{}
		for _, def := range h.ctrl.List() {
			if req.Msg.Name != "" && req.Msg.Name != def.Name {
				continue
			}
			wanted[def.Name] = true
			lookupCtx, done := context.WithTimeout(ctx, 5*time.Second)
			target, found, err := h.ctrl.Tap(lookupCtx, def.Name)
			done()
			previous, exists := subscriptions[def.Name]
			if err != nil {
				continue
			}
			if exists && found && previous.target.Local == target.Local && previous.target.Addr == target.Addr {
				continue
			}
			if exists {
				previous.cancel()
				delete(subscriptions, def.Name)
			}
			if !found || (target.Local == nil && target.Addr == "") {
				continue
			}
			subCtx, stop := context.WithCancel(ctx)
			subscriptions[def.Name] = tapSubscription{target: target, cancel: stop}
			workers.Add(1)
			go func(name string, target driver.TapTarget) {
				defer workers.Done()
				if target.Local != nil {
					ch := target.Local.Subscribe()
					defer target.Local.Unsubscribe(ch)
					for {
						select {
						case <-subCtx.Done():
							return
						case event := <-ch:
							converted, err := tapstream.Event(event)
							if err == nil && converted != nil {
								send(&gofiv1.WatchTrafficResponse{Listener: name, Event: converted})
							}
						}
					}
				}
				remote := gofiv1connect.NewGoFiServiceClient(client, "http://"+target.Addr)
				for subCtx.Err() == nil {
					relay, err := remote.WatchTraffic(subCtx, connect.NewRequest(&gofiv1.WatchTrafficRequest{Name: name}))
					if err == nil {
						for relay.Receive() {
							event := relay.Msg()
							if event.Event != nil {
								event.Listener = name
								send(event)
							}
						}
						relay.Close()
					}
					timer := time.NewTimer(5 * time.Second)
					select {
					case <-subCtx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}(def.Name, target)
		}
		for name, subscription := range subscriptions {
			if !wanted[name] {
				subscription.cancel()
				delete(subscriptions, name)
			}
		}
	}
	refresh()
	if err := stream.Send(&gofiv1.WatchTrafficResponse{Heartbeat: true}); err != nil {
		return err
	}
	updates := time.NewTicker(5 * time.Second)
	defer updates.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-updates.C:
			refresh()
		case <-heartbeat.C:
			if err := stream.Send(&gofiv1.WatchTrafficResponse{Heartbeat: true}); err != nil {
				return err
			}
		case event := <-events:
			if _, exists := subscriptions[event.Listener]; !exists {
				continue
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}
