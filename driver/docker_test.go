package driver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/wcrum/gofi/spec"
)

// fakeDockerAPI is a hand-written stand-in for the narrow dockerAPI
// interface, letting the Docker driver's logic be unit tested without a
// real Docker/Podman daemon.
type fakeDockerAPI struct {
	containers map[string]*fakeContainer // keyed by name

	createErr     error // returned once by ContainerCreate, then cleared
	createErrOnce bool

	createCalls []createCall
	removeCalls []string
	stopCalls   []string
}

type createCall struct {
	name   string
	config *container.Config
	host   *container.HostConfig
}

type fakeContainer struct {
	id     string
	name   string
	config *container.Config
	labels map[string]string
	nets   map[string]*network.EndpointSettings
	state  *container.State
}

func newFakeDockerAPI() *fakeDockerAPI {
	return &fakeDockerAPI{containers: make(map[string]*fakeContainer)}
}

func (f *fakeDockerAPI) ContainerCreate(_ context.Context, config *container.Config, hostConfig *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	f.createCalls = append(f.createCalls, createCall{name: containerName, config: config, host: hostConfig})

	if f.createErrOnce {
		f.createErrOnce = false
		return container.CreateResponse{}, f.createErr
	}

	if _, exists := f.containers[containerName]; exists {
		return container.CreateResponse{}, cerrdefs.ErrConflict
	}

	f.containers[containerName] = &fakeContainer{
		id:     containerName + "-id",
		name:   containerName,
		config: config,
		labels: config.Labels,
	}
	return container.CreateResponse{ID: containerName + "-id"}, nil
}

func (f *fakeDockerAPI) ContainerStart(_ context.Context, _ string, _ container.StartOptions) error {
	return nil
}

func (f *fakeDockerAPI) ContainerList(_ context.Context, options container.ListOptions) ([]container.Summary, error) {
	var wantLabel, wantValue string
	if options.Filters.Len() > 0 {
		for _, kv := range options.Filters.Get("label") {
			// kv looks like "gofi.managed-by=true"
			for i := 0; i < len(kv); i++ {
				if kv[i] == '=' {
					wantLabel, wantValue = kv[:i], kv[i+1:]
					break
				}
			}
		}
	}

	var out []container.Summary
	for _, c := range f.containers {
		if wantLabel != "" && c.labels[wantLabel] != wantValue {
			continue
		}
		out = append(out, container.Summary{ID: c.id, Names: []string{"/" + c.name}, Labels: c.labels})
	}
	return out, nil
}

func (f *fakeDockerAPI) ContainerInspect(_ context.Context, containerID string) (container.InspectResponse, error) {
	for _, c := range f.containers {
		if c.id == containerID || c.name == containerID {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{ID: c.id, Name: c.name, State: c.state},
				Config:            c.config,
				NetworkSettings:   &container.NetworkSettings{NetworkSettingsBase: container.NetworkSettingsBase{}, DefaultNetworkSettings: container.DefaultNetworkSettings{}, Networks: c.nets},
			}, nil
		}
	}
	return container.InspectResponse{}, cerrdefs.ErrNotFound
}

func (f *fakeDockerAPI) ContainerStop(_ context.Context, containerID string, _ container.StopOptions) error {
	f.stopCalls = append(f.stopCalls, containerID)
	if _, ok := f.containers[containerID]; !ok {
		return cerrdefs.ErrNotFound
	}
	return nil
}

func (f *fakeDockerAPI) ContainerRemove(_ context.Context, containerID string, _ container.RemoveOptions) error {
	f.removeCalls = append(f.removeCalls, containerID)
	if _, ok := f.containers[containerID]; !ok {
		return cerrdefs.ErrNotFound
	}
	delete(f.containers, containerID)
	return nil
}

func testDef(name string, port int) spec.ListenerDefinition {
	return spec.ListenerDefinition{
		Name:    name,
		Source:  spec.Source{Port: port, Network: "tcp"},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Sinks:   []spec.SinkCfg{{Type: "stdout"}},
	}
}

