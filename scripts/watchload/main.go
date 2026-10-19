// Command watchload opens many WatchTraffic and WatchFlowStats streams
// against a GoFi controller and reports what they cost it: upstream streams,
// Kubernetes API requests, CPU, memory, goroutines (from /metrics before and
// after), plus what the clients received and missed.
//
//	GOWORK=off go run ./scripts/watchload -addr http://localhost:9090 -traffic 50 -stats 20 -flows a,b,c -duration 30s
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
)

func main() {
	addr := flag.String("addr", "http://localhost:9090", "controller base URL")
	trafficClients := flag.Int("traffic", 20, "WatchTraffic streams to open (spread over -flows; 0 flows = each watches every flow)")
	statsClients := flag.Int("stats", 5, "WatchFlowStats streams to open (every flow)")
	flows := flag.String("flows", "", "comma-separated flow names for traffic streams (empty: watch all)")
	duration := flag.Duration("duration", 30*time.Second, "how long to hold the streams open")
	h2 := flag.Bool("h2", false, "use HTTP/2 prior knowledge (h2c) to the controller")
	flag.Parse()

	client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 0, MaxIdleConnsPerHost: 1000}}
	if *h2 {
		client = &http.Client{Transport: &http2.Transport{AllowHTTP: true, DialTLSContext: dialPlain}}
	}
	rpc := gofiv1connect.NewGoFiServiceClient(client, *addr)
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	before := scrape(*addr + "/metrics")
	names := strings.FieldsFunc(*flows, func(r rune) bool { return r == ',' || r == ' ' })
	if len(names) == 0 {
		list, err := rpc.ListFlows(ctx, connect.NewRequest(&gofiv1.ListFlowsRequest{}))
		if err != nil {
			fmt.Fprintln(os.Stderr, "list flows:", err)
			os.Exit(1)
		}
		fmt.Printf("controller lists %d flows (revision %d); traffic streams watch all of them\n", len(list.Msg.Flows), list.Msg.Revision)
	}

	var events, heartbeats, dropped, statsMessages, statsFlows, failures atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < *trafficClients; i++ {
		name := ""
		if len(names) > 0 {
			name = names[i%len(names)]
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream, err := rpc.WatchTraffic(ctx, connect.NewRequest(&gofiv1.WatchTrafficRequest{Name: name}))
			if err != nil {
				failures.Add(1)
				return
			}
			defer stream.Close()
			for stream.Receive() {
				msg := stream.Msg()
				if msg.Event != nil {
					events.Add(1)
				} else if msg.Heartbeat {
					heartbeats.Add(1)
				}
				dropped.Add(int64(msg.Dropped))
			}
			if err := stream.Err(); err != nil && ctx.Err() == nil {
				failures.Add(1)
			}
		}()
	}
	for i := 0; i < *statsClients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream, err := rpc.WatchFlowStats(ctx, connect.NewRequest(&gofiv1.WatchFlowStatsRequest{}))
			if err != nil {
				failures.Add(1)
				return
			}
			defer stream.Close()
			for stream.Receive() {
				statsMessages.Add(1)
				statsFlows.Add(int64(len(stream.Msg().Flows)))
			}
			if err := stream.Err(); err != nil && ctx.Err() == nil {
				failures.Add(1)
			}
		}()
	}
	// Sample the controller midway, once every stream is established.
	time.Sleep(min(*duration/2, 10*time.Second))
	during := scrape(*addr + "/metrics")
	wg.Wait()
	elapsed := time.Since(start)
	after := scrape(*addr + "/metrics")

	fmt.Printf("\n%d traffic + %d stats streams for %s\n", *trafficClients, *statsClients, elapsed.Round(time.Second))
	fmt.Printf("clients received: %d events (%.0f/s), %d heartbeats, %d dropped (controller queue full), %d stats messages (%d flow samples), %d stream failures\n",
		events.Load(), float64(events.Load())/elapsed.Seconds(), heartbeats.Load(), dropped.Load(), statsMessages.Load(), statsFlows.Load(), failures.Load())
	fmt.Println("\ncontroller (/metrics)             before      during       after")
	for _, key := range []string{
		"gofi_watch_streams{kind=\"traffic\"}", "gofi_watch_streams{kind=\"stats\"}",
		"gofi_hub_upstreams{kind=\"traffic\"}", "gofi_hub_upstreams{kind=\"stats\"}",
		"gofi_tap_subscriptions{layer=\"controller\"}", "gofi_tap_subscriptions{layer=\"runtime\"}",
		"gofi_tap_dropped_total{layer=\"controller\"}", "gofi_tap_dropped_total{layer=\"runtime\"}", "gofi_tap_dropped_total{layer=\"pipeline\"}",
		"gofi_relay_reconnects_total{kind=\"traffic\"}", "gofi_relay_reconnects_total{kind=\"stats\"}",
		"gofi_watch_streams_rejected_total{reason=\"global\"}", "gofi_watch_streams_rejected_total{reason=\"client\"}",
		"go_goroutines", "process_resident_memory_bytes", "process_cpu_seconds_total",
	} {
		fmt.Printf("%-40s %10s %10s %10s\n", key, cell(before[key]), cell(during[key]), cell(after[key]))
	}
	apiBefore, apiAfter := sumPrefix(before, "rest_client_requests_total"), sumPrefix(after, "rest_client_requests_total")
	if apiAfter > 0 || apiBefore > 0 {
		fmt.Printf("\nKubernetes API requests during the run: %.0f (%.2f/s)\n", apiAfter-apiBefore, (apiAfter-apiBefore)/elapsed.Seconds())
	}
	if cpuBefore, cpuAfter := before["process_cpu_seconds_total"], after["process_cpu_seconds_total"]; cpuAfter > 0 {
		fmt.Printf("controller CPU during the run: %.2f cores average\n", (cpuAfter-cpuBefore)/elapsed.Seconds())
	}
}

func dialPlain(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
}

func cell(value float64) string {
	if value == 0 {
		return "0"
	}
	if value >= 1e6 {
		return fmt.Sprintf("%.1fM", value/1e6)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// scrape reads a Prometheus text exposition into "name{labels}" -> value.
func scrape(url string) map[string]float64 {
	out := map[string]float64{}
	resp, err := http.Get(url)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		space := strings.LastIndexByte(line, ' ')
		if space < 0 {
			continue
		}
		value, err := strconv.ParseFloat(line[space+1:], 64)
		if err != nil {
			continue
		}
		out[line[:space]] = value
	}
	return out
}

func sumPrefix(metrics map[string]float64, prefix string) float64 {
	keys := make([]string, 0)
	for key := range metrics {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	total := 0.0
	for _, key := range keys {
		total += metrics[key]
	}
	return total
}
