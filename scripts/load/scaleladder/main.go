// Command scaleladder measures how GoFi's control plane scales with the
// number of flows: it creates N flows through the API, times convergence to
// all-running, samples controller RSS/CPU and (when reachable) API server
// request rates, repeats for each rung of the ladder, and prints a Markdown
// table plus a JSON report under testenv/reports/.
//
//	go run ./scripts/load/scaleladder --server http://127.0.0.1:9090 --ladder 10,100,500
//
// Flags select the deployment shape only insofar as flows need Kubernetes
// Services (--nodeport) and whether the API server's /metrics is scraped
// (--apiserver-metrics URL with --kubeconfig for TLS/auth, or an envtest
// kubeconfig). See docs/testing.md ("Scale ladder").
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/wcrum/gofi/internal/testkit/flows"
	"github.com/wcrum/gofi/internal/testkit/gofiapi"
	"github.com/wcrum/gofi/internal/testkit/report"
)

type rung struct {
	Flows              int     `json:"flows"`
	CreateSeconds      float64 `json:"createSeconds"`
	ConvergeSeconds    float64 `json:"convergeSeconds"`
	ControllerRSSMiB   float64 `json:"controllerRssMiB"`
	ControllerCPUDelta float64 `json:"controllerCpuSecondsDelta"`
	APIServerRequests  float64 `json:"apiserverRequestsDelta"`
	APIServerQPS       float64 `json:"apiserverQps"`
	PodRSSMiBTotal     float64 `json:"podRssMiBTotal,omitempty"`
	Failed             int     `json:"failed"`
}

func main() {
	server := flag.String("server", envOr("GOFI_SERVER", "http://127.0.0.1:9090"), "controller base URL")
	ladder := flag.String("ladder", "10,100,500", "comma-separated flow counts")
	basePort := flag.Int("base-port", 40000, "first listen port")
	nodePort := flag.Bool("nodeport", false, "expose each flow through a NodePort Service (Kubernetes)")
	nodePortBase := flag.Int("nodeport-base", 30200, "first NodePort when --nodeport")
	keep := flag.Bool("keep", false, "leave the flows in place after the run")
	timeout := flag.Duration("timeout", 10*time.Minute, "time allowed per rung")
	apiserverMetrics := flag.String("apiserver-metrics", "", "Kubernetes API server /metrics URL (optional)")
	kubeconfig := flag.String("kubeconfig", os.Getenv("KUBECONFIG"), "kubeconfig for --apiserver-metrics")
	podMetrics := flag.String("pod-metrics", "", "optional kubectl-top style file (name rss) to sum per-pod RSS; see scripts/load/kwok-setup.sh")
	flag.Parse()

	api := gofiapi.New(*server)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	if err := api.WaitReady(ctx); err != nil {
		fail(err)
	}
	var apiserver *http.Client
	var apiserverURL string
	if *apiserverMetrics != "" {
		apiserverURL = *apiserverMetrics
		apiserver = http.DefaultClient
		if *kubeconfig != "" {
			cfg, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
			if err != nil {
				fail(err)
			}
			rt, err := rest.TransportFor(cfg)
			if err != nil {
				fail(err)
			}
			apiserver = &http.Client{Transport: rt, Timeout: 30 * time.Second}
		}
	}

	rep := report.Start("scale-ladder", "load")
	var rungs []rung
	created := []string{}
	cleanup := func() {
		if *keep {
			return
		}
		for _, name := range created {
			_ = api.Delete(context.Background(), name)
		}
	}
	defer cleanup()
	for _, field := range strings.Split(*ladder, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil {
			fail(err)
		}
		r := rung{Flows: n}
		before := scrape(ctx, api)
		apiBefore := scrapeURL(ctx, apiserver, apiserverURL)
		rungCtx, cancelRung := context.WithTimeout(ctx, *timeout)
		names := make([]string, 0, n)
		start := time.Now()
		for i := len(created); i < n; i++ {
			opts := []flows.Option{}
			if *nodePort {
				opts = append(opts, flows.Service("NodePort", *nodePortBase+i))
			}
			def := flows.Listen(fmt.Sprintf("scale-%04d", i), *basePort+i, opts...)
			if err := api.Upsert(rungCtx, def); err != nil {
				fmt.Fprintf(os.Stderr, "upsert %s: %v\n", def.Name, err)
				r.Failed++
				continue
			}
			created = append(created, def.Name)
		}
		names = append(names, created...)
		r.CreateSeconds = time.Since(start).Seconds()
		if err := api.WaitAllPhase(rungCtx, names, "running"); err != nil {
			fmt.Fprintf(os.Stderr, "rung %d: %v\n", n, err)
			r.Failed++
		}
		r.ConvergeSeconds = time.Since(start).Seconds()
		cancelRung()
		after := scrape(ctx, api)
		apiAfter := scrapeURL(ctx, apiserver, apiserverURL)
		r.ControllerRSSMiB = after.Value("process_resident_memory_bytes", nil) / (1 << 20)
		r.ControllerCPUDelta = after.Value("process_cpu_seconds_total", nil) - before.Value("process_cpu_seconds_total", nil)
		if apiserver != nil {
			r.APIServerRequests = apiAfter.Value("apiserver_request_total", nil) - apiBefore.Value("apiserver_request_total", nil)
			if r.ConvergeSeconds > 0 {
				r.APIServerQPS = r.APIServerRequests / r.ConvergeSeconds
			}
		}
		if *podMetrics != "" {
			r.PodRSSMiBTotal = sumPodRSS(*podMetrics)
		}
		rungs = append(rungs, r)
		fmt.Fprintf(os.Stderr, "rung %d: converged in %.1fs, RSS %.1f MiB\n", n, r.ConvergeSeconds, r.ControllerRSSMiB)
	}
	rep.Set("rungs", rungs)
	rep.Set("server", *server)
	rep.Finish(true, "", nil)
	if path, err := rep.Write(); err == nil {
		fmt.Fprintln(os.Stderr, "report:", path)
	}
	fmt.Println(table(rungs, apiserver != nil, *podMetrics != ""))
}

