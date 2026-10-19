// Package proc runs the gofi binary for end-to-end tests: build it once,
// start a controller with a given environment, wait for /readyz, sample its
// RSS, and stop it gracefully (SIGTERM) or abruptly (SIGKILL).
package proc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Build compiles the repository's main package to out (static, CGO off) and
// returns its path. GOFI_BINARY skips the build.
func Build(ctx context.Context, repoRoot, out string) (string, error) {
	if bin := os.Getenv("GOFI_BINARY"); bin != "" {
		return bin, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", out, ".")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, output)
	}
	return out, nil
}

// FreePort asks the kernel for a free TCP port.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Controller is a running gofi controller process.
type Controller struct {
	Binary  string
	Dir     string // data dir: flow file, history
	Port    int
	Env     map[string]string
	cmd     *exec.Cmd
	logFile *os.File
	logPath string
	mu      sync.Mutex
	exited  chan struct{}
	exitErr error
	lines   []string
}

// Options for Start.
type Options struct {
	Binary string
	// Dir holds listeners.yaml (GOFI_CONFIG_FILE) and history/.
	Dir  string
	Port int
	// Env adds or overrides environment variables (GOFI_DRIVER defaults to
	// inprocess, GOFI_HISTORY stays on).
	Env map[string]string
	// LogPath receives stdout/stderr (default <Dir>/gofi-<port>.log).
	LogPath string
}

// Start launches the controller and waits for /readyz.
func Start(ctx context.Context, opts Options) (*Controller, error) {
	if opts.Port == 0 {
		port, err := FreePort()
		if err != nil {
			return nil, err
		}
		opts.Port = port
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}
	c := &Controller{Binary: opts.Binary, Dir: opts.Dir, Port: opts.Port, Env: opts.Env, exited: make(chan struct{})}
	c.logPath = opts.LogPath
	if c.logPath == "" {
		c.logPath = filepath.Join(opts.Dir, fmt.Sprintf("gofi-%d.log", opts.Port))
	}
	return c, c.start(ctx)
}

// ConfigPath is the flow file.
func (c *Controller) ConfigPath() string { return filepath.Join(c.Dir, "listeners.yaml") }

// HistoryDir is the revision directory.
func (c *Controller) HistoryDir() string { return filepath.Join(c.Dir, "history") }

// URL is the API base URL.
func (c *Controller) URL() string { return fmt.Sprintf("http://127.0.0.1:%d", c.Port) }

// PID of the running process (0 when stopped).
func (c *Controller) PID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *Controller) start(ctx context.Context) error {
	logFile, err := os.OpenFile(c.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(c.Binary)
	cmd.Dir = c.Dir
	env := map[string]string{
		"GOFI_DRIVER":        "inprocess",
		"GOFI_DEPLOY_MODE":   "binary",
		"GOFI_CONFIG_FILE":   c.ConfigPath(),
		"GOFI_HISTORY_DIR":   c.HistoryDir(),
		"INGRESS_API_ADDR":   fmt.Sprintf("127.0.0.1:%d", c.Port),
		"TAP_RELAY_ADDR":     "127.0.0.1:0",
		"GOFI_DRAIN_TIMEOUT": "5s",
	}
	for k, v := range c.Env {
		env[k] = v
	}
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start %s: %w", c.Binary, err)
	}
	c.mu.Lock()
	c.cmd = cmd
	c.logFile = logFile
	c.exited = make(chan struct{})
	c.exitErr = nil
	c.mu.Unlock()
	go func() {
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 1<<20), 1<<22)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(logFile, line)
			c.mu.Lock()
			if len(c.lines) < 5000 {
				c.lines = append(c.lines, line)
			}
			c.mu.Unlock()
		}
	}()
	exited := c.exited
	go func() {
		err := cmd.Wait()
		pw.Close()
		c.mu.Lock()
		c.exitErr = err
		c.mu.Unlock()
		close(exited)
	}()
	return c.waitReady(ctx)
}

