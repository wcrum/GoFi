package api

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"connectrpc.com/connect"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"github.com/wcrum/gofi/sandbox"
)

// functionTTL bounds how long a worker's function list is reused unchanged
// when its binding did not change; a restart or new digest changes the
// binding and refreshes at once.
const (
	functionTTL        = 60 * time.Second
	functionRPCTimeout = 3 * time.Second
	functionRPCLimit   = 8
)

// functionCache remembers each library's functions by the binding they were
// read from, shared by every browser poll.
type functionCache struct {
	client *http.Client
	mu     sync.Mutex
	byLib  map[string]*functionEntry
}

type functionEntry struct {
	mu        sync.Mutex // one fetch at a time per library
	endpoint  string
	token     string
	at        time.Time
	functions []*gofiv1.LibraryFunction
}

func newFunctionCache() *functionCache {
	transport := &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 2, IdleConnTimeout: 60 * time.Second}
	return &functionCache{client: &http.Client{Transport: transport}, byLib: map[string]*functionEntry{}}
}

func (c *functionCache) entry(name string) *functionEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.byLib[name]
	if e == nil {
		e = &functionEntry{}
		c.byLib[name] = e
	}
	return e
}

// list returns the functions of the given ready libraries, refreshing stale
// entries concurrently (at most functionRPCLimit at a time).
func (c *functionCache) list(ctx context.Context, libraries []sandbox.Definition, bindings map[string]sandbox.Binding) []*gofiv1.LibraryFunction {
	results := make([][]*gofiv1.LibraryFunction, len(libraries))
	var wg sync.WaitGroup
	slots := make(chan struct{}, functionRPCLimit)
	for i, d := range libraries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = c.functions(ctx, d, bindings[d.Name])
		}()
	}
	wg.Wait()
	var out []*gofiv1.LibraryFunction
	for _, items := range results {
		out = append(out, items...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Library != out[j].Library {
			return out[i].Library < out[j].Library
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// functions returns one library's functions from cache or the worker.
func (c *functionCache) functions(ctx context.Context, d sandbox.Definition, binding sandbox.Binding) []*gofiv1.LibraryFunction {
	e := c.entry(d.Name)
	e.mu.Lock()
	defer e.mu.Unlock()
	same := e.endpoint == binding.Endpoint && e.token == binding.Token
	if same && e.functions != nil && time.Since(e.at) < functionTTL {
		return e.functions
	}
	callCtx, cancel := context.WithTimeout(ctx, functionRPCTimeout)
	defer cancel()
	call := connect.NewRequest(&gofiv1.WorkerListFunctionsRequest{})
	call.Header().Set("Authorization", "Bearer "+binding.Token)
	res, err := gofiv1connect.NewWorkerServiceClient(c.client, binding.Endpoint, connect.WithReadMaxBytes(1<<20)).ListFunctions(callCtx, call)
	if err != nil {
		if same {
			return e.functions // a busy worker keeps its last known list
		}
		return nil
	}
	functions := make([]*gofiv1.LibraryFunction, 0, len(res.Msg.Functions))
	for _, f := range res.Msg.Functions {
		functions = append(functions, &gofiv1.LibraryFunction{Library: d.Name, Name: f.Name, Kind: f.Kind, Description: f.Description, Params: f.Params, ManagedBy: d.ManagedBy, Every: f.Every})
	}
	e.endpoint, e.token, e.at, e.functions = binding.Endpoint, binding.Token, time.Now(), functions
	return functions
}

// invalidate forgets a library's entry (for tests and future Ready hooks).
func (c *functionCache) invalidate(name string) {
	c.mu.Lock()
	delete(c.byLib, name)
	c.mu.Unlock()
}