func table(rungs []rung, withAPIServer, withPods bool) string {
	var b strings.Builder
	b.WriteString("| flows | create (s) | converge (s) | controller RSS (MiB) | controller CPU (s) |")
	if withAPIServer {
		b.WriteString(" apiserver requests | apiserver QPS |")
	}
	if withPods {
		b.WriteString(" pods RSS total (MiB) |")
	}
	b.WriteString(" failed |\n| --- | --- | --- | --- | --- |")
	if withAPIServer {
		b.WriteString(" --- | --- |")
	}
	if withPods {
		b.WriteString(" --- |")
	}
	b.WriteString(" --- |\n")
	for _, r := range rungs {
		fmt.Fprintf(&b, "| %d | %.2f | %.2f | %.1f | %.2f |", r.Flows, r.CreateSeconds, r.ConvergeSeconds, r.ControllerRSSMiB, r.ControllerCPUDelta)
		if withAPIServer {
			fmt.Fprintf(&b, " %.0f | %.1f |", r.APIServerRequests, r.APIServerQPS)
		}
		if withPods {
			fmt.Fprintf(&b, " %.1f |", r.PodRSSMiBTotal)
		}
		fmt.Fprintf(&b, " %d |\n", r.Failed)
	}
	return b.String()
}

func scrape(ctx context.Context, api *gofiapi.Client) gofiapi.Samples {
	s, err := api.Metrics(ctx)
	if err != nil {
		return gofiapi.Samples{}
	}
	return s
}

func scrapeURL(ctx context.Context, client *http.Client, url string) gofiapi.Samples {
	if client == nil || url == "" {
		return gofiapi.Samples{}
	}
	s, err := gofiapi.ScrapeMetrics(ctx, client, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apiserver metrics:", err)
		return gofiapi.Samples{}
	}
	return s
}

// sumPodRSS reads "name bytes" lines (as scripts/load/pod-rss.sh writes).
func sumPodRSS(path string) float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	total := 0.0
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
			total += v
		}
	}
	return total / (1 << 20)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "scaleladder:", err)
	os.Exit(1)
}