func TestSelfInspect(t *testing.T) {
	fake := newFakeDockerAPI()
	fake.containers["myhost"] = &fakeContainer{
		id:   "myhost-id",
		name: "myhost",
		config: &container.Config{
			Image: "ghcr.io/wcrum/gofi:latest",
		},
		nets: map[string]*network.EndpointSettings{
			"gofi_default": {},
		},
	}
	t.Setenv("HOSTNAME", "myhost")

	image, netName, err := selfInspect(context.Background(), fake)
	if err != nil {
		t.Fatalf("selfInspect: %v", err)
	}
	if image != "ghcr.io/wcrum/gofi:latest" {
		t.Errorf("image = %q, want ghcr.io/wcrum/gofi:latest", image)
	}
	if netName != "gofi_default" {
		t.Errorf("network = %q, want gofi_default", netName)
	}
}

func TestSelfInspectMultipleNetworksPicksOneNoError(t *testing.T) {
	fake := newFakeDockerAPI()
	fake.containers["myhost"] = &fakeContainer{
		id:   "myhost-id",
		name: "myhost",
		config: &container.Config{
			Image: "ghcr.io/wcrum/gofi:latest",
		},
		nets: map[string]*network.EndpointSettings{
			"net-a": {},
			"net-b": {},
		},
	}
	t.Setenv("HOSTNAME", "myhost")

	image, netName, err := selfInspect(context.Background(), fake)
	if err != nil {
		t.Fatalf("selfInspect: %v", err)
	}
	if image == "" || netName == "" {
		t.Errorf("expected non-empty image/network, got image=%q network=%q", image, netName)
	}
}

func TestSelfInspectIncompleteErrors(t *testing.T) {
	fake := newFakeDockerAPI()
	fake.containers["myhost"] = &fakeContainer{
		id:     "myhost-id",
		name:   "myhost",
		config: &container.Config{Image: ""},
	}
	t.Setenv("HOSTNAME", "myhost")

	_, _, err := selfInspect(context.Background(), fake)
	if err == nil {
		t.Fatal("expected error for incomplete self-inspect result")
	}
}

