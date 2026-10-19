package assistant

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/wcrum/gofi/atomicfile"
	"sigs.k8s.io/yaml"
)

const (
	APIVersion = "gofi.wcrum.dev/v1alpha1"
	Kind       = "AssistantConfiguration"
)

// FileStore keeps the configuration in one YAML resource. The file holds the
// API key, so it is written with mode 0600; prefer GOFI_ASSISTANT_API_KEY_FILE
// where the host has a secrets mount.
type FileStore struct {
	Path string
	mu   sync.Mutex
}

type resource struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Spec       Configuration `json:"spec"`
}

func decode(b []byte) (Configuration, error) {
	var r resource
	if err := yaml.UnmarshalStrict(b, &r); err != nil {
		return Configuration{}, err
	}
	if r.APIVersion != APIVersion || r.Kind != Kind {
		return Configuration{}, fmt.Errorf("expected an %s resource", Kind)
	}
	return r.Spec, nil
}

func (f *FileStore) Backend() string { return "file:" + f.Path }

func (f *FileStore) Load(_ context.Context) (Configuration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, recovered, err := atomicfile.Decode(f.Path, decode)
	if os.IsNotExist(err) {
		return Configuration{}, nil
	}
	if err != nil {
		return Configuration{}, err
	}
	if recovered {
		slog.Warn("assistant configuration unreadable; recovered from backup", "path", f.Path)
	}
	return c, nil
}

func (f *FileStore) Save(_ context.Context, c Configuration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := yaml.Marshal(resource{APIVersion: APIVersion, Kind: Kind, Spec: c})
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(f.Path, data, 0o600)
}
