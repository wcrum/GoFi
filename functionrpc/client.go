// Package functionrpc invokes isolated Python functions through generated
// ConnectRPC clients. It never evaluates Python in the GoFi controller.
package functionrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/spec"
	"google.golang.org/protobuf/types/known/structpb"
)

type Binding struct {
	Endpoint       string `json:"endpoint"`
	Token          string `json:"token"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}
type Resolver func(context.Context) (map[string]Binding, error)

var resolverMu sync.RWMutex
var resolve Resolver
var transport = &http.Transport{Proxy: nil, ResponseHeaderTimeout: 5 * time.Second}
var client = &http.Client{Transport: transport}

func SetResolver(r Resolver) { resolverMu.Lock(); resolve = r; resolverMu.Unlock() }
func Bindings(ctx context.Context) (map[string]Binding, error) {
	resolverMu.RLock()
	r := resolve
	resolverMu.RUnlock()
	if r != nil {
		return r(ctx)
	}
	var bindings map[string]Binding
	if raw := os.Getenv("GOFI_PYTHON_BINDINGS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &bindings); err != nil {
			return nil, fmt.Errorf("invalid Python runtime bindings")
		}
	}
	return bindings, nil
}
func Environment(ctx context.Context, def spec.ListenerDefinition) (string, error) {
	bindings, err := Bindings(ctx)
	if err != nil {
		return "", err
	}
	refs := []*spec.PythonRef{def.Source.Python, def.Parser.Python}
	for _, s := range def.Stages {
		refs = append(refs, s.Python)
	}
	for _, s := range def.Sinks {
		refs = append(refs, s.Python)
	}
	if def.DeadLetterSink != nil {
		refs = append(refs, def.DeadLetterSink.Python)
	}
	used := map[string]Binding{}
	for _, ref := range refs {
		if ref != nil {
			binding, ok := bindings[ref.Library]
			if !ok {
				return "", fmt.Errorf("Python library %q is not ready", ref.Library)
			}
			used[ref.Library] = binding
		}
	}
	data, err := json.Marshal(used)
	return string(data), err
}

func Invoke(ctx context.Context, ref *spec.PythonRef, kind string, msg *message.Message) (*gofiv1.WorkerInvokeResponse, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	bindings, err := Bindings(ctx)
	if err != nil {
		return nil, err
	}
	binding, ok := bindings[ref.Library]
	if !ok {
		return nil, fmt.Errorf("Python library %q is not ready", ref.Library)
	}
	timeout := binding.TimeoutSeconds
	if timeout <= 0 || timeout > 30 {
		timeout = 5
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	fields, err := structpb.NewStruct(msg.Fields)
	if err != nil {
		return nil, err
	}
	meta := map[string]any{}
	for k, v := range msg.Meta {
		meta[k] = v
	}
	metaValue, _ := structpb.NewStruct(meta)
	req := connect.NewRequest(&gofiv1.WorkerInvokeRequest{Name: ref.Function, Kind: kind, Raw: msg.Raw, Fields: fields, Meta: metaValue})
	req.Header().Set("Authorization", "Bearer "+binding.Token)
	rpc := gofiv1connect.NewWorkerServiceClient(client, binding.Endpoint, connect.WithReadMaxBytes(2<<20), connect.WithSendMaxBytes(2<<20))
	res, err := rpc.Invoke(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("Python %s %s/%s: %w", kind, ref.Library, ref.Function, err)
	}
	return res.Msg, nil
}
