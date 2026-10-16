package sandbox

import (
	"connectrpc.com/connect"
	"context"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	gofiv1 "github.com/wcrum/gofi/gen/gofi/v1"
	"github.com/wcrum/gofi/gen/gofi/v1/gofiv1connect"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerIsolatedWorker(t *testing.T) {
	if os.Getenv("GOFI_DOCKER_INTEGRATION") != "1" {
		t.Skip("set GOFI_DOCKER_INTEGRATION=1 to exercise Docker worker isolation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	d, e := NewDocker(DockerConfig{Network: fmt.Sprintf("gofi-sandbox-test-%d", os.Getpid())})
	if e != nil {
		t.Fatal(e)
	}
	defer d.cli.NetworkRemove(context.Background(), d.cfg.Network)
	name := fmt.Sprintf("test-%d", os.Getpid())
	defer d.Stop(context.Background(), name)
	s := New(d, &FileStore{Path: filepath.Join(t.TempDir(), "sandbox.yaml")})
	source := "import json\nimport gofi\n@gofi.parser('telemetry')\ndef parse(raw): return json.loads(raw)\n"
	if e = s.Apply(ctx, Definition{Name: name, SourceCode: source}); e != nil {
		logs, _ := d.cli.ContainerLogs(ctx, "gofi-python-"+name, container.LogsOptions{ShowStdout: true, ShowStderr: true})
		if logs != nil {
			defer logs.Close()
			data := make([]byte, 4096)
			n, _ := logs.Read(data)
			t.Log(string(data[:n]))
		}
		t.Fatal(e)
	}
	bindings, _ := s.Bindings(ctx)
	b := bindings[name]
	rpc := gofiv1connect.NewWorkerServiceClient(http.DefaultClient, b.Endpoint)
	req := connect.NewRequest(&gofiv1.WorkerInvokeRequest{Name: "telemetry", Kind: "parser", Raw: []byte(`{"app":"gofi"}`)})
	req.Header().Set("Authorization", "Bearer "+b.Token)
	res, e := rpc.Invoke(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	if res.Msg.Fields.AsMap()["app"] != "gofi" {
		t.Fatal(res.Msg)
	}
	format := connect.NewRequest(&gofiv1.WorkerFormatSourceRequest{Source: "import gofi\n@gofi.parser('x')\ndef x(raw):return {'ok':1}\n"})
	format.Header().Set("Authorization", "Bearer "+b.Token)
	formatted, err := rpc.FormatSource(ctx, format)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(formatted.Msg.Source, `return {"ok": 1}`) {
		t.Fatalf("unexpected formatter output: %s", formatted.Msg.Source)
	}
	validate := connect.NewRequest(&gofiv1.WorkerValidateSourceRequest{Source: "import gofi\n@gofi.parser('x')\ndef x(raw):return missing_name\n"})
	validate.Header().Set("Authorization", "Bearer "+b.Token)
	validation, err := rpc.ValidateSource(ctx, validate)
	if err != nil {
		t.Fatal(err)
	}
	if validation.Msg.Valid || len(validation.Msg.Diagnostics) == 0 || validation.Msg.Diagnostics[0].Code != "F821" {
		t.Fatalf("validator failed to report undefined name: %v", validation.Msg)
	}
	if _, e = rpc.ListFunctions(ctx, connect.NewRequest(&gofiv1.WorkerListFunctionsRequest{})); connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated request accepted: %v", e)
	}
	info, e := d.cli.ContainerInspect(ctx, "gofi-python-"+name)
	if e != nil {
		t.Fatal(e)
	}
	if info.Config.User != "65532:65532" || !info.HostConfig.ReadonlyRootfs || info.HostConfig.Memory != 128*1024*1024 || len(info.HostConfig.CapDrop) != 1 {
		t.Fatal("worker resource/security bounds missing")
	}
	n, e := d.cli.NetworkInspect(ctx, d.cfg.Network, network.InspectOptions{})
	if e != nil || !n.Internal {
		t.Fatalf("worker network must deny external egress: %v", e)
	}
	if e = s.Delete(ctx, name); e != nil {
		t.Fatal(e)
	}
}
