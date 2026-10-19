package history

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/wcrum/gofi/atomicfile"
	"sigs.k8s.io/yaml"
)

// File keeps revisions as FlowRevision YAML files, one per revision:
// <Dir>/<flow>/<number>.yaml. Binary and Docker deployments put Dir next to
// the flow configuration file, so backing up or mounting that directory
// keeps the history with the flows.
type File struct {
	Dir string
}

func (f *File) Describe() string { return "file:" + f.Dir }

func (f *File) flowDir(flow string) (string, error) {
	if err := validFlowName(flow); err != nil {
		return "", err
	}
	return filepath.Join(f.Dir, flow), nil
}

func (f *File) path(flow string, number int64) (string, error) {
	dir, err := f.flowDir(flow)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, strconv.FormatInt(number, 10)+".yaml"), nil
}

// Append writes the revision to a temporary file and hard-links it into
// place, which fails if the number is taken: revisions are never overwritten.
func (f *File) Append(_ context.Context, rev Revision) error {
	target, err := f.path(rev.Flow, rev.Number)
	if err != nil {
		return err
	}
	item, err := toResource(rev)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(item)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rev-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Link(tmp.Name(), target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrExists
		}
		return err
	}
	// The link is only durable once the directory entry is, which matters
	// on flash-backed hosts that lose power.
	return atomicfile.SyncDir(dir)
}

func (f *File) read(path string) (Revision, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Revision{}, err
	}
	var item resource
	if err = yaml.Unmarshal(data, &item); err != nil {
		return Revision{}, fmt.Errorf("%s: %w", path, err)
	}
	return fromResource(item)
}

func (f *File) numbers(flow string) ([]int64, error) {
	dir, err := f.flowDir(flow)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".yaml")
		if !ok || entry.IsDir() {
			continue
		}
		if n, err := strconv.ParseInt(name, 10, 64); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (f *File) List(_ context.Context, flow string) ([]Revision, error) {
	numbers, err := f.numbers(flow)
	if err != nil {
		return nil, err
	}
	out := make([]Revision, 0, len(numbers))
	for _, n := range numbers {
		path, _ := f.path(flow, n)
		rev, err := f.read(path)
		if os.IsNotExist(err) {
			continue // pruned concurrently
		}
		if err == nil && (rev.Flow != flow || rev.Number != n) {
			err = fmt.Errorf("%s does not hold revision %d of %q", path, n, flow)
		}
		if err != nil {
			// One torn or corrupted revision file (power loss on flash) must
			// not hide the rest of the flow's history.
			slog.Warn("flow history: skipping unreadable revision", "flow", flow, "revision", n, "error", err)
			continue
		}
		out = append(out, rev)
	}
	return out, nil
}

func (f *File) Get(_ context.Context, flow string, number int64) (Revision, error) {
	path, err := f.path(flow, number)
	if err != nil {
		return Revision{}, err
	}
	rev, err := f.read(path)
	if os.IsNotExist(err) {
		return Revision{}, ErrNotFound
	}
	return rev, err
}

func (f *File) Delete(_ context.Context, flow string, numbers ...int64) error {
	for _, n := range numbers {
		path, err := f.path(flow, n)
		if err != nil {
			return err
		}
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if remaining, err := f.numbers(flow); err == nil && len(remaining) == 0 {
		dir, _ := f.flowDir(flow)
		_ = os.Remove(dir) // only succeeds when empty
	}
	return nil
}

func (f *File) Flows(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(f.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() && validFlowName(entry.Name()) == nil {
			out = append(out, entry.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
