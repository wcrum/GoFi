package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/wcrum/gofi/driver"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/sink"
	"github.com/wcrum/gofi/spec"
)

const maxReplayBytes = 1 << 20

func (h *Handler) ResolveTargets(ctx context.Context, req *connect.Request[gofiv1.ResolveTargetsRequest]) (*connect.Response[gofiv1.ResolveTargetsResponse], error) {
	if req.Msg.Target == nil {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("target is required"))
	}
	var target spec.KubernetesTarget
	if err := fromProto(req.Msg.Target, &target); err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	if err := h.ctrl.ValidateTarget(target, req.Msg.Network); err != nil {
		return nil, rpcError(connect.CodeInvalidArgument, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	endpoints, err := h.ctrl.ResolveTargets(ctx, target, req.Msg.Network)
	if err != nil {
		return nil, rpcError(connect.CodeUnavailable, err)
	}
	result := &gofiv1.ResolveTargetsResponse{}
	for _, endpoint := range endpoints {
		value := &gofiv1.Endpoint{}
		if err := toProto(endpoint, value); err != nil {
			return nil, rpcError(connect.CodeInternal, err)
		}
		result.Endpoints = append(result.Endpoints, value)
	}
	return connect.NewResponse(result), nil
}

// Replay sends exactly the provided bytes once to each resolved destination.
// Sent reports a successful network write, not an application acknowledgement.
func (h *Handler) Replay(ctx context.Context, req *connect.Request[gofiv1.ReplayRequest]) (*connect.Response[gofiv1.ReplayResponse], error) {
	request := req.Msg
	if request.Network != "tcp" && request.Network != "udp" {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("network must be tcp or udp"))
	}
	if len(request.Raw) == 0 || len(request.Raw) > maxReplayBytes {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("raw must contain 1 byte to 1 MiB, including framing bytes"))
	}
	if request.Network == "udp" && len(request.Raw) > 65507 {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("UDP replay payload exceeds the 65507 byte datagram limit"))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	endpoints := []driver.Endpoint{}
	if request.Target != nil {
		if request.Url != "" {
			return nil, rpcError(connect.CodeInvalidArgument, errors.New("choose URL or Kubernetes selector target"))
		}
		var target spec.KubernetesTarget
		if err := fromProto(request.Target, &target); err != nil {
			return nil, rpcError(connect.CodeInvalidArgument, err)
		}
		if err := h.ctrl.ValidateTarget(target, request.Network); err != nil {
			return nil, rpcError(connect.CodeInvalidArgument, err)
		}
		var err error
		endpoints, err = h.ctrl.ResolveTargets(ctx, target, request.Network)
		if err != nil {
			return nil, rpcError(connect.CodeUnavailable, err)
		}
	} else {
		if err := sink.Validate(spec.SinkCfg{Type: request.Network, URL: request.Url}); err != nil {
			return nil, rpcError(connect.CodeInvalidArgument, err)
		}
		parsed, _ := url.Parse(request.Url)
		port, _ := strconv.Atoi(parsed.Port())
		endpoints = append(endpoints, driver.Endpoint{Name: parsed.Host, Kind: "network", Type: "Direct", Host: parsed.Hostname(), Ports: []driver.EndpointPort{{Port: int32(port), Protocol: request.Network, URL: request.Url}}})
	}
	if len(endpoints) == 0 {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("selector has no ready running Pod destinations"))
	}
	if len(endpoints) > driver.MaxResolvedTargets {
		return nil, rpcError(connect.CodeInvalidArgument, errors.New("replay supports at most 100 Pod destinations; narrow the selector"))
	}
	response := &gofiv1.ReplayResponse{Deliveries: make([]*gofiv1.ReplayDelivery, len(endpoints))}
	var workers sync.WaitGroup
	for i, endpoint := range endpoints {
		value := &gofiv1.Endpoint{}
		if err := toProto(endpoint, value); err != nil {
			return nil, rpcError(connect.CodeInternal, err)
		}
		delivery := &gofiv1.ReplayDelivery{Endpoint: value}
		response.Deliveries[i] = delivery
		workers.Add(1)
		go func() {
			defer workers.Done()
			if len(endpoint.Ports) != 1 {
				delivery.Error = "resolved destination must have exactly one port"
				return
			}
			parsed, err := url.Parse(endpoint.Ports[0].URL)
			if err != nil || parsed.Scheme != request.Network || parsed.Hostname() == "" || parsed.Port() == "" {
				delivery.Error = "invalid resolved destination"
				return
			}
			if err := sendReplay(ctx, request.Network, parsed.Host, request.Raw); err != nil {
				delivery.Error = err.Error()
				return
			}
			delivery.Sent = true
		}()
	}
	workers.Wait()
	return connect.NewResponse(response), nil
}
func sendReplay(ctx context.Context, network, address string, raw []byte) error {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if network == "udp" {
		n, err := conn.Write(raw)
		if err == nil && n != len(raw) {
			return io.ErrShortWrite
		}
		return err
	}
	for len(raw) > 0 {
		n, err := conn.Write(raw)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		raw = raw[n:]
	}
	return nil
}