func (c *Controller) waitReady(ctx context.Context) error {
	deadline, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", c.Port), 300*time.Millisecond)
		if err == nil {
			fmt.Fprintf(conn, "GET /readyz HTTP/1.0\r\nHost: localhost\r\n\r\n")
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 64)
			n, _ := conn.Read(buf)
			conn.Close()
			if strings.Contains(string(buf[:n]), " 200 ") {
				return nil
			}
		}
		select {
		case <-c.exited:
			return fmt.Errorf("gofi exited before ready: %v\n%s", c.exitErr, c.Tail(30))
		case <-deadline.Done():
			return fmt.Errorf("gofi not ready on %d: %w\n%s", c.Port, deadline.Err(), c.Tail(30))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Tail returns the last n log lines.
func (c *Controller) Tail(n int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.lines) < n {
		n = len(c.lines)
	}
	return strings.Join(c.lines[len(c.lines)-n:], "\n")
}

// Logs returns every captured line.
func (c *Controller) Logs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

// LogPath is where output goes.
func (c *Controller) LogPath() string { return c.logPath }

// Signal sends sig to the process.
func (c *Controller) Signal(sig syscall.Signal) error {
	pid := c.PID()
	if pid == 0 {
		return fmt.Errorf("not running")
	}
	return syscall.Kill(pid, sig)
}

// Stop sends SIGTERM and waits up to timeout, then SIGKILLs. It returns the
// time the process took to exit.
func (c *Controller) Stop(timeout time.Duration) (time.Duration, error) {
	started := time.Now()
	if err := c.Signal(syscall.SIGTERM); err != nil {
		return 0, err
	}
	select {
	case <-c.exited:
	case <-time.After(timeout):
		_ = c.Signal(syscall.SIGKILL)
		<-c.exited
		return time.Since(started), fmt.Errorf("gofi did not stop within %s; killed", timeout)
	}
	c.closeLog()
	return time.Since(started), nil
}

// Kill SIGKILLs the process and waits for it to exit.
func (c *Controller) Kill() error {
	if err := c.Signal(syscall.SIGKILL); err != nil {
		return err
	}
	<-c.exited
	c.closeLog()
	return nil
}

func (c *Controller) closeLog() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.logFile != nil {
		c.logFile.Close()
		c.logFile = nil
	}
}

// Restart starts the same configuration again (after Stop or Kill).
func (c *Controller) Restart(ctx context.Context) error {
	return c.start(ctx)
}

// Exited reports whether the process has exited.
func (c *Controller) Exited() bool {
	select {
	case <-c.exited:
		return true
	default:
		return false
	}
}

// RSS returns the process's resident set size in bytes (Linux /proc).
func (c *Controller) RSS() (int64, error) {
	return RSSOf(c.PID())
}

// RSSOf reads VmRSS of pid.
func RSSOf(pid int) (int64, error) {
	if pid == 0 {
		return 0, fmt.Errorf("not running")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return 0, err
				}
				return kb * 1024, nil
			}
		}
	}
	return 0, fmt.Errorf("VmRSS not found")
}

// Sampler records RSS peaks while running.
type Sampler struct {
	mu   sync.Mutex
	peak int64
	last int64
	n    int
	stop chan struct{}
	done chan struct{}
}

// SampleRSS samples pid every interval until Stop.
func SampleRSS(pidFn func() int, interval time.Duration) *Sampler {
	s := &Sampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				rss, err := RSSOf(pidFn())
				if err != nil {
					continue
				}
				s.mu.Lock()
				s.last = rss
				s.n++
				if rss > s.peak {
					s.peak = rss
				}
				s.mu.Unlock()
			}
		}
	}()
	return s
}

// Stop ends sampling and returns peak and last RSS in bytes.
func (s *Sampler) Stop() (peak, last int64) {
	close(s.stop)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak, s.last
}

// MiB formats bytes.
func MiB(b int64) float64 { return float64(b) / (1 << 20) }
