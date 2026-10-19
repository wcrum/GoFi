package assistant

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ErrDisabled is returned by Chat while the configuration is off.
var ErrDisabled = errors.New("the assistant is not enabled; an administrator can enable it under Administration")

// ErrNoKey is returned when no API key is stored or in the environment.
var ErrNoKey = errors.New("the assistant has no API key; set one under Administration or with GOFI_ASSISTANT_API_KEY")

// requestTimeout bounds one model request. Tool loops make several.
const requestTimeout = 5 * time.Minute

func newClient(c Configuration, key string) anthropic.Client {
	c = Effective(c)
	opts := []option.RequestOption{option.WithAPIKey(key), option.WithMaxRetries(2), option.WithRequestTimeout(requestTimeout), option.WithHTTPClient(httpClient())}
	if c.BaseURL != "" {
		// The SDK appends /v1/messages itself; a base copied from an
		// OpenAI-style configuration often ends in /v1.
		opts = append(opts, option.WithBaseURL(strings.TrimSuffix(strings.TrimRight(c.BaseURL, "/"), "/v1")))
	}
	return anthropic.NewClient(opts...)
}

// ErrPrivateEndpoint is returned when the endpoint resolves to a loopback,
// link-local, or private address and GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT
// is not "1". Any portal user can set the endpoint, so by default the
// controller refuses to be pointed at itself, the cluster, or the cloud
// metadata service; an operator with an in-cluster gateway opts in.
var ErrPrivateEndpoint = errors.New("the endpoint resolves to a private or local address; set GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT=1 to allow an internal gateway")

func allowPrivateEndpoint() bool { return os.Getenv("GOFI_ASSISTANT_ALLOW_PRIVATE_ENDPOINT") == "1" }

func privateAddress(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast()
}

// httpClient never follows redirects (a redirect would carry the x-api-key
// header to another host) and checks the resolved address at dial time, so a
// hostname that rebinds to a private address is caught too.
func httpClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	// A stalled endpoint must not hold a turn slot for the whole turn.
	transport.ResponseHeaderTimeout = 60 * time.Second
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var firstErr error
		for _, addr := range addrs {
			if !allowPrivateEndpoint() && privateAddress(addr.IP) {
				if firstErr == nil {
					firstErr = ErrPrivateEndpoint
				}
				continue
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			if firstErr == nil {
				firstErr = err
			}
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("no address for %s", host)
		}
		return nil, firstErr
	}
	return &http.Client{Transport: limitingTransport{transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// HTTPClient is httpClient for the controller's LLM proxy (api): the same
// redirect and private-address rules apply to requests made for a worker.
func HTTPClient() *http.Client { return httpClient() }

// Check sends one tiny request and reports whether the endpoint answered.
func Check(ctx context.Context, c Configuration) (model string, latency time.Duration, err error) {
	key, _ := Key(c)
	if key == "" {
		return "", 0, ErrNoKey
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := providerFor(c, key)
	if err != nil {
		return "", 0, err
	}
	started := time.Now()
	model, err = p.check(ctx, c, key)
	return model, time.Since(started), err
}

// endpointError reports what went wrong with a model request without
// echoing the endpoint's response body (which an arbitrary endpoint
// controls) into chat replies or logs.
func endpointError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		kind := ""
		if apiErr.Response != nil {
			kind = clipText(apiErr.Response.Header.Get("x-request-id"), 64)
		}
		return &EndpointError{Status: apiErr.StatusCode, RequestID: kind}
	}
	if errors.Is(err, ErrPrivateEndpoint) {
		return err
	}
	text := err.Error()
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return fmt.Errorf("model endpoint: %s", text)
}

// clipText bounds an endpoint-controlled string that reaches users or logs.
func clipText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// MaxResponseBytes bounds what the controller reads from the endpoint.
const MaxResponseBytes = 8 << 20

// ErrResponseTooLarge ends a reply that exceeds MaxResponseBytes instead of
// treating the cut-off as a complete answer.
var ErrResponseTooLarge = errors.New("model endpoint: the response exceeds 8 MiB")

type limitedBody struct {
	io.ReadCloser
	reader io.Reader
	seen   int64
}

func (l *limitedBody) Read(p []byte) (int, error) {
	n, err := l.reader.Read(p)
	l.seen += int64(n)
	if l.seen > MaxResponseBytes {
		return n, ErrResponseTooLarge
	}
	return n, err
}

type limitingTransport struct{ next http.RoundTripper }

func (t limitingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(r)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = &limitedBody{ReadCloser: resp.Body, reader: io.LimitReader(resp.Body, MaxResponseBytes+1)}
	return resp, nil
}

// readiness caches the last probe so GetAssistantConfiguration can report
// whether chat works without a model request on every page load.
type readiness struct {
	mu      sync.Mutex
	at      time.Time
	ok      bool
	message string
	// probed is the configuration the probe used; a change invalidates it.
	probed Configuration
	// probing is set while one probe runs; callers meanwhile get the last
	// state instead of queueing behind it.
	probing bool
}

const readinessTTL = 5 * time.Minute

// Ready reports whether chat can work: enabled, a key, and an endpoint that
// answered within readinessTTL. A failed probe is retried after one minute.
func (r *readiness) Ready(ctx context.Context, c Configuration) (bool, string) {
	if !c.Enabled {
		return false, "disabled"
	}
	key, _ := Key(c)
	if key == "" {
		return false, "no API key"
	}
	r.mu.Lock()
	ttl := readinessTTL
	if !r.ok {
		ttl = time.Minute
	}
	if r.probed == c && time.Since(r.at) < ttl {
		defer r.mu.Unlock()
		return r.ok, r.message
	}
	if r.probing {
		// One probe at a time; report what we know meanwhile.
		ok, message := r.ok, r.message
		if r.probed != c {
			ok, message = false, "checking the endpoint"
		}
		r.mu.Unlock()
		return ok, message
	}
	r.probing = true
	r.mu.Unlock()
	model, _, err := Check(ctx, c)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probing = false
	if err != nil && errors.Is(err, context.Canceled) {
		return false, "probe interrupted; retrying"
	}
	r.at, r.probed = time.Now(), c
	if err != nil {
		r.ok, r.message = false, clipText(err.Error(), 300)
		if errors.Is(err, context.DeadlineExceeded) {
			r.message = "the endpoint did not answer within 15 s"
		}
	} else {
		r.ok, r.message = true, clipText(model, 128)
	}
	return r.ok, r.message
}

// Record stores the outcome of an explicit check so a save or a Test
// connection click refreshes readiness immediately.
func (r *readiness) Record(c Configuration, model string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.at, r.probed = time.Now(), c
	if err != nil {
		r.ok, r.message = false, clipText(err.Error(), 300)
	} else {
		r.ok, r.message = true, clipText(model, 128)
	}
}
