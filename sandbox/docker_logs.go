package sandbox

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// Follow streams a GoFi-managed worker container's stdout and stderr until
// the container stops or is removed, or ctx ends. Reading logs never keeps a
// container alive; scratch-worker cleanup removes it as usual.
func (d *Docker) Follow(ctx context.Context, worker string, tail int, emit func(RawLine) error) error {
	info, err := d.cli.ContainerInspect(ctx, worker)
	if errdefs.IsNotFound(err) {
		return ErrNoWorker
	}
	if err != nil {
		return err
	}
	if info.Config == nil || info.Config.Labels["gofi.managed-by"] != "gofi" || info.Config.Labels["gofi.role"] != "" {
		return fmt.Errorf("refusing to read logs of a container GoFi does not manage as a worker")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	body, err := d.cli.ContainerLogs(ctx, info.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: true, Tail: strconv.Itoa(tail)})
	if errdefs.IsNotFound(err) {
		return ErrNoWorker
	}
	if err != nil {
		return err
	}
	defer body.Close()

	var mu sync.Mutex
	serial := func(line RawLine) error { mu.Lock(); defer mu.Unlock(); return emit(line) }
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	var copying sync.WaitGroup
	copying.Add(1)
	go func() {
		defer copying.Done()
		_, err := stdcopy.StdCopy(outW, errW, body)
		outW.CloseWithError(err)
		errW.CloseWithError(err)
	}()
	results := make(chan error, 2)
	go func() { results <- scanLines(outR, "stdout", true, serial) }()
	go func() { results <- scanLines(errR, "stderr", true, serial) }()
	var first error
	for range 2 {
		if err := <-results; err != nil && first == nil {
			first = err
			// A failed emit (client gone): stop reading so every goroutine ends.
			cancel()
			body.Close()
			outR.Close()
			errR.Close()
		}
	}
	copying.Wait()
	if ctx.Err() != nil && first != nil {
		return nil
	}
	return first
}
