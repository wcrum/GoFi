// Command gofictl manages a running GoFi controller over ConnectRPC: apply
// Flow resources from YAML, list and inspect flows, start/stop/restart them,
// browse and roll back their history, and watch live throughput (top).
//
//	gofictl [--server URL] <command> [flags] [args]
//
// The server defaults to $GOFI_SERVER, then http://127.0.0.1:9090.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"

	"github.com/wcrum/gofi/internal/testkit/gofiapi"
)

const defaultServer = "http://127.0.0.1:9090"

func usage(w *os.File) {
	fmt.Fprintf(w, `gofictl controls a running GoFi controller.

Usage:
  gofictl [--server URL] <command> [flags] [args]

Commands:
  apply -f FILE [-f FILE...] [--dry-run] [-m MESSAGE]
        Create or update the Flow resources in FILE (a Flow, a v1 List, a
        FlowList, or multi-document YAML; "-" reads stdin).
  get flows [-o table|wide|yaml|json|name]
  get flow NAME [NAME...] [-o yaml|json]
  delete flow NAME [NAME...]
  start|stop|restart NAME [NAME...]
  history NAME [--revision N -o yaml]
  rollback NAME REVISION [-m MESSAGE]
  top [--interval 1s] [--no-tui] [--count N]
        Live phase and throughput of every flow (q or Ctrl-C exits).

Flags:
  --server URL   controller base URL (default $GOFI_SERVER or %s)
  -h, --help     this help

Documentation: docs/cli.md
`, defaultServer)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// globals are flags accepted anywhere on the command line.
type globals struct {
	server string
}

func (g *globals) register(fs *flag.FlagSet) {
	// A value parsed before the command word stays the default afterwards.
	server := g.server
	if server == "" {
		server = os.Getenv("GOFI_SERVER")
	}
	if server == "" {
		server = defaultServer
	}
	fs.StringVar(&g.server, "server", server, "controller base URL")
	fs.StringVar(&g.server, "s", server, "controller base URL (shorthand)")
}

func (g *globals) client() *gofiapi.Client {
	server := g.server
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	return gofiapi.New(server)
}

func run(args []string) error {
	var g globals
	root := flag.NewFlagSet("gofictl", flag.ContinueOnError)
	root.SetOutput(os.Stderr)
	root.Usage = func() { usage(os.Stderr) }
	g.register(root)
	rest, err := parseInterspersed(root, args, 1)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		usage(os.Stderr)
		return errors.New("a command is required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	command, args := rest[0], rest[1:]
	switch command {
	case "apply":
		return runApply(ctx, &g, args)
	case "get":
		return runGet(ctx, &g, args)
	case "delete", "del", "rm":
		return runDelete(ctx, &g, args)
	case "start", "stop", "restart":
		return runLifecycle(ctx, &g, command, args)
	case "history", "revisions":
		return runHistory(ctx, &g, args)
	case "rollback":
		return runRollback(ctx, &g, args)
	case "top":
		return runTop(ctx, &g, args)
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	}
	usage(os.Stderr)
	return fmt.Errorf("unknown command %q", command)
}

// newFlagSet builds a command's flag set with the global flags included, so
// --server may appear before or after the command word.
func newFlagSet(name string, g *globals) *flag.FlagSet {
	fs := flag.NewFlagSet("gofictl "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	g.register(fs)
	return fs
}

// parseInterspersed parses flags that appear anywhere among positional
// arguments (kubectl style). With stopAfter > 0, parsing stops once that many
// positionals were seen, leaving later flags for a subcommand.
func parseInterspersed(fs *flag.FlagSet, args []string, stopAfter int) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, args[0])
		args = args[1:]
		if stopAfter > 0 && len(positionals) >= stopAfter {
			return append(positionals, args...), nil
		}
	}
}

// describe turns an RPC error into a one-line message with its code.
func describe(err error) string {
	var cerr *connect.Error
	if errors.As(err, &cerr) {
		return fmt.Sprintf("%s (%s)", cerr.Message(), cerr.Code())
	}
	return err.Error()
}

func isNotFound(err error) bool { return connect.CodeOf(err) == connect.CodeNotFound }

// age formats a duration since t compactly (1h2m, 3d, 45s); "-" for zero.
func age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t).Round(time.Second)
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

// table renders rows with left-aligned columns separated by three spaces,
// like kubectl.
func table(header []string, rows [][]string) string {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	var b strings.Builder
	write := func(cells []string) {
		for i, cell := range cells {
			if i == len(cells)-1 {
				b.WriteString(cell)
			} else {
				b.WriteString(cell)
				b.WriteString(strings.Repeat(" ", widths[i]-len(cell)+3))
			}
		}
		b.WriteByte('\n')
	}
	write(header)
	for _, row := range rows {
		write(row)
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