func TestNewDockerConfigOverridesSkipSelfInspect(t *testing.T) {
	fake := newFakeDockerAPI() // no containers registered; self-inspect would fail
	t.Setenv("HOSTNAME", "nonexistent")

	d, err := newDockerWithClient(fake, DockerConfig{Image: "my-image:tag", Network: "my-net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}
	if d.image != "my-image:tag" || d.network != "my-net" {
		t.Errorf("got image=%q network=%q", d.image, d.network)
	}
}

func TestDockerCreateSetsLabelsAndEnv(t *testing.T) {
	fake := newFakeDockerAPI()
	d, err := newDockerWithClient(fake, DockerConfig{Image: "img", Network: "net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}

	def := testDef("cot-ingest", 9000)
	if err := d.Create(context.Background(), def); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(fake.createCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(fake.createCalls))
	}
	call := fake.createCalls[0]
	wantName := "gofi-listener-cot-ingest"
	if call.name != wantName {
		t.Errorf("container name = %q, want %q", call.name, wantName)
	}
	if call.config.Labels[labelListener] != "cot-ingest" {
		t.Errorf("label %s = %q, want cot-ingest", labelListener, call.config.Labels[labelListener])
	}
	if call.config.Labels[labelManaged] != "true" {
		t.Errorf("label %s = %q, want true", labelManaged, call.config.Labels[labelManaged])
	}
	if string(call.host.NetworkMode) != "net" {
		t.Errorf("network mode = %q, want net", call.host.NetworkMode)
	}
	if call.host.RestartPolicy.Name != container.RestartPolicyDisabled {
		t.Errorf("restart policy = %q, want %q", call.host.RestartPolicy.Name, container.RestartPolicyDisabled)
	}

	wantPort, err := nat.NewPort("tcp", "9000")
	if err != nil {
		t.Fatalf("nat.NewPort: %v", err)
	}
	if _, ok := call.config.ExposedPorts[wantPort]; !ok {
		t.Errorf("ExposedPorts = %v, want to contain %v", call.config.ExposedPorts, wantPort)
	}
	bindings, ok := call.host.PortBindings[wantPort]
	if !ok || len(bindings) != 1 {
		t.Fatalf("PortBindings[%v] = %v, want exactly one binding", wantPort, call.host.PortBindings[wantPort])
	}
	if bindings[0].HostIP != "0.0.0.0" || bindings[0].HostPort != "9000" {
		t.Errorf("port binding = %+v, want HostIP=0.0.0.0 HostPort=9000", bindings[0])
	}

	var gotEnv string
	for _, e := range call.config.Env {
		if len(e) > len("LISTENER_SPEC=") && e[:len("LISTENER_SPEC=")] == "LISTENER_SPEC=" {
			gotEnv = e[len("LISTENER_SPEC="):]
		}
	}
	if gotEnv == "" {
		t.Fatal("LISTENER_SPEC env var not set")
	}
	var decoded spec.ListenerDefinition
	if err := json.Unmarshal([]byte(gotEnv), &decoded); err != nil {
		t.Fatalf("decode LISTENER_SPEC: %v", err)
	}
	if decoded.Name != "cot-ingest" || decoded.Source.Port != 9000 {
		t.Errorf("decoded spec mismatch: %+v", decoded)
	}
}

func TestDockerCreateNameConflictRetriesOnce(t *testing.T) {
	fake := newFakeDockerAPI()
	// Pre-populate a stale container with the same name gofi would generate.
	fake.containers["gofi-listener-cot-ingest"] = &fakeContainer{
		id:   "stale-id",
		name: "gofi-listener-cot-ingest",
	}

	d, err := newDockerWithClient(fake, DockerConfig{Image: "img", Network: "net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}

	def := testDef("cot-ingest", 9000)
	if err := d.Create(context.Background(), def); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(fake.createCalls) != 2 {
		t.Fatalf("expected 2 create calls (initial conflict + retry), got %d", len(fake.createCalls))
	}
	if len(fake.removeCalls) != 1 || fake.removeCalls[0] != "gofi-listener-cot-ingest" {
		t.Errorf("expected exactly one remove call for the stale container, got %v", fake.removeCalls)
	}
	if _, ok := fake.containers["gofi-listener-cot-ingest"]; !ok {
		t.Error("expected container to exist after retry")
	}
}

func TestDockerListDecodesSpecFromEnv(t *testing.T) {
	fake := newFakeDockerAPI()
	def := testDef("cot-ingest", 9000)
	raw, _ := json.Marshal(def)
	fake.containers["gofi-listener-cot-ingest"] = &fakeContainer{
		id:   "id-1",
		name: "gofi-listener-cot-ingest",
		config: &container.Config{
			Env:    []string{"LISTENER_SPEC=" + string(raw)},
			Labels: map[string]string{labelManaged: "true", labelListener: "cot-ingest"},
		},
		labels: map[string]string{labelManaged: "true", labelListener: "cot-ingest"},
	}
	// A non-managed container should be excluded.
	fake.containers["other"] = &fakeContainer{
		id:     "id-2",
		name:   "other",
		config: &container.Config{Env: []string{"FOO=bar"}},
		labels: map[string]string{},
	}

	d, err := newDockerWithClient(fake, DockerConfig{Image: "img", Network: "net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}

	got, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 listener, got %d: %+v", len(got), got)
	}
	if got[0].Name != "cot-ingest" || got[0].Source.Port != 9000 {
		t.Errorf("decoded spec mismatch: %+v", got[0])
	}
}

func TestDockerDeleteTreatsNotFoundAsSuccess(t *testing.T) {
	fake := newFakeDockerAPI() // empty: nothing to delete
	d, err := newDockerWithClient(fake, DockerConfig{Image: "img", Network: "net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}
	if err := d.Delete(context.Background(), "nonexistent"); err != nil {
		t.Errorf("Delete on nonexistent container should succeed, got %v", err)
	}
}

func TestDockerTapReturnsContainerAddr(t *testing.T) {
	fake := newFakeDockerAPI()
	d, err := newDockerWithClient(fake, DockerConfig{Image: "img", Network: "net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}
	target, ok, err := d.Tap(context.Background(), "cot-ingest")
	if err != nil || !ok {
		t.Fatalf("Tap: ok=%v err=%v", ok, err)
	}
	want := "gofi-listener-cot-ingest:9091"
	if target.Addr != want {
		t.Errorf("Tap addr = %q, want %q", target.Addr, want)
	}
	if target.Local != nil {
		t.Errorf("Tap.Local should be nil for the docker driver")
	}
}

func TestDockerKind(t *testing.T) {
	fake := newFakeDockerAPI()
	d, err := newDockerWithClient(fake, DockerConfig{Image: "img", Network: "net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}
	if d.Kind() != "docker" {
		t.Errorf("Kind() = %q, want docker", d.Kind())
	}
}

func TestDockerCreatePublishesUDPPort(t *testing.T) {
	fake := newFakeDockerAPI()
	d, err := newDockerWithClient(fake, DockerConfig{Image: "img", Network: "net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}

	def := spec.ListenerDefinition{
		Name:    "udp-ingest",
		Source:  spec.Source{Port: 5140, Network: "udp"},
		Framing: spec.Framing{Mode: "line"},
		Parser:  spec.ParserCfg{Format: "json"},
		Sinks:   []spec.SinkCfg{{Type: "stdout"}},
	}
	if err := d.Create(context.Background(), def); err != nil {
		t.Fatalf("Create: %v", err)
	}

	call := fake.createCalls[0]
	wantPort, err := nat.NewPort("udp", "5140")
	if err != nil {
		t.Fatalf("nat.NewPort: %v", err)
	}
	if _, ok := call.config.ExposedPorts[wantPort]; !ok {
		t.Errorf("ExposedPorts = %v, want to contain %v", call.config.ExposedPorts, wantPort)
	}
	if _, ok := call.host.PortBindings[wantPort]; !ok {
		t.Errorf("PortBindings = %v, want to contain %v", call.host.PortBindings, wantPort)
	}
}

func TestDockerRuntimeStatusReportsRunningState(t *testing.T) {
	fake := newFakeDockerAPI()
	d, err := newDockerWithClient(fake, DockerConfig{Image: "img", Network: "net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}

	def := testDef("cot-ingest", 9000)
	if err := d.Create(context.Background(), def); err != nil {
		t.Fatalf("Create: %v", err)
	}

	startedAt := "2026-07-03T12:00:00.000000000Z"
	fake.containers["gofi-listener-cot-ingest"].state = &container.State{
		Status:    container.StateRunning,
		StartedAt: startedAt,
	}

	info, ok, err := d.RuntimeStatus(context.Background(), "cot-ingest")
	if err != nil {
		t.Fatalf("RuntimeStatus: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for existing container")
	}
	if info.Kind != "docker" {
		t.Errorf("Kind = %q, want docker", info.Kind)
	}
	if info.Status != "running" {
		t.Errorf("Status = %q, want running", info.Status)
	}
	if info.ID == "" {
		t.Error("expected non-empty ID")
	}
	wantTime, _ := time.Parse(time.RFC3339Nano, startedAt)
	if !info.StartedAt.Equal(wantTime) {
		t.Errorf("StartedAt = %v, want %v", info.StartedAt, wantTime)
	}
}

func TestDockerRuntimeStatusNotFound(t *testing.T) {
	fake := newFakeDockerAPI()
	d, err := newDockerWithClient(fake, DockerConfig{Image: "img", Network: "net"})
	if err != nil {
		t.Fatalf("newDockerWithClient: %v", err)
	}

	_, ok, err := d.RuntimeStatus(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("RuntimeStatus: %v", err)
	}
	if ok {
		t.Error("expected ok=false for nonexistent container")
	}
}
